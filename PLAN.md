# PLAN: Generalized multi-harness shallow profiles for `caam` (Claude + Codex now, Antigravity-ready)

> **Issue:** https://github.com/Dicklesworthstone/coding_agent_account_manager/issues/34
>
> **One-line goal:** Make `caam shallow-profile` and `caam shallow-spawn` work for **Codex CLI** identities as well as the existing **Claude Code** identities, by replacing the Claude-hardcoded internals with a small **provider-keyed "Layout" registry** so that a third harness (Antigravity / `agy`) — or any future one — drops in as *one descriptor plus one registration line*, with **no changes to the manager or the CLI**.
>
> **This document is self-contained for understanding, and near-self-contained for implementation.** A fresh engineer should be able to understand *what* to change and *why* from this file alone, and to implement it while editing the repo in place. Everything you **modify** or whose **exact behavior is load-bearing** is quoted verbatim below (§4 + the phase snippets), with file paths as of the current `main`. A few **unchanged, trivial** helpers (`HomeFor`, `DefaultBaseDir`, `Profile`/`List`/`Get`/`Delete` plumbing) are summarized by behavior rather than fully quoted — when implementing in place you'll see their bodies; their exact text is not needed to understand the change. The Go snippets are written in a compressed one-line-body style for density; run `gofmt -w` (Phase 13) to expand them.

---

## 0. How to read this document

- **Sections 1–4** orient you: what `caam` is, the three different "isolation modes," what "shallow" means, and the exact current code you will modify (quoted verbatim).
- **Section 5** is the *target architecture* — the Layout registry that makes this generalizable. Read it before any phase.
- **Section 6** is the desired user experience.
- **Section 7** is the phase-by-phase implementation, with concrete Go.
- **Section 8** is the acceptance checklist (the definition of done).
- **Appendix A** is the *generalization proof*: the complete Antigravity (`agy`) Layout that a future engineer would paste in. If the architecture in Section 5 cannot express Appendix A without touching `Manager.Create` or the CLI, the design has failed — so Appendix A is also a design constraint, not just documentation.
- **Appendix B** records rejected alternatives and *why*, so reviewers don't re-litigate them.
- **Appendix C** is the canonical error-message catalog (keep messages identical to these so tests are stable).

---

## 1. Project briefing (for an engineer with no repo access)

- **What `caam` is:** "Coding Agent Account Manager." A Go CLI (`caam`) that lets a developer juggle **multiple subscription accounts** for AI coding CLIs — Claude Code (Anthropic, `claude`), Codex CLI (OpenAI, `codex`), Gemini/Antigravity (Google, `agy`), etc. — so they can switch accounts in <100ms when one hits a rate limit, and run several accounts **concurrently** on one machine.
- **Language / toolchain:** Go. `go.mod`:
  - module path: `github.com/Dicklesworthstone/coding_agent_account_manager`
  - `go 1.24.0`
- **CLI framework:** [`spf13/cobra`](https://github.com/spf13/cobra). Commands live under `cmd/caam/cmd/`; the root command is `rootCmd` and subcommands self-register in `init()` blocks.
- **Build / test commands:**
  ```sh
  go build ./...
  go vet ./...
  gofmt -l .                 # must print nothing (formatting gate)
  go test ./...              # full suite
  go test ./internal/shallow ./cmd/caam/cmd ./internal/provider/codex   # focused suite for this change
  ```
- **Where the relevant code lives:**
  | Path | Role |
  |------|------|
  | `internal/shallow/shallow.go` | The shallow-profile engine (`Manager`, layout constants, symlink farm). **Primary file to change.** |
  | `cmd/caam/cmd/shallow.go` | The `shallow-profile` / `shallow-spawn` cobra commands. **Primary file to change.** |
  | `internal/shallow/shallow_test.go` | Unit tests for the engine. |
  | `cmd/caam/cmd/shallow_test.go` | CLI-surface tests. |
  | `internal/provider/codex/codex.go` | Codex provider adapter. Reused (already has `EnsureFileCredentialStore`). |
  | `internal/provider/agy/agy.go` | Antigravity provider adapter. **Reference only** for Appendix A. |
  | `internal/authfile/authfile.go` | The credential vault (`Vault`, `ProfilePath`, per-tool auth-file sets). |
  | `internal/provider/provider.go` | The heavy `Provider` interface and a `Registry` (used by login/activate; *not* reused here — see Appendix B). |
  | `cmd/caam/cmd/codex_daemon.go` | `checkCodexDaemon` (used by `activate`/`next`, **not** by shallow — see §7 Phase 7). |
  | `README.md` | User docs (shallow section ~lines 141–208). |

---

## 2. The three isolation modes (do not confuse them)

`caam` offers three *different* ways to pin a session to an account. This change is about the **third**.

1. **`caam activate <tool> <profile>` / `caam next` — in-place auth swap.**
   Overwrites the *single* live auth file on disk (e.g. `~/.codex/auth.json`) with a vaulted copy. Fast, but **serial**: only one account is "active" at a time across the whole machine. (For Codex this is where `checkCodexDaemon` matters — a running Codex daemon caches the old auth in memory.)

2. **`caam profile add <tool> <profile>` — fully isolated HOME.**
   Each profile gets its own blank, fully-isolated `HOME`-like directory (via `provider.PrepareProfile`), sharing *nothing* with the real HOME. Strong isolation, but you lose shared state (shell history, git config, conversation history).

3. **`caam shallow-profile create … ` + `caam shallow-spawn … ` — shallow HOME (THIS CHANGE).**
   A per-identity `HOME` where **only the auth-bearing (and auth-policy) files are real**, and **everything else is a symlink back to the user's real `~/`**. You get N truly-parallel sessions, each pinned to a different account, while still sharing shell history, git config, SSH keys, and the harness's own conversation history. This is the orchestrator's tool for fanning out N accounts at once.

Today, mode 3 is **hardcoded to Claude Code**. This plan generalizes it.

---

## 2.1 Scope: core vs. safety hardening

This plan does two things. Keep them distinct so the team can choose how to land them:

- **Core (the issue, #34):** generalize the shallow engine into the `Layout` registry (§5), add **Codex**, keep the **flat, default-to-Claude** CLI, prove **Antigravity** is a drop-in (Appendix A). This is the bulk of the plan.
- **Core isolation hardening (Phase 3.5 — MUST ship; these are auth-exposure/data-loss bugs, several pre-existing):** (a) **shadow CAAM's own vault + `CAAM_HOME`** so a shallow HOME can't read every account's credentials; (b) **reject a symlinked profile path** (`alice -> bob` identity hijack); (c) **destructive ops only on real shallow-profile dirs** (so a symlinked base can't make `--force`/`delete` rm real-home files); (d) **canonical-base** in the symlink-farm skip (so a symlinked base can't expose the whole profile tree). Plus the lower-risk: Codex daemon **allow-list**; **credential-override env stripping** (API keys/tokens incl. `CODEX_API_KEY`); the security-critical **Codex `config.toml` regex** fix; **cleanup-on-error**; **strict** stored-metadata; comprehensive **`validateLayout`**.
- **Deferred to Appendix D (verify-first / larger):** (e) isolating Claude's **secondary** auth (`~/.config/claude-code/auth.json`), which hinges on Claude Code's exact `CLAUDE_CONFIG_DIR` semantics; (f) **precise nested-shadowing** that preserves sibling sharing under a shadowed top dir (the core does a conservative whole-top-dir shadow); (g) a **global sensitive-root** policy so a shallow *shell* can't launch a *different* harness against real auth; (h) parsing the copied Codex `config.toml` to unset arbitrary custom-provider `env_key`s. The core plan does **not** claim (e)/(g); it isolates the harness recorded in a profile's metadata.

Recommendation: land Core + Phase 3.5 together — for an *auth-isolation* feature, shipping with a known credential-exposure hole is not an option. The Appendix D items are explicitly out of core scope; track them as their own beads. Every hardening item is marked where it appears.

---

## 2.2 Threat model — what "isolation" means here (read before the safety sections)

Shallow profiles provide **cooperative, same-UID, path-based isolation**, and that is the *only* guarantee this plan makes:

- **Cooperative & path-based.** Each profile redirects `HOME` (and a few harness env vars) so a tool that *honors* those conventions reads/writes the right account's auth, and so concurrent tools don't *accidentally* cross-contaminate. The symlink-farm hardening (Phase 3.5) exists to stop a cooperative tool from *accidentally* falling back onto a path that resolves to real/other-account auth (the vault, another profile, a secondary auth file). That is the real, common failure mode for an orchestrator fanning out N agent sessions.
- **Same-UID, NOT a sandbox.** All profiles run as the same Unix user, as sibling directories under one base. A **hostile or buggy** same-UID process can always read `$BASE/<other>/...` (via `../` or an absolute path) or any real `~/` file — no `HOME` trick prevents that. Shallow profiles are **not** a security boundary against an adversarial process; for that you need OS isolation (separate users, containers, namespaces). The plan must not claim otherwise.
- **Consequences for "BLOCKER"-style findings.** Adversarial scenarios — a malicious process reading a sibling profile, a TOCTOU race where another process swaps `.codex` for a symlink mid-create — are **out of scope by design**, not bugs to fix here. We still do the cheap, high-value cooperative hardening (don't *hand* a tool a working path to the wrong auth: shadow the vault, strip discovery env vars, reject symlinked profile paths, refuse destructive ops on non-profiles), and we document the rest as non-goals (§8.1).

Everywhere this plan says "isolation," read it as this cooperative guarantee.

---

## 3. What "shallow" means, concretely

A shallow profile named `alice` is a directory (default `~/orch-homes/alice/`, overridable — see §4) that becomes the session's `HOME`. Inside it:

- A **small set of real files/dirs** that *must* differ per identity (the credentials, the per-identity lock, the harness's rewritten settings/policy file).
- **Symlinks** for everything else, pointing back into the real `~/`, so shared state passes through unchanged.

For **Claude** today, the real entries are:

```
~/orch-homes/alice/
  .claude/                     (REAL dir — so real files can live here and the
                                symlink farm doesn't replace it with a link to ~/.claude)
    .credentials.json          (REAL file, 0600 — per-identity OAuth tokens; the whole point)
    .credentials.lock          (REAL file, 0600 — Claude's flock target; a shared symlink here
                                would serialize concurrent sessions on one lock)
    projects/, todos/, ...     (SYMLINKS → ~/.claude/projects, etc. — shared conversation history)
  .claude.json                 (REAL file, 0600 — Claude rewrites this every run; a symlink would
                                mutate the user's real ~/.claude.json under the shallow identity)
  .caam-shallow.json           (REAL file, 0600 — caam's metadata sidecar; see Meta in §4)
  .bashrc, .gitconfig, .ssh    (SYMLINKS → ~/.bashrc, etc.)
```

**The generalization insight:** "which entries are real, where they live, what env vars to set, and where the credential comes from" is *exactly* the per-harness knowledge. Capture it in a data descriptor (`Layout`), key it by provider id, and the rest of the engine becomes harness-agnostic.

---

## 4. Current code shape (verbatim — this is what you are changing)

> These excerpts are the *current* `main`. Quoted so you need no repo access. Line numbers are approximate anchors.

### 4.1 `internal/shallow/shallow.go` — the Claude-shaped engine

Package-level constants that encode "what is real" (the Claude hardcoding):

```go
// ProfileMetaFilename is the JSON sidecar that records when/where the
// shallow profile was created and which credential source was used.
const ProfileMetaFilename = ".caam-shallow.json"

// Real (non-symlinked) entries inside the shallow HOME, relative to that HOME.
var realEntries = []string{
	".claude/.credentials.json",
	".claude/.credentials.lock",
	".claude.json",
	ProfileMetaFilename,
}

// realDirs are directories that must exist as real directories inside the
// shallow HOME so that real files can live in them.
var realDirs = map[string]bool{
	".claude": true,
}

// alwaysSkip lists top-level entries in the user's real HOME that should
// NEVER be symlinked.
var alwaysSkip = map[string]bool{
	".":          true,
	"..":         true,
	"orch-homes": true,
}
```

The metadata sidecar (no provider field today):

```go
// Meta is the JSON sidecar persisted at ~/orch-homes/<name>/.caam-shallow.json.
type Meta struct {
	Name           string    `json:"name"`
	CreatedAt      time.Time `json:"created_at"`
	CredentialFrom string    `json:"credential_from,omitempty"`
	RealHome       string    `json:"real_home"`
	Version        int       `json:"version"`
}
```

The manager and its base-dir precedence:

```go
type Manager struct {
	baseDir  string // e.g. ~/orch-homes
	realHome string // e.g. /home/user
}

// FULL CURRENT BODY (Phase 3.5 inserts the symlink-alias guard after the
// `abs == rabs` check):
func NewManager(baseDir, realHome string) (*Manager, error) {
	if strings.TrimSpace(realHome) == "" {
		h, err := os.UserHomeDir()
		if err != nil { return nil, fmt.Errorf("resolve user home: %w", err) }
		realHome = h
	}
	if strings.TrimSpace(baseDir) == "" { baseDir = DefaultBaseDir(realHome) }
	abs, err := filepath.Abs(baseDir)
	if err != nil { return nil, fmt.Errorf("resolve base dir: %w", err) }
	rabs, err := filepath.Abs(realHome)
	if err != nil { return nil, fmt.Errorf("resolve real home: %w", err) }
	if abs == rabs { return nil, fmt.Errorf("shallow base dir cannot be the user's real home") }
	// <-- Phase 3.5 (symlink-alias guard) goes here.
	return &Manager{baseDir: abs, realHome: rabs}, nil
}

// DefaultBaseDir precedence: $CAAM_SHALLOW_HOMES_DIR > $CAAM_HOME/shallow-homes > <realHome>/orch-homes
func DefaultBaseDir(realHome string) string {
	if v := strings.TrimSpace(os.Getenv("CAAM_SHALLOW_HOMES_DIR")); v != "" { return v }
	if v := strings.TrimSpace(os.Getenv("CAAM_HOME")); v != "" { return filepath.Join(v, "shallow-homes") }
	return filepath.Join(realHome, "orch-homes")
}

func (m *Manager) BaseDir() string  { return m.baseDir }
func (m *Manager) RealHome() string { return m.realHome }
// HomeFor = validateProfileName(name) then filepath.Join(m.baseDir, clean).
func (m *Manager) HomeFor(name string) (string, error) { /* validateProfileName + join */ }
```

The creation options and the **monolithic** `Create` (the thing to refactor):

```go
type CreateOptions struct {
	CredentialSource    string // path copied to <home>/.claude/.credentials.json
	SourceClaudeJSON    string // optional path copied to <home>/.claude.json
	CredentialFromLabel string // descriptive label recorded in Meta
	Force               bool
}

func (m *Manager) Create(name string, opts CreateOptions) (string, error) {
	home, _ := m.HomeFor(name)
	// ... handle existing dir + --force (os.RemoveAll) ...
	os.MkdirAll(home, 0o700)
	for dirName := range realDirs {
		os.MkdirAll(filepath.Join(home, dirName), 0o700)
	}
	m.populateSymlinks(home)                       // top-level farm
	for dirName := range realDirs {
		m.populateInnerSymlinks(home, dirName)     // inner farm (.claude/projects, etc.)
	}

	// --- Claude-hardcoded real-file writes (to be extracted) ---
	credPath := filepath.Join(home, ".claude", ".credentials.json")
	if opts.CredentialSource != "" {
		copyFileMode(opts.CredentialSource, credPath, 0o600)
	} else {
		writeFileAtomic(credPath, []byte(""), 0o600) // empty placeholder
	}
	lockPath := filepath.Join(home, ".claude", ".credentials.lock")
	writeFileAtomic(lockPath, []byte(""), 0o600)
	claudeJSONPath := filepath.Join(home, ".claude.json")
	switch {
	case opts.SourceClaudeJSON != "":
		copyFileMode(opts.SourceClaudeJSON, claudeJSONPath, 0o600)
	default:
		realClaudeJSON := filepath.Join(m.realHome, ".claude.json")
		if _, err := os.Stat(realClaudeJSON); err == nil {
			copyFileMode(realClaudeJSON, claudeJSONPath, 0o600)
		} else {
			writeFileAtomic(claudeJSONPath, []byte("{}\n"), 0o600)
		}
	}
	// --- end Claude-hardcoded block ---

	meta := Meta{Name: name, CreatedAt: time.Now().UTC(),
		CredentialFrom: opts.CredentialFromLabel, RealHome: m.realHome, Version: 1}
	writeMeta(home, &meta)
	return home, nil
}
```

The two symlink helpers (today they take a bare `dirName string`; they will take a `Layout`):

```go
// populateSymlinks reads top-level entries in realHome and symlinks each into
// home, skipping names that collide with realEntries/realDirs/alwaysSkip and the
// shallow base-dir-nesting guard.
func (m *Manager) populateSymlinks(home string) error {
	entries, _ := os.ReadDir(m.realHome)
	skip := map[string]bool{}
	for _, p := range realEntries {
		top := strings.SplitN(p, string(os.PathSeparator), 2)[0]
		skip[top] = true
	}
	for d := range realDirs { skip[d] = true }
	for k := range alwaysSkip { skip[k] = true }
	// Skip the shallow base dir if it nests under realHome (e.g. ~/orch-homes).
	if rel, err := filepath.Rel(m.realHome, m.baseDir); err == nil && !strings.HasPrefix(rel, "..") {
		top := strings.SplitN(rel, string(os.PathSeparator), 2)[0]
		if top != "" && top != "." { skip[top] = true }
	}
	for _, e := range entries {
		if skip[e.Name()] { continue }
		src := filepath.Join(m.realHome, e.Name())
		dst := filepath.Join(home, e.Name())
		if _, err := os.Lstat(src); errors.Is(err, os.ErrNotExist) { continue } // vanished mid-iter
		atomicSymlink(src, dst)
	}
	return nil
}

// populateInnerSymlinks symlinks every entry inside ~/<dirName> into
// <home>/<dirName>, skipping realEntries that live under <dirName>.
// Smart fallback: if ~/<dirName> doesn't exist, it's a no-op.
func (m *Manager) populateInnerSymlinks(home, dirName string) error {
	srcDir := filepath.Join(m.realHome, dirName)
	if _, err := os.Stat(srcDir); errors.Is(err, os.ErrNotExist) { return nil }
	skip := map[string]bool{}
	for _, p := range realEntries {
		parts := strings.SplitN(p, string(os.PathSeparator), 2)
		if len(parts) == 2 && parts[0] == dirName { skip[parts[1]] = true }
	}
	entries, _ := os.ReadDir(srcDir)
	for _, e := range entries {
		if skip[e.Name()] { continue }
		atomicSymlink(filepath.Join(srcDir, e.Name()), filepath.Join(home, dirName, e.Name()))
	}
	return nil
}
```

The lightweight summary type returned by `List`/`Get`:

```go
type Profile struct {
	Name string
	Path string
	Meta *Meta // may be nil if metadata is missing/corrupt
}
```

Reusable primitives — **full bodies** (unchanged; you call them; quoted so no repo access is needed):

```go
// atomicSymlink replaces (or creates) dst as a symlink → src. If dst is a real
// directory it is preserved (no-op); otherwise an existing dst is removed first.
func atomicSymlink(src, dst string) error {
	if info, err := os.Lstat(dst); err == nil {
		if info.Mode()&os.ModeSymlink == 0 && info.IsDir() {
			return nil // preserve existing real directory — never clobber real data
		}
		if err := os.Remove(dst); err != nil { return fmt.Errorf("remove existing %s: %w", dst, err) }
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil { return fmt.Errorf("create parent: %w", err) }
	return os.Symlink(src, dst)
}

// copyFileMode copies src→dst atomically (temp file in dst's dir, chmod, fsync,
// rename) and sets dst's mode. MkdirAll's the parent.
func copyFileMode(src, dst string, mode os.FileMode) error {
	in, err := os.Open(src)
	if err != nil { return fmt.Errorf("open source: %w", err) }
	defer in.Close()
	if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil { return fmt.Errorf("create parent: %w", err) }
	tmp, err := os.CreateTemp(filepath.Dir(dst), ".tmp-*")
	if err != nil { return fmt.Errorf("create tmp: %w", err) }
	tmpName := tmp.Name()
	defer func() { _ = tmp.Close(); if _, err := os.Stat(tmpName); err == nil { _ = os.Remove(tmpName) } }()
	if _, err := io.Copy(tmp, in); err != nil { return fmt.Errorf("copy: %w", err) }
	if err := tmp.Chmod(mode); err != nil { return fmt.Errorf("chmod tmp: %w", err) }
	if err := tmp.Sync(); err != nil { return fmt.Errorf("sync tmp: %w", err) }
	if err := tmp.Close(); err != nil { return fmt.Errorf("close tmp: %w", err) }
	tmp = nil // prevent the deferred Close from racing rename
	return os.Rename(tmpName, dst)
}

// writeFileAtomic writes data→path atomically with the given mode.
func writeFileAtomic(path string, data []byte, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil { return fmt.Errorf("create parent: %w", err) }
	tmp, err := os.CreateTemp(filepath.Dir(path), ".tmp-*")
	if err != nil { return fmt.Errorf("create tmp: %w", err) }
	tmpName := tmp.Name()
	defer func() { if _, err := os.Stat(tmpName); err == nil { _ = os.Remove(tmpName) } }()
	if _, err := tmp.Write(data); err != nil { _ = tmp.Close(); return fmt.Errorf("write: %w", err) }
	if err := tmp.Chmod(mode); err != nil { _ = tmp.Close(); return fmt.Errorf("chmod: %w", err) }
	if err := tmp.Sync(); err != nil { _ = tmp.Close(); return fmt.Errorf("sync: %w", err) }
	if err := tmp.Close(); err != nil { return fmt.Errorf("close: %w", err) }
	return os.Rename(tmpName, path)
}

func writeMeta(home string, m *Meta) error  // json.MarshalIndent(m,"","  ")+"\n" → ProfileMetaFilename (0600) via writeFileAtomic
func validateProfileName(name string) (string, error) // allows [A-Za-z0-9_.@+-]; rejects "", ".", "..", abs paths, and path separators
```

> Note the `tmp = nil` trick in `copyFileMode`: it disarms the deferred `Close` so the deferred cleanup doesn't race the successful `os.Rename`. Preserve it. (`readMeta`'s full body is given in §7 Phase 2 because it changes.)

`Get`/`List` populate `Profile.Meta` via `readMeta`, **swallowing `readMeta` errors** (so a profile with a missing/corrupt sidecar still lists and is deletable, with `Meta == nil`) — this is load-bearing for Phase 7's strict-spawn:

```go
func (m *Manager) Get(name string) (*Profile, error) {
	home, err := m.HomeFor(name)
	if err != nil { return nil, err }
	st, err := os.Stat(home)            // CURRENT: os.Stat follows symlinks. Phase 3.5 (2)
	if err != nil { return nil, err }   // changes this to os.Lstat + reject a leaf symlink
	if !st.IsDir() { return nil, fmt.Errorf("%s is not a directory", home) }
	p := &Profile{Name: name, Path: home}
	if meta, err := readMeta(home); err == nil { p.Meta = meta } // err → Meta stays nil
	return p, nil
}

func (m *Manager) Delete(name string) error {
	home, err := m.HomeFor(name)
	if err != nil { return err }
	st, err := os.Lstat(home)
	if err != nil { return err }
	if !st.IsDir() { return fmt.Errorf("%s is not a directory", home) }
	if abs, err := filepath.Abs(home); err == nil && abs == m.realHome {
		return fmt.Errorf("refusing to delete real HOME (%s)", abs)
	}
	return os.RemoveAll(home) // never traverses a leaf symlink; see the ancestor-symlink caveat below
}
```

`List` is `Get` over `os.ReadDir(m.baseDir)` (skipping non-dirs and invalid names), sorted by name. **`RemoveAll` safety:** it removes a *leaf* symlink without following it, so symlinks *inside* a profile can't reach the real HOME. **Caveat (Phase 3.5):** path resolution still follows symlinked *ancestor* components of the base, and `NewManager` uses `filepath.Abs` not `EvalSymlinks` — so `--base` = a symlink whose target is the real HOME could make `Delete`/`Create --force` touch real-HOME children. Phase 3.5 adds the guard. The credential accessor (only referenced by tests — see §7 Phase 3):

```go
// CredentialPath returns the absolute path to a profile's .credentials.json.
func (m *Manager) CredentialPath(name string) (string, error) {
	home, _ := m.HomeFor(name)
	return filepath.Join(home, ".claude", ".credentials.json"), nil
}
```

### 4.2 `cmd/caam/cmd/shallow.go` — the CLI surface

`create` flags + `runShallowProfileCreate` (abridged to the parts that change):

```go
shallowProfileCreateCmd.Flags().String("from-vault", "", "credential source: <tool>/<profile> ...")
shallowProfileCreateCmd.Flags().String("from-file", "", "credential source: arbitrary path ...")
shallowProfileCreateCmd.Flags().String("from-claude-json", "", "optional path → <home>/.claude.json")
shallowProfileCreateCmd.Flags().Bool("force", false, "overwrite existing")
shallowProfileCreateCmd.Flags().Bool("json", false, "output as JSON")

type shallowCreateOutput struct {
	Success        bool   `json:"success"`
	Name           string `json:"name"`
	Path           string `json:"path"`
	CredentialFrom string `json:"credential_from,omitempty"`
	Error          string `json:"error,omitempty"`
}
```

The `create` handler's preamble — these locals (`name`, `jsonOut`, `force`, `fromVault`, `fromFile`, `fromClaudeJSON`, `output`, `emit`, `mgr`) are what the Phase 8 snippet builds on; the `--tool` read is what you add:

```go
func runShallowProfileCreate(cmd *cobra.Command, args []string) error {
	name := args[0]
	jsonOut, _ := cmd.Flags().GetBool("json")
	force, _ := cmd.Flags().GetBool("force")
	fromVault, _ := cmd.Flags().GetString("from-vault")
	fromFile, _ := cmd.Flags().GetString("from-file")
	fromClaudeJSON, _ := cmd.Flags().GetString("from-claude-json")
	// tool, _ := cmd.Flags().GetString("tool")   // ADDED in Phase 8

	output := shallowCreateOutput{Name: name}
	emit := func(err error) error { // on error, print JSON {success:false,error} or return err
		if jsonOut {
			output.Success = err == nil
			if err != nil { output.Error = err.Error() }
			enc := json.NewEncoder(cmd.OutOrStdout()); enc.SetIndent("", "  ")
			return enc.Encode(output)
		}
		return err
	}

	if fromVault != "" && fromFile != "" {
		return emit(fmt.Errorf("--from-vault and --from-file are mutually exclusive"))
	}
	mgr, err := resolveShallowManager(cmd)
	if err != nil { return emit(fmt.Errorf("init shallow manager: %w", err)) }
	// ... Phase 8 provider resolution + opts construction + mgr.Create(name, opts) ...
}
```

The manager resolver shared by all shallow commands (note: the `--base` flag wins so tests/operators can isolate):

```go
func resolveShallowManager(cmd *cobra.Command) (*shallow.Manager, error) {
	base, _ := cmd.Flags().GetString("base")
	return shallow.NewManager(strings.TrimSpace(base), "")
}
```

The vault resolver that **rejects every non-Claude tool** (the gate to remove):

```go
// resolveVaultCredential takes "tool/profile" and returns (credPath, label, err).
func resolveVaultCredential(spec string) (string, string, error) {
	// ... split spec on "/" into tool, profile ...
	tool := strings.ToLower(strings.TrimSpace(parts[0]))
	profile := strings.TrimSpace(parts[1])
	if tool != "claude" {
		return "", "", fmt.Errorf("--from-vault currently supports tool=claude only (got %q)", tool)
	}
	if vault == nil { return "", "", fmt.Errorf("vault not initialized") }
	credPath := filepath.Join(vault.ProfilePath(tool, profile), ".credentials.json")
	if _, err := os.Stat(credPath); err != nil {
		return "", "", fmt.Errorf("vault profile %s/%s missing .credentials.json: %w", tool, profile, err)
	}
	return credPath, "vault:" + tool + "/" + profile, nil
}
```

> Here, `vault` is a **package-level `*authfile.Vault`** in package `cmd` (declared in `root.go`: `var ( vault *authfile.Vault; … )`), initialized in the root `PersistentPreRunE` as `vault = authfile.NewVault(authfile.DefaultVaultPath())`. `vault.ProfilePath(tool, profile)` returns `<vaultRoot>/<tool>/<profile>` (see §4.4). In tests, `shallowEnv` re-initializes it (see §4.6).
>
> `cmd/caam/cmd/root.go` also holds the auth-swap **tools map** and helpers that `shallow.SupportedProviders()` must stay a subset of (Phase 11 test #10):
> ```go
> var tools = map[string]func() authfile.AuthFileSet{
> 	"codex": authfile.CodexAuthFiles, "claude": authfile.ClaudeAuthFiles,
> 	"gemini": authfile.GeminiAuthFiles, "agy": authfile.AntigravityAuthFiles,
> 	"opencode": authfile.OpenCodeAuthFiles, "cursor": authfile.CursorAuthFiles,
> }
> func supportedTools() []string  // sorted keys of `tools`
> func supportedToolsList() string // strings.Join(supportedTools(), ", ")
> ```

`runShallowSpawn` — the env it builds today (only `HOME`, `SHALLOW_PROFILE`, strips `CLAUDE_CONFIG_DIR`):

```go
if printEnv {
	fmt.Fprintf(cmd.OutOrStdout(), "HOME=%s\n", prof.Path)
	fmt.Fprintf(cmd.OutOrStdout(), "SHALLOW_PROFILE=%s\n", name)
	return nil
}
// ... LookPath rest[0] ...
envMap := /* os.Environ() parsed into map */
envMap["HOME"] = prof.Path
envMap["SHALLOW_PROFILE"] = name
delete(envMap, "CLAUDE_CONFIG_DIR")
// ... flatten to []string, then: ...
return spawnExec(binPath, rest, envSlice)

// spawnExec is a var so tests can inject a fake:
var spawnExec = func(binPath string, args []string, env []string) error {
	return syscall.Exec(binPath, args, env)
}
```

### 4.3 `internal/provider/codex/codex.go` — reused Codex helpers

```go
// codexHome resolves $CODEX_HOME, else ~/.codex.
func codexHome() string { /* ... */ }
func ResolveHome() string { return codexHome() }

// Current detector regex — DOUBLE-QUOTE ONLY (Phase 6 broadens it to both styles).
var codexCredentialsStoreRe = regexp.MustCompile(`(?m)^\s*cli_auth_credentials_store\s*=\s*\"[^\"]*\"`)

// EnsureFileCredentialStore makes <home>/config.toml contain
// cli_auth_credentials_store = "file" (required so caam can manage auth.json).
// FULL CURRENT BODY (you modify only the regex above in Phase 6):
func EnsureFileCredentialStore(home string) error {
	home = strings.TrimSpace(home)
	if home == "" { return fmt.Errorf("codex home is empty") }
	configPath := filepath.Join(home, "config.toml")
	const settingLine = `cli_auth_credentials_store = "file"`
	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		if err := os.MkdirAll(home, 0700); err != nil { return fmt.Errorf("create codex home: %w", err) }
		content := "# Managed by caam to ensure file-based auth storage\n" + settingLine + "\n"
		return atomicWriteFile(configPath, []byte(content), 0600) // codex-pkg-local atomic writer
	}
	data, err := os.ReadFile(configPath)
	if err != nil { return fmt.Errorf("read config.toml: %w", err) }
	if match := codexCredentialsStoreRe.Find(data); match != nil {
		if strings.Contains(string(match), `"file"`) { return nil }
		updated := codexCredentialsStoreRe.ReplaceAll(data, []byte(settingLine))
		return atomicWriteFile(configPath, updated, 0600)
	}
	text := string(data)
	if !strings.HasSuffix(text, "\n") { text += "\n" }
	text += settingLine + "\n"
	return atomicWriteFile(configPath, []byte(text), 0600)
}

// auth lives at $CODEX_HOME/auth.json:
func (p *Provider) AuthFiles() []provider.AuthFileSpec {
	return []provider.AuthFileSpec{{Path: filepath.Join(codexHome(), "auth.json"), Required: true}}
}

// For fully-isolated (mode 2) profiles only:
func (p *Provider) Env(ctx, prof) (map[string]string, error) {
	return map[string]string{"CODEX_HOME": prof.CodexHomePath(), "HOME": prof.HomePath()}, nil
}
```

> `EnsureFileCredentialStore` is **exported** and has no dependency on `internal/shallow`, so importing `internal/provider/codex` from `internal/shallow` is cycle-free (verify with `go build ./...`).

### 4.4 `internal/authfile/authfile.go` — the vault

```go
func NewVault(basePath string) *Vault
func DefaultVaultPath() string // $CAAM_HOME/data/vault, else $XDG_DATA_HOME/caam/vault, else ~/.local/share/caam/vault
func (v *Vault) BasePath() string { return v.basePath } // the configured vault root (used by Phase 3.5 shadowing)

// `Backup` stores each auth file by basename: filename := filepath.Base(spec.Path)
// then writes <basePath>/<tool>/<profile>/<filename>. (This is why a Layout's
// AuthFile.VaultName = the file's basename — see Appendix A.)

// ProfilePath returns <basePath>/<tool>/<profile>.
func (v *Vault) ProfilePath(tool, profile string) string {
	return filepath.Join(v.basePath, tool, profile)
}

// Per-tool credential filenames (what `caam backup <tool>` writes into the vault):
//   claude → <vault>/claude/<profile>/.credentials.json   (primary)
//   codex  → <vault>/codex/<profile>/auth.json
//   agy    → <vault>/agy/<profile>/antigravity-oauth-token (+ google_accounts.json, oauth_creds.json, settings.json)
```

### 4.5 `internal/provider/agy/agy.go` — Antigravity auth model (reference for Appendix A)

```go
// agy is authenticated by an on-disk OAuth token; the token file is the sole authoritative credential.
func geminiHome() string    { /* $GEMINI_HOME else ~/.gemini */ }
func antigravityDir() string { return filepath.Join(geminiHome(), "antigravity-cli") }
func TokenPath() string       { return filepath.Join(antigravityDir(), "antigravity-oauth-token") } // REQUIRED
func AccountsPath() string    { return filepath.Join(geminiHome(), "google_accounts.json") }         // optional
func OAuthCredsPath() string  { return filepath.Join(geminiHome(), "oauth_creds.json") }             // optional
func SettingsPath() string    { return filepath.Join(antigravityDir(), "settings.json") }            // optional
func (p *Provider) Env(ctx, prof) (map[string]string, error) {
	return map[string]string{"HOME": prof.HomePath()}, nil // HOME-only; agy derives ~/.gemini from HOME
}
```

**Why this matters now:** Antigravity's real layout is **nested** (`~/.gemini/antigravity-cli/antigravity-oauth-token`) and **multi-file** (token + 3 shared `~/.gemini/*` files). The Layout abstraction in §5 must handle nested real dirs/files and multi-file credential provisioning *from the start*, or "easy to add a harness" is false. Appendix A proves it does.

### 4.6 Test harness facts you must preserve

- `internal/shallow/shallow_test.go`:
  - `fakeHome(t)` builds a temp real HOME with `.bashrc/.zshrc/.gitconfig`, `.ssh/.config/.cargo/.bun` (each with a `marker`), `.claude/{projects,todos,shell-snapshots}/marker`, `.claude.json`, `.claude/.credentials.json`.
  - `credSource(t, body)` writes a temp credential file, returns its path.
  - `TestCreateBuildsSymlinkFarmAndRealAuthFiles` asserts `.claude` is a real dir, auth files are real (not symlinks), inner dirs are symlinks, perms are `0600`.
  - A "no broken symlinks" test walks the tree and asserts every symlink resolves.
  - `mgr.CredentialPath("alice")` is used at ~line 407.
- `cmd/caam/cmd/shallow_test.go`:
  - `shallowEnv(t)` sets `CAAM_HOME`, `HOME`, `CAAM_SHALLOW_HOMES_DIR` to temp dirs and re-inits `vault = authfile.NewVault(authfile.DefaultVaultPath())`.
  - `runCmdCaptured(t, args...)` runs a **freshly-built** command tree (`newShallowTestRoot`) to avoid cobra flag-state bleed.
  - **`newShallowTestRoot()` hand-clones the create command's flags** — so any new flag (e.g. `--tool`) must be added **both** in the real `init()` *and* in `newShallowTestRoot`, or the CLI tests won't see it. This is the single easiest thing to forget. Verbatim (this is what you edit — add the one marked line):
    ```go
    func newShallowTestRoot() *cobra.Command {
    	root := &cobra.Command{Use: "caam-test"}
    	create := &cobra.Command{Use: "create <name>", Args: shallowProfileCreateCmd.Args, RunE: shallowProfileCreateCmd.RunE}
    	create.Flags().String("from-vault", "", "")
    	create.Flags().String("from-file", "", "")
    	create.Flags().String("from-claude-json", "", "")
    	create.Flags().String("tool", "", "")        // <-- ADD THIS (mirror the real init())
    	create.Flags().Bool("force", false, "")
    	create.Flags().Bool("json", false, "")
    	list := &cobra.Command{Use: "list", RunE: shallowProfileListCmd.RunE}
    	list.Flags().Bool("json", false, "")
    	del := &cobra.Command{Use: "delete <name>", Args: shallowProfileDeleteCmd.Args, RunE: shallowProfileDeleteCmd.RunE}
    	del.Flags().Bool("force", false, ""); del.Flags().Bool("json", false, "")
    	parent := &cobra.Command{Use: "shallow-profile"}
    	parent.PersistentFlags().String("base", "", "")
    	parent.AddCommand(create, list, del)
    	spawn := &cobra.Command{Use: "shallow-spawn <name> -- <cmd>", Args: shallowSpawnCmd.Args, RunE: shallowSpawnCmd.RunE}
    	spawn.Flags().String("base", "", ""); spawn.Flags().Bool("print-env", false, "")
    	root.AddCommand(parent); root.AddCommand(spawn)
    	return root
    }
    ```
  - `spawnExec` is swapped for a capturing fake to assert env/args without exec'ing.
  - Tests use `sh` (Unix) as the exec target.

---

## 5. Target architecture — the Layout registry

> Read this whole section before writing code. Everything in §7 is mechanical once this is clear.

### 5.1 Core idea

Introduce, **inside `internal/shallow`**, a `Layout` value type that fully describes one harness's shallow shape, plus a tiny **immutable** registry keyed by provider id (built once via `mustBuildLayouts`). `Manager.Create` and the CLI consult the registry and become harness-agnostic. Adding a harness = define a `Layout` + add one line to `mustBuildLayouts(...)`.

### 5.2 The `Layout` and `AuthFile` types

All path fields are **slash-form** (`".gemini/antigravity-cli"`) and converted with `filepath.FromSlash` at use sites; this keeps descriptors readable and OS-independent.

```go
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
	// (see clearedEnvVars + Layout.SpawnEnv in §5.3). Many
	// harnesses need nothing here. nil is allowed.
	ProviderEnvSet func(home string) []EnvVar

	// CreateManagedFiles, if non-nil, runs AFTER the symlink farm and credential
	// provisioning. It creates the layout's NON-credential real files and normalizes
	// policy: Claude → the .credentials.lock + .claude.json; Codex → config.toml +
	// EnsureFileCredentialStore. (This is the one non-declarative escape hatch; the
	// env and symlink behavior above stays declarative so the CLI/tests can reason
	// about it.)
	CreateManagedFiles func(m *Manager, home string, opts CreateOptions) error
}
```

### 5.3 Derived helpers, the env model, and layout validation

**Slash-path helpers + real-file set** (single source of truth — the credential dest never appears in two lists):

```go
func slashTop(p string) string  { return strings.SplitN(filepath.ToSlash(p), "/", 2)[0] }
func slashDir(p string) string  { return path.Dir(filepath.ToSlash(p)) }  // needs: import "path"
func slashBase(p string) string { return path.Base(filepath.ToSlash(p)) }

// realFileSet returns every slash-form file that must be REAL (never a symlink):
// RealFiles ∪ {each Credentials[i].DestRel} ∪ {ProfileMetaFilename}.
func (l Layout) realFileSet() map[string]bool {
	s := map[string]bool{ProfileMetaFilename: true}
	for _, f := range l.RealFiles { s[filepath.ToSlash(f)] = true }
	for _, c := range l.Credentials { s[filepath.ToSlash(c.DestRel)] = true }
	return s
}

// Primary returns the unique Credentials entry with Primary=true (guaranteed by
// validateLayout). Exported so the CLI can read the primary's VaultName/DestRel.
func (l Layout) Primary() AuthFile {
	for _, c := range l.Credentials { if c.Primary { return c } }
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
		if _, err := os.Lstat(abs); err == nil { out = append(out, abs) }
	}
	sort.Strings(out)
	return out
}
```

**The environment model.** Isolating the *active* harness requires more than *adding* `HOME`/`CODEX_HOME`: an inherited env var that repoints THIS harness's auth/config (e.g. a stale `GEMINI_HOME` for an agy profile, which has no `ProviderEnvSet` to overwrite it) must be DELETED. We delete the small known set for every spawn (cheap, and correct for whichever provider is active), then the active layout re-sets the one it owns. We also delete inherited **credential-override** vars (API keys / OAuth tokens) so the profile authenticates as its vaulted subscription identity, not an inherited key. And `--print-env` must express deletions in a shell-safe way.

> **Cross-harness limitation (be honest):** deleting these vars does NOT make a shallow *shell* safe for a DIFFERENT harness than the profile's own. A Claude profile's symlink farm still passes `~/.codex` straight through (Claude's layout doesn't reserve it), so `shallow-spawn <claude-profile> -- bash` then running `codex` would read the **real** `~/.codex`. A shallow profile isolates **the harness recorded in its metadata**; launching a different harness from its shell is out of scope here (a future "global sensitive-root" policy could close it — Appendix D). Do not claim otherwise.

```go
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
//   caam shallow-spawn p -- env OPENAI_API_KEY=… codex
var credentialOverrideEnvVars = []string{
	"OPENAI_API_KEY", "CODEX_API_KEY", "CODEX_ACCESS_TOKEN", // Codex API-key / token auth
	"ANTHROPIC_API_KEY", "ANTHROPIC_AUTH_TOKEN", "CLAUDE_CODE_OAUTH_TOKEN", // Claude API-key / token
	"CLAUDE_CODE_USE_BEDROCK", "CLAUDE_CODE_USE_VERTEX", "CLAUDE_CODE_USE_FOUNDRY", // Claude cloud-provider auth selectors (take precedence over subscription OAuth)
	// add GEMINI_API_KEY / GOOGLE_API_KEY when agy ships
}

// caamDiscoveryEnvVars: caam's OWN direct locator vars. Stripped so a cooperative
// tool/script in a shallow shell isn't *handed* a pointer to the vault/profiles via
// the environment. This is a courtesy, NOT a boundary: per the §2.2 threat model a
// same-UID process can read the vault regardless (it's the same user). We do NOT
// strip the generic XDG_DATA_HOME (another vault locator via authfile.DefaultVaultPath)
// — stripping it would change behavior for every app, and §2.2 already concedes
// same-UID vault access. Don't claim the vault is hidden from a child `caam`.
var caamDiscoveryEnvVars = []string{"CAAM_HOME", "CAAM_SHALLOW_HOMES_DIR"}

func clearedEnvVars() []string {
	out := append([]string{}, repointingEnvVars...)
	out = append(out, credentialOverrideEnvVars...)
	return append(out, caamDiscoveryEnvVars...)
}

// shellQuote single-quotes s for POSIX sh (escaping embedded quotes via '\''),
// so an arbitrary --base path can't inject shell into --print-env output.
func shellQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

// SpawnEnv applies the shallow env transform to env IN PLACE: delete every cleared
// var, set HOME + SHALLOW_PROFILE, then the provider sets. Used by the exec path.
// (Delete-then-set is correct: codex's ProviderEnvSet re-adds CODEX_HOME.)
func (l Layout) SpawnEnv(home, name string, env map[string]string) {
	for _, k := range clearedEnvVars() { delete(env, k) }
	env["HOME"] = home
	env["SHALLOW_PROFILE"] = name
	if l.ProviderEnvSet != nil {
		for _, kv := range l.ProviderEnvSet(home) { env[kv.Key] = kv.Value }
	}
}

// SpawnEnvLines renders the spawn env as POSIX-shell statements for --print-env:
// `export KEY='<quoted>'` for each set var (HOME, SHALLOW_PROFILE, provider sets),
// then `unset KEY` for each cleared var not re-set. Values are shell-quoted, and
// `export` (not bare KEY=VALUE) guarantees child processes inherit them. The
// primary, injection-free path is still the EXEC form (`-- cmd`); --print-env is
// for inspection and wrappers that `eval "$(… --print-env)"`. Shares the cleared/set
// logic with SpawnEnv (single source of truth).
func (l Layout) SpawnEnvLines(home, name string) []string {
	set := map[string]bool{}
	var lines []string
	emit := func(k, v string) { lines = append(lines, "export "+k+"="+shellQuote(v)); set[k] = true }
	emit("HOME", home)
	emit("SHALLOW_PROFILE", name)
	if l.ProviderEnvSet != nil {
		for _, kv := range l.ProviderEnvSet(home) { emit(kv.Key, kv.Value) }
	}
	for _, k := range clearedEnvVars() {
		if !set[k] { lines = append(lines, "unset "+k) }
	}
	return lines
}
```

> **Why these vars (and not `XDG_CONFIG_HOME`/`XDG_DATA_HOME`):** for the shipped Claude/Codex layouts, `CODEX_HOME`/`CODEX_SQLITE_HOME` (set) and a redirected `HOME` fully cover the active harness; deleting `CLAUDE_CONFIG_DIR`/`GEMINI_HOME` covers the agy-style "reads-a-var-we-don't-set" case. Stripping `XDG_CONFIG_HOME`/`XDG_DATA_HOME` would change behavior for *every* app launched in a shallow shell (fallback to `HOME/.config`/`HOME/.local/share`) — broad and beyond this feature's need. It's an **optional extension** (Appendix D / §2.1), most relevant once an XDG-based harness (`opencode`) gets shallow support and is paired with isolating Claude's secondary auth.
>
> **Credential-override vars ARE stripped by default.** Unlike repointing (which redirects a *file path*), `OPENAI_API_KEY` / `ANTHROPIC_API_KEY` / `ANTHROPIC_AUTH_TOKEN` / `CLAUDE_CODE_OAUTH_TOKEN` *replace the auth itself* — an inherited key would make every shallow profile authenticate as that one key, defeating per-profile identity. So they're deleted by default (the whole point of shallow profiles is per-identity auth). Document the escape hatch in the README: to use env-key auth in a shallow session, inject it past caam — `caam shallow-spawn p -- env OPENAI_API_KEY=… codex`. This is a deliberate, documented behavior, with tests asserting the strip.

**Layout validation** (run once, at registry build — programmer-error guard that also closes a real write-through-symlink hole):

```go
// validateLayout is called by mustBuildLayouts (§5.4) for every built-in layout.
// A failure is a programmer error and panics at init, never at runtime.
func validateLayout(l Layout) error {
	if l.Provider == "" || l.Provider != strings.ToLower(strings.TrimSpace(l.Provider)) {
		return fmt.Errorf("provider id must be non-empty lowercase, got %q", l.Provider)
	}
	if err := checkBasename(l.Provider); err != nil { // used as a vault dir name → safe component
		return fmt.Errorf("provider id %q: %w", l.Provider, err)
	}
	if l.DefaultBin == "" { return fmt.Errorf("%s: DefaultBin required", l.Provider) }
	if l.ProviderEnvSet != nil { // env keys are rendered as shell in --print-env
		envKeyRe := regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
		seenKey := map[string]bool{}
		for _, kv := range l.ProviderEnvSet("/probe") {
			if !envKeyRe.MatchString(kv.Key) { return fmt.Errorf("%s: invalid env key %q", l.Provider, kv.Key) }
			if kv.Key == "HOME" || kv.Key == "SHALLOW_PROFILE" { return fmt.Errorf("%s: ProviderEnvSet must not set %q", l.Provider, kv.Key) }
			if seenKey[kv.Key] { return fmt.Errorf("%s: duplicate ProviderEnvSet key %q", l.Provider, kv.Key) }
			seenKey[kv.Key] = true
		}
	}

	realDir := map[string]bool{}
	for _, d := range l.RealDirs {
		if err := checkSlashRel(d); err != nil { return fmt.Errorf("%s RealDir %q: %w", l.Provider, d, err) }
		realDir[filepath.ToSlash(d)] = true
	}
	for _, f := range l.RealFiles {
		if err := checkSlashRel(f); err != nil { return fmt.Errorf("%s RealFile %q: %w", l.Provider, f, err) }
	}
	prim, dest, vname := 0, map[string]bool{}, map[string]bool{}
	for _, c := range l.Credentials {
		if err := checkSlashRel(c.DestRel); err != nil { return fmt.Errorf("%s DestRel %q: %w", l.Provider, c.DestRel, err) }
		if err := checkBasename(c.VaultName); err != nil { // joined into a vault path → must be a safe leaf
			return fmt.Errorf("%s VaultName %q: %w", l.Provider, c.VaultName, err)
		}
		if c.Primary { prim++; if !c.Required { return fmt.Errorf("%s: primary credential must be Required", l.Provider) } }
		if dest[filepath.ToSlash(c.DestRel)] { return fmt.Errorf("%s: duplicate DestRel %q", l.Provider, c.DestRel) }
		if vname[c.VaultName] { return fmt.Errorf("%s: duplicate VaultName %q", l.Provider, c.VaultName) }
		dest[filepath.ToSlash(c.DestRel)] = true; vname[c.VaultName] = true
	}
	if prim != 1 { return fmt.Errorf("%s: exactly one Primary credential required, got %d", l.Provider, prim) }
	// CRITICAL: every nested real file's parent dir must be a RealDir, else a copy
	// would write THROUGH a passthrough symlink into the real ~/.
	for f := range l.realFileSet() {
		if p := slashDir(f); p != "." && !realDir[p] {
			return fmt.Errorf("%s: real file %q parent %q is not a RealDir", l.Provider, f, p)
		}
	}
	for _, f := range l.RealFiles {
		if dest[filepath.ToSlash(f)] { return fmt.Errorf("%s: RealFile %q duplicates a credential DestRel", l.Provider, f) }
	}
	roots := map[string]bool{}
	for _, r := range l.InnerSymlinkRoots {
		if err := checkSlashRel(r); err != nil { return fmt.Errorf("%s InnerSymlinkRoot %q: %w", l.Provider, r, err) }
		r = filepath.ToSlash(r)
		if !realDir[r] { return fmt.Errorf("%s: InnerSymlinkRoot %q is not a RealDir", l.Provider, r) }
		roots[r] = true
	}
	checkNames := func(kind, root string, names []string) error {
		seen := map[string]bool{}
		for _, n := range names {
			if err := checkBasename(n); err != nil { return fmt.Errorf("%s %s[%q] name %q: %w", l.Provider, kind, root, n, err) }
			if seen[n] { return fmt.Errorf("%s %s[%q] duplicate name %q", l.Provider, kind, root, n) }
			seen[n] = true
		}
		return nil
	}
	for k, names := range l.InnerSkip {
		rk := filepath.ToSlash(k) // normalize the map key before the root lookup
		if !roots[rk] { return fmt.Errorf("%s: InnerSkip root %q is not an InnerSymlinkRoot", l.Provider, k) }
		if err := checkNames("InnerSkip", rk, names); err != nil { return err }
	}
	for k, names := range l.InnerSymlinkAllow {
		rk := filepath.ToSlash(k)
		if !roots[rk] { return fmt.Errorf("%s: InnerSymlinkAllow root %q is not an InnerSymlinkRoot", l.Provider, k) }
		if len(l.InnerSkip[k]) > 0 { return fmt.Errorf("%s: root %q sets both InnerSkip and InnerSymlinkAllow", l.Provider, k) }
		if err := checkNames("InnerSymlinkAllow", rk, names); err != nil { return err }
	}
	return nil
}

// checkSlashRel rejects anything that isn't a safe relative slash path.
func checkSlashRel(p string) error {
	if p == "" || p == "." || p == ".." { return fmt.Errorf("empty or dot path") }
	if strings.ContainsRune(p, '\\') { return fmt.Errorf("backslash not allowed (use slash-form)") }
	if path.IsAbs(p) { return fmt.Errorf("must be relative") }
	// Also reject OS-absolute / volume-prefixed paths: path.IsAbs("C:/x") is false,
	// but filepath.FromSlash("C:/x") is absolute on Windows and would escape the home.
	osp := filepath.FromSlash(p)
	if filepath.IsAbs(osp) || filepath.VolumeName(osp) != "" {
		return fmt.Errorf("must be relative (no volume/drive prefix)")
	}
	if strings.HasSuffix(p, "/") { return fmt.Errorf("trailing slash") }
	for _, seg := range strings.Split(filepath.ToSlash(p), "/") {
		if seg == "" || seg == "." || seg == ".." { return fmt.Errorf("bad path segment %q", seg) }
	}
	return nil
}

// checkBasename rejects anything that isn't a safe single path component (used for
// VaultName and InnerSkip/Allow names — all of which are joined into paths).
func checkBasename(n string) error {
	if n == "" || n == "." || n == ".." { return fmt.Errorf("empty or dot name") }
	if strings.ContainsAny(n, `/\`) { return fmt.Errorf("must be a single component (no separators)") }
	return nil
}
```

### 5.4 The registry, ordering, and provider resolution

The registry is **immutable after init** — built once by `mustBuildLayouts`, never mutated at runtime. There is **no exported `RegisterLayout`**: built-in layouts are added to the `mustBuildLayouts(...)` call (that is the literal "one line" to add Antigravity). This removes any global-mutable-state / parallel-test hazard and forces every layout through `validateLayout`.

```go
const (
	ProviderClaude = "claude"
	ProviderCodex  = "codex"
)

// providerOrder is the display order (Claude first, then Codex, then any others
// alphabetically). All "supported: ..." strings derive from SupportedProviders(),
// so adding a layout auto-updates every message — in a stable, ergonomic order.
var providerOrder = []string{ProviderClaude, ProviderCodex}

// layouts is built once and never mutated. Adding Antigravity = add AntigravityLayout()
// to this call (the single line referenced throughout this plan).
var layouts = mustBuildLayouts(
	ClaudeLayout(),
	CodexLayout(),
	// AntigravityLayout(),  // ← ships later; see Appendix A
)

func mustBuildLayouts(ls ...Layout) map[string]Layout {
	m := make(map[string]Layout, len(ls))
	for _, l := range ls {
		if err := validateLayout(l); err != nil { panic("shallow: invalid layout: " + err.Error()) }
		if _, dup := m[l.Provider]; dup { panic("shallow: duplicate layout " + l.Provider) }
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
		if _, ok := layouts[p]; ok { out = append(out, p); seen[p] = true }
	}
	extra := make([]string, 0)
	for p := range layouts { if !seen[p] { extra = append(extra, p) } }
	sort.Strings(extra)
	return append(out, extra...)
}

func supportedList() string { return strings.Join(SupportedProviders(), ", ") }

// NormalizeProvider resolves USER INPUT only (a --tool flag or a --from-vault
// tool): "" → claude (the ergonomic default harness, NOT a compat shim), else a
// registered provider or a clear error. Do NOT use this on stored metadata.
func NormalizeProvider(p string) (string, error) {
	p = strings.ToLower(strings.TrimSpace(p))
	if p == "" { return ProviderClaude, nil }
	if _, ok := layouts[p]; !ok {
		return "", fmt.Errorf("unsupported shallow provider %q (supported: %s)", p, supportedList())
	}
	return p, nil
}

// LayoutForProvider returns the layout for a known provider id (a normalized input
// or a stored metadata value). It is STRICT and EXACT: no empty→claude default and
// no case/space normalization — `Create` always writes canonical lowercase ids, so
// a stored id that isn't byte-exact-registered is malformed and must surface, not
// be silently "fixed". (NormalizeProvider does the lower/trim for *user input*.)
func LayoutForProvider(p string) (Layout, error) {
	l, ok := layouts[p]
	if !ok {
		if p == "" { return Layout{}, fmt.Errorf("shallow profile has no recorded provider (malformed metadata)") }
		return Layout{}, fmt.Errorf("unsupported shallow provider %q (supported: %s)", p, supportedList())
	}
	return l, nil
}
```

> **Two entry points, on purpose:** `NormalizeProvider` is for **user input** (lower/trim + empty→claude default). `LayoutForProvider` is for an **already-decided** id (normalized input, or a stored metadata value) and is **strict + exact** — empty/unknown is an error. A stored profile with no provider is *malformed*, not *legacy Claude*, so it surfaces a clear error rather than silently running as Claude (which, for a Codex profile, would skip `CODEX_HOME` and leak — see Phase 7).
>
> **Immutability — decision:** `layouts` is built once and never reassigned. A returned `Layout` is a struct copy that still *shares* its slices/maps with the registry, so immutability is **by convention**: callers (engine + CLI) only **read** layout fields and must never mutate a returned `Layout`'s `RealDirs`/`InnerSkip`/etc. (none in this plan do). Do not export any mutator. *(Optional belt-and-suspenders: have `LayoutForProvider` return `cloneLayout(l)` — a deep copy of the slices/maps — to make it enforced rather than conventional; it's called per spawn/create so the cost is negligible. The core plan uses the convention; ship the clone if you want the compiler-free guarantee.)*

### 5.5 The two concrete layouts shipped in this change

```go
func ClaudeLayout() Layout {
	return Layout{
		Provider:          ProviderClaude,
		DefaultBin:        "claude",
		RealDirs:          []string{".claude"},
		RealFiles:         []string{".claude/.credentials.lock", ".claude.json"},
		Credentials:       []AuthFile{{VaultName: ".credentials.json", DestRel: ".claude/.credentials.json", Primary: true, Required: true}},
		InnerSymlinkRoots: []string{".claude"},
		// No ProviderEnvSet: HOME is redirected and the universal repoint-var strip
		// (§5.3) deletes any inherited CLAUDE_CONFIG_DIR — matching today's behavior.
		// DO NOT set CLAUDE_CONFIG_DIR here: official Claude Code docs indicate it
		// relocates the PRIMARY .credentials.json (not just a secondary config dir),
		// so setting it would make Claude ignore the vaulted <home>/.claude/.credentials.json
		// this layout writes. (An earlier draft set it — intentionally reverted.)
		// Isolating Claude's SECONDARY auth (~/.config/claude-code/auth.json +
		// .claude/settings.json apiKeyHelper) is deferred to Appendix D.1 (verify-first).
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
		// Start MINIMAL and audited. Current Codex writes conversation state as
		// `sessions/` and a `history.jsonl` FILE under CODEX_HOME. `logs/` is
		// deliberately EXCLUDED until audited — Codex login diagnostics can write
		// OAuth-callback / account details into logs, so sharing the real log dir
		// could leak auth context across identities. The allow-list works for files
		// and dirs alike; verify each entry against the installed Codex and add only
		// entries confirmed to hold no socket/auth/diagnostic material.
		InnerSymlinkAllow: map[string][]string{".codex": {"sessions", "history.jsonl"}},
		ProviderEnvSet: func(home string) []EnvVar {
			cx := filepath.Join(home, ".codex")
			return []EnvVar{{Key: "CODEX_HOME", Value: cx}, {Key: "CODEX_SQLITE_HOME", Value: cx}}
		},
		CreateManagedFiles: createCodexManagedFiles,
	}
}
```

Notes:
- **Codex daemon isolation is allow-list-based** (`InnerSymlinkAllow`), not a fragile deny-list. The earlier plan deny-listed a guessed `app-server-control` basename; but the Codex daemon's runtime/control-socket directory name is an external-tool artifact that has varied across releases and is **not** referenced in this repo. A deny-list that misses the real name would let `populateInnerSymlinks` create `<shallow>/.codex/<realname>` → `~/.codex/<realname>`, and a daemon found at `$CODEX_HOME/<realname>` would then follow that symlink to the **real** daemon (which has another identity's auth cached in memory) — a bleed. Setting `CODEX_HOME` does **not** rescue this, because the leaked socket is *inside* `CODEX_HOME` via the symlink. The allow-list closes the hole structurally: only verified-safe shared state (`sessions`/`history.jsonl`) is shared; any unknown runtime dir is simply absent, so Codex creates a fresh one under the shallow `CODEX_HOME`. (`logs` is deliberately excluded — login diagnostics can write auth context.) The trade-off is that any *other* genuinely-shareable `~/.codex/*` data dir won't be shared until audited and added — a safe failure mode (lost sharing, never lost isolation). Before extending the list, confirm the entry is pure data with no live socket or auth diagnostics.
- **Repoint-var stripping** (`repointingEnvVars`, §5.3) isolates the *active* harness against stale parent overrides (e.g. a parent `CODEX_HOME=/bogus` can't repoint a Codex profile). It does **not** make a shallow *shell* safe for a *different* harness — a Claude profile still symlinks `~/.codex` through, so launching `codex` from a Claude shallow shell reads real auth. See the cross-harness limitation in §5.3 and Appendix D.2.
- **Convention to mirror:** the repo already exposes `supportedTools()` / `supportedToolsList()` in `cmd/caam/cmd/root.go` (keys of a `var tools map[string]func() authfile.AuthFileSet`) as the source of truth for auth-swap tool names. `shallow.SupportedProviders()` is the deliberately *narrower* analogue: only providers with a shallow `Layout` registered (`claude, codex` now), so `--from-vault gemini/x` is correctly rejected as "not supported **for shallow profiles**" even though `gemini` is a valid auth-swap tool. A cheap CLI test asserts `SupportedProviders() ⊆ keys(tools)` to catch a typo'd provider id desyncing from the vault tool name (Phase 11).

### 5.6 Design rationale (so reviewers don't re-litigate — full list in Appendix B)

- **Why a local `Layout` registry, not the existing `provider.Registry`?** `provider.Provider` is a heavy interface (`Login`, `Status`, `ImportAuth`, `ValidateToken`, …) about *auth flows*. Shallow needs only *filesystem shape + env + vault filenames*. Coupling to it would drag browser/login machinery into shallow and make a trivial harness expensive to add. The descriptor is the right altitude. **Honest caveat:** this means "we don't depend on the heavy `provider.Provider` interface" — individual layouts *may* still call a small provider-specific helper (Codex's `CreateManagedFiles` calls `codex.EnsureFileCredentialStore`); `internal/shallow` importing `internal/provider/codex` is fine because that edge is cycle-free. The `Provider` **id strings stay identical** across both registries, so they never diverge (a Phase-11 test asserts the subset relationship).
- **Why generic credential provisioning (`Credentials []AuthFile`)?** Antigravity needs 4 files from one vault dir; Claude/Codex need 1. A list degenerates cleanly to 1 and makes the multi-file case *data*, not *code* — "add a harness = add a descriptor," never "edit `Create`."
- **Why a declarative env model (`ProviderEnvSet` + cleared-var sets, rendered by `SpawnEnv`/`SpawnEnvLines`) instead of a free-form closure?** Two reasons. (1) **Print-env must express deletions in shell** — a wrapper built from `--print-env` has to `export` the sets and `unset` the cleared vars (and quote values), which a map-mutating closure can't render. (2) **A single source of truth** for exec and print, with no `ExtraEnvKeys` ordering field that drifts from what's actually set. (Note: this isolates the *active* harness against stale parent overrides and inherited API keys; it does **not** by itself make a shallow *shell* safe for a *different* harness — that needs the global-root work in Appendix D.2.)
- **Why an allow-list option for inner symlinks?** "Symlink everything except known-bad" is unsafe for a root that can hold volatile runtime/socket artifacts (Codex). An allow-list fails safe (lost sharing) instead of fails open (auth bleed). Claude/Antigravity keep the deny-list default; Codex opts into the allow-list.
- **Why an immutable registry (no exported `RegisterLayout`)?** A global mutable map plus exported registration invites runtime mutation and parallel-test races. Built once via `mustBuildLayouts(...)` (which runs `validateLayout` on each), it's race-free and still a one-line addition for a new harness.
- **Why strict stored-metadata resolution (no empty→claude default on read)?** A stored profile with no/unknown provider is *malformed*, full stop — silently treating it as Claude would be both a guess and a safety hole (a Codex profile mis-run as Claude skips `CODEX_HOME` and leaks). `NormalizeProvider("")→claude` is for **user input** only (the ergonomic default); stored metadata goes through strict `LayoutForProvider`.
- **Why keep the flat command tree and default to Claude (not a `caam shallow <provider> …` subtree)?** Ergonomics. The common case is "spawn a Claude session"; qualifying every command with a provider is friction. `create` defaults to `claude`, infers the provider from `--from-vault <tool>/<profile>`, needs `--tool` only for the empty-credential Codex case; `shallow-spawn` reads the provider from metadata (no new surface). A fully "general & unified" tree was considered and rejected as less ergonomic (Appendix B).
- **No backwards compatibility anywhere.** No compat shims, aliases, or migration logic. `Version` simply stamps the current schema; the empty→claude default is for *user input* only.

---

## 6. Desired user experience

```sh
# One-time: vault the accounts (existing commands; codex/agy already supported by `backup`).
caam backup claude alice@example.com
caam backup codex  bob@example.com

# Create shallow identities. Provider resolution is ergonomic:
caam shallow-profile create alice --from-vault claude/alice@example.com   # → claude (inferred)
caam shallow-profile create cbob  --from-vault codex/bob@example.com       # → codex  (inferred)
caam shallow-profile create cbob  --tool codex --from-vault codex/bob      # explicit, must agree with inference
caam shallow-profile create cfile --tool codex --from-file /path/auth.json # codex from arbitrary file
caam shallow-profile create scratch                                        # → claude (default), empty creds
caam shallow-profile create cscratch --tool codex                          # → codex,  empty creds
caam shallow-profile create alice --json                                   # machine-readable

# Spawn — no provider flag needed; provider comes from metadata.
caam shallow-spawn alice -- claude
caam shallow-spawn cbob  -- codex
caam shallow-spawn cbob  --print-env    # dry run for shell wrappers
```

**Provider resolution rules** (implemented in `inferShallowProvider`, §7 Phase 8):

1. If `--from-vault <tool>/<profile>` is given, the inferred provider is `<tool>`.
2. If `--tool` is also given and disagrees with (1) → error (Appendix C).
3. Else if `--tool` is given, use it.
4. Else → `claude` (the default harness).

`--print-env` emits deterministic, shell-quoted, eval-able statements (`SpawnEnvLines`, §5.3): `export KEY='value'` for `HOME`, `SHALLOW_PROFILE`, and the layout's provider sets (in that order), then `unset KEY` for every cleared var (repointing + credential-override) not re-set. `eval "$(caam shallow-spawn <name> --print-env)"` reproduces the exec path's isolation; values are quoted so an exotic `--base` can't inject shell. (The exec form `-- cmd` remains the primary, injection-free path.)

After the `export` lines, there is one `unset KEY` for **every** var in `clearedEnvVars()` (the full list = `repointingEnvVars` + `credentialOverrideEnvVars` + `caamDiscoveryEnvVars`, §5.3) that the active layout did **not** re-set, in that order. Illustrative (the `…` stands for the remaining credential-override entries — `CODEX_API_KEY`, `CODEX_ACCESS_TOKEN`, the `ANTHROPIC_*`/`CLAUDE_CODE_*` vars):

```
# claude  (no ProviderEnvSet — every cleared var is unset)
export HOME='<base>/alice'
export SHALLOW_PROFILE='alice'
unset CLAUDE_CONFIG_DIR
unset CODEX_HOME
unset CODEX_SQLITE_HOME
unset GEMINI_HOME
unset OPENAI_API_KEY
…                          # the rest of credentialOverrideEnvVars (CODEX_*, ANTHROPIC_*, CLAUDE_CODE_USE_*)
unset CAAM_HOME
unset CAAM_SHALLOW_HOMES_DIR
# codex  (sets CODEX_HOME + CODEX_SQLITE_HOME, so those are NOT unset)
export HOME='<base>/cbob'
export SHALLOW_PROFILE='cbob'
export CODEX_HOME='<base>/cbob/.codex'
export CODEX_SQLITE_HOME='<base>/cbob/.codex'
unset CLAUDE_CONFIG_DIR
unset GEMINI_HOME
unset OPENAI_API_KEY
…                          # the rest of credentialOverrideEnvVars
unset CAAM_HOME
unset CAAM_SHALLOW_HOMES_DIR
```

---

## 7. Implementation phases

> Edit files **in place** (no `fooV2.go`). Make changes **manually** (no codemod scripts). After substantive changes run `go build ./... && go vet ./... && gofmt -l . && go test ./internal/shallow ./cmd/caam/cmd ./internal/provider/codex`.

### Phase 1 — Layout type, registry, helpers (`internal/shallow/shallow.go`)

1. Add imports `"path"` (for `path.Dir`/`path.Base` on slash paths), `"syscall"` (for `errors.Is(err, syscall.ENOTDIR)` in `populateInnerSymlinks`), `"regexp"` (for the `ProviderEnvSet` key check in `validateLayout`), and `"github.com/Dicklesworthstone/coding_agent_account_manager/internal/authfile"` (for `authfile.DefaultVaultPath()` in `NewManager`, Phase 3.5). **Do NOT add the `codexprovider` import yet** — it's only used in Phase 6, and Go fails the build with "imported and not used" if you add it early. (Both `internal/shallow`→`internal/provider/codex` and `internal/shallow`→`internal/authfile` edges are cycle-free — neither imports `internal/shallow`.)
2. Add, all in `internal/shallow/shallow.go`:
   - the `EnvVar`, `AuthFile`, and `Layout` types (§5.2);
   - the helpers `slashTop/slashDir/slashBase/realFileSet`, the exported `Primary`/`ManagedFilePaths`, the env model (`EnvVar`, `repointingEnvVars`, `Layout.SpawnEnv`, `Layout.SpawnEnvLines`), and `validateLayout`/`checkSlashRel` (§5.3);
   - the registry: `ProviderClaude/ProviderCodex` consts, `providerOrder`, `mustBuildLayouts`, the `var layouts = mustBuildLayouts(ClaudeLayout(), CodexLayout())` line, `SupportedProviders`/`supportedList`, `NormalizeProvider`, `LayoutForProvider` (§5.4) — note there is **no** exported `RegisterLayout` and **no** `init()`;
   - the `ClaudeLayout`/`CodexLayout` constructors (§5.5).

> **Note on intermediate build state — implement in two coherent batches, not phase-by-phase.** Several phases forward-reference later ones (Phase 1's `layouts` mentions `createClaude/CodexManagedFiles` from Phases 5–6; Phase 3's `Create` calls `provisionCredentials` from Phase 4; the Phase 8 CLI snippet sets `output.Provider`/`output.ManagedFiles` whose struct fields are added in Phase 9). So `go build ./...` won't be green *between* phases. Land **Batch 1 = the engine** (`internal/shallow`, Phases 1–6) as one unit, then **Batch 2 = the CLI** (`cmd/caam/cmd/shallow.go`, Phases 7–9 — apply the Phase 9 struct-field additions together with the Phase 8 handler) as one unit; build/vet/test after each batch. The §7 intro's "build after substantive changes" means after a coherent unit, not after every numbered step.
3. **Delete** the now-obsolete package globals `realEntries` and `realDirs` (their knowledge moves into the layouts). Keep `alwaysSkip` and `ProfileMetaFilename`. (`alwaysSkip`'s `"orch-homes"` entry is now largely redundant with `caamShadowTops()` (Phase 3.5), which shadows the actual base dir; keep it as conservative legacy behavior, but note it will also hide an unrelated real `~/orch-homes` even when the base is elsewhere — acceptable and documented.)

### Phase 2 — Provider in metadata (`internal/shallow/shallow.go`)

Add `Provider` to `Meta`:

```go
type Meta struct {
	Name           string    `json:"name"`
	Provider       string    `json:"provider"`            // always set on write
	CreatedAt      time.Time `json:"created_at"`
	CredentialFrom string    `json:"credential_from,omitempty"`
	RealHome       string    `json:"real_home"`
	Version        int       `json:"version"`
}
```

- On **write** (`Create`): set `Provider` to the normalized provider; set `Version: 2`. (`Version` simply stamps the current schema — it is not a migration marker and the reader never branches on it.)
- On **read** (`readMeta`): **unmarshal only — do NOT invent, default, or validate the provider.** `readMeta` stays exactly as today plus the new `Provider` field deserializing automatically:

  ```go
  func readMeta(home string) (*Meta, error) {
  	data, err := os.ReadFile(filepath.Join(home, ProfileMetaFilename))
  	if err != nil { return nil, err }
  	var m Meta
  	if err := json.Unmarshal(data, &m); err != nil { return nil, err }
  	return &m, nil // Provider is taken verbatim; may be "" or unknown for a hand-edited sidecar
  }
  ```

  **Why `readMeta` neither defaults nor validates:** per the no-compat rule, a profile with no/unknown provider is *malformed*, not *legacy Claude* — so we must not silently call it Claude (a Codex profile mis-run as Claude would skip `CODEX_HOME` and leak; see Phase 7). And `readMeta` must not *error* on a bad provider either: `List()`/`Get()` swallow `readMeta` errors (`if meta, err := readMeta(home); err == nil { p.Meta = meta }`), so erroring would make a hand-corrupted profile **vanish from `list`**. The resolution: `readMeta` keeps the raw value verbatim (possibly `""`); the **strict `LayoutForProvider`** (§5.4) is where an empty/unknown provider becomes a clear, actionable error — at `shallow-spawn`/`CredentialPath` time. `list` displays the raw provider (or `—` when empty) without guessing (Phase 9). This is the split that lets a malformed profile still be *listed* and *deleted*, while *spawn* refuses it loudly.

### Phase 3 — Refactor `Manager.Create` around layouts (`internal/shallow/shallow.go`)

Extend options and rewrite `Create` into a generic, layout-driven flow:

```go
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

	CredentialFromLabel string
	Force               bool
}

func (m *Manager) Create(name string, opts CreateOptions) (retHome string, retErr error) {
	provider, err := NormalizeProvider(opts.Provider) // user-input default: "" → claude
	if err != nil { return "", err }
	opts.Provider = provider // normalize before handing opts to layout hooks
	layout, err := LayoutForProvider(provider)
	if err != nil { return "", err }

	if opts.SourceClaudeJSON != "" && provider != ProviderClaude {
		return "", fmt.Errorf("--from-claude-json is only valid for provider claude (got %q)", provider)
	}
	if opts.CredentialSourceDir != "" && opts.CredentialSource != "" {
		// Engine-level guard (the CLI also makes --from-vault/--from-file mutually
		// exclusive). Manager.Create is a package API; bad callers must fail loudly.
		return "", fmt.Errorf("CredentialSourceDir and CredentialSource are mutually exclusive")
	}

	home, err := m.HomeFor(name)
	if err != nil { return "", err }

	// Existing-dir + --force handling — HARDENED by Phase 3.5: the --force RemoveAll
	// must only delete a genuine shallow profile, never an arbitrary path reached via
	// a symlinked base:
	//   if _, err := os.Lstat(home); err == nil {
	//       if !opts.Force { return "", fmt.Errorf("shallow profile %q already exists ...", name, home) }
	//       if err := m.assertIsShallowProfile(home); err != nil { return "", err } // Phase 3.5 guard
	//       if err := os.RemoveAll(home); err != nil { return "", ... }
	//   } else if !errors.Is(err, os.ErrNotExist) { return "", ... }

	if err := os.MkdirAll(home, 0o700); err != nil { return "", fmt.Errorf("create profile dir: %w", err) }

	// Transactional hygiene (NOT backwards-compat): once we own a freshly-created
	// (or just --force-cleared) `home`, remove the half-built profile on ANY later
	// error, so a failed create never leaves a meta-less directory that blocks
	// re-create and that `shallow-spawn` can't classify. The "exists && !force"
	// early-return above happens BEFORE this defer is armed, so we never delete a
	// profile we didn't just create.
	defer func() {
		if retErr != nil { _ = os.RemoveAll(home) }
	}()

	for _, d := range layout.RealDirs { // MkdirAll handles nested parents
		if err := os.MkdirAll(filepath.Join(home, filepath.FromSlash(d)), 0o700); err != nil {
			return "", fmt.Errorf("create real dir %s: %w", d, err)
		}
	}

	if err := m.populateSymlinks(home, layout); err != nil { return "", fmt.Errorf("populate symlinks: %w", err) }
	for _, root := range layout.InnerSymlinkRoots {
		if err := m.populateInnerSymlinks(home, layout, root); err != nil {
			return "", fmt.Errorf("populate %s symlinks: %w", root, err)
		}
	}

	if err := m.provisionCredentials(home, layout, opts); err != nil { return "", err }

	if layout.CreateManagedFiles != nil {
		if err := layout.CreateManagedFiles(m, home, opts); err != nil { return "", err }
	}

	// Post-create invariant: every real file the layout declares (RealFiles ∪ the
	// credential dests, i.e. realFileSet minus the meta sidecar) must now exist as a
	// REAL regular file with no symlinked ancestor inside the profile. Catches a
	// CreateManagedFiles hook that forgot to write a declared RealFile, and any
	// write-through-symlink that slipped past validateLayout.
	// optionalDest = slash-form dests of NON-required credentials (may be absent
	// when not present in the vault). Everything else in realFileSet must exist.
	optionalDest := map[string]bool{}
	for _, c := range layout.Credentials {
		if !c.Primary && !c.Required { optionalDest[filepath.ToSlash(c.DestRel)] = true }
	}
	for f := range layout.realFileSet() {
		if f == ProfileMetaFilename { continue } // written just below
		abs := filepath.Join(home, filepath.FromSlash(f))
		if err := assertNoSymlinkAncestor(home, abs); err != nil { return "", fmt.Errorf("post-create %s: %w", f, err) }
		st, err := os.Lstat(abs)
		if errors.Is(err, os.ErrNotExist) {
			if optionalDest[f] { continue } // optional + absent = fine
			return "", fmt.Errorf("post-create: required managed file %s was not created", f)
		}
		if err != nil || st.Mode()&os.ModeSymlink != 0 || !st.Mode().IsRegular() {
			return "", fmt.Errorf("post-create: managed file %s is not a regular file", f)
		}
	}

	meta := Meta{Name: name, Provider: provider, CreatedAt: time.Now().UTC(),
		CredentialFrom: opts.CredentialFromLabel, RealHome: m.realHome, Version: 2}
	if err := writeMeta(home, &meta); err != nil { return "", fmt.Errorf("write metadata: %w", err) }
	return home, nil
}
```

> The named return `(retHome string, retErr error)` is what lets the deferred cleanup see whether the function failed. Don't drop it.

Rewrite the two symlink helpers to be layout-driven and **nested-aware**:

```go
func (m *Manager) populateSymlinks(home string, layout Layout) error {
	entries, err := os.ReadDir(m.realHome)
	if err != nil { return fmt.Errorf("read real home %s: %w", m.realHome, err) }

	skip := map[string]bool{}
	for f := range layout.realFileSet() { skip[slashTop(f)] = true } // top component of each real file
	for _, d := range layout.RealDirs    { skip[slashTop(d)] = true }
	for k := range alwaysSkip            { skip[k] = true }
	// Shadow CAAM-owned roots (the shallow base, $CAAM_HOME, and the vault root)
	// when they nest under realHome — so no shallow HOME can reach the vault or
	// another profile. caamShadowTops() subsumes the old base-dir nesting guard.
	// See Phase 3.5.
	for t := range m.caamShadowTops() { skip[t] = true }

	for _, e := range entries {
		if skip[e.Name()] { continue }
		src := filepath.Join(m.realHome, e.Name())
		dst := filepath.Join(home, e.Name())
		if _, err := os.Lstat(src); err != nil {
			if errors.Is(err, os.ErrNotExist) { continue }
			return fmt.Errorf("stat source %s: %w", src, err)
		}
		if err := atomicSymlink(src, dst); err != nil { return fmt.Errorf("symlink %s -> %s: %w", dst, src, err) }
	}
	return nil
}

// populateInnerSymlinks symlinks the children of ~/<root> into <home>/<root>.
// Two modes per root:
//   - ALLOW-list (layout.InnerSymlinkAllow[root] set): symlink ONLY those basenames.
//   - DENY-list (default): symlink everything EXCEPT this layout's real files/dirs
//     under <root> and InnerSkip[root].
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
		if errors.Is(err, os.ErrNotExist) || errors.Is(err, syscall.ENOTDIR) { return nil }
		return fmt.Errorf("stat %s: %w", srcDir, err)
	}
	if !st.IsDir() { return nil } // real ~/<root> is a FILE, not a dir → nothing to mirror

	// Real files/dirs of this layout under <root> are never symlinked (their
	// presence as real entries is the whole point).
	reserved := map[string]bool{}
	for f := range layout.realFileSet() {
		if slashDir(f) == root { reserved[slashBase(f)] = true }
	}
	for _, d := range layout.RealDirs {
		d = filepath.ToSlash(d)
		if slashDir(d) == root { reserved[slashBase(d)] = true }
	}

	entries, err := os.ReadDir(srcDir)
	if err != nil { return fmt.Errorf("read %s: %w", srcDir, err) }

	// link mirrors the top-level farm's vanished-source guard: if the entry
	// disappeared between ReadDir and here, skip it instead of creating a dangling
	// symlink.
	link := func(n string) error {
		src := filepath.Join(srcDir, n)
		dst := filepath.Join(dstDir, n)
		if _, err := os.Lstat(src); err != nil {
			if errors.Is(err, os.ErrNotExist) { return nil }
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
		for _, n := range allow { allowed[n] = true }
		for _, e := range entries {
			n := e.Name()
			if !allowed[n] || reserved[n] { continue }
			if err := link(n); err != nil { return err }
		}
		return nil
	}

	// DENY-list mode (default).
	skip := map[string]bool{}
	for k := range reserved { skip[k] = true }
	for _, n := range layout.InnerSkip[root] { skip[n] = true }
	for _, e := range entries {
		n := e.Name()
		if skip[n] { continue }
		if err := link(n); err != nil { return err }
	}
	return nil
}
```

Make `CredentialPath` provider-aware and **strict** (only tests call it; no production caller). It resolves the layout from recorded metadata via the strict `LayoutForProvider`, so a missing/unknown provider is a clear error rather than a silent Claude default:

```go
// CredentialPath returns the absolute path to a profile's PRIMARY credential
// file, resolved from the profile's recorded provider layout.
func (m *Manager) CredentialPath(name string) (string, error) {
	prof, err := m.Get(name)
	if err != nil { return "", err }
	if prof.Meta == nil {
		return "", fmt.Errorf("shallow profile %q has no readable metadata", name)
	}
	layout, err := LayoutForProvider(prof.Meta.Provider) // strict: empty/unknown → error
	if err != nil { return "", err }
	home, err := m.HomeFor(name)
	if err != nil { return "", err }
	return filepath.Join(home, filepath.FromSlash(layout.Primary().DestRel)), nil
}
```

### Phase 3.5 — Isolation hardening (CORE: these close real auth-exposure / data-loss holes)

The symlink farm and the destructive ops have four exploitable gaps (found by adversarial review). These are **core** for an auth-isolation feature — not optional polish.

**(1) Shadow CAAM's OWN data (vault + `CAAM_HOME`) — the worst leak.** The vault holds *every* account's credentials. With no `CAAM_HOME`, it defaults to `~/.local/share/caam/vault`; under the default base `~/orch-homes`, the farm would symlink `.local -> ~/.local`, so a process spawned in *any* shallow HOME could read `$HOME/.local/share/caam/vault/<tool>/<other>/auth.json` — **all accounts, from every profile.** Fix: the farm must never expose a CAAM-owned root. Collect the CAAM-owned absolute paths — the **vault root** (`authfile.DefaultVaultPath()` or the configured vault `BasePath()`), **`$CAAM_HOME`** if set, and the **base dir** itself — and for each that nests under `realHome`, add its **top-level component** to the `populateSymlinks` skip set. (The base-dir skip already exists; generalize it to all three.) This is conservative — it shadows the whole top dir (e.g. all of `.local`) rather than just `.local/share/caam` — which is **safe** (no leak) at the cost of not sharing siblings under that top dir. Document it, and recommend operators set `CAAM_HOME` *outside* `~` (e.g. `/var/lib/caam`) so nothing under `~` is shadowed. (Precise nested-shadowing that preserves sibling sharing is the refinement in Appendix D.2.)

```go
// In populateSymlinks, build the skip set from CAAM-owned roots too. Each root is
// CANONICALIZED (a symlinked CAAM_HOME like /tmp/ch -> ~/real-caam must still be
// caught) and matched both lexically and canonically against realHome.
func (m *Manager) caamShadowTops() map[string]bool {
	tops := map[string]bool{}
	// Match nesting against BOTH the lexical realHome and its canonical form (a
	// symlinked realHome component could otherwise hide the vault's nesting).
	realHomes := []string{m.realHome}
	if crh, err := resolveExistingSymlinks(m.realHome); err == nil && crh != m.realHome {
		realHomes = append(realHomes, crh)
	}
	addOne := func(p string) {
		if p == "" { return }
		abs, err := filepath.Abs(p)
		if err != nil { return }
		for _, rh := range realHomes {
			if rel, err := filepath.Rel(rh, abs); err == nil &&
				rel != ".." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
				if t := slashTop(rel); t != "" && t != "." { tops[t] = true }
			}
		}
	}
	add := func(p string) { // match both the lexical path and its symlink-resolved form
		addOne(p)
		if p != "" { if c, err := resolveExistingSymlinks(p); err == nil { addOne(c) } }
	}
	add(m.baseDir); add(m.canonBase)   // the shallow base (lexical + canonical)
	add(os.Getenv("CAAM_HOME"))
	add(m.vaultRoot)                   // configured vault root; defaulted in NewManager (never "")
	return tops
}
```

> **New `Manager` fields + exported setter (compile-safe):** add `canonBase string` and `vaultRoot string` (both unexported). `NewManager` defaults `vaultRoot = authfile.DefaultVaultPath()` so the vault is shadowed even for direct `shallow.NewManager()` callers (the `internal/shallow` → `internal/authfile` import is cycle-free; a small locator helper, per §5.6). Because `cmd` cannot write an unexported field, add an **exported setter** the CLI calls:
> ```go
> // SetVaultRoot overrides the vault root that the manager shadows from shallow HOMEs.
> func (m *Manager) SetVaultRoot(p string) { if p != "" { m.vaultRoot = p } }
> ```
> Then `resolveShallowManager` does `mgr.SetVaultRoot(vault.BasePath())` after `NewManager` (the package-level `vault` is an `*authfile.Vault`; `func (v *Vault) BasePath() string { return v.basePath }`). Engine tests call `SetVaultRoot(tempVault)`. This shadows BOTH the configured case (vault under `$CAAM_HOME/data/vault` — also covered by the `CAAM_HOME` add) AND the no-`CAAM_HOME` default (`~/.local/share/caam/vault`, whose top `.local` must be shadowed). `vaultRoot` is never `""` after `NewManager`.

**(2) Reject a symlinked PROFILE PATH (`alice -> bob` hijack).** `Get` uses `os.Stat(home)` (follows symlinks). So `ln -s $BASE/bob $BASE/alice; shallow-spawn alice -- codex` makes `Get("alice")` read **bob's** metadata and spawn sets `CODEX_HOME=$BASE/alice/.codex` → resolves into bob's profile: alice becomes bob. Fix: `Get` (and `Delete`, `Create`'s existing-dir check) must `os.Lstat` the profile path and **reject a leaf symlink**:

```go
// In Get (and the existing-dir branch of Create, and Delete): after computing home,
st, err := os.Lstat(home)
if err != nil { return ... }
if st.Mode()&os.ModeSymlink != 0 {
	return ... fmt.Errorf("shallow profile %q is a symlink; refusing (potential identity hijack)", name)
}
if !st.IsDir() { return ... }
```

**(3) Destructive ops only on real shallow profiles.** `Create --force` and `Delete` do `os.RemoveAll(home)`. With a symlinked base aliasing into real data (`--base /tmp/b`, `/tmp/b -> ~/.ssh`, `create known_hosts --force`), `home` resolves to `~/.ssh/known_hosts` and `RemoveAll` **deletes the real file.** The equality-only base check (below) doesn't catch this (the base resolves to `~/.ssh`, not `realHome`). Fix: never `RemoveAll` a path that isn't a genuine shallow profile. Before any `RemoveAll(home)` in `--force`/`Delete`, require `home` to be a real directory (not symlink, per (2)) **containing `.caam-shallow.json`** — otherwise refuse:

```go
func (m *Manager) assertIsShallowProfile(home string) error {
	st, err := os.Lstat(home) // Lstat — a symlinked profile path is never a profile
	if err != nil { return err }
	if st.Mode()&os.ModeSymlink != 0 || !st.IsDir() {
		return fmt.Errorf("%s is not a shallow profile directory", home)
	}
	// The sidecar must be a REAL regular file (Lstat, not Stat — a symlinked
	// sidecar pointing at some real file elsewhere must not qualify).
	mst, err := os.Lstat(filepath.Join(home, ProfileMetaFilename))
	if err != nil || mst.Mode()&os.ModeSymlink != 0 || !mst.Mode().IsRegular() {
		return fmt.Errorf("%s has no real %s; refusing to remove (not a shallow profile)", home, ProfileMetaFilename)
	}
	return nil
}
```
So `Create --force` and `Delete` only remove a dir that has a real `.caam-shallow.json` (never an arbitrary real-home child reached via a symlinked base).

> **Consequence — resolve the "malformed is deletable" tension:** a profile whose `.caam-shallow.json` is **missing** is *listable* but **not** auto-removable by `delete`/`--force` (the guard can't distinguish it from a real-home dir reached via a symlinked base, and refusing is the safe choice). Document that a metadata-less directory must be removed manually (`rm -rf <base>/<name>`). A profile with *present-but-malformed* metadata (empty/unknown provider) **is** deletable (the sidecar exists). Update Phase 2/§9 wording to match: "listable; deletable only if the sidecar file exists."

**(4) Canonical base in the farm skip + the base==realHome reject.** Add `canonBase string` and `vaultRoot string` to the `Manager` struct. In `NewManager`, compute the canonical base, reject a base that resolves to realHome, and populate the new fields in the returned struct literal (the current `NewManager` ends with `return &Manager{baseDir: abs, realHome: rabs}` — replace that tail):

```go
// ... (existing NewManager body: resolve realHome, baseDir → abs/rabs, reject abs==rabs) ...
canonBase := abs
if cb, err := resolveExistingSymlinks(abs); err == nil {
	rhome := rabs
	if rh, err := resolveExistingSymlinks(rabs); err == nil { rhome = rh }
	if cb == rhome {
		return nil, fmt.Errorf("shallow base dir resolves (via symlink) to the real home: %s", cb)
	}
	canonBase = cb
}
return &Manager{baseDir: abs, realHome: rabs, canonBase: canonBase, vaultRoot: authfile.DefaultVaultPath()}, nil
```
(`caamShadowTops()` reads `m.canonBase` and `m.vaultRoot`; `resolveShallowManager` overrides `vaultRoot` with `vault.BasePath()`.)

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
			for i := len(tail) - 1; i >= 0; i-- { resolved = filepath.Join(resolved, tail[i]) }
			return resolved, nil
		} else if !errors.Is(err, os.ErrNotExist) {
			return "", err
		}
		parent := filepath.Dir(cur)
		if parent == cur { return "", fmt.Errorf("no existing ancestor for %s", p) }
		tail = append(tail, filepath.Base(cur))
		cur = parent
	}
}
```

The base-reject stays **equality-only** (`canonBase == canonRealHome`) — don't reject "nested under realHome" (the default `~/orch-homes` is intentionally nested). The leak cases that equality misses ((1),(3),(4)) are closed by shadowing CAAM roots, the destructive-op guard, and the canonical-base skip — not by broadening the reject.

Phase-10 tests for all four: `TestVaultNotExposedInShallowHome` (no `CAAM_HOME` and `CAAM_HOME=~/.caam`: assert no symlink path inside the profile resolves to the vault); `TestRejectsSymlinkedProfilePath` (`alice -> bob` → `Get`/spawn/delete refuse); `TestForceAndDeleteRefuseNonProfileDir` (`--base /tmp/b → ~/.ssh`, `create known_hosts --force` must error and **not** delete `~/.ssh/known_hosts`); `TestCanonicalBaseSkipsProfileTree` (`--base /tmp/b → ~/profiles` → no `$SHALLOW_HOME/profiles` symlink); plus `TestRejectsSymlinkedBaseAliasingRealHome` (base symlink → realHome rejected).

### Phase 4 — Generic credential provisioning (`internal/shallow/shallow.go`)

```go
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
		// silently leave other required artifacts absent. (Claude/Codex/Antigravity
		// each have exactly one Required credential, so this never triggers for them;
		// it's a guard against a future multi-required layout.)
		required := 0
		for _, c := range layout.Credentials { if c.Required { required++ } }
		if required > 1 {
			return fmt.Errorf("--from-file is not supported for provider %q (it has multiple required credentials); use --from-vault", layout.Provider)
		}
		if err := copyFileMode(opts.CredentialSource, primDst, 0o600); err != nil {
			return fmt.Errorf("copy credentials: %w", err)
		}
	default:
		// No source: write only an empty Primary placeholder. Reject this for a
		// layout with MORE THAN ONE required credential — an empty-create can't
		// satisfy multiple required artifacts (Claude/Codex/agy have exactly one
		// required, so this never triggers for them; it's a future-layout guard).
		required := 0
		for _, c := range layout.Credentials { if c.Required { required++ } }
		if required > 1 {
			return fmt.Errorf("provider %q requires multiple credentials; create with --from-vault (empty create unsupported)", layout.Provider)
		}
		if err := writeFileAtomic(primDst, []byte(""), 0o600); err != nil {
			return fmt.Errorf("write empty credentials: %w", err)
		}
	}
	return nil
}
```

> Note: for the **vault** path the CLI passes both `CredentialSourceDir` *and* a `CredentialFromLabel` like `vault:codex/bob`, so the "missing file" error reads naturally. The CLI also pre-checks the Primary exists before calling `Create` (Appendix C / Phase 8) to fail fast with a friendly message; the engine-level check above is the backstop. The `c.Primary || c.Required` condition is belt-and-suspenders — `validateLayout` already forces the Primary to be Required, so the Primary is always covered.

### Phase 5 — Claude managed files (`internal/shallow/shallow.go`)

Extract today's Claude-specific real-file writes (lock + `.claude.json`) — **not** the credential copy (that's generic now):

```go
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
```

### Phase 6 — Codex managed files (`internal/shallow/shallow.go`)

Add the import now (first use): `codexprovider "github.com/Dicklesworthstone/coding_agent_account_manager/internal/provider/codex"`.

```go
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
```

> **Trade-off (documented):** a fresh minimal config means the user's *non-auth* Codex settings (e.g. `model`) are NOT carried into the shallow session. That's the safe default — preserving them requires TOML sanitization (strip/override `log_dir`, `sqlite_home`, `model_providers.*.env_key`, command-backed auth), which is deferred to **Appendix D.4**. The `CODEX_SQLITE_HOME` env we set (§5.5) further pins sqlite state to the shallow `.codex` regardless of config. Update the Phase-10 codex tests accordingly: assert the shallow `config.toml` is a real `0600` file containing `cli_auth_credentials_store = "file"` and does **not** contain a copied `log_dir`/`sqlite_home`/`model_providers` from the real config. (The single-quote duplicate-key regression test still applies at the `internal/provider/codex` unit level, since `EnsureFileCredentialStore` is still the writer.)

> `EnsureFileCredentialStore` (verbatim body in §4.3) writes `config.toml` at mode `0600` in the branches that **write** (create / replace-line / append). The already-`"file"` branch returns *without* writing or chmodding — but on the shallow-create path that's still `0600`, because `createCodexManagedFiles` copied the real config with `copyFileMode(..., 0o600)` *before* calling it (and a freshly-created config is `0600`). So after this, `.codex/config.toml` is a real `0600` file containing `cli_auth_credentials_store = "file"`, preserving pre-existing keys. (A standalone provider-level test of `EnsureFileCredentialStore` must seed the file at `0600` itself to assert mode, since the no-op branch doesn't set it.) Ordering is correct: `provisionCredentials` writes `auth.json` *before* this runs.

> **Security-critical fix — part of the core patch, not optional:** `EnsureFileCredentialStore`'s detector regex (`codex.go`) only matches a **double-quoted** value:
> ```go
> var codexCredentialsStoreRe = regexp.MustCompile(`(?m)^\s*cli_auth_credentials_store\s*=\s*\"[^\"]*\"`)
> ```
> A valid TOML config with a **single-quoted** value — `cli_auth_credentials_store = 'keyring'` — does **not** match, so the function *appends* a second `cli_auth_credentials_store = "file"` line. **Defining the same bare key twice is invalid TOML**, so a spec-compliant parser (Codex is Rust, almost certainly the `toml` crate) will **error out** rather than take the last value — i.e. the un-fixed path can break Codex's config entirely, not merely look ugly. Broaden the regex to accept both quote styles:
> ```go
> var codexCredentialsStoreRe = regexp.MustCompile(`(?m)^\s*cli_auth_credentials_store\s*=\s*(?:"[^"]*"|'[^']*')`)
> ```
> Add the Phase-10 test (real config seeded with `cli_auth_credentials_store = 'keyring'` → shallow config has exactly **one** such key, value `"file"`, and preserves other keys). **Caveat:** the broadened regex fixes the single-quoted/other-value case, but `ReplaceAll` rewrites *every* matching line — it does not collapse a config that *already* has duplicate `cli_auth_credentials_store` keys into one. That input is already invalid TOML; if you want a hard "exactly one" guarantee, rewrite the first match and drop subsequent matches (or do a TOML-aware rewrite). The minimal regex fix covers the realistic cases; the duplicate-pre-existing-key case is out of scope for the regex approach — state that in the test. (This lives in `internal/provider/codex/codex.go`; it's a small, safe hardening the shallow feature depends on.)

### Phase 7 — Provider-aware `shallow-spawn` (`cmd/caam/cmd/shallow.go`)

Rewrite `runShallowSpawn` to (a) resolve the provider **strictly** from metadata and (b) drive the environment entirely from the layout's `SpawnEnv`/`SpawnEnvLines` (§5.3):

```go
func runShallowSpawn(cmd *cobra.Command, args []string) error {
	name := args[0]
	rest := args[1:]
	printEnv, _ := cmd.Flags().GetBool("print-env")

	mgr, err := resolveShallowManager(cmd)
	if err != nil { return fmt.Errorf("init shallow manager: %w", err) }

	prof, err := mgr.Get(name)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("shallow profile %q does not exist (try `caam shallow-profile create %s`)", name, name)
		}
		return fmt.Errorf("load shallow profile: %w", err)
	}

	// STRICT: a profile with missing/unreadable metadata (Meta == nil) or no
	// recorded provider is malformed — refuse, never silently assume Claude. A
	// Codex profile mis-run as Claude would skip CODEX_HOME and read the real
	// ~/.codex auth. Emit spawn-specific messages (don't just wrap the §5.4
	// LayoutForProvider error — its wording differs and the catalog/tests depend
	// on the exact spawn phrasing).
	if prof.Meta == nil || prof.Meta.Provider == "" {
		return fmt.Errorf("shallow profile %q has no recorded provider (missing or malformed metadata); recreate it", name)
	}
	layout, err := shallow.LayoutForProvider(prof.Meta.Provider) // strict: empty/unknown → error
	if err != nil {
		return fmt.Errorf("shallow profile %q uses unsupported provider %q (supported: %s)",
			name, prof.Meta.Provider, strings.Join(shallow.SupportedProviders(), ", "))
	}

	if printEnv {
		// SpawnEnvLines is the single source of truth shared with the exec path:
		// KEY=VALUE for HOME/SHALLOW_PROFILE/provider-sets, then `unset KEY` for every
		// repointing var that's cleared — so a shell wrapper built from --print-env
		// reproduces the exec path's isolation (otherwise it would re-leak).
		for _, line := range layout.SpawnEnvLines(prof.Path, name) {
			fmt.Fprintln(cmd.OutOrStdout(), line)
		}
		return nil
	}

	if len(rest) == 0 {
		return fmt.Errorf("missing command after %q (use `caam shallow-spawn %s -- %s`)", name, name, layout.DefaultBin)
	}

	// Cheap pre-spawn integrity check (cooperative-isolation hardening, §2.2): a
	// managed credential/policy file must not have been swapped for a symlink (which
	// could repoint the session at another identity's auth), and each REQUIRED
	// credential must actually be present as a regular file.
	for _, p := range layout.ManagedFilePaths(prof.Path) { // managed files that exist
		if st, err := os.Lstat(p); err == nil && st.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("shallow profile %q has a symlinked managed file %s; refusing (potential identity leak)", name, p)
		}
	}
	for _, c := range layout.Credentials {
		if !c.Required { continue }
		cp := filepath.Join(prof.Path, filepath.FromSlash(c.DestRel))
		// No symlinked ANCESTOR inside the profile (a swapped <home>/.codex symlink
		// would make Lstat(cp) follow it to the real file): walk cp's ancestors down
		// from prof.Path and Lstat each.
		if err := assertNoSymlinkAncestor(prof.Path, cp); err != nil {
			return fmt.Errorf("shallow profile %q: %w", name, err)
		}
		st, err := os.Lstat(cp)
		if err != nil || st.Mode()&os.ModeSymlink != 0 || !st.Mode().IsRegular() {
			return fmt.Errorf("shallow profile %q is missing or has a non-regular credential %s; recreate it", name, c.DestRel)
		}
	}

	// assertNoSymlinkAncestor Lstats each path component of abs strictly below root;
	// any symlink ancestor is rejected. (root itself was already checked non-symlink
	// by Phase 3.5 (2).) Defined once, reused by the create-time invariant test too.
	//   func assertNoSymlinkAncestor(root, abs string) error {
	//       rel, err := filepath.Rel(root, abs); if err != nil { return err }
	//       cur := root
	//       for _, seg := range strings.Split(filepath.ToSlash(rel), "/") {
	//           cur = filepath.Join(cur, seg)
	//           if cur == abs { break } // don't re-check the leaf here
	//           st, err := os.Lstat(cur)
	//           if err != nil { if errors.Is(err, os.ErrNotExist) { return nil }; return err }
	//           if st.Mode()&os.ModeSymlink != 0 { return fmt.Errorf("symlinked ancestor %s", cur) }
	//       }
	//       return nil
	//   }

	binPath, err := exec.LookPath(rest[0])
	if err != nil { return fmt.Errorf("lookup %q: %w", rest[0], err) }

	envMap := make(map[string]string, len(os.Environ())+4)
	for _, e := range os.Environ() {
		if idx := strings.IndexByte(e, '='); idx > 0 { envMap[e[:idx]] = e[idx+1:] }
	}
	layout.SpawnEnv(prof.Path, name, envMap) // deletes all repointing vars, sets HOME/SHALLOW_PROFILE + provider sets
	envSlice := make([]string, 0, len(envMap))
	for k, v := range envMap { envSlice = append(envSlice, k+"="+v) }
	return spawnExec(binPath, rest, envSlice)
}
```

Notes:
- `--print-env` emits shell-quoted `export KEY='…'` for set vars plus `unset KEY` for cleared vars (§5.3 `SpawnEnvLines`). This is deliberate: the flag is "for shell wrappers," which must `export` (so children inherit) and `unset` the cleared vars (or they silently re-leak), with values quoted against an exotic `--base`. The issue's acceptance (`HOME`, `SHALLOW_PROFILE`, `CODEX_HOME` present for codex) holds — they appear as `export HOME='…'` etc. The **exec** form (`-- cmd`) remains the primary, injection-free path.
- **Do not** call `checkCodexDaemon` from `shallow-spawn`. That guard is for in-place auth swaps (`activate`/`next`), where a daemon caches the *old* auth. Shallow Codex sessions avoid the real daemon by setting a distinct `CODEX_HOME` and by the allow-list keeping any real daemon control dir out of the shallow `.codex`; that is the correct guard here.

### Phase 8 — `shallow-profile create` CLI (`cmd/caam/cmd/shallow.go`)

1. **Add the `--tool` flag — in BOTH places:**
   - real `init()` — **derive the supported list from the registry** so it never goes stale when a harness is added (package-var init runs before `init()`, so `shallow.SupportedProviders()` is ready):
     ```go
     shallowProfileCreateCmd.Flags().String("tool", "",
         fmt.Sprintf("shallow provider/harness (supported: %s; default claude)",
             strings.Join(shallow.SupportedProviders(), ", ")))
     ```
   - the test clone `newShallowTestRoot()` in `cmd/caam/cmd/shallow_test.go`:
     ```go
     create.Flags().String("tool", "", "")
     ```
2. Add provider-inference + generalized vault resolution helpers:

```go
type shallowVaultRef struct{ Tool, Profile string }

func parseShallowVaultRef(spec string) (shallowVaultRef, error) {
	spec = strings.TrimSpace(spec)
	parts := strings.SplitN(spec, "/", 2)
	if len(parts) != 2 || strings.TrimSpace(parts[0]) == "" || strings.TrimSpace(parts[1]) == "" {
		return shallowVaultRef{}, fmt.Errorf("--from-vault must be in the form <tool>/<profile>, got %q", spec)
	}
	tool := strings.ToLower(strings.TrimSpace(parts[0]))
	profile := strings.TrimSpace(parts[1])
	// The profile is joined into a vault path (vault.ProfilePath), so reject
	// traversal/separators that could escape the profile leaf, but allow legitimate
	// names like "v1..2" / "alice@example.com": forbid only path separators and the
	// exact dot names. (No "/" or "\" → filepath.Join keeps it a single component.)
	if profile == "." || profile == ".." || strings.ContainsAny(profile, `/\`) {
		return shallowVaultRef{}, fmt.Errorf("--from-vault profile %q is invalid (no path separators or '.'/'..')", profile)
	}
	return shallowVaultRef{Tool: tool, Profile: profile}, nil
}

// inferShallowProvider resolves the provider id per the §6 rules and validates
// that --tool agrees with --from-vault. It is the SINGLE provider-resolution
// path for `create` (no caller re-derives the provider), and it emits the
// CLI-flavored error strings from Appendix C.
func inferShallowProvider(toolFlag, fromVault string) (string, error) {
	toolFlag = strings.ToLower(strings.TrimSpace(toolFlag))
	if fromVault != "" {
		ref, err := parseShallowVaultRef(fromVault)
		if err != nil { return "", err }
		if toolFlag != "" && toolFlag != ref.Tool {
			return "", fmt.Errorf("--tool %q does not match --from-vault tool %q", toolFlag, ref.Tool)
		}
		p, err := shallow.NormalizeProvider(ref.Tool)
		if err != nil {
			return "", fmt.Errorf("--from-vault tool %q is not supported for shallow profiles (supported: %s)",
				ref.Tool, strings.Join(shallow.SupportedProviders(), ", "))
		}
		return p, nil
	}
	if toolFlag == "" { return shallow.ProviderClaude, nil } // ergonomic default
	// CLI-flavored unsupported-tool error (don't surface the engine-style string).
	p, err := shallow.NormalizeProvider(toolFlag)
	if err != nil {
		return "", fmt.Errorf("--tool %q is not supported for shallow profiles (supported: %s)",
			toolFlag, strings.Join(shallow.SupportedProviders(), ", "))
	}
	return p, nil
}

// resolveShallowVaultDir returns the vault profile DIR + descriptive label for an
// ALREADY-resolved provider, after verifying the provider's Primary credential is
// present in the vault. (Provider/mismatch/unsupported errors are handled earlier
// by inferShallowProvider, so this function never re-derives the provider.)
func resolveShallowVaultDir(providerID string, ref shallowVaultRef) (sourceDir, label string, err error) {
	if vault == nil { return "", "", fmt.Errorf("vault not initialized") }
	if !strings.EqualFold(ref.Tool, providerID) { // defensive: caller must pass matching pair
		return "", "", fmt.Errorf("internal: provider %q != vault tool %q", providerID, ref.Tool)
	}
	dir := vault.ProfilePath(ref.Tool, ref.Profile)
	layout, err := shallow.LayoutForProvider(providerID)
	if err != nil { return "", "", err } // providerID is pre-resolved, but fail loudly if not
	primary := layout.Primary()
	if _, err := os.Stat(filepath.Join(dir, primary.VaultName)); err != nil {
		return "", "", fmt.Errorf("vault profile %s/%s missing %s: %w", ref.Tool, ref.Profile, primary.VaultName, err)
	}
	return dir, "vault:" + ref.Tool + "/" + ref.Profile, nil
}
```

> **Naming:** in package `cmd`, the identifier `provider` is the imported `internal/provider` package. So name the local provider-id variable **`providerID`** (not `provider`) in these CLI functions to avoid shadowing the package. (Inside `internal/shallow` there is no such import, so a local `provider` variable there is fine.)
> `Layout.Primary()` and `Layout.ManagedFilePaths()` are defined once in §5.3; the CLI reads `layout.Primary().VaultName`.

3. Rewrite `runShallowProfileCreate` to thread the provider through a single resolution:

```go
tool, _ := cmd.Flags().GetString("tool")
// ... fromVault/fromFile mutual-exclusion check unchanged ...

providerID, err := inferShallowProvider(tool, fromVault) // §6 rules + mismatch + unsupported-tool errors
if err != nil { return emit(err) }
output.Provider = providerID // set early so JSON ERROR output also carries the resolved provider
if fromClaudeJSON != "" && providerID != shallow.ProviderClaude {
	return emit(fmt.Errorf("--from-claude-json is only valid for --tool claude (got %s)", providerID))
}

opts := shallow.CreateOptions{Provider: providerID, Force: force, SourceClaudeJSON: fromClaudeJSON}

switch {
case fromVault != "":
	ref, _ := parseShallowVaultRef(fromVault) // already validated inside inferShallowProvider
	dir, label, err := resolveShallowVaultDir(providerID, ref)
	if err != nil { return emit(err) }
	opts.CredentialSourceDir, opts.CredentialFromLabel = dir, label
case fromFile != "":
	abs, err := filepath.Abs(fromFile)
	if err != nil { return emit(fmt.Errorf("resolve --from-file: %w", err)) }
	st, err := os.Stat(abs)
	if err != nil { return emit(fmt.Errorf("--from-file: %w", err)) }
	if st.IsDir() { return emit(fmt.Errorf("--from-file: %s is a directory", abs)) }
	opts.CredentialSource, opts.CredentialFromLabel = abs, "file:"+abs
default:
	if !jsonOut {
		fmt.Fprintln(cmd.ErrOrStderr(), "note: no --from-vault/--from-file given; the credential file will be empty.")
		fmt.Fprintln(cmd.ErrOrStderr(), "      Populate it before running 'shallow-spawn' (e.g. by signing in inside the shallow HOME).")
	}
}

// Resolve the layout once for output (managed_files + the provider-correct next
// step). LayoutForProvider is safe here: providerID came from NormalizeProvider.
layout, err := shallow.LayoutForProvider(providerID)
if err != nil { return emit(err) }

home, err := mgr.Create(name, opts)
if err != nil { return emit(fmt.Errorf("create shallow profile: %w", err)) }
output.Provider = providerID                       // Phase 9
output.ManagedFiles = layout.ManagedFilePaths(home) // Phase 9 (JSON)
// human next-step hint uses layout.DefaultBin: `caam shallow-spawn <name> -- <bin>`
```

4. **Delete** the old Claude-only `resolveVaultCredential` (replaced by `inferShallowProvider` + `resolveShallowVaultDir`). Confirm nothing else references it: `grep -rn resolveVaultCredential`.

### Phase 9 — Output: provider in JSON + human (`cmd/caam/cmd/shallow.go`)

Add `Provider` (and optionally `ManagedFiles`) to create output and `Provider` to list output:

```go
type shallowCreateOutput struct {
	Success        bool     `json:"success"`
	Name           string   `json:"name"`
	Provider       string   `json:"provider"`
	Path           string   `json:"path"`
	CredentialFrom string   `json:"credential_from,omitempty"`
	ManagedFiles   []string `json:"managed_files,omitempty"`
	Error          string   `json:"error,omitempty"`
}

type shallowListItem struct {
	Name           string    `json:"name"`
	Provider       string    `json:"provider"`
	Path           string    `json:"path"`
	CredentialFrom string    `json:"credential_from,omitempty"`
	CreatedAt      time.Time `json:"created_at,omitempty"`
}
```

- `output.Provider` / `output.ManagedFiles` are set in Phase 8. For list items, never fall back to `claude`. Distinguish the two surfaces:
  - **JSON** `item.Provider = p.Meta.Provider` **verbatim** (so a malformed profile is `"provider":""` or `"provider":"codx"` — machine-readable truth; nil `Meta` → `""`). Don't substitute `—` in JSON.
  - **Human** table shows `—` for a blank/unknown provider: `prov := "—"; if p.Meta != nil && p.Meta.Provider != "" { prov = p.Meta.Provider }`.
  `delete` doesn't read the provider, so a profile with *present-but-malformed* provider metadata is listable and deletable. A profile whose sidecar is *entirely missing* is listable but **not** auto-deletable (Phase 3.5's destructive guard requires a real `.caam-shallow.json`); it must be removed manually. (This is the safe trade — see Phase 3.5 (3).)
- `ManagedFiles`: `layout.ManagedFilePaths(home)` (defined in §5.3) — sorted absolute paths of the real files that **actually exist** (so a multi-file harness's uncopied optional companions are omitted).
- Human `create` output: print `Provider: <providerID>` and the provider-correct next step using the layout's `DefaultBin`: `caam shallow-spawn <name> -- <layout.DefaultBin>` (claude→`claude`, codex→`codex`, agy→`agy`). Do not hardcode an id→bin map in the CLI.
- Human `list` output: add a `TOOL` column:
  ```
  NAME                    TOOL      CREDENTIALS                       CREATED
  alice                   claude    vault:claude/alice@example.com    2026-06-28 12:34
  cbob                    codex     vault:codex/bob@example.com       2026-06-28 12:35
  ```
- Empty-list hint stays provider-agnostic and lists the supported set from the registry (so it never goes stale):
  ```go
  fmt.Fprintln(out, "Create one with:")
  fmt.Fprintln(out, "  caam shallow-profile create <name> --from-vault <tool>/<profile>")
  fmt.Fprintf(out,  "Supported shallow tools: %s\n", strings.Join(shallow.SupportedProviders(), ", "))
  ```

### Phase 10 — Engine tests (`internal/shallow/shallow_test.go`)

Extend `fakeHome` to seed a realistic `~/.codex` (Claude isolation is unchanged from today, so `.config` stays a passthrough symlink — no `.config` changes here):
- `.codex/auth.json` (placeholder JSON); `.codex/config.toml` with `model = "gpt-5"\n`;
- `.codex/sessions/marker` and a `.codex/history.jsonl` file (the allow-listed shared state — prove they get symlinked);
- the **runtime/log/state artifacts the allow-list must keep out** (verify the exact names against the installed Codex): `.codex/log/marker` (Codex's default log dir is `$CODEX_HOME/log`, singular — and `logs/` if your version uses that), `.codex/app-server-daemon/marker`, `.codex/app-server-control/marker`, `.codex/state.sqlite`, and an unknown `.codex/run-1234/marker` — prove **none** of these is symlinked (the allow-list keeps everything-but-`{sessions,history.jsonl}` out regardless of name).

> **Note:** Claude shallow behavior is unchanged in the core plan, so `TestCreateBuildsSymlinkFarmAndRealAuthFiles` (which asserts `.config` is a symlink and the `.claude` invariants) stays **green as-is**. Any test constructing `CreateOptions{}` without `Provider` still means claude (engine default) — fine; assert the provider explicitly where useful.

Add tests (names are suggestions; keep the assertions):
1. **`TestCreateClaudeLayoutStillWorks`** — `CreateOptions{Provider: "claude", CredentialSource: src}`; assert: `.claude` real dir; `.claude/.credentials.json` real `0600` == src; `.claude/.credentials.lock` real; `.claude.json` real; top-level dotfiles (`.bashrc`, `.ssh`, `.config`, …) symlinked; inner `.claude/projects` symlinked. (Identical to today's Claude invariants — proves the refactor is behavior-preserving for Claude.)
2. **`TestCreateCodexLayoutFromCredentialSource`** — `Provider: "codex", CredentialSource: src`; assert `.codex` real; `.codex/auth.json` real `0600` == src; `.codex/config.toml` real `0600` containing `cli_auth_credentials_store = "file"`; `.codex/sessions` and `.codex/history.jsonl` are symlinks; **`.codex/log`, `.codex/app-server-daemon`, `.codex/app-server-control`, `.codex/state.sqlite`, and `.codex/run-1234` are absent (never symlinks)** — the allow-list test (logs/runtime/sqlite excluded for auth-diagnostic & state-isolation safety). Note the sharing claim honestly: this shares **minimal CLI transcript state only** (`sessions`/`history.jsonl`); full desktop/sidebar history (which may live in SQLite) is not guaranteed to carry over.
3. **`TestCreateCodexLayoutWritesFreshMinimalConfig`** — real config has `model = "gpt-5"` and `log_dir = "/abs/real/log"`; after create assert the shallow `config.toml` is a real `0600` file containing exactly one `cli_auth_credentials_store = "file"` and does **NOT** contain `model`, `log_dir`, or `sqlite_home` (proves we write fresh, not copy — the path-key leak fix).
4. **`TestEnsureFileCredentialStoreSingleQuoted`** *(in `internal/provider/codex`, not shallow — that's where the writer lives)* — seed `config.toml` at `0600` with `model = "gpt-5"\ncli_auth_credentials_store = 'keyring'\n`; call `EnsureFileCredentialStore`; assert it preserves `model`, has **exactly one** `cli_auth_credentials_store`, value `"file"`. (Exercises the broadened regex from Phase 6. Note: shallow-create itself no longer copies the real config, so this guards the standalone helper used by `activate`/`profile add`.)
5. **`TestCreateCodexLayoutEmptyAuth`** — `Provider: "codex"`, no source; `.codex/auth.json` exists, real, `0600`.
6. **`TestMetaRecordsProvider`** — create codex; assert `.caam-shallow.json` has `provider == "codex"`, `Version == 2`, correct `credential_from`/`real_home`.
7. **`TestCredentialPathStrictOnMalformedMeta`** *(replaces the old defaults-to-claude test)* — hand-write metadata with **no** `provider`, and another with `provider:"codx"`; assert `CredentialPath`/`LayoutForProvider` **error** (no silent claude). Also assert `List()` still returns the profile (so it's listable/deletable) with `Meta.Provider` verbatim.
8. **`TestCreateRejectsUnsupportedProvider`** — `Provider: "gemini"`; error contains `unsupported shallow provider "gemini" (supported: claude, codex)`.
9. **`TestCreateRejectsClaudeJSONForNonClaude`** — `Provider: "codex", SourceClaudeJSON: x`; error contains `only valid for provider claude`.
10. **`TestCreateRejectsBothSources`** — `Provider:"codex", CredentialSource:f, CredentialSourceDir:d`; error contains `mutually exclusive`.
11. **`TestCreateProducesNoBrokenSymlinksForCodex`** — codex variant of the no-broken-symlinks walk.
12. **`TestCreateCleansUpOnError`** — force a mid-create failure (e.g. point `CredentialSourceDir` at a vault dir missing the required `auth.json`), assert `Create` errors **and** leaves no `<base>/<name>` directory behind (the deferred `RemoveAll`). Also assert a subsequent `Create` of the same name (no `--force`) succeeds.
13. **Isolation invariants** (the feature's reason to exist):
    - **`TestRealHomeUntouched`** — snapshot real `~/.claude/.credentials.json` and `~/.codex/auth.json` bytes; create claude and codex profiles; assert the real files are byte-identical afterward.
    - **`TestCredentialsAreIndependentRealFiles`** — create two codex profiles; assert each `.codex/auth.json` is a real file (not symlink); write to one; assert the other and the real `~/.codex/auth.json` are unchanged.
    - **`TestNoRealFileEscapesViaSymlinkedParent`** — for every entry in the created profile, walk each credential dest's parent chain within the profile and assert no ancestor component is a symlink (so a credential write can't traverse into real `~/`).
14. **`TestConcurrentCreateDistinctProfiles`** — `t.Parallel()`-style: create N (e.g. 8) distinct profiles concurrently; assert each is well-formed (real auth files, valid metadata) and their primary credentials are distinct files. (The headline use case.)
15. **`TestNestedRealDirLayoutWalk`** — define a **test-only** layout shaped like Antigravity (`RealDirs: [".gemini", ".gemini/antigravity-cli"]`, both as `InnerSymlinkRoots`, a primary at `.gemini/antigravity-cli/token`) and a fake `~/.gemini` with extra children. Since `Manager.Create` only accepts a registered provider id (not a `Layout`), drive this through the **unexported** helpers directly: `MkdirAll` the real dirs, then `mgr.populateSymlinks(home, testLayout)`, `mgr.populateInnerSymlinks(home, testLayout, root)` per root, and `mgr.provisionCredentials(home, testLayout, opts)`. Assert both dirs are real, the nested real dir is skipped when inner-linking `.gemini`, the token is a real file, and unrelated `.gemini/*` children are symlinks. (This is why the engine tests must be in `package shallow` — see the package note below.)

> **Test package:** `internal/shallow/shallow_test.go` is `package shallow` (white-box) — required so these tests can call unexported `validateLayout`, `mustBuildLayouts`, `populateSymlinks`, `populateInnerSymlinks`, `provisionCredentials`. Keep it `package shallow`, not `shallow_test`. (`cmd/caam/cmd/shallow_test.go` is `package cmd`.) To test that `mustBuildLayouts`/`validateLayout` reject a bad descriptor, call `validateLayout(bad)` directly (returns an error) and, for the panic path, wrap `mustBuildLayouts(bad)` in a `func(){ defer func(){ recover() }() ; ... }()` helper.
16. **`TestValidateLayoutRejectsBadDescriptors`** — call `validateLayout` on: a layout whose nested credential parent isn't a `RealDir`; one with two Primaries / zero Primaries; one whose `InnerSymlinkRoot` isn't a `RealDir`; one setting both `InnerSkip` and `InnerSymlinkAllow` for a root; one with an absolute/`..` path — assert each returns an error. (Also assert `mustBuildLayouts` would panic on one — e.g. via a helper that recovers.)
17. **`TestBaseDirNestingGuardDotDotPrefix`** — set `baseDir = <realHome>/..orch-homes`; create a profile; assert no symlink named `..orch-homes` appears inside it (the precise nesting-guard fix).
18. **`TestRejectsSymlinkedBaseAliasingRealHome`** — (a) `--base` = a symlink whose target **is** `realHome` → `NewManager` errors (the equality rule); (b) `--base` = a symlink to a *second* real dir (not `realHome`) → `NewManager` succeeds and `Create(Force:true)`/`Delete` operate inside that second dir, never the alias target. (The Phase-3.5 hardening; matches the single equality rule — no nested-under-realHome rejection.)

Keep the existing tests for default base dir precedence, duplicate handling, name validation, list sorting, delete safety, and the nested base-dir guard.

### Phase 11 — CLI tests (`cmd/caam/cmd/shallow_test.go`)

- **Add `create.Flags().String("tool", "", "")` to `newShallowTestRoot()`** (mirrors the real `init()`; without it the `--tool` tests error with "unknown flag").
- Extend `fakeShallowHome` to seed `.codex/{auth.json,config.toml}`.

> **Existing test stays green:** `TestShallowSpawnExecsCorrectHome` (claude) asserts `CLAUDE_CONFIG_DIR` is **stripped** and `HOME`/`SHALLOW_PROFILE` set — all still true under the Claude layout (no `ProviderEnvSet`; the universal strip clears `CLAUDE_CONFIG_DIR`). No change needed.

Add tests:
1. **`TestShallowCreateCodexFromVault_JSON`** — stage `<CAAM_HOME>/data/vault/codex/alice/auth.json`; `create codex-alice --from-vault codex/alice --json`; assert `success:true`, `provider:"codex"`, `credential_from:"vault:codex/alice"`, `<base>/codex-alice/.codex/auth.json` has copied content, `.codex/config.toml` contains `cli_auth_credentials_store = "file"`.
2. **`TestShallowCreateCodexFromFile`** — temp `auth.json`; `create codex-file --tool codex --from-file <path> --json`; assert it lands at `.codex/auth.json`, `provider:"codex"`.
3. **`TestShallowCreateVaultToolMismatch`** — `create bad --tool codex --from-vault claude/alice --json`; error contains `--tool "codex" does not match --from-vault tool "claude"`.
4. **`TestShallowCreateUnsupportedVaultTool`** — `create bad --from-vault gemini/alice --json`; error contains `not supported for shallow profiles (supported: claude, codex)`.
5. **`TestShallowSpawnPrintEnvCodex`** — codex profile; `shallow-spawn codex-alice --print-env`; assert output contains `export CODEX_HOME='<base>/codex-alice/.codex'`, `export CODEX_SQLITE_HOME='<base>/codex-alice/.codex'`, `export HOME='<base>/codex-alice'`, `export SHALLOW_PROFILE='codex-alice'`, **and** `unset CLAUDE_CONFIG_DIR`, `unset OPENAI_API_KEY`, `unset CODEX_API_KEY`, `unset CAAM_HOME`. Assert there is **no** `unset CODEX_HOME`/`unset CODEX_SQLITE_HOME` line (they're set, not cleared). (Assert on the `export KEY=`/`unset KEY` shape; values are single-quoted.)
6. **`TestShallowSpawnStripsRepointingEnv`** — `t.Setenv` parent `CODEX_HOME=/bogus`, `GEMINI_HOME=/bogus`, `CLAUDE_CONFIG_DIR=/bogus`, `OPENAI_API_KEY=sk-bogus`, `ANTHROPIC_API_KEY=bogus`; create a **codex** profile; inject fake `spawnExec`; run `shallow-spawn codex-alice -- sh -c 'echo hi'`; assert the captured env has `CODEX_HOME=<base>/codex-alice/.codex` (overwritten, not `/bogus`) and contains **no** `GEMINI_HOME`, **no** `CLAUDE_CONFIG_DIR`, **no** `OPENAI_API_KEY`, **no** `ANTHROPIC_API_KEY`. Repeat for a **claude** profile asserting `HOME`/`SHALLOW_PROFILE` set and **no** `CLAUDE_CONFIG_DIR`/`CODEX_HOME`/`GEMINI_HOME`/API-key vars.
7. **`TestShallowListJSONIncludesProvider`** — one claude + one codex; `list --json`; assert each `provider` correct.
8. **`TestShallowCreateManagedFilesJSON`** — codex profile `--json`; unmarshal `managed_files`; assert it contains `<base>/<name>/.codex/auth.json`, `.codex/config.toml`, `.caam-shallow.json` (only coverage for the `managed_files` acceptance item — else drop that item from §8).
9. **`TestShallowSpawnRejectsMalformedMeta`** — create a profile, then (a) delete its `.caam-shallow.json`, (b) in a second profile rewrite it to `"provider":""`, (c) in a third to `"provider":"gemini"`; `shallow-spawn <name> --print-env` must **error**, never print Claude-default env. Assert substrings: (a)+(b) → `has no recorded provider`; (c) → `uses unsupported provider "gemini"`. Set parent `CODEX_HOME=/bogus` first to prove it doesn't silently fall through to a leaky Claude spawn.
10. **`TestShallowProvidersSubsetOfTools`** — assert every `shallow.SupportedProviders()` id is a key of the `tools` map in `root.go` (catches a typo'd provider id desyncing from the vault tool name).
11. **`TestLayoutVaultNamesMatchAuthfile`** — for each shipped provider, assert the layout's `Primary().VaultName` equals the basename `caam backup` writes — i.e. `filepath.Base(authfile.CodexAuthFiles().Files[0].Path) == "auth.json"` for codex, and the `.credentials.json` primary for claude (`authfile.ClaudeAuthFiles().Files[0]`). Catches basename drift between a layout and the vault, which would silently break `--from-vault` (and is the exact failure that would break Appendix A's agy drop-in if `backup` and the layout disagreed).

> `TestShallowCreateUnknownVaultProfile` (existing) asserts `Contains(stdout, "missing .credentials.json")`. The new error still contains that substring, so the test passes unchanged — no edit needed (just don't be surprised it stays green).

Keep existing Claude CLI tests green (they pass `--tool`-less → claude), except the two flagged updates above.

> Cross-platform note: spawn tests rely on `sh` via `exec.LookPath` (fine on Unix CI). For Windows, assert env construction without `LookPath("sh")`; out of scope here.

### Phase 12 — Docs + help (`README.md`, `cmd/caam/cmd/shallow.go`)

- README shallow section (~lines 141–208): state that shallow profiles support **Claude and Codex**; contrast the three modes (§2); add the Codex examples from §6; document the Codex shallow layout and the daemon caveat:
  ```
  <shallow-home>/.codex/              real directory
  <shallow-home>/.codex/auth.json     real file, 0600 (per-identity OAuth)
  <shallow-home>/.codex/config.toml   real file, 0600 (enforces file auth store)
  <shallow-home>/.codex/sessions, history.jsonl   symlink → ~/.codex/… (audited allow-listed shared state)
  <shallow-home>/.codex/<anything else>               NOT shared (allow-list keeps daemon/runtime dirs out)
  ```
  - **Codex daemon caveat:** a long-lived Codex daemon caches auth in memory. Shallow Codex sessions set `CODEX_HOME` to the shallow `.codex` **and** use an allow-list so no real `~/.codex` runtime/control dir is symlinked in — so a daemon started inside a shallow session is *designed* to belong to that shallow `CODEX_HOME`. This relies on Codex rooting daemon/socket discovery under `CODEX_HOME`; **verify against the installed Codex** (the exact runtime-dir name is an external artifact this repo doesn't pin), and add that verification to the acceptance for the Codex hardening.
  - **Env isolation note:** `shallow-spawn` clears the active harness's repointing vars (`CLAUDE_CONFIG_DIR`, `CODEX_HOME`, `GEMINI_HOME`) and inherited credential-override vars (`OPENAI_API_KEY`, `ANTHROPIC_API_KEY`, `ANTHROPIC_AUTH_TOKEN`, `CLAUDE_CODE_OAUTH_TOKEN`), then sets the active harness's own. So a shallow profile uses its vaulted subscription identity, not an inherited key. To use env-key auth inside a shallow session, inject it past caam: `caam shallow-spawn p -- env OPENAI_API_KEY=… codex`. `--print-env` emits `export`/`unset` lines so wrappers reproduce this.
  - **Known limitations (be explicit, don't over-claim):** (1) a shallow profile isolates the **harness it was created for**; launching a *different* harness from its shell may read that harness's real `~/` auth (the symlink farm passes it through). Use a profile of the right provider. (2) Claude's **secondary** auth (`~/.config/claude-code/auth.json`) is **not** isolated in this version (same as today); see the deferred hardening (Appendix D). (3) On macOS, Claude may use the login Keychain (not a file); shallow isolation is file-based and cannot redirect a Keychain.
  - Replace the stale "antigravity is deferred" note: say Antigravity is the next planned harness (Appendix A) without committing it.
- Update the cobra `Long` help for `shallow-profile`, `create`, and `shallow-spawn` to mention both providers, the `--tool` flag, and inference from `--from-vault`. Keep help and README consistent. Also fix the **flag-help drift** (these strings are stale/Claude-specific today): the `--from-file` help says "a `.credentials.json`" → make it "a credential file (defaults to claude; use `--tool codex` for a Codex `auth.json`)"; note explicitly that **`--from-file` never infers the provider from the filename** — without `--tool` it defaults to claude. Update the `--print-env` help ("print `HOME=...` assignments") to describe the `export`/`unset` output. Update the package doc comment (§ Phase 13) and any root/`shallow-profile` help that still presents only two isolation modes or Claude-only examples.
- *(Optional, nice-to-have)* register a cobra completion function for `--tool` that returns `shallow.SupportedProviders()`, so tab-completion lists the supported harnesses.

### Phase 13 — Format, validate, smoke

Also update the **package doc comment** at the top of `internal/shallow/shallow.go` (lines 1–31): it currently describes a Claude-only layout. Rewrite it to describe the provider-keyed `Layout` registry (Claude + Codex, Antigravity-ready) so the top of the primary file doesn't contradict the new design. (The Go snippets in this plan are written in a compressed one-line-body style for density; `gofmt -w` will expand/realign them — that's expected, run it before `gofmt -l .`.)

```sh
gofmt -w internal/shallow/shallow.go cmd/caam/cmd/shallow.go \
        internal/provider/codex/codex.go \
        internal/shallow/shallow_test.go cmd/caam/cmd/shallow_test.go
go build ./... && go vet ./... && gofmt -l .
go test ./internal/shallow ./cmd/caam/cmd ./internal/provider/codex
go test ./...   # if too slow / unrelated failures exist, record the focused result + reason
```

Manual smoke test:

```sh
tmp="$(mktemp -d)"
export CAAM_HOME="$tmp/caam"
export CAAM_SHALLOW_HOMES_DIR="$tmp/shallow"
mkdir -p "$CAAM_HOME/data/vault/codex/alice"
printf '{"access_token":"fake"}\n' > "$CAAM_HOME/data/vault/codex/alice/auth.json"

caam shallow-profile create codex-alice --from-vault codex/alice --json
caam shallow-spawn      codex-alice --print-env
test -f "$CAAM_SHALLOW_HOMES_DIR/codex-alice/.codex/auth.json"
test -f "$CAAM_SHALLOW_HOMES_DIR/codex-alice/.codex/config.toml"
test ! -L "$CAAM_SHALLOW_HOMES_DIR/codex-alice/.codex/auth.json"
test ! -L "$CAAM_SHALLOW_HOMES_DIR/codex-alice/.codex/config.toml"
```

Expected `--print-env` (shell-quoted `export` + `unset`; full shape = the codex example in §6): `export HOME/SHALLOW_PROFILE/CODEX_HOME/CODEX_SQLITE_HOME` followed by an `unset` for every other var in `clearedEnvVars()` (§5.3) — `CLAUDE_CONFIG_DIR`, `GEMINI_HOME`, the credential-override vars (`OPENAI_API_KEY`, `CODEX_API_KEY`, …), and `CAAM_HOME`/`CAAM_SHALLOW_HOMES_DIR`.

---

## 8. Acceptance checklist (definition of done)

Engine / behavior:
- [ ] `caam shallow-profile create <name> --from-vault codex/<profile> --json` succeeds when the vault has `auth.json`.
- [ ] `caam shallow-profile create <name> --tool codex --from-file /path/auth.json --json` lands the file at `.codex/auth.json`.
- [ ] `--print-env` output is `export KEY='…'` (quoted) for set vars + `unset KEY` for cleared vars: codex sets HOME/SHALLOW_PROFILE/CODEX_HOME/CODEX_SQLITE_HOME and unsets CLAUDE_CONFIG_DIR/GEMINI_HOME + API-key + CAAM-discovery vars; claude sets HOME/SHALLOW_PROFILE and unsets CLAUDE_CONFIG_DIR/CODEX_HOME/CODEX_SQLITE_HOME/GEMINI_HOME + API-key + CAAM-discovery vars.
- [ ] `.codex` is a real directory; `.codex/auth.json` and `.codex/config.toml` are real `0600` files (not symlinks).
- [ ] `.codex/config.toml` contains `cli_auth_credentials_store = "file"` and preserves pre-existing keys (incl. a single-quoted prior value, with no duplicate key).
- [ ] Allow-listed `.codex` shared state (`sessions`/`history.jsonl`, verified against installed Codex) is symlinked when present; **any other** `.codex/*` (incl. `logs` and a daemon/runtime/control dir of any name) is NOT symlinked.
- [ ] Missing/vanished passthrough sources are skipped without leaving broken symlinks (top-level AND inner farms).
- [ ] Existing Claude credential isolation preserved (the `.claude` real files + farm behave as today), except the farm now additionally shadows CAAM-owned roots (Phase 3.5) and rejects symlinked profile paths — update only the tests those touch.

Isolation / safety (Phase 3.5 — core):
- [ ] **Vault not exposed:** no symlink inside any shallow HOME resolves to the configured vault root (tested with the default vault, `CAAM_HOME` set to a subdir of `~`, and a symlinked `CAAM_HOME`); `NewManager` rejects the degenerate `CAAM_HOME == realHome`.
- [ ] **No profile-symlink hijack:** a profile path that is a symlink (`alice -> bob`) is refused by `Get`/spawn/`CredentialPath`/`Delete`/`Create --force`.
- [ ] **Destructive ops are safe:** `Create --force` and `Delete` only `RemoveAll` a real dir containing `.caam-shallow.json` — a symlinked base (`/tmp/b -> ~/.ssh`) can't make them delete real-home files.
- [ ] **No profile-tree exposure:** a symlinked base (`/tmp/b -> ~/profiles`) does not leave a `profiles` symlink inside the shallow HOME (canonical-base skip).
- [ ] Creating a profile never mutates the real `~/` (real `~/.claude/.credentials.json` / `~/.codex/auth.json` / vault byte-identical before/after).
- [ ] No credential dest has a symlinked ancestor inside the profile (no write-through into real `~/`).
- [ ] `shallow-spawn` strips the active harness's repointing vars (incl. `CODEX_SQLITE_HOME`), inherited credential-override vars (incl. `OPENAI_API_KEY`, `CODEX_API_KEY`), and CAAM discovery vars (`CAAM_HOME`, `CAAM_SHALLOW_HOMES_DIR`); then sets the active harness's own (codex `CODEX_HOME`/`CODEX_SQLITE_HOME` override a `/bogus` parent).
- [ ] `shallow-spawn` refuses to exec if a managed credential/policy file in the profile is a symlink (pre-spawn integrity check).
- [ ] `--print-env` output is shell-quoted `export`/`unset` (safe to `eval` with an exotic `--base`).
- [ ] Codex daemon/runtime dirs (`app-server-daemon`/`app-server-control`/etc.) and `logs` are never symlinked; only the audited allow-list (`sessions`, `history.jsonl`) is.
- [ ] Two concurrent profiles have distinct, independent credential files (parallel-create test passes).
- [ ] `Create` cleans up the half-built dir on error; spawn/`CredentialPath` refuse malformed metadata.
- [ ] Docs state the limitations honestly (§8.1): cross-harness shell launches and Claude's secondary auth are **not** isolated in this version (Appendix D).

Generalization / API:
- [ ] `Manager.Create` and `runShallowSpawn` contain **no per-harness behavior branches** — all per-harness behavior comes from the `Layout`. (The only provider check is *validation* of the claude-only `--from-claude-json` flag, which is a flag guard, not behavior.)
- [ ] Adding a harness requires only a new `Layout` constructor + one line in `mustBuildLayouts(...)` (proven by the Phase-10 nested test-only layout + the Appendix A engine trace, which needs zero `Create`/CLI edits).
- [ ] Every "supported: …" string (errors, `--tool` help, empty-list hint) is generated from `SupportedProviders()` — no hardcoded `claude, codex` literals.
- [ ] `validateLayout` rejects bad descriptors (nested real-file parent not a RealDir; ≠1 Primary; root not a RealDir; both InnerSkip+Allow; abs/`..` paths) at registry build.

Metadata / output:
- [ ] New metadata records `provider` and `version: 2`. `readMeta` never invents/validates a provider; strict `LayoutForProvider` errors on empty/unknown.
- [ ] JSON create/list output includes `provider`; create output includes `managed_files` (existing files only). `list` shows a malformed profile's provider verbatim (or `—`), never a guessed `claude`.

Process:
- [ ] No backwards-compatibility shims, no `fooV2.go`, edits made in place and by hand.
- [ ] `go build ./... && go vet ./... && gofmt -l .` clean; focused tests pass (full suite run or its omission recorded).
- [ ] README + cobra help updated and consistent.

## 8.1 Known limitations & non-goals (state these explicitly in README/help)

A safety-sensitive feature should be honest about what it does **not** do. Document each:

- **Cooperative, not adversarial (the big one — see §2.2).** All profiles run as one Unix user. A hostile or buggy same-UID process can read any sibling profile (`$BASE/<other>/...`) or real `~/` file directly; no `HOME` redirect prevents that. Likewise, an adversarial create-time TOCTOU (another same-UID process swapping a profile dir for a symlink mid-create) is **out of scope** — for true adversarial isolation use separate users / containers / namespaces. The hardening here prevents *accidental* cross-contamination among *cooperative* tools, not attacks.
- **Single-provider isolation.** A shallow profile isolates the harness recorded in its metadata. Launching a *different* harness from a shallow shell may read that harness's real `~/` auth (its root passes through the symlink farm). Deferred fix: Appendix D.2. → use a profile of the right provider.
- **Claude secondary auth not isolated** (`~/.config/claude-code/auth.json`) — same as today; Appendix D.1.
- **macOS Keychain.** Where a harness stores creds in the OS keychain (Claude on macOS), shallow can't redirect it; isolation is file-based.
- **Default tool homes only.** Create mirrors the *default* real locations (`~/.codex`, `~/.gemini`, `~/.claude`). If a user's real tool state lives at a custom `$CODEX_HOME`/`$GEMINI_HOME` outside `~/`, shallow create won't mirror that state (vault backup may still have used it). Out of scope; a future `SourceRoot` hook on `Layout` could add it.
- **Codex custom-provider `env_key` not stripped.** Phase 3.5 strips known Codex token vars (`CODEX_API_KEY`, `CODEX_ACCESS_TOKEN`, `OPENAI_API_KEY`), but a copied `~/.codex/config.toml` can define a custom `model_providers.*.env_key = "SOME_KEY"`; an inherited `SOME_KEY` would still authenticate. Parsing the seeded config to unset arbitrary `env_key`s is deferred (Appendix D.4).
- **Vault shadowing is conservative.** Phase 3.5 shadows the *whole top-level dir* containing the vault/`CAAM_HOME` when it's under `~` (e.g. all of `~/.local` for the default vault), so siblings under that dir aren't shared in shallow HOMEs. Precise sub-path shadowing (Appendix D.2/D.5) preserves sibling sharing. Mitigation: set `CAAM_HOME` outside `~` (e.g. `/var/lib/caam`). **Degenerate config:** `CAAM_HOME=$HOME` (vault directly in the real home) can't be shadowed without shadowing the whole home — `NewManager` should reject `CAAM_HOME == realHome` (or the vault root == realHome) with a clear error; document that `CAAM_HOME` must be a subdirectory or outside `~`.
- **Spawn does a cheap integrity check, not deep validation.** Spawn validates the metadata provider and refuses if a managed credential/policy file is a **symlink** or (for a required credential) missing/non-regular (Phase 7). It does **not** deep-validate contents — e.g. it doesn't re-confirm Codex `config.toml` still has `cli_auth_credentials_store = "file"`, or that a credential is non-empty/valid. A hand-corrupted profile beyond those checks is the user's responsibility; a future `shallow-profile doctor` could do deeper validation (own bead).
- **Concurrency contract.** Supported: concurrent `create` of *distinct* profile names, and concurrent `shallow-spawn` of already-created profiles (the headline use). **Not** supported (no locking): concurrent mutation of the *same* profile (two `create`s, `create` vs `delete`). Also note: shared allow-listed state (Codex `sessions`/`history.jsonl`, Claude `.claude/projects`) is intentionally shared across identities — N parallel sessions may write it concurrently, same as today's Claude behavior. Run `go test -race ./internal/shallow ./cmd/caam/cmd` for the concurrency tests.
- **Profile portability.** Profiles record `Meta.RealHome` and use absolute symlinks; moving a profile or changing `HOME` later isn't supported.

---

## Appendix A — Adding a new harness: Antigravity (`agy`) worked example (the generalization proof)

> This is **not** implemented in this issue. It exists to (a) prove the Section-5 abstraction is sufficient for a structurally-different harness, and (b) make the future addition a copy-paste. If anything here would require editing `Manager.Create`, `provisionCredentials`, the symlink helpers, or the CLI, the abstraction in §5 must be fixed *now*.

Antigravity's real auth (from `internal/provider/agy/agy.go`):
- **Authoritative credential (required):** `~/.gemini/antigravity-cli/antigravity-oauth-token` (mode `0600`, not device-bound).
- **Companion files (optional, restore full context):** `~/.gemini/google_accounts.json` (active email), `~/.gemini/oauth_creds.json` (shared Google creds cache), `~/.gemini/antigravity-cli/settings.json` (default model/telemetry).
- **Env:** HOME only — agy derives `~/.gemini` from HOME (honoring `GEMINI_HOME` if set, so a shallow spawn must **delete** a stray `GEMINI_HOME`, mirroring the `CLAUDE_CONFIG_DIR` guard).
- **Nested + multi-file + shared-dir:** `.gemini` is shared with the legacy Gemini CLI, so `.gemini` must be a real dir whose *non-agy* children symlink back, while `.gemini/antigravity-cli` is a real dir holding the real token/settings.

The complete descriptor — note it touches **nothing** but its own constructor:

```go
func AntigravityLayout() Layout {
	return Layout{
		Provider:   "agy",
		DefaultBin: "agy",
		// Two nested real dirs: the shared .gemini and the agy-specific subdir.
		RealDirs:  []string{".gemini", ".gemini/antigravity-cli"},
		RealFiles: nil, // no non-credential policy files
		Credentials: []AuthFile{
			{VaultName: "antigravity-oauth-token", DestRel: ".gemini/antigravity-cli/antigravity-oauth-token", Primary: true, Required: true},
			{VaultName: "google_accounts.json",    DestRel: ".gemini/google_accounts.json"},
			{VaultName: "oauth_creds.json",         DestRel: ".gemini/oauth_creds.json"},
			{VaultName: "settings.json",            DestRel: ".gemini/antigravity-cli/settings.json"},
		},
		// Symlink the non-real children of BOTH real dirs back to the real HOME
		// (deny-list default — .gemini holds no volatile socket dir, unlike .codex).
		InnerSymlinkRoots: []string{".gemini", ".gemini/antigravity-cli"},
		// No ProviderEnvSet and no CreateManagedFiles: a redirected HOME plus the
		// UNIVERSAL repoint-var strip (which already deletes GEMINI_HOME) fully
		// isolates agy. This is the payoff of the env-model refactor — what used to
		// need a per-layout `delete(GEMINI_HOME)` is now free.
	}
}
```

What an engineer would do to ship agy shallow support, in full:
1. Paste `AntigravityLayout()` into `internal/shallow/shallow.go`.
2. Add one line — `AntigravityLayout(),` — to the `mustBuildLayouts(...)` call (§5.4). (No `init()`, no exported `RegisterLayout`.)
3. Ensure `caam backup agy <profile>` vaults the four files under `<vault>/agy/<profile>/` with those exact basenames. **Verified:** `Vault.Backup` stores each file by `filepath.Base(spec.Path)` (authfile.go), and `AntigravityAuthFiles()` (authfile.go) already names them `antigravity-oauth-token`, `google_accounts.json`, `oauth_creds.json`, `settings.json` — all unique, so they land flat in the vault dir matching the `VaultName`s above.
4. Add docs + tests mirroring the Codex ones (an `agy` create-from-vault test asserting nested real files + symlinked `.gemini` siblings).

Trace the descriptor through the **unchanged** engine to confirm sufficiency:
- `Create` → `MkdirAll(home/.gemini)` and `MkdirAll(home/.gemini/antigravity-cli)` (both real). `validateLayout` passes: every nested cred dest's parent (`.gemini`, `.gemini/antigravity-cli`) is a `RealDir`; both `InnerSymlinkRoots` are `RealDirs`.
- `populateSymlinks` skip top-components = `{.gemini}` ∪ `alwaysSkip` → `.gemini` stays a real dir; every other real-HOME top entry symlinks through. ✓
- `populateInnerSymlinks(".gemini")` → skips `antigravity-cli` (nested real dir, parent `.gemini`), `google_accounts.json`, `oauth_creds.json` (cred dests, parent `.gemini`); symlinks all other `~/.gemini/*` (legacy gemini state) through. ✓
- `populateInnerSymlinks(".gemini/antigravity-cli")` → skips `antigravity-oauth-token`, `settings.json`; symlinks any other `~/.gemini/antigravity-cli/*` through. ✓
- `provisionCredentials(CredentialSourceDir = <vault>/agy/<profile>)` → copies the required token plus each present optional companion; absent companions skip without error. `--from-file` is allowed (only one Required credential). ✓
- `SpawnEnv` deletes all `repointingEnvVars` (incl. `GEMINI_HOME`) and sets HOME/SHALLOW_PROFILE; no `ProviderEnvSet`, so `--print-env` prints `HOME`, `SHALLOW_PROFILE`, then `unset` lines for the repoint vars. ✓

Zero edits to `Manager.Create`, the symlink helpers, `provisionCredentials`, or the CLI *logic*. That is the bar this design is held to. The only shared-code touches when agy ships are **data additions**, not logic: the `mustBuildLayouts(...)` registration line, and (if you want agy's API-key auth stripped like the others) adding `GEMINI_API_KEY`/`GOOGLE_API_KEY` to `credentialOverrideEnvVars`. Both are one-liners appending to existing lists — no control-flow changes. (Also verify, when agy ships: that `~/.gemini` / `~/.gemini/antigravity-cli` hold no volatile runtime/socket dir that the deny-list mode would symlink through — if they do, switch agy's roots to `InnerSymlinkAllow` like Codex.)

> **CI proof of the nested/multi-file machinery now (without shipping agy):** the Phase-10 tests `TestNestedRealDirLayoutWalk` (a test-only layout shaped like agy) and `TestValidateLayoutRejectsBadDescriptors` exercise exactly the nested-real-dir + validation paths agy needs — they compile and run today. The `AntigravityLayout()` above is an *illustration to paste in when agy ships*; at that point also add `validateLayout(AntigravityLayout())` to the suite. (Don't add the `AntigravityLayout()` function to the codebase now just to test it — the test-only nested layout already proves the abstraction.)

---

## Appendix B — Rejected alternatives (do not re-introduce)

1. **Scattered `if provider == "codex"` branches in `Create`/CLI.** Rejected: every new harness re-opens the manager and the CLI; high tech-debt; exactly what the issue asks to avoid.
2. **Reusing `internal/provider.Registry` / the `provider.Provider` interface.** Rejected: that interface is about auth *flows* (login/status/import/validate), not filesystem shape; coupling shallow to it drags in browser/login deps and makes a trivial harness expensive to add. The shared **id strings** keep the two registries aligned without code coupling.
3. **A "general & unified" command tree (`caam shallow <provider> create|spawn`).** Rejected as less ergonomic: the dominant case is a single Claude session, and qualifying every command with a provider is friction. The flat tree with `--tool` + `--from-vault` inference + claude default is the chosen ergonomics.
4. **Single-credential model (`CredentialRelPath string` + per-provider copy code).** Rejected: cannot express Antigravity's 4-file auth without editing `Create`. The `Credentials []AuthFile` list makes multi-file a *data* concern.
5. **Any backwards-compatibility handling (compat shims, provider aliases, metadata migration, defaulting malformed stored metadata).** Rejected per repo policy. `NormalizeProvider("") → claude` is an ergonomic default for *user input*, not a compat shim; stored metadata is resolved strictly.
6. **A free-form `SpawnEnv(home,name,env)` closure (mutate-the-map).** Rejected: deletions can't be rendered for `--print-env`, so a shell wrapper would silently re-leak; and an `ExtraEnvKeys` ordering hint drifts from what's actually set. Replaced by `ProviderEnvSet` (declarative sets) + universal `repointingEnvVars` (declarative deletes), shared by exec and print.
7. **Deny-list-only inner symlinks.** Rejected for roots with volatile runtime/socket artifacts (Codex): a missed name fails *open* (auth bleed). The per-root `InnerSymlinkAllow` fails *closed* (lost sharing).
8. **Exported `RegisterLayout` + global mutable registry / `init()` registration.** Rejected: invites runtime mutation and parallel-test races, and bypasses validation. Replaced by `mustBuildLayouts(...)` (immutable, validates each).
9. **Defaulting stored metadata with empty/unknown provider to Claude.** Rejected: it's a compat shim *and* a safety hole (a Codex profile mis-run as Claude skips `CODEX_HOME` and leaks). Strict `LayoutForProvider` errors instead; `list` shows the raw value.
10. **Setting `CLAUDE_CONFIG_DIR` to a shallow dir to isolate Claude's secondary auth.** Rejected for the core: current Claude Code docs indicate `CLAUDE_CONFIG_DIR` relocates where `.credentials.json` (the *primary*) lives, so setting it could bypass the vaulted `<home>/.claude/.credentials.json` — a regression. Isolating the secondary auth is real but needs verified Claude semantics; deferred to **Appendix D**, not shipped blind.
11. **Claiming a shallow shell is safe for *other* harnesses via env-stripping alone.** Rejected: the symlink farm still passes other providers' auth roots through, so env-stripping doesn't make `shallow-spawn <claude> -- bash; codex` safe. Either narrow the claim (done) or implement global sensitive-root isolation (Appendix D).

---

## Appendix C — Stable CLI/engine error catalog (assert these substrings in tests)

These are the **intended, stable** user-facing strings — keep them exact and have tests assert the meaningful substring. Low-level wrapped filesystem errors (the `: %w` tails — `stat`/`open`/`no such file` text) are **not** canonical; they vary by OS and Go version, so tests should assert only the caam-authored prefix, never the wrapped tail. Other reachable-but-non-canonical errors (e.g. `--from-file %q: <stat err>`, `init shallow manager: <err>`, the `NewManager` symlinked-base error) are intentionally not pinned here.

| Condition | Message |
|----------|---------|
| Unsupported provider (engine) | `unsupported shallow provider "gemini" (supported: claude, codex)` |
| Unsupported `--tool` (no `--from-vault`, CLI) | `--tool "gemini" is not supported for shallow profiles (supported: claude, codex)` |
| Unsupported vault tool (CLI) | `--from-vault tool "gemini" is not supported for shallow profiles (supported: claude, codex)` |
| `--tool` vs `--from-vault` mismatch | `--tool "codex" does not match --from-vault tool "claude"` |
| Bad `--from-vault` form | `--from-vault must be in the form <tool>/<profile>, got "…"` |
| Missing codex vault auth | `vault profile codex/alice missing auth.json: <stat err>` |
| Missing claude vault auth | `vault profile claude/alice missing .credentials.json: <stat err>` |
| Invalid `--from-vault` profile | `--from-vault profile "../x" is invalid (no path separators or '.'/'..')` |
| `--from-claude-json` with non-claude (CLI, Phase 8) | `--from-claude-json is only valid for --tool claude (got codex)` |
| `--from-claude-json` with non-claude (engine backstop, Phase 3) | `--from-claude-json is only valid for provider claude (got "codex")` |
| Both credential sources (engine guard, Phase 3) | `CredentialSourceDir and CredentialSource are mutually exclusive` |
| `--from-file` for a multi-required layout (Phase 4) | `--from-file is not supported for provider "x" (it has multiple required credentials); use --from-vault` |
| Spawn on missing/unreadable OR empty-provider metadata | `shallow profile "x" has no recorded provider (missing or malformed metadata); recreate it` |
| Spawn on unknown-provider metadata | `shallow profile "x" uses unsupported provider "gemini" (supported: claude, codex)` |
| Engine layout lookup (unknown id, internal) | `unsupported shallow provider "gemini" (supported: claude, codex)` |
| `--from-vault` + `--from-file` together (CLI) | `--from-vault and --from-file are mutually exclusive` (unchanged) |

> **Ordering:** the "supported: …" tail comes from `strings.Join(shallow.SupportedProviders(), ", ")`, which uses `providerOrder` (claude, codex, then extras alphabetically). So it reads `claude, codex` now and `claude, codex, agy` once Antigravity registers — Claude first for ergonomics, stable across additions. Where it keeps tests robust, assert the supported-set substring is present rather than hardcoding the whole tail.
> **Two `--from-claude-json` messages on purpose:** the CLI layer (Phase 8) phrases it via the flag (`--tool claude`); the engine backstop (`Manager.Create`) can't know about flags, so it uses the resolved `provider`. Keep both strings exact so each test targets the right one.
> **Spawn emits bespoke messages, not wrapped engine errors:** Phase 7 produces the two spawn rows above directly (it does **not** wrap `LayoutForProvider`'s internal `unsupported shallow provider …` string, which differs — that engine string is the last row and is only seen by direct `Manager.Create`/`CredentialPath` callers). So the malformed-metadata case (nil `Meta` or empty provider) reads `… has no recorded provider …` and the unknown-provider case reads `… uses unsupported provider "gemini" …`. Tests assert those substrings.

---

## Appendix D — Deferred hardening (verify-first; out of core scope)

These are real isolation improvements the reviews surfaced, but each needs behavior verified against the installed tools before implementation, and each expands scope beyond "add Codex." Track them as their own beads. The core plan deliberately does **not** claim any of them.

### D.1 Isolate Claude's secondary auth surfaces (`~/.config/claude-code/auth.json` + `.claude/settings.json`)

**Gap:** `internal/authfile/authfile.go` records **two** further Claude auth-relevant surfaces beyond the primary `~/.claude/.credentials.json`: (a) `$CLAUDE_CONFIG_DIR/auth.json` (default `~/.config/claude-code/auth.json`) — `.config` is a passthrough symlink, so it resolves to the real shared file; and (b) `~/.claude/settings.json`, which can carry `apiKeyHelper` / API-key mode and is currently inner-symlinked through `.claude`, so a shared `settings.json` could route every profile through one helper/key, defeating per-profile OAuth. The core plan keeps today's behavior (isolates only `~/.claude/.credentials.json` + `.claude.json`, deletes inherited `CLAUDE_CONFIG_DIR`). Fixing both is part of this deferred item: in the chosen design, also `InnerSkip[".claude"] = {"settings.json"}` (or reserve a sanitized real `settings.json`) so the helper can't be inherited.

**Why deferred — the trap:** the obvious fix (set `CLAUDE_CONFIG_DIR=<home>/.config/claude-code`) may be wrong. Conflicting signals: this repo's model treats `CLAUDE_CONFIG_DIR` as pointing to a dir that holds the *secondary* `auth.json`, but current Claude Code docs indicate `CLAUDE_CONFIG_DIR` controls where the *primary* `.credentials.json` lives. If the latter is true, setting it would make Claude ignore the vaulted `<home>/.claude/.credentials.json` the engine writes — a regression.

**Before implementing, verify against the installed Claude Code:** (1) does `CLAUDE_CONFIG_DIR` relocate the primary `.credentials.json`, the secondary `auth.json`, or both? (2) does Claude actually read `~/.config/claude-code/auth.json`? (3) the precedence among `~/.claude/.credentials.json`, `$CLAUDE_CONFIG_DIR/*`, and env tokens. Use the project's Claude-docs reference for authoritative answers; do not guess.

**Then choose:**
- *If `CLAUDE_CONFIG_DIR` only affects the secondary config dir:* make `.config` + `.config/claude-code` real dirs (`RealDirs`), keep `.config` an `InnerSymlinkRoot` (so other `~/.config/*` apps pass through), `InnerSkip[".config/claude-code"] = {"auth.json"}`, and **delete** inherited `CLAUDE_CONFIG_DIR` + `XDG_CONFIG_HOME` so Claude falls back to the shallow `HOME/.config/claude-code`. Do **not** set `CLAUDE_CONFIG_DIR` (avoid the primary-relocation risk). This reuses the nested-real-dir machinery (`validateLayout` already supports it). Document the `.config` passthrough side effect (a brand-new `~/.config/<app>` created during the session lands in the shallow HOME).
- *If `CLAUDE_CONFIG_DIR` relocates the primary:* move the Claude **primary** credential `DestRel` to `.config/claude-code/.credentials.json`, set `CLAUDE_CONFIG_DIR=<home>/.config/claude-code`, and reserve that file — i.e. the whole Claude layout changes. Re-verify the lock-file location too.

This is the (a)/(g) item from §2.1.

### D.2 Global sensitive-root isolation (cross-harness shell safety)

**Gap:** a shallow profile's symlink farm passes through *other* providers' auth roots. `shallow-spawn <claude-profile> -- bash` then running `codex` reads the real `~/.codex` (the Claude layout doesn't reserve `.codex`). The core plan narrows the guarantee: a profile isolates the harness in its metadata only.

**Design (when implemented):** derive a global set of "sensitive roots" = the union of every registered layout's auth/config roots (`.claude`, `.codex`, `.gemini/antigravity-cli`, and any future). The top-level symlink farm skips ALL of them for EVERY profile (not just the active one), so a non-active provider's root is simply **absent** in the shallow HOME (a tool launched there finds nothing → fresh login, no leak). Subtlety: a *mixed* root like `.config` (one provider's auth subdir among many apps' configs) needs partial handling — `.config` real + inner-symlink-root for all profiles, with the auth subpath shadowed — which is exactly D.1's machinery generalized. Add a `TestCrossHarnessShellIsolation` proving a Claude profile leaves `.codex` absent.

**Trade-off:** this turns a 2-provider change into a "all profiles know about all providers' roots" coupling, and reduces sharing (non-active roots absent). Worth it only if cross-harness shells are a real workflow. This is the (h) item from §2.1.

### D.3 Broaden the cleared env set (XDG)

Add `XDG_CONFIG_HOME` / `XDG_DATA_HOME` to the cleared set so non-harness or XDG-based tools (e.g. a future `opencode` shallow harness whose auth lives under `$XDG_DATA_HOME/opencode`) launched in a shallow shell can't escape. Deferred because it changes behavior for *every* app in a shallow shell (fallback to `HOME/.config`/`HOME/.local/share`) and is only load-bearing once D.1/D.2 land. Implementation: extend `repointingEnvVars` (or a dedicated list) and the README note; add a strip test.

### D.4 Strip Codex custom-provider `env_key`s

After seeding the shallow `~/.codex/config.toml`, parse it (TOML) and, for every `model_providers.<id>.env_key`, add that variable name to the per-spawn cleared set (or unset it in the layout's `ProviderEnvSet`/a profile-specific env file). This closes the gap where a copied config + an inherited custom-provider key authenticates the session via env instead of the vaulted identity. Deferred because it needs per-profile dynamic env derived from config (not a static list) and a TOML parse. Add tests with `env_key = "PORTKEY_API_KEY"`.

### D.5 Precise nested-shadowing (preserve sibling sharing)

Phase 3.5 shadows the *whole top-level dir* of a CAAM-owned root under `~` (conservative; safe but over-broad). The refinement: shadow only the exact sub-path (e.g. make `.local` + `.local/share` real inner-symlink-roots, leave `.local/share/caam` absent, symlink the rest) — the same nested-real-dir machinery as D.1. This restores sharing of unrelated siblings (`~/.local/share/<otherapp>`, `~/.local/bin`) while keeping the vault hidden. Generalize D.2's global-sensitive-root pass to also cover CAAM-owned roots precisely.
