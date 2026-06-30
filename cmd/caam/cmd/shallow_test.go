package cmd

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/Dicklesworthstone/coding_agent_account_manager/internal/authfile"
	"github.com/Dicklesworthstone/coding_agent_account_manager/internal/shallow"
	"github.com/spf13/cobra"
)

// fakeShallowHome lays down a representative real HOME inside t.TempDir() and
// returns its path. Mirrors internal/shallow's helper but local to this pkg.
func fakeShallowHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	for _, f := range []string{".bashrc", ".zshrc", ".gitconfig"} {
		if err := os.WriteFile(filepath.Join(home, f), []byte("# "+f+"\n"), 0o644); err != nil {
			t.Fatalf("seed %s: %v", f, err)
		}
	}
	for _, d := range []string{".ssh", ".config"} {
		if err := os.MkdirAll(filepath.Join(home, d), 0o700); err != nil {
			t.Fatalf("mkdir %s: %v", d, err)
		}
	}
	claudeDir := filepath.Join(home, ".claude")
	if err := os.MkdirAll(filepath.Join(claudeDir, "projects"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".claude.json"), []byte(`{"x":1}`), 0o600); err != nil {
		t.Fatal(err)
	}
	// Seed a representative ~/.codex so codex shallow profiles have a realistic
	// real HOME to mirror from.
	codexDir := filepath.Join(home, ".codex")
	if err := os.MkdirAll(codexDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(codexDir, "auth.json"), []byte(`{"real":"codex"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(codexDir, "config.toml"), []byte("# real config\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return home
}

// shallowEnv sets up isolated CAAM_HOME, real HOME, and shallow base dir for
// a single test, returning the shallow base path and the fake real HOME path.
func shallowEnv(t *testing.T) (basePath, realHome string) {
	t.Helper()
	realHome = fakeShallowHome(t)
	basePath = filepath.Join(t.TempDir(), "orch-homes")
	caamHome := t.TempDir()
	t.Setenv("CAAM_HOME", caamHome)
	t.Setenv("HOME", realHome)
	t.Setenv("CAAM_SHALLOW_HOMES_DIR", basePath)
	// Reinitialize the package-level vault so resolveVaultCredential can read it.
	vault = authfile.NewVault(authfile.DefaultVaultPath())
	return basePath, realHome
}

// runCmdCaptured executes a single subcommand path against a fresh root command
// (we can't reuse the package-level rootCmd because cobra retains flag state
// between invocations and other tests already wire it). It returns stdout/stderr.
func runCmdCaptured(t *testing.T, args ...string) (string, string, error) {
	t.Helper()
	// Build a minimal command tree exposing our shallow surface. Cobra trees
	// are cheap and this avoids cross-test flag pollution.
	root := newShallowTestRoot()
	var stdout, stderr bytes.Buffer
	root.SetOut(&stdout)
	root.SetErr(&stderr)
	root.SetArgs(args)
	err := root.Execute()
	return stdout.String(), stderr.String(), err
}

// newShallowTestRoot builds a fresh command tree that mirrors how root.go
// wires the shallow commands, but without the rest of caam's persistent
// pre-run hooks (which would try to migrate data, register providers, etc.).
func newShallowTestRoot() *cobra.Command {
	root := &cobra.Command{Use: "caam-test"}
	// Re-create the same surface; we reuse the existing var pointers' fields
	// only via a small clone helper to keep flag state pristine per call.
	// Easiest: build a fresh tree from scratch using the same RunE funcs.
	create := &cobra.Command{
		Use:  "create <name>",
		Args: shallowProfileCreateCmd.Args,
		RunE: shallowProfileCreateCmd.RunE,
	}
	create.Flags().String("from-vault", "", "")
	create.Flags().String("from-file", "", "")
	create.Flags().String("from-claude-json", "", "")
	create.Flags().String("tool", "", "")
	create.Flags().Bool("force", false, "")
	create.Flags().Bool("json", false, "")

	list := &cobra.Command{
		Use:  "list",
		RunE: shallowProfileListCmd.RunE,
	}
	list.Flags().Bool("json", false, "")

	del := &cobra.Command{
		Use:  "delete <name>",
		Args: shallowProfileDeleteCmd.Args,
		RunE: shallowProfileDeleteCmd.RunE,
	}
	del.Flags().Bool("force", false, "")
	del.Flags().Bool("json", false, "")

	doctor := &cobra.Command{
		Use:  "doctor [name]",
		Args: shallowProfileDoctorCmd.Args,
		RunE: shallowProfileDoctorCmd.RunE,
	}
	doctor.Flags().Bool("json", false, "")

	parent := &cobra.Command{Use: "shallow-profile"}
	parent.PersistentFlags().String("base", "", "")
	parent.AddCommand(create, list, del, doctor)

	spawn := &cobra.Command{
		Use:  "shallow-spawn <name> -- <cmd>",
		Args: shallowSpawnCmd.Args,
		RunE: shallowSpawnCmd.RunE,
	}
	spawn.Flags().String("base", "", "")
	spawn.Flags().Bool("print-env", false, "")
	spawn.Flags().Bool("json", false, "")

	root.AddCommand(parent)
	root.AddCommand(spawn)
	return root
}

func TestShallowCreateAndList_JSON(t *testing.T) {
	base, _ := shallowEnv(t)

	// Stage a vault profile so --from-vault works.
	caamHome := os.Getenv("CAAM_HOME")
	vaultDir := filepath.Join(caamHome, "data", "vault", "claude", "alice")
	if err := os.MkdirAll(vaultDir, 0o700); err != nil {
		t.Fatal(err)
	}
	credBody := `{"claudeAiOauth":{"accessToken":"fake","refreshToken":"r"}}`
	if err := os.WriteFile(filepath.Join(vaultDir, ".credentials.json"), []byte(credBody), 0o600); err != nil {
		t.Fatal(err)
	}

	// Create the shallow profile via the CLI surface.
	stdout, _, err := runCmdCaptured(t, "shallow-profile", "create", "alice", "--from-vault", "claude/alice", "--json")
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	var createResp struct {
		Success        bool   `json:"success"`
		Name           string `json:"name"`
		Path           string `json:"path"`
		CredentialFrom string `json:"credential_from"`
	}
	if err := json.Unmarshal([]byte(stdout), &createResp); err != nil {
		t.Fatalf("unmarshal create JSON %q: %v", stdout, err)
	}
	if !createResp.Success || createResp.Name != "alice" {
		t.Fatalf("unexpected response: %+v", createResp)
	}
	if createResp.CredentialFrom != "vault:claude/alice" {
		t.Fatalf("CredentialFrom %q", createResp.CredentialFrom)
	}
	wantPath := filepath.Join(base, "alice")
	if createResp.Path != wantPath {
		t.Fatalf("path %q != %q", createResp.Path, wantPath)
	}

	// Verify credentials file contents and perms.
	credDst := filepath.Join(wantPath, ".claude", ".credentials.json")
	gotBody, err := os.ReadFile(credDst)
	if err != nil {
		t.Fatalf("read creds: %v", err)
	}
	if string(gotBody) != credBody {
		t.Fatalf("creds mismatch: %q", gotBody)
	}
	if st, err := os.Stat(credDst); err != nil {
		t.Fatal(err)
	} else if st.Mode().Perm() != 0o600 {
		t.Fatalf("creds perm %v", st.Mode().Perm())
	}

	// List should include alice.
	stdout, _, err = runCmdCaptured(t, "shallow-profile", "list", "--json")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	var listResp struct {
		BaseDir  string `json:"base_dir"`
		Count    int    `json:"count"`
		Profiles []struct {
			Name           string `json:"name"`
			Path           string `json:"path"`
			CredentialFrom string `json:"credential_from"`
		} `json:"profiles"`
	}
	if err := json.Unmarshal([]byte(stdout), &listResp); err != nil {
		t.Fatalf("unmarshal list: %v", err)
	}
	if listResp.Count != 1 || len(listResp.Profiles) != 1 {
		t.Fatalf("unexpected list: %+v", listResp)
	}
	if listResp.Profiles[0].Name != "alice" {
		t.Fatalf("listed name %q", listResp.Profiles[0].Name)
	}
	if listResp.BaseDir != base {
		t.Fatalf("listed BaseDir %q != %q", listResp.BaseDir, base)
	}
}

func TestShallowCreateFromFile(t *testing.T) {
	_, _ = shallowEnv(t)

	srcDir := t.TempDir()
	src := filepath.Join(srcDir, "creds.json")
	if err := os.WriteFile(src, []byte(`{"from":"file"}`), 0o600); err != nil {
		t.Fatal(err)
	}

	stdout, _, err := runCmdCaptured(t, "shallow-profile", "create", "bob", "--from-file", src, "--json")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if !strings.Contains(stdout, `"success": true`) {
		t.Fatalf("expected success in %q", stdout)
	}
}

func TestShallowCreateRejectsBothCredentialSources(t *testing.T) {
	_, _ = shallowEnv(t)
	stdout, _, err := runCmdCaptured(t, "shallow-profile", "create", "alice",
		"--from-vault", "claude/alice", "--from-file", "/tmp/nope", "--json")
	if err == nil {
		t.Fatalf("expected non-zero exit on error, got nil; stdout=%q", stdout)
	}
	if !strings.Contains(stdout, "mutually exclusive") {
		t.Fatalf("expected mutual-exclusivity error, got %q", stdout)
	}
}

func TestShallowCreateUnknownVaultProfile(t *testing.T) {
	_, _ = shallowEnv(t)
	stdout, _, err := runCmdCaptured(t, "shallow-profile", "create", "alice",
		"--from-vault", "claude/missing", "--json")
	if err == nil {
		t.Fatalf("expected non-zero exit on error, got nil; stdout=%q", stdout)
	}
	if !strings.Contains(stdout, "missing .credentials.json") {
		t.Fatalf("expected missing-creds error, got %q", stdout)
	}
}

func TestShallowDelete(t *testing.T) {
	base, _ := shallowEnv(t)
	if _, _, err := runCmdCaptured(t, "shallow-profile", "create", "alice", "--json"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(base, "alice")); err != nil {
		t.Fatalf("expected profile dir: %v", err)
	}
	if _, _, err := runCmdCaptured(t, "shallow-profile", "delete", "alice", "--force", "--json"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(base, "alice")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("profile dir still exists after delete: err=%v", err)
	}
}

func TestShallowDeleteNotFound(t *testing.T) {
	_, _ = shallowEnv(t)
	stdout, _, err := runCmdCaptured(t, "shallow-profile", "delete", "ghost", "--force", "--json")
	if err == nil {
		t.Fatalf("expected non-zero exit on error, got nil; stdout=%q", stdout)
	}
	if !strings.Contains(stdout, "does not exist") {
		t.Fatalf("expected not-exist error, got %q", stdout)
	}
}

// TestShallowSpawnPrintEnv verifies that --print-env emits the right HOME
// without exec'ing anything.
func TestShallowSpawnPrintEnv(t *testing.T) {
	base, _ := shallowEnv(t)
	if _, _, err := runCmdCaptured(t, "shallow-profile", "create", "alice", "--json"); err != nil {
		t.Fatal(err)
	}
	stdout, _, err := runCmdCaptured(t, "shallow-spawn", "alice", "--print-env")
	if err != nil {
		t.Fatalf("spawn print-env: %v", err)
	}
	wantHome := filepath.Join(base, "alice")
	if !strings.Contains(stdout, "export HOME='"+wantHome+"'") {
		t.Fatalf("expected exported HOME assignment in %q", stdout)
	}
	if !strings.Contains(stdout, "export SHALLOW_PROFILE='alice'") {
		t.Fatalf("expected exported SHALLOW_PROFILE in %q", stdout)
	}
}

// TestShallowSpawnExecsCorrectHome injects a fake spawnExec to verify that
// runShallowSpawn sets HOME correctly and forwards args+bin.
func TestShallowSpawnExecsCorrectHome(t *testing.T) {
	base, _ := shallowEnv(t)
	if _, _, err := runCmdCaptured(t, "shallow-profile", "create", "alice", "--json"); err != nil {
		t.Fatal(err)
	}

	// Pick a real binary (sh is universal on Unix). LookPath needs it on $PATH.
	type captured struct {
		bin  string
		args []string
		env  []string
	}
	var got captured
	origExec := spawnExec
	spawnExec = func(bin string, args []string, env []string) error {
		got = captured{bin: bin, args: args, env: env}
		return nil
	}
	t.Cleanup(func() { spawnExec = origExec })

	if _, _, err := runCmdCaptured(t, "shallow-spawn", "alice", "--", "sh", "-c", "echo hi"); err != nil {
		t.Fatalf("spawn: %v", err)
	}
	if !strings.HasSuffix(got.bin, "/sh") && got.bin != "sh" {
		t.Fatalf("unexpected bin: %q", got.bin)
	}
	if len(got.args) != 3 || got.args[0] != "sh" || got.args[1] != "-c" || got.args[2] != "echo hi" {
		t.Fatalf("unexpected args: %v", got.args)
	}
	wantHome := "HOME=" + filepath.Join(base, "alice")
	wantProfile := "SHALLOW_PROFILE=alice"
	foundHome, foundProfile := false, false
	var sawClaudeCfg bool
	for _, e := range got.env {
		if e == wantHome {
			foundHome = true
		}
		if e == wantProfile {
			foundProfile = true
		}
		if strings.HasPrefix(e, "CLAUDE_CONFIG_DIR=") {
			sawClaudeCfg = true
		}
	}
	if !foundHome {
		t.Fatalf("env missing %s; got %v", wantHome, got.env)
	}
	if !foundProfile {
		t.Fatalf("env missing %s; got %v", wantProfile, got.env)
	}
	if sawClaudeCfg {
		t.Fatalf("CLAUDE_CONFIG_DIR should be stripped, got %v", got.env)
	}
}

// TestShallowSpawnUnknownProfile must produce a clear error.
func TestShallowSpawnUnknownProfile(t *testing.T) {
	_, _ = shallowEnv(t)
	_, _, err := runCmdCaptured(t, "shallow-spawn", "ghost", "--", "sh")
	if err == nil {
		t.Fatalf("expected error")
	}
	if !strings.Contains(err.Error(), "does not exist") {
		t.Fatalf("unexpected error: %v", err)
	}
}

// TestShallowSpawnNoCommand requires a command after the profile name.
func TestShallowSpawnNoCommand(t *testing.T) {
	_, _ = shallowEnv(t)
	if _, _, err := runCmdCaptured(t, "shallow-profile", "create", "alice", "--json"); err != nil {
		t.Fatal(err)
	}
	_, _, err := runCmdCaptured(t, "shallow-spawn", "alice")
	if err == nil {
		t.Fatalf("expected error when no command provided")
	}
}

// stageCodexVault writes a Codex auth.json into the vault profile dir for the
// given profile and returns its content.
func stageCodexVault(t *testing.T, profile string) string {
	t.Helper()
	caamHome := os.Getenv("CAAM_HOME")
	vaultDir := filepath.Join(caamHome, "data", "vault", "codex", profile)
	if err := os.MkdirAll(vaultDir, 0o700); err != nil {
		t.Fatal(err)
	}
	body := `{"OPENAI_API_KEY":null,"tokens":{"access":"codex-fake"}}`
	if err := os.WriteFile(filepath.Join(vaultDir, "auth.json"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return body
}

// TestShallowCreateCodexFromVault stages a codex vault profile and creates a
// shallow profile from it, asserting provider/credential routing and the
// codex config.toml normalization.
func TestShallowCreateCodexFromVault(t *testing.T) {
	base, _ := shallowEnv(t)
	body := stageCodexVault(t, "alice")

	stdout, _, err := runCmdCaptured(t, "shallow-profile", "create", "codex-alice",
		"--from-vault", "codex/alice", "--json")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	var resp struct {
		Success        bool   `json:"success"`
		Provider       string `json:"provider"`
		CredentialFrom string `json:"credential_from"`
	}
	if err := json.Unmarshal([]byte(stdout), &resp); err != nil {
		t.Fatalf("unmarshal %q: %v", stdout, err)
	}
	if !resp.Success {
		t.Fatalf("expected success: %q", stdout)
	}
	if resp.Provider != "codex" {
		t.Fatalf("provider %q != codex", resp.Provider)
	}
	if resp.CredentialFrom != "vault:codex/alice" {
		t.Fatalf("credential_from %q", resp.CredentialFrom)
	}

	homePath := filepath.Join(base, "codex-alice")
	gotAuth, err := os.ReadFile(filepath.Join(homePath, ".codex", "auth.json"))
	if err != nil {
		t.Fatalf("read codex auth.json: %v", err)
	}
	if string(gotAuth) != body {
		t.Fatalf("auth.json content %q != %q", gotAuth, body)
	}
	cfg, err := os.ReadFile(filepath.Join(homePath, ".codex", "config.toml"))
	if err != nil {
		t.Fatalf("read config.toml: %v", err)
	}
	if !strings.Contains(string(cfg), `cli_auth_credentials_store = "file"`) {
		t.Fatalf("config.toml missing credential store directive: %q", cfg)
	}
}

// TestShallowCreateCodexFromFile copies an arbitrary codex auth.json via
// --tool codex --from-file.
func TestShallowCreateCodexFromFile(t *testing.T) {
	base, _ := shallowEnv(t)
	srcDir := t.TempDir()
	src := filepath.Join(srcDir, "auth.json")
	body := `{"from":"file-codex"}`
	if err := os.WriteFile(src, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}

	stdout, _, err := runCmdCaptured(t, "shallow-profile", "create", "cbob",
		"--tool", "codex", "--from-file", src, "--json")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	var resp struct {
		Success  bool   `json:"success"`
		Provider string `json:"provider"`
	}
	if err := json.Unmarshal([]byte(stdout), &resp); err != nil {
		t.Fatalf("unmarshal %q: %v", stdout, err)
	}
	if !resp.Success || resp.Provider != "codex" {
		t.Fatalf("unexpected response %+v (%q)", resp, stdout)
	}
	gotAuth, err := os.ReadFile(filepath.Join(base, "cbob", ".codex", "auth.json"))
	if err != nil {
		t.Fatalf("read codex auth.json: %v", err)
	}
	if string(gotAuth) != body {
		t.Fatalf("auth.json content %q != %q", gotAuth, body)
	}
}

// TestShallowCreateVaultToolMismatch rejects a --tool that disagrees with the
// --from-vault tool prefix.
func TestShallowCreateVaultToolMismatch(t *testing.T) {
	_, _ = shallowEnv(t)
	stdout, _, err := runCmdCaptured(t, "shallow-profile", "create", "bad",
		"--tool", "codex", "--from-vault", "claude/alice", "--json")
	if err == nil {
		t.Fatalf("expected non-zero exit on error, got nil; stdout=%q", stdout)
	}
	var resp struct {
		Success bool   `json:"success"`
		Error   string `json:"error"`
	}
	if err := json.Unmarshal([]byte(stdout), &resp); err != nil {
		t.Fatalf("unmarshal %q: %v", stdout, err)
	}
	if resp.Success {
		t.Fatalf("expected failure, got %q", stdout)
	}
	if !strings.Contains(resp.Error, `--tool "codex" does not match --from-vault tool "claude"`) {
		t.Fatalf("expected tool-mismatch error, got %q", resp.Error)
	}
}

// TestShallowCreateUnsupportedVaultTool rejects an unsupported provider implied
// by --from-vault.
func TestShallowCreateUnsupportedVaultTool(t *testing.T) {
	_, _ = shallowEnv(t)
	stdout, _, err := runCmdCaptured(t, "shallow-profile", "create", "bad",
		"--from-vault", "gemini/alice", "--json")
	if err == nil {
		t.Fatalf("expected non-zero exit on error, got nil; stdout=%q", stdout)
	}
	if !strings.Contains(stdout, "not supported for shallow profiles (supported: claude, codex)") {
		t.Fatalf("expected unsupported-tool error, got %q", stdout)
	}
}

// TestShallowSpawnPrintEnvCodex verifies the codex --print-env output sets the
// codex env vars and unsets the repointing/override/discovery vars.
func TestShallowSpawnPrintEnvCodex(t *testing.T) {
	base, _ := shallowEnv(t)
	if _, _, err := runCmdCaptured(t, "shallow-profile", "create", "codex-alice",
		"--tool", "codex", "--json"); err != nil {
		t.Fatal(err)
	}
	stdout, _, err := runCmdCaptured(t, "shallow-spawn", "codex-alice", "--print-env")
	if err != nil {
		t.Fatalf("spawn print-env: %v", err)
	}
	homePath := filepath.Join(base, "codex-alice")
	codexDir := filepath.Join(homePath, ".codex")
	mustContain := []string{
		"export CODEX_HOME='" + codexDir + "'",
		"export CODEX_SQLITE_HOME='" + codexDir + "'",
		"export HOME='" + homePath + "'",
		"export SHALLOW_PROFILE='codex-alice'",
		"unset CLAUDE_CONFIG_DIR",
		"unset OPENAI_API_KEY",
		"unset CAAM_HOME",
	}
	for _, want := range mustContain {
		if !strings.Contains(stdout, want) {
			t.Fatalf("print-env missing %q in:\n%s", want, stdout)
		}
	}
	// CODEX_HOME / CODEX_SQLITE_HOME are SET, so they must not also be unset.
	for _, bad := range []string{"unset CODEX_HOME", "unset CODEX_SQLITE_HOME"} {
		if strings.Contains(stdout, bad) {
			t.Fatalf("print-env unexpectedly contains %q in:\n%s", bad, stdout)
		}
	}
}

// TestShallowSpawnCodexStripsLeakyEnv asserts the exec env is repointed to the
// shallow codex dir and that repointing/override/discovery vars are stripped.
func TestShallowSpawnCodexStripsLeakyEnv(t *testing.T) {
	base, _ := shallowEnv(t)
	t.Setenv("CODEX_HOME", "/bogus")
	t.Setenv("GEMINI_HOME", "/bogus")
	t.Setenv("CLAUDE_CONFIG_DIR", "/bogus")
	t.Setenv("OPENAI_API_KEY", "sk-bogus")
	t.Setenv("CODEX_API_KEY", "bogus")
	t.Setenv("CAAM_HOME", "/bogus")

	if _, _, err := runCmdCaptured(t, "shallow-profile", "create", "codex-alice",
		"--tool", "codex", "--json"); err != nil {
		t.Fatal(err)
	}

	var gotEnv []string
	origExec := spawnExec
	spawnExec = func(bin string, args []string, env []string) error {
		gotEnv = env
		return nil
	}
	t.Cleanup(func() { spawnExec = origExec })

	if _, _, err := runCmdCaptured(t, "shallow-spawn", "codex-alice", "--", "sh", "-c", "echo hi"); err != nil {
		t.Fatalf("spawn: %v", err)
	}

	wantCodexHome := "CODEX_HOME=" + filepath.Join(base, "codex-alice", ".codex")
	foundCodexHome := false
	forbidden := map[string]bool{
		"GEMINI_HOME": false, "CLAUDE_CONFIG_DIR": false,
		"OPENAI_API_KEY": false, "CODEX_API_KEY": false, "CAAM_HOME": false,
	}
	for _, e := range gotEnv {
		if e == wantCodexHome {
			foundCodexHome = true
		}
		if idx := strings.IndexByte(e, '='); idx > 0 {
			if _, ok := forbidden[e[:idx]]; ok {
				forbidden[e[:idx]] = true
			}
		}
	}
	if !foundCodexHome {
		t.Fatalf("env missing %s; got %v", wantCodexHome, gotEnv)
	}
	for k, present := range forbidden {
		if present {
			t.Fatalf("env should not contain %s; got %v", k, gotEnv)
		}
	}
}

// TestShallowListIncludesProvider verifies the list JSON reports each profile's
// provider.
func TestShallowListIncludesProvider(t *testing.T) {
	_, _ = shallowEnv(t)
	if _, _, err := runCmdCaptured(t, "shallow-profile", "create", "alice", "--json"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := runCmdCaptured(t, "shallow-profile", "create", "cbob",
		"--tool", "codex", "--json"); err != nil {
		t.Fatal(err)
	}
	stdout, _, err := runCmdCaptured(t, "shallow-profile", "list", "--json")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	var resp struct {
		Profiles []struct {
			Name     string `json:"name"`
			Provider string `json:"provider"`
		} `json:"profiles"`
	}
	if err := json.Unmarshal([]byte(stdout), &resp); err != nil {
		t.Fatalf("unmarshal %q: %v", stdout, err)
	}
	got := map[string]string{}
	for _, p := range resp.Profiles {
		got[p.Name] = p.Provider
	}
	if got["alice"] != "claude" {
		t.Fatalf("alice provider %q != claude (%q)", got["alice"], stdout)
	}
	if got["cbob"] != "codex" {
		t.Fatalf("cbob provider %q != codex (%q)", got["cbob"], stdout)
	}
}

// TestShallowCreateManagedFilesJSON asserts the create JSON managed_files list
// includes the codex auth/config and the meta sidecar.
func TestShallowCreateManagedFilesJSON(t *testing.T) {
	base, _ := shallowEnv(t)
	stdout, _, err := runCmdCaptured(t, "shallow-profile", "create", "cbob",
		"--tool", "codex", "--json")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	var resp struct {
		ManagedFiles []string `json:"managed_files"`
	}
	if err := json.Unmarshal([]byte(stdout), &resp); err != nil {
		t.Fatalf("unmarshal %q: %v", stdout, err)
	}
	homePath := filepath.Join(base, "cbob")
	wantSet := map[string]bool{
		filepath.Join(homePath, ".codex", "auth.json"):   false,
		filepath.Join(homePath, ".codex", "config.toml"): false,
		filepath.Join(homePath, ".caam-shallow.json"):    false,
	}
	for _, f := range resp.ManagedFiles {
		if _, ok := wantSet[f]; ok {
			wantSet[f] = true
		}
	}
	for f, found := range wantSet {
		if !found {
			t.Fatalf("managed_files missing %q; got %v", f, resp.ManagedFiles)
		}
	}
}

// TestShallowSpawnRejectsMalformedMeta verifies spawn refuses profiles whose
// metadata records no provider or an unsupported provider, never silently
// falling back to a Claude env.
func TestShallowSpawnRejectsMalformedMeta(t *testing.T) {
	base, _ := shallowEnv(t)
	metaPath := func(name string) string {
		return filepath.Join(base, name, ".caam-shallow.json")
	}

	// (a) missing metadata entirely.
	if _, _, err := runCmdCaptured(t, "shallow-profile", "create", "nometa", "--json"); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(metaPath("nometa")); err != nil {
		t.Fatal(err)
	}
	_, _, err := runCmdCaptured(t, "shallow-spawn", "nometa", "--print-env")
	if err == nil || !strings.Contains(err.Error(), "has no recorded provider") {
		t.Fatalf("missing-meta: expected 'has no recorded provider', got %v", err)
	}

	// (b) provider explicitly empty.
	if _, _, err := runCmdCaptured(t, "shallow-profile", "create", "emptyprov", "--json"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(metaPath("emptyprov"),
		[]byte(`{"name":"emptyprov","provider":"","version":2}`), 0o600); err != nil {
		t.Fatal(err)
	}
	_, _, err = runCmdCaptured(t, "shallow-spawn", "emptyprov", "--print-env")
	if err == nil || !strings.Contains(err.Error(), "has no recorded provider") {
		t.Fatalf("empty-provider: expected 'has no recorded provider', got %v", err)
	}

	// (c) provider set to an unsupported value.
	if _, _, err := runCmdCaptured(t, "shallow-profile", "create", "geminiprov", "--json"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(metaPath("geminiprov"),
		[]byte(`{"name":"geminiprov","provider":"gemini","version":2}`), 0o600); err != nil {
		t.Fatal(err)
	}
	_, _, err = runCmdCaptured(t, "shallow-spawn", "geminiprov", "--print-env")
	if err == nil || !strings.Contains(err.Error(), `uses unsupported provider "gemini"`) {
		t.Fatalf("gemini-provider: expected 'uses unsupported provider \"gemini\"', got %v", err)
	}
}

// TestShallowProvidersSubsetOfTools asserts every shallow provider id is also a
// key in the package-level auth-swap tools map, so a shallow harness always has
// a corresponding auth file set.
func TestShallowProvidersSubsetOfTools(t *testing.T) {
	for _, p := range shallow.SupportedProviders() {
		if _, ok := tools[p]; !ok {
			t.Fatalf("shallow provider %q is not a key of the tools map", p)
		}
	}
}

// TestShallowLayoutVaultNamesMatchAuthFiles asserts each layout's Primary
// VaultName matches the basename of the corresponding authfile credential, so
// --from-vault reads the file the rest of caam writes.
func TestShallowLayoutVaultNamesMatchAuthFiles(t *testing.T) {
	codexLayout, err := shallow.LayoutForProvider("codex")
	if err != nil {
		t.Fatal(err)
	}
	wantCodex := filepath.Base(authfile.CodexAuthFiles().Files[0].Path)
	if codexLayout.Primary().VaultName != wantCodex {
		t.Fatalf("codex VaultName %q != authfile basename %q", codexLayout.Primary().VaultName, wantCodex)
	}
	if wantCodex != "auth.json" {
		t.Fatalf("unexpected codex authfile basename %q", wantCodex)
	}

	claudeLayout, err := shallow.LayoutForProvider("claude")
	if err != nil {
		t.Fatal(err)
	}
	wantClaude := filepath.Base(authfile.ClaudeAuthFiles().Files[0].Path)
	if claudeLayout.Primary().VaultName != wantClaude {
		t.Fatalf("claude VaultName %q != authfile basename %q", claudeLayout.Primary().VaultName, wantClaude)
	}
	if wantClaude != ".credentials.json" {
		t.Fatalf("unexpected claude authfile basename %q", wantClaude)
	}
}

// TestShallowListJSONError asserts `list --json` always produces valid JSON.
// An empty base can't easily force mgr.List() to fail (NewManager creates the
// base dir), so this covers the success-shape side: count:0 with a parseable
// envelope. The error path now routes through the same shallowListOutput struct
// (Error field) so a forced failure would emit {"...","error":...}, not a bare
// string — see runShallowProfileList's emitErr.
func TestShallowListJSONError(t *testing.T) {
	_, _ = shallowEnv(t)
	stdout, _, err := runCmdCaptured(t, "shallow-profile", "list", "--json")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	var resp struct {
		BaseDir  string `json:"base_dir"`
		Count    int    `json:"count"`
		Error    string `json:"error"`
		Profiles []struct {
			Name string `json:"name"`
		} `json:"profiles"`
	}
	if err := json.Unmarshal([]byte(stdout), &resp); err != nil {
		t.Fatalf("list --json must emit valid JSON, got %q: %v", stdout, err)
	}
	if resp.Count != 0 || len(resp.Profiles) != 0 {
		t.Fatalf("expected empty list, got %+v", resp)
	}
	if resp.Error != "" {
		t.Fatalf("unexpected error on clean list: %q", resp.Error)
	}
}

// TestShallowSpawnJSONError verifies that a spawn error under --json is emitted
// as {"success":false,"error":...} on stdout rather than a bare returned error.
func TestShallowSpawnJSONError(t *testing.T) {
	_, _ = shallowEnv(t)
	stdout, _, err := runCmdCaptured(t, "shallow-spawn", "ghost", "--json", "--", "sh")
	if err == nil {
		t.Fatalf("expected non-zero exit on error, got nil; stdout=%q", stdout)
	}
	var resp struct {
		Success bool   `json:"success"`
		Error   string `json:"error"`
	}
	if err := json.Unmarshal([]byte(stdout), &resp); err != nil {
		t.Fatalf("unmarshal %q: %v", stdout, err)
	}
	if resp.Success {
		t.Fatalf("expected success=false, got %q", stdout)
	}
	if !strings.Contains(resp.Error, "does not exist") {
		t.Fatalf("expected 'does not exist' in error, got %q", resp.Error)
	}
}

// TestShallowSpawnPrintEnvJSON verifies the --print-env --json shape for a codex
// profile: HOME/SHALLOW_PROFILE + the codex provider sets land in `set`, while
// the cleared (but not re-set) vars land in `unset`.
func TestShallowSpawnPrintEnvJSON(t *testing.T) {
	base, _ := shallowEnv(t)
	if _, _, err := runCmdCaptured(t, "shallow-profile", "create", "codex-alice",
		"--tool", "codex", "--json"); err != nil {
		t.Fatal(err)
	}
	stdout, _, err := runCmdCaptured(t, "shallow-spawn", "codex-alice", "--print-env", "--json")
	if err != nil {
		t.Fatalf("spawn print-env --json: %v", err)
	}
	var resp struct {
		Success        bool              `json:"success"`
		Home           string            `json:"home"`
		ShallowProfile string            `json:"shallow_profile"`
		Set            map[string]string `json:"set"`
		Unset          []string          `json:"unset"`
	}
	if err := json.Unmarshal([]byte(stdout), &resp); err != nil {
		t.Fatalf("unmarshal %q: %v", stdout, err)
	}
	if !resp.Success {
		t.Fatalf("expected success, got %q", stdout)
	}
	homePath := filepath.Join(base, "codex-alice")
	if resp.Home != homePath {
		t.Fatalf("home %q != %q", resp.Home, homePath)
	}
	if resp.ShallowProfile != "codex-alice" {
		t.Fatalf("shallow_profile %q", resp.ShallowProfile)
	}
	if !strings.HasSuffix(resp.Set["CODEX_HOME"], "/.codex") {
		t.Fatalf("set CODEX_HOME %q does not end in /.codex", resp.Set["CODEX_HOME"])
	}
	if resp.Set["HOME"] != homePath {
		t.Fatalf("set HOME %q != %q", resp.Set["HOME"], homePath)
	}
	if resp.Set["SHALLOW_PROFILE"] != "codex-alice" {
		t.Fatalf("set SHALLOW_PROFILE %q", resp.Set["SHALLOW_PROFILE"])
	}
	unsetHas := func(k string) bool {
		for _, u := range resp.Unset {
			if u == k {
				return true
			}
		}
		return false
	}
	if !unsetHas("CLAUDE_CONFIG_DIR") {
		t.Fatalf("unset should contain CLAUDE_CONFIG_DIR, got %v", resp.Unset)
	}
	if !unsetHas("OPENAI_API_KEY") {
		t.Fatalf("unset should contain OPENAI_API_KEY, got %v", resp.Unset)
	}
	if unsetHas("CODEX_HOME") {
		t.Fatalf("CODEX_HOME is set, must not also be in unset: %v", resp.Unset)
	}
}

// TestShallowDeletePromptOnStderr verifies the non-force, non-json delete prompt
// is written to stderr (not stdout), so a caller capturing stdout sees only the
// command's real output.
func TestShallowDeletePromptOnStderr(t *testing.T) {
	_, _ = shallowEnv(t)
	if _, _, err := runCmdCaptured(t, "shallow-profile", "create", "alice", "--json"); err != nil {
		t.Fatal(err)
	}
	// Build a root we can drive stdin on (decline the prompt with "n").
	root := newShallowTestRoot()
	var stdout, stderr bytes.Buffer
	root.SetOut(&stdout)
	root.SetErr(&stderr)
	root.SetIn(strings.NewReader("n\n"))
	root.SetArgs([]string{"shallow-profile", "delete", "alice"})
	if err := root.Execute(); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if !strings.Contains(stderr.String(), "Delete shallow profile") {
		t.Fatalf("prompt should be on stderr, got stderr=%q", stderr.String())
	}
	if strings.Contains(stdout.String(), "Delete shallow profile") {
		t.Fatalf("prompt should NOT be on stdout, got stdout=%q", stdout.String())
	}
}

// TestShallowPrintEnvGolden locks the exact eval-able --print-env contract for
// both a claude and a codex profile. The non-deterministic base path is
// normalized to the literal <BASE> before comparing against an inline golden.
func TestShallowPrintEnvGolden(t *testing.T) {
	base, _ := shallowEnv(t)

	for _, name := range []string{"cgold", "claude-gold"} {
		args := []string{"shallow-profile", "create", name, "--json"}
		if name == "cgold" {
			args = []string{"shallow-profile", "create", name, "--tool", "codex", "--json"}
		}
		if _, _, err := runCmdCaptured(t, args...); err != nil {
			t.Fatalf("create %s: %v", name, err)
		}
	}

	// normalize replaces the test's per-run base dir prefix with <BASE> so the
	// golden is stable across runs, and splits into a sorted line set.
	normalize := func(out string) []string {
		out = strings.ReplaceAll(out, base, "<BASE>")
		var lines []string
		for _, l := range strings.Split(strings.TrimRight(out, "\n"), "\n") {
			if l != "" {
				lines = append(lines, l)
			}
		}
		sort.Strings(lines)
		return lines
	}

	// --- codex golden -------------------------------------------------------
	codexOut, _, err := runCmdCaptured(t, "shallow-spawn", "cgold", "--print-env")
	if err != nil {
		t.Fatalf("codex print-env: %v", err)
	}
	wantCodex := []string{
		"export CODEX_HOME='<BASE>/cgold/.codex'",
		"export CODEX_SQLITE_HOME='<BASE>/cgold/.codex'",
		"export HOME='<BASE>/cgold'",
		"export SHALLOW_PROFILE='cgold'",
		"unset ANTHROPIC_API_KEY",
		"unset ANTHROPIC_AUTH_TOKEN",
		"unset CAAM_HOME",
		"unset CAAM_SHALLOW_HOMES_DIR",
		"unset CLAUDE_CODE_OAUTH_TOKEN",
		"unset CLAUDE_CODE_USE_BEDROCK",
		"unset CLAUDE_CODE_USE_FOUNDRY",
		"unset CLAUDE_CODE_USE_VERTEX",
		"unset CLAUDE_CONFIG_DIR",
		"unset CODEX_ACCESS_TOKEN",
		"unset CODEX_API_KEY",
		"unset GEMINI_HOME",
		"unset OPENAI_API_KEY",
	}
	sort.Strings(wantCodex)
	if got := normalize(codexOut); !equalStringSlices(got, wantCodex) {
		t.Fatalf("codex --print-env golden mismatch:\n got: %#v\nwant: %#v", got, wantCodex)
	}
	// Belt-and-suspenders on the load-bearing properties.
	if strings.Contains(codexOut, "unset CODEX_HOME") || strings.Contains(codexOut, "unset CODEX_SQLITE_HOME") {
		t.Fatalf("codex print-env must NOT unset the SET codex vars:\n%s", codexOut)
	}

	// --- claude golden ------------------------------------------------------
	claudeOut, _, err := runCmdCaptured(t, "shallow-spawn", "claude-gold", "--print-env")
	if err != nil {
		t.Fatalf("claude print-env: %v", err)
	}
	wantClaude := []string{
		"export HOME='<BASE>/claude-gold'",
		"export SHALLOW_PROFILE='claude-gold'",
		"unset ANTHROPIC_API_KEY",
		"unset ANTHROPIC_AUTH_TOKEN",
		"unset CAAM_HOME",
		"unset CAAM_SHALLOW_HOMES_DIR",
		"unset CLAUDE_CODE_OAUTH_TOKEN",
		"unset CLAUDE_CODE_USE_BEDROCK",
		"unset CLAUDE_CODE_USE_FOUNDRY",
		"unset CLAUDE_CODE_USE_VERTEX",
		"unset CLAUDE_CONFIG_DIR",
		"unset CODEX_ACCESS_TOKEN",
		"unset CODEX_API_KEY",
		"unset CODEX_HOME",
		"unset CODEX_SQLITE_HOME",
		"unset GEMINI_HOME",
		"unset OPENAI_API_KEY",
	}
	sort.Strings(wantClaude)
	if got := normalize(claudeOut); !equalStringSlices(got, wantClaude) {
		t.Fatalf("claude --print-env golden mismatch:\n got: %#v\nwant: %#v", got, wantClaude)
	}
	// Claude sets no CODEX_HOME and MUST unset it.
	if strings.Contains(claudeOut, "export CODEX_HOME") {
		t.Fatalf("claude print-env must NOT export CODEX_HOME:\n%s", claudeOut)
	}
	if !strings.Contains(claudeOut, "unset CODEX_HOME") {
		t.Fatalf("claude print-env must unset CODEX_HOME:\n%s", claudeOut)
	}
}

func equalStringSlices(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// TestShallowDoctorHealthy creates a codex profile and asserts doctor reports it
// healthy and exits zero.
func TestShallowDoctorHealthy(t *testing.T) {
	_, _ = shallowEnv(t)
	if _, _, err := runCmdCaptured(t, "shallow-profile", "create", "codex-alice",
		"--tool", "codex", "--json"); err != nil {
		t.Fatal(err)
	}
	stdout, _, err := runCmdCaptured(t, "shallow-profile", "doctor", "codex-alice")
	if err != nil {
		t.Fatalf("doctor: expected exit 0, got %v (stdout=%q)", err, stdout)
	}
	if !strings.Contains(stdout, "healthy") {
		t.Fatalf("expected 'healthy' in output, got %q", stdout)
	}
	if !strings.Contains(stdout, "codex-alice") {
		t.Fatalf("expected profile name in output, got %q", stdout)
	}
}

// TestShallowDoctorUnhealthy corrupts a codex profile by deleting its required
// credential and asserts doctor exits non-zero and names the problem.
func TestShallowDoctorUnhealthy(t *testing.T) {
	base, _ := shallowEnv(t)
	if _, _, err := runCmdCaptured(t, "shallow-profile", "create", "codex-alice",
		"--tool", "codex", "--json"); err != nil {
		t.Fatal(err)
	}
	// Remove the required credential to make the shape invalid.
	if err := os.Remove(filepath.Join(base, "codex-alice", ".codex", "auth.json")); err != nil {
		t.Fatal(err)
	}
	stdout, _, err := runCmdCaptured(t, "shallow-profile", "doctor", "codex-alice")
	if err == nil {
		t.Fatalf("expected non-zero exit for unhealthy profile; stdout=%q", stdout)
	}
	if !strings.Contains(stdout, "✗") || !strings.Contains(stdout, "codex-alice") {
		t.Fatalf("expected a failing line naming the profile, got %q", stdout)
	}
}

// TestShallowDoctorMalformedMeta deletes a profile's metadata sidecar and
// asserts doctor flags it as malformed / no recorded provider.
func TestShallowDoctorMalformedMeta(t *testing.T) {
	base, _ := shallowEnv(t)
	if _, _, err := runCmdCaptured(t, "shallow-profile", "create", "nometa", "--json"); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(base, "nometa", ".caam-shallow.json")); err != nil {
		t.Fatal(err)
	}
	stdout, _, err := runCmdCaptured(t, "shallow-profile", "doctor", "nometa")
	if err == nil {
		t.Fatalf("expected non-zero exit for malformed metadata; stdout=%q", stdout)
	}
	if !strings.Contains(stdout, "malformed") || !strings.Contains(stdout, "no recorded provider") {
		t.Fatalf("expected malformed/no-recorded-provider message, got %q", stdout)
	}
}

// TestShallowDoctorJSON diagnoses all profiles (one healthy, one unhealthy) in
// --json mode and asserts the per-profile flags, the overall healthy=false, and
// the non-zero exit.
func TestShallowDoctorJSON(t *testing.T) {
	base, _ := shallowEnv(t)
	if _, _, err := runCmdCaptured(t, "shallow-profile", "create", "good", "--json"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := runCmdCaptured(t, "shallow-profile", "create", "bad",
		"--tool", "codex", "--json"); err != nil {
		t.Fatal(err)
	}
	// Corrupt "bad" by removing its required credential.
	if err := os.Remove(filepath.Join(base, "bad", ".codex", "auth.json")); err != nil {
		t.Fatal(err)
	}

	stdout, _, err := runCmdCaptured(t, "shallow-profile", "doctor", "--json")
	if err == nil {
		t.Fatalf("expected non-zero exit when a profile is unhealthy; stdout=%q", stdout)
	}
	var resp struct {
		Healthy  bool `json:"healthy"`
		Profiles []struct {
			Name    string `json:"name"`
			Healthy bool   `json:"healthy"`
			Error   string `json:"error"`
		} `json:"profiles"`
	}
	if err := json.Unmarshal([]byte(stdout), &resp); err != nil {
		t.Fatalf("unmarshal doctor JSON %q: %v", stdout, err)
	}
	if resp.Healthy {
		t.Fatalf("expected overall healthy=false, got %q", stdout)
	}
	byName := map[string]bool{}
	for _, p := range resp.Profiles {
		byName[p.Name] = p.Healthy
	}
	if !byName["good"] {
		t.Fatalf("expected 'good' healthy=true, got %q", stdout)
	}
	if byName["bad"] {
		t.Fatalf("expected 'bad' healthy=false, got %q", stdout)
	}
}

// TestShallowDoctorAllEmpty runs doctor with no profiles and asserts a friendly
// message and a zero exit (nothing is unhealthy).
func TestShallowDoctorAllEmpty(t *testing.T) {
	_, _ = shallowEnv(t)
	stdout, _, err := runCmdCaptured(t, "shallow-profile", "doctor")
	if err != nil {
		t.Fatalf("expected exit 0 with no profiles, got %v (stdout=%q)", err, stdout)
	}
	if !strings.Contains(stdout, "No shallow profiles found") {
		t.Fatalf("expected friendly empty message, got %q", stdout)
	}
}

// FuzzParseShallowVaultRef proves parseShallowVaultRef never accepts a
// reference whose Profile could escape a vault path: any accepted input yields
// a Profile with no path separator, not `.`/`..`, and a non-empty Tool.
func FuzzParseShallowVaultRef(f *testing.F) {
	for _, seed := range []string{
		"claude/alice", "codex/bob", "x", "a/b/c", "claude/../etc",
		"claude/a b", "", "claude/", "/alice", "claude/.", "claude/..",
		"CLAUDE/Alice", "claude/a\\b", "claude/a/b", " claude / bob ",
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, spec string) {
		ref, err := parseShallowVaultRef(spec)
		if err != nil {
			return // rejected — nothing to prove
		}
		if ref.Tool == "" {
			t.Fatalf("parseShallowVaultRef(%q) accepted with empty Tool", spec)
		}
		if ref.Profile == "" {
			t.Fatalf("parseShallowVaultRef(%q) accepted with empty Profile", spec)
		}
		if ref.Profile == "." || ref.Profile == ".." {
			t.Fatalf("parseShallowVaultRef(%q) accepted dot Profile %q", spec, ref.Profile)
		}
		if strings.ContainsAny(ref.Profile, `/\`) {
			t.Fatalf("parseShallowVaultRef(%q) accepted Profile %q containing a separator", spec, ref.Profile)
		}
		// Mirror the real downstream use: Profile is joined into a vault path; the
		// joined leaf must remain a single component under the tool dir (no escape).
		joined := filepath.Join("vault", ref.Tool, ref.Profile)
		if filepath.Base(joined) != ref.Profile {
			t.Fatalf("parseShallowVaultRef(%q): Profile %q does not survive as a single path component (joined=%q)", spec, ref.Profile, joined)
		}
		if filepath.Dir(joined) != filepath.Join("vault", ref.Tool) {
			t.Fatalf("parseShallowVaultRef(%q): Profile %q escapes its tool dir (joined=%q)", spec, ref.Profile, joined)
		}
	})
}
