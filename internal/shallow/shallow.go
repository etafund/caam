// Package shallow implements "shallow profile" mode for caam — a per-identity
// HOME directory where only the auth-bearing files are real and everything else
// is a symlink back to the user's real HOME.
//
// This enables concurrent multi-account multiplexing (N parallel sessions, each
// pinned to a different account) while preserving shared state — shell history,
// git config, ssh keys, harness conversation history, etc.
//
// Which entries are real, where they live, what env vars to set, and where the
// credential comes from is per-harness knowledge captured in a Layout value (see
// §5 of PLAN.md). Manager.Create and the CLI consult a provider-keyed registry of
// layouts and are harness-agnostic; adding a harness is a descriptor plus one
// registration line.
package shallow

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"syscall"
	"time"

	authfile "github.com/Dicklesworthstone/coding_agent_account_manager/internal/authfile"
	codexprovider "github.com/Dicklesworthstone/coding_agent_account_manager/internal/provider/codex"
)

// ProfileMetaFilename is the JSON sidecar that records when/where the
// shallow profile was created and which credential source was used.
const ProfileMetaFilename = ".caam-shallow.json"

// alwaysSkip lists top-level entries in the user's real HOME that should
// NEVER be symlinked. We never want to recursively expose the real HOME
// inside the shallow HOME, and we never want to capture leftover orch-homes
// or caam-managed state.
var alwaysSkip = map[string]bool{
	".":          true,
	"..":         true,
	"orch-homes": true, // user-style default location
}

// EnvVar is a key/value pair a layout SETS in the shallow-spawn environment.
type EnvVar struct{ Key, Value string }

// AuthFile maps one credential artifact from a source into the shallow HOME.
type AuthFile struct {
	// VaultName is this artifact's basename inside a `caam backup` vault profile dir.
	//   claude → ".credentials.json"   codex → "auth.json"   agy → "antigravity-oauth-token"
	// (--from-file does NOT use VaultName; it targets the Primary credential's DestRel.)
	VaultName string
	// DestRel is the slash-form destination relative to the shallow HOME.
	//   claude → ".claude/.credentials.json"   codex → ".codex/auth.json"
	//   agy    → ".gemini/antigravity-cli/antigravity-oauth-token"
	DestRel string
	// Primary marks THE credential. Exactly one AuthFile per Layout is Primary, and
	// it must be Required. --from-file copies to the Primary's DestRel. With no
	// source, the Primary is created as an empty 0600 placeholder (the harness
	// overwrites it on first login).
	Primary bool
	// Required: when copying from a vault profile dir, a missing source for a
	// Required file is an error. Non-required files are copied only when present.
	// (--from-file is only permitted when the layout has exactly ONE Required
	// credential — the Primary — so a single file can't silently leave other
	// required artifacts absent. validateLayout + provisionCredentials enforce this.)
	Required bool
}

// Layout fully describes one harness's shallow shape. It is a pure data
// descriptor (plus two function fields); the registry is immutable after init
// and every layout is checked by validateLayout (§5.3).
type Layout struct {
	// Provider is the canonical id (lowercase; matches the vault tool name and
	// provider.Provider.ID()): "claude", "codex", "agy".
	Provider string

	// DefaultBin is the harness binary for the "next step" hint text only
	// (e.g. "caam shallow-spawn <name> -- <DefaultBin>"). claude→"claude",
	// codex→"codex", agy→"agy". Keeps the CLI free of a hardcoded id→bin map.
	DefaultBin string

	// RealDirs are directories (slash-form, MAY be nested, e.g. ".gemini/antigravity-cli"
	// or ".config/claude-code") that must exist as REAL directories so real files can
	// live in them and the symlink farm won't replace them with links to the real HOME.
	// validateLayout requires every nested real file's parent dir to appear here.
	RealDirs []string

	// RealFiles are NON-credential files (slash-form, may be nested) reserved as
	// REAL files: lock files, policy files (codex config.toml). The engine does NOT
	// create them (it only uses them to build skip sets); the layout's
	// CreateManagedFiles MUST create every entry. Credential dests are NOT listed
	// here — they come from Credentials (single source of truth, avoids drift).
	RealFiles []string

	// Credentials describes how auth artifacts are provisioned (see AuthFile).
	// Exactly one entry must have Primary=true (and Required=true).
	Credentials []AuthFile

	// InnerSymlinkRoots are real directories (slash-form, may be nested) whose
	// *inner* entries are symlinked back to the matching real HOME dir, except this
	// layout's real files/dirs and (per InnerSkip / InnerSymlinkAllow) below.
	InnerSymlinkRoots []string

	// InnerSkip is a DENY-list per root: inner basenames to neither symlink nor
	// create. Use for a small, known, stable set (e.g. Claude's secondary auth.json).
	InnerSkip map[string][]string

	// InnerSymlinkAllow is an ALLOW-list per root: when set, ONLY these inner
	// basenames are symlinked; everything else under that root is left absent.
	// Use for roots that may contain VOLATILE runtime/socket/control artifacts whose
	// names you can't fully enumerate (e.g. Codex's .codex) — "symlink everything
	// except known-bad" is unsafe there. Mutually exclusive with InnerSkip per root
	// (validateLayout rejects a root that sets both).
	InnerSymlinkAllow map[string][]string

	// ProviderEnvSet returns the provider-specific env vars to SET, in print order
	// (codex → CODEX_HOME=<home>/.codex; claude/agy → nil — a redirected HOME plus
	// the cleared vars suffice). HOME and SHALLOW_PROFILE are set for every spawn;
	// the cleared vars (repointing + credential-override) are DELETED for every spawn
	// (see clearedEnvVars + Layout.SpawnEnv). Many harnesses need nothing here.
	// nil is allowed.
	ProviderEnvSet func(home string) []EnvVar

	// CreateManagedFiles, if non-nil, runs AFTER the symlink farm and credential
	// provisioning. It creates the layout's NON-credential real files and normalizes
	// policy: Claude → the .credentials.lock + .claude.json; Codex → config.toml +
	// EnsureFileCredentialStore. (This is the one non-declarative escape hatch; the
	// env and symlink behavior above stays declarative so the CLI/tests can reason
	// about it.)
	CreateManagedFiles func(m *Manager, home string, opts CreateOptions) error
}

// slashTop returns the first slash-separated component of p.
func slashTop(p string) string { return strings.SplitN(filepath.ToSlash(p), "/", 2)[0] }

// slashDir returns the slash-form parent directory of p.
func slashDir(p string) string { return path.Dir(filepath.ToSlash(p)) }

// slashBase returns the slash-form basename of p.
func slashBase(p string) string { return path.Base(filepath.ToSlash(p)) }

// realFileSet returns every slash-form file that must be REAL (never a symlink):
// RealFiles ∪ {each Credentials[i].DestRel} ∪ {ProfileMetaFilename}.
func (l Layout) realFileSet() map[string]bool {
	s := map[string]bool{ProfileMetaFilename: true}
	for _, f := range l.RealFiles {
		s[filepath.ToSlash(f)] = true
	}
	for _, c := range l.Credentials {
		s[filepath.ToSlash(c.DestRel)] = true
	}
	return s
}

// Primary returns the unique Credentials entry with Primary=true (guaranteed by
// validateLayout). Exported so the CLI can read the primary's VaultName/DestRel.
func (l Layout) Primary() AuthFile {
	for _, c := range l.Credentials {
		if c.Primary {
			return c
		}
	}
	return AuthFile{} // unreachable: validateLayout requires exactly one Primary
}

// ManagedFilePaths returns sorted absolute paths of this profile's real files
// that ACTUALLY EXIST under `home`. (For Claude/Codex all reserved files exist;
// for a multi-file harness like Antigravity, optional companions that weren't
// copied are correctly omitted.) Used for the create JSON `managed_files`.
func (l Layout) ManagedFilePaths(home string) []string {
	out := make([]string, 0, len(l.realFileSet()))
	for f := range l.realFileSet() {
		abs := filepath.Join(home, filepath.FromSlash(f))
		if _, err := os.Lstat(abs); err == nil {
			out = append(out, abs)
		}
	}
	sort.Strings(out)
	return out
}

// repointingEnvVars: per-harness vars that redirect a harness's auth/config dir
// OUTSIDE the shallow HOME. Deleted for every spawn so the ACTIVE harness can't be
// repointed by a stale parent value (esp. agy: reads GEMINI_HOME, no ProviderEnvSet).
var repointingEnvVars = []string{
	"CLAUDE_CONFIG_DIR", // Claude: claude-code config dir override
	"CODEX_HOME",        // Codex: overrides ~/.codex
	"CODEX_SQLITE_HOME", // Codex: overrides the sqlite-backed state location (codex sets it back)
	"GEMINI_HOME",       // Gemini/Antigravity: overrides ~/.gemini
}

// credentialOverrideEnvVars: env credentials that take PRECEDENCE over a harness's
// subscription/on-disk auth. Deleted by default so a shallow profile uses its
// vaulted identity. A user who really wants env-key auth injects it past caam:
//
//	caam shallow-spawn p -- env OPENAI_API_KEY=… codex
var credentialOverrideEnvVars = []string{
	"OPENAI_API_KEY", "CODEX_API_KEY", "CODEX_ACCESS_TOKEN", // Codex API-key / token auth
	"ANTHROPIC_API_KEY", "ANTHROPIC_AUTH_TOKEN", "CLAUDE_CODE_OAUTH_TOKEN", // Claude API-key / token
	"CLAUDE_CODE_USE_BEDROCK", "CLAUDE_CODE_USE_VERTEX", "CLAUDE_CODE_USE_FOUNDRY", // Claude cloud-provider auth selectors
}

// caamDiscoveryEnvVars: caam's OWN direct locator vars. Stripped so a cooperative
// tool/script in a shallow shell isn't *handed* a pointer to the vault/profiles via
// the environment. This is a courtesy, NOT a boundary: per the threat model a
// same-UID process can read the vault regardless (it's the same user).
var caamDiscoveryEnvVars = []string{"CAAM_HOME", "CAAM_SHALLOW_HOMES_DIR"}

func clearedEnvVars() []string {
	out := append([]string{}, repointingEnvVars...)
	out = append(out, credentialOverrideEnvVars...)
	return append(out, caamDiscoveryEnvVars...)
}

// shellQuote single-quotes s for POSIX sh (escaping embedded quotes via '\”),
// so an arbitrary --base path can't inject shell into --print-env output.
func shellQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

// SpawnEnv applies the shallow env transform to env IN PLACE: delete every cleared
// var, set HOME + SHALLOW_PROFILE, then the provider sets. Used by the exec path.
// (Delete-then-set is correct: codex's ProviderEnvSet re-adds CODEX_HOME.)
func (l Layout) SpawnEnv(home, name string, env map[string]string) {
	for _, k := range clearedEnvVars() {
		delete(env, k)
	}
	env["HOME"] = home
	env["SHALLOW_PROFILE"] = name
	if l.ProviderEnvSet != nil {
		for _, kv := range l.ProviderEnvSet(home) {
			env[kv.Key] = kv.Value
		}
	}
}

// SpawnEnvLines renders the spawn env as POSIX-shell statements for --print-env:
// `export KEY='<quoted>'` for each set var (HOME, SHALLOW_PROFILE, provider sets),
// then `unset KEY` for each cleared var not re-set. Values are shell-quoted, and
// `export` (not bare KEY=VALUE) guarantees child processes inherit them. Shares the
// cleared/set logic with SpawnEnv (single source of truth).
func (l Layout) SpawnEnvLines(home, name string) []string {
	set := map[string]bool{}
	var lines []string
	emit := func(k, v string) {
		lines = append(lines, "export "+k+"="+shellQuote(v))
		set[k] = true
	}
	emit("HOME", home)
	emit("SHALLOW_PROFILE", name)
	if l.ProviderEnvSet != nil {
		for _, kv := range l.ProviderEnvSet(home) {
			emit(kv.Key, kv.Value)
		}
	}
	for _, k := range clearedEnvVars() {
		if !set[k] {
			lines = append(lines, "unset "+k)
		}
	}
	return lines
}

// validateLayout is called by mustBuildLayouts for every built-in layout.
// A failure is a programmer error and panics at init, never at runtime.
func validateLayout(l Layout) error {
	if l.Provider == "" || l.Provider != strings.ToLower(strings.TrimSpace(l.Provider)) {
		return fmt.Errorf("provider id must be non-empty lowercase, got %q", l.Provider)
	}
	if err := checkBasename(l.Provider); err != nil { // used as a vault dir name → safe component
		return fmt.Errorf("provider id %q: %w", l.Provider, err)
	}
	if l.DefaultBin == "" {
		return fmt.Errorf("%s: DefaultBin required", l.Provider)
	}
	if l.ProviderEnvSet != nil { // env keys are rendered as shell in --print-env
		envKeyRe := regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
		seenKey := map[string]bool{}
		for _, kv := range l.ProviderEnvSet("/probe") {
			if !envKeyRe.MatchString(kv.Key) {
				return fmt.Errorf("%s: invalid env key %q", l.Provider, kv.Key)
			}
			if kv.Key == "HOME" || kv.Key == "SHALLOW_PROFILE" {
				return fmt.Errorf("%s: ProviderEnvSet must not set %q", l.Provider, kv.Key)
			}
			if seenKey[kv.Key] {
				return fmt.Errorf("%s: duplicate ProviderEnvSet key %q", l.Provider, kv.Key)
			}
			seenKey[kv.Key] = true
		}
	}

	realDir := map[string]bool{}
	for _, d := range l.RealDirs {
		if err := checkSlashRel(d); err != nil {
			return fmt.Errorf("%s RealDir %q: %w", l.Provider, d, err)
		}
		realDir[filepath.ToSlash(d)] = true
	}
	for _, f := range l.RealFiles {
		if err := checkSlashRel(f); err != nil {
			return fmt.Errorf("%s RealFile %q: %w", l.Provider, f, err)
		}
	}
	prim, dest, vname := 0, map[string]bool{}, map[string]bool{}
	for _, c := range l.Credentials {
		if err := checkSlashRel(c.DestRel); err != nil {
			return fmt.Errorf("%s DestRel %q: %w", l.Provider, c.DestRel, err)
		}
		if err := checkBasename(c.VaultName); err != nil { // joined into a vault path → must be a safe leaf
			return fmt.Errorf("%s VaultName %q: %w", l.Provider, c.VaultName, err)
		}
		if c.Primary {
			prim++
			if !c.Required {
				return fmt.Errorf("%s: primary credential must be Required", l.Provider)
			}
		}
		if dest[filepath.ToSlash(c.DestRel)] {
			return fmt.Errorf("%s: duplicate DestRel %q", l.Provider, c.DestRel)
		}
		if vname[c.VaultName] {
			return fmt.Errorf("%s: duplicate VaultName %q", l.Provider, c.VaultName)
		}
		dest[filepath.ToSlash(c.DestRel)] = true
		vname[c.VaultName] = true
	}
	if prim != 1 {
		return fmt.Errorf("%s: exactly one Primary credential required, got %d", l.Provider, prim)
	}
	// CRITICAL: every nested real file's parent dir must be a RealDir, else a copy
	// would write THROUGH a passthrough symlink into the real ~/.
	for f := range l.realFileSet() {
		if p := slashDir(f); p != "." && !realDir[p] {
			return fmt.Errorf("%s: real file %q parent %q is not a RealDir", l.Provider, f, p)
		}
	}
	for _, f := range l.RealFiles {
		if dest[filepath.ToSlash(f)] {
			return fmt.Errorf("%s: RealFile %q duplicates a credential DestRel", l.Provider, f)
		}
	}
	roots := map[string]bool{}
	for _, r := range l.InnerSymlinkRoots {
		if err := checkSlashRel(r); err != nil {
			return fmt.Errorf("%s InnerSymlinkRoot %q: %w", l.Provider, r, err)
		}
		r = filepath.ToSlash(r)
		if !realDir[r] {
			return fmt.Errorf("%s: InnerSymlinkRoot %q is not a RealDir", l.Provider, r)
		}
		roots[r] = true
	}
	checkNames := func(kind, root string, names []string) error {
		seen := map[string]bool{}
		for _, n := range names {
			if err := checkBasename(n); err != nil {
				return fmt.Errorf("%s %s[%q] name %q: %w", l.Provider, kind, root, n, err)
			}
			if seen[n] {
				return fmt.Errorf("%s %s[%q] duplicate name %q", l.Provider, kind, root, n)
			}
			seen[n] = true
		}
		return nil
	}
	for k, names := range l.InnerSkip {
		rk := filepath.ToSlash(k) // normalize the map key before the root lookup
		if !roots[rk] {
			return fmt.Errorf("%s: InnerSkip root %q is not an InnerSymlinkRoot", l.Provider, k)
		}
		if err := checkNames("InnerSkip", rk, names); err != nil {
			return err
		}
	}
	for k, names := range l.InnerSymlinkAllow {
		rk := filepath.ToSlash(k)
		if !roots[rk] {
			return fmt.Errorf("%s: InnerSymlinkAllow root %q is not an InnerSymlinkRoot", l.Provider, k)
		}
		if len(l.InnerSkip[k]) > 0 {
			return fmt.Errorf("%s: root %q sets both InnerSkip and InnerSymlinkAllow", l.Provider, k)
		}
		if err := checkNames("InnerSymlinkAllow", rk, names); err != nil {
			return err
		}
	}
	return nil
}

// checkSlashRel rejects anything that isn't a safe relative slash path.
func checkSlashRel(p string) error {
	if p == "" || p == "." || p == ".." {
		return fmt.Errorf("empty or dot path")
	}
	if strings.ContainsRune(p, '\\') {
		return fmt.Errorf("backslash not allowed (use slash-form)")
	}
	if path.IsAbs(p) {
		return fmt.Errorf("must be relative")
	}
	// Also reject OS-absolute / volume-prefixed paths: path.IsAbs("C:/x") is false,
	// but filepath.FromSlash("C:/x") is absolute on Windows and would escape the home.
	osp := filepath.FromSlash(p)
	if filepath.IsAbs(osp) || filepath.VolumeName(osp) != "" {
		return fmt.Errorf("must be relative (no volume/drive prefix)")
	}
	if strings.HasSuffix(p, "/") {
		return fmt.Errorf("trailing slash")
	}
	for _, seg := range strings.Split(filepath.ToSlash(p), "/") {
		if seg == "" || seg == "." || seg == ".." {
			return fmt.Errorf("bad path segment %q", seg)
		}
	}
	return nil
}

// checkBasename rejects anything that isn't a safe single path component (used for
// VaultName and InnerSkip/Allow names — all of which are joined into paths).
func checkBasename(n string) error {
	if n == "" || n == "." || n == ".." {
		return fmt.Errorf("empty or dot name")
	}
	if strings.ContainsAny(n, `/\`) {
		return fmt.Errorf("must be a single component (no separators)")
	}
	return nil
}

const (
	ProviderClaude = "claude"
	ProviderCodex  = "codex"
)

// providerOrder is the display order (Claude first, then Codex, then any others
// alphabetically). All "supported: ..." strings derive from SupportedProviders(),
// so adding a layout auto-updates every message — in a stable, ergonomic order.
var providerOrder = []string{ProviderClaude, ProviderCodex}

// layouts is built once and never mutated. Adding Antigravity = add AntigravityLayout()
// to this call (the single line referenced throughout the plan).
var layouts = mustBuildLayouts(
	ClaudeLayout(),
	CodexLayout(),
)

func mustBuildLayouts(ls ...Layout) map[string]Layout {
	m := make(map[string]Layout, len(ls))
	for _, l := range ls {
		if err := validateLayout(l); err != nil {
			panic("shallow: invalid layout: " + err.Error())
		}
		if _, dup := m[l.Provider]; dup {
			panic("shallow: duplicate layout " + l.Provider)
		}
		m[l.Provider] = l
	}
	return m
}

// SupportedProviders returns the registered provider ids in providerOrder (known
// first, then any extras alphabetically).
func SupportedProviders() []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(layouts))
	for _, p := range providerOrder {
		if _, ok := layouts[p]; ok {
			out = append(out, p)
			seen[p] = true
		}
	}
	extra := make([]string, 0)
	for p := range layouts {
		if !seen[p] {
			extra = append(extra, p)
		}
	}
	sort.Strings(extra)
	return append(out, extra...)
}

func supportedList() string { return strings.Join(SupportedProviders(), ", ") }

// NormalizeProvider resolves USER INPUT only (a --tool flag or a --from-vault
// tool): "" → claude (the ergonomic default harness, NOT a compat shim), else a
// registered provider or a clear error. Do NOT use this on stored metadata.
func NormalizeProvider(p string) (string, error) {
	p = strings.ToLower(strings.TrimSpace(p))
	if p == "" {
		return ProviderClaude, nil
	}
	if _, ok := layouts[p]; !ok {
		return "", fmt.Errorf("unsupported shallow provider %q (supported: %s)", p, supportedList())
	}
	return p, nil
}

// LayoutForProvider returns the layout for a known provider id (a normalized input
// or a stored metadata value). It is STRICT and EXACT: no empty→claude default and
// no case/space normalization — Create always writes canonical lowercase ids, so a
// stored id that isn't byte-exact-registered is malformed and must surface, not be
// silently "fixed". (NormalizeProvider does the lower/trim for user input.)
func LayoutForProvider(p string) (Layout, error) {
	l, ok := layouts[p]
	if !ok {
		if p == "" {
			return Layout{}, fmt.Errorf("shallow profile has no recorded provider (malformed metadata)")
		}
		return Layout{}, fmt.Errorf("unsupported shallow provider %q (supported: %s)", p, supportedList())
	}
	return l, nil
}

func ClaudeLayout() Layout {
	return Layout{
		Provider:          ProviderClaude,
		DefaultBin:        "claude",
		RealDirs:          []string{".claude"},
		RealFiles:         []string{".claude/.credentials.lock", ".claude.json"},
		Credentials:       []AuthFile{{VaultName: ".credentials.json", DestRel: ".claude/.credentials.json", Primary: true, Required: true}},
		InnerSymlinkRoots: []string{".claude"},
		// No ProviderEnvSet: HOME is redirected and the universal repoint-var strip
		// deletes any inherited CLAUDE_CONFIG_DIR — matching today's behavior. DO NOT
		// set CLAUDE_CONFIG_DIR here: official Claude Code docs indicate it relocates
		// the PRIMARY .credentials.json (not just a secondary config dir), so setting
		// it would make Claude ignore the vaulted <home>/.claude/.credentials.json
		// this layout writes.
		CreateManagedFiles: createClaudeManagedFiles,
	}
}

func CodexLayout() Layout {
	return Layout{
		Provider:          ProviderCodex,
		DefaultBin:        "codex",
		RealDirs:          []string{".codex"},
		RealFiles:         []string{".codex/config.toml"},
		Credentials:       []AuthFile{{VaultName: "auth.json", DestRel: ".codex/auth.json", Primary: true, Required: true}},
		InnerSymlinkRoots: []string{".codex"},
		// ALLOW-list, not deny-list: only known-safe SHARED state (dirs OR files) is
		// symlinked back; everything else under ~/.codex (incl. any daemon
		// runtime/control/socket dir, whatever its name) is left ABSENT in the shallow
		// HOME. Robust against daemon-auth bleed without knowing control-dir names.
		// `logs/` is deliberately EXCLUDED until audited — Codex login diagnostics can
		// write OAuth-callback / account details into logs.
		InnerSymlinkAllow: map[string][]string{".codex": {"sessions", "history.jsonl"}},
		ProviderEnvSet: func(home string) []EnvVar {
			cx := filepath.Join(home, ".codex")
			return []EnvVar{{Key: "CODEX_HOME", Value: cx}, {Key: "CODEX_SQLITE_HOME", Value: cx}}
		},
		CreateManagedFiles: createCodexManagedFiles,
	}
}

func createClaudeManagedFiles(m *Manager, home string, opts CreateOptions) error {
	// flock target — must be a real, per-identity file.
	lockPath := filepath.Join(home, ".claude", ".credentials.lock")
	if err := writeFileAtomic(lockPath, []byte(""), 0o600); err != nil {
		return fmt.Errorf("create credentials lock: %w", err)
	}
	// .claude.json: explicit source > real ~/.claude.json > {} skeleton.
	claudeJSONPath := filepath.Join(home, ".claude.json")
	switch {
	case opts.SourceClaudeJSON != "":
		if err := copyFileMode(opts.SourceClaudeJSON, claudeJSONPath, 0o600); err != nil {
			return fmt.Errorf("copy .claude.json: %w", err)
		}
	default:
		realClaudeJSON := filepath.Join(m.realHome, ".claude.json")
		switch _, err := os.Stat(realClaudeJSON); {
		case err == nil:
			if err := copyFileMode(realClaudeJSON, claudeJSONPath, 0o600); err != nil {
				return fmt.Errorf("seed .claude.json from real HOME: %w", err)
			}
		case errors.Is(err, os.ErrNotExist):
			if err := writeFileAtomic(claudeJSONPath, []byte("{}\n"), 0o600); err != nil {
				return fmt.Errorf("write skeleton .claude.json: %w", err)
			}
		default: // permission/IO error — don't silently fall back to a skeleton
			return fmt.Errorf("stat real .claude.json: %w", err)
		}
	}
	return nil
}

func createCodexManagedFiles(m *Manager, home string, opts CreateOptions) error {
	codexDir := filepath.Join(home, ".codex")
	// Write a FRESH minimal config.toml (just the file credential store) — do NOT
	// copy the real ~/.codex/config.toml. A copied config can carry PATH-bearing keys
	// that escape isolation: `log_dir`/`sqlite_home` pointing back at the real
	// ~/.codex, or a custom `[model_providers.*] env_key` that authenticates via an
	// inherited env var instead of the shallow auth.json. EnsureFileCredentialStore
	// with no existing config writes exactly `cli_auth_credentials_store = "file"`.
	if err := codexprovider.EnsureFileCredentialStore(codexDir); err != nil {
		return fmt.Errorf("configure codex credential store: %w", err)
	}
	return nil
}

// Meta is the JSON sidecar persisted at ~/orch-homes/<name>/.caam-shallow.json.
type Meta struct {
	Name           string    `json:"name"`
	Provider       string    `json:"provider"` // always set on write
	CreatedAt      time.Time `json:"created_at"`
	CredentialFrom string    `json:"credential_from,omitempty"`
	RealHome       string    `json:"real_home"`
	Version        int       `json:"version"`
}

// Manager handles creation, listing, deletion, and inspection of shallow
// profiles. It is safe to construct cheaply and use across calls.
type Manager struct {
	baseDir   string // e.g. ~/orch-homes
	realHome  string // e.g. /home/user
	canonBase string // baseDir with existing symlinked ancestors resolved
	vaultRoot string // CAAM vault root to shadow from shallow HOMEs (never "")
}

// NewManager creates a Manager rooted at baseDir, using realHome as the
// symlink target. If baseDir is empty, DefaultBaseDir() is used.
func NewManager(baseDir, realHome string) (*Manager, error) {
	if strings.TrimSpace(realHome) == "" {
		h, err := os.UserHomeDir()
		if err != nil {
			return nil, fmt.Errorf("resolve user home: %w", err)
		}
		realHome = h
	}
	if strings.TrimSpace(baseDir) == "" {
		baseDir = DefaultBaseDir(realHome)
	}
	abs, err := filepath.Abs(baseDir)
	if err != nil {
		return nil, fmt.Errorf("resolve base dir: %w", err)
	}
	rabs, err := filepath.Abs(realHome)
	if err != nil {
		return nil, fmt.Errorf("resolve real home: %w", err)
	}
	if abs == rabs {
		return nil, fmt.Errorf("shallow base dir cannot be the user's real home")
	}
	canonBase := abs
	if cb, err := resolveExistingSymlinks(abs); err == nil {
		rhome := rabs
		if rh, err := resolveExistingSymlinks(rabs); err == nil {
			rhome = rh
		}
		if cb == rhome {
			return nil, fmt.Errorf("shallow base dir resolves (via symlink) to the real home: %s", cb)
		}
		canonBase = cb
	}
	return &Manager{baseDir: abs, realHome: rabs, canonBase: canonBase, vaultRoot: authfile.DefaultVaultPath()}, nil
}

// SetVaultRoot overrides the vault root that the manager shadows from shallow HOMEs.
func (m *Manager) SetVaultRoot(p string) {
	if p != "" {
		m.vaultRoot = p
	}
}

// DefaultBaseDir returns the default location for shallow profiles.
// Order of precedence:
//  1. $CAAM_SHALLOW_HOMES_DIR (full override)
//  2. $CAAM_HOME/shallow-homes (if CAAM_HOME is set)
//  3. <realHome>/orch-homes (matches the "homemade" convention from issue #16)
func DefaultBaseDir(realHome string) string {
	if v := strings.TrimSpace(os.Getenv("CAAM_SHALLOW_HOMES_DIR")); v != "" {
		return v
	}
	if v := strings.TrimSpace(os.Getenv("CAAM_HOME")); v != "" {
		return filepath.Join(v, "shallow-homes")
	}
	return filepath.Join(realHome, "orch-homes")
}

// BaseDir returns the directory that holds all shallow profiles for this manager.
func (m *Manager) BaseDir() string { return m.baseDir }

// RealHome returns the symlink target HOME used for passthroughs.
func (m *Manager) RealHome() string { return m.realHome }

// HomeFor returns the absolute path of the shallow HOME for the named profile.
func (m *Manager) HomeFor(name string) (string, error) {
	clean, err := validateProfileName(name)
	if err != nil {
		return "", err
	}
	return filepath.Join(m.baseDir, clean), nil
}

// CreateOptions controls how a shallow profile is provisioned.
type CreateOptions struct {
	Provider string // "" → claude

	// CredentialSource: a single file (from --from-file) copied to the layout's
	// Primary credential dest.
	CredentialSource string
	// CredentialSourceDir: a vault profile directory (from --from-vault); each
	// layout AuthFile present there is copied to its DestRel.
	CredentialSourceDir string

	// SourceClaudeJSON: claude-only; copied to <home>/.claude.json. Error if set
	// with a non-claude provider.
	SourceClaudeJSON string

	// CredentialFromLabel is recorded in the metadata file and surfaced by
	// `shallow-profile list`. It is purely descriptive (e.g. "vault:claude/alice").
	CredentialFromLabel string

	// Force overwrites an existing shallow profile of the same name.
	Force bool
}

// Create provisions a new shallow profile.
func (m *Manager) Create(name string, opts CreateOptions) (retHome string, retErr error) {
	provider, err := NormalizeProvider(opts.Provider) // user-input default: "" → claude
	if err != nil {
		return "", err
	}
	opts.Provider = provider // normalize before handing opts to layout hooks
	layout, err := LayoutForProvider(provider)
	if err != nil {
		return "", err
	}

	if opts.SourceClaudeJSON != "" && provider != ProviderClaude {
		return "", fmt.Errorf("--from-claude-json is only valid for provider claude (got %q)", provider)
	}
	if opts.CredentialSourceDir != "" && opts.CredentialSource != "" {
		// Engine-level guard (the CLI also makes --from-vault/--from-file mutually
		// exclusive). Manager.Create is a package API; bad callers must fail loudly.
		return "", fmt.Errorf("CredentialSourceDir and CredentialSource are mutually exclusive")
	}

	home, err := m.HomeFor(name)
	if err != nil {
		return "", err
	}

	// Existing-dir + --force handling: reject a symlinked profile path (identity
	// hijack), and the --force RemoveAll must only delete a genuine shallow profile,
	// never an arbitrary path reached via a symlinked base.
	if st, err := os.Lstat(home); err == nil {
		if st.Mode()&os.ModeSymlink != 0 {
			return "", fmt.Errorf("shallow profile %q is a symlink; refusing (potential identity hijack)", name)
		}
		if !opts.Force {
			return "", fmt.Errorf("shallow profile %q already exists at %s (use --force to overwrite)", name, home)
		}
		if err := m.assertIsShallowProfile(home); err != nil {
			return "", err
		}
		if err := os.RemoveAll(home); err != nil {
			return "", fmt.Errorf("remove existing profile: %w", err)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", fmt.Errorf("stat profile dir: %w", err)
	}

	if err := os.MkdirAll(home, 0o700); err != nil {
		return "", fmt.Errorf("create profile dir: %w", err)
	}

	// Transactional hygiene: once we own a freshly-created (or just --force-cleared)
	// `home`, remove the half-built profile on ANY later error, so a failed create
	// never leaves a meta-less directory that blocks re-create and that shallow-spawn
	// can't classify. The "exists && !force" early-return above happens BEFORE this
	// defer is armed, so we never delete a profile we didn't just create.
	defer func() {
		if retErr != nil {
			_ = os.RemoveAll(home)
		}
	}()

	for _, d := range layout.RealDirs { // MkdirAll handles nested parents
		if err := os.MkdirAll(filepath.Join(home, filepath.FromSlash(d)), 0o700); err != nil {
			return "", fmt.Errorf("create real dir %s: %w", d, err)
		}
	}

	if err := m.populateSymlinks(home, layout); err != nil {
		return "", fmt.Errorf("populate symlinks: %w", err)
	}
	for _, root := range layout.InnerSymlinkRoots {
		if err := m.populateInnerSymlinks(home, layout, root); err != nil {
			return "", fmt.Errorf("populate %s symlinks: %w", root, err)
		}
	}

	if err := m.provisionCredentials(home, layout, opts); err != nil {
		return "", err
	}

	if layout.CreateManagedFiles != nil {
		if err := layout.CreateManagedFiles(m, home, opts); err != nil {
			return "", err
		}
	}

	// Post-create invariant: every real file the layout declares (RealFiles ∪ the
	// credential dests, i.e. realFileSet minus the meta sidecar) must now exist as a
	// REAL regular file with no symlinked ancestor inside the profile.
	optionalDest := map[string]bool{}
	for _, c := range layout.Credentials {
		if !c.Primary && !c.Required {
			optionalDest[filepath.ToSlash(c.DestRel)] = true
		}
	}
	for f := range layout.realFileSet() {
		if f == ProfileMetaFilename {
			continue // written just below
		}
		abs := filepath.Join(home, filepath.FromSlash(f))
		if err := assertNoSymlinkAncestor(home, abs); err != nil {
			return "", fmt.Errorf("post-create %s: %w", f, err)
		}
		st, err := os.Lstat(abs)
		if errors.Is(err, os.ErrNotExist) {
			if optionalDest[f] {
				continue // optional + absent = fine
			}
			return "", fmt.Errorf("post-create: required managed file %s was not created", f)
		}
		if err != nil || st.Mode()&os.ModeSymlink != 0 || !st.Mode().IsRegular() {
			return "", fmt.Errorf("post-create: managed file %s is not a regular file", f)
		}
	}

	meta := Meta{
		Name:           name,
		Provider:       provider,
		CreatedAt:      time.Now().UTC(),
		CredentialFrom: opts.CredentialFromLabel,
		RealHome:       m.realHome,
		Version:        2,
	}
	if err := writeMeta(home, &meta); err != nil {
		return "", fmt.Errorf("write metadata: %w", err)
	}
	return home, nil
}

// provisionCredentials copies auth artifacts into the shallow HOME per the layout.
// Priority: CredentialSourceDir (vault, multi-file) > CredentialSource (single file → Primary)
// > none (empty Primary placeholder).
func (m *Manager) provisionCredentials(home string, layout Layout, opts CreateOptions) error {
	prim := layout.Primary()
	primDst := filepath.Join(home, filepath.FromSlash(prim.DestRel))

	switch {
	case opts.CredentialSourceDir != "":
		for _, c := range layout.Credentials {
			src := filepath.Join(opts.CredentialSourceDir, c.VaultName)
			dst := filepath.Join(home, filepath.FromSlash(c.DestRel))
			if _, err := os.Stat(src); err != nil {
				if errors.Is(err, os.ErrNotExist) {
					if c.Primary || c.Required {
						// CredentialFromLabel is e.g. "vault:codex/bob"; don't prefix "vault profile" again.
						return fmt.Errorf("%s missing %s: %w", opts.CredentialFromLabel, c.VaultName, err)
					}
					continue // optional + absent → skip
				}
				return fmt.Errorf("stat %s: %w", src, err)
			}
			if err := copyFileMode(src, dst, 0o600); err != nil {
				return fmt.Errorf("copy %s: %w", c.VaultName, err)
			}
		}
	case opts.CredentialSource != "":
		// A single --from-file maps to the Primary only. Forbid it when the layout
		// has MORE THAN ONE Required credential — otherwise a single file would
		// silently leave other required artifacts absent.
		required := 0
		for _, c := range layout.Credentials {
			if c.Required {
				required++
			}
		}
		if required > 1 {
			return fmt.Errorf("--from-file is not supported for provider %q (it has multiple required credentials); use --from-vault", layout.Provider)
		}
		if err := copyFileMode(opts.CredentialSource, primDst, 0o600); err != nil {
			return fmt.Errorf("copy credentials: %w", err)
		}
	default:
		// No source: write only an empty Primary placeholder. Reject this for a layout
		// with MORE THAN ONE required credential — an empty-create can't satisfy
		// multiple required artifacts.
		required := 0
		for _, c := range layout.Credentials {
			if c.Required {
				required++
			}
		}
		if required > 1 {
			return fmt.Errorf("provider %q requires multiple credentials; create with --from-vault (empty create unsupported)", layout.Provider)
		}
		if err := writeFileAtomic(primDst, []byte(""), 0o600); err != nil {
			return fmt.Errorf("write empty credentials: %w", err)
		}
	}
	return nil
}

// populateSymlinks reads top-level entries in realHome and creates a symlink in
// home for each, skipping names that collide with the layout's real files/dirs,
// alwaysSkip, and the CAAM-owned roots (vault, base, $CAAM_HOME).
func (m *Manager) populateSymlinks(home string, layout Layout) error {
	entries, err := os.ReadDir(m.realHome)
	if err != nil {
		return fmt.Errorf("read real home %s: %w", m.realHome, err)
	}

	skip := map[string]bool{}
	for f := range layout.realFileSet() {
		skip[slashTop(f)] = true // top component of each real file
	}
	for _, d := range layout.RealDirs {
		skip[slashTop(d)] = true
	}
	for k := range alwaysSkip {
		skip[k] = true
	}
	// Shadow CAAM-owned roots (the shallow base, $CAAM_HOME, and the vault root)
	// when they nest under realHome — so no shallow HOME can reach the vault or
	// another profile. caamShadowTops() subsumes the old base-dir nesting guard.
	for t := range m.caamShadowTops() {
		skip[t] = true
	}

	for _, e := range entries {
		name := e.Name()
		if skip[name] {
			continue
		}
		src := filepath.Join(m.realHome, name)
		dst := filepath.Join(home, name)
		// If the source has vanished mid-iteration, skip silently.
		if _, err := os.Lstat(src); err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			return fmt.Errorf("stat source %s: %w", src, err)
		}
		if err := atomicSymlink(src, dst); err != nil {
			return fmt.Errorf("symlink %s -> %s: %w", dst, src, err)
		}
	}
	return nil
}

// populateInnerSymlinks symlinks the children of ~/<root> into <home>/<root>.
// Two modes per root:
//   - ALLOW-list (layout.InnerSymlinkAllow[root] set): symlink ONLY those basenames.
//   - DENY-list (default): symlink everything EXCEPT this layout's real files/dirs
//     under <root> and InnerSkip[root].
//
// In both modes, real files/dirs of the layout are never symlinked.
// Smart fallback: a missing OR non-directory ~/<root> is a no-op.
// `root` is slash-form and may be nested (e.g. ".gemini/antigravity-cli").
func (m *Manager) populateInnerSymlinks(home string, layout Layout, root string) error {
	root = filepath.ToSlash(root)
	srcDir := filepath.Join(m.realHome, filepath.FromSlash(root))
	dstDir := filepath.Join(home, filepath.FromSlash(root))
	st, err := os.Stat(srcDir)
	if err != nil {
		// Missing, OR an ancestor is a file (ENOTDIR) for a nested root like
		// ".gemini/antigravity-cli" when ~/.gemini is a file → nothing to mirror.
		if errors.Is(err, os.ErrNotExist) || errors.Is(err, syscall.ENOTDIR) {
			return nil
		}
		return fmt.Errorf("stat %s: %w", srcDir, err)
	}
	if !st.IsDir() {
		return nil // real ~/<root> is a FILE, not a dir → nothing to mirror
	}

	// Real files/dirs of this layout under <root> are never symlinked (their
	// presence as real entries is the whole point).
	reserved := map[string]bool{}
	for f := range layout.realFileSet() {
		if slashDir(f) == root {
			reserved[slashBase(f)] = true
		}
	}
	for _, d := range layout.RealDirs {
		d = filepath.ToSlash(d)
		if slashDir(d) == root {
			reserved[slashBase(d)] = true
		}
	}

	entries, err := os.ReadDir(srcDir)
	if err != nil {
		return fmt.Errorf("read %s: %w", srcDir, err)
	}

	// link mirrors the top-level farm's vanished-source guard: if the entry
	// disappeared between ReadDir and here, skip it instead of creating a dangling
	// symlink.
	link := func(n string) error {
		src := filepath.Join(srcDir, n)
		dst := filepath.Join(dstDir, n)
		if _, err := os.Lstat(src); err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return nil
			}
			return fmt.Errorf("stat source %s: %w", src, err)
		}
		if err := atomicSymlink(src, dst); err != nil {
			return fmt.Errorf("symlink %s -> %s: %w", dst, src, err)
		}
		return nil
	}

	if allow, ok := layout.InnerSymlinkAllow[root]; ok {
		// ALLOW-list mode: link only the allowed, present, non-reserved entries.
		allowed := map[string]bool{}
		for _, n := range allow {
			allowed[n] = true
		}
		for _, e := range entries {
			n := e.Name()
			if !allowed[n] || reserved[n] {
				continue
			}
			if err := link(n); err != nil {
				return err
			}
		}
		return nil
	}

	// DENY-list mode (default).
	skip := map[string]bool{}
	for k := range reserved {
		skip[k] = true
	}
	for _, n := range layout.InnerSkip[root] {
		skip[n] = true
	}
	for _, e := range entries {
		n := e.Name()
		if skip[n] {
			continue
		}
		if err := link(n); err != nil {
			return err
		}
	}
	return nil
}

// caamShadowTops returns the top-level realHome components that must NOT be
// symlinked into a shallow HOME because they (lexically or via symlink) contain
// CAAM-owned data: the shallow base, $CAAM_HOME, and the vault root.
func (m *Manager) caamShadowTops() map[string]bool {
	tops := map[string]bool{}
	// Match nesting against BOTH the lexical realHome and its canonical form (a
	// symlinked realHome component could otherwise hide the vault's nesting).
	realHomes := []string{m.realHome}
	if crh, err := resolveExistingSymlinks(m.realHome); err == nil && crh != m.realHome {
		realHomes = append(realHomes, crh)
	}
	addOne := func(p string) {
		if p == "" {
			return
		}
		abs, err := filepath.Abs(p)
		if err != nil {
			return
		}
		for _, rh := range realHomes {
			if rel, err := filepath.Rel(rh, abs); err == nil &&
				rel != ".." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
				if t := slashTop(rel); t != "" && t != "." {
					tops[t] = true
				}
			}
		}
	}
	add := func(p string) { // match both the lexical path and its symlink-resolved form
		addOne(p)
		if p != "" {
			if c, err := resolveExistingSymlinks(p); err == nil {
				addOne(c)
			}
		}
	}
	add(m.baseDir)
	add(m.canonBase) // the shallow base (lexical + canonical)
	add(os.Getenv("CAAM_HOME"))
	add(m.vaultRoot) // configured vault root; defaulted in NewManager (never "")
	return tops
}

// resolveExistingSymlinks canonicalizes the longest EXISTING ancestor of p
// (EvalSymlinks errors on a non-existent path) then rejoins the not-yet-existing
// tail — so a base that doesn't exist yet still resolves its existing symlinked
// ancestors. p must be absolute.
func resolveExistingSymlinks(p string) (string, error) {
	p = filepath.Clean(p)
	var tail []string
	cur := p
	for {
		if resolved, err := filepath.EvalSymlinks(cur); err == nil {
			for i := len(tail) - 1; i >= 0; i-- {
				resolved = filepath.Join(resolved, tail[i])
			}
			return resolved, nil
		} else if !errors.Is(err, os.ErrNotExist) {
			return "", err
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return "", fmt.Errorf("no existing ancestor for %s", p)
		}
		tail = append(tail, filepath.Base(cur))
		cur = parent
	}
}

// assertIsShallowProfile verifies home is a real directory containing a real
// regular .caam-shallow.json sidecar — the precondition for any destructive op
// (Delete / --force RemoveAll), so a symlinked base can't make caam rm real data.
func (m *Manager) assertIsShallowProfile(home string) error {
	st, err := os.Lstat(home) // Lstat — a symlinked profile path is never a profile
	if err != nil {
		return err
	}
	if st.Mode()&os.ModeSymlink != 0 || !st.IsDir() {
		return fmt.Errorf("%s is not a shallow profile directory", home)
	}
	// The sidecar must be a REAL regular file (Lstat, not Stat — a symlinked sidecar
	// pointing at some real file elsewhere must not qualify).
	mst, err := os.Lstat(filepath.Join(home, ProfileMetaFilename))
	if err != nil || mst.Mode()&os.ModeSymlink != 0 || !mst.Mode().IsRegular() {
		return fmt.Errorf("%s has no real %s; refusing to remove (not a shallow profile)", home, ProfileMetaFilename)
	}
	return nil
}

// assertNoSymlinkAncestor Lstats each path component of abs strictly below root;
// any symlink ancestor is rejected. (root itself is assumed already checked
// non-symlink by the caller.)
func assertNoSymlinkAncestor(root, abs string) error {
	rel, err := filepath.Rel(root, abs)
	if err != nil {
		return err
	}
	cur := root
	for _, seg := range strings.Split(filepath.ToSlash(rel), "/") {
		cur = filepath.Join(cur, seg)
		if cur == abs {
			break // don't re-check the leaf here
		}
		st, err := os.Lstat(cur)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return nil
			}
			return err
		}
		if st.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("symlinked ancestor %s", cur)
		}
	}
	return nil
}

// Profile is a lightweight summary returned by List.
type Profile struct {
	Name string
	Path string
	Meta *Meta // may be nil if metadata is missing/corrupt
}

// List returns all shallow profiles under the manager's base dir, sorted by name.
func (m *Manager) List() ([]Profile, error) {
	entries, err := os.ReadDir(m.baseDir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("read base dir: %w", err)
	}
	var out []Profile
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		name := e.Name()
		if _, err := validateProfileName(name); err != nil {
			continue
		}
		home := filepath.Join(m.baseDir, name)
		p := Profile{Name: name, Path: home}
		if meta, err := readMeta(home); err == nil {
			p.Meta = meta
		}
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// Get loads a single profile by name. Returns os.ErrNotExist if missing. A
// symlinked profile path is rejected (potential identity hijack); a malformed
// sidecar leaves Meta == nil (still listable/deletable).
func (m *Manager) Get(name string) (*Profile, error) {
	home, err := m.HomeFor(name)
	if err != nil {
		return nil, err
	}
	st, err := os.Lstat(home)
	if err != nil {
		return nil, err
	}
	if st.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("shallow profile %q is a symlink; refusing (potential identity hijack)", name)
	}
	if !st.IsDir() {
		return nil, fmt.Errorf("%s is not a directory", home)
	}
	p := &Profile{Name: name, Path: home}
	if meta, err := readMeta(home); err == nil {
		p.Meta = meta
	}
	return p, nil
}

// Delete removes a shallow profile and all its files. It is safe even if the
// directory contains symlinks: os.RemoveAll never traverses them. A symlinked
// profile path is rejected, and only a genuine shallow profile (real sidecar) is
// removed.
func (m *Manager) Delete(name string) error {
	home, err := m.HomeFor(name)
	if err != nil {
		return err
	}
	st, err := os.Lstat(home)
	if err != nil {
		return err
	}
	if st.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("shallow profile %q is a symlink; refusing (potential identity hijack)", name)
	}
	if !st.IsDir() {
		return fmt.Errorf("%s is not a directory", home)
	}
	// Sanity guard: never delete the user's real HOME by accident.
	if abs, err := filepath.Abs(home); err == nil && abs == m.realHome {
		return fmt.Errorf("refusing to delete real HOME (%s)", abs)
	}
	// Only ever RemoveAll a genuine shallow profile (real .caam-shallow.json), so a
	// symlinked base can't make this delete arbitrary real-home data.
	if err := m.assertIsShallowProfile(home); err != nil {
		return err
	}
	return os.RemoveAll(home)
}

// CredentialPath returns the absolute path to a profile's PRIMARY credential
// file, resolved from the profile's recorded provider layout.
func (m *Manager) CredentialPath(name string) (string, error) {
	prof, err := m.Get(name)
	if err != nil {
		return "", err
	}
	if prof.Meta == nil {
		return "", fmt.Errorf("shallow profile %q has no readable metadata", name)
	}
	layout, err := LayoutForProvider(prof.Meta.Provider) // strict: empty/unknown → error
	if err != nil {
		return "", err
	}
	home, err := m.HomeFor(name)
	if err != nil {
		return "", err
	}
	return filepath.Join(home, filepath.FromSlash(layout.Primary().DestRel)), nil
}

// validateProfileName enforces a small, safe character set for profile names
// (matching the regex used elsewhere in caam) and rejects path-traversal.
func validateProfileName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", fmt.Errorf("profile name cannot be empty")
	}
	if name == "." || name == ".." {
		return "", fmt.Errorf("invalid profile name: %q", name)
	}
	for _, r := range name {
		if !((r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') ||
			(r >= '0' && r <= '9') || r == '_' || r == '-' || r == '.' || r == '@' || r == '+') {
			return "", fmt.Errorf("invalid profile name: %q (only alphanumeric, _, -, ., @, + allowed)", name)
		}
	}
	if filepath.IsAbs(name) || filepath.VolumeName(name) != "" {
		return "", fmt.Errorf("invalid profile name: %q", name)
	}
	if strings.Contains(name, string(os.PathSeparator)) {
		return "", fmt.Errorf("invalid profile name: %q (path separator not allowed)", name)
	}
	return name, nil
}

// atomicSymlink replaces (or creates) dst as a symlink pointing at src.
// If dst exists as a symlink or non-directory, it is replaced. If it exists
// as a real directory, the call is a no-op (we don't clobber real data).
func atomicSymlink(src, dst string) error {
	if info, err := os.Lstat(dst); err == nil {
		if info.Mode()&os.ModeSymlink == 0 && info.IsDir() {
			return nil // preserve existing real directory
		}
		if err := os.Remove(dst); err != nil {
			return fmt.Errorf("remove existing %s: %w", dst, err)
		}
	}
	parent := filepath.Dir(dst)
	if err := os.MkdirAll(parent, 0o700); err != nil {
		return fmt.Errorf("create parent: %w", err)
	}
	return os.Symlink(src, dst)
}

// copyFileMode copies src to dst, then chmods dst to mode. The destination
// is written via a temp file in the same directory and renamed in place.
func copyFileMode(src, dst string, mode os.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("open source: %w", err)
	}
	defer in.Close()

	parent := filepath.Dir(dst)
	if err := os.MkdirAll(parent, 0o700); err != nil {
		return fmt.Errorf("create parent: %w", err)
	}

	tmp, err := os.CreateTemp(parent, ".tmp-*")
	if err != nil {
		return fmt.Errorf("create tmp: %w", err)
	}
	tmpName := tmp.Name()
	cleanup := func() { _ = os.Remove(tmpName) }
	defer func() {
		if tmp != nil {
			_ = tmp.Close()
		}
		if _, err := os.Stat(tmpName); err == nil {
			cleanup()
		}
	}()

	if _, err := io.Copy(tmp, in); err != nil {
		return fmt.Errorf("copy: %w", err)
	}
	if err := tmp.Chmod(mode); err != nil {
		return fmt.Errorf("chmod tmp: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		return fmt.Errorf("sync tmp: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close tmp: %w", err)
	}
	tmp = nil // prevent the deferred Close from racing rename
	if err := os.Rename(tmpName, dst); err != nil {
		return fmt.Errorf("rename: %w", err)
	}
	return nil
}

// writeFileAtomic writes data to path with mode, atomically.
func writeFileAtomic(path string, data []byte, mode os.FileMode) error {
	parent := filepath.Dir(path)
	if err := os.MkdirAll(parent, 0o700); err != nil {
		return fmt.Errorf("create parent: %w", err)
	}
	tmp, err := os.CreateTemp(parent, ".tmp-*")
	if err != nil {
		return fmt.Errorf("create tmp: %w", err)
	}
	tmpName := tmp.Name()
	defer func() {
		if _, err := os.Stat(tmpName); err == nil {
			_ = os.Remove(tmpName)
		}
	}()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write: %w", err)
	}
	if err := tmp.Chmod(mode); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("chmod: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("sync: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close: %w", err)
	}
	return os.Rename(tmpName, path)
}

func writeMeta(home string, m *Meta) error {
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return writeFileAtomic(filepath.Join(home, ProfileMetaFilename), data, 0o600)
}

func readMeta(home string) (*Meta, error) {
	data, err := os.ReadFile(filepath.Join(home, ProfileMetaFilename))
	if err != nil {
		return nil, err
	}
	var m Meta
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, err
	}
	return &m, nil // Provider is taken verbatim; may be "" or unknown for a hand-edited sidecar
}
