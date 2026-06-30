package shallow

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"

	authfile "github.com/Dicklesworthstone/coding_agent_account_manager/internal/authfile"
)

// fakeHome lays down a realistic-looking real HOME inside t.TempDir() and
// returns its path. It includes a representative slice of dotfiles (real
// files), dot-directories, and the .claude/ structure (with conversation
// history that the shallow profile must continue to share).
func fakeHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()

	// Top-level real files (will become symlinks in shallow profiles).
	for _, f := range []string{".bashrc", ".zshrc", ".gitconfig"} {
		if err := os.WriteFile(filepath.Join(home, f), []byte("# "+f+"\n"), 0o644); err != nil {
			t.Fatalf("seed %s: %v", f, err)
		}
	}
	// Top-level real dirs (will become symlinks in shallow profiles).
	for _, d := range []string{".ssh", ".config", ".cargo", ".bun"} {
		full := filepath.Join(home, d)
		if err := os.MkdirAll(full, 0o700); err != nil {
			t.Fatalf("mkdir %s: %v", d, err)
		}
		if err := os.WriteFile(filepath.Join(full, "marker"), []byte(d), 0o600); err != nil {
			t.Fatalf("seed marker in %s: %v", d, err)
		}
	}
	// .claude/ structure with the auth files plus conversation history.
	claudeDir := filepath.Join(home, ".claude")
	if err := os.MkdirAll(claudeDir, 0o700); err != nil {
		t.Fatalf("mkdir .claude: %v", err)
	}
	for _, sub := range []string{"projects", "todos", "shell-snapshots"} {
		full := filepath.Join(claudeDir, sub)
		if err := os.MkdirAll(full, 0o700); err != nil {
			t.Fatalf("mkdir .claude/%s: %v", sub, err)
		}
		if err := os.WriteFile(filepath.Join(full, "marker"), []byte(sub), 0o600); err != nil {
			t.Fatalf("seed marker in .claude/%s: %v", sub, err)
		}
	}
	if err := os.WriteFile(filepath.Join(home, ".claude.json"), []byte(`{"hello":"world"}`), 0o600); err != nil {
		t.Fatalf("seed .claude.json: %v", err)
	}
	if err := os.WriteFile(filepath.Join(claudeDir, ".credentials.json"), []byte(`{"placeholder":true}`), 0o600); err != nil {
		t.Fatalf("seed .credentials.json: %v", err)
	}

	// .codex/ structure: the allow-listed shared state (sessions, history.jsonl)
	// PLUS the runtime/log/state artifacts the allow-list must keep OUT.
	codexDir := filepath.Join(home, ".codex")
	if err := os.MkdirAll(codexDir, 0o700); err != nil {
		t.Fatalf("mkdir .codex: %v", err)
	}
	if err := os.WriteFile(filepath.Join(codexDir, "auth.json"), []byte(`{"placeholder":"real-codex"}`), 0o600); err != nil {
		t.Fatalf("seed .codex/auth.json: %v", err)
	}
	if err := os.WriteFile(filepath.Join(codexDir, "config.toml"), []byte("model = \"gpt-5\"\n"), 0o600); err != nil {
		t.Fatalf("seed .codex/config.toml: %v", err)
	}
	// Allow-listed shared state: a sessions/ dir and a history.jsonl file.
	if err := os.MkdirAll(filepath.Join(codexDir, "sessions"), 0o700); err != nil {
		t.Fatalf("mkdir .codex/sessions: %v", err)
	}
	if err := os.WriteFile(filepath.Join(codexDir, "sessions", "marker"), []byte("sessions"), 0o600); err != nil {
		t.Fatalf("seed .codex/sessions/marker: %v", err)
	}
	if err := os.WriteFile(filepath.Join(codexDir, "history.jsonl"), []byte("{}\n"), 0o600); err != nil {
		t.Fatalf("seed .codex/history.jsonl: %v", err)
	}
	// Runtime/log/state artifacts that must NEVER be symlinked (not in the allow-list).
	for _, d := range []string{"log", "logs", "app-server-daemon", "app-server-control", "run-1234"} {
		full := filepath.Join(codexDir, d)
		if err := os.MkdirAll(full, 0o700); err != nil {
			t.Fatalf("mkdir .codex/%s: %v", d, err)
		}
		if err := os.WriteFile(filepath.Join(full, "marker"), []byte(d), 0o600); err != nil {
			t.Fatalf("seed .codex/%s/marker: %v", d, err)
		}
	}
	if err := os.WriteFile(filepath.Join(codexDir, "state.sqlite"), []byte("SQLITE"), 0o600); err != nil {
		t.Fatalf("seed .codex/state.sqlite: %v", err)
	}
	return home
}

// credSource creates a temp file containing valid-looking credential JSON
// and returns its path.
func credSource(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "creds.json")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write cred source: %v", err)
	}
	return path
}

func TestDefaultBaseDirPrecedence(t *testing.T) {
	t.Setenv("CAAM_SHALLOW_HOMES_DIR", "/explicit/override")
	t.Setenv("CAAM_HOME", "/some/caam")
	if got := DefaultBaseDir("/home/u"); got != "/explicit/override" {
		t.Fatalf("override should win, got %q", got)
	}

	t.Setenv("CAAM_SHALLOW_HOMES_DIR", "")
	if got := DefaultBaseDir("/home/u"); got != "/some/caam/shallow-homes" {
		t.Fatalf("CAAM_HOME path expected, got %q", got)
	}

	t.Setenv("CAAM_HOME", "")
	if got := DefaultBaseDir("/home/u"); got != "/home/u/orch-homes" {
		t.Fatalf("default expected, got %q", got)
	}
}

func TestNewManagerRejectsBaseDirEqualToHome(t *testing.T) {
	home := t.TempDir()
	_, err := NewManager(home, home)
	if err == nil {
		t.Fatalf("expected error when baseDir == realHome")
	}
}

func TestCreateBuildsSymlinkFarmAndRealAuthFiles(t *testing.T) {
	home := fakeHome(t)
	base := filepath.Join(t.TempDir(), "orch-homes")
	mgr, err := NewManager(base, home)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}

	credPath := credSource(t, `{"alice":true}`)
	got, err := mgr.Create("alice", CreateOptions{
		CredentialSource:    credPath,
		CredentialFromLabel: "vault:claude/alice",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if got != filepath.Join(base, "alice") {
		t.Fatalf("unexpected home path %q", got)
	}

	// .claude is a real directory.
	st, err := os.Lstat(filepath.Join(got, ".claude"))
	if err != nil {
		t.Fatalf("stat .claude: %v", err)
	}
	if !st.IsDir() || (st.Mode()&os.ModeSymlink) != 0 {
		t.Fatalf(".claude must be a real directory, got mode %v", st.Mode())
	}

	// .credentials.json is a real file with our copied contents.
	credDst := filepath.Join(got, ".claude", ".credentials.json")
	credSt, err := os.Lstat(credDst)
	if err != nil {
		t.Fatalf("stat creds: %v", err)
	}
	if (credSt.Mode() & os.ModeSymlink) != 0 {
		t.Fatalf(".credentials.json must be a real file, not a symlink")
	}
	body, err := os.ReadFile(credDst)
	if err != nil {
		t.Fatalf("read creds: %v", err)
	}
	if string(body) != `{"alice":true}` {
		t.Fatalf("creds contents wrong: %q", body)
	}
	// Permissions tight (0600).
	if credSt.Mode().Perm() != 0o600 {
		t.Fatalf("creds perm not 0600: %v", credSt.Mode().Perm())
	}

	// .credentials.lock is a real (empty) file.
	lockSt, err := os.Lstat(filepath.Join(got, ".claude", ".credentials.lock"))
	if err != nil {
		t.Fatalf("stat lock: %v", err)
	}
	if (lockSt.Mode() & os.ModeSymlink) != 0 {
		t.Fatalf(".credentials.lock must be a real file")
	}

	// .claude.json is a real file (seeded from real HOME).
	claudeJSONSt, err := os.Lstat(filepath.Join(got, ".claude.json"))
	if err != nil {
		t.Fatalf("stat .claude.json: %v", err)
	}
	if (claudeJSONSt.Mode() & os.ModeSymlink) != 0 {
		t.Fatalf(".claude.json must be a real file")
	}

	// Top-level dotfiles must be symlinks pointing back to real HOME.
	for _, name := range []string{".bashrc", ".zshrc", ".gitconfig", ".ssh", ".config", ".cargo", ".bun"} {
		dst := filepath.Join(got, name)
		linkInfo, err := os.Lstat(dst)
		if err != nil {
			t.Fatalf("stat %s: %v", name, err)
		}
		if (linkInfo.Mode() & os.ModeSymlink) == 0 {
			t.Fatalf("%s should be a symlink, mode=%v", name, linkInfo.Mode())
		}
		target, err := os.Readlink(dst)
		if err != nil {
			t.Fatalf("readlink %s: %v", name, err)
		}
		want := filepath.Join(home, name)
		if target != want {
			t.Fatalf("%s symlink target %q != %q", name, target, want)
		}
	}

	// Inner .claude entries (other than .credentials*) must be symlinks
	// pointing at the corresponding entries under real ~/.claude.
	for _, name := range []string{"projects", "todos", "shell-snapshots"} {
		dst := filepath.Join(got, ".claude", name)
		st, err := os.Lstat(dst)
		if err != nil {
			t.Fatalf("stat .claude/%s: %v", name, err)
		}
		if (st.Mode() & os.ModeSymlink) == 0 {
			t.Fatalf(".claude/%s should be a symlink", name)
		}
		target, err := os.Readlink(dst)
		if err != nil {
			t.Fatalf("readlink .claude/%s: %v", name, err)
		}
		if target != filepath.Join(home, ".claude", name) {
			t.Fatalf(".claude/%s target wrong: %q", name, target)
		}
	}

	// Conversation-history passthrough: reading via shallow HOME yields the
	// same contents as reading via real HOME.
	for _, sub := range []string{"projects", "todos", "shell-snapshots"} {
		body, err := os.ReadFile(filepath.Join(got, ".claude", sub, "marker"))
		if err != nil {
			t.Fatalf("read passthrough .claude/%s/marker: %v", sub, err)
		}
		if string(body) != sub {
			t.Fatalf("passthrough mismatch for .claude/%s/marker: %q", sub, body)
		}
	}

	// Metadata sidecar exists and round-trips.
	metaPath := filepath.Join(got, ProfileMetaFilename)
	metaBytes, err := os.ReadFile(metaPath)
	if err != nil {
		t.Fatalf("read meta: %v", err)
	}
	var meta Meta
	if err := json.Unmarshal(metaBytes, &meta); err != nil {
		t.Fatalf("unmarshal meta: %v", err)
	}
	if meta.Name != "alice" {
		t.Fatalf("meta name %q", meta.Name)
	}
	if meta.CredentialFrom != "vault:claude/alice" {
		t.Fatalf("meta credential_from %q", meta.CredentialFrom)
	}
	if meta.RealHome != home {
		t.Fatalf("meta real home %q != %q", meta.RealHome, home)
	}
	if meta.CreatedAt.IsZero() {
		t.Fatalf("meta created_at is zero")
	}
}

func TestCreateSmartFallbackForMissingSources(t *testing.T) {
	// Real HOME has only .bashrc; everything else is missing.
	home := t.TempDir()
	if err := os.WriteFile(filepath.Join(home, ".bashrc"), []byte("# shell\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	mgr, err := NewManager(filepath.Join(t.TempDir(), "homes"), home)
	if err != nil {
		t.Fatal(err)
	}
	got, err := mgr.Create("bob", CreateOptions{})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	// .bashrc is a symlink.
	if st, err := os.Lstat(filepath.Join(got, ".bashrc")); err != nil || (st.Mode()&os.ModeSymlink) == 0 {
		t.Fatalf(".bashrc should be a symlink (err=%v, mode=%v)", err, st.Mode())
	}
	// No symlink for .ssh because it doesn't exist in real HOME.
	if _, err := os.Lstat(filepath.Join(got, ".ssh")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf(".ssh should NOT exist (smart fallback), err=%v", err)
	}
	// .credentials.json was created empty (no credential source given).
	if body, err := os.ReadFile(filepath.Join(got, ".claude", ".credentials.json")); err != nil {
		t.Fatalf("read creds: %v", err)
	} else if len(body) != 0 {
		t.Fatalf("expected empty creds, got %q", body)
	}
	// .claude.json got the skeleton because real HOME has no .claude.json.
	if body, err := os.ReadFile(filepath.Join(got, ".claude.json")); err != nil {
		t.Fatalf("read claude.json: %v", err)
	} else if !strings.HasPrefix(string(body), "{") {
		t.Fatalf("expected JSON skeleton, got %q", body)
	}
}

func TestCreateRefusesDuplicateWithoutForce(t *testing.T) {
	home := fakeHome(t)
	mgr, err := NewManager(filepath.Join(t.TempDir(), "homes"), home)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := mgr.Create("alice", CreateOptions{}); err != nil {
		t.Fatal(err)
	}
	if _, err := mgr.Create("alice", CreateOptions{}); err == nil {
		t.Fatalf("expected error on duplicate create without --force")
	}
	if _, err := mgr.Create("alice", CreateOptions{Force: true}); err != nil {
		t.Fatalf("force overwrite: %v", err)
	}
}

func TestCreateRejectsBadName(t *testing.T) {
	home := fakeHome(t)
	mgr, err := NewManager(filepath.Join(t.TempDir(), "homes"), home)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"", "..", ".", "../oops", "with/slash", "white space", "weird$char"} {
		if _, err := mgr.Create(name, CreateOptions{}); err == nil {
			t.Errorf("expected error for invalid name %q", name)
		}
	}
}

func TestListReturnsSortedProfiles(t *testing.T) {
	home := fakeHome(t)
	mgr, err := NewManager(filepath.Join(t.TempDir(), "homes"), home)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"charlie", "alice", "bob"} {
		if _, err := mgr.Create(name, CreateOptions{}); err != nil {
			t.Fatal(err)
		}
	}
	got, err := mgr.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("expected 3 profiles, got %d", len(got))
	}
	names := make([]string, len(got))
	for i, p := range got {
		names[i] = p.Name
	}
	if !sort.StringsAreSorted(names) {
		t.Fatalf("profiles not sorted: %v", names)
	}
	if !sort.SliceIsSorted(got, func(i, j int) bool { return got[i].Name < got[j].Name }) {
		t.Fatalf("profile slice not sorted")
	}
	for _, p := range got {
		if p.Meta == nil {
			t.Errorf("profile %q missing meta", p.Name)
		}
	}
}

func TestListEmptyAndMissingBaseDir(t *testing.T) {
	mgr, err := NewManager(filepath.Join(t.TempDir(), "doesnotexist"), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	got, err := mgr.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("expected 0 profiles, got %d", len(got))
	}
}

func TestDeleteRemovesProfile(t *testing.T) {
	home := fakeHome(t)
	mgr, err := NewManager(filepath.Join(t.TempDir(), "homes"), home)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := mgr.Create("alice", CreateOptions{}); err != nil {
		t.Fatal(err)
	}
	if err := mgr.Delete("alice"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := mgr.Get("alice"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected ErrNotExist, got %v", err)
	}
	// Real HOME is intact (delete must never traverse symlinks).
	for _, f := range []string{".bashrc", ".gitconfig", ".claude/projects/marker"} {
		if _, err := os.Stat(filepath.Join(home, f)); err != nil {
			t.Fatalf("real HOME entry %q vanished: %v", f, err)
		}
	}
}

func TestDeleteUnknownProfile(t *testing.T) {
	mgr, err := NewManager(filepath.Join(t.TempDir(), "homes"), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := mgr.Delete("nope"); err == nil {
		t.Fatalf("expected error deleting unknown profile")
	}
}

// TestCredentialIsolation ensures that mutating one shallow profile's
// .credentials.json does NOT affect another profile or the real HOME.
func TestCredentialIsolation(t *testing.T) {
	home := fakeHome(t)
	mgr, err := NewManager(filepath.Join(t.TempDir(), "homes"), home)
	if err != nil {
		t.Fatal(err)
	}
	src := credSource(t, `{"identity":"original"}`)
	for _, name := range []string{"alice", "bob"} {
		if _, err := mgr.Create(name, CreateOptions{CredentialSource: src}); err != nil {
			t.Fatalf("Create %s: %v", name, err)
		}
	}

	aliceCred, _ := mgr.CredentialPath("alice")
	bobCred, _ := mgr.CredentialPath("bob")
	realCred := filepath.Join(home, ".claude", ".credentials.json")

	// Mutate alice's credentials only.
	if err := os.WriteFile(aliceCred, []byte(`{"identity":"alice-rotated"}`), 0o600); err != nil {
		t.Fatalf("write alice creds: %v", err)
	}
	// Bob and real HOME must be unchanged.
	if body, err := os.ReadFile(bobCred); err != nil || string(body) != `{"identity":"original"}` {
		t.Fatalf("bob creds drifted: body=%q err=%v", body, err)
	}
	if body, err := os.ReadFile(realCred); err != nil || string(body) != `{"placeholder":true}` {
		t.Fatalf("real HOME creds drifted: body=%q err=%v", body, err)
	}
}

// TestCredentialsLockIsIndependent verifies that two shallow profiles each
// get their OWN .credentials.lock (not a symlink to the user's real lock),
// so two concurrent Claude sessions can each flock their own lock without
// blocking each other.
func TestCredentialsLockIsIndependent(t *testing.T) {
	home := fakeHome(t)
	// Pre-create a real ~/.claude/.credentials.lock to verify we don't symlink to it.
	if err := os.WriteFile(filepath.Join(home, ".claude", ".credentials.lock"), []byte("real-lock"), 0o600); err != nil {
		t.Fatal(err)
	}

	mgr, err := NewManager(filepath.Join(t.TempDir(), "homes"), home)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"alice", "bob"} {
		if _, err := mgr.Create(name, CreateOptions{}); err != nil {
			t.Fatalf("Create %s: %v", name, err)
		}
	}

	aliceLock := filepath.Join(mgr.BaseDir(), "alice", ".claude", ".credentials.lock")
	bobLock := filepath.Join(mgr.BaseDir(), "bob", ".claude", ".credentials.lock")

	for _, p := range []string{aliceLock, bobLock} {
		st, err := os.Lstat(p)
		if err != nil {
			t.Fatalf("stat %s: %v", p, err)
		}
		if (st.Mode() & os.ModeSymlink) != 0 {
			t.Fatalf("%s must NOT be a symlink", p)
		}
	}

	// Writing to one lock must not affect the other.
	if err := os.WriteFile(aliceLock, []byte("alice"), 0o600); err != nil {
		t.Fatal(err)
	}
	if body, err := os.ReadFile(bobLock); err != nil {
		t.Fatal(err)
	} else if string(body) == "alice" {
		t.Fatalf("bob lock contains alice contents: locks not independent")
	}
}

// TestSkipShallowBaseDirNestedInRealHome covers the case where the shallow
// base dir is itself under realHome (e.g. ~/orch-homes when realHome is ~).
// Without this guard, populateSymlinks would symlink ~/orch-homes back into
// the shallow profile, creating a nasty recursion.
func TestSkipShallowBaseDirNestedInRealHome(t *testing.T) {
	home := fakeHome(t)
	base := filepath.Join(home, "orch-homes")
	if err := os.MkdirAll(base, 0o700); err != nil {
		t.Fatal(err)
	}
	mgr, err := NewManager(base, home)
	if err != nil {
		t.Fatal(err)
	}
	got, err := mgr.Create("alice", CreateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	// The shallow HOME must NOT contain a symlink named "orch-homes".
	if _, err := os.Lstat(filepath.Join(got, "orch-homes")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("shallow HOME should not contain orch-homes: err=%v", err)
	}
}

// TestPreservesExistingRealDir ensures that when a real directory already
// exists in the shallow HOME (e.g. .claude), populateSymlinks does NOT
// replace it with a symlink. This is what protects the per-profile
// credentials directory.
func TestPreservesExistingRealDir(t *testing.T) {
	home := fakeHome(t)
	mgr, err := NewManager(filepath.Join(t.TempDir(), "homes"), home)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := mgr.Create("alice", CreateOptions{}); err != nil {
		t.Fatal(err)
	}
	st, err := os.Lstat(filepath.Join(mgr.BaseDir(), "alice", ".claude"))
	if err != nil {
		t.Fatal(err)
	}
	if (st.Mode()&os.ModeSymlink) != 0 || !st.IsDir() {
		t.Fatalf(".claude must remain a real directory, got mode=%v", st.Mode())
	}
}

// TestCustomClaudeJSONSource verifies the SourceClaudeJSON option.
func TestCustomClaudeJSONSource(t *testing.T) {
	home := fakeHome(t)
	mgr, err := NewManager(filepath.Join(t.TempDir(), "homes"), home)
	if err != nil {
		t.Fatal(err)
	}
	src := credSource(t, `{"customSetting":42}`)
	if _, err := mgr.Create("alice", CreateOptions{SourceClaudeJSON: src}); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(mgr.BaseDir(), "alice", ".claude.json"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != `{"customSetting":42}` {
		t.Fatalf(".claude.json mismatch: %q", got)
	}
}

// TestGetReturnsMeta verifies Get loads metadata.
func TestGetReturnsMeta(t *testing.T) {
	home := fakeHome(t)
	mgr, err := NewManager(filepath.Join(t.TempDir(), "homes"), home)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := mgr.Create("alice", CreateOptions{CredentialFromLabel: "vault:claude/alice"}); err != nil {
		t.Fatal(err)
	}
	p, err := mgr.Get("alice")
	if err != nil {
		t.Fatal(err)
	}
	if p.Meta == nil {
		t.Fatalf("expected meta, got nil")
	}
	if p.Meta.CredentialFrom != "vault:claude/alice" {
		t.Fatalf("meta CredentialFrom %q", p.Meta.CredentialFrom)
	}
}

// TestCreateProducesNoBrokenSymlinks scans the shallow HOME and asserts
// every symlink resolves to an existing path.
func TestCreateProducesNoBrokenSymlinks(t *testing.T) {
	home := fakeHome(t)
	mgr, err := NewManager(filepath.Join(t.TempDir(), "homes"), home)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := mgr.Create("alice", CreateOptions{}); err != nil {
		t.Fatal(err)
	}

	root := filepath.Join(mgr.BaseDir(), "alice")
	err = filepath.WalkDir(root, func(path string, _ os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		info, err := os.Lstat(path)
		if err != nil {
			return err
		}
		if (info.Mode() & os.ModeSymlink) == 0 {
			return nil
		}
		target, err := os.Readlink(path)
		if err != nil {
			return err
		}
		// Resolve relative targets against the symlink's dir.
		if !filepath.IsAbs(target) {
			target = filepath.Join(filepath.Dir(path), target)
		}
		if _, err := os.Stat(target); err != nil {
			return fmt.Errorf("broken symlink %s -> %s: %w", path, target, err)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Phase 10 — engine tests
// ---------------------------------------------------------------------------

// newMgr builds a Manager rooted at a fresh base dir under realHome's sibling
// tmp, never under realHome itself.
func newMgr(t *testing.T, home string) *Manager {
	t.Helper()
	mgr, err := NewManager(filepath.Join(t.TempDir(), "homes"), home)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	return mgr
}

// assertReal fails unless path is a real (non-symlink) regular file with perm.
func assertRealFilePerm(t *testing.T, path string, perm os.FileMode) {
	t.Helper()
	st, err := os.Lstat(path)
	if err != nil {
		t.Fatalf("lstat %s: %v", path, err)
	}
	if st.Mode()&os.ModeSymlink != 0 {
		t.Fatalf("%s must be a real file, got symlink", path)
	}
	if !st.Mode().IsRegular() {
		t.Fatalf("%s must be a regular file, mode=%v", path, st.Mode())
	}
	if st.Mode().Perm() != perm {
		t.Fatalf("%s perm = %v, want %v", path, st.Mode().Perm(), perm)
	}
}

func assertIsSymlink(t *testing.T, path string) {
	t.Helper()
	st, err := os.Lstat(path)
	if err != nil {
		t.Fatalf("lstat %s: %v", path, err)
	}
	if st.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("%s must be a symlink, mode=%v", path, st.Mode())
	}
}

func assertAbsent(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("%s must be absent, err=%v", path, err)
	}
}

func assertRealDir(t *testing.T, path string) {
	t.Helper()
	st, err := os.Lstat(path)
	if err != nil {
		t.Fatalf("lstat %s: %v", path, err)
	}
	if st.Mode()&os.ModeSymlink != 0 || !st.IsDir() {
		t.Fatalf("%s must be a real directory, mode=%v", path, st.Mode())
	}
}

// 1. Claude layout is behavior-preserving under the refactor.
func TestCreateClaudeLayoutStillWorks(t *testing.T) {
	home := fakeHome(t)
	mgr := newMgr(t, home)
	src := credSource(t, `{"claude":"identity"}`)
	got, err := mgr.Create("alice", CreateOptions{Provider: "claude", CredentialSource: src})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	assertRealDir(t, filepath.Join(got, ".claude"))
	credDst := filepath.Join(got, ".claude", ".credentials.json")
	assertRealFilePerm(t, credDst, 0o600)
	if body, err := os.ReadFile(credDst); err != nil || string(body) != `{"claude":"identity"}` {
		t.Fatalf("creds = %q err=%v", body, err)
	}
	assertRealFilePerm(t, filepath.Join(got, ".claude", ".credentials.lock"), 0o600)
	assertRealFilePerm(t, filepath.Join(got, ".claude.json"), 0o600)
	for _, name := range []string{".bashrc", ".ssh", ".config", ".cargo"} {
		assertIsSymlink(t, filepath.Join(got, name))
	}
	assertIsSymlink(t, filepath.Join(got, ".claude", "projects"))
}

// 2. Codex from a single credential source.
func TestCreateCodexLayoutFromCredentialSource(t *testing.T) {
	home := fakeHome(t)
	mgr := newMgr(t, home)
	src := credSource(t, `{"codex":"token"}`)
	got, err := mgr.Create("alice", CreateOptions{Provider: "codex", CredentialSource: src})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	assertRealDir(t, filepath.Join(got, ".codex"))
	authDst := filepath.Join(got, ".codex", "auth.json")
	assertRealFilePerm(t, authDst, 0o600)
	if body, err := os.ReadFile(authDst); err != nil || string(body) != `{"codex":"token"}` {
		t.Fatalf("auth.json = %q err=%v", body, err)
	}
	cfg := filepath.Join(got, ".codex", "config.toml")
	assertRealFilePerm(t, cfg, 0o600)
	cfgBody, err := os.ReadFile(cfg)
	if err != nil {
		t.Fatalf("read config.toml: %v", err)
	}
	if !strings.Contains(string(cfgBody), `cli_auth_credentials_store = "file"`) {
		t.Fatalf("config.toml missing file credential store: %q", cfgBody)
	}
	// Allow-listed shared state is symlinked.
	assertIsSymlink(t, filepath.Join(got, ".codex", "sessions"))
	assertIsSymlink(t, filepath.Join(got, ".codex", "history.jsonl"))
	// Runtime/log/state artifacts must be absent (never symlinked).
	for _, n := range []string{"log", "logs", "app-server-daemon", "app-server-control", "run-1234", "state.sqlite"} {
		assertAbsent(t, filepath.Join(got, ".codex", n))
	}
}

// 3. Codex writes a FRESH minimal config — not a copy of the real one.
func TestCreateCodexLayoutWritesFreshMinimalConfig(t *testing.T) {
	home := fakeHome(t)
	// Real config carries keys that must NOT leak.
	realCfg := filepath.Join(home, ".codex", "config.toml")
	if err := os.WriteFile(realCfg, []byte("model = \"gpt-5\"\nlog_dir = \"/abs/real/log\"\nsqlite_home = \"/abs/real/sqlite\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	mgr := newMgr(t, home)
	src := credSource(t, `{"codex":"token"}`)
	got, err := mgr.Create("alice", CreateOptions{Provider: "codex", CredentialSource: src})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	cfg := filepath.Join(got, ".codex", "config.toml")
	assertRealFilePerm(t, cfg, 0o600)
	body, err := os.ReadFile(cfg)
	if err != nil {
		t.Fatalf("read config.toml: %v", err)
	}
	s := string(body)
	if strings.Count(s, "cli_auth_credentials_store") != 1 {
		t.Fatalf("expected exactly one cli_auth_credentials_store, got %q", s)
	}
	if !strings.Contains(s, `cli_auth_credentials_store = "file"`) {
		t.Fatalf("missing file store directive: %q", s)
	}
	for _, leaked := range []string{"model", "log_dir", "sqlite_home"} {
		if strings.Contains(s, leaked) {
			t.Fatalf("config.toml leaked copied key %q: %q", leaked, s)
		}
	}
}

// 5. Codex with no credential source → empty real 0600 auth.json.
func TestCreateCodexLayoutEmptyAuth(t *testing.T) {
	home := fakeHome(t)
	mgr := newMgr(t, home)
	got, err := mgr.Create("alice", CreateOptions{Provider: "codex"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	authDst := filepath.Join(got, ".codex", "auth.json")
	assertRealFilePerm(t, authDst, 0o600)
	if body, err := os.ReadFile(authDst); err != nil || len(body) != 0 {
		t.Fatalf("expected empty auth.json, got %q err=%v", body, err)
	}
}

// 6. Metadata records provider + Version 2 + descriptive fields.
func TestMetaRecordsProvider(t *testing.T) {
	home := fakeHome(t)
	mgr := newMgr(t, home)
	if _, err := mgr.Create("alice", CreateOptions{Provider: "codex", CredentialFromLabel: "vault:codex/alice"}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	meta, err := readMeta(filepath.Join(mgr.BaseDir(), "alice"))
	if err != nil {
		t.Fatalf("readMeta: %v", err)
	}
	if meta.Provider != "codex" {
		t.Fatalf("provider = %q, want codex", meta.Provider)
	}
	if meta.Version != 2 {
		t.Fatalf("version = %d, want 2", meta.Version)
	}
	if meta.CredentialFrom != "vault:codex/alice" {
		t.Fatalf("credential_from = %q", meta.CredentialFrom)
	}
	if meta.RealHome != home {
		t.Fatalf("real_home = %q, want %q", meta.RealHome, home)
	}
}

// 7. CredentialPath/LayoutForProvider are strict on malformed metadata; List
// still returns the profile (listable/deletable), with Provider verbatim.
func TestCredentialPathStrictOnMalformedMeta(t *testing.T) {
	home := fakeHome(t)
	mgr := newMgr(t, home)

	// Profile with NO provider in meta.
	noProv, err := mgr.Create("noprov", CreateOptions{Provider: "codex"})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(noProv, ProfileMetaFilename),
		[]byte(`{"name":"noprov","real_home":"`+home+`","version":2}`), 0o600); err != nil {
		t.Fatal(err)
	}

	// Profile with an UNKNOWN provider ("codx").
	badProv, err := mgr.Create("badprov", CreateOptions{Provider: "codex"})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(badProv, ProfileMetaFilename),
		[]byte(`{"name":"badprov","provider":"codx","real_home":"`+home+`","version":2}`), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := mgr.CredentialPath("noprov"); err == nil {
		t.Fatalf("CredentialPath should error on missing provider")
	}
	if _, err := mgr.CredentialPath("badprov"); err == nil {
		t.Fatalf("CredentialPath should error on unknown provider")
	}
	if _, err := LayoutForProvider(""); err == nil {
		t.Fatalf("LayoutForProvider(\"\") should error")
	}
	if _, err := LayoutForProvider("codx"); err == nil {
		t.Fatalf("LayoutForProvider(\"codx\") should error")
	}

	// Both profiles still listable with verbatim provider.
	profs, err := mgr.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	got := map[string]string{}
	for _, p := range profs {
		if p.Meta != nil {
			got[p.Name] = p.Meta.Provider
		} else {
			got[p.Name] = "<nil-meta>"
		}
	}
	if got["noprov"] != "" {
		t.Fatalf("noprov provider = %q, want empty verbatim", got["noprov"])
	}
	if got["badprov"] != "codx" {
		t.Fatalf("badprov provider = %q, want codx verbatim", got["badprov"])
	}
}

// 8. Unsupported provider is rejected with the supported-set message.
func TestCreateRejectsUnsupportedProvider(t *testing.T) {
	home := fakeHome(t)
	mgr := newMgr(t, home)
	_, err := mgr.Create("alice", CreateOptions{Provider: "gemini"})
	if err == nil {
		t.Fatalf("expected error for unsupported provider")
	}
	if !strings.Contains(err.Error(), `unsupported shallow provider "gemini"`) ||
		!strings.Contains(err.Error(), "claude, codex") {
		t.Fatalf("error = %v", err)
	}
}

// 9. SourceClaudeJSON for a non-claude provider is rejected.
func TestCreateRejectsClaudeJSONForNonClaude(t *testing.T) {
	home := fakeHome(t)
	mgr := newMgr(t, home)
	src := credSource(t, `{}`)
	_, err := mgr.Create("alice", CreateOptions{Provider: "codex", SourceClaudeJSON: src})
	if err == nil || !strings.Contains(err.Error(), "only valid for provider claude") {
		t.Fatalf("error = %v", err)
	}
}

// 10. CredentialSource and CredentialSourceDir together are rejected.
func TestCreateRejectsBothSources(t *testing.T) {
	home := fakeHome(t)
	mgr := newMgr(t, home)
	f := credSource(t, `{}`)
	d := t.TempDir()
	_, err := mgr.Create("alice", CreateOptions{Provider: "codex", CredentialSource: f, CredentialSourceDir: d})
	if err == nil || !strings.Contains(err.Error(), "mutually exclusive") {
		t.Fatalf("error = %v", err)
	}
}

// 11. No broken symlinks for a codex profile.
func TestCreateProducesNoBrokenSymlinksForCodex(t *testing.T) {
	home := fakeHome(t)
	mgr := newMgr(t, home)
	got, err := mgr.Create("alice", CreateOptions{Provider: "codex"})
	if err != nil {
		t.Fatal(err)
	}
	err = filepath.WalkDir(got, func(p string, _ os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		info, err := os.Lstat(p)
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink == 0 {
			return nil
		}
		target, err := os.Readlink(p)
		if err != nil {
			return err
		}
		if !filepath.IsAbs(target) {
			target = filepath.Join(filepath.Dir(p), target)
		}
		if _, err := os.Stat(target); err != nil {
			return fmt.Errorf("broken symlink %s -> %s: %w", p, target, err)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk: %v", err)
	}
}

// 12. Mid-create failure cleans up; a later create of the same name succeeds.
func TestCreateCleansUpOnError(t *testing.T) {
	home := fakeHome(t)
	mgr := newMgr(t, home)
	// Vault dir missing the required auth.json → provisionCredentials fails.
	emptyVault := t.TempDir()
	_, err := mgr.Create("alice", CreateOptions{
		Provider:            "codex",
		CredentialSourceDir: emptyVault,
		CredentialFromLabel: "vault:codex/alice",
	})
	if err == nil {
		t.Fatalf("expected create to fail with missing auth.json")
	}
	// No leftover profile dir.
	if _, err := os.Lstat(filepath.Join(mgr.BaseDir(), "alice")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("half-built profile dir was not removed, err=%v", err)
	}
	// A later non-force create succeeds.
	if _, err := mgr.Create("alice", CreateOptions{Provider: "codex"}); err != nil {
		t.Fatalf("subsequent create should succeed: %v", err)
	}
}

// 13a. Creating profiles never mutates the real ~/ credential files.
func TestRealHomeUntouched(t *testing.T) {
	home := fakeHome(t)
	mgr := newMgr(t, home)

	claudeReal := filepath.Join(home, ".claude", ".credentials.json")
	codexReal := filepath.Join(home, ".codex", "auth.json")
	claudeBefore, err := os.ReadFile(claudeReal)
	if err != nil {
		t.Fatal(err)
	}
	codexBefore, err := os.ReadFile(codexReal)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := mgr.Create("c", CreateOptions{Provider: "claude", CredentialSource: credSource(t, `{"x":1}`)}); err != nil {
		t.Fatal(err)
	}
	if _, err := mgr.Create("x", CreateOptions{Provider: "codex", CredentialSource: credSource(t, `{"y":1}`)}); err != nil {
		t.Fatal(err)
	}

	claudeAfter, err := os.ReadFile(claudeReal)
	if err != nil {
		t.Fatal(err)
	}
	codexAfter, err := os.ReadFile(codexReal)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(claudeBefore, claudeAfter) {
		t.Fatalf("real ~/.claude/.credentials.json mutated")
	}
	if !bytes.Equal(codexBefore, codexAfter) {
		t.Fatalf("real ~/.codex/auth.json mutated")
	}
}

// 13b. Two codex profiles get independent real auth files.
func TestCredentialsAreIndependentRealFiles(t *testing.T) {
	home := fakeHome(t)
	mgr := newMgr(t, home)
	src := credSource(t, `{"identity":"original"}`)
	for _, n := range []string{"alice", "bob"} {
		if _, err := mgr.Create(n, CreateOptions{Provider: "codex", CredentialSource: src}); err != nil {
			t.Fatalf("Create %s: %v", n, err)
		}
	}
	aliceAuth, _ := mgr.CredentialPath("alice")
	bobAuth, _ := mgr.CredentialPath("bob")
	assertRealFilePerm(t, aliceAuth, 0o600)
	assertRealFilePerm(t, bobAuth, 0o600)
	realAuth := filepath.Join(home, ".codex", "auth.json")

	if err := os.WriteFile(aliceAuth, []byte(`{"identity":"alice-rotated"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if body, err := os.ReadFile(bobAuth); err != nil || string(body) != `{"identity":"original"}` {
		t.Fatalf("bob auth drifted: %q err=%v", body, err)
	}
	if body, err := os.ReadFile(realAuth); err != nil || string(body) != `{"placeholder":"real-codex"}` {
		t.Fatalf("real codex auth drifted: %q err=%v", body, err)
	}
}

// 13c. No credential dest has a symlinked ancestor inside the profile.
func TestNoRealFileEscapesViaSymlinkedParent(t *testing.T) {
	home := fakeHome(t)
	mgr := newMgr(t, home)
	for _, prov := range []string{"claude", "codex"} {
		got, err := mgr.Create(prov+"p", CreateOptions{Provider: prov})
		if err != nil {
			t.Fatalf("Create %s: %v", prov, err)
		}
		layout, err := LayoutForProvider(prov)
		if err != nil {
			t.Fatal(err)
		}
		for _, c := range layout.Credentials {
			dst := filepath.Join(got, filepath.FromSlash(c.DestRel))
			if err := assertNoSymlinkAncestor(got, dst); err != nil {
				t.Fatalf("%s cred %s has symlinked ancestor: %v", prov, c.DestRel, err)
			}
		}
	}
}

// 14. Concurrent create of N distinct profiles → each well-formed + distinct.
func TestConcurrentCreateDistinctProfiles(t *testing.T) {
	home := fakeHome(t)
	mgr := newMgr(t, home)

	const N = 8
	var wg sync.WaitGroup
	errs := make([]error, N)
	for i := 0; i < N; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			src := credSource(t, fmt.Sprintf(`{"id":%d}`, i))
			_, err := mgr.Create(fmt.Sprintf("p%d", i), CreateOptions{Provider: "codex", CredentialSource: src})
			errs[i] = err
		}(i)
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("concurrent create p%d: %v", i, err)
		}
	}
	seen := map[string]bool{}
	for i := 0; i < N; i++ {
		name := fmt.Sprintf("p%d", i)
		authDst := filepath.Join(mgr.BaseDir(), name, ".codex", "auth.json")
		assertRealFilePerm(t, authDst, 0o600)
		meta, err := readMeta(filepath.Join(mgr.BaseDir(), name))
		if err != nil || meta.Provider != "codex" || meta.Version != 2 {
			t.Fatalf("%s meta malformed: %+v err=%v", name, meta, err)
		}
		body, err := os.ReadFile(authDst)
		if err != nil {
			t.Fatal(err)
		}
		if seen[string(body)] {
			t.Fatalf("%s credential not distinct: %q", name, body)
		}
		seen[string(body)] = true
	}
}

// nestedTestLayout is a test-only layout shaped like Antigravity, used to drive
// the unexported helpers directly (Manager.Create only accepts registered ids).
func nestedTestLayout() Layout {
	return Layout{
		Provider:   "agytest",
		DefaultBin: "agytest",
		RealDirs:   []string{".gemini", ".gemini/antigravity-cli"},
		Credentials: []AuthFile{
			{VaultName: "token", DestRel: ".gemini/antigravity-cli/token", Primary: true, Required: true},
		},
		InnerSymlinkRoots: []string{".gemini", ".gemini/antigravity-cli"},
	}
}

// 15. Nested real-dir layout exercised via the unexported helpers.
func TestNestedRealDirLayoutWalk(t *testing.T) {
	home := t.TempDir()
	// A fake ~/.gemini with extra children + a nested antigravity-cli dir.
	gem := filepath.Join(home, ".gemini")
	if err := os.MkdirAll(filepath.Join(gem, "antigravity-cli"), 0o700); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"oauth_creds.json", "google_accounts.json", "settings-gemini.json"} {
		if err := os.WriteFile(filepath.Join(gem, f), []byte(f), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	// extra children under the nested dir
	if err := os.WriteFile(filepath.Join(gem, "antigravity-cli", "settings.json"), []byte("inner"), 0o600); err != nil {
		t.Fatal(err)
	}

	mgr := newMgr(t, home)
	layout := nestedTestLayout()
	profHome := filepath.Join(mgr.BaseDir(), "agy1")
	for _, d := range layout.RealDirs {
		if err := os.MkdirAll(filepath.Join(profHome, filepath.FromSlash(d)), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := mgr.populateSymlinks(profHome, layout); err != nil {
		t.Fatalf("populateSymlinks: %v", err)
	}
	for _, root := range layout.InnerSymlinkRoots {
		if err := mgr.populateInnerSymlinks(profHome, layout, root); err != nil {
			t.Fatalf("populateInnerSymlinks %s: %v", root, err)
		}
	}
	tok := credSource(t, `{"token":"abc"}`)
	if err := mgr.provisionCredentials(profHome, layout, CreateOptions{CredentialSource: tok}); err != nil {
		t.Fatalf("provisionCredentials: %v", err)
	}

	// Both real dirs stay real.
	assertRealDir(t, filepath.Join(profHome, ".gemini"))
	assertRealDir(t, filepath.Join(profHome, ".gemini", "antigravity-cli"))
	// Nested real dir is skipped (not symlinked) when inner-linking .gemini.
	antiSt, err := os.Lstat(filepath.Join(profHome, ".gemini", "antigravity-cli"))
	if err != nil {
		t.Fatal(err)
	}
	if antiSt.Mode()&os.ModeSymlink != 0 {
		t.Fatalf(".gemini/antigravity-cli must NOT be a symlink")
	}
	// Token is a real file.
	assertRealFilePerm(t, filepath.Join(profHome, ".gemini", "antigravity-cli", "token"), 0o600)
	// Unrelated .gemini/* children are symlinks.
	for _, f := range []string{"oauth_creds.json", "google_accounts.json", "settings-gemini.json"} {
		assertIsSymlink(t, filepath.Join(profHome, ".gemini", f))
	}
	// Unrelated nested child is symlinked too.
	assertIsSymlink(t, filepath.Join(profHome, ".gemini", "antigravity-cli", "settings.json"))
}

// 16. validateLayout rejects bad descriptors; mustBuildLayouts panics on bad.
func TestValidateLayoutRejectsBadDescriptors(t *testing.T) {
	good := func() Layout {
		return Layout{
			Provider:          "good",
			DefaultBin:        "good",
			RealDirs:          []string{".g", ".g/sub"},
			Credentials:       []AuthFile{{VaultName: "tok", DestRel: ".g/sub/tok", Primary: true, Required: true}},
			InnerSymlinkRoots: []string{".g"},
		}
	}
	if err := validateLayout(good()); err != nil {
		t.Fatalf("baseline good layout should validate: %v", err)
	}

	cases := map[string]func(l *Layout){
		"nested cred parent not RealDir": func(l *Layout) {
			l.RealDirs = []string{".g"} // drop .g/sub so cred parent isn't a RealDir
		},
		"two primaries": func(l *Layout) {
			l.Credentials = append(l.Credentials, AuthFile{VaultName: "t2", DestRel: ".g/sub/t2", Primary: true, Required: true})
		},
		"zero primaries": func(l *Layout) {
			l.Credentials = []AuthFile{{VaultName: "tok", DestRel: ".g/sub/tok", Required: true}}
		},
		"root not RealDir": func(l *Layout) {
			l.InnerSymlinkRoots = []string{".notreal"}
		},
		"both skip and allow": func(l *Layout) {
			l.InnerSkip = map[string][]string{".g": {"a"}}
			l.InnerSymlinkAllow = map[string][]string{".g": {"b"}}
		},
		"absolute path real dir": func(l *Layout) {
			l.RealDirs = append(l.RealDirs, "/abs")
		},
		"dotdot path real dir": func(l *Layout) {
			l.RealDirs = append(l.RealDirs, "../escape")
		},
		"inner root with no real dirs": func(l *Layout) {
			// .g is an InnerSymlinkRoot but no longer a RealDir.
			l.RealDirs = []string{".g/sub"}
		},
		"bad vault name": func(l *Layout) {
			l.Credentials = []AuthFile{{VaultName: "../evil", DestRel: ".g/sub/tok", Primary: true, Required: true}}
		},
		"root in both skip and allow (FIX 6)": func(l *Layout) {
			l.InnerSkip = map[string][]string{".g": {"a"}}
			l.InnerSymlinkAllow = map[string][]string{".g": {"b"}}
		},
		"bad InnerSkip key abs (FIX 6)": func(l *Layout) {
			l.InnerSkip = map[string][]string{"/abs": {"a"}}
		},
		"bad InnerSymlinkAllow key backslash (FIX 6)": func(l *Layout) {
			l.InnerSymlinkAllow = map[string][]string{`.g\sub`: {"a"}}
		},
		"bad InnerSkip key dotdot (FIX 6)": func(l *Layout) {
			l.InnerSkip = map[string][]string{"..": {"a"}}
		},
	}
	for name, mut := range cases {
		l := good()
		mut(&l)
		if err := validateLayout(l); err == nil {
			t.Errorf("expected validateLayout error for %q", name)
		}
	}

	// mustBuildLayouts panics on a bad descriptor.
	func() {
		defer func() {
			if r := recover(); r == nil {
				t.Fatalf("mustBuildLayouts should panic on a bad layout")
			}
		}()
		bad := good()
		bad.Credentials = nil // zero primaries
		_ = mustBuildLayouts(bad)
	}()
}

// 17. base-dir with a `..`-prefix child name is not symlinked into the profile.
func TestBaseDirNestingGuardDotDotPrefix(t *testing.T) {
	home := fakeHome(t)
	base := filepath.Join(home, "..orch-homes")
	if err := os.MkdirAll(base, 0o700); err != nil {
		t.Fatal(err)
	}
	mgr, err := NewManager(base, home)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	got, err := mgr.Create("alice", CreateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	assertAbsent(t, filepath.Join(got, "..orch-homes"))
}

// 18. Symlinked-base aliasing: BOTH a base symlink → realHome AND a base symlink
// → a second real dir are now rejected (FIX 3): a symlinked base LEAF lets
// destructive ops operate under the alias target, so NewManager refuses it
// regardless of where the alias points.
func TestRejectsSymlinkedBaseAliasingRealHome(t *testing.T) {
	// (a) base symlink whose target IS realHome.
	realHome := fakeHome(t)
	aliasDir := t.TempDir()
	aliasA := filepath.Join(aliasDir, "alias-a")
	if err := os.Symlink(realHome, aliasA); err != nil {
		t.Fatal(err)
	}
	if _, err := NewManager(aliasA, realHome); err == nil {
		t.Fatalf("NewManager should reject a base symlink targeting realHome")
	}

	// (b) base symlink to a SECOND real dir (not realHome) is ALSO rejected now —
	// a symlinked base leaf is refused outright (FIX 3).
	secondReal := filepath.Join(t.TempDir(), "second")
	if err := os.MkdirAll(secondReal, 0o700); err != nil {
		t.Fatal(err)
	}
	aliasB := filepath.Join(aliasDir, "alias-b")
	if err := os.Symlink(secondReal, aliasB); err != nil {
		t.Fatal(err)
	}
	if _, err := NewManager(aliasB, realHome); err == nil {
		t.Fatalf("NewManager should reject a symlinked base leaf even when it targets a second real dir")
	}
	// realHome's data untouched.
	if _, err := os.Stat(filepath.Join(realHome, ".bashrc")); err != nil {
		t.Fatalf("realHome .bashrc vanished: %v", err)
	}
}

// Phase 3.5: vault is never exposed via a symlink inside the shallow HOME.
func TestVaultNotExposedInShallowHome(t *testing.T) {
	home := fakeHome(t)
	// Place a vault UNDER realHome and seed a "secret" credential in it.
	vaultRoot := filepath.Join(home, ".caam-vault")
	if err := os.MkdirAll(filepath.Join(vaultRoot, "claude", "secret"), 0o700); err != nil {
		t.Fatal(err)
	}
	mgr := newMgr(t, home)
	mgr.SetVaultRoot(vaultRoot)
	got, err := mgr.Create("alice", CreateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	// The vault's top-level component (.caam-vault) must not appear in the profile.
	assertAbsent(t, filepath.Join(got, ".caam-vault"))
	// Belt-and-suspenders: no symlink in the profile resolves to the vault top.
	vaultResolved, _ := filepath.EvalSymlinks(vaultRoot)
	err = filepath.WalkDir(got, func(p string, _ os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		st, err := os.Lstat(p)
		if err != nil {
			return err
		}
		if st.Mode()&os.ModeSymlink == 0 {
			return nil
		}
		resolved, err := filepath.EvalSymlinks(p)
		if err != nil {
			return nil // dangling resolves elsewhere; ignore
		}
		if resolved == vaultResolved || strings.HasPrefix(resolved, vaultResolved+string(os.PathSeparator)) {
			return fmt.Errorf("symlink %s resolves into the vault: %s", p, resolved)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("vault exposure: %v", err)
	}
}

// Phase 3.5: a symlinked profile path (alice -> bob) is rejected by Get/Delete.
func TestSymlinkedProfilePathRejected(t *testing.T) {
	home := fakeHome(t)
	mgr := newMgr(t, home)
	if _, err := mgr.Create("bob", CreateOptions{}); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(mgr.BaseDir(), "alice")
	if err := os.Symlink(filepath.Join(mgr.BaseDir(), "bob"), alias); err != nil {
		t.Fatal(err)
	}
	if _, err := mgr.Get("alice"); err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("Get should reject symlinked profile path, err=%v", err)
	}
	if err := mgr.Delete("alice"); err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("Delete should reject symlinked profile path, err=%v", err)
	}
	// bob remains intact.
	if _, err := mgr.Get("bob"); err != nil {
		t.Fatalf("bob should still resolve: %v", err)
	}
}

// Phase 3.5: destructive ops refuse a non-profile dir (real dir w/o sidecar).
func TestDestructiveOpsRefuseNonProfileDir(t *testing.T) {
	home := fakeHome(t)
	mgr := newMgr(t, home)
	if err := os.MkdirAll(mgr.BaseDir(), 0o700); err != nil {
		t.Fatal(err)
	}
	notProfile := filepath.Join(mgr.BaseDir(), "notaprofile")
	if err := os.MkdirAll(notProfile, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(notProfile, "important.txt"), []byte("keep me"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := mgr.Delete("notaprofile"); err == nil {
		t.Fatalf("Delete should refuse a dir without a real sidecar")
	}
	if _, err := mgr.Create("notaprofile", CreateOptions{Force: true}); err == nil {
		t.Fatalf("Create --force should refuse to clobber a non-profile dir")
	}
	// The dir and its data survive.
	if _, err := os.Stat(filepath.Join(notProfile, "important.txt")); err != nil {
		t.Fatalf("non-profile data was destroyed: %v", err)
	}
}

// Phase 3.5: a canonical-base whose ANCESTOR is a symlink into realHome does not
// leave the resolved top-level component as a symlink inside the shallow HOME
// (canonical-base skip). The base LEAF itself is a real dir (FIX 3 rejects only
// symlinked leaves), so the symlink lives one level up.
func TestCanonicalBaseSymlinkNotExposed(t *testing.T) {
	home := fakeHome(t)
	// realHome/realdir is the real target; the base is <alias>/profiles where the
	// alias is a symlink to realHome/realdir. The base leaf (profiles) is a real
	// dir, but its canonical form resolves under realHome/realdir.
	realDir := filepath.Join(home, "realdir")
	if err := os.MkdirAll(filepath.Join(realDir, "profiles"), 0o700); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(t.TempDir(), "b")
	if err := os.Symlink(realDir, alias); err != nil {
		t.Fatal(err)
	}
	aliasBase := filepath.Join(alias, "profiles")
	mgr, err := NewManager(aliasBase, home)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	got, err := mgr.Create("alice", CreateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	// No `realdir` symlink (the canonical base's top-level component under
	// realHome) inside the profile.
	assertAbsent(t, filepath.Join(got, "realdir"))
}

// Sanity: the shipped layouts' primary VaultName matches what `caam backup`
// writes (basename drift would silently break --from-vault).
func TestLayoutVaultNamesMatchAuthfile(t *testing.T) {
	claude, err := LayoutForProvider("claude")
	if err != nil {
		t.Fatal(err)
	}
	if got, want := claude.Primary().VaultName, filepath.Base(authfile.ClaudeAuthFiles().Files[0].Path); got != want {
		t.Fatalf("claude primary VaultName = %q, want %q", got, want)
	}
	codex, err := LayoutForProvider("codex")
	if err != nil {
		t.Fatal(err)
	}
	if got, want := codex.Primary().VaultName, filepath.Base(authfile.CodexAuthFiles().Files[0].Path); got != want {
		t.Fatalf("codex primary VaultName = %q, want %q", got, want)
	}
}

// FIX 1: a vault/CAAM_HOME nested UNDER a layout RealDir (e.g. ~/.claude/caam)
// must NOT be mirrored into the profile by populateInnerSymlinks. Without the
// per-candidate isProtectedSource check, <profile>/.claude/caam would become a
// symlink to ~/.claude/caam, exposing the vault/base/sibling profiles.
func TestVaultUnderRealDirNotExposed(t *testing.T) {
	home := fakeHome(t)
	// Seed CAAM_HOME (and the vault) UNDER the real ~/.claude dir.
	caamHome := filepath.Join(home, ".claude", "caam")
	vaultRoot := filepath.Join(caamHome, "vault")
	if err := os.MkdirAll(filepath.Join(vaultRoot, "claude", "secret"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CAAM_HOME", caamHome)

	mgr := newMgr(t, home)
	mgr.SetVaultRoot(vaultRoot)
	got, err := mgr.Create("alice", CreateOptions{Provider: "claude"})
	if err != nil {
		t.Fatal(err)
	}
	// The nested vault component must be ABSENT inside .claude (not a symlink).
	assertAbsent(t, filepath.Join(got, ".claude", "caam"))
	// Belt-and-suspenders: no symlink anywhere in the profile resolves into the vault.
	vaultResolved, _ := filepath.EvalSymlinks(vaultRoot)
	err = filepath.WalkDir(got, func(p string, _ os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		st, err := os.Lstat(p)
		if err != nil {
			return err
		}
		if st.Mode()&os.ModeSymlink == 0 {
			return nil
		}
		resolved, err := filepath.EvalSymlinks(p)
		if err != nil {
			return nil
		}
		if resolved == vaultResolved || strings.HasPrefix(resolved, vaultResolved+string(os.PathSeparator)) {
			return fmt.Errorf("symlink %s resolves into the vault: %s", p, resolved)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("vault exposure: %v", err)
	}
}

// FIX 2: ValidateProfileShape rejects a profile whose RealDir was swapped for a
// symlink to the real ~/.codex, and passes for a freshly-created profile. It
// also errors when a required credential or config.toml is deleted.
func TestValidateProfileShapeRejectsSymlinkedParent(t *testing.T) {
	home := fakeHome(t)
	mgr := newMgr(t, home)
	layout, err := LayoutForProvider("codex")
	if err != nil {
		t.Fatal(err)
	}
	got, err := mgr.Create("alice", CreateOptions{Provider: "codex"})
	if err != nil {
		t.Fatal(err)
	}
	// Positive: a fresh profile passes.
	if err := mgr.ValidateProfileShape("alice", layout); err != nil {
		t.Fatalf("fresh profile should pass ValidateProfileShape: %v", err)
	}

	// Replace <home>/.codex with a symlink to the real ~/.codex.
	codexDir := filepath.Join(got, ".codex")
	if err := os.RemoveAll(codexDir); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(home, ".codex"), codexDir); err != nil {
		t.Fatal(err)
	}
	if err := mgr.ValidateProfileShape("alice", layout); err == nil {
		t.Fatalf("ValidateProfileShape should reject a symlinked .codex dir")
	}

	// Fresh profile, then delete the required credential → error.
	got2, err := mgr.Create("bob", CreateOptions{Provider: "codex", Force: true})
	_ = got2
	if err != nil {
		t.Fatal(err)
	}
	bobHome := filepath.Join(mgr.BaseDir(), "bob")
	if err := os.Remove(filepath.Join(bobHome, ".codex", "auth.json")); err != nil {
		t.Fatal(err)
	}
	if err := mgr.ValidateProfileShape("bob", layout); err == nil {
		t.Fatalf("ValidateProfileShape should reject a missing required credential")
	}

	// Fresh profile, then delete config.toml (a RealFile) → error.
	if _, err := mgr.Create("carol", CreateOptions{Provider: "codex"}); err != nil {
		t.Fatal(err)
	}
	carolHome := filepath.Join(mgr.BaseDir(), "carol")
	if err := os.Remove(filepath.Join(carolHome, ".codex", "config.toml")); err != nil {
		t.Fatal(err)
	}
	if err := mgr.ValidateProfileShape("carol", layout); err == nil {
		t.Fatalf("ValidateProfileShape should reject a missing config.toml")
	}
}

// FIX 3: NewManager rejects a symlinked base LEAF (but not symlinked ancestors).
func TestRejectsSymlinkedBaseLeaf(t *testing.T) {
	home := fakeHome(t)
	realDir := filepath.Join(t.TempDir(), "real")
	if err := os.MkdirAll(realDir, 0o700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(realDir, link); err != nil {
		t.Fatal(err)
	}
	if _, err := NewManager(link, home); err == nil {
		t.Fatalf("NewManager should reject a symlinked base leaf")
	} else if !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("error = %v, want a symlink-refusal", err)
	}

	// A symlinked ANCESTOR with a real leaf is fine (e.g. symlinked tmpdir).
	ancestorLink := filepath.Join(t.TempDir(), "anc")
	if err := os.Symlink(realDir, ancestorLink); err != nil {
		t.Fatal(err)
	}
	leaf := filepath.Join(ancestorLink, "base") // base leaf is a real dir under the alias
	if err := os.MkdirAll(filepath.Join(realDir, "base"), 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := NewManager(leaf, home); err != nil {
		t.Fatalf("NewManager should accept a symlinked ANCESTOR with a real leaf: %v", err)
	}
}

// FIX 4: a --force create with a bad source must NOT destroy the existing
// profile — the source preflight fails before the RemoveAll.
func TestForcePreflightDoesNotDestroyOnBadSource(t *testing.T) {
	home := fakeHome(t)
	mgr := newMgr(t, home)
	// Create a real claude profile with a custom .claude.json so we can detect survival.
	src := credSource(t, `{"v":"original"}`)
	if _, err := mgr.Create("alice", CreateOptions{Provider: "claude", SourceClaudeJSON: src}); err != nil {
		t.Fatal(err)
	}
	aliceHome := filepath.Join(mgr.BaseDir(), "alice")
	before, err := os.ReadFile(filepath.Join(aliceHome, ".claude.json"))
	if err != nil {
		t.Fatal(err)
	}

	// Force-recreate with a nonexistent --from-claude-json source → must error.
	if _, err := mgr.Create("alice", CreateOptions{
		Provider:         "claude",
		Force:            true,
		SourceClaudeJSON: "/nonexistent/claude.json",
	}); err == nil {
		t.Fatalf("expected error for nonexistent --from-claude-json source")
	}
	// The original profile must still exist intact.
	if _, err := mgr.Get("alice"); err != nil {
		t.Fatalf("original profile should survive a bad-source --force: %v", err)
	}
	after, err := os.ReadFile(filepath.Join(aliceHome, ".claude.json"))
	if err != nil {
		t.Fatalf("original .claude.json destroyed: %v", err)
	}
	if !bytes.Equal(before, after) {
		t.Fatalf(".claude.json changed: before=%q after=%q", before, after)
	}

	// Also: a bad --from-file source.
	if _, err := mgr.Create("alice", CreateOptions{
		Provider:         "claude",
		Force:            true,
		CredentialSource: "/nonexistent/creds.json",
	}); err == nil {
		t.Fatalf("expected error for nonexistent --from-file source")
	}
	if _, err := mgr.Get("alice"); err != nil {
		t.Fatalf("original profile should survive a bad --from-file --force: %v", err)
	}

	// Also: a --from-vault dir missing the required credential.
	emptyVault := t.TempDir()
	if _, err := mgr.Create("alice", CreateOptions{
		Provider:            "codex",
		Force:               true,
		CredentialSourceDir: emptyVault,
		CredentialFromLabel: "vault:codex/alice",
	}); err == nil {
		t.Fatalf("expected error for vault dir missing required credential")
	}
	if _, err := mgr.Get("alice"); err != nil {
		t.Fatalf("original profile should survive a bad --from-vault --force: %v", err)
	}
}

// FIX 5: LayoutForProvider returns a deep copy — mutating it must not affect a
// later fetch of the same provider.
func TestLayoutForProviderReturnsIndependentCopy(t *testing.T) {
	first, err := LayoutForProvider("codex")
	if err != nil {
		t.Fatal(err)
	}
	// Mutate the returned layout's maps/slices.
	first.InnerSymlinkAllow[".codex"] = append(first.InnerSymlinkAllow[".codex"], "POISON")
	first.InnerSymlinkAllow["EVIL_ROOT"] = []string{"x"}
	first.RealDirs = append(first.RealDirs, "EVIL_DIR")
	first.RealDirs[0] = "MUTATED"
	first.Credentials[0].VaultName = "MUTATED"

	second, err := LayoutForProvider("codex")
	if err != nil {
		t.Fatal(err)
	}
	if got := second.InnerSymlinkAllow[".codex"]; len(got) != 2 || got[0] != "sessions" || got[1] != "history.jsonl" {
		t.Fatalf("InnerSymlinkAllow[.codex] leaked mutation: %v", got)
	}
	if _, bad := second.InnerSymlinkAllow["EVIL_ROOT"]; bad {
		t.Fatalf("InnerSymlinkAllow gained EVIL_ROOT from a prior mutation")
	}
	if len(second.RealDirs) != 1 || second.RealDirs[0] != ".codex" {
		t.Fatalf("RealDirs leaked mutation: %v", second.RealDirs)
	}
	if second.Credentials[0].VaultName != "auth.json" {
		t.Fatalf("Credentials leaked mutation: %v", second.Credentials[0])
	}
}

// ---------------------------------------------------------------------------
// Fuzz tests — prove the security-sensitive string/path validators never
// green-light an escaping input. These also run as normal corpus-seeded tests
// under `go test` (each seed becomes a t.Run subtest).
// ---------------------------------------------------------------------------

// posixSingleUnquote is a tiny POSIX single-quote *unquoter* used only by the
// fuzz test. It reverses exactly what shellQuote produces: a string wrapped in
// single quotes where every literal `'` was rendered as the 4-char sequence
//
//	'\''   (close-quote, backslash-escaped quote, re-open-quote)
//
// It returns the decoded literal and ok=false if s is not a well-formed
// single-quoted POSIX word (which shellQuote must never emit).
func posixSingleUnquote(s string) (string, bool) {
	// A single-quoted word always starts with ' and ends with ' (the empty
	// string '' decodes to "").
	if len(s) < 2 || s[0] != '\'' || s[len(s)-1] != '\'' {
		return "", false
	}
	var b strings.Builder
	i := 1 // skip the opening quote
	end := len(s) - 1
	for i < end {
		c := s[i]
		if c == '\'' {
			// Inside a single-quoted region a bare ' can only appear as the
			// start of the `'\''` escape: ' (we're here) \ ' '.
			if i+3 <= end && s[i+1] == '\\' && s[i+2] == '\'' && s[i+3] == '\'' {
				b.WriteByte('\'')
				i += 4
				continue
			}
			return "", false // a bare unescaped quote that isn't the closer
		}
		b.WriteByte(c)
		i++
	}
	return b.String(), true
}

// FuzzShellQuote asserts shellQuote round-trips for arbitrary input (the
// quoted form decodes back to the original) and that the quoted form has no
// quote that could break out of the single-quoted word.
func FuzzShellQuote(f *testing.F) {
	for _, seed := range []string{
		"''", "a", "a b", "it's", "$(x)", "`backtick`", "with`tick",
		"line\nbreak", `back\slash`, "ünïcode", "", "'", "''''", "a'b'c",
		"\t", "rm -rf /", "x'\\''y",
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, s string) {
		q := shellQuote(s)

		// 1. Round-trip: decoding the quoted form yields the original string.
		got, ok := posixSingleUnquote(q)
		if !ok {
			t.Fatalf("shellQuote(%q) = %q is not a well-formed single-quoted word", s, q)
		}
		if got != s {
			t.Fatalf("round-trip mismatch: shellQuote(%q)=%q decoded to %q", s, q, got)
		}

		// 2. No unescaped break-out quote. The ONLY quote literal shellQuote may
		// emit inside the word is the exact 4-char escape `'\''`. After removing
		// every such escape the remainder must be a single contiguous
		// single-quoted region — i.e. it starts and ends with the outer quotes and
		// contains NO other quote. If any bare ' survived removal, a value could
		// terminate the quoting early and inject shell.
		if len(q) < 2 || q[0] != '\'' || q[len(q)-1] != '\'' {
			t.Fatalf("shellQuote(%q)=%q not wrapped in single quotes", s, q)
		}
		stripped := strings.ReplaceAll(q, `'\''`, "")
		// What remains is the literal payload bracketed by the outer quotes; with
		// every escape removed, exactly the two outer quotes may stay. Any
		// additional quote is a break-out.
		if strings.Count(stripped, "'") > 2 {
			t.Fatalf("shellQuote(%q)=%q has an unescaped break-out quote (stripped=%q)", s, q, stripped)
		}
	})
}

// FuzzCheckSlashRel proves checkSlashRel never accepts an escaping path: any
// accepted input, when cleaned, stays relative, contains no `..` component, is
// not absolute, and is filepath.IsLocal-safe.
func FuzzCheckSlashRel(f *testing.F) {
	for _, seed := range []string{
		".claude", "a/b", "/abs", "../x", "a/../b", "", "C:\\x", "a//b",
		".", "..", "a/", "a/./b", "a/..", "foo/bar/baz", "\\\\unc\\share",
		"a\\b", "./a", "x/../../y",
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, s string) {
		if checkSlashRel(s) != nil {
			return // rejected — nothing to prove
		}
		// Accepted: the safety property must hold.
		if path.IsAbs(s) {
			t.Fatalf("checkSlashRel accepted slash-absolute path %q", s)
		}
		osp := filepath.FromSlash(s)
		if filepath.IsAbs(osp) {
			t.Fatalf("checkSlashRel accepted OS-absolute path %q", s)
		}
		if filepath.VolumeName(osp) != "" {
			t.Fatalf("checkSlashRel accepted volume-prefixed path %q", s)
		}
		// filepath.IsLocal is the canonical "stays within the current dir, no
		// escape, no absolute, no volume" predicate. An accepted path MUST be local.
		if !filepath.IsLocal(osp) {
			t.Fatalf("checkSlashRel accepted non-local path %q (FromSlash=%q)", s, osp)
		}
		// No `..` component survives, and Clean doesn't reveal an escape.
		for _, seg := range strings.Split(filepath.ToSlash(s), "/") {
			if seg == ".." {
				t.Fatalf("checkSlashRel accepted path %q containing a .. component", s)
			}
		}
		cleaned := path.Clean(filepath.ToSlash(s))
		if cleaned == ".." || strings.HasPrefix(cleaned, "../") {
			t.Fatalf("checkSlashRel accepted path %q that cleans to an escaping %q", s, cleaned)
		}
	})
}
