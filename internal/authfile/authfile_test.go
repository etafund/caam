package authfile

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestNewVault(t *testing.T) {
	v := NewVault("/some/path")
	if v == nil {
		t.Fatal("NewVault returned nil")
	}
	if v.basePath != "/some/path" {
		t.Errorf("basePath = %q, want %q", v.basePath, "/some/path")
	}
}

func TestDefaultVaultPath(t *testing.T) {
	// Save and restore environment
	origCaamHome := os.Getenv("CAAM_HOME")
	origXDG := os.Getenv("XDG_DATA_HOME")
	defer os.Setenv("CAAM_HOME", origCaamHome)
	defer os.Setenv("XDG_DATA_HOME", origXDG)

	t.Run("with CAAM_HOME set", func(t *testing.T) {
		os.Setenv("CAAM_HOME", "/custom/caam")
		os.Setenv("XDG_DATA_HOME", "/custom/data")
		path := DefaultVaultPath()
		want := "/custom/caam/data/vault"
		if path != want {
			t.Errorf("DefaultVaultPath() = %q, want %q", path, want)
		}
	})

	t.Run("with XDG_DATA_HOME set", func(t *testing.T) {
		os.Unsetenv("CAAM_HOME")
		os.Setenv("XDG_DATA_HOME", "/custom/data")
		path := DefaultVaultPath()
		want := "/custom/data/caam/vault"
		if path != want {
			t.Errorf("DefaultVaultPath() = %q, want %q", path, want)
		}
	})

	t.Run("without XDG_DATA_HOME", func(t *testing.T) {
		os.Unsetenv("CAAM_HOME")
		os.Unsetenv("XDG_DATA_HOME")
		path := DefaultVaultPath()
		homeDir, _ := os.UserHomeDir()
		want := filepath.Join(homeDir, ".local", "share", "caam", "vault")
		if path != want {
			t.Errorf("DefaultVaultPath() = %q, want %q", path, want)
		}
	})
}

func TestVaultProfilePath(t *testing.T) {
	v := NewVault("/vault")
	path := v.ProfilePath("claude", "work-1")
	want := "/vault/claude/work-1"
	if path != want {
		t.Errorf("ProfilePath() = %q, want %q", path, want)
	}
}

func TestVaultBackupPath(t *testing.T) {
	v := NewVault("/vault")
	path := v.BackupPath("claude", "work-1", "auth.json")
	want := "/vault/claude/work-1/auth.json"
	if path != want {
		t.Errorf("BackupPath() = %q, want %q", path, want)
	}
}

func TestVaultBackup(t *testing.T) {
	t.Run("successful backup with required file", func(t *testing.T) {
		tmpDir := t.TempDir()
		vaultDir := filepath.Join(tmpDir, "vault")
		authDir := filepath.Join(tmpDir, "auth")

		// Create auth file
		if err := os.MkdirAll(authDir, 0700); err != nil {
			t.Fatal(err)
		}
		authFile := filepath.Join(authDir, "auth.json")
		content := []byte(`{"token": "secret123"}`)
		if err := os.WriteFile(authFile, content, 0600); err != nil {
			t.Fatal(err)
		}

		v := NewVault(vaultDir)
		fileSet := AuthFileSet{
			Tool: "testtool",
			Files: []AuthFileSpec{
				{Tool: "testtool", Path: authFile, Required: true},
			},
		}

		if err := v.Backup(fileSet, "profile1"); err != nil {
			t.Fatalf("Backup() error = %v", err)
		}

		// Verify backup was created
		backupPath := v.BackupPath("testtool", "profile1", "auth.json")
		backedUp, err := os.ReadFile(backupPath)
		if err != nil {
			t.Fatalf("reading backup: %v", err)
		}
		if string(backedUp) != string(content) {
			t.Errorf("backup content = %q, want %q", backedUp, content)
		}

		// Verify metadata was written
		metaPath := filepath.Join(vaultDir, "testtool", "profile1", "meta.json")
		if _, err := os.Stat(metaPath); err != nil {
			t.Errorf("metadata file not created: %v", err)
		}
	})

	t.Run("missing required file fails", func(t *testing.T) {
		tmpDir := t.TempDir()
		vaultDir := filepath.Join(tmpDir, "vault")

		v := NewVault(vaultDir)
		fileSet := AuthFileSet{
			Tool: "testtool",
			Files: []AuthFileSpec{
				{Tool: "testtool", Path: "/nonexistent/auth.json", Required: true},
			},
		}

		err := v.Backup(fileSet, "profile1")
		if err == nil {
			t.Fatal("Backup() should fail for missing required file")
		}
	})

	t.Run("invalid profile name fails", func(t *testing.T) {
		tmpDir := t.TempDir()
		vaultDir := filepath.Join(tmpDir, "vault")
		authDir := filepath.Join(tmpDir, "auth")

		if err := os.MkdirAll(authDir, 0700); err != nil {
			t.Fatal(err)
		}
		authFile := filepath.Join(authDir, "auth.json")
		if err := os.WriteFile(authFile, []byte(`{"token": "secret123"}`), 0600); err != nil {
			t.Fatal(err)
		}

		v := NewVault(vaultDir)
		fileSet := AuthFileSet{
			Tool: "testtool",
			Files: []AuthFileSpec{
				{Tool: "testtool", Path: authFile, Required: true},
			},
		}

		if err := v.Backup(fileSet, "/"); err == nil {
			t.Fatal("Backup() should fail for invalid profile name")
		}
	})

	t.Run("invalid characters in profile name fails", func(t *testing.T) {
		tmpDir := t.TempDir()
		vaultDir := filepath.Join(tmpDir, "vault")
		authDir := filepath.Join(tmpDir, "auth")

		if err := os.MkdirAll(authDir, 0700); err != nil {
			t.Fatal(err)
		}
		authFile := filepath.Join(authDir, "auth.json")
		if err := os.WriteFile(authFile, []byte(`{"token": "secret123"}`), 0600); err != nil {
			t.Fatal(err)
		}

		v := NewVault(vaultDir)
		fileSet := AuthFileSet{
			Tool: "testtool",
			Files: []AuthFileSpec{
				{Tool: "testtool", Path: authFile, Required: true},
			},
		}

		// Test various invalid characters that should be rejected:
		// - Control characters (filesystem issues)
		// - Shell metacharacters (command injection prevention)
		// - Spaces (shell word splitting issues)
		invalidNames := []string{
			// Control characters
			"profile\nwith\nnewlines",
			"profile\twith\ttabs",
			"profile\rwith\rcarriage",
			"profile\x00with\x00null",
			"profile\x1fwith\x1fcontrol",
			"profile\x7fwith\x7fdel",
			// Shell metacharacters (command injection vectors)
			"profile$(touch /tmp/pwned)",
			"profile`touch /tmp/pwned`",
			"profile;rm -rf /",
			"profile|cat /etc/passwd",
			"profile&background",
			"profile'quoted'",
			`profile"doublequoted"`,
			// Spaces (word splitting)
			"profile with spaces",
			// Other special characters (@ is allowed for email-based names)
			"profile#hashtag",
			"profile$var",
			"profile%mod",
			"profile^caret",
			"profile*glob",
			"profile?question",
			"profile[bracket]",
			"profile{brace}",
			"profile<redirect>",
			"profile!bang",
			"profile~tilde",
		}

		for _, name := range invalidNames {
			if err := v.Backup(fileSet, name); err == nil {
				t.Errorf("Backup() should fail for profile name with invalid chars: %q", name)
			}
		}

		// Test valid names that should pass
		validNames := []string{
			"simple",
			"with-hyphen",
			"with_underscore",
			"with.period",
			"MixedCase123",
			"profile-1.backup_v2",
			"alice@gmail.com",
			"work@company.com",
			"profile@email",
		}

		for _, name := range validNames {
			// Create a fresh vault for each valid test
			testVault := filepath.Join(tmpDir, "vault-valid-"+name)
			vt := NewVault(testVault)
			if err := vt.Backup(fileSet, name); err != nil {
				t.Errorf("Backup() should succeed for valid profile name %q: %v", name, err)
			}
		}
	})

	t.Run("optional file missing succeeds", func(t *testing.T) {
		tmpDir := t.TempDir()
		vaultDir := filepath.Join(tmpDir, "vault")
		authDir := filepath.Join(tmpDir, "auth")

		// Create required file only
		if err := os.MkdirAll(authDir, 0700); err != nil {
			t.Fatal(err)
		}
		requiredFile := filepath.Join(authDir, "required.json")
		if err := os.WriteFile(requiredFile, []byte(`{}`), 0600); err != nil {
			t.Fatal(err)
		}

		v := NewVault(vaultDir)
		fileSet := AuthFileSet{
			Tool: "testtool",
			Files: []AuthFileSpec{
				{Tool: "testtool", Path: requiredFile, Required: true},
				{Tool: "testtool", Path: filepath.Join(authDir, "optional.json"), Required: false},
			},
		}

		if err := v.Backup(fileSet, "profile1"); err != nil {
			t.Fatalf("Backup() error = %v", err)
		}
	})

	t.Run("no files to backup fails", func(t *testing.T) {
		tmpDir := t.TempDir()
		vaultDir := filepath.Join(tmpDir, "vault")

		v := NewVault(vaultDir)
		fileSet := AuthFileSet{
			Tool: "testtool",
			Files: []AuthFileSpec{
				{Tool: "testtool", Path: "/nonexistent/auth.json", Required: false},
			},
		}

		err := v.Backup(fileSet, "profile1")
		if err == nil {
			t.Fatal("Backup() should fail when no files to backup")
		}
	})
}

func TestVaultRestore(t *testing.T) {
	t.Run("successful restore", func(t *testing.T) {
		tmpDir := t.TempDir()
		vaultDir := filepath.Join(tmpDir, "vault")
		authDir := filepath.Join(tmpDir, "auth")

		// Create backup in vault
		profileDir := filepath.Join(vaultDir, "testtool", "profile1")
		if err := os.MkdirAll(profileDir, 0700); err != nil {
			t.Fatal(err)
		}
		backupContent := []byte(`{"token": "restored"}`)
		backupFile := filepath.Join(profileDir, "auth.json")
		if err := os.WriteFile(backupFile, backupContent, 0600); err != nil {
			t.Fatal(err)
		}

		v := NewVault(vaultDir)
		authFile := filepath.Join(authDir, "auth.json")
		fileSet := AuthFileSet{
			Tool: "testtool",
			Files: []AuthFileSpec{
				{Tool: "testtool", Path: authFile, Required: true},
			},
		}

		if err := v.Restore(fileSet, "profile1"); err != nil {
			t.Fatalf("Restore() error = %v", err)
		}

		// Verify restore
		restored, err := os.ReadFile(authFile)
		if err != nil {
			t.Fatalf("reading restored file: %v", err)
		}
		if string(restored) != string(backupContent) {
			t.Errorf("restored content = %q, want %q", restored, backupContent)
		}
	})

	t.Run("profile not found fails", func(t *testing.T) {
		tmpDir := t.TempDir()
		vaultDir := filepath.Join(tmpDir, "vault")

		v := NewVault(vaultDir)
		fileSet := AuthFileSet{
			Tool: "testtool",
			Files: []AuthFileSpec{
				{Tool: "testtool", Path: "/some/auth.json", Required: true},
			},
		}

		err := v.Restore(fileSet, "nonexistent")
		if err == nil {
			t.Fatal("Restore() should fail for nonexistent profile")
		}
	})

	t.Run("missing required backup fails", func(t *testing.T) {
		tmpDir := t.TempDir()
		vaultDir := filepath.Join(tmpDir, "vault")

		// Create profile dir but without the required file
		profileDir := filepath.Join(vaultDir, "testtool", "profile1")
		if err := os.MkdirAll(profileDir, 0700); err != nil {
			t.Fatal(err)
		}

		v := NewVault(vaultDir)
		fileSet := AuthFileSet{
			Tool: "testtool",
			Files: []AuthFileSpec{
				{Tool: "testtool", Path: "/some/auth.json", Required: true},
			},
		}

		err := v.Restore(fileSet, "profile1")
		if err == nil {
			t.Fatal("Restore() should fail for missing required backup")
		}
	})

	t.Run("optional backup missing succeeds", func(t *testing.T) {
		tmpDir := t.TempDir()
		vaultDir := filepath.Join(tmpDir, "vault")
		authDir := filepath.Join(tmpDir, "auth")

		// Create profile dir with required file only
		profileDir := filepath.Join(vaultDir, "testtool", "profile1")
		if err := os.MkdirAll(profileDir, 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(profileDir, "required.json"), []byte(`{}`), 0600); err != nil {
			t.Fatal(err)
		}

		v := NewVault(vaultDir)
		fileSet := AuthFileSet{
			Tool: "testtool",
			Files: []AuthFileSpec{
				{Tool: "testtool", Path: filepath.Join(authDir, "required.json"), Required: true},
				{Tool: "testtool", Path: filepath.Join(authDir, "optional.json"), Required: false},
			},
		}

		if err := v.Restore(fileSet, "profile1"); err != nil {
			t.Fatalf("Restore() error = %v", err)
		}
	})
}

func TestVaultRestoreQuarantinesStaleOptionalLiveFile(t *testing.T) {
	tmpDir := t.TempDir()
	vaultDir := filepath.Join(tmpDir, "vault")
	authDir := filepath.Join(tmpDir, "auth")
	if err := os.MkdirAll(authDir, 0700); err != nil {
		t.Fatal(err)
	}

	requiredPath := filepath.Join(authDir, "required.json")
	optionalPath := filepath.Join(authDir, "optional.json")
	fileSet := AuthFileSet{
		Tool: "testtool",
		Files: []AuthFileSpec{
			{Tool: "testtool", Path: requiredPath, Required: true},
			{Tool: "testtool", Path: optionalPath, Required: false},
		},
	}
	v := NewVault(vaultDir)

	if err := os.WriteFile(requiredPath, []byte(`{"required":"with-optional"}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(optionalPath, []byte(`{"optional":"with-optional"}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := v.Backup(fileSet, "with-optional"); err != nil {
		t.Fatalf("Backup(with-optional): %v", err)
	}

	if err := os.WriteFile(requiredPath, []byte(`{"required":"required-only"}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(optionalPath, optionalPath+".away"); err != nil {
		t.Fatal(err)
	}
	if err := v.Backup(fileSet, "required-only"); err != nil {
		t.Fatalf("Backup(required-only): %v", err)
	}

	if err := v.Restore(fileSet, "with-optional"); err != nil {
		t.Fatalf("Restore(with-optional): %v", err)
	}
	if _, err := os.Stat(optionalPath); err != nil {
		t.Fatalf("optional should exist after restoring with-optional: %v", err)
	}

	if err := v.Restore(fileSet, "required-only"); err != nil {
		t.Fatalf("Restore(required-only): %v", err)
	}
	if _, err := os.Stat(optionalPath); !os.IsNotExist(err) {
		t.Fatalf("optional should be quarantined away after restoring required-only, stat err=%v", err)
	}
	if got := string(readSingleQuarantinedFile(t, v.ProfilePath("testtool", "required-only"), "optional.json")); got != `{"optional":"with-optional"}` {
		t.Fatalf("quarantined optional content = %q", got)
	}
}

func TestVaultBackupQuarantinesStaleOptionalVaultFile(t *testing.T) {
	tmpDir := t.TempDir()
	vaultDir := filepath.Join(tmpDir, "vault")
	authDir := filepath.Join(tmpDir, "auth")
	if err := os.MkdirAll(authDir, 0700); err != nil {
		t.Fatal(err)
	}

	requiredPath := filepath.Join(authDir, "required.json")
	optionalPath := filepath.Join(authDir, "optional.json")
	fileSet := AuthFileSet{
		Tool: "testtool",
		Files: []AuthFileSpec{
			{Tool: "testtool", Path: requiredPath, Required: true},
			{Tool: "testtool", Path: optionalPath, Required: false},
		},
	}
	v := NewVault(vaultDir)

	if err := os.WriteFile(requiredPath, []byte(`{"required":"v1"}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(optionalPath, []byte(`{"optional":"v1"}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := v.Backup(fileSet, "acct"); err != nil {
		t.Fatalf("Backup(v1): %v", err)
	}

	if err := os.WriteFile(requiredPath, []byte(`{"required":"v2"}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(optionalPath, optionalPath+".away"); err != nil {
		t.Fatal(err)
	}
	if err := v.Backup(fileSet, "acct"); err != nil {
		t.Fatalf("Backup(v2): %v", err)
	}

	if _, err := os.Stat(v.BackupPath("testtool", "acct", "optional.json")); !os.IsNotExist(err) {
		t.Fatalf("stale optional backup should be quarantined away, stat err=%v", err)
	}
	if got := string(readSingleQuarantinedFile(t, v.ProfilePath("testtool", "acct"), "optional.json")); got != `{"optional":"v1"}` {
		t.Fatalf("quarantined vault optional content = %q", got)
	}

	var meta struct {
		ManagedFiles []managedFileState `json:"managed_files"`
	}
	data, err := os.ReadFile(filepath.Join(v.ProfilePath("testtool", "acct"), "meta.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &meta); err != nil {
		t.Fatal(err)
	}
	var sawAbsentOptional bool
	for _, f := range meta.ManagedFiles {
		if f.VaultName == "optional.json" && !f.Present && !f.Required {
			sawAbsentOptional = true
		}
	}
	if !sawAbsentOptional {
		t.Fatalf("meta.json did not record absent optional managed file: %+v", meta.ManagedFiles)
	}

	if err := os.WriteFile(optionalPath, []byte(`{"optional":"foreign-live"}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := v.Restore(fileSet, "acct"); err != nil {
		t.Fatalf("Restore(acct): %v", err)
	}
	data, err = os.ReadFile(optionalPath)
	if err != nil {
		t.Fatalf("restore should leave unproven foreign live optional in place: %v", err)
	}
	if string(data) != `{"optional":"foreign-live"}` {
		t.Fatalf("foreign live optional changed: %q", data)
	}
}

func TestVaultBackupMissingRequiredDoesNotMutateExistingProfile(t *testing.T) {
	tmpDir := t.TempDir()
	vaultDir := filepath.Join(tmpDir, "vault")
	authDir := filepath.Join(tmpDir, "auth")
	if err := os.MkdirAll(authDir, 0700); err != nil {
		t.Fatal(err)
	}

	requiredPath := filepath.Join(authDir, "required.json")
	optionalPath := filepath.Join(authDir, "optional.json")
	fileSet := AuthFileSet{
		Tool: "testtool",
		Files: []AuthFileSpec{
			{Tool: "testtool", Path: requiredPath, Required: true},
			{Tool: "testtool", Path: optionalPath, Required: false},
		},
	}
	v := NewVault(vaultDir)

	if err := os.WriteFile(requiredPath, []byte(`{"required":"old"}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(optionalPath, []byte(`{"optional":"old"}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := v.Backup(fileSet, "acct"); err != nil {
		t.Fatalf("Backup(old): %v", err)
	}

	if err := os.Rename(requiredPath, requiredPath+".away"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(optionalPath, []byte(`{"optional":"new-foreign"}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := v.Backup(fileSet, "acct"); err == nil {
		t.Fatal("Backup() succeeded with missing required file")
	}

	data, err := os.ReadFile(v.BackupPath("testtool", "acct", "optional.json"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != `{"optional":"old"}` {
		t.Fatalf("failed backup mutated optional snapshot: %q", data)
	}
}

func TestVaultBackupPostValidationFailureDoesNotMutateExistingProfile(t *testing.T) {
	tmpDir := t.TempDir()
	vaultDir := filepath.Join(tmpDir, "vault")
	authDir := filepath.Join(tmpDir, "auth")
	if err := os.MkdirAll(authDir, 0700); err != nil {
		t.Fatal(err)
	}

	requiredPath := filepath.Join(authDir, "required.json")
	optionalPath := filepath.Join(authDir, "optional.json")
	fileSet := AuthFileSet{
		Tool: "testtool",
		Files: []AuthFileSpec{
			{Tool: "testtool", Path: requiredPath, Required: true},
			{Tool: "testtool", Path: optionalPath, Required: false},
		},
	}
	v := NewVault(vaultDir)

	if err := os.WriteFile(requiredPath, []byte(`{"required":"old"}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(optionalPath, []byte(`{"optional":"old"}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := v.Backup(fileSet, "acct"); err != nil {
		t.Fatalf("Backup(old): %v", err)
	}

	if err := os.WriteFile(requiredPath, []byte(`{"required":"new"}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(optionalPath, optionalPath+".away"); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(optionalPath, 0700); err != nil {
		t.Fatal(err)
	}
	if err := v.Backup(fileSet, "acct"); err == nil {
		t.Fatal("Backup() succeeded with directory at planned auth-file path")
	}

	data, err := os.ReadFile(v.BackupPath("testtool", "acct", "required.json"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != `{"required":"old"}` {
		t.Fatalf("failed staged backup mutated required snapshot: %q", data)
	}
	data, err = os.ReadFile(v.BackupPath("testtool", "acct", "optional.json"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != `{"optional":"old"}` {
		t.Fatalf("failed staged backup mutated optional snapshot: %q", data)
	}
}

func TestVaultBackupNoFilesDoesNotCreateProfileDir(t *testing.T) {
	tmpDir := t.TempDir()
	v := NewVault(filepath.Join(tmpDir, "vault"))
	missingPath := filepath.Join(tmpDir, "auth", "required.json")
	fileSet := AuthFileSet{
		Tool:  "testtool",
		Files: []AuthFileSpec{{Tool: "testtool", Path: missingPath, Required: true}},
	}

	if err := v.Backup(fileSet, "missing"); err == nil {
		t.Fatal("Backup() succeeded with no files")
	}
	if _, err := os.Stat(v.ProfilePath("testtool", "missing")); !os.IsNotExist(err) {
		t.Fatalf("failed backup should not create profile dir, stat err=%v", err)
	}
}

func TestVaultBackupClaudeSettingsOnlyDoesNotCreateAuthProfile(t *testing.T) {
	tmpDir := t.TempDir()
	home := filepath.Join(tmpDir, "home")
	t.Setenv("HOME", home)
	t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(home, ".config", "claude-code"))
	if err := os.MkdirAll(home, 0700); err != nil {
		t.Fatal(err)
	}
	settingsPath := filepath.Join(home, ".claude.json")
	if err := os.WriteFile(settingsPath, []byte(`{"theme":"dark"}`), 0600); err != nil {
		t.Fatal(err)
	}

	v := NewVault(filepath.Join(tmpDir, "vault"))
	if err := v.Backup(ClaudeAuthFiles(), "settings-only"); err == nil {
		t.Fatal("Backup() accepted .claude.json settings as an auth-bearing optional profile")
	}
	if _, err := os.Stat(v.ProfilePath("claude", "settings-only")); !os.IsNotExist(err) {
		t.Fatalf("settings-only Claude backup should not create a profile dir, stat err=%v", err)
	}
}

func TestVaultBackupClaudeUserSettingsWithoutAPIKeyHelperDoesNotCreateAuthProfile(t *testing.T) {
	tmpDir := t.TempDir()
	home := filepath.Join(tmpDir, "home")
	t.Setenv("HOME", home)
	t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(home, ".config", "claude-code"))
	settingsPath := filepath.Join(home, ".claude", "settings.json")
	if err := os.MkdirAll(filepath.Dir(settingsPath), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(settingsPath, []byte(`{"theme":"dark"}`), 0600); err != nil {
		t.Fatal(err)
	}

	v := NewVault(filepath.Join(tmpDir, "vault"))
	if err := v.Backup(ClaudeAuthFiles(), "plain-user-settings"); err == nil {
		t.Fatal("Backup() accepted plain .claude/settings.json as an auth-bearing optional profile")
	}
	if _, err := os.Stat(v.ProfilePath("claude", "plain-user-settings")); !os.IsNotExist(err) {
		t.Fatalf("plain user settings backup should not create a profile dir, stat err=%v", err)
	}
}

func TestVaultBackupClaudeAPIKeyHelperSettingsCreatesOptionalAuthProfile(t *testing.T) {
	tmpDir := t.TempDir()
	home := filepath.Join(tmpDir, "home")
	t.Setenv("HOME", home)
	t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(home, ".config", "claude-code"))
	settingsPath := filepath.Join(home, ".claude", "settings.json")
	if err := os.MkdirAll(filepath.Dir(settingsPath), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(settingsPath, []byte(`{"apiKeyHelper":"security find-generic-password -w -s claude"}`), 0600); err != nil {
		t.Fatal(err)
	}

	v := NewVault(filepath.Join(tmpDir, "vault"))
	if err := v.Backup(ClaudeAuthFiles(), "api-key-helper"); err != nil {
		t.Fatalf("Backup() rejected API-key-helper settings: %v", err)
	}
	if _, err := os.Stat(v.ProfilePath("claude", "api-key-helper")); err != nil {
		t.Fatalf("API-key-helper backup should create a profile dir: %v", err)
	}
}

func TestClaudeSettingsOnlyDoesNotCountAsAuthFilesOrActiveProfile(t *testing.T) {
	tmpDir := t.TempDir()
	home := filepath.Join(tmpDir, "home")
	t.Setenv("HOME", home)
	t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(home, ".config", "claude-code"))
	if err := os.MkdirAll(home, 0700); err != nil {
		t.Fatal(err)
	}
	settingsPath := filepath.Join(home, ".claude.json")
	if err := os.WriteFile(settingsPath, []byte(`{"theme":"dark"}`), 0600); err != nil {
		t.Fatal(err)
	}
	fileSet := ClaudeAuthFiles()
	if HasAuthFiles(fileSet) {
		t.Fatal("HasAuthFiles() treated .claude.json settings as auth")
	}

	v := NewVault(filepath.Join(tmpDir, "vault"))
	profileDir := v.ProfilePath("claude", "settings-only")
	if err := os.MkdirAll(profileDir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(profileDir, ".claude.json"), []byte(`{"theme":"dark"}`), 0600); err != nil {
		t.Fatal(err)
	}
	active, err := v.ActiveProfile(fileSet)
	if err != nil {
		t.Fatal(err)
	}
	if active != "" {
		t.Fatalf("ActiveProfile() matched settings-only Claude profile %q", active)
	}
}

func TestClaudeAPIKeyHelperSettingsCountsAsAuthFiles(t *testing.T) {
	tmpDir := t.TempDir()
	home := filepath.Join(tmpDir, "home")
	t.Setenv("HOME", home)
	t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(home, ".config", "claude-code"))
	settingsPath := filepath.Join(home, ".claude", "settings.json")
	if err := os.MkdirAll(filepath.Dir(settingsPath), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(settingsPath, []byte(`{"apiKeyHelper":"op read op://vault/claude/key"}`), 0600); err != nil {
		t.Fatal(err)
	}
	if !HasAuthFiles(ClaudeAuthFiles()) {
		t.Fatal("HasAuthFiles() did not treat apiKeyHelper settings as auth")
	}
}

func TestVaultBackupCurrentUsesUniqueNames(t *testing.T) {
	oldNow := timeNow
	timeNow = func() time.Time {
		return time.Date(2026, 7, 5, 12, 0, 0, 123456789, time.UTC)
	}
	t.Cleanup(func() { timeNow = oldNow })

	tmpDir := t.TempDir()
	v := NewVault(filepath.Join(tmpDir, "vault"))
	authDir := filepath.Join(tmpDir, "auth")
	if err := os.MkdirAll(authDir, 0700); err != nil {
		t.Fatal(err)
	}
	authPath := filepath.Join(authDir, "auth.json")
	if err := os.WriteFile(authPath, []byte(`{"token":"current"}`), 0600); err != nil {
		t.Fatal(err)
	}
	fileSet := AuthFileSet{
		Tool:  "testtool",
		Files: []AuthFileSpec{{Tool: "testtool", Path: authPath, Required: true}},
	}

	first, err := v.BackupCurrent(fileSet)
	if err != nil {
		t.Fatalf("BackupCurrent(first): %v", err)
	}
	second, err := v.BackupCurrent(fileSet)
	if err != nil {
		t.Fatalf("BackupCurrent(second): %v", err)
	}
	if first == "" || second == "" || first == second {
		t.Fatalf("BackupCurrent names = %q, %q; want distinct non-empty names", first, second)
	}
}

func TestVaultRejectsDuplicateVaultNames(t *testing.T) {
	tmpDir := t.TempDir()
	v := NewVault(filepath.Join(tmpDir, "vault"))
	fileSet := AuthFileSet{
		Tool: "testtool",
		Files: []AuthFileSpec{
			{Tool: "testtool", Path: filepath.Join(tmpDir, "a", "auth.json"), Required: false},
			{Tool: "testtool", Path: filepath.Join(tmpDir, "b", "auth.json"), Required: false},
		},
		AllowOptionalOnly: true,
	}

	if err := v.Backup(fileSet, "dup"); err == nil {
		t.Fatal("Backup() accepted duplicate vault basenames")
	}
	if err := v.Restore(fileSet, "dup"); err == nil {
		t.Fatal("Restore() accepted duplicate vault basenames")
	}
}

func TestVaultRejectsDotPrefixedProfileName(t *testing.T) {
	tmpDir := t.TempDir()
	v := NewVault(filepath.Join(tmpDir, "vault"))
	authDir := filepath.Join(tmpDir, "auth")
	if err := os.MkdirAll(authDir, 0700); err != nil {
		t.Fatal(err)
	}
	authPath := filepath.Join(authDir, "auth.json")
	if err := os.WriteFile(authPath, []byte(`{"token":"x"}`), 0600); err != nil {
		t.Fatal(err)
	}
	fileSet := AuthFileSet{
		Tool:  "testtool",
		Files: []AuthFileSpec{{Tool: "testtool", Path: authPath, Required: true}},
	}
	if err := v.Backup(fileSet, ".work"); err == nil {
		t.Fatal("Backup() accepted dot-prefixed profile name")
	}
}

func TestVaultRestoreRequiresExactCleanupProof(t *testing.T) {
	tmpDir := t.TempDir()
	vaultDir := filepath.Join(tmpDir, "vault")
	authDir := filepath.Join(tmpDir, "auth")
	if err := os.MkdirAll(authDir, 0700); err != nil {
		t.Fatal(err)
	}

	requiredPath := filepath.Join(authDir, "required.json")
	optionalPath := filepath.Join(authDir, ".claude.json")
	fileSet := AuthFileSet{
		Tool: "claude",
		Files: []AuthFileSpec{
			{Tool: "claude", Path: requiredPath, Required: true},
			{Tool: "claude", Path: optionalPath, Required: false},
		},
	}
	v := NewVault(vaultDir)

	if err := os.WriteFile(requiredPath, []byte(`{"required":"with-settings"}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(optionalPath, []byte(`{"theme":"snapshot"}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := v.Backup(fileSet, "with-settings"); err != nil {
		t.Fatalf("Backup(with-settings): %v", err)
	}

	if err := os.WriteFile(requiredPath, []byte(`{"required":"without-settings"}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(optionalPath, optionalPath+".away"); err != nil {
		t.Fatal(err)
	}
	if err := v.Backup(fileSet, "without-settings"); err != nil {
		t.Fatalf("Backup(without-settings): %v", err)
	}

	if err := os.WriteFile(optionalPath, []byte(`{"theme":"foreign-live-user-data"}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := v.Restore(fileSet, "without-settings"); err != nil {
		t.Fatalf("Restore(without-settings): %v", err)
	}
	data, err := os.ReadFile(optionalPath)
	if err != nil {
		t.Fatalf("restore should leave byte-different settings-only file in place: %v", err)
	}
	if string(data) != `{"theme":"foreign-live-user-data"}` {
		t.Fatalf("settings-only live file changed: %q", data)
	}
}

func TestVaultRestoreLegacyProfileIsNotCleanupProof(t *testing.T) {
	tmpDir := t.TempDir()
	vaultDir := filepath.Join(tmpDir, "vault")
	authDir := filepath.Join(tmpDir, "auth")
	if err := os.MkdirAll(authDir, 0700); err != nil {
		t.Fatal(err)
	}

	requiredPath := filepath.Join(authDir, "required.json")
	optionalPath := filepath.Join(authDir, "optional.json")
	fileSet := AuthFileSet{
		Tool: "testtool",
		Files: []AuthFileSpec{
			{Tool: "testtool", Path: requiredPath, Required: true},
			{Tool: "testtool", Path: optionalPath, Required: false},
		},
	}
	v := NewVault(vaultDir)

	if err := os.WriteFile(requiredPath, []byte(`{"required":"target"}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := v.Backup(fileSet, "target-without-optional"); err != nil {
		t.Fatalf("Backup(target-without-optional): %v", err)
	}

	legacyDir := v.ProfilePath("testtool", "legacy-proof")
	if err := os.MkdirAll(legacyDir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(legacyDir, "optional.json"), []byte(`{"optional":"legacy-proof"}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(legacyDir, "meta.json"), []byte(`{"tool":"testtool","profile":"legacy-proof","files":1}`), 0600); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(optionalPath, []byte(`{"optional":"legacy-proof"}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := v.Restore(fileSet, "target-without-optional"); err != nil {
		t.Fatalf("Restore(target-without-optional): %v", err)
	}
	data, err := os.ReadFile(optionalPath)
	if err != nil {
		t.Fatalf("restore should leave live optional when only proof is legacy: %v", err)
	}
	if string(data) != `{"optional":"legacy-proof"}` {
		t.Fatalf("legacy-proof live optional changed: %q", data)
	}
}

func TestVaultRestoreAbsentMetadataMustMatchSpecPath(t *testing.T) {
	tmpDir := t.TempDir()
	vaultDir := filepath.Join(tmpDir, "vault")
	authDir := filepath.Join(tmpDir, "auth")
	oldDir := filepath.Join(tmpDir, "old")
	newDir := filepath.Join(tmpDir, "new")
	for _, dir := range []string{authDir, oldDir, newDir} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}

	requiredPath := filepath.Join(authDir, "required.json")
	oldOptionalPath := filepath.Join(oldDir, "optional.json")
	newOptionalPath := filepath.Join(newDir, "optional.json")
	fileSet := AuthFileSet{
		Tool: "testtool",
		Files: []AuthFileSpec{
			{Tool: "testtool", Path: requiredPath, Required: true},
			{Tool: "testtool", Path: newOptionalPath, Required: false},
		},
	}
	v := NewVault(vaultDir)

	if err := os.WriteFile(requiredPath, []byte(`{"required":"proof"}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(newOptionalPath, []byte(`{"optional":"proof"}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := v.Backup(fileSet, "current-proof"); err != nil {
		t.Fatalf("Backup(current-proof): %v", err)
	}

	targetDir := v.ProfilePath("testtool", "target-old-absent")
	if err := os.MkdirAll(targetDir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(targetDir, "required.json"), []byte(`{"required":"target"}`), 0600); err != nil {
		t.Fatal(err)
	}
	meta := vaultProfileMeta{
		Tool:            "testtool",
		Profile:         "target-old-absent",
		BackedUpAt:      time.Now().Format(time.RFC3339),
		Files:           1,
		ManifestVersion: 1,
		ManagedFiles: []managedFileState{
			{VaultName: "required.json", OriginalPath: requiredPath, Required: true, Present: true},
			{VaultName: "optional.json", OriginalPath: oldOptionalPath, Required: false, Present: false},
		},
	}
	raw, err := json.Marshal(meta)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(targetDir, "meta.json"), raw, 0600); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(newOptionalPath, []byte(`{"optional":"proof"}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := v.Restore(fileSet, "target-old-absent"); err != nil {
		t.Fatalf("Restore(target-old-absent): %v", err)
	}
	data, err := os.ReadFile(newOptionalPath)
	if err != nil {
		t.Fatalf("restore should leave current-path optional in place: %v", err)
	}
	if string(data) != `{"optional":"proof"}` {
		t.Fatalf("current-path optional changed: %q", data)
	}
}

func TestVaultRestoreOptionalOnlyQuarantinesStaleRequiredLiveFile(t *testing.T) {
	tmpDir := t.TempDir()
	vaultDir := filepath.Join(tmpDir, "vault")
	authDir := filepath.Join(tmpDir, "auth")
	if err := os.MkdirAll(authDir, 0700); err != nil {
		t.Fatal(err)
	}

	requiredPath := filepath.Join(authDir, "required.json")
	optionalPath := filepath.Join(authDir, "optional.json")
	fileSet := AuthFileSet{
		Tool: "testtool",
		Files: []AuthFileSpec{
			{Tool: "testtool", Path: requiredPath, Required: true},
			{Tool: "testtool", Path: optionalPath, Required: false},
		},
		AllowOptionalOnly: true,
	}
	v := NewVault(vaultDir)

	if err := os.WriteFile(requiredPath, []byte(`{"required":"with-required"}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(optionalPath, []byte(`{"optional":"with-required"}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := v.Backup(fileSet, "with-required"); err != nil {
		t.Fatalf("Backup(with-required): %v", err)
	}

	if err := os.Rename(requiredPath, requiredPath+".away"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(optionalPath, []byte(`{"optional":"optional-only"}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := v.Backup(fileSet, "optional-only"); err != nil {
		t.Fatalf("Backup(optional-only): %v", err)
	}

	if err := v.Restore(fileSet, "with-required"); err != nil {
		t.Fatalf("Restore(with-required): %v", err)
	}
	if err := v.Restore(fileSet, "optional-only"); err != nil {
		t.Fatalf("Restore(optional-only): %v", err)
	}
	if _, err := os.Stat(requiredPath); !os.IsNotExist(err) {
		t.Fatalf("required auth should be quarantined away for optional-only profile, stat err=%v", err)
	}
	if got := string(readSingleQuarantinedFile(t, v.ProfilePath("testtool", "optional-only"), "required.json")); got != `{"required":"with-required"}` {
		t.Fatalf("quarantined required content = %q", got)
	}
}

func TestVaultRestorePresentMetadataMustMatchSpecPath(t *testing.T) {
	tmpDir := t.TempDir()
	vaultDir := filepath.Join(tmpDir, "vault")
	authDir := filepath.Join(tmpDir, "auth")
	oldDir := filepath.Join(tmpDir, "old")
	newDir := filepath.Join(tmpDir, "new")
	for _, dir := range []string{authDir, oldDir, newDir} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}

	requiredPath := filepath.Join(authDir, "required.json")
	oldOptionalPath := filepath.Join(oldDir, "optional.json")
	newOptionalPath := filepath.Join(newDir, "optional.json")
	v := NewVault(vaultDir)
	targetDir := v.ProfilePath("testtool", "target-old-present")
	if err := os.MkdirAll(targetDir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(targetDir, "required.json"), []byte(`{"required":"target"}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(targetDir, "optional.json"), []byte(`{"optional":"old-path"}`), 0600); err != nil {
		t.Fatal(err)
	}
	meta := vaultProfileMeta{
		Tool:            "testtool",
		Profile:         "target-old-present",
		BackedUpAt:      time.Now().Format(time.RFC3339),
		Files:           2,
		ManifestVersion: 1,
		ManagedFiles: []managedFileState{
			{VaultName: "required.json", OriginalPath: requiredPath, Required: true, Present: true},
			{VaultName: "optional.json", OriginalPath: oldOptionalPath, Required: false, Present: true},
		},
	}
	raw, err := json.Marshal(meta)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(targetDir, "meta.json"), raw, 0600); err != nil {
		t.Fatal(err)
	}

	fileSet := AuthFileSet{
		Tool: "testtool",
		Files: []AuthFileSpec{
			{Tool: "testtool", Path: requiredPath, Required: true},
			{Tool: "testtool", Path: newOptionalPath, Required: false},
		},
	}
	if err := v.Restore(fileSet, "target-old-present"); err != nil {
		t.Fatalf("Restore(target-old-present): %v", err)
	}
	if _, err := os.Stat(newOptionalPath); !os.IsNotExist(err) {
		t.Fatalf("restore should not copy old-path optional to current path, stat err=%v", err)
	}
}

func TestVaultRestoreManagedManifestMissingEntryDoesNotRestoreStaleBasename(t *testing.T) {
	tmpDir := t.TempDir()
	vaultDir := filepath.Join(tmpDir, "vault")
	authDir := filepath.Join(tmpDir, "auth")
	if err := os.MkdirAll(authDir, 0700); err != nil {
		t.Fatal(err)
	}

	requiredPath := filepath.Join(authDir, "required.json")
	optionalPath := filepath.Join(authDir, "optional.json")
	v := NewVault(vaultDir)
	targetDir := v.ProfilePath("testtool", "managed-missing-entry")
	if err := os.MkdirAll(targetDir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(targetDir, "required.json"), []byte(`{"required":"target"}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(targetDir, "optional.json"), []byte(`{"optional":"stale-basename"}`), 0600); err != nil {
		t.Fatal(err)
	}
	meta := vaultProfileMeta{
		Tool:            "testtool",
		Profile:         "managed-missing-entry",
		BackedUpAt:      time.Now().Format(time.RFC3339),
		Files:           1,
		ManifestVersion: 1,
		ManagedFiles: []managedFileState{
			{VaultName: "required.json", OriginalPath: requiredPath, Required: true, Present: true},
		},
	}
	raw, err := json.Marshal(meta)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(targetDir, "meta.json"), raw, 0600); err != nil {
		t.Fatal(err)
	}

	fileSet := AuthFileSet{
		Tool: "testtool",
		Files: []AuthFileSpec{
			{Tool: "testtool", Path: requiredPath, Required: true},
			{Tool: "testtool", Path: optionalPath, Required: false},
		},
	}
	if err := v.Restore(fileSet, "managed-missing-entry"); err != nil {
		t.Fatalf("Restore(managed-missing-entry): %v", err)
	}
	if _, err := os.Stat(optionalPath); !os.IsNotExist(err) {
		t.Fatalf("managed restore should not copy stale optional basename without metadata entry, stat err=%v", err)
	}
}

func TestVaultRestoreManagedManifestMissingRequiredEntryFailsClosed(t *testing.T) {
	tmpDir := t.TempDir()
	vaultDir := filepath.Join(tmpDir, "vault")
	authDir := filepath.Join(tmpDir, "auth")
	if err := os.MkdirAll(authDir, 0700); err != nil {
		t.Fatal(err)
	}

	requiredPath := filepath.Join(authDir, "required.json")
	optionalPath := filepath.Join(authDir, "optional.json")
	v := NewVault(vaultDir)
	targetDir := v.ProfilePath("testtool", "missing-required-entry")
	if err := os.MkdirAll(targetDir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(targetDir, "required.json"), []byte(`{"required":"stale-basename"}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(targetDir, "optional.json"), []byte(`{"optional":"target"}`), 0600); err != nil {
		t.Fatal(err)
	}
	meta := vaultProfileMeta{
		Tool:            "testtool",
		Profile:         "missing-required-entry",
		BackedUpAt:      time.Now().Format(time.RFC3339),
		Files:           1,
		ManifestVersion: 1,
		ManagedFiles: []managedFileState{
			{VaultName: "optional.json", OriginalPath: optionalPath, Required: false, Present: true},
		},
	}
	raw, err := json.Marshal(meta)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(targetDir, "meta.json"), raw, 0600); err != nil {
		t.Fatal(err)
	}

	fileSet := AuthFileSet{
		Tool: "testtool",
		Files: []AuthFileSpec{
			{Tool: "testtool", Path: requiredPath, Required: true},
			{Tool: "testtool", Path: optionalPath, Required: false},
		},
		AllowOptionalOnly: true,
	}
	if err := v.Restore(fileSet, "missing-required-entry"); err == nil {
		t.Fatal("Restore() succeeded with required spec missing from managed metadata")
	}
	if _, err := os.Stat(requiredPath); !os.IsNotExist(err) {
		t.Fatalf("managed restore should not copy stale required basename without metadata entry, stat err=%v", err)
	}
	if _, err := os.Stat(optionalPath); !os.IsNotExist(err) {
		t.Fatalf("restore should fail before mutating optional auth, stat err=%v", err)
	}
}

func TestVaultRestoreMalformedManagedMetadataFailsClosed(t *testing.T) {
	tmpDir := t.TempDir()
	vaultDir := filepath.Join(tmpDir, "vault")
	authDir := filepath.Join(tmpDir, "auth")
	if err := os.MkdirAll(authDir, 0700); err != nil {
		t.Fatal(err)
	}

	requiredPath := filepath.Join(authDir, "required.json")
	v := NewVault(vaultDir)
	targetDir := v.ProfilePath("testtool", "malformed-meta")
	if err := os.MkdirAll(targetDir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(targetDir, "required.json"), []byte(`{"required":"target"}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(targetDir, "meta.json"), []byte(`{`), 0600); err != nil {
		t.Fatal(err)
	}

	fileSet := AuthFileSet{
		Tool:  "testtool",
		Files: []AuthFileSpec{{Tool: "testtool", Path: requiredPath, Required: true}},
	}
	if err := v.Restore(fileSet, "malformed-meta"); err == nil {
		t.Fatal("Restore() succeeded with malformed managed metadata")
	}
	if _, err := os.Stat(requiredPath); !os.IsNotExist(err) {
		t.Fatalf("malformed metadata should fail before restoring live auth, stat err=%v", err)
	}
}

func TestVaultRestoreManagedEmptyMetadataFailsClosed(t *testing.T) {
	tmpDir := t.TempDir()
	vaultDir := filepath.Join(tmpDir, "vault")
	authDir := filepath.Join(tmpDir, "auth")
	if err := os.MkdirAll(authDir, 0700); err != nil {
		t.Fatal(err)
	}

	requiredPath := filepath.Join(authDir, "required.json")
	v := NewVault(vaultDir)
	targetDir := v.ProfilePath("testtool", "empty-managed-meta")
	if err := os.MkdirAll(targetDir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(targetDir, "required.json"), []byte(`{"required":"target"}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(targetDir, "meta.json"), []byte(`{"manifest_version":1,"managed_files":[]}`), 0600); err != nil {
		t.Fatal(err)
	}

	fileSet := AuthFileSet{
		Tool:  "testtool",
		Files: []AuthFileSpec{{Tool: "testtool", Path: requiredPath, Required: true}},
	}
	if err := v.Restore(fileSet, "empty-managed-meta"); err == nil {
		t.Fatal("Restore() succeeded with empty managed metadata")
	}
	if _, err := os.Stat(requiredPath); !os.IsNotExist(err) {
		t.Fatalf("empty managed metadata should fail before restoring live auth, stat err=%v", err)
	}
}

func TestVaultRestoreInvalidManagedMetadataEntryFailsClosed(t *testing.T) {
	tmpDir := t.TempDir()
	vaultDir := filepath.Join(tmpDir, "vault")
	authDir := filepath.Join(tmpDir, "auth")
	if err := os.MkdirAll(authDir, 0700); err != nil {
		t.Fatal(err)
	}

	requiredPath := filepath.Join(authDir, "required.json")
	v := NewVault(vaultDir)
	targetDir := v.ProfilePath("testtool", "invalid-managed-entry")
	if err := os.MkdirAll(targetDir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(targetDir, "required.json"), []byte(`{"required":"target"}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(targetDir, "meta.json"), []byte(`{"manifest_version":1,"managed_files":[{"vault_name":"","original_path":"","present":true}]}`), 0600); err != nil {
		t.Fatal(err)
	}

	fileSet := AuthFileSet{
		Tool:  "testtool",
		Files: []AuthFileSpec{{Tool: "testtool", Path: requiredPath, Required: true}},
	}
	if err := v.Restore(fileSet, "invalid-managed-entry"); err == nil {
		t.Fatal("Restore() succeeded with invalid managed metadata entry")
	}
	if _, err := os.Stat(requiredPath); !os.IsNotExist(err) {
		t.Fatalf("invalid managed metadata should fail before restoring live auth, stat err=%v", err)
	}
}

func TestVaultRestoreGeminiManagedMetadataUsesMigratedOAuthName(t *testing.T) {
	tmpDir := t.TempDir()
	vaultDir := filepath.Join(tmpDir, "vault")
	geminiDir := filepath.Join(tmpDir, ".gemini")
	if err := os.MkdirAll(geminiDir, 0700); err != nil {
		t.Fatal(err)
	}

	currentPath := filepath.Join(geminiDir, "oauth_creds.json")
	legacyPath := filepath.Join(geminiDir, "oauth_credentials.json")
	v := NewVault(vaultDir)
	targetDir := v.ProfilePath("gemini", "legacy-managed")
	if err := os.MkdirAll(targetDir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(targetDir, "oauth_credentials.json"), []byte(`{"token":"legacy"}`), 0600); err != nil {
		t.Fatal(err)
	}
	meta := vaultProfileMeta{
		Tool:            "gemini",
		Profile:         "legacy-managed",
		BackedUpAt:      time.Now().Format(time.RFC3339),
		Files:           1,
		ManifestVersion: 1,
		ManagedFiles: []managedFileState{
			{VaultName: "oauth_credentials.json", OriginalPath: legacyPath, Required: true, Present: true},
		},
	}
	raw, err := json.Marshal(meta)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(targetDir, "meta.json"), raw, 0600); err != nil {
		t.Fatal(err)
	}

	fileSet := AuthFileSet{
		Tool:  "gemini",
		Files: []AuthFileSpec{{Tool: "gemini", Path: currentPath, Required: true}},
	}
	if err := v.Restore(fileSet, "legacy-managed"); err != nil {
		t.Fatalf("Restore(legacy-managed): %v", err)
	}
	data, err := os.ReadFile(currentPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != `{"token":"legacy"}` {
		t.Fatalf("restored Gemini OAuth content = %q", data)
	}
}

func TestVaultRestoreGeminiManagedMetadataUsesLegacySnapshotWhenBothOAuthNamesExist(t *testing.T) {
	tmpDir := t.TempDir()
	vaultDir := filepath.Join(tmpDir, "vault")
	geminiDir := filepath.Join(tmpDir, ".gemini")
	if err := os.MkdirAll(geminiDir, 0700); err != nil {
		t.Fatal(err)
	}

	currentPath := filepath.Join(geminiDir, "oauth_creds.json")
	legacyPath := filepath.Join(geminiDir, "oauth_credentials.json")
	v := NewVault(vaultDir)
	targetDir := v.ProfilePath("gemini", "legacy-managed-both")
	if err := os.MkdirAll(targetDir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(targetDir, "oauth_credentials.json"), []byte(`{"token":"metadata-referenced"}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(targetDir, "oauth_creds.json"), []byte(`{"token":"stale-new-name"}`), 0600); err != nil {
		t.Fatal(err)
	}
	meta := vaultProfileMeta{
		Tool:            "gemini",
		Profile:         "legacy-managed-both",
		BackedUpAt:      time.Now().Format(time.RFC3339),
		Files:           1,
		ManifestVersion: 1,
		ManagedFiles: []managedFileState{
			{VaultName: "oauth_credentials.json", OriginalPath: legacyPath, Required: true, Present: true},
		},
	}
	raw, err := json.Marshal(meta)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(targetDir, "meta.json"), raw, 0600); err != nil {
		t.Fatal(err)
	}

	fileSet := AuthFileSet{
		Tool:  "gemini",
		Files: []AuthFileSpec{{Tool: "gemini", Path: currentPath, Required: true}},
	}
	if err := v.Restore(fileSet, "legacy-managed-both"); err != nil {
		t.Fatalf("Restore(legacy-managed-both): %v", err)
	}
	data, err := os.ReadFile(currentPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != `{"token":"metadata-referenced"}` {
		t.Fatalf("restored Gemini OAuth content = %q", data)
	}
}

func TestVaultRestoreGeminiManagedInvalidMetaDoesNotMigrate(t *testing.T) {
	tmpDir := t.TempDir()
	vaultDir := filepath.Join(tmpDir, "vault")
	geminiDir := filepath.Join(tmpDir, ".gemini")
	if err := os.MkdirAll(geminiDir, 0700); err != nil {
		t.Fatal(err)
	}

	currentPath := filepath.Join(geminiDir, "oauth_creds.json")
	v := NewVault(vaultDir)
	targetDir := v.ProfilePath("gemini", "invalid-managed")
	if err := os.MkdirAll(targetDir, 0700); err != nil {
		t.Fatal(err)
	}
	oldVaultPath := filepath.Join(targetDir, "oauth_credentials.json")
	newVaultPath := filepath.Join(targetDir, "oauth_creds.json")
	if err := os.WriteFile(oldVaultPath, []byte(`{"token":"old"}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(newVaultPath, []byte(`{"token":"new"}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(targetDir, "meta.json"), []byte(`{"manifest_version":1,"managed_files":[]}`), 0600); err != nil {
		t.Fatal(err)
	}

	fileSet := AuthFileSet{
		Tool:  "gemini",
		Files: []AuthFileSpec{{Tool: "gemini", Path: currentPath, Required: true}},
	}
	if err := v.Restore(fileSet, "invalid-managed"); err == nil {
		t.Fatal("Restore() succeeded with invalid managed Gemini metadata")
	}
	oldData, err := os.ReadFile(oldVaultPath)
	if err != nil {
		t.Fatalf("old Gemini vault file was touched: %v", err)
	}
	newData, err := os.ReadFile(newVaultPath)
	if err != nil {
		t.Fatalf("new Gemini vault file was touched: %v", err)
	}
	if string(oldData) != `{"token":"old"}` || string(newData) != `{"token":"new"}` {
		t.Fatalf("Gemini vault files changed: old=%q new=%q", oldData, newData)
	}
	if _, err := os.Stat(currentPath); !os.IsNotExist(err) {
		t.Fatalf("invalid metadata should fail before restoring live Gemini auth, stat err=%v", err)
	}
}

func TestVaultRestoreManagedUsesMetadataVaultNameForSource(t *testing.T) {
	tmpDir := t.TempDir()
	vaultDir := filepath.Join(tmpDir, "vault")
	authDir := filepath.Join(tmpDir, "auth")
	if err := os.MkdirAll(authDir, 0700); err != nil {
		t.Fatal(err)
	}

	authPath := filepath.Join(authDir, "auth.json")
	v := NewVault(vaultDir)
	targetDir := v.ProfilePath("testtool", "custom-vault-name")
	if err := os.MkdirAll(targetDir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(targetDir, "auth.snapshot"), []byte(`{"token":"snapshot"}`), 0600); err != nil {
		t.Fatal(err)
	}
	meta := vaultProfileMeta{
		Tool:            "testtool",
		Profile:         "custom-vault-name",
		BackedUpAt:      time.Now().Format(time.RFC3339),
		Files:           1,
		ManifestVersion: 1,
		ManagedFiles: []managedFileState{
			{VaultName: "auth.snapshot", OriginalPath: authPath, Required: true, Present: true},
		},
	}
	raw, err := json.Marshal(meta)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(targetDir, "meta.json"), raw, 0600); err != nil {
		t.Fatal(err)
	}

	fileSet := AuthFileSet{
		Tool:  "testtool",
		Files: []AuthFileSpec{{Tool: "testtool", Path: authPath, Required: true}},
	}
	if err := v.Restore(fileSet, "custom-vault-name"); err != nil {
		t.Fatalf("Restore(custom-vault-name): %v", err)
	}
	data, err := os.ReadFile(authPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != `{"token":"snapshot"}` {
		t.Fatalf("restored auth content = %q", data)
	}
}

func TestVaultRestoreManagedOriginalPathMatchBeatsStaleBasename(t *testing.T) {
	tmpDir := t.TempDir()
	vaultDir := filepath.Join(tmpDir, "vault")
	authDir := filepath.Join(tmpDir, "auth")
	oldDir := filepath.Join(tmpDir, "old")
	for _, dir := range []string{authDir, oldDir} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}

	authPath := filepath.Join(authDir, "auth.json")
	oldPath := filepath.Join(oldDir, "auth.json")
	v := NewVault(vaultDir)
	targetDir := v.ProfilePath("testtool", "shadowed-basename")
	if err := os.MkdirAll(targetDir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(targetDir, "auth.json"), []byte(`{"token":"stale-basename"}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(targetDir, "auth.snapshot"), []byte(`{"token":"correct"}`), 0600); err != nil {
		t.Fatal(err)
	}
	meta := vaultProfileMeta{
		Tool:            "testtool",
		Profile:         "shadowed-basename",
		BackedUpAt:      time.Now().Format(time.RFC3339),
		Files:           2,
		ManifestVersion: 1,
		ManagedFiles: []managedFileState{
			{VaultName: "auth.json", OriginalPath: oldPath, Required: true, Present: true},
			{VaultName: "auth.snapshot", OriginalPath: authPath, Required: true, Present: true},
		},
	}
	raw, err := json.Marshal(meta)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(targetDir, "meta.json"), raw, 0600); err != nil {
		t.Fatal(err)
	}

	fileSet := AuthFileSet{
		Tool:  "testtool",
		Files: []AuthFileSpec{{Tool: "testtool", Path: authPath, Required: true}},
	}
	if err := v.Restore(fileSet, "shadowed-basename"); err != nil {
		t.Fatalf("Restore(shadowed-basename): %v", err)
	}
	data, err := os.ReadFile(authPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != `{"token":"correct"}` {
		t.Fatalf("restored auth content = %q", data)
	}
}

func TestVaultRestoreManagedCleanupProofUsesMetadataVaultName(t *testing.T) {
	tmpDir := t.TempDir()
	vaultDir := filepath.Join(tmpDir, "vault")
	authDir := filepath.Join(tmpDir, "auth")
	if err := os.MkdirAll(authDir, 0700); err != nil {
		t.Fatal(err)
	}

	requiredPath := filepath.Join(authDir, "required.json")
	optionalPath := filepath.Join(authDir, "optional.json")
	v := NewVault(vaultDir)
	proofDir := v.ProfilePath("testtool", "proof")
	if err := os.MkdirAll(proofDir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(proofDir, "optional.snapshot"), []byte(`{"optional":"stale"}`), 0600); err != nil {
		t.Fatal(err)
	}
	proofMeta := vaultProfileMeta{
		Tool:            "testtool",
		Profile:         "proof",
		BackedUpAt:      time.Now().Format(time.RFC3339),
		Files:           1,
		ManifestVersion: 1,
		ManagedFiles: []managedFileState{
			{VaultName: "optional.snapshot", OriginalPath: optionalPath, Required: false, Present: true},
		},
	}
	raw, err := json.Marshal(proofMeta)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(proofDir, "meta.json"), raw, 0600); err != nil {
		t.Fatal(err)
	}

	targetDir := v.ProfilePath("testtool", "target")
	if err := os.MkdirAll(targetDir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(targetDir, "required.json"), []byte(`{"required":"target"}`), 0600); err != nil {
		t.Fatal(err)
	}
	targetMeta := vaultProfileMeta{
		Tool:            "testtool",
		Profile:         "target",
		BackedUpAt:      time.Now().Format(time.RFC3339),
		Files:           1,
		ManifestVersion: 1,
		ManagedFiles: []managedFileState{
			{VaultName: "required.json", OriginalPath: requiredPath, Required: true, Present: true},
			{VaultName: "optional.snapshot", OriginalPath: optionalPath, Required: false, Present: false},
		},
	}
	raw, err = json.Marshal(targetMeta)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(targetDir, "meta.json"), raw, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(optionalPath, []byte(`{"optional":"stale"}`), 0600); err != nil {
		t.Fatal(err)
	}

	fileSet := AuthFileSet{
		Tool: "testtool",
		Files: []AuthFileSpec{
			{Tool: "testtool", Path: requiredPath, Required: true},
			{Tool: "testtool", Path: optionalPath, Required: false},
		},
	}
	if err := v.Restore(fileSet, "target"); err != nil {
		t.Fatalf("Restore(target): %v", err)
	}
	if _, err := os.Stat(optionalPath); !os.IsNotExist(err) {
		t.Fatalf("cleanup proof with metadata vault name should quarantine live optional, stat err=%v", err)
	}
}

func TestVaultRestoreManagedPreflightOptionalBeforeRequiredAbsentDoesNotMutate(t *testing.T) {
	tmpDir := t.TempDir()
	vaultDir := filepath.Join(tmpDir, "vault")
	authDir := filepath.Join(tmpDir, "auth")
	if err := os.MkdirAll(authDir, 0700); err != nil {
		t.Fatal(err)
	}

	optionalPath := filepath.Join(authDir, "optional.json")
	requiredPath := filepath.Join(authDir, "required.json")
	v := NewVault(vaultDir)
	targetDir := v.ProfilePath("testtool", "preflight-required-absent")
	if err := os.MkdirAll(targetDir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(targetDir, "optional.json"), []byte(`{"optional":"target"}`), 0600); err != nil {
		t.Fatal(err)
	}
	meta := vaultProfileMeta{
		Tool:            "testtool",
		Profile:         "preflight-required-absent",
		BackedUpAt:      time.Now().Format(time.RFC3339),
		Files:           1,
		ManifestVersion: 1,
		ManagedFiles: []managedFileState{
			{VaultName: "optional.json", OriginalPath: optionalPath, Required: false, Present: true},
			{VaultName: "required.json", OriginalPath: requiredPath, Required: true, Present: false},
		},
	}
	raw, err := json.Marshal(meta)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(targetDir, "meta.json"), raw, 0600); err != nil {
		t.Fatal(err)
	}

	fileSet := AuthFileSet{
		Tool: "testtool",
		Files: []AuthFileSpec{
			{Tool: "testtool", Path: optionalPath, Required: false},
			{Tool: "testtool", Path: requiredPath, Required: true},
		},
	}
	if err := v.Restore(fileSet, "preflight-required-absent"); err == nil {
		t.Fatal("Restore() succeeded with required managed auth absent and optional-only disabled")
	}
	if _, err := os.Stat(optionalPath); !os.IsNotExist(err) {
		t.Fatalf("preflight should fail before restoring earlier optional auth, stat err=%v", err)
	}
}

func TestVaultRestoreClaudeManagedSettingsOnlyDoesNotMutate(t *testing.T) {
	tmpDir := t.TempDir()
	home := filepath.Join(tmpDir, "home")
	t.Setenv("HOME", home)
	t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(home, ".config", "claude-code"))
	if err := os.MkdirAll(home, 0700); err != nil {
		t.Fatal(err)
	}

	requiredPath := filepath.Join(home, ".claude", ".credentials.json")
	settingsPath := filepath.Join(home, ".claude.json")
	v := NewVault(filepath.Join(tmpDir, "vault"))
	targetDir := v.ProfilePath("claude", "settings-only")
	if err := os.MkdirAll(targetDir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(targetDir, ".claude.json"), []byte(`{"theme":"dark"}`), 0600); err != nil {
		t.Fatal(err)
	}
	meta := vaultProfileMeta{
		Tool:            "claude",
		Profile:         "settings-only",
		BackedUpAt:      time.Now().Format(time.RFC3339),
		Files:           1,
		ManifestVersion: 1,
		ManagedFiles: []managedFileState{
			{VaultName: ".credentials.json", OriginalPath: requiredPath, Required: true, Present: false},
			{VaultName: ".claude.json", OriginalPath: settingsPath, Required: false, Present: true},
		},
	}
	raw, err := json.Marshal(meta)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(targetDir, "meta.json"), raw, 0600); err != nil {
		t.Fatal(err)
	}

	if err := v.Restore(ClaudeAuthFiles(), "settings-only"); err == nil {
		t.Fatal("Restore() accepted .claude.json settings as an auth-bearing optional profile")
	}
	if _, err := os.Stat(settingsPath); !os.IsNotExist(err) {
		t.Fatalf("settings-only restore should fail before writing live .claude.json, stat err=%v", err)
	}
}

func TestVaultRestoreClaudeLegacySettingsOnlyDoesNotMutate(t *testing.T) {
	tmpDir := t.TempDir()
	home := filepath.Join(tmpDir, "home")
	t.Setenv("HOME", home)
	t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(home, ".config", "claude-code"))
	if err := os.MkdirAll(home, 0700); err != nil {
		t.Fatal(err)
	}

	settingsPath := filepath.Join(home, ".claude.json")
	v := NewVault(filepath.Join(tmpDir, "vault"))
	targetDir := v.ProfilePath("claude", "legacy-settings-only")
	if err := os.MkdirAll(targetDir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(targetDir, ".claude.json"), []byte(`{"theme":"dark"}`), 0600); err != nil {
		t.Fatal(err)
	}

	if err := v.Restore(ClaudeAuthFiles(), "legacy-settings-only"); err == nil {
		t.Fatal("Restore() accepted legacy .claude.json settings as an auth-bearing optional profile")
	}
	if _, err := os.Stat(settingsPath); !os.IsNotExist(err) {
		t.Fatalf("legacy settings-only restore should fail before writing live .claude.json, stat err=%v", err)
	}
}

func TestVaultRestoreManagedPreflightDirectorySourceDoesNotMutate(t *testing.T) {
	tmpDir := t.TempDir()
	vaultDir := filepath.Join(tmpDir, "vault")
	authDir := filepath.Join(tmpDir, "auth")
	if err := os.MkdirAll(authDir, 0700); err != nil {
		t.Fatal(err)
	}

	optionalPath := filepath.Join(authDir, "optional.json")
	requiredPath := filepath.Join(authDir, "required.json")
	v := NewVault(vaultDir)
	targetDir := v.ProfilePath("testtool", "preflight-directory-source")
	if err := os.MkdirAll(targetDir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(targetDir, "optional.json"), []byte(`{"optional":"target"}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(targetDir, "required.json"), 0700); err != nil {
		t.Fatal(err)
	}
	meta := vaultProfileMeta{
		Tool:            "testtool",
		Profile:         "preflight-directory-source",
		BackedUpAt:      time.Now().Format(time.RFC3339),
		Files:           2,
		ManifestVersion: 1,
		ManagedFiles: []managedFileState{
			{VaultName: "optional.json", OriginalPath: optionalPath, Required: false, Present: true},
			{VaultName: "required.json", OriginalPath: requiredPath, Required: true, Present: true},
		},
	}
	raw, err := json.Marshal(meta)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(targetDir, "meta.json"), raw, 0600); err != nil {
		t.Fatal(err)
	}

	fileSet := AuthFileSet{
		Tool: "testtool",
		Files: []AuthFileSpec{
			{Tool: "testtool", Path: optionalPath, Required: false},
			{Tool: "testtool", Path: requiredPath, Required: true},
		},
	}
	if err := v.Restore(fileSet, "preflight-directory-source"); err == nil {
		t.Fatal("Restore() succeeded with directory managed source")
	}
	if _, err := os.Stat(optionalPath); !os.IsNotExist(err) {
		t.Fatalf("preflight should fail before restoring earlier optional auth, stat err=%v", err)
	}
}

func TestVaultRestoreManagedDuplicateOriginalPathFailsClosed(t *testing.T) {
	tmpDir := t.TempDir()
	vaultDir := filepath.Join(tmpDir, "vault")
	authDir := filepath.Join(tmpDir, "auth")
	if err := os.MkdirAll(authDir, 0700); err != nil {
		t.Fatal(err)
	}

	authPath := filepath.Join(authDir, "auth.json")
	v := NewVault(vaultDir)
	targetDir := v.ProfilePath("testtool", "duplicate-original")
	if err := os.MkdirAll(targetDir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(targetDir, "a.json"), []byte(`{"token":"a"}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(targetDir, "b.json"), []byte(`{"token":"b"}`), 0600); err != nil {
		t.Fatal(err)
	}
	meta := vaultProfileMeta{
		Tool:            "testtool",
		Profile:         "duplicate-original",
		BackedUpAt:      time.Now().Format(time.RFC3339),
		Files:           2,
		ManifestVersion: 1,
		ManagedFiles: []managedFileState{
			{VaultName: "a.json", OriginalPath: authPath, Required: true, Present: true},
			{VaultName: "b.json", OriginalPath: authPath, Required: true, Present: true},
		},
	}
	raw, err := json.Marshal(meta)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(targetDir, "meta.json"), raw, 0600); err != nil {
		t.Fatal(err)
	}

	fileSet := AuthFileSet{
		Tool:  "testtool",
		Files: []AuthFileSpec{{Tool: "testtool", Path: authPath, Required: true}},
	}
	if err := v.Restore(fileSet, "duplicate-original"); err == nil {
		t.Fatal("Restore() succeeded with duplicate managed original paths")
	}
	if _, err := os.Stat(authPath); !os.IsNotExist(err) {
		t.Fatalf("duplicate original paths should fail before restoring live auth, stat err=%v", err)
	}
}

func TestVaultRestoreManagedRejectsPathfulVaultName(t *testing.T) {
	tmpDir := t.TempDir()
	vaultDir := filepath.Join(tmpDir, "vault")
	authDir := filepath.Join(tmpDir, "auth")
	if err := os.MkdirAll(authDir, 0700); err != nil {
		t.Fatal(err)
	}

	authPath := filepath.Join(authDir, "auth.json")
	v := NewVault(vaultDir)
	for _, vaultName := range []string{"subdir/auth.json", "../auth.json", "/abs/auth.json"} {
		profile := "bad-vault-name-" + strings.NewReplacer("/", "-", ".", "dot").Replace(vaultName)
		targetDir := v.ProfilePath("testtool", profile)
		if err := os.MkdirAll(targetDir, 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(targetDir, "auth.json"), []byte(`{"token":"target"}`), 0600); err != nil {
			t.Fatal(err)
		}
		meta := vaultProfileMeta{
			Tool:            "testtool",
			Profile:         profile,
			BackedUpAt:      time.Now().Format(time.RFC3339),
			Files:           1,
			ManifestVersion: 1,
			ManagedFiles: []managedFileState{
				{VaultName: vaultName, OriginalPath: authPath, Required: true, Present: true},
			},
		}
		raw, err := json.Marshal(meta)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(targetDir, "meta.json"), raw, 0600); err != nil {
			t.Fatal(err)
		}

		fileSet := AuthFileSet{
			Tool:  "testtool",
			Files: []AuthFileSpec{{Tool: "testtool", Path: authPath, Required: true}},
		}
		if err := v.Restore(fileSet, profile); err == nil {
			t.Fatalf("Restore() succeeded with pathful vault name %q", vaultName)
		}
		if _, err := os.Stat(authPath); !os.IsNotExist(err) {
			t.Fatalf("pathful vault name should fail before restoring live auth, stat err=%v", err)
		}
	}
}

func TestVaultRestoreLegacyProfileMissingOptionalDoesNotQuarantineLiveFile(t *testing.T) {
	tmpDir := t.TempDir()
	vaultDir := filepath.Join(tmpDir, "vault")
	authDir := filepath.Join(tmpDir, "auth")
	profileDir := filepath.Join(vaultDir, "testtool", "legacy")
	if err := os.MkdirAll(profileDir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(authDir, 0700); err != nil {
		t.Fatal(err)
	}

	requiredPath := filepath.Join(authDir, "required.json")
	optionalPath := filepath.Join(authDir, "optional.json")
	if err := os.WriteFile(filepath.Join(profileDir, "required.json"), []byte(`{"required":"legacy"}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(profileDir, "meta.json"), []byte(`{"tool":"testtool","profile":"legacy","files":1}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(optionalPath, []byte(`{"optional":"live"}`), 0600); err != nil {
		t.Fatal(err)
	}

	v := NewVault(vaultDir)
	fileSet := AuthFileSet{
		Tool: "testtool",
		Files: []AuthFileSpec{
			{Tool: "testtool", Path: requiredPath, Required: true},
			{Tool: "testtool", Path: optionalPath, Required: false},
		},
	}
	if err := v.Restore(fileSet, "legacy"); err != nil {
		t.Fatalf("Restore(legacy): %v", err)
	}
	data, err := os.ReadFile(optionalPath)
	if err != nil {
		t.Fatalf("legacy restore should leave live optional in place: %v", err)
	}
	if string(data) != `{"optional":"live"}` {
		t.Fatalf("live optional changed: %q", data)
	}
}

func TestVaultRestoreCorruptOtherProfileIsNotCleanupProof(t *testing.T) {
	tmpDir := t.TempDir()
	vaultDir := filepath.Join(tmpDir, "vault")
	authDir := filepath.Join(tmpDir, "auth")
	if err := os.MkdirAll(authDir, 0700); err != nil {
		t.Fatal(err)
	}

	requiredPath := filepath.Join(authDir, "required.json")
	optionalPath := filepath.Join(authDir, "optional.json")
	v := NewVault(vaultDir)
	fileSet := AuthFileSet{
		Tool: "testtool",
		Files: []AuthFileSpec{
			{Tool: "testtool", Path: requiredPath, Required: true},
			{Tool: "testtool", Path: optionalPath, Required: false},
		},
		AllowOptionalOnly: true,
	}

	corruptDir := v.ProfilePath("testtool", "corrupt-proof")
	if err := os.MkdirAll(corruptDir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(corruptDir, "required.json"), []byte(`{"required":"live"}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(corruptDir, "meta.json"), []byte(`{`), 0600); err != nil {
		t.Fatal(err)
	}

	targetDir := v.ProfilePath("testtool", "optional-only")
	if err := os.MkdirAll(targetDir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(targetDir, "optional.json"), []byte(`{"optional":"target"}`), 0600); err != nil {
		t.Fatal(err)
	}
	meta := vaultProfileMeta{
		Tool:            "testtool",
		Profile:         "optional-only",
		BackedUpAt:      time.Now().Format(time.RFC3339),
		Files:           1,
		ManifestVersion: 1,
		ManagedFiles: []managedFileState{
			{VaultName: "required.json", OriginalPath: requiredPath, Required: true, Present: false},
			{VaultName: "optional.json", OriginalPath: optionalPath, Required: false, Present: true},
		},
	}
	raw, err := json.Marshal(meta)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(targetDir, "meta.json"), raw, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(requiredPath, []byte(`{"required":"live"}`), 0600); err != nil {
		t.Fatal(err)
	}

	if err := v.Restore(fileSet, "optional-only"); err != nil {
		t.Fatalf("Restore(optional-only): %v", err)
	}
	data, err := os.ReadFile(requiredPath)
	if err != nil {
		t.Fatalf("corrupt unrelated proof profile should not authorize cleanup: %v", err)
	}
	if string(data) != `{"required":"live"}` {
		t.Fatalf("required auth changed: %q", data)
	}
}

func TestVaultRestoreDoesNotQuarantineSharedProviderOptional(t *testing.T) {
	tmpDir := t.TempDir()
	geminiHome := filepath.Join(tmpDir, "gemini-home")
	t.Setenv("HOME", filepath.Join(tmpDir, "home"))
	t.Setenv("GEMINI_HOME", geminiHome)
	if err := os.MkdirAll(geminiHome, 0700); err != nil {
		t.Fatal(err)
	}

	settingsPath := filepath.Join(geminiHome, "settings.json")
	sharedPath := filepath.Join(geminiHome, "oauth_creds.json")
	if err := os.WriteFile(settingsPath, []byte(`{"active":"gemini"}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(sharedPath, []byte(`{"shared":"belongs-to-agy-too"}`), 0600); err != nil {
		t.Fatal(err)
	}

	v := NewVault(filepath.Join(tmpDir, "vault"))
	fs := GeminiAuthFiles()
	if err := v.Backup(fs, "with-shared"); err != nil {
		t.Fatalf("Backup(with-shared): %v", err)
	}

	if err := os.Rename(sharedPath, sharedPath+".away"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(settingsPath, []byte(`{"active":"settings-only"}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := v.Backup(fs, "settings-only"); err != nil {
		t.Fatalf("Backup(settings-only): %v", err)
	}

	if err := os.WriteFile(sharedPath, []byte(`{"shared":"belongs-to-agy-too"}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := v.Restore(fs, "settings-only"); err != nil {
		t.Fatalf("Restore(settings-only): %v", err)
	}
	data, err := os.ReadFile(sharedPath)
	if err != nil {
		t.Fatalf("shared optional should remain live: %v", err)
	}
	if string(data) != `{"shared":"belongs-to-agy-too"}` {
		t.Fatalf("shared optional changed: %q", data)
	}
}

func readSingleQuarantinedFile(t *testing.T, profileDir, base string) []byte {
	t.Helper()
	var matches []string
	err := filepath.WalkDir(filepath.Join(profileDir, ".caam-quarantine"), func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		if strings.HasSuffix(filepath.Base(path), "-"+base) {
			matches = append(matches, path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 1 {
		t.Fatalf("quarantine matches for %s = %v, want exactly one", base, matches)
	}
	data, err := os.ReadFile(matches[0])
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestMigrateGeminiVaultDir(t *testing.T) {
	t.Run("renames old file to new", func(t *testing.T) {
		dir := t.TempDir()
		oldPath := filepath.Join(dir, "oauth_credentials.json")
		newPath := filepath.Join(dir, "oauth_creds.json")
		if err := os.WriteFile(oldPath, []byte(`{"client_id":"x"}`), 0600); err != nil {
			t.Fatal(err)
		}
		if err := MigrateGeminiVaultDir(dir); err != nil {
			t.Fatalf("MigrateGeminiVaultDir() error = %v", err)
		}
		if _, err := os.Stat(newPath); err != nil {
			t.Errorf("new file should exist: %v", err)
		}
		if _, err := os.Stat(oldPath); !os.IsNotExist(err) {
			t.Errorf("old file should not exist after rename")
		}
	})

	t.Run("no-op when new file already exists", func(t *testing.T) {
		dir := t.TempDir()
		oldPath := filepath.Join(dir, "oauth_credentials.json")
		newPath := filepath.Join(dir, "oauth_creds.json")
		if err := os.WriteFile(oldPath, []byte(`old`), 0600); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(newPath, []byte(`new`), 0600); err != nil {
			t.Fatal(err)
		}
		if err := MigrateGeminiVaultDir(dir); err != nil {
			t.Fatalf("MigrateGeminiVaultDir() error = %v", err)
		}
		data, _ := os.ReadFile(newPath)
		if string(data) != "new" {
			t.Errorf("new file should be unchanged, got %q", data)
		}
	})

	t.Run("no-op when no old file", func(t *testing.T) {
		dir := t.TempDir()
		if err := MigrateGeminiVaultDir(dir); err != nil {
			t.Fatalf("MigrateGeminiVaultDir() error = %v", err)
		}
	})

	t.Run("idempotent", func(t *testing.T) {
		dir := t.TempDir()
		oldPath := filepath.Join(dir, "oauth_credentials.json")
		newPath := filepath.Join(dir, "oauth_creds.json")
		if err := os.WriteFile(oldPath, []byte(`{"client_id":"x"}`), 0600); err != nil {
			t.Fatal(err)
		}
		// Run twice
		if err := MigrateGeminiVaultDir(dir); err != nil {
			t.Fatal(err)
		}
		if err := MigrateGeminiVaultDir(dir); err != nil {
			t.Fatal(err)
		}
		data, _ := os.ReadFile(newPath)
		if string(data) != `{"client_id":"x"}` {
			t.Errorf("content should survive double migration, got %q", data)
		}
	})
}

func TestVaultRestore_MigratesGeminiFilename(t *testing.T) {
	tmpDir := t.TempDir()
	vaultDir := filepath.Join(tmpDir, "vault")
	authDir := filepath.Join(tmpDir, "auth")

	// Create vault profile with OLD filename
	profileDir := filepath.Join(vaultDir, "gemini", "testprofile")
	if err := os.MkdirAll(profileDir, 0700); err != nil {
		t.Fatal(err)
	}
	oldContent := []byte(`{"client_id":"test","client_secret":"s","refresh_token":"r"}`)
	if err := os.WriteFile(filepath.Join(profileDir, "oauth_credentials.json"), oldContent, 0600); err != nil {
		t.Fatal(err)
	}

	v := NewVault(vaultDir)
	authFile := filepath.Join(authDir, ".gemini", "oauth_creds.json")
	fileSet := AuthFileSet{
		Tool: "gemini",
		Files: []AuthFileSpec{
			{Tool: "gemini", Path: authFile, Required: false},
		},
		AllowOptionalOnly: true,
	}

	if err := v.Restore(fileSet, "testprofile"); err != nil {
		t.Fatalf("Restore() error = %v", err)
	}

	// Verify the file was restored to the NEW name location
	restored, err := os.ReadFile(authFile)
	if err != nil {
		t.Fatalf("restored file should exist at new path: %v", err)
	}
	if string(restored) != string(oldContent) {
		t.Errorf("restored content = %q, want %q", restored, oldContent)
	}
}

func TestVaultList(t *testing.T) {
	t.Run("empty vault returns empty list", func(t *testing.T) {
		tmpDir := t.TempDir()
		v := NewVault(tmpDir)

		profiles, err := v.List("testtool")
		if err != nil {
			t.Fatalf("List() error = %v", err)
		}
		if len(profiles) != 0 {
			t.Errorf("List() = %v, want empty", profiles)
		}
	})

	t.Run("returns profiles", func(t *testing.T) {
		tmpDir := t.TempDir()

		// Create profile directories
		profiles := []string{"profile1", "profile2", "profile3"}
		for _, p := range profiles {
			if err := os.MkdirAll(filepath.Join(tmpDir, "testtool", p), 0700); err != nil {
				t.Fatal(err)
			}
		}

		v := NewVault(tmpDir)
		result, err := v.List("testtool")
		if err != nil {
			t.Fatalf("List() error = %v", err)
		}

		if len(result) != len(profiles) {
			t.Errorf("List() returned %d profiles, want %d", len(result), len(profiles))
		}
	})

	t.Run("ignores files in tool directory", func(t *testing.T) {
		tmpDir := t.TempDir()

		// Create profile dir and a file (not a dir)
		if err := os.MkdirAll(filepath.Join(tmpDir, "testtool", "profile1"), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(tmpDir, "testtool", "somefile.txt"), []byte(""), 0600); err != nil {
			t.Fatal(err)
		}

		v := NewVault(tmpDir)
		result, err := v.List("testtool")
		if err != nil {
			t.Fatalf("List() error = %v", err)
		}

		if len(result) != 1 {
			t.Errorf("List() returned %d profiles, want 1", len(result))
		}
	})
}

func TestVaultListAll(t *testing.T) {
	t.Run("empty vault returns empty map", func(t *testing.T) {
		tmpDir := t.TempDir()
		v := NewVault(tmpDir)

		result, err := v.ListAll()
		if err != nil {
			t.Fatalf("ListAll() error = %v", err)
		}
		if len(result) != 0 {
			t.Errorf("ListAll() = %v, want empty", result)
		}
	})

	t.Run("returns all tools and profiles", func(t *testing.T) {
		tmpDir := t.TempDir()

		// Create profiles for multiple tools
		tools := map[string][]string{
			"claude": {"work", "personal"},
			"codex":  {"main"},
			"gemini": {"account1", "account2", "account3"},
		}

		for tool, profiles := range tools {
			for _, p := range profiles {
				if err := os.MkdirAll(filepath.Join(tmpDir, tool, p), 0700); err != nil {
					t.Fatal(err)
				}
			}
		}

		v := NewVault(tmpDir)
		result, err := v.ListAll()
		if err != nil {
			t.Fatalf("ListAll() error = %v", err)
		}

		if len(result) != len(tools) {
			t.Errorf("ListAll() returned %d tools, want %d", len(result), len(tools))
		}

		for tool, expectedProfiles := range tools {
			gotProfiles, ok := result[tool]
			if !ok {
				t.Errorf("tool %q not found in result", tool)
				continue
			}
			if len(gotProfiles) != len(expectedProfiles) {
				t.Errorf("tool %q: got %d profiles, want %d", tool, len(gotProfiles), len(expectedProfiles))
			}
		}
	})
}

func TestVaultBackup_SystemProfilesAreImmutable(t *testing.T) {
	tmpDir := t.TempDir()
	vaultDir := filepath.Join(tmpDir, "vault")
	authDir := filepath.Join(tmpDir, "auth")

	if err := os.MkdirAll(authDir, 0700); err != nil {
		t.Fatal(err)
	}
	authFile := filepath.Join(authDir, "auth.json")
	if err := os.WriteFile(authFile, []byte(`{"token":"v1"}`), 0600); err != nil {
		t.Fatal(err)
	}

	v := NewVault(vaultDir)
	fileSet := AuthFileSet{
		Tool: "testtool",
		Files: []AuthFileSpec{
			{Tool: "testtool", Path: authFile, Required: true},
		},
	}

	if err := v.Backup(fileSet, "_system"); err != nil {
		t.Fatalf("Backup() error = %v", err)
	}

	// Change source and ensure overwrite is refused.
	if err := os.WriteFile(authFile, []byte(`{"token":"v2"}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := v.Backup(fileSet, "_system"); err == nil {
		t.Fatal("Backup() should refuse overwriting system profiles")
	}
}

func TestVaultBackupOriginal(t *testing.T) {
	t.Run("creates _original when current auth is not backed up", func(t *testing.T) {
		tmpDir := t.TempDir()
		vaultDir := filepath.Join(tmpDir, "vault")
		authDir := filepath.Join(tmpDir, "auth")

		if err := os.MkdirAll(authDir, 0700); err != nil {
			t.Fatal(err)
		}
		authFile := filepath.Join(authDir, "auth.json")
		original := []byte(`{"token":"original"}`)
		if err := os.WriteFile(authFile, original, 0600); err != nil {
			t.Fatal(err)
		}

		v := NewVault(vaultDir)
		fileSet := AuthFileSet{
			Tool: "testtool",
			Files: []AuthFileSpec{
				{Tool: "testtool", Path: authFile, Required: true},
			},
		}

		did, err := v.BackupOriginal(fileSet)
		if err != nil {
			t.Fatalf("BackupOriginal() error = %v", err)
		}
		if !did {
			t.Fatal("BackupOriginal() did = false, want true")
		}

		backupPath := v.BackupPath("testtool", "_original", "auth.json")
		got, err := os.ReadFile(backupPath)
		if err != nil {
			t.Fatalf("ReadFile() error = %v", err)
		}
		if string(got) != string(original) {
			t.Fatalf("backup content mismatch: got %q want %q", got, original)
		}

		// Idempotent: second call is a no-op.
		did, err = v.BackupOriginal(fileSet)
		if err != nil {
			t.Fatalf("BackupOriginal() second call error = %v", err)
		}
		if did {
			t.Fatal("BackupOriginal() second call did = true, want false")
		}
	})

	t.Run("skips when current auth already matches a vault profile", func(t *testing.T) {
		tmpDir := t.TempDir()
		vaultDir := filepath.Join(tmpDir, "vault")
		authDir := filepath.Join(tmpDir, "auth")

		if err := os.MkdirAll(authDir, 0700); err != nil {
			t.Fatal(err)
		}
		authFile := filepath.Join(authDir, "auth.json")
		content := []byte(`{"token":"already-backed-up"}`)
		if err := os.WriteFile(authFile, content, 0600); err != nil {
			t.Fatal(err)
		}

		v := NewVault(vaultDir)
		fileSet := AuthFileSet{
			Tool: "testtool",
			Files: []AuthFileSpec{
				{Tool: "testtool", Path: authFile, Required: true},
			},
		}

		if err := v.Backup(fileSet, "work"); err != nil {
			t.Fatalf("Backup() error = %v", err)
		}

		did, err := v.BackupOriginal(fileSet)
		if err != nil {
			t.Fatalf("BackupOriginal() error = %v", err)
		}
		if did {
			t.Fatal("BackupOriginal() did = true, want false")
		}

		if _, err := os.Stat(v.ProfilePath("testtool", "_original")); !os.IsNotExist(err) {
			t.Fatalf("_original should not be created; stat err=%v", err)
		}
	})
}

func TestVaultDelete(t *testing.T) {
	t.Run("deletes profile directory", func(t *testing.T) {
		tmpDir := t.TempDir()

		// Create profile with files
		profileDir := filepath.Join(tmpDir, "testtool", "profile1")
		if err := os.MkdirAll(profileDir, 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(profileDir, "auth.json"), []byte("{}"), 0600); err != nil {
			t.Fatal(err)
		}

		v := NewVault(tmpDir)
		if err := v.Delete("testtool", "profile1"); err != nil {
			t.Fatalf("Delete() error = %v", err)
		}

		// Verify deletion
		if _, err := os.Stat(profileDir); !os.IsNotExist(err) {
			t.Error("profile directory should be deleted")
		}
	})

	t.Run("refuses to delete system profiles without force", func(t *testing.T) {
		tmpDir := t.TempDir()

		profileDir := filepath.Join(tmpDir, "testtool", "_system")
		if err := os.MkdirAll(profileDir, 0700); err != nil {
			t.Fatal(err)
		}

		v := NewVault(tmpDir)
		if err := v.Delete("testtool", "_system"); err == nil {
			t.Fatal("Delete() should fail for system profiles")
		}

		// Verify still exists.
		if _, err := os.Stat(profileDir); err != nil {
			t.Fatalf("profile directory should still exist: %v", err)
		}

		if err := v.DeleteForce("testtool", "_system"); err != nil {
			t.Fatalf("DeleteForce() error = %v", err)
		}
		if _, err := os.Stat(profileDir); !os.IsNotExist(err) {
			t.Error("profile directory should be deleted")
		}
	})

	t.Run("deleting nonexistent profile is noop", func(t *testing.T) {
		tmpDir := t.TempDir()
		v := NewVault(tmpDir)

		// Should not error
		if err := v.Delete("testtool", "nonexistent"); err != nil {
			t.Fatalf("Delete() error = %v", err)
		}
	})

	t.Run("rejects invalid segments", func(t *testing.T) {
		tmpDir := t.TempDir()
		v := NewVault(tmpDir)

		if err := v.Delete("/", "profile"); err == nil {
			t.Fatal("Delete() should fail for invalid tool segment")
		}
		if err := v.Delete("testtool", "/"); err == nil {
			t.Fatal("Delete() should fail for invalid profile segment")
		}
	})
}

func TestVaultActiveProfile(t *testing.T) {
	t.Run("returns matching profile", func(t *testing.T) {
		tmpDir := t.TempDir()
		vaultDir := filepath.Join(tmpDir, "vault")
		authDir := filepath.Join(tmpDir, "auth")

		// Create auth file with specific content
		if err := os.MkdirAll(authDir, 0700); err != nil {
			t.Fatal(err)
		}
		authFile := filepath.Join(authDir, "auth.json")
		content := []byte(`{"token": "match-this-content"}`)
		if err := os.WriteFile(authFile, content, 0600); err != nil {
			t.Fatal(err)
		}

		// Create matching profile in vault
		profileDir := filepath.Join(vaultDir, "testtool", "myprofile")
		if err := os.MkdirAll(profileDir, 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(profileDir, "auth.json"), content, 0600); err != nil {
			t.Fatal(err)
		}

		v := NewVault(vaultDir)
		fileSet := AuthFileSet{
			Tool: "testtool",
			Files: []AuthFileSpec{
				{Tool: "testtool", Path: authFile, Required: true},
			},
		}

		profile, err := v.ActiveProfile(fileSet)
		if err != nil {
			t.Fatalf("ActiveProfile() error = %v", err)
		}
		if profile != "myprofile" {
			t.Errorf("ActiveProfile() = %q, want %q", profile, "myprofile")
		}
	})

	t.Run("claude credentials match after access token rotation", func(t *testing.T) {
		tmpDir := t.TempDir()
		vaultDir := filepath.Join(tmpDir, "vault")
		authDir := filepath.Join(tmpDir, "auth")

		if err := os.MkdirAll(authDir, 0700); err != nil {
			t.Fatal(err)
		}
		authFile := filepath.Join(authDir, ".credentials.json")
		current := []byte(`{"claudeAiOauth":{"accessToken":"live-new","refreshToken":"stable-refresh","expiresAt":1783119600000}}`)
		if err := os.WriteFile(authFile, current, 0600); err != nil {
			t.Fatal(err)
		}

		profileDir := filepath.Join(vaultDir, "claude", "alice")
		if err := os.MkdirAll(profileDir, 0700); err != nil {
			t.Fatal(err)
		}
		vaultCopy := []byte(`{"claudeAiOauth":{"accessToken":"vault-old","refreshToken":"stable-refresh","expiresAt":1783112400000}}`)
		if err := os.WriteFile(filepath.Join(profileDir, ".credentials.json"), vaultCopy, 0600); err != nil {
			t.Fatal(err)
		}

		v := NewVault(vaultDir)
		fileSet := AuthFileSet{
			Tool: "claude",
			Files: []AuthFileSpec{
				{Tool: "claude", Path: authFile, Required: true},
			},
		}

		profile, err := v.ActiveProfile(fileSet)
		if err != nil {
			t.Fatalf("ActiveProfile() error = %v", err)
		}
		if profile != "alice" {
			t.Errorf("ActiveProfile() = %q, want %q", profile, "alice")
		}
	})

	t.Run("prefers copied profile over source when both match", func(t *testing.T) {
		tmpDir := t.TempDir()
		vaultDir := filepath.Join(tmpDir, "vault")
		authDir := filepath.Join(tmpDir, "auth")

		if err := os.MkdirAll(authDir, 0700); err != nil {
			t.Fatal(err)
		}
		authFile := filepath.Join(authDir, "auth.json")
		content := []byte(`{"token": "same-account"}`)
		if err := os.WriteFile(authFile, content, 0600); err != nil {
			t.Fatal(err)
		}

		v := NewVault(vaultDir)
		srcDir := v.ProfilePath("testtool", "auto-20260121-143022")
		if err := os.MkdirAll(srcDir, 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(srcDir, "auth.json"), content, 0600); err != nil {
			t.Fatal(err)
		}
		meta := []byte(`{"tool":"testtool","profile":"auto-20260121-143022"}`)
		if err := os.WriteFile(filepath.Join(srcDir, "meta.json"), meta, 0600); err != nil {
			t.Fatal(err)
		}
		if err := v.CopyProfile("testtool", "auto-20260121-143022", "work"); err != nil {
			t.Fatalf("CopyProfile() error = %v", err)
		}

		fileSet := AuthFileSet{
			Tool: "testtool",
			Files: []AuthFileSpec{
				{Tool: "testtool", Path: authFile, Required: true},
			},
		}

		profile, err := v.ActiveProfile(fileSet)
		if err != nil {
			t.Fatalf("ActiveProfile() error = %v", err)
		}
		if profile != "work" {
			t.Errorf("ActiveProfile() = %q, want %q", profile, "work")
		}
	})

	t.Run("claude credentials match legacy vault without refresh token", func(t *testing.T) {
		tmpDir := t.TempDir()
		vaultDir := filepath.Join(tmpDir, "vault")
		authDir := filepath.Join(tmpDir, "auth")

		if err := os.MkdirAll(authDir, 0700); err != nil {
			t.Fatal(err)
		}
		authFile := filepath.Join(authDir, ".credentials.json")
		current := []byte(`{"claudeAiOauth":{"accessToken":"same-access","refreshToken":"current-refresh","expiresAt":1783119600000}}`)
		if err := os.WriteFile(authFile, current, 0600); err != nil {
			t.Fatal(err)
		}

		profileDir := filepath.Join(vaultDir, "claude", "legacy")
		if err := os.MkdirAll(profileDir, 0700); err != nil {
			t.Fatal(err)
		}
		vaultCopy := []byte(`{"claudeAiOauth":{"accessToken":"same-access","expiresAt":1783112400000}}`)
		if err := os.WriteFile(filepath.Join(profileDir, ".credentials.json"), vaultCopy, 0600); err != nil {
			t.Fatal(err)
		}

		v := NewVault(vaultDir)
		fileSet := AuthFileSet{
			Tool: "claude",
			Files: []AuthFileSpec{
				{Tool: "claude", Path: authFile, Required: true},
			},
		}

		profile, err := v.ActiveProfile(fileSet)
		if err != nil {
			t.Fatalf("ActiveProfile() error = %v", err)
		}
		if profile != "legacy" {
			t.Errorf("ActiveProfile() = %q, want %q", profile, "legacy")
		}
	})

	t.Run("ignores optional file differences when required files present", func(t *testing.T) {
		tmpDir := t.TempDir()
		vaultDir := filepath.Join(tmpDir, "vault")
		authDir := filepath.Join(tmpDir, "auth")

		if err := os.MkdirAll(authDir, 0700); err != nil {
			t.Fatal(err)
		}
		requiredPath := filepath.Join(authDir, "auth.json")
		optionalPath := filepath.Join(authDir, "settings.json")
		requiredContent := []byte(`{"token": "required-match"}`)
		if err := os.WriteFile(requiredPath, requiredContent, 0600); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(optionalPath, []byte(`{"session": "current-volatile"}`), 0600); err != nil {
			t.Fatal(err)
		}

		profileDir := filepath.Join(vaultDir, "testtool", "stable")
		if err := os.MkdirAll(profileDir, 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(profileDir, "auth.json"), requiredContent, 0600); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(profileDir, "settings.json"), []byte(`{"session": "backup-volatile"}`), 0600); err != nil {
			t.Fatal(err)
		}

		v := NewVault(vaultDir)
		fileSet := AuthFileSet{
			Tool: "testtool",
			Files: []AuthFileSpec{
				{Tool: "testtool", Path: requiredPath, Required: true},
				{Tool: "testtool", Path: optionalPath, Required: false},
			},
		}

		profile, err := v.ActiveProfile(fileSet)
		if err != nil {
			t.Fatalf("ActiveProfile() error = %v", err)
		}
		if profile != "stable" {
			t.Errorf("ActiveProfile() = %q, want %q", profile, "stable")
		}
	})

	t.Run("matches optional-only profiles when allowed", func(t *testing.T) {
		tmpDir := t.TempDir()
		vaultDir := filepath.Join(tmpDir, "vault")
		authDir := filepath.Join(tmpDir, "auth")

		if err := os.MkdirAll(authDir, 0700); err != nil {
			t.Fatal(err)
		}
		optionalPath := filepath.Join(authDir, "optional.json")
		optionalContent := []byte(`{"token": "optional-only"}`)
		if err := os.WriteFile(optionalPath, optionalContent, 0600); err != nil {
			t.Fatal(err)
		}

		profileDir := filepath.Join(vaultDir, "testtool", "optional")
		if err := os.MkdirAll(profileDir, 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(profileDir, "optional.json"), optionalContent, 0600); err != nil {
			t.Fatal(err)
		}

		v := NewVault(vaultDir)
		fileSet := AuthFileSet{
			Tool: "testtool",
			Files: []AuthFileSpec{
				{Tool: "testtool", Path: filepath.Join(authDir, "required.json"), Required: true},
				{Tool: "testtool", Path: optionalPath, Required: false},
			},
			AllowOptionalOnly: true,
		}

		profile, err := v.ActiveProfile(fileSet)
		if err != nil {
			t.Fatalf("ActiveProfile() error = %v", err)
		}
		if profile != "optional" {
			t.Errorf("ActiveProfile() = %q, want %q", profile, "optional")
		}
	})

	t.Run("returns empty for no matching profile", func(t *testing.T) {
		tmpDir := t.TempDir()
		vaultDir := filepath.Join(tmpDir, "vault")
		authDir := filepath.Join(tmpDir, "auth")

		// Create auth file
		if err := os.MkdirAll(authDir, 0700); err != nil {
			t.Fatal(err)
		}
		authFile := filepath.Join(authDir, "auth.json")
		if err := os.WriteFile(authFile, []byte(`{"token": "current"}`), 0600); err != nil {
			t.Fatal(err)
		}

		// Create non-matching profile
		profileDir := filepath.Join(vaultDir, "testtool", "other")
		if err := os.MkdirAll(profileDir, 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(profileDir, "auth.json"), []byte(`{"token": "different"}`), 0600); err != nil {
			t.Fatal(err)
		}

		v := NewVault(vaultDir)
		fileSet := AuthFileSet{
			Tool: "testtool",
			Files: []AuthFileSpec{
				{Tool: "testtool", Path: authFile, Required: true},
			},
		}

		profile, err := v.ActiveProfile(fileSet)
		if err != nil {
			t.Fatalf("ActiveProfile() error = %v", err)
		}
		if profile != "" {
			t.Errorf("ActiveProfile() = %q, want empty string", profile)
		}
	})

	t.Run("returns empty for no auth files", func(t *testing.T) {
		tmpDir := t.TempDir()
		vaultDir := filepath.Join(tmpDir, "vault")

		v := NewVault(vaultDir)
		fileSet := AuthFileSet{
			Tool: "testtool",
			Files: []AuthFileSpec{
				{Tool: "testtool", Path: "/nonexistent/auth.json", Required: true},
			},
		}

		profile, err := v.ActiveProfile(fileSet)
		if err != nil {
			t.Fatalf("ActiveProfile() error = %v", err)
		}
		if profile != "" {
			t.Errorf("ActiveProfile() = %q, want empty string", profile)
		}
	})
}

func TestHasAuthFiles(t *testing.T) {
	t.Run("returns true when required file exists", func(t *testing.T) {
		tmpDir := t.TempDir()
		authFile := filepath.Join(tmpDir, "auth.json")
		if err := os.WriteFile(authFile, []byte("{}"), 0600); err != nil {
			t.Fatal(err)
		}

		fileSet := AuthFileSet{
			Tool: "testtool",
			Files: []AuthFileSpec{
				{Tool: "testtool", Path: authFile, Required: true},
			},
		}

		if !HasAuthFiles(fileSet) {
			t.Error("HasAuthFiles() = false, want true")
		}
	})

	t.Run("returns false when required file missing", func(t *testing.T) {
		fileSet := AuthFileSet{
			Tool: "testtool",
			Files: []AuthFileSpec{
				{Tool: "testtool", Path: "/nonexistent/auth.json", Required: true},
			},
		}

		if HasAuthFiles(fileSet) {
			t.Error("HasAuthFiles() = true, want false")
		}
	})

	t.Run("ignores optional files", func(t *testing.T) {
		fileSet := AuthFileSet{
			Tool: "testtool",
			Files: []AuthFileSpec{
				{Tool: "testtool", Path: "/nonexistent/optional.json", Required: false},
			},
		}

		// No required files means no auth present
		if HasAuthFiles(fileSet) {
			t.Error("HasAuthFiles() = true, want false (no required files)")
		}
	})

	t.Run("accepts optional files when allowed", func(t *testing.T) {
		tmpDir := t.TempDir()
		optionalPath := filepath.Join(tmpDir, "optional.json")
		if err := os.WriteFile(optionalPath, []byte("{}"), 0600); err != nil {
			t.Fatal(err)
		}

		fileSet := AuthFileSet{
			Tool: "testtool",
			Files: []AuthFileSpec{
				{Tool: "testtool", Path: optionalPath, Required: false},
			},
			AllowOptionalOnly: true,
		}

		if !HasAuthFiles(fileSet) {
			t.Error("HasAuthFiles() = false, want true (optional files allowed)")
		}
	})
}

func TestClearAuthFiles(t *testing.T) {
	t.Run("removes existing files", func(t *testing.T) {
		tmpDir := t.TempDir()

		// Create auth files
		authFile1 := filepath.Join(tmpDir, "auth1.json")
		authFile2 := filepath.Join(tmpDir, "auth2.json")
		if err := os.WriteFile(authFile1, []byte("{}"), 0600); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(authFile2, []byte("{}"), 0600); err != nil {
			t.Fatal(err)
		}

		fileSet := AuthFileSet{
			Tool: "testtool",
			Files: []AuthFileSpec{
				{Tool: "testtool", Path: authFile1, Required: true},
				{Tool: "testtool", Path: authFile2, Required: false},
			},
		}

		if err := ClearAuthFiles(fileSet); err != nil {
			t.Fatalf("ClearAuthFiles() error = %v", err)
		}

		// Verify files removed
		if _, err := os.Stat(authFile1); !os.IsNotExist(err) {
			t.Error("authFile1 should be removed")
		}
		if _, err := os.Stat(authFile2); !os.IsNotExist(err) {
			t.Error("authFile2 should be removed")
		}
	})

	t.Run("handles nonexistent files gracefully", func(t *testing.T) {
		fileSet := AuthFileSet{
			Tool: "testtool",
			Files: []AuthFileSpec{
				{Tool: "testtool", Path: "/nonexistent/auth.json", Required: true},
			},
		}

		// Should not error
		if err := ClearAuthFiles(fileSet); err != nil {
			t.Fatalf("ClearAuthFiles() error = %v", err)
		}
	})
}

func TestCopyFile(t *testing.T) {
	t.Run("copies file content", func(t *testing.T) {
		tmpDir := t.TempDir()

		src := filepath.Join(tmpDir, "source.txt")
		dst := filepath.Join(tmpDir, "dest.txt")
		content := []byte("test content for copy")

		if err := os.WriteFile(src, content, 0600); err != nil {
			t.Fatal(err)
		}

		if err := copyFile(src, dst); err != nil {
			t.Fatalf("copyFile() error = %v", err)
		}

		got, err := os.ReadFile(dst)
		if err != nil {
			t.Fatalf("reading dst: %v", err)
		}
		if string(got) != string(content) {
			t.Errorf("copied content = %q, want %q", got, content)
		}
	})

	t.Run("creates parent directories", func(t *testing.T) {
		tmpDir := t.TempDir()

		src := filepath.Join(tmpDir, "source.txt")
		dst := filepath.Join(tmpDir, "nested", "deep", "dest.txt")

		if err := os.WriteFile(src, []byte("content"), 0600); err != nil {
			t.Fatal(err)
		}

		if err := copyFile(src, dst); err != nil {
			t.Fatalf("copyFile() error = %v", err)
		}

		if _, err := os.Stat(dst); err != nil {
			t.Errorf("destination file not created: %v", err)
		}
	})

	t.Run("sets secure permissions", func(t *testing.T) {
		tmpDir := t.TempDir()

		src := filepath.Join(tmpDir, "source.txt")
		dst := filepath.Join(tmpDir, "dest.txt")

		if err := os.WriteFile(src, []byte("secret"), 0600); err != nil {
			t.Fatal(err)
		}

		if err := copyFile(src, dst); err != nil {
			t.Fatalf("copyFile() error = %v", err)
		}

		info, err := os.Stat(dst)
		if err != nil {
			t.Fatal(err)
		}

		// Check permissions are 0600
		if info.Mode().Perm() != 0600 {
			t.Errorf("file permissions = %o, want 0600", info.Mode().Perm())
		}
	})
}

func TestHashFile(t *testing.T) {
	t.Run("returns correct SHA256 hash", func(t *testing.T) {
		tmpDir := t.TempDir()

		content := []byte("hash this content")
		file := filepath.Join(tmpDir, "test.txt")
		if err := os.WriteFile(file, content, 0600); err != nil {
			t.Fatal(err)
		}

		got, err := hashFile(file)
		if err != nil {
			t.Fatalf("hashFile() error = %v", err)
		}

		// Calculate expected hash
		h := sha256.Sum256(content)
		want := hex.EncodeToString(h[:])

		if got != want {
			t.Errorf("hashFile() = %q, want %q", got, want)
		}
	})

	t.Run("same content produces same hash", func(t *testing.T) {
		tmpDir := t.TempDir()

		content := []byte("identical content")
		file1 := filepath.Join(tmpDir, "file1.txt")
		file2 := filepath.Join(tmpDir, "file2.txt")

		if err := os.WriteFile(file1, content, 0600); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(file2, content, 0600); err != nil {
			t.Fatal(err)
		}

		hash1, err := hashFile(file1)
		if err != nil {
			t.Fatal(err)
		}
		hash2, err := hashFile(file2)
		if err != nil {
			t.Fatal(err)
		}

		if hash1 != hash2 {
			t.Errorf("identical files have different hashes: %q vs %q", hash1, hash2)
		}
	})

	t.Run("different content produces different hash", func(t *testing.T) {
		tmpDir := t.TempDir()

		file1 := filepath.Join(tmpDir, "file1.txt")
		file2 := filepath.Join(tmpDir, "file2.txt")

		if err := os.WriteFile(file1, []byte("content A"), 0600); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(file2, []byte("content B"), 0600); err != nil {
			t.Fatal(err)
		}

		hash1, err := hashFile(file1)
		if err != nil {
			t.Fatal(err)
		}
		hash2, err := hashFile(file2)
		if err != nil {
			t.Fatal(err)
		}

		if hash1 == hash2 {
			t.Error("different files should have different hashes")
		}
	})

	t.Run("error for nonexistent file", func(t *testing.T) {
		_, err := hashFile("/nonexistent/file.txt")
		if err == nil {
			t.Error("hashFile() should error for nonexistent file")
		}
	})
}

func TestBackupRestore_RoundTrip(t *testing.T) {
	tmpDir := t.TempDir()
	vaultDir := filepath.Join(tmpDir, "vault")
	authDir := filepath.Join(tmpDir, "auth")

	// Create original auth file
	if err := os.MkdirAll(authDir, 0700); err != nil {
		t.Fatal(err)
	}
	authFile := filepath.Join(authDir, "auth.json")
	originalContent := []byte(`{"token": "original-secret-token-12345"}`)
	if err := os.WriteFile(authFile, originalContent, 0600); err != nil {
		t.Fatal(err)
	}

	v := NewVault(vaultDir)
	fileSet := AuthFileSet{
		Tool: "testtool",
		Files: []AuthFileSpec{
			{Tool: "testtool", Path: authFile, Required: true},
		},
	}

	// Backup
	if err := v.Backup(fileSet, "roundtrip"); err != nil {
		t.Fatalf("Backup() error = %v", err)
	}

	// Modify original
	if err := os.WriteFile(authFile, []byte(`{"token": "modified"}`), 0600); err != nil {
		t.Fatal(err)
	}

	// Restore
	if err := v.Restore(fileSet, "roundtrip"); err != nil {
		t.Fatalf("Restore() error = %v", err)
	}

	// Verify original content restored
	restored, err := os.ReadFile(authFile)
	if err != nil {
		t.Fatal(err)
	}
	if string(restored) != string(originalContent) {
		t.Errorf("restored content = %q, want %q", restored, originalContent)
	}

	// Verify active profile detection
	profile, err := v.ActiveProfile(fileSet)
	if err != nil {
		t.Fatal(err)
	}
	if profile != "roundtrip" {
		t.Errorf("ActiveProfile() = %q, want %q", profile, "roundtrip")
	}
}

func TestVaultBackupCurrent(t *testing.T) {
	t.Run("creates timestamped backup", func(t *testing.T) {
		tmpDir := t.TempDir()
		vaultDir := filepath.Join(tmpDir, "vault")
		authDir := filepath.Join(tmpDir, "auth")

		if err := os.MkdirAll(authDir, 0700); err != nil {
			t.Fatal(err)
		}
		authFile := filepath.Join(authDir, "auth.json")
		content := []byte(`{"token":"current"}`)
		if err := os.WriteFile(authFile, content, 0600); err != nil {
			t.Fatal(err)
		}

		v := NewVault(vaultDir)
		fileSet := AuthFileSet{
			Tool: "testtool",
			Files: []AuthFileSpec{
				{Tool: "testtool", Path: authFile, Required: true},
			},
		}

		backupName, err := v.BackupCurrent(fileSet)
		if err != nil {
			t.Fatalf("BackupCurrent() error = %v", err)
		}

		// Check backup name format
		if backupName == "" {
			t.Fatal("BackupCurrent() returned empty name")
		}
		if len(backupName) < 8 || backupName[:8] != "_backup_" {
			t.Errorf("backup name %q doesn't start with _backup_", backupName)
		}

		// Verify backup content
		backupPath := v.BackupPath("testtool", backupName, "auth.json")
		got, err := os.ReadFile(backupPath)
		if err != nil {
			t.Fatalf("ReadFile() error = %v", err)
		}
		if string(got) != string(content) {
			t.Errorf("backup content = %q, want %q", got, content)
		}
	})

	t.Run("no-op when no auth files", func(t *testing.T) {
		tmpDir := t.TempDir()
		vaultDir := filepath.Join(tmpDir, "vault")

		v := NewVault(vaultDir)
		fileSet := AuthFileSet{
			Tool: "testtool",
			Files: []AuthFileSpec{
				{Tool: "testtool", Path: "/nonexistent/auth.json", Required: true},
			},
		}

		backupName, err := v.BackupCurrent(fileSet)
		if err != nil {
			t.Fatalf("BackupCurrent() error = %v", err)
		}
		if backupName != "" {
			t.Errorf("BackupCurrent() = %q, want empty string when no auth files", backupName)
		}
	})
}

func TestVaultRotateAutoBackups(t *testing.T) {
	t.Run("deletes oldest when over limit", func(t *testing.T) {
		tmpDir := t.TempDir()
		v := NewVault(tmpDir)

		// Create 5 backup profiles
		backups := []string{
			"_backup_20251201_100000",
			"_backup_20251202_100000",
			"_backup_20251203_100000",
			"_backup_20251204_100000",
			"_backup_20251205_100000",
		}
		for _, name := range backups {
			profileDir := v.ProfilePath("testtool", name)
			if err := os.MkdirAll(profileDir, 0700); err != nil {
				t.Fatal(err)
			}
		}

		// Rotate to keep only 3
		if err := v.RotateAutoBackups("testtool", 3); err != nil {
			t.Fatalf("RotateAutoBackups() error = %v", err)
		}

		// Check remaining profiles
		profiles, _ := v.List("testtool")
		if len(profiles) != 3 {
			t.Errorf("after rotation: %d profiles, want 3", len(profiles))
		}

		// Oldest 2 should be deleted
		for _, name := range backups[:2] {
			profileDir := v.ProfilePath("testtool", name)
			if _, err := os.Stat(profileDir); !os.IsNotExist(err) {
				t.Errorf("profile %s should have been deleted", name)
			}
		}

		// Newest 3 should remain
		for _, name := range backups[2:] {
			profileDir := v.ProfilePath("testtool", name)
			if _, err := os.Stat(profileDir); os.IsNotExist(err) {
				t.Errorf("profile %s should still exist", name)
			}
		}
	})

	t.Run("no-op when within limit", func(t *testing.T) {
		tmpDir := t.TempDir()
		v := NewVault(tmpDir)

		// Create 2 backup profiles
		backups := []string{"_backup_20251201_100000", "_backup_20251202_100000"}
		for _, name := range backups {
			profileDir := v.ProfilePath("testtool", name)
			if err := os.MkdirAll(profileDir, 0700); err != nil {
				t.Fatal(err)
			}
		}

		// Rotate with limit of 5 (more than we have)
		if err := v.RotateAutoBackups("testtool", 5); err != nil {
			t.Fatalf("RotateAutoBackups() error = %v", err)
		}

		// All should remain
		profiles, _ := v.List("testtool")
		if len(profiles) != 2 {
			t.Errorf("after rotation: %d profiles, want 2", len(profiles))
		}
	})

	t.Run("no-op when maxBackups is 0", func(t *testing.T) {
		tmpDir := t.TempDir()
		v := NewVault(tmpDir)

		// Create many backups
		for i := 0; i < 10; i++ {
			profileDir := v.ProfilePath("testtool", "_backup_2025120"+string(rune('0'+i))+"_100000")
			if err := os.MkdirAll(profileDir, 0700); err != nil {
				t.Fatal(err)
			}
		}

		// Rotate with 0 means unlimited
		if err := v.RotateAutoBackups("testtool", 0); err != nil {
			t.Fatalf("RotateAutoBackups() error = %v", err)
		}

		// All should remain (0 = unlimited)
		profiles, _ := v.List("testtool")
		if len(profiles) != 10 {
			t.Errorf("after rotation: %d profiles, want 10 (unlimited)", len(profiles))
		}
	})

	t.Run("only rotates _backup_ profiles", func(t *testing.T) {
		tmpDir := t.TempDir()
		v := NewVault(tmpDir)

		// Create mix of profiles
		profiles := []string{
			"_backup_20251201_100000",
			"_backup_20251202_100000",
			"_backup_20251203_100000",
			"_original",
			"work",
			"personal",
		}
		for _, name := range profiles {
			profileDir := v.ProfilePath("testtool", name)
			if err := os.MkdirAll(profileDir, 0700); err != nil {
				t.Fatal(err)
			}
		}

		// Rotate to keep only 1 backup
		if err := v.RotateAutoBackups("testtool", 1); err != nil {
			t.Fatalf("RotateAutoBackups() error = %v", err)
		}

		// Should have: 1 backup + _original + work + personal = 4
		remaining, _ := v.List("testtool")
		if len(remaining) != 4 {
			t.Errorf("after rotation: %d profiles, want 4", len(remaining))
		}

		// _original, work, personal should still exist
		for _, name := range []string{"_original", "work", "personal"} {
			profileDir := v.ProfilePath("testtool", name)
			if _, err := os.Stat(profileDir); os.IsNotExist(err) {
				t.Errorf("non-backup profile %s should still exist", name)
			}
		}
	})
}

func TestVaultCopyProfile(t *testing.T) {
	t.Run("successful copy", func(t *testing.T) {
		tmpDir := t.TempDir()
		v := NewVault(tmpDir)

		// Create source profile with files
		srcDir := v.ProfilePath("testtool", "source")
		if err := os.MkdirAll(srcDir, 0700); err != nil {
			t.Fatal(err)
		}

		authContent := []byte(`{"token": "secret123"}`)
		if err := os.WriteFile(filepath.Join(srcDir, "auth.json"), authContent, 0600); err != nil {
			t.Fatal(err)
		}

		metaContent := []byte(`{"tool": "testtool", "profile": "source"}`)
		if err := os.WriteFile(filepath.Join(srcDir, "meta.json"), metaContent, 0600); err != nil {
			t.Fatal(err)
		}

		// Copy to destination
		if err := v.CopyProfile("testtool", "source", "dest"); err != nil {
			t.Fatalf("CopyProfile() error = %v", err)
		}

		// Verify destination exists with same content
		destDir := v.ProfilePath("testtool", "dest")
		if _, err := os.Stat(destDir); os.IsNotExist(err) {
			t.Fatal("destination directory not created")
		}

		copiedAuth, err := os.ReadFile(filepath.Join(destDir, "auth.json"))
		if err != nil {
			t.Fatalf("reading dest auth: %v", err)
		}
		if string(copiedAuth) != string(authContent) {
			t.Errorf("auth content = %q, want %q", copiedAuth, authContent)
		}

		// Verify source still exists
		if _, err := os.Stat(srcDir); os.IsNotExist(err) {
			t.Error("source directory should still exist")
		}
	})

	t.Run("source not found fails", func(t *testing.T) {
		tmpDir := t.TempDir()
		v := NewVault(tmpDir)

		err := v.CopyProfile("testtool", "nonexistent", "dest")
		if err == nil {
			t.Fatal("CopyProfile() should fail for nonexistent source")
		}
	})

	t.Run("destination exists fails", func(t *testing.T) {
		tmpDir := t.TempDir()
		v := NewVault(tmpDir)

		// Create both source and destination
		srcDir := v.ProfilePath("testtool", "source")
		dstDir := v.ProfilePath("testtool", "dest")
		if err := os.MkdirAll(srcDir, 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(dstDir, 0700); err != nil {
			t.Fatal(err)
		}

		err := v.CopyProfile("testtool", "source", "dest")
		if err == nil {
			t.Fatal("CopyProfile() should fail when destination exists")
		}
	})

	t.Run("invalid source profile name fails", func(t *testing.T) {
		tmpDir := t.TempDir()
		v := NewVault(tmpDir)

		err := v.CopyProfile("testtool", "../escape", "dest")
		if err == nil {
			t.Fatal("CopyProfile() should fail for invalid source profile name")
		}
	})

	t.Run("invalid destination profile name fails", func(t *testing.T) {
		tmpDir := t.TempDir()
		v := NewVault(tmpDir)

		// Create source
		srcDir := v.ProfilePath("testtool", "source")
		if err := os.MkdirAll(srcDir, 0700); err != nil {
			t.Fatal(err)
		}

		err := v.CopyProfile("testtool", "source", "../escape")
		if err == nil {
			t.Fatal("CopyProfile() should fail for invalid destination profile name")
		}
	})

	t.Run("meta.json updated with new profile name", func(t *testing.T) {
		tmpDir := t.TempDir()
		v := NewVault(tmpDir)

		// Create source profile with meta.json
		srcDir := v.ProfilePath("testtool", "auto-20260121-143022")
		if err := os.MkdirAll(srcDir, 0700); err != nil {
			t.Fatal(err)
		}

		metaContent := []byte(`{"tool": "testtool", "profile": "auto-20260121-143022", "backed_up_at": "2026-01-21T14:30:22Z"}`)
		if err := os.WriteFile(filepath.Join(srcDir, "meta.json"), metaContent, 0600); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(srcDir, "auth.json"), []byte(`{}`), 0600); err != nil {
			t.Fatal(err)
		}

		// Copy to friendly name
		if err := v.CopyProfile("testtool", "auto-20260121-143022", "work"); err != nil {
			t.Fatalf("CopyProfile() error = %v", err)
		}

		// Read and verify meta.json
		destDir := v.ProfilePath("testtool", "work")
		metaPath := filepath.Join(destDir, "meta.json")
		metaData, err := os.ReadFile(metaPath)
		if err != nil {
			t.Fatalf("reading meta.json: %v", err)
		}

		var meta map[string]interface{}
		if err := json.Unmarshal(metaData, &meta); err != nil {
			t.Fatalf("parsing meta.json: %v", err)
		}

		if meta["profile"] != "work" {
			t.Errorf("meta.json profile = %v, want 'work'", meta["profile"])
		}
		if meta["copied_from"] != "auto-20260121-143022" {
			t.Errorf("meta.json copied_from = %v, want 'auto-20260121-143022'", meta["copied_from"])
		}
		if meta["copied_at"] == nil {
			t.Error("meta.json should have copied_at timestamp")
		}
	})

	t.Run("copies system profile to regular name", func(t *testing.T) {
		tmpDir := t.TempDir()
		v := NewVault(tmpDir)

		// Create system profile
		srcDir := v.ProfilePath("testtool", "_backup_20251217_143022")
		if err := os.MkdirAll(srcDir, 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(srcDir, "auth.json"), []byte(`{}`), 0600); err != nil {
			t.Fatal(err)
		}

		// Copy to regular name
		if err := v.CopyProfile("testtool", "_backup_20251217_143022", "restored"); err != nil {
			t.Fatalf("CopyProfile() error = %v", err)
		}

		// Both should exist
		if _, err := os.Stat(srcDir); os.IsNotExist(err) {
			t.Error("source system profile should still exist")
		}
		if _, err := os.Stat(v.ProfilePath("testtool", "restored")); os.IsNotExist(err) {
			t.Error("destination profile should exist")
		}
	})

	t.Run("malformed meta.json fails copy", func(t *testing.T) {
		tmpDir := t.TempDir()
		v := NewVault(tmpDir)

		srcDir := v.ProfilePath("testtool", "source")
		if err := os.MkdirAll(srcDir, 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(srcDir, "auth.json"), []byte(`{}`), 0600); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(srcDir, "meta.json"), []byte(`{`), 0600); err != nil {
			t.Fatal(err)
		}

		err := v.CopyProfile("testtool", "source", "dest")
		if err == nil {
			t.Fatal("CopyProfile() should fail for malformed copied meta.json")
		}
	})
}
