package authfile

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// writeJSON is a small helper for these tests.
func writeJSONT(t *testing.T, path string, v map[string]interface{}) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
}

func readJSONT(t *testing.T, path string) map[string]interface{} {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]interface{}
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	return m
}

func desktopPath(home string) string {
	return filepath.Join(home, "Library", "Application Support", "Claude", "config.json")
}

// TestClaudeDesktopTokenCacheDetection verifies token presence gating.
func TestClaudeDesktopTokenCacheDetection(t *testing.T) {
	home := t.TempDir()
	p := desktopPath(home)

	// Absent -> not ok, no error.
	if _, ok, err := claudeDesktopTokenCache(p); err != nil || ok {
		t.Fatalf("absent config: ok=%v err=%v", ok, err)
	}

	// Present but no token cache -> not ok.
	writeJSONT(t, p, map[string]interface{}{"theme": "dark", "windowBounds": "x"})
	if _, ok, err := claudeDesktopTokenCache(p); err != nil || ok {
		t.Fatalf("token-less config: ok=%v err=%v", ok, err)
	}

	// With a token cache -> ok, and only the token field is extracted.
	writeJSONT(t, p, map[string]interface{}{
		"theme":              "dark",
		"oauth:tokenCacheV2": "ENC-BLOB-A",
	})
	fields, ok, err := claudeDesktopTokenCache(p)
	if err != nil || !ok {
		t.Fatalf("token config: ok=%v err=%v", ok, err)
	}
	if len(fields) != 1 || fields["oauth:tokenCacheV2"] != "ENC-BLOB-A" {
		t.Fatalf("extracted fields wrong: %v", fields)
	}
}

// TestClaudeDesktopBackupRestoreRoundTrip proves the core PR #44 fix: swapping
// profiles updates the desktop token cache to the activated account while
// preserving unrelated desktop settings, and never persists those settings.
func TestClaudeDesktopBackupRestoreRoundTrip(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	// Neutralize CLAUDE_CONFIG_DIR/XDG so ClaudeAuthFiles is deterministic.
	t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(home, ".config", "claude-code"))

	vault := NewVault(filepath.Join(t.TempDir(), "vault"))
	fs := ClaudeAuthFiles()

	// Required primary credential must exist for backup to succeed.
	writeCreds := func(token string) {
		p := filepath.Join(home, ".claude", ".credentials.json")
		writeJSONT(t, p, map[string]interface{}{
			"claudeAiOauth": map[string]interface{}{"accessToken": token, "refreshToken": token},
		})
	}

	// --- Account A: desktop config holds A's cache + an unrelated setting.
	writeCreds("A-TOKEN")
	desktop := desktopPath(home)
	writeJSONT(t, desktop, map[string]interface{}{
		"userTheme":          "solarized", // unrelated desktop setting
		"oauth:tokenCacheV2": "ENC-A",
	})
	if err := vault.Backup(fs, "alice"); err != nil {
		t.Fatalf("backup alice: %v", err)
	}

	// The vault snapshot must contain ONLY the token field, not userTheme.
	snap := readJSONT(t, filepath.Join(vault.ProfilePath("claude", "alice"), "config.json"))
	if snap["oauth:tokenCacheV2"] != "ENC-A" {
		t.Fatalf("snapshot missing token: %v", snap)
	}
	if _, leaked := snap["userTheme"]; leaked {
		t.Fatalf("snapshot leaked unrelated desktop setting: %v", snap)
	}

	// --- Account B: sign in as B, desktop cache becomes B; back up B.
	writeCreds("B-TOKEN")
	writeJSONT(t, desktop, map[string]interface{}{
		"userTheme":          "solarized",
		"oauth:tokenCacheV2": "ENC-B",
	})
	if err := vault.Backup(fs, "bob"); err != nil {
		t.Fatalf("backup bob: %v", err)
	}

	// Now the live desktop cache is B. Simulate the reported bug surface: user
	// wants A back. Restore alice -> desktop cache must flip to A, userTheme kept.
	if err := vault.Restore(fs, "alice"); err != nil {
		t.Fatalf("restore alice: %v", err)
	}
	live := readJSONT(t, desktop)
	if live["oauth:tokenCacheV2"] != "ENC-A" {
		t.Fatalf("restore did not flip desktop cache to A: %v", live)
	}
	if live["userTheme"] != "solarized" {
		t.Fatalf("restore clobbered unrelated desktop setting: %v", live)
	}

	// --- Clear (logout): only the token cache is scrubbed, userTheme survives.
	if err := ClearAuthFiles(fs); err != nil {
		t.Fatalf("clear: %v", err)
	}
	live = readJSONT(t, desktop)
	if _, ok := live["oauth:tokenCacheV2"]; ok {
		t.Fatalf("clear did not scrub token cache: %v", live)
	}
	if live["userTheme"] != "solarized" {
		t.Fatalf("clear destroyed unrelated desktop setting: %v", live)
	}
}

func TestClaudeDesktopRestoreMissingSnapshotScrubsTokenCacheOnly(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(home, ".config", "claude-code"))

	vault := NewVault(filepath.Join(t.TempDir(), "vault"))
	fs := ClaudeAuthFiles()

	writeCreds := func(token string) {
		p := filepath.Join(home, ".claude", ".credentials.json")
		writeJSONT(t, p, map[string]interface{}{
			"claudeAiOauth": map[string]interface{}{"accessToken": token, "refreshToken": token},
		})
	}

	desktop := desktopPath(home)
	writeCreds("A-TOKEN")
	writeJSONT(t, desktop, map[string]interface{}{
		"userTheme":          "solarized",
		"oauth:tokenCacheV2": "ENC-A",
	})
	if err := vault.Backup(fs, "with-desktop"); err != nil {
		t.Fatalf("backup with desktop: %v", err)
	}

	writeCreds("B-TOKEN")
	writeJSONT(t, desktop, map[string]interface{}{
		"userTheme": "solarized",
	})
	if err := vault.Backup(fs, "without-desktop"); err != nil {
		t.Fatalf("backup without desktop: %v", err)
	}

	if err := vault.Restore(fs, "with-desktop"); err != nil {
		t.Fatalf("restore with desktop: %v", err)
	}
	writeJSONT(t, desktop, map[string]interface{}{
		"userTheme":          "custom-live",
		"windowBounds":       "100,100,800,600",
		"oauth:tokenCacheV2": "ENC-A",
	})
	if err := vault.Restore(fs, "without-desktop"); err != nil {
		t.Fatalf("restore without desktop: %v", err)
	}
	live := readJSONT(t, desktop)
	if _, ok := live["oauth:tokenCacheV2"]; ok {
		t.Fatalf("restore did not scrub stale token cache: %v", live)
	}
	if live["userTheme"] != "custom-live" || live["windowBounds"] != "100,100,800,600" {
		t.Fatalf("restore clobbered unrelated settings: %v", live)
	}
}

func TestClaudeDesktopRestoreMissingSnapshotPreservesForeignTokenCache(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(home, ".config", "claude-code"))

	vault := NewVault(filepath.Join(t.TempDir(), "vault"))
	fs := ClaudeAuthFiles()
	creds := filepath.Join(home, ".claude", ".credentials.json")
	writeJSONT(t, creds, map[string]interface{}{
		"claudeAiOauth": map[string]interface{}{"accessToken": "B-TOKEN", "refreshToken": "B-TOKEN"},
	})
	desktop := desktopPath(home)
	writeJSONT(t, desktop, map[string]interface{}{"userTheme": "solarized"})
	if err := vault.Backup(fs, "without-desktop"); err != nil {
		t.Fatalf("backup without desktop: %v", err)
	}

	writeJSONT(t, desktop, map[string]interface{}{
		"userTheme":          "custom-live",
		"oauth:tokenCacheV2": "ENC-FOREIGN",
	})
	if err := vault.Restore(fs, "without-desktop"); err != nil {
		t.Fatalf("restore without desktop: %v", err)
	}
	live := readJSONT(t, desktop)
	if live["oauth:tokenCacheV2"] != "ENC-FOREIGN" {
		t.Fatalf("restore scrubbed unproven desktop token cache: %v", live)
	}
	if live["userTheme"] != "custom-live" {
		t.Fatalf("restore clobbered unrelated settings: %v", live)
	}
}

func TestClaudeDesktopTokenlessSnapshotReportsNoRestore(t *testing.T) {
	home := t.TempDir()
	vaultPath := filepath.Join(t.TempDir(), "config.json")
	livePath := desktopPath(home)
	writeJSONT(t, vaultPath, map[string]interface{}{"userTheme": "snapshot"})
	writeJSONT(t, livePath, map[string]interface{}{
		"userTheme":          "live",
		"oauth:tokenCacheV2": "ENC-LIVE",
	})

	restored, err := restoreClaudeDesktopTokenCache(vaultPath, livePath)
	if err != nil {
		t.Fatal(err)
	}
	if restored {
		t.Fatal("tokenless snapshot reported a restored token cache")
	}
	live := readJSONT(t, livePath)
	if live["oauth:tokenCacheV2"] != "ENC-LIVE" || live["userTheme"] != "live" {
		t.Fatalf("tokenless snapshot changed live desktop config: %v", live)
	}
}

func TestClaudeDesktopTokenlessSnapshotDoesNotAuthorizeOptionalOnlyCleanup(t *testing.T) {
	home := t.TempDir()
	vault := NewVault(filepath.Join(t.TempDir(), "vault"))
	requiredPath := filepath.Join(home, ".claude", ".credentials.json")
	desktop := desktopPath(home)
	fileSet := AuthFileSet{
		Tool: "claude",
		Files: []AuthFileSpec{
			{Tool: "claude", Path: requiredPath, Required: true},
			{Tool: "claude", Path: desktop, Required: false},
		},
		AllowOptionalOnly: true,
	}

	writeJSONT(t, requiredPath, map[string]interface{}{
		"claudeAiOauth": map[string]interface{}{"accessToken": "A-TOKEN", "refreshToken": "A-TOKEN"},
	})
	if err := vault.Backup(fileSet, "with-required"); err != nil {
		t.Fatalf("backup with required: %v", err)
	}

	targetDir := vault.ProfilePath("claude", "tokenless-desktop")
	if err := os.MkdirAll(targetDir, 0700); err != nil {
		t.Fatal(err)
	}
	writeJSONT(t, filepath.Join(targetDir, "config.json"), map[string]interface{}{
		"userTheme": "tokenless-snapshot",
	})
	meta := vaultProfileMeta{
		Tool:            "claude",
		Profile:         "tokenless-desktop",
		BackedUpAt:      "2026-07-05T00:00:00Z",
		Files:           1,
		ManifestVersion: 1,
		ManagedFiles: []managedFileState{
			{VaultName: "required.json", OriginalPath: requiredPath, Required: true, Present: false},
			{VaultName: "config.json", OriginalPath: desktop, Required: false, Present: true, ClaudeDesktopTokenCache: true},
		},
	}
	raw, err := json.Marshal(meta)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(targetDir, "meta.json"), raw, 0600); err != nil {
		t.Fatal(err)
	}

	if err := vault.Restore(fileSet, "tokenless-desktop"); err == nil {
		t.Fatal("Restore() succeeded with tokenless Claude Desktop snapshot")
	}
	if _, err := os.Stat(requiredPath); err != nil {
		t.Fatalf("tokenless optional restore should leave required auth in place: %v", err)
	}
}

// TestClaudeDesktopActiveProfileHashIgnoresSettings proves the hash keys on the
// token cache only, so unrelated desktop settings don't perturb detection.
func TestClaudeDesktopActiveProfileHashIgnoresSettings(t *testing.T) {
	home := t.TempDir()
	p := desktopPath(home)

	writeJSONT(t, p, map[string]interface{}{"userTheme": "a", "oauth:tokenCache": "ENC"})
	h1, err := stableFileHash("claude", p)
	if err != nil {
		t.Fatal(err)
	}
	writeJSONT(t, p, map[string]interface{}{"userTheme": "b", "extra": 42, "oauth:tokenCache": "ENC"})
	h2, err := stableFileHash("claude", p)
	if err != nil {
		t.Fatal(err)
	}
	if h1 != h2 {
		t.Fatalf("hash changed with unrelated settings: %s != %s", h1, h2)
	}
	writeJSONT(t, p, map[string]interface{}{"userTheme": "b", "oauth:tokenCache": "DIFFERENT"})
	h3, err := stableFileHash("claude", p)
	if err != nil {
		t.Fatal(err)
	}
	if h3 == h1 {
		t.Fatalf("hash did not change when token cache changed")
	}
}
