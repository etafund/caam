package cmd

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/Dicklesworthstone/coding_agent_account_manager/internal/shallow"
	"github.com/spf13/cobra"
)

// =============================================================================
// SHALLOW PROFILE COMMANDS — concurrent multi-account multiplexing.
// =============================================================================
//
// "Shallow" because each profile shares everything with the user's real HOME
// EXCEPT the auth-bearing files for ONE harness. Which files are real, where
// they live, what env vars to set, and where the credential comes from is
// captured by a provider-keyed Layout in internal/shallow, so these commands
// are harness-agnostic: Claude, Codex, and Antigravity (agy) today. The
// provider is resolved from --from-vault / --tool at create time (defaulting
// to claude) and recorded in metadata; shallow-spawn reads it back from
// metadata.

// resolveShallowManager returns a shallow.Manager rooted at the path implied by
// (in priority order): --base flag, $CAAM_SHALLOW_HOMES_DIR, $CAAM_HOME/shallow-homes,
// or ~/orch-homes. The flag wins so tests and operators can isolate.
func resolveShallowManager(cmd *cobra.Command) (*shallow.Manager, error) {
	base, _ := cmd.Flags().GetString("base")
	mgr, err := shallow.NewManager(strings.TrimSpace(base), "")
	if err != nil {
		return nil, err
	}
	// Shadow CAAM's own vault root so a shallow HOME can't symlink-through to
	// every account's credentials.
	if vault != nil {
		mgr.SetVaultRoot(vault.BasePath())
	}
	return mgr, nil
}

// shallowProfileCmd is the parent command for shallow profile management.
var shallowProfileCmd = &cobra.Command{
	Use:   "shallow-profile",
	Short: "Manage shallow profiles for concurrent multi-account use",
	Long: `Manage shallow profiles — per-identity HOME directories where the auth
files of one harness are real and MOST other entries are symlinks back to your
real HOME (CAAM-owned roots like the vault/base are withheld, Codex shares only
an allow-listed subset of ~/.codex, and some credential-bearing config may be
withheld). It is cooperative path isolation, not a sandbox — see README.

This enables N parallel sessions, each pinned to a different account, while
preserving shared state (shell history, git config, ssh keys, the harness's
own conversation history). Unlike 'caam profile add' which gives each profile
a blank, fully-isolated HOME, shallow profiles only isolate what MUST differ
(the credentials).

Shallow profiles support Claude Code, Codex CLI, and Antigravity (agy). The
provider is inferred from --from-vault <tool>/<profile>, set explicitly with
--tool, or defaults to claude.

Layout under ~/orch-homes/<name>/ for Claude:

  .claude/.credentials.json       (real file — per-identity OAuth tokens)
  .claude/.credentials.lock       (real file — per-identity flock target)
  .claude.json                    (real file — Claude rewrites this on each run)
  .claude/projects, .claude/todos (symlinks → ~/.claude/projects, etc.)
  .bashrc, .gitconfig, .ssh, ...  (symlinks → ~/.bashrc, etc.)

Spawn under a shallow identity with:

  caam shallow-spawn <name> -- claude
  caam shallow-spawn <name> -- codex
  caam shallow-spawn <name> -- agy

which sets HOME=~/orch-homes/<name> (plus the provider's env) and execs the command.`,
}

func init() {
	shallowProfileCmd.PersistentFlags().String("base", "", "shallow profiles base dir (default: $CAAM_SHALLOW_HOMES_DIR or ~/orch-homes)")
	shallowProfileCmd.AddCommand(shallowProfileCreateCmd)
	shallowProfileCmd.AddCommand(shallowProfileListCmd)
	shallowProfileCmd.AddCommand(shallowProfileDeleteCmd)
	shallowProfileCmd.AddCommand(shallowProfileDoctorCmd)
	rootCmd.AddCommand(shallowProfileCmd)
	rootCmd.AddCommand(shallowSpawnCmd)
}

// shallowProfileCreateCmd creates a new shallow profile.
var shallowProfileCreateCmd = &cobra.Command{
	Use:   "create <name>",
	Short: "Create a new shallow profile",
	Long: `Create a new shallow profile. Provisions the symlink farm and copies a
credential file into the provider's auth path inside the shallow HOME.

Provider resolution:
  --from-vault <tool>/<profile>   provider is <tool> (claude, codex, or agy)
  --tool <provider>               explicit provider; must agree with --from-vault
  (neither)                       defaults to claude

Credential source (one of):
  --from-vault <tool>/<profile>   Use an existing caam vault profile's credentials
  --from-file <path>              Copy credentials from an arbitrary path
  (none)                          Leave credentials empty; populate later via login

Examples:
  caam shallow-profile create alice --from-vault claude/alice@example.com
  caam shallow-profile create cbob  --from-vault codex/bob@example.com
  caam shallow-profile create agatha --from-vault agy/agatha@example.com
  caam shallow-profile create cbob  --tool codex --from-file /tmp/bob.auth.json
  caam shallow-profile create scratch                 # claude, empty credentials
  caam shallow-profile create cscratch --tool codex   # codex, empty credentials
  caam shallow-profile create alice --json
  caam shallow-profile create alice --force           # overwrite existing
  caam shallow-profile create alice --base /tmp/test-orch-homes`,
	Args: cobra.ExactArgs(1),
	RunE: runShallowProfileCreate,
}

func init() {
	shallowProfileCreateCmd.Flags().String("from-vault", "", "credential source: <tool>/<profile> from caam's vault (e.g. claude/alice@example.com); infers the provider")
	shallowProfileCreateCmd.Flags().String("from-file", "", "credential source: arbitrary path to a credential file (defaults to claude; use --tool codex for a Codex auth.json — the provider is never inferred from the filename)")
	shallowProfileCreateCmd.Flags().String("from-claude-json", "", "optional path to copy as <home>/.claude.json (claude only; defaults to ~/.claude.json)")
	shallowProfileCreateCmd.Flags().String("tool", "",
		fmt.Sprintf("shallow provider/harness (supported: %s; default claude)",
			strings.Join(shallow.SupportedProviders(), ", ")))
	shallowProfileCreateCmd.Flags().Bool("force", false, "overwrite an existing shallow profile")
	shallowProfileCreateCmd.Flags().Bool("json", false, "output as JSON")
}

type shallowCreateOutput struct {
	Success        bool     `json:"success"`
	Name           string   `json:"name"`
	Provider       string   `json:"provider"`
	Path           string   `json:"path"`
	CredentialFrom string   `json:"credential_from,omitempty"`
	ManagedFiles   []string `json:"managed_files,omitempty"`
	Error          string   `json:"error,omitempty"`
}

// shallowVaultRef is a parsed --from-vault reference.
type shallowVaultRef struct{ Tool, Profile string }

// parseShallowVaultRef parses "<tool>/<profile>". The profile is joined into a
// vault path, so reject path separators and the exact dot names, but allow
// legitimate names like "v1..2" / "alice@example.com".
func parseShallowVaultRef(spec string) (shallowVaultRef, error) {
	spec = strings.TrimSpace(spec)
	parts := strings.SplitN(spec, "/", 2)
	if len(parts) != 2 || strings.TrimSpace(parts[0]) == "" || strings.TrimSpace(parts[1]) == "" {
		return shallowVaultRef{}, fmt.Errorf("--from-vault must be in the form <tool>/<profile>, got %q", spec)
	}
	tool := strings.ToLower(strings.TrimSpace(parts[0]))
	profile := strings.TrimSpace(parts[1])
	if profile == "." || profile == ".." || strings.ContainsAny(profile, `/\`) {
		return shallowVaultRef{}, fmt.Errorf("--from-vault profile %q is invalid (no path separators or '.'/'..')", profile)
	}
	return shallowVaultRef{Tool: tool, Profile: profile}, nil
}

// inferShallowProvider resolves the provider id per the §6 rules and validates
// that --tool agrees with --from-vault. It is the SINGLE provider-resolution
// path for `create` and emits CLI-flavored error strings (Appendix C).
func inferShallowProvider(toolFlag, fromVault string) (string, error) {
	toolFlag = strings.ToLower(strings.TrimSpace(toolFlag))
	if fromVault != "" {
		ref, err := parseShallowVaultRef(fromVault)
		if err != nil {
			return "", err
		}
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
	if toolFlag == "" {
		return shallow.ProviderClaude, nil // ergonomic default
	}
	p, err := shallow.NormalizeProvider(toolFlag)
	if err != nil {
		return "", fmt.Errorf("--tool %q is not supported for shallow profiles (supported: %s)",
			toolFlag, strings.Join(shallow.SupportedProviders(), ", "))
	}
	return p, nil
}

// resolveShallowVaultDir returns the vault profile DIR + descriptive label for
// an ALREADY-resolved provider, after verifying the provider's Primary
// credential is present in the vault.
func resolveShallowVaultDir(providerID string, ref shallowVaultRef) (sourceDir, label string, err error) {
	if vault == nil {
		return "", "", fmt.Errorf("vault not initialized")
	}
	if !strings.EqualFold(ref.Tool, providerID) { // defensive: caller must pass a matching pair
		return "", "", fmt.Errorf("internal: provider %q != vault tool %q", providerID, ref.Tool)
	}
	dir := vault.ProfilePath(ref.Tool, ref.Profile)
	layout, err := shallow.LayoutForProvider(providerID)
	if err != nil {
		return "", "", err
	}
	primary := layout.Primary()
	if _, err := os.Stat(filepath.Join(dir, primary.VaultName)); err != nil {
		return "", "", fmt.Errorf("vault profile %s/%s missing %s: %w", ref.Tool, ref.Profile, primary.VaultName, err)
	}
	return dir, "vault:" + ref.Tool + "/" + ref.Profile, nil
}

func runShallowProfileCreate(cmd *cobra.Command, args []string) error {
	name := args[0]
	jsonOut, _ := cmd.Flags().GetBool("json")
	force, _ := cmd.Flags().GetBool("force")
	fromVault, _ := cmd.Flags().GetString("from-vault")
	fromFile, _ := cmd.Flags().GetString("from-file")
	fromClaudeJSON, _ := cmd.Flags().GetString("from-claude-json")
	tool, _ := cmd.Flags().GetString("tool")

	output := shallowCreateOutput{Name: name}
	emit := func(err error) error {
		if jsonOut {
			output.Success = err == nil
			if err != nil {
				output.Error = err.Error()
			}
			enc := json.NewEncoder(cmd.OutOrStdout())
			enc.SetIndent("", "  ")
			_ = enc.Encode(output)
			// Emit the JSON error envelope on stdout but still exit non-zero
			// (so automation checking $? isn't misled), and silence cobra so it
			// doesn't also print the human error after the JSON.
			if err != nil {
				cmd.SilenceErrors = true
				cmd.SilenceUsage = true
			}
			return err
		}
		return err
	}

	if fromVault != "" && fromFile != "" {
		return emit(fmt.Errorf("--from-vault and --from-file are mutually exclusive"))
	}

	providerID, err := inferShallowProvider(tool, fromVault)
	if err != nil {
		return emit(err)
	}
	output.Provider = providerID // set early so JSON ERROR output also carries the resolved provider
	if fromClaudeJSON != "" && providerID != shallow.ProviderClaude {
		return emit(fmt.Errorf("--from-claude-json is only valid for --tool claude (got %s)", providerID))
	}

	mgr, err := resolveShallowManager(cmd)
	if err != nil {
		return emit(fmt.Errorf("init shallow manager: %w", err))
	}

	opts := shallow.CreateOptions{Provider: providerID, Force: force, SourceClaudeJSON: fromClaudeJSON}

	switch {
	case fromVault != "":
		ref, _ := parseShallowVaultRef(fromVault) // already validated inside inferShallowProvider
		dir, label, err := resolveShallowVaultDir(providerID, ref)
		if err != nil {
			return emit(err)
		}
		opts.CredentialSourceDir, opts.CredentialFromLabel = dir, label
	case fromFile != "":
		abs, err := filepath.Abs(fromFile)
		if err != nil {
			return emit(fmt.Errorf("resolve --from-file: %w", err))
		}
		st, err := os.Stat(abs)
		if err != nil {
			return emit(fmt.Errorf("--from-file: %w", err))
		}
		if st.IsDir() {
			return emit(fmt.Errorf("--from-file: %s is a directory", abs))
		}
		opts.CredentialSource, opts.CredentialFromLabel = abs, "file:"+abs
	default:
		// No credential source — terse stderr nudge unless json.
		if !jsonOut {
			fmt.Fprintln(cmd.ErrOrStderr(), "note: no --from-vault/--from-file given; the credential file will be empty.")
			fmt.Fprintln(cmd.ErrOrStderr(), "      Populate it before running 'shallow-spawn' (e.g. by signing in inside the shallow HOME).")
		}
	}

	// Resolve the layout once for output (managed_files + the provider-correct
	// next step). LayoutForProvider is safe here: providerID came from
	// NormalizeProvider.
	layout, err := shallow.LayoutForProvider(providerID)
	if err != nil {
		return emit(err)
	}

	home, err := mgr.Create(name, opts)
	if err != nil {
		return emit(fmt.Errorf("create shallow profile: %w", err))
	}

	output.Path = home
	output.CredentialFrom = opts.CredentialFromLabel
	output.ManagedFiles = layout.ManagedFilePaths(home)
	if jsonOut {
		output.Success = true
		enc := json.NewEncoder(cmd.OutOrStdout())
		enc.SetIndent("", "  ")
		return enc.Encode(output)
	}

	fmt.Fprintf(cmd.OutOrStdout(), "Created shallow profile %q\n", name)
	fmt.Fprintf(cmd.OutOrStdout(), "  Provider: %s\n", providerID)
	fmt.Fprintf(cmd.OutOrStdout(), "  Path: %s\n", home)
	if opts.CredentialFromLabel != "" {
		fmt.Fprintf(cmd.OutOrStdout(), "  Credentials: %s\n", opts.CredentialFromLabel)
	}
	fmt.Fprintf(cmd.OutOrStdout(), "\nNext steps:\n")
	fmt.Fprintf(cmd.OutOrStdout(), "  caam shallow-spawn %s -- %s\n", name, layout.DefaultBin)
	return nil
}

// shallowProfileListCmd lists existing shallow profiles.
var shallowProfileListCmd = &cobra.Command{
	Use:     "list",
	Aliases: []string{"ls"},
	Short:   "List shallow profiles",
	Long: `List shallow profiles under the configured base dir.

Examples:
  caam shallow-profile list
  caam shallow-profile list --json`,
	RunE: runShallowProfileList,
}

func init() {
	shallowProfileListCmd.Flags().Bool("json", false, "output as JSON")
}

type shallowListItem struct {
	Name           string    `json:"name"`
	Provider       string    `json:"provider"`
	Path           string    `json:"path"`
	CredentialFrom string    `json:"credential_from,omitempty"`
	CreatedAt      time.Time `json:"created_at,omitempty"`
}

type shallowListOutput struct {
	BaseDir  string            `json:"base_dir"`
	Profiles []shallowListItem `json:"profiles"`
	Count    int               `json:"count"`
	Error    string            `json:"error,omitempty"`
}

func runShallowProfileList(cmd *cobra.Command, _ []string) error {
	jsonOut, _ := cmd.Flags().GetBool("json")
	// emitErr surfaces an early-return error as JSON (mirroring the list output
	// struct, so a `--json` consumer always gets valid JSON) when --json is set;
	// otherwise it returns the bare error for the human path.
	emitErr := func(err error) error {
		if jsonOut {
			enc := json.NewEncoder(cmd.OutOrStdout())
			enc.SetIndent("", "  ")
			_ = enc.Encode(shallowListOutput{Error: err.Error()})
			cmd.SilenceErrors = true
			cmd.SilenceUsage = true
			return err
		}
		return err
	}

	mgr, err := resolveShallowManager(cmd)
	if err != nil {
		return emitErr(fmt.Errorf("init shallow manager: %w", err))
	}
	profiles, err := mgr.List()
	if err != nil {
		return emitErr(fmt.Errorf("list shallow profiles: %w", err))
	}

	if jsonOut {
		out := shallowListOutput{BaseDir: mgr.BaseDir(), Count: len(profiles)}
		for _, p := range profiles {
			item := shallowListItem{Name: p.Name, Path: p.Path}
			if p.Meta != nil {
				item.Provider = p.Meta.Provider // verbatim; no claude fallback
				item.CredentialFrom = p.Meta.CredentialFrom
				item.CreatedAt = p.Meta.CreatedAt
			}
			out.Profiles = append(out.Profiles, item)
		}
		// Stable order for deterministic output.
		sort.Slice(out.Profiles, func(i, j int) bool { return out.Profiles[i].Name < out.Profiles[j].Name })
		enc := json.NewEncoder(cmd.OutOrStdout())
		enc.SetIndent("", "  ")
		return enc.Encode(out)
	}

	if len(profiles) == 0 {
		fmt.Fprintf(cmd.OutOrStdout(), "No shallow profiles in %s\n", mgr.BaseDir())
		fmt.Fprintln(cmd.OutOrStdout(), "Create one with:")
		fmt.Fprintln(cmd.OutOrStdout(), "  caam shallow-profile create <name> --from-vault <tool>/<profile>")
		fmt.Fprintf(cmd.OutOrStdout(), "Supported shallow tools: %s\n", strings.Join(shallow.SupportedProviders(), ", "))
		return nil
	}
	fmt.Fprintf(cmd.OutOrStdout(), "Shallow profiles (base: %s)\n", mgr.BaseDir())
	fmt.Fprintf(cmd.OutOrStdout(), "%-22s  %-8s  %-32s  %s\n", "NAME", "TOOL", "CREDENTIALS", "CREATED")
	for _, p := range profiles {
		prov := "—"
		credFrom := "(none)"
		created := "?"
		if p.Meta != nil {
			if p.Meta.Provider != "" {
				prov = p.Meta.Provider
			}
			if p.Meta.CredentialFrom != "" {
				credFrom = p.Meta.CredentialFrom
			}
			if !p.Meta.CreatedAt.IsZero() {
				created = p.Meta.CreatedAt.Local().Format("2006-01-02 15:04")
			}
		}
		fmt.Fprintf(cmd.OutOrStdout(), "%-22s  %-8s  %-32s  %s\n", p.Name, prov, credFrom, created)
	}
	return nil
}

// shallowProfileDeleteCmd deletes a shallow profile.
var shallowProfileDeleteCmd = &cobra.Command{
	Use:     "delete <name>",
	Aliases: []string{"rm"},
	Short:   "Delete a shallow profile",
	Long: `Delete a shallow profile. Removes the entire ~/orch-homes/<name>/ tree.
Symlinks inside it are removed without following them, so your real HOME is safe.

Examples:
  caam shallow-profile delete alice
  caam shallow-profile delete alice --force
  caam shallow-profile delete alice --json`,
	Args: cobra.ExactArgs(1),
	RunE: runShallowProfileDelete,
}

func init() {
	shallowProfileDeleteCmd.Flags().Bool("force", false, "skip confirmation prompt")
	shallowProfileDeleteCmd.Flags().Bool("json", false, "output as JSON")
}

type shallowDeleteOutput struct {
	Success bool   `json:"success"`
	Name    string `json:"name"`
	Error   string `json:"error,omitempty"`
}

func runShallowProfileDelete(cmd *cobra.Command, args []string) error {
	name := args[0]
	jsonOut, _ := cmd.Flags().GetBool("json")
	force, _ := cmd.Flags().GetBool("force")

	emit := func(err error) error {
		if jsonOut {
			out := shallowDeleteOutput{Name: name, Success: err == nil}
			if err != nil {
				out.Error = err.Error()
			}
			enc := json.NewEncoder(cmd.OutOrStdout())
			enc.SetIndent("", "  ")
			_ = enc.Encode(out)
			if err != nil {
				cmd.SilenceErrors = true
				cmd.SilenceUsage = true
			}
			return err
		}
		return err
	}

	mgr, err := resolveShallowManager(cmd)
	if err != nil {
		return emit(fmt.Errorf("init shallow manager: %w", err))
	}

	if _, err := mgr.Get(name); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return emit(fmt.Errorf("shallow profile %q does not exist", name))
		}
		return emit(err)
	}

	if !force && !jsonOut {
		// Prompt on stderr (consistent with the create empty-cred nudge) so a
		// caller capturing stdout doesn't conflate the prompt with output.
		fmt.Fprintf(cmd.ErrOrStderr(), "Delete shallow profile %q? [y/N]: ", name)
		var confirm string
		_, _ = fmt.Fscanln(cmd.InOrStdin(), &confirm)
		if strings.ToLower(strings.TrimSpace(confirm)) != "y" {
			fmt.Fprintln(cmd.OutOrStdout(), "Cancelled")
			return nil
		}
	}

	if err := mgr.Delete(name); err != nil {
		return emit(fmt.Errorf("delete shallow profile: %w", err))
	}

	if jsonOut {
		enc := json.NewEncoder(cmd.OutOrStdout())
		enc.SetIndent("", "  ")
		return enc.Encode(shallowDeleteOutput{Name: name, Success: true})
	}
	fmt.Fprintf(cmd.OutOrStdout(), "Deleted shallow profile %q\n", name)
	return nil
}

// shallowProfileDoctorCmd runs the same pre-spawn integrity check that
// shallow-spawn performs, but read-only, so you can verify a profile (or all of
// them) before fanning out parallel sessions.
var shallowProfileDoctorCmd = &cobra.Command{
	Use:     "doctor [name]",
	Aliases: []string{"check"},
	Short:   "Health-check shallow profiles (read-only pre-spawn integrity check)",
	Long: `Diagnose one or all shallow profiles. With a name, checks that single
profile; with no name, checks every profile under the base dir.

For each profile this runs the SAME integrity check that shallow-spawn performs
right before exec'ing a harness: the recorded provider must be supported, the
auth-bearing directories/files must be real (not symlinked, no symlinked
ancestor), and the required credential must be present. It is completely safe
and read-only — nothing is created, moved, or modified.

Use it before fanning out parallel sessions to catch a profile whose .claude or
.codex directory was swapped for a symlink, whose credential went missing, or
whose metadata no longer records a usable provider.

Exit status is non-zero if ANY diagnosed profile is unhealthy (or a named
profile does not exist), so it composes in scripts. Pass --json for a
machine-readable report.

Examples:
  caam shallow-profile doctor                 # check all profiles
  caam shallow-profile doctor alice           # check one
  caam shallow-profile doctor --json          # machine-readable, all profiles
  caam shallow-profile check alice            # alias`,
	Args: cobra.MaximumNArgs(1),
	RunE: runShallowProfileDoctor,
}

func init() {
	shallowProfileDoctorCmd.Flags().Bool("json", false, "output as JSON")
}

// shallowDoctorResult is one profile's diagnosis.
type shallowDoctorResult struct {
	Name     string `json:"name"`
	Provider string `json:"provider"`
	Healthy  bool   `json:"healthy"`
	Error    string `json:"error"`
}

type shallowDoctorOutput struct {
	Profiles []shallowDoctorResult `json:"profiles"`
	Healthy  bool                  `json:"healthy"`
}

// diagnoseShallowProfile computes the health of a single named profile using the
// same checks shallow-spawn applies. It never returns an error; a problem is
// captured in the result's Error field with Healthy=false.
func diagnoseShallowProfile(mgr *shallow.Manager, name string) shallowDoctorResult {
	res := shallowDoctorResult{Name: name}
	prof, err := mgr.Get(name)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			res.Error = "does not exist"
		} else {
			res.Error = err.Error()
		}
		return res
	}
	if prof.Meta == nil || prof.Meta.Provider == "" {
		res.Error = "malformed metadata (no recorded provider); recreate it"
		return res
	}
	res.Provider = prof.Meta.Provider
	layout, err := shallow.LayoutForProvider(prof.Meta.Provider)
	if err != nil {
		res.Error = fmt.Sprintf("unsupported provider %q (supported: %s)",
			prof.Meta.Provider, strings.Join(shallow.SupportedProviders(), ", "))
		return res
	}
	if err := mgr.ValidateProfileShape(name, layout); err != nil {
		res.Error = err.Error()
		return res
	}
	res.Healthy = true
	return res
}

func runShallowProfileDoctor(cmd *cobra.Command, args []string) error {
	jsonOut, _ := cmd.Flags().GetBool("json")

	emitErr := func(err error) error {
		if jsonOut {
			enc := json.NewEncoder(cmd.OutOrStdout())
			enc.SetIndent("", "  ")
			_ = enc.Encode(shallowDoctorOutput{Profiles: []shallowDoctorResult{}, Healthy: false})
			cmd.SilenceErrors = true
			cmd.SilenceUsage = true
			return err
		}
		return err
	}

	mgr, err := resolveShallowManager(cmd)
	if err != nil {
		return emitErr(fmt.Errorf("init shallow manager: %w", err))
	}

	// Build the list of names to diagnose: one named arg, or all profiles in
	// listed order.
	var names []string
	single := len(args) == 1
	if single {
		names = []string{args[0]}
	} else {
		profiles, err := mgr.List()
		if err != nil {
			return emitErr(fmt.Errorf("list shallow profiles: %w", err))
		}
		for _, p := range profiles {
			names = append(names, p.Name)
		}
	}

	results := make([]shallowDoctorResult, 0, len(names))
	unhealthy := 0
	for _, n := range names {
		r := diagnoseShallowProfile(mgr, n)
		if !r.Healthy {
			unhealthy++
		}
		results = append(results, r)
	}
	allHealthy := unhealthy == 0

	if jsonOut {
		enc := json.NewEncoder(cmd.OutOrStdout())
		enc.SetIndent("", "  ")
		_ = enc.Encode(shallowDoctorOutput{Profiles: results, Healthy: allHealthy})
		if !allHealthy {
			cmd.SilenceErrors = true
			cmd.SilenceUsage = true
			if single {
				return fmt.Errorf("shallow profile %q is unhealthy", args[0])
			}
			return fmt.Errorf("%d of %d shallow profiles are unhealthy", unhealthy, len(results))
		}
		return nil
	}

	// HUMAN output.
	if !single && len(results) == 0 {
		fmt.Fprintf(cmd.OutOrStdout(), "No shallow profiles found in %s — nothing to check.\n", mgr.BaseDir())
		fmt.Fprintln(cmd.OutOrStdout(), "Create one with:")
		fmt.Fprintln(cmd.OutOrStdout(), "  caam shallow-profile create <name> --from-vault <tool>/<profile>")
		return nil
	}
	for _, r := range results {
		if r.Healthy {
			fmt.Fprintf(cmd.OutOrStdout(), "✓ %s (%s): healthy — ready to spawn\n", r.Name, r.Provider)
			continue
		}
		if r.Provider != "" {
			fmt.Fprintf(cmd.OutOrStdout(), "✗ %s (%s): %s\n", r.Name, r.Provider, r.Error)
		} else {
			fmt.Fprintf(cmd.OutOrStdout(), "✗ %s: %s\n", r.Name, r.Error)
		}
	}

	if !allHealthy {
		cmd.SilenceErrors = true
		cmd.SilenceUsage = true
		if single {
			return fmt.Errorf("shallow profile %q is unhealthy", args[0])
		}
		return fmt.Errorf("%d of %d shallow profiles are unhealthy", unhealthy, len(results))
	}
	return nil
}

// shallowSpawnCmd sets HOME=<orch-homes>/<name> (plus the provider's env) and
// execs the requested command.
var shallowSpawnCmd = &cobra.Command{
	Use:   "shallow-spawn <name> -- <cmd> [args...]",
	Short: "Run a command under a shallow profile's HOME",
	Long: `Set HOME (and the recorded provider's env, e.g. CODEX_HOME) to the named
shallow profile and exec the given command. The provider is read from the
profile's metadata — no provider flag is needed. Concurrent invocations under
different names hit independent credential files and can run truly in parallel.

Examples:
  caam shallow-spawn alice -- claude
  caam shallow-spawn cbob  -- codex
  caam shallow-spawn alice -- claude --print "explain this codebase"
  caam shallow-spawn alice -- bash -c 'echo $HOME'

Use 'caam shallow-spawn <name> --print-env' to print eval-able shell statements
(export KEY='value' for the vars set, unset KEY for the vars cleared) that
WOULD be applied without executing anything (useful for shell wrappers:
eval "$(caam shallow-spawn <name> --print-env)").

Note: on a malformed or missing profile, --print-env prints NOTHING and exits
non-zero, so wrappers should check $? before eval'ing its output:
out="$(caam shallow-spawn <name> --print-env)" && eval "$out".

Pass --json to get machine-readable output instead: errors become
{"success":false,"error":...} on stdout, and --print-env --json emits
{"success":true,"home":...,"shallow_profile":...,"set":{...},"unset":[...]}.`,
	Args: cobra.MinimumNArgs(1),
	RunE: runShallowSpawn,
}

func init() {
	shallowSpawnCmd.Flags().String("base", "", "shallow profiles base dir")
	shallowSpawnCmd.Flags().Bool("print-env", false, "print eval-able export/unset statements and exit (no exec)")
	shallowSpawnCmd.Flags().Bool("json", false, "output as JSON (errors and --print-env)")
	shallowSpawnCmd.Flags().Bool("reload-daemon", false, "for codex: SIGTERM a running codex app-server/mcp-server daemon so the switched auth takes effect (it respawns on next use)")
}

// shallowSpawnPrintEnvOutput is the --print-env --json shape: the env transform
// rendered as data instead of shell statements.
type shallowSpawnPrintEnvOutput struct {
	Success        bool              `json:"success"`
	Home           string            `json:"home"`
	ShallowProfile string            `json:"shallow_profile"`
	Set            map[string]string `json:"set"`
	Unset          []string          `json:"unset"`
}

func runShallowSpawn(cmd *cobra.Command, args []string) error {
	name := args[0]
	rest := args[1:]
	printEnv, _ := cmd.Flags().GetBool("print-env")
	jsonOut, _ := cmd.Flags().GetBool("json")
	reloadDaemon, _ := cmd.Flags().GetBool("reload-daemon")

	// emit surfaces a pre-exec error as {"success":false,"error":...} on stdout
	// when --json is set, then silences cobra and still returns the error so the
	// process exits non-zero (automation checking $? isn't misled); otherwise it
	// returns the bare error for the human/exit-code path.
	emit := func(err error) error {
		if jsonOut {
			out := struct {
				Success bool   `json:"success"`
				Error   string `json:"error"`
			}{Success: false, Error: err.Error()}
			enc := json.NewEncoder(cmd.OutOrStdout())
			enc.SetIndent("", "  ")
			_ = enc.Encode(out)
			cmd.SilenceErrors = true
			cmd.SilenceUsage = true
			return err
		}
		return err
	}

	mgr, err := resolveShallowManager(cmd)
	if err != nil {
		return emit(fmt.Errorf("init shallow manager: %w", err))
	}

	prof, err := mgr.Get(name)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return emit(fmt.Errorf("shallow profile %q does not exist (try `caam shallow-profile create %s`)", name, name))
		}
		return emit(fmt.Errorf("load shallow profile: %w", err))
	}

	// STRICT: a profile with missing/unreadable metadata (Meta == nil) or no
	// recorded provider is malformed — refuse, never silently assume Claude. A
	// Codex profile mis-run as Claude would skip CODEX_HOME and read the real
	// ~/.codex auth. Emit spawn-specific messages (don't wrap the engine's
	// LayoutForProvider error — its wording differs and tests depend on the
	// exact spawn phrasing).
	if prof.Meta == nil || prof.Meta.Provider == "" {
		return emit(fmt.Errorf("shallow profile %q has no recorded provider (missing or malformed metadata); recreate it", name))
	}
	layout, err := shallow.LayoutForProvider(prof.Meta.Provider) // strict: empty/unknown → error
	if err != nil {
		return emit(fmt.Errorf("shallow profile %q uses unsupported provider %q (supported: %s)",
			name, prof.Meta.Provider, strings.Join(shallow.SupportedProviders(), ", ")))
	}

	// Full pre-spawn integrity check, BEFORE both --print-env and exec: every
	// real dir / real file / required credential must be present, regular, and
	// free of a symlinked ancestor. This refuses a profile whose .codex/.claude
	// directory was swapped for a symlink (which would repoint the session at
	// another identity's real auth via HOME/CODEX_HOME) — Lstat on the leaf alone
	// would miss that because it follows symlinked parents.
	if err := mgr.ValidateProfileShape(name, layout); err != nil {
		return emit(err)
	}

	if printEnv {
		if jsonOut {
			// Render the env transform as data: `set` mirrors SpawnEnv's writes
			// (HOME/SHALLOW_PROFILE + provider sets), `unset` is parsed from the
			// SpawnEnvLines `unset KEY` lines (single source of truth for the
			// cleared-but-not-reset vars).
			set := map[string]string{}
			layout.SpawnEnv(prof.Path, name, set)
			var unset []string
			for _, line := range layout.SpawnEnvLines(prof.Path, name) {
				if k, ok := strings.CutPrefix(line, "unset "); ok {
					unset = append(unset, k)
				}
			}
			out := shallowSpawnPrintEnvOutput{
				Success:        true,
				Home:           prof.Path,
				ShallowProfile: name,
				Set:            set,
				Unset:          unset,
			}
			enc := json.NewEncoder(cmd.OutOrStdout())
			enc.SetIndent("", "  ")
			return enc.Encode(out)
		}
		// SpawnEnvLines is the single source of truth shared with the exec path:
		// export KEY='value' for HOME/SHALLOW_PROFILE/provider-sets, then
		// `unset KEY` for every cleared var not re-set — so a shell wrapper built
		// from --print-env reproduces the exec path's isolation.
		for _, line := range layout.SpawnEnvLines(prof.Path, name) {
			fmt.Fprintln(cmd.OutOrStdout(), line)
		}
		return nil
	}

	if len(rest) == 0 {
		return emit(fmt.Errorf("missing command after %q (use `caam shallow-spawn %s -- %s`)", name, name, layout.DefaultBin))
	}

	binPath, err := exec.LookPath(rest[0])
	if err != nil {
		return emit(fmt.Errorf("lookup %q: %w", rest[0], err))
	}

	// Build environment: inherit, then apply the layout's spawn transform
	// (deletes the cleared vars, sets HOME/SHALLOW_PROFILE + provider sets).
	envMap := make(map[string]string, len(os.Environ())+4)
	for _, e := range os.Environ() {
		if idx := strings.IndexByte(e, '='); idx > 0 {
			envMap[e[:idx]] = e[idx+1:]
		}
	}
	layout.SpawnEnv(prof.Path, name, envMap)
	envSlice := make([]string, 0, len(envMap))
	for k, v := range envMap {
		envSlice = append(envSlice, k+"="+v)
	}

	daemonWarn := runShallowCodexDaemonCheck(prof.Meta.Provider, reloadDaemon)
	printShallowCodexDaemonWarning(cmd.ErrOrStderr(), daemonWarn)

	// On Unix, exec the target so signals/exit propagate naturally and we don't
	// add a stray caam process to the tree.
	return spawnExec(binPath, rest, envSlice)
}

var runShallowCodexDaemonCheck = checkCodexDaemon

var printShallowCodexDaemonWarning = printCodexDaemonWarning

// spawnExec replaces the current process image with the target on Unix.
// Wrapped in a function so tests can inject a fake.
var spawnExec = func(binPath string, args []string, env []string) error {
	return syscall.Exec(binPath, args, env)
}
