// Package authfile manages auth file backup/restore for instant account switching.
//
// The core insight: AI coding tools store OAuth tokens in specific files.
// Instead of logging in/out (slow, requires browser), we can:
//  1. Backup the auth file after logging in once
//  2. Label it with the account name
//  3. Restore it instantly when we need to switch
//
// This enables sub-second account switching for "all you can eat" subscriptions
// like GPT Pro, Claude Max, and Gemini Ultra when hitting usage limits.
package authfile

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"
)

// AuthFileSpec defines where a tool stores its auth credentials.
type AuthFileSpec struct {
	// Tool is the tool identifier (codex, claude, gemini).
	Tool string

	// Path is the absolute path to the auth file.
	Path string

	// Description is a human-readable description.
	Description string

	// Required indicates if this file must exist for auth to work.
	Required bool
}

// AuthFileSet is a collection of auth files that together represent
// a complete authentication state for a tool.
type AuthFileSet struct {
	Tool  string
	Files []AuthFileSpec
	// AllowOptionalOnly permits auth states that rely solely on optional files
	// (e.g., API key or helper-based auth that doesn't create OAuth artifacts).
	AllowOptionalOnly bool
}

// CodexAuthFiles returns the auth files for Codex CLI.
// Codex stores auth in $CODEX_HOME/auth.json (default ~/.codex/auth.json).
func CodexAuthFiles() AuthFileSet {
	home := os.Getenv("CODEX_HOME")
	if home == "" {
		homeDir, _ := os.UserHomeDir()
		home = filepath.Join(homeDir, ".codex")
	}

	return AuthFileSet{
		Tool: "codex",
		Files: []AuthFileSpec{
			{
				Tool:        "codex",
				Path:        filepath.Join(home, "auth.json"),
				Description: "Codex CLI OAuth token (GPT Pro subscription)",
				Required:    true,
			},
		},
	}
}

// ClaudeAuthFiles returns the auth files for Claude Code.
// Claude Code stores OAuth credentials in:
//   - ~/.claude/.credentials.json (primary - contains claudeAiOauth with tokens)
//   - ~/.claude.json (settings file - not auth, but backed up for completeness)
//   - ~/.config/claude-code/auth.json (auth credentials; or $CLAUDE_CONFIG_DIR/auth.json)
//   - ~/.claude/settings.json (user settings)
//   - ~/Library/Application Support/Claude/config.json (macOS: Claude Desktop's
//     encrypted OAuth token cache; only its oauth:tokenCache* fields are tracked)
func ClaudeAuthFiles() AuthFileSet {
	homeDir, _ := os.UserHomeDir()
	claudeConfigDir := os.Getenv("CLAUDE_CONFIG_DIR")
	if claudeConfigDir == "" {
		xdgConfig := os.Getenv("XDG_CONFIG_HOME")
		if xdgConfig == "" {
			xdgConfig = filepath.Join(homeDir, ".config")
		}
		claudeConfigDir = filepath.Join(xdgConfig, "claude-code")
	}

	return AuthFileSet{
		Tool: "claude",
		Files: []AuthFileSpec{
			{
				Tool:        "claude",
				Path:        filepath.Join(homeDir, ".claude", ".credentials.json"),
				Description: "Claude Code OAuth credentials (Claude Max subscription)",
				Required:    true,
			},
			{
				Tool:        "claude",
				Path:        filepath.Join(homeDir, ".claude.json"),
				Description: "Claude Code settings and session state",
				Required:    false, // This is a settings file, not strictly required for auth
			},
			{
				Tool:        "claude",
				Path:        filepath.Join(claudeConfigDir, "auth.json"),
				Description: "Claude Code auth credentials",
				Required:    false,
			},
			{
				Tool:        "claude",
				Path:        filepath.Join(homeDir, ".claude", "settings.json"),
				Description: "Claude Code user settings (apiKeyHelper / API key mode)",
				Required:    false,
			},
			{
				Tool:        "claude",
				Path:        claudeDesktopConfigPath(homeDir),
				Description: "Claude Desktop encrypted OAuth token cache (macOS)",
				Required:    false,
			},
		},
		AllowOptionalOnly: true,
	}
}

// claudeDesktopConfigPath is the macOS Claude Desktop config that holds the
// encrypted OAuth token cache recent Claude Code builds can rehydrate from.
func claudeDesktopConfigPath(homeDir string) string {
	return filepath.Join(homeDir, "Library", "Application Support", "Claude", "config.json")
}

// GeminiAuthFiles returns the auth files for Gemini CLI.
// Gemini CLI stores Google OAuth tokens in ~/.gemini/ directory.
func GeminiAuthFiles() AuthFileSet {
	homeDir, _ := os.UserHomeDir()

	// Check for GEMINI_HOME override
	geminiHome := os.Getenv("GEMINI_HOME")
	if geminiHome == "" {
		geminiHome = filepath.Join(homeDir, ".gemini")
	}

	return AuthFileSet{
		Tool: "gemini",
		Files: []AuthFileSpec{
			{
				Tool:        "gemini",
				Path:        filepath.Join(geminiHome, "settings.json"),
				Description: "Gemini CLI settings with Google OAuth state (Gemini Ultra subscription)",
				Required:    true,
			},
			// Additional auth files that may store tokens
			{
				Tool:        "gemini",
				Path:        filepath.Join(geminiHome, "oauth_creds.json"),
				Description: "Gemini CLI OAuth credentials cache",
				Required:    false,
			},
			{
				Tool:        "gemini",
				Path:        filepath.Join(geminiHome, ".env"),
				Description: "Gemini API key (.env file)",
				Required:    false,
			},
		},
		AllowOptionalOnly: true,
	}
}

// AntigravityAuthFiles returns the auth files for the Antigravity CLI (agy),
// Google's successor to the legacy Gemini CLI (gmi).
//
// agy is authenticated solely by an on-disk OAuth token at
// ~/.gemini/antigravity-cli/antigravity-oauth-token (this file alone is
// sufficient; it is NOT device-bound). The active Google account email is
// recorded in ~/.gemini/google_accounts.json, and the shared Google OAuth creds
// cache lives at ~/.gemini/oauth_creds.json. The antigravity-cli settings.json
// carries the default model.
//
// Keyring note: agy does NOT use the OS keyring (libsecret) on Linux — the token
// file is the authoritative credential, so caam backs up files only.
//
// Every basename here is unique, so files from the two directories
// (~/.gemini and ~/.gemini/antigravity-cli) never collide in the vault.
func AntigravityAuthFiles() AuthFileSet {
	homeDir, _ := os.UserHomeDir()

	geminiHome := os.Getenv("GEMINI_HOME")
	if geminiHome == "" {
		geminiHome = filepath.Join(homeDir, ".gemini")
	}
	antigravityHome := filepath.Join(geminiHome, "antigravity-cli")

	return AuthFileSet{
		Tool: "agy",
		Files: []AuthFileSpec{
			{
				Tool:        "agy",
				Path:        filepath.Join(antigravityHome, "antigravity-oauth-token"),
				Description: "Antigravity CLI OAuth token (authoritative agy credential)",
				Required:    true,
			},
			{
				Tool:        "agy",
				Path:        filepath.Join(geminiHome, "google_accounts.json"),
				Description: "Active Google account for Antigravity (google_accounts.json)",
				Required:    false,
			},
			{
				Tool:        "agy",
				Path:        filepath.Join(geminiHome, "oauth_creds.json"),
				Description: "Shared Google OAuth credentials cache (oauth_creds.json)",
				Required:    false,
			},
			{
				Tool:        "agy",
				Path:        filepath.Join(antigravityHome, "settings.json"),
				Description: "Antigravity CLI settings (default model / telemetry)",
				Required:    false,
			},
		},
		// The token file is required; AllowOptionalOnly is left false so a backup
		// without the token correctly fails (an account snapshot is meaningless
		// without the authoritative credential).
	}
}

// OpenCodeAuthFiles returns the auth files for OpenCode.
// OpenCode stores auth in $XDG_DATA_HOME/opencode/auth.json (default ~/.local/share/opencode/auth.json).
func OpenCodeAuthFiles() AuthFileSet {
	homeDir, _ := os.UserHomeDir()

	dataHome := os.Getenv("XDG_DATA_HOME")
	if dataHome == "" {
		dataHome = filepath.Join(homeDir, ".local", "share")
	}

	return AuthFileSet{
		Tool: "opencode",
		Files: []AuthFileSpec{
			{
				Tool:        "opencode",
				Path:        filepath.Join(dataHome, "opencode", "auth.json"),
				Description: "OpenCode auth credentials",
				Required:    true,
			},
		},
	}
}

// CursorAuthFiles returns the auth files for Cursor CLI.
// Cursor stores config in ~/.cursor/ directory.
func CursorAuthFiles() AuthFileSet {
	homeDir, _ := os.UserHomeDir()

	return AuthFileSet{
		Tool: "cursor",
		Files: []AuthFileSpec{
			{
				Tool:        "cursor",
				Path:        filepath.Join(homeDir, ".cursor", "cli-config.json"),
				Description: "Cursor CLI auth (authInfo)",
				Required:    false,
			},
			{
				Tool:        "cursor",
				Path:        filepath.Join(homeDir, ".cursor", "auth.json"),
				Description: "Cursor CLI auth credentials (legacy)",
				Required:    false,
			},
			{
				Tool:        "cursor",
				Path:        filepath.Join(homeDir, ".cursor", "settings.json"),
				Description: "Cursor CLI settings",
				Required:    false,
			},
		},
		AllowOptionalOnly: true,
	}
}

// GetAuthFileSet returns the AuthFileSet for the given provider name.
func GetAuthFileSet(provider string) (AuthFileSet, bool) {
	switch strings.ToLower(provider) {
	case "claude":
		return ClaudeAuthFiles(), true
	case "codex":
		return CodexAuthFiles(), true
	case "gemini":
		return GeminiAuthFiles(), true
	case "agy", "antigravity":
		return AntigravityAuthFiles(), true
	case "opencode", "oc":
		return OpenCodeAuthFiles(), true
	case "cursor", "cur":
		return CursorAuthFiles(), true
	default:
		return AuthFileSet{}, false
	}
}

// Vault manages stored auth file backups.
type Vault struct {
	basePath string // ~/.local/share/caam/vault
}

const originalProfileName = "_original"
const profileMetaFilename = "meta.json"
const quarantineDirName = ".caam-quarantine"
const profileBackupDirName = ".caam-profile-backups"
const backupCurrentRetries = 100

var timeNow = time.Now

type vaultProfileMeta struct {
	Tool            string             `json:"tool"`
	Profile         string             `json:"profile"`
	Description     string             `json:"description,omitempty"`
	BackedUpAt      string             `json:"backed_up_at"`
	Files           int                `json:"files"`
	ManifestVersion int                `json:"manifest_version,omitempty"`
	Type            string             `json:"type,omitempty"`
	CreatedBy       string             `json:"created_by,omitempty"`
	OriginalPaths   []string           `json:"original_paths,omitempty"`
	ManagedFiles    []managedFileState `json:"managed_files,omitempty"`
}

type managedFileState struct {
	VaultName               string `json:"vault_name"`
	OriginalPath            string `json:"original_path"`
	Required                bool   `json:"required"`
	Present                 bool   `json:"present"`
	ClaudeDesktopTokenCache bool   `json:"claude_desktop_token_cache,omitempty"`
}

type backupPlanEntry struct {
	spec                AuthFileSpec
	state               managedFileState
	claudeDesktopFields map[string]interface{}
}

type restoreCleanupEntry struct {
	spec  AuthFileSpec
	state managedFileState
}

// IsSystemProfile reports whether a profile name is reserved for system-managed
// profiles (created automatically by caam safety features).
//
// Convention: profile names starting with '_' are system profiles.
func IsSystemProfile(name string) bool {
	return strings.HasPrefix(strings.TrimSpace(name), "_")
}

var errProtectedSystemProfile = fmt.Errorf("protected system profile")

// NewVault creates a new vault at the given path.
func NewVault(basePath string) *Vault {
	return &Vault{basePath: basePath}
}

// BasePath returns the on-disk path to the vault root directory.
func (v *Vault) BasePath() string {
	return v.basePath
}

// DefaultVaultPath returns the default vault location.
// Falls back to current directory if home directory cannot be determined.
func DefaultVaultPath() string {
	if caamHome := os.Getenv("CAAM_HOME"); caamHome != "" {
		return filepath.Join(caamHome, "data", "vault")
	}
	if xdgData := os.Getenv("XDG_DATA_HOME"); xdgData != "" {
		return filepath.Join(xdgData, "caam", "vault")
	}
	homeDir, err := os.UserHomeDir()
	if err != nil {
		// Fallback to current directory - unusual but handles edge cases
		return filepath.Join(".local", "share", "caam", "vault")
	}
	return filepath.Join(homeDir, ".local", "share", "caam", "vault")
}

// ProfilePath returns the path to a profile's backup directory.
// Structure: vault/<tool>/<profile>/
func (v *Vault) ProfilePath(tool, profile string) string {
	return filepath.Join(v.basePath, tool, profile)
}

// BackupPath returns the path where a specific auth file is backed up.
// Structure: vault/<tool>/<profile>/<filename>
func (v *Vault) BackupPath(tool, profile, filename string) string {
	return filepath.Join(v.ProfilePath(tool, profile), filename)
}

// Backup saves the current auth files to the vault.
func (v *Vault) Backup(fileSet AuthFileSet, profile string) error {
	profileDir, err := v.safeProfileDir(fileSet.Tool, profile)
	if err != nil {
		return err
	}
	if err := validateUniqueVaultNames(fileSet); err != nil {
		return err
	}

	tool := strings.TrimSpace(fileSet.Tool)
	profile = strings.TrimSpace(profile)

	// System profiles are immutable safety artifacts; never overwrite them.
	if IsSystemProfile(profile) {
		st, err := os.Stat(profileDir)
		if err == nil {
			if st.IsDir() {
				return fmt.Errorf("%w: refusing to overwrite %s/%s", errProtectedSystemProfile, tool, profile)
			}
			return fmt.Errorf("profile path exists and is not a directory: %s", profileDir)
		}
		if err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("stat profile dir: %w", err)
		}
	}
	if st, err := os.Stat(profileDir); err == nil && !st.IsDir() {
		return fmt.Errorf("profile path exists and is not a directory: %s", profileDir)
	} else if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("stat profile dir: %w", err)
	}

	backedUp := 0
	requiredFound := false
	optionalFound := false
	var missingRequired []string
	var originalPaths []string
	managedFiles := make([]managedFileState, 0, len(fileSet.Files))
	plan := make([]backupPlanEntry, 0, len(fileSet.Files))
	for _, spec := range fileSet.Files {
		state := managedStateForSpec(fileSet.Tool, spec)
		// Claude Desktop config: capture ONLY the oauth:tokenCache* fields, so we
		// never persist (or later clobber) unrelated desktop settings (PR #44).
		if isClaudeDesktopConfig(fileSet.Tool, spec.Path) {
			fields, ok, err := claudeDesktopTokenCache(spec.Path)
			if err != nil {
				return err
			}
			if !ok {
				managedFiles = append(managedFiles, state)
				continue // no token cache present — nothing to back up
			}
			backedUp++
			optionalFound = true
			originalPaths = append(originalPaths, spec.Path)
			state.Present = true
			managedFiles = append(managedFiles, state)
			plan = append(plan, backupPlanEntry{
				spec:                spec,
				state:               state,
				claudeDesktopFields: fields,
			})
			continue
		}

		if _, err := os.Stat(spec.Path); err != nil {
			if !os.IsNotExist(err) {
				return fmt.Errorf("stat %s: %w", spec.Path, err)
			}
			if spec.Required {
				missingRequired = append(missingRequired, spec.Path)
			}
			managedFiles = append(managedFiles, state)
			continue // Skip optional files that don't exist
		}

		backedUp++
		if spec.Required {
			requiredFound = true
		} else if optionalFileCountsAsAuth(fileSet.Tool, spec.Path, spec.Path) {
			optionalFound = true
		}
		originalPaths = append(originalPaths, spec.Path)
		state.Present = true
		managedFiles = append(managedFiles, state)
		plan = append(plan, backupPlanEntry{spec: spec, state: state})
	}

	if backedUp == 0 {
		return fmt.Errorf("no auth files found to backup for %s; ensure you're logged in first with '%s' or 'caam add %s'", tool, tool, tool)
	}
	if len(missingRequired) > 0 {
		if !(fileSet.AllowOptionalOnly && !requiredFound && optionalFound) {
			return fmt.Errorf("required auth file not found: %s", missingRequired[0])
		}
	}

	stagingDir, err := v.newBackupStagingDir(tool, profile)
	if err != nil {
		return err
	}
	committed := false
	defer func() {
		if !committed {
			_ = os.RemoveAll(stagingDir)
		}
	}()

	for _, f := range managedFiles {
		if f.Present {
			continue
		}
		stalePath := filepath.Join(profileDir, f.VaultName)
		if _, err := v.copyManagedPathToQuarantine(stagingDir, "backup", f.OriginalPath, stalePath); err != nil {
			return fmt.Errorf("quarantine stale backup %s: %w", stalePath, err)
		}
	}
	for _, entry := range plan {
		destPath := filepath.Join(stagingDir, entry.state.VaultName)
		if entry.state.ClaudeDesktopTokenCache {
			if err := writeJSONFileAtomic(destPath, entry.claudeDesktopFields, 0600); err != nil {
				return fmt.Errorf("backup %s: %w", entry.spec.Path, err)
			}
			continue
		}
		if err := copyFile(entry.spec.Path, destPath); err != nil {
			return fmt.Errorf("backup %s: %w", entry.spec.Path, err)
		}
	}

	// Write metadata
	meta := vaultProfileMeta{
		Tool:            tool,
		Profile:         profile,
		BackedUpAt:      time.Now().Format(time.RFC3339),
		Files:           backedUp,
		ManifestVersion: 1,
		Type:            "user",
		CreatedBy:       "user",
		OriginalPaths:   originalPaths,
		ManagedFiles:    managedFiles,
	}
	if IsSystemProfile(profile) {
		meta.Type = "system"
		meta.CreatedBy = "auto"
		if profile == originalProfileName {
			meta.CreatedBy = "first-activate"
		}
	}
	if err := writeJSONFileAtomic(filepath.Join(stagingDir, profileMetaFilename), meta, 0600); err != nil {
		return fmt.Errorf("write metadata: %w", err)
	}

	if err := v.commitProfileStaging(tool, profile, profileDir, stagingDir); err != nil {
		return err
	}
	committed = true

	return nil
}

// HasOriginalBackup reports whether the system-managed `_original` profile exists
// for the given tool.
func (v *Vault) HasOriginalBackup(tool string) (bool, error) {
	profileDir, err := v.safeProfileDir(tool, originalProfileName)
	if err != nil {
		return false, err
	}
	st, err := os.Stat(profileDir)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, fmt.Errorf("stat original profile dir: %w", err)
	}
	if !st.IsDir() {
		return false, fmt.Errorf("original profile path is not a directory: %s", profileDir)
	}
	return true, nil
}

// BackupCurrent creates a timestamped backup of the current auth state.
// Returns the backup profile name (e.g., "_backup_20251217_143022") if created,
// or empty string if there was nothing to back up.
func (v *Vault) BackupCurrent(fileSet AuthFileSet) (string, error) {
	// Only back up when at least one auth file exists.
	if !HasAuthFiles(fileSet) {
		return "", nil
	}

	for attempt := range backupCurrentRetries {
		// Generate timestamped backup name with nanosecond precision so repeated
		// safety backups in the same second do not collide with immutable system
		// profiles. Retry handles fixed clocks and filesystem timestamp quirks.
		now := timeNow()
		timestamp := fmt.Sprintf("%s_%09d", now.Format("20060102_150405"), now.Nanosecond())
		backupName := "_backup_" + timestamp
		if attempt > 0 {
			backupName = fmt.Sprintf("%s_%03d", backupName, attempt)
		}

		if err := v.Backup(fileSet, backupName); err != nil {
			if errors.Is(err, errProtectedSystemProfile) {
				continue
			}
			return "", fmt.Errorf("backup current: %w", err)
		}

		return backupName, nil
	}

	return "", fmt.Errorf("backup current: could not create unique backup name after %d attempts", backupCurrentRetries)
}

// ResnapshotOutgoing re-captures the live auth files of the currently-active
// profile back into its own vault directory BEFORE a switch overwrites them.
//
// Rationale (Codex/ChatGPT refresh-token rotation): while a profile is active,
// the tool silently rotates its OAuth tokens in place. The vault snapshot taken
// at backup/login time goes stale the moment the first rotation lands. If we
// switch away (clobbering the live file) without re-snapshotting, the stale
// vault copy now holds an already-consumed refresh_token; the NEXT time that
// profile is restored, presenting it trips the IdP's reuse detection and
// revokes the whole token family.
//
// Guards (all skip silently, returning nil):
//   - empty/missing outgoing profile (unknown live state)
//   - system profiles (_original, _backup_*, _auto_backup_* — immutable)
//   - the target profile we are about to switch TO (would be pointless/racey)
//   - no live auth files present
//
// Errors are returned to the caller but are intended to be treated as
// NON-FATAL (a failed re-snapshot must never block a switch).
func (v *Vault) ResnapshotOutgoing(fileSet AuthFileSet, outgoing, target string) error {
	outgoing = strings.TrimSpace(outgoing)
	target = strings.TrimSpace(target)

	if outgoing == "" || outgoing == target {
		return nil
	}
	if IsSystemProfile(outgoing) {
		return nil // immutable safety artifacts; never rewrite
	}
	if !HasAuthFiles(fileSet) {
		return nil // nothing live to capture
	}

	// Only re-snapshot if the outgoing profile still actually exists in the
	// vault (don't resurrect a deleted profile).
	profileDir, err := v.safeProfileDir(fileSet.Tool, outgoing)
	if err != nil {
		return err
	}
	if st, err := os.Stat(profileDir); err != nil || !st.IsDir() {
		return nil
	}

	return v.Backup(fileSet, outgoing)
}

// RotateAutoBackups removes old auto-backup profiles to stay within the limit.
// Backups are sorted by timestamp (oldest first) and oldest are deleted.
// A maxBackups of 0 means unlimited (no rotation).
func (v *Vault) RotateAutoBackups(tool string, maxBackups int) error {
	if maxBackups <= 0 {
		return nil // Unlimited
	}

	profiles, err := v.List(tool)
	if err != nil {
		return fmt.Errorf("list profiles: %w", err)
	}

	// Filter to auto-backup profiles only
	var backups []string
	for _, p := range profiles {
		if strings.HasPrefix(p, "_backup_") {
			backups = append(backups, p)
		}
	}

	// Already within limit?
	if len(backups) <= maxBackups {
		return nil
	}

	// Sort by name (which includes timestamp, so oldest first)
	// _backup_20251217_143022 sorts lexicographically by date/time
	sort.Strings(backups)

	// Delete oldest until we're within limit
	toDelete := len(backups) - maxBackups
	for i := 0; i < toDelete; i++ {
		if err := v.DeleteForce(tool, backups[i]); err != nil {
			return fmt.Errorf("delete old backup %s: %w", backups[i], err)
		}
	}

	return nil
}

// BackupOriginal creates the system-managed `_original` profile for a tool if
// needed. This is intended to preserve a user's pre-caam auth state.
//
// Behavior:
// - No-op if `_original` already exists
// - No-op if no current auth files exist
// - No-op if current auth already matches an existing vault profile
// - Otherwise backups current auth as `_original`
//
// It returns true if a backup was created.
func (v *Vault) BackupOriginal(fileSet AuthFileSet) (bool, error) {
	exists, err := v.HasOriginalBackup(fileSet.Tool)
	if err != nil {
		return false, err
	}
	if exists {
		return false, nil
	}

	// Only back up when at least one auth file exists.
	if !HasAuthFiles(fileSet) {
		return false, nil
	}

	active, err := v.ActiveProfile(fileSet)
	if err != nil {
		return false, fmt.Errorf("detect active profile: %w", err)
	}
	if active != "" {
		return false, nil
	}

	if err := v.Backup(fileSet, originalProfileName); err != nil {
		return false, err
	}
	return true, nil
}

// MigrateGeminiVaultDir renames oauth_credentials.json to oauth_creds.json in a
// vault profile directory if the old name exists and the new name does not.
// CAAM previously stored "oauth_credentials.json" but Gemini CLI reads "oauth_creds.json".
// This is a no-op if the directory already has the new name or has no OAuth file.
func MigrateGeminiVaultDir(dir string) error {
	oldName := filepath.Join(dir, "oauth_credentials.json")
	newName := filepath.Join(dir, "oauth_creds.json")
	if _, err := os.Stat(oldName); err != nil {
		if os.IsNotExist(err) {
			return nil // old file doesn't exist, nothing to migrate
		}
		return err // permission or I/O error
	}
	if _, err := os.Stat(newName); err == nil {
		// New file already exists; remove legacy file to avoid confusion.
		_ = os.Remove(oldName)
		return nil
	}
	if err := os.Rename(oldName, newName); err != nil {
		// Handle race: another process may have completed the migration.
		if _, statErr := os.Stat(newName); statErr == nil {
			return nil
		}
		return err
	}
	return nil
}

// Restore copies backed-up auth files to their original locations.
func (v *Vault) Restore(fileSet AuthFileSet, profile string) error {
	profileDir, err := v.safeProfileDir(fileSet.Tool, profile)
	if err != nil {
		return err
	}
	if err := validateUniqueVaultNames(fileSet); err != nil {
		return err
	}

	if _, err := os.Stat(profileDir); os.IsNotExist(err) {
		return fmt.Errorf("profile %s/%s not found in vault; run 'caam ls %s' to see available profiles", fileSet.Tool, profile, fileSet.Tool)
	}

	restored := 0
	requiredFound := false
	optionalFound := false
	var missingRequired []string
	var pendingRequiredCleanup []restoreCleanupEntry
	managedManifest, hasManagedManifest, err := v.managedFileManifest(profileDir)
	if err != nil {
		return err
	}
	if fileSet.Tool == "gemini" && !hasManagedManifest {
		if err := MigrateGeminiVaultDir(profileDir); err != nil {
			return fmt.Errorf("vault migration (oauth_credentials.json -> oauth_creds.json): %w", err)
		}
	}
	if hasManagedManifest {
		if err := preflightManagedRestore(fileSet, profileDir, managedManifest); err != nil {
			return err
		}
	} else {
		if err := preflightLegacyRestoreAuthViability(fileSet, profileDir); err != nil {
			return err
		}
	}
	for _, spec := range fileSet.Files {
		filename := filepath.Base(spec.Path)
		srcPath := filepath.Join(profileDir, filename)
		state, stateKnown := managedStateForRestore(fileSet, managedManifest, spec)
		stateMatchesSpec := stateKnown && filepath.Clean(state.OriginalPath) == filepath.Clean(spec.Path)
		if hasManagedManifest {
			if !stateKnown {
				continue
			}
			if !stateMatchesSpec {
				continue
			}
			srcPath = filepath.Join(profileDir, state.VaultName)
		}

		if hasManagedManifest && !state.Present {
			if _, err := v.quarantineManagedPath(profileDir, "restore-vault", state.OriginalPath, srcPath); err != nil {
				return fmt.Errorf("quarantine stale vault auth file %s: %w", srcPath, err)
			}
			if spec.Required {
				missingRequired = append(missingRequired, srcPath)
				if fileSet.AllowOptionalOnly {
					pendingRequiredCleanup = append(pendingRequiredCleanup, restoreCleanupEntry{spec: spec, state: state})
				}
				continue
			}
			if err := v.cleanupAbsentManagedLive(profileDir, fileSet, spec, state); err != nil {
				return err
			}
			continue
		}

		// Check if backup exists
		if _, err := os.Stat(srcPath); os.IsNotExist(err) {
			if hasManagedManifest && state.Present {
				return fmt.Errorf("managed backup listed in metadata but missing: %s", srcPath)
			}
			if spec.Required {
				missingRequired = append(missingRequired, srcPath)
			}
			continue // Skip optional files
		} else if err != nil {
			return fmt.Errorf("stat backup %s: %w", srcPath, err)
		}

		// Claude Desktop config: MERGE the snapshot's oauth:tokenCache* fields
		// into the live desktop config (replacing any stale cache) while leaving
		// unrelated desktop settings intact, so switching the CAAM profile also
		// swaps the account the desktop cache would otherwise reassert (PR #44).
		if isClaudeDesktopConfig(fileSet.Tool, spec.Path) {
			restoredTokenCache, err := restoreClaudeDesktopTokenCache(srcPath, spec.Path)
			if err != nil {
				return err
			}
			if !restoredTokenCache {
				if spec.Required {
					missingRequired = append(missingRequired, srcPath)
				}
				continue
			}
			restored++
			optionalFound = true
			continue
		}

		// Ensure parent directory exists
		if err := os.MkdirAll(filepath.Dir(spec.Path), 0700); err != nil {
			return fmt.Errorf("create parent dir for %s: %w", spec.Path, err)
		}

		// Freshness guard (Codex/ChatGPT refresh-token rotation safety):
		// If the LIVE auth file is the SAME OpenAI identity as this snapshot but
		// was refreshed more recently, the snapshot's refresh_token has already
		// been rotated out (consumed). Restoring it verbatim would trip the
		// IdP's reuse detection and revoke the whole token family, bricking the
		// account. In that case, leave the live file untouched. Different
		// identity / missing live file / older-or-equal live / unparseable
		// timestamps all fall through to the normal verbatim copy, so genuine
		// cross-account switches and non-codex restores are never blocked.
		if fileSet.Tool == "codex" && codexLiveIsNewer(spec.Path, srcPath) {
			restored++
			if spec.Required {
				requiredFound = true
			} else if optionalFileCountsAsAuth(fileSet.Tool, spec.Path, srcPath) {
				optionalFound = true
			}
			continue
		}

		// Copy from vault to original location
		if err := copyFile(srcPath, spec.Path); err != nil {
			return fmt.Errorf("restore %s: %w", spec.Path, err)
		}
		restored++
		if spec.Required {
			requiredFound = true
		} else if optionalFileCountsAsAuth(fileSet.Tool, spec.Path, srcPath) {
			optionalFound = true
		}
	}

	if restored == 0 {
		return fmt.Errorf("no auth files restored for %s/%s", fileSet.Tool, profile)
	}
	if len(missingRequired) > 0 {
		if !(fileSet.AllowOptionalOnly && !requiredFound && optionalFound) {
			return fmt.Errorf("required backup not found: %s", missingRequired[0])
		}
	}
	for _, entry := range pendingRequiredCleanup {
		if err := v.cleanupAbsentManagedLive(profileDir, fileSet, entry.spec, entry.state); err != nil {
			return err
		}
	}

	return nil
}

// List returns all profiles stored for a tool.
func (v *Vault) List(tool string) ([]string, error) {
	toolDir, err := v.safeToolDir(tool)
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(toolDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	var profiles []string
	for _, e := range entries {
		if hiddenVaultEntry(e.Name()) {
			continue
		}
		if e.IsDir() {
			profiles = append(profiles, e.Name())
		}
	}
	return profiles, nil
}

// ListAll returns all profiles for all tools.
func (v *Vault) ListAll() (map[string][]string, error) {
	result := make(map[string][]string)

	entries, err := os.ReadDir(v.basePath)
	if err != nil {
		if os.IsNotExist(err) {
			return result, nil
		}
		return nil, err
	}

	for _, e := range entries {
		if hiddenVaultEntry(e.Name()) {
			continue
		}
		if e.IsDir() {
			profiles, err := v.List(e.Name())
			if err != nil {
				continue
			}
			result[e.Name()] = profiles
		}
	}

	return result, nil
}

// Delete removes a profile from the vault.
func (v *Vault) Delete(tool, profile string) error {
	if IsSystemProfile(profile) {
		return fmt.Errorf("%w: refusing to delete %s/%s without force", errProtectedSystemProfile, tool, profile)
	}
	return v.DeleteForce(tool, profile)
}

// DeleteForce removes a profile from the vault, including system profiles.
// Prefer Delete unless the caller has an explicit reason to remove protected
// profiles.
func (v *Vault) DeleteForce(tool, profile string) error {
	profileDir, err := v.safeProfileDir(tool, profile)
	if err != nil {
		return err
	}
	return os.RemoveAll(profileDir)
}

// CopyProfile creates a copy of a profile with a new name.
// This is a non-destructive operation: the source profile remains unchanged.
// Returns an error if the source doesn't exist or the destination already exists.
func (v *Vault) CopyProfile(tool, srcProfile, dstProfile string) error {
	srcDir, err := v.safeProfileDir(tool, srcProfile)
	if err != nil {
		return fmt.Errorf("invalid source profile: %w", err)
	}
	dstDir, err := v.safeProfileDir(tool, dstProfile)
	if err != nil {
		return fmt.Errorf("invalid destination profile: %w", err)
	}

	// Verify source exists
	if _, err := os.Stat(srcDir); os.IsNotExist(err) {
		return fmt.Errorf("source profile %s/%s not found", tool, srcProfile)
	}

	// Verify destination doesn't exist
	if _, err := os.Stat(dstDir); err == nil {
		return fmt.Errorf("destination profile %s/%s already exists", tool, dstProfile)
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("check destination: %w", err)
	}

	// Create destination directory
	if err := os.MkdirAll(dstDir, 0700); err != nil {
		return fmt.Errorf("create destination dir: %w", err)
	}

	// Copy all files from source to destination
	entries, err := os.ReadDir(srcDir)
	if err != nil {
		os.RemoveAll(dstDir) // Cleanup on failure
		return fmt.Errorf("read source dir: %w", err)
	}

	for _, entry := range entries {
		if entry.IsDir() {
			continue // Skip subdirectories
		}

		srcPath := filepath.Join(srcDir, entry.Name())
		dstPath := filepath.Join(dstDir, entry.Name())

		if err := copyFile(srcPath, dstPath); err != nil {
			os.RemoveAll(dstDir) // Cleanup on failure
			return fmt.Errorf("copy %s: %w", entry.Name(), err)
		}
	}

	// Update meta.json with new profile name
	metaPath := filepath.Join(dstDir, "meta.json")
	if _, err := os.Stat(metaPath); err == nil {
		data, err := os.ReadFile(metaPath)
		if err != nil {
			return fmt.Errorf("read copied meta.json: %w", err)
		}
		var meta map[string]interface{}
		if err := json.Unmarshal(data, &meta); err != nil {
			return fmt.Errorf("parse copied meta.json: %w", err)
		}
		meta["profile"] = dstProfile
		meta["copied_from"] = srcProfile
		meta["copied_at"] = time.Now().Format(time.RFC3339)
		updated, err := json.MarshalIndent(meta, "", "  ")
		if err != nil {
			return fmt.Errorf("marshal copied meta.json: %w", err)
		}
		tmpPath := metaPath + ".tmp"
		if err := os.WriteFile(tmpPath, updated, 0600); err != nil {
			return fmt.Errorf("write copied meta.json: %w", err)
		}
		if err := os.Rename(tmpPath, metaPath); err != nil {
			return fmt.Errorf("replace copied meta.json: %w", err)
		}
	} else if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("stat copied meta.json: %w", err)
	}

	return nil
}

// ActiveProfile returns which profile is currently active (if any).
// It compares the current auth files with vault backups using stable identity
// hashing. For tools like Claude and Codex, only identity-bearing fields are
// hashed so that volatile metadata (e.g., changelogLastFetched, numStartups)
// does not break profile detection.
func (v *Vault) ActiveProfile(fileSet AuthFileSet) (string, error) {
	profiles, err := v.List(fileSet.Tool)
	if err != nil {
		return "", err
	}

	// Hash the current auth files using stable identity extraction.
	// Prefer required files for matching; optional files can change frequently
	// (e.g., settings/session files) and should not break profile detection.
	currentHashes := make(map[string][]string)
	optionalHashes := make(map[string][]string)
	requiredFound := false
	for _, spec := range fileSet.Files {
		// A Claude Desktop config with no token cache carries no identity; skip it
		// so unrelated desktop settings never drive profile detection (PR #44).
		if isClaudeDesktopConfig(fileSet.Tool, spec.Path) {
			if _, ok, err := claudeDesktopTokenCache(spec.Path); err != nil || !ok {
				continue
			}
		}
		if _, err := os.Stat(spec.Path); os.IsNotExist(err) {
			continue
		}
		hashes, err := stableFileHashes(fileSet.Tool, spec.Path)
		if err != nil {
			continue
		}
		base := filepath.Base(spec.Path)
		if spec.Required {
			requiredFound = true
			currentHashes[base] = hashes
			continue
		}
		if !optionalFileCountsAsAuth(fileSet.Tool, spec.Path, spec.Path) {
			continue
		}
		optionalHashes[base] = hashes
	}

	if !requiredFound {
		if fileSet.AllowOptionalOnly {
			currentHashes = optionalHashes
		}
	}

	if len(currentHashes) == 0 {
		return "", nil // No relevant auth files present
	}

	// Compare with each profile.
	// Prefer user-named profiles over system profiles (_backup_*, _original,
	// _auto_backup_*). System profiles can share the same identity as a named
	// profile (same account re-authenticated), and because they sort
	// alphabetically before most user names (underscore < lowercase letters),
	// they would otherwise shadow the intended named profile.
	userMatches := make([]string, 0, len(profiles))
	var systemMatch string
	for _, profile := range profiles {
		profileDir := v.ProfilePath(fileSet.Tool, profile)
		matches := true

		for filename, currentFileHashes := range currentHashes {
			backupPath := filepath.Join(profileDir, filename)
			backupHashes, err := stableFileHashes(fileSet.Tool, backupPath)
			if err != nil {
				matches = false
				break
			}
			if !hashesOverlap(currentFileHashes, backupHashes) {
				matches = false
				break
			}
		}

		if matches {
			if !IsSystemProfile(profile) {
				userMatches = append(userMatches, profile)
				continue
			}
			if systemMatch == "" {
				systemMatch = profile // Remember first system match as fallback
			}
		}
	}

	if len(userMatches) > 0 {
		return v.preferredActiveProfile(fileSet.Tool, userMatches), nil
	}
	return systemMatch, nil // Fall back to system profile, or "" if no match
}

func (v *Vault) preferredActiveProfile(tool string, matches []string) string {
	if len(matches) == 1 {
		return matches[0]
	}
	matchSet := make(map[string]bool, len(matches))
	for _, match := range matches {
		matchSet[match] = true
	}

	var preferred string
	var preferredCopiedAt time.Time
	for _, match := range matches {
		meta, ok := v.profileMeta(tool, match)
		if !ok {
			continue
		}
		copiedFrom, _ := meta["copied_from"].(string)
		if copiedFrom == "" || !matchSet[copiedFrom] {
			continue
		}
		copiedAt, _ := time.Parse(time.RFC3339, copiedAtString(meta["copied_at"]))
		if preferred == "" || copiedAt.After(preferredCopiedAt) {
			preferred = match
			preferredCopiedAt = copiedAt
		}
	}
	if preferred != "" {
		return preferred
	}
	return matches[0]
}

func (v *Vault) profileMeta(tool, profile string) (map[string]interface{}, bool) {
	data, err := os.ReadFile(filepath.Join(v.ProfilePath(tool, profile), "meta.json"))
	if err != nil {
		return nil, false
	}
	var meta map[string]interface{}
	if err := json.Unmarshal(data, &meta); err != nil {
		return nil, false
	}
	return meta, true
}

func copiedAtString(value interface{}) string {
	if s, ok := value.(string); ok {
		return s
	}
	return ""
}

// HasAuthFiles checks if the tool currently has auth files present.
func HasAuthFiles(fileSet AuthFileSet) bool {
	optionalFound := false
	for _, spec := range fileSet.Files {
		// The Claude Desktop config only counts as auth when it holds a token
		// cache (the file also exists for token-less desktop installs).
		if isClaudeDesktopConfig(fileSet.Tool, spec.Path) {
			if _, ok, err := claudeDesktopTokenCache(spec.Path); err == nil && ok {
				optionalFound = true
			}
			continue
		}
		if _, err := os.Stat(spec.Path); err == nil {
			if spec.Required {
				return true
			}
			if optionalFileCountsAsAuth(fileSet.Tool, spec.Path, spec.Path) {
				optionalFound = true
			}
		}
	}
	if fileSet.AllowOptionalOnly && optionalFound {
		return true
	}
	return false
}

// ClearAuthFiles removes all auth files for a tool (logout).
func ClearAuthFiles(fileSet AuthFileSet) error {
	for _, spec := range fileSet.Files {
		// For the Claude Desktop config, scrub only the oauth:tokenCache* keys so
		// logout does not destroy the user's unrelated desktop settings (PR #44).
		if isClaudeDesktopConfig(fileSet.Tool, spec.Path) {
			if err := scrubClaudeDesktopTokenCache(spec.Path); err != nil {
				return err
			}
			continue
		}
		if err := os.Remove(spec.Path); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("remove %s: %w", spec.Path, err)
		}
	}
	return nil
}

// --- Claude Desktop OAuth token cache (macOS) -------------------------------
//
// Recent Claude Code builds on macOS can refresh ~/.claude.json from Claude
// Desktop's encrypted OAuth token cache at
// ~/Library/Application Support/Claude/config.json. If caam swaps only the
// dotfiles, that cache silently reasserts the previous account on the next
// launch (reported in PR #44). caam therefore tracks ONLY the oauth cache
// fields: it captures them on backup, merges them back on restore, and scrubs
// only them on clear — never touching the unrelated Claude Desktop settings
// stored in the same file. The values are opaque encrypted blobs; caam moves
// them verbatim, which is valid for the same-machine backup/restore it performs.
const (
	claudeDesktopTokenKey   = "oauth:tokenCache"
	claudeDesktopTokenKeyV2 = "oauth:tokenCacheV2"
)

var claudeDesktopTokenKeys = []string{claudeDesktopTokenKey, claudeDesktopTokenKeyV2}

// isClaudeDesktopConfig reports whether spec.Path is the macOS Claude Desktop
// config.json, which needs field-scoped handling rather than whole-file copy.
func isClaudeDesktopConfig(tool, path string) bool {
	return tool == "claude" &&
		filepath.Base(path) == "config.json" &&
		strings.Contains(filepath.ToSlash(path), "/Library/Application Support/Claude/")
}

// claudeDesktopTokenCache reads path and returns just its oauth:tokenCache*
// fields. ok is false when the file is absent or carries no token cache.
func claudeDesktopTokenCache(path string) (fields map[string]interface{}, ok bool, err error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, false, nil
		}
		return nil, false, fmt.Errorf("read Claude desktop config: %w", err)
	}
	var root map[string]interface{}
	if err := json.Unmarshal(data, &root); err != nil {
		return nil, false, fmt.Errorf("parse Claude desktop config %s: %w", path, err)
	}
	fields = map[string]interface{}{}
	for _, k := range claudeDesktopTokenKeys {
		if v, exists := root[k]; exists {
			fields[k] = v
		}
	}
	return fields, len(fields) > 0, nil
}

// restoreClaudeDesktopTokenCache merges the token-cache fields captured in the
// vault snapshot (vaultPath) into the live desktop config (livePath), replacing
// any stale cache and preserving every other setting. The live file is created
// if it does not yet exist. The bool reports whether token-cache fields were
// actually restored.
func restoreClaudeDesktopTokenCache(vaultPath, livePath string) (bool, error) {
	fields, ok, err := claudeDesktopTokenCache(vaultPath)
	if err != nil {
		return false, err
	}
	if !ok {
		return false, nil // snapshot has nothing to restore
	}
	live := map[string]interface{}{}
	if data, rerr := os.ReadFile(livePath); rerr == nil {
		if uerr := json.Unmarshal(data, &live); uerr != nil {
			return false, fmt.Errorf("parse live Claude desktop config %s: %w", livePath, uerr)
		}
	} else if !os.IsNotExist(rerr) {
		return false, fmt.Errorf("read live Claude desktop config: %w", rerr)
	}
	// Drop any stale cache, then apply the snapshot's fields.
	for _, k := range claudeDesktopTokenKeys {
		delete(live, k)
	}
	for k, v := range fields {
		live[k] = v
	}
	if err := os.MkdirAll(filepath.Dir(livePath), 0700); err != nil {
		return false, fmt.Errorf("create Claude desktop config dir: %w", err)
	}
	if err := writeJSONFileAtomic(livePath, live, 0600); err != nil {
		return false, fmt.Errorf("write Claude desktop config: %w", err)
	}
	return true, nil
}

// scrubClaudeDesktopTokenCache deletes only the oauth:tokenCache* keys from the
// live desktop config, leaving all other settings intact. A missing file or a
// file with no token cache is a no-op.
func scrubClaudeDesktopTokenCache(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("read Claude desktop config: %w", err)
	}
	var root map[string]interface{}
	if err := json.Unmarshal(data, &root); err != nil {
		return fmt.Errorf("parse Claude desktop config %s: %w", path, err)
	}
	changed := false
	for _, k := range claudeDesktopTokenKeys {
		if _, ok := root[k]; ok {
			delete(root, k)
			changed = true
		}
	}
	if !changed {
		return nil
	}
	if err := writeJSONFileAtomic(path, root, 0600); err != nil {
		return fmt.Errorf("write Claude desktop config: %w", err)
	}
	return nil
}

// hashClaudeDesktopConfig hashes ONLY the oauth:tokenCache* fields, so unrelated
// desktop settings never perturb active-profile detection.
func hashClaudeDesktopConfig(path string) (string, error) {
	fields, ok, err := claudeDesktopTokenCache(path)
	if err != nil {
		return "", err
	}
	if !ok {
		h := sha256.New()
		h.Write([]byte("claude:desktop:no-token"))
		return hex.EncodeToString(h.Sum(nil)), nil
	}
	canonical, err := json.Marshal(fields)
	if err != nil {
		return "", err
	}
	h := sha256.New()
	h.Write([]byte("claude:desktop:"))
	h.Write(canonical)
	return hex.EncodeToString(h.Sum(nil)), nil
}

func managedStateForSpec(tool string, spec AuthFileSpec) managedFileState {
	return managedFileState{
		VaultName:               filepath.Base(spec.Path),
		OriginalPath:            spec.Path,
		Required:                spec.Required,
		ClaudeDesktopTokenCache: isClaudeDesktopConfig(tool, spec.Path),
	}
}

func validateUniqueVaultNames(fileSet AuthFileSet) error {
	seen := make(map[string]string, len(fileSet.Files))
	for _, spec := range fileSet.Files {
		name := managedStateForSpec(fileSet.Tool, spec).VaultName
		if prev, ok := seen[name]; ok {
			return fmt.Errorf("duplicate managed auth vault name %q for %s and %s", name, prev, spec.Path)
		}
		seen[name] = spec.Path
	}
	return nil
}

func hiddenVaultEntry(name string) bool {
	return strings.HasPrefix(name, ".")
}

func (v *Vault) newBackupStagingDir(tool, profile string) (string, error) {
	toolDir, err := v.safeToolDir(tool)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(toolDir, 0700); err != nil {
		return "", fmt.Errorf("create tool dir: %w", err)
	}
	dir, err := os.MkdirTemp(toolDir, "."+profile+".staging.*")
	if err != nil {
		return "", fmt.Errorf("create staging profile dir: %w", err)
	}
	if err := os.Chmod(dir, 0700); err != nil {
		return "", fmt.Errorf("chmod staging profile dir: %w", err)
	}
	return dir, nil
}

func (v *Vault) commitProfileStaging(tool, profile, profileDir, stagingDir string) error {
	if st, err := os.Stat(profileDir); err == nil {
		if !st.IsDir() {
			return fmt.Errorf("profile path exists and is not a directory: %s", profileDir)
		}
		backupDir, err := v.profileReplacementBackupDir(tool, profile)
		if err != nil {
			return err
		}
		if err := os.Rename(profileDir, backupDir); err != nil {
			return fmt.Errorf("move old profile aside: %w", err)
		}
		if err := os.Rename(stagingDir, profileDir); err != nil {
			_ = os.Rename(backupDir, profileDir)
			return fmt.Errorf("commit staged profile: %w", err)
		}
		return nil
	} else if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("stat profile dir: %w", err)
	}
	if err := os.Rename(stagingDir, profileDir); err != nil {
		return fmt.Errorf("commit staged profile: %w", err)
	}
	return nil
}

func (v *Vault) profileReplacementBackupDir(tool, profile string) (string, error) {
	stamp := time.Now().UTC().Format("20060102T150405.000000000Z")
	dir := filepath.Join(v.basePath, profileBackupDirName, tool, profile, stamp)
	if err := os.MkdirAll(filepath.Dir(dir), 0700); err != nil {
		return "", fmt.Errorf("create profile replacement backup dir: %w", err)
	}
	return dir, nil
}

func (v *Vault) managedFileManifest(profileDir string) (map[string]managedFileState, bool, error) {
	metaPath := filepath.Join(profileDir, profileMetaFilename)
	data, err := os.ReadFile(metaPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, false, nil
		}
		return nil, false, fmt.Errorf("read profile metadata: %w", err)
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, false, fmt.Errorf("parse profile metadata %s: %w", metaPath, err)
	}
	_, managedFieldPresent := raw["managed_files"]
	var meta vaultProfileMeta
	if err := json.Unmarshal(data, &meta); err != nil {
		return nil, false, fmt.Errorf("parse profile metadata %s: %w", metaPath, err)
	}
	if len(meta.ManagedFiles) == 0 {
		if managedFieldPresent || meta.ManifestVersion > 0 {
			return map[string]managedFileState{}, true, nil
		}
		return nil, false, nil
	}
	managedProfile := managedFieldPresent || meta.ManifestVersion > 0
	managed := make(map[string]managedFileState, len(meta.ManagedFiles))
	originalPaths := make(map[string]string, len(meta.ManagedFiles))
	for _, state := range meta.ManagedFiles {
		state.OriginalPath = strings.TrimSpace(state.OriginalPath)
		if state.OriginalPath == "" || !filepath.IsAbs(state.OriginalPath) {
			if managedProfile {
				return nil, false, fmt.Errorf("invalid managed file metadata in %s", metaPath)
			}
			continue
		}
		originalPath := filepath.Clean(state.OriginalPath)
		if prev, exists := originalPaths[originalPath]; exists {
			return nil, false, fmt.Errorf("duplicate managed original path %q for %s and %s", originalPath, prev, state.VaultName)
		}
		originalPaths[originalPath] = state.VaultName
		name := strings.TrimSpace(state.VaultName)
		if name == "" {
			name = filepath.Base(originalPath)
		}
		if name == "" || name == "." || name == string(os.PathSeparator) || name != filepath.Base(name) {
			if managedProfile {
				return nil, false, fmt.Errorf("invalid managed file metadata in %s", metaPath)
			}
			continue
		}
		state.VaultName = name
		if prev, exists := managed[name]; exists {
			return nil, false, fmt.Errorf("duplicate managed file metadata %q for %s and %s", name, prev.OriginalPath, state.OriginalPath)
		}
		managed[name] = state
	}
	return managed, len(managed) > 0, nil
}

func preflightManagedRestore(fileSet AuthFileSet, profileDir string, manifest map[string]managedFileState) error {
	requiredMissing := false
	requiredPresent := false
	authOptionalPresent := false
	for _, spec := range fileSet.Files {
		srcPath := filepath.Join(profileDir, filepath.Base(spec.Path))
		if geminiHasDuplicateOAuthAlias(fileSet, manifest, spec) {
			return fmt.Errorf("duplicate Gemini managed OAuth metadata for %s", spec.Path)
		}
		state, stateKnown := managedStateForRestore(fileSet, manifest, spec)
		if !stateKnown {
			if spec.Required {
				return fmt.Errorf("managed metadata missing required file: %s", srcPath)
			}
			continue
		}
		srcPath = filepath.Join(profileDir, state.VaultName)
		if filepath.Clean(state.OriginalPath) != filepath.Clean(spec.Path) {
			if spec.Required {
				return fmt.Errorf("managed metadata path mismatch for required file: %s", srcPath)
			}
			continue
		}
		if !state.Present {
			if spec.Required {
				requiredMissing = true
			}
			if st, err := os.Lstat(srcPath); err == nil {
				if !st.Mode().IsRegular() {
					return fmt.Errorf("managed stale backup path is not a regular file: %s", srcPath)
				}
			} else if err != nil && !os.IsNotExist(err) {
				return fmt.Errorf("stat stale backup %s: %w", srcPath, err)
			}
			if spec.Required && !fileSet.AllowOptionalOnly {
				return fmt.Errorf("required backup not found: %s", srcPath)
			}
			continue
		}
		if spec.Required {
			requiredPresent = true
		} else if optionalFileCountsAsAuth(fileSet.Tool, spec.Path, srcPath) {
			authOptionalPresent = true
		}
		st, err := os.Stat(srcPath)
		if err != nil {
			if os.IsNotExist(err) {
				return fmt.Errorf("managed backup listed in metadata but missing: %s", srcPath)
			}
			return fmt.Errorf("stat backup %s: %w", srcPath, err)
		}
		if !st.Mode().IsRegular() {
			return fmt.Errorf("managed backup path is not a regular file: %s", srcPath)
		}
		if isClaudeDesktopConfig(fileSet.Tool, spec.Path) {
			if _, ok, err := claudeDesktopTokenCache(srcPath); err != nil {
				return err
			} else if !ok {
				return fmt.Errorf("managed Claude desktop token cache listed but empty: %s", srcPath)
			}
		}
	}
	if requiredMissing && fileSet.AllowOptionalOnly && !requiredPresent && !authOptionalPresent {
		return fmt.Errorf("required backup not found and no auth-bearing optional backup is present")
	}
	return nil
}

func preflightLegacyRestoreAuthViability(fileSet AuthFileSet, profileDir string) error {
	requiredMissing := false
	requiredPresent := false
	authOptionalPresent := false
	var firstMissingRequired string
	for _, spec := range fileSet.Files {
		srcPath := filepath.Join(profileDir, filepath.Base(spec.Path))
		st, err := os.Stat(srcPath)
		if err != nil {
			if os.IsNotExist(err) {
				if spec.Required {
					requiredMissing = true
					if firstMissingRequired == "" {
						firstMissingRequired = srcPath
					}
				}
				continue
			}
			return fmt.Errorf("stat backup %s: %w", srcPath, err)
		}
		if !st.Mode().IsRegular() {
			return fmt.Errorf("backup path is not a regular file: %s", srcPath)
		}
		if spec.Required {
			requiredPresent = true
			continue
		}
		if isClaudeDesktopConfig(fileSet.Tool, spec.Path) {
			if _, ok, err := claudeDesktopTokenCache(srcPath); err == nil && ok {
				authOptionalPresent = true
			}
			continue
		}
		if optionalFileCountsAsAuth(fileSet.Tool, spec.Path, srcPath) {
			authOptionalPresent = true
		}
	}
	if requiredMissing && !(fileSet.AllowOptionalOnly && !requiredPresent && authOptionalPresent) {
		return fmt.Errorf("required backup not found: %s", firstMissingRequired)
	}
	return nil
}

func managedStateForRestore(fileSet AuthFileSet, manifest map[string]managedFileState, spec AuthFileSpec) (managedFileState, bool) {
	filename := filepath.Base(spec.Path)
	targetPath := filepath.Clean(spec.Path)
	for _, candidate := range manifest {
		if filepath.Clean(candidate.OriginalPath) == targetPath {
			return candidate, true
		}
	}
	if fileSet.Tool == "gemini" && filename == "oauth_creds.json" {
		legacyState, ok := manifest["oauth_credentials.json"]
		if ok && geminiLegacyOAuthPathMatches(legacyState.OriginalPath, spec.Path) {
			legacyState.OriginalPath = spec.Path
			return legacyState, true
		}
	}
	state, ok := manifest[filename]
	if ok {
		return state, true
	}
	return managedFileState{}, false
}

func geminiHasDuplicateOAuthAlias(fileSet AuthFileSet, manifest map[string]managedFileState, spec AuthFileSpec) bool {
	if fileSet.Tool != "gemini" || filepath.Base(spec.Path) != "oauth_creds.json" {
		return false
	}
	current, hasCurrent := manifest["oauth_creds.json"]
	legacy, hasLegacy := manifest["oauth_credentials.json"]
	return hasCurrent &&
		hasLegacy &&
		filepath.Clean(current.OriginalPath) == filepath.Clean(spec.Path) &&
		geminiLegacyOAuthPathMatches(legacy.OriginalPath, spec.Path)
}

func geminiLegacyOAuthPathMatches(legacyPath, currentPath string) bool {
	legacyPath = filepath.Clean(legacyPath)
	currentPath = filepath.Clean(currentPath)
	return filepath.Base(legacyPath) == "oauth_credentials.json" &&
		filepath.Base(currentPath) == "oauth_creds.json" &&
		filepath.Dir(legacyPath) == filepath.Dir(currentPath)
}

func optionalFileCountsAsAuth(tool, specPath, contentPath string) bool {
	if tool != "claude" {
		return true
	}
	if filepath.Base(specPath) == ".claude.json" {
		return false
	}
	if filepath.Base(specPath) == "settings.json" && filepath.Base(filepath.Dir(specPath)) == ".claude" {
		return claudeSettingsHasAPIKeyHelper(contentPath)
	}
	return true
}

func claudeSettingsHasAPIKeyHelper(path string) bool {
	data, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	var root map[string]interface{}
	if err := json.Unmarshal(data, &root); err != nil {
		return false
	}
	v, exists := root["apiKeyHelper"]
	return exists && nonEmptyIdentityValue(v)
}

func authPathSharedWithOtherProvider(tool, path string) bool {
	target := filepath.Clean(path)
	for _, candidate := range []AuthFileSet{
		ClaudeAuthFiles(),
		CodexAuthFiles(),
		GeminiAuthFiles(),
		AntigravityAuthFiles(),
		OpenCodeAuthFiles(),
		CursorAuthFiles(),
	} {
		if candidate.Tool == tool {
			continue
		}
		for _, spec := range candidate.Files {
			if filepath.Clean(spec.Path) == target {
				return true
			}
		}
	}
	return false
}

func (v *Vault) cleanupAbsentManagedLive(profileDir string, fileSet AuthFileSet, spec AuthFileSpec, state managedFileState) error {
	if authPathSharedWithOtherProvider(fileSet.Tool, spec.Path) {
		return nil
	}
	managedLive, err := v.livePathMatchesVaultSnapshot(fileSet, spec)
	if err != nil {
		return err
	}
	if !managedLive {
		return nil
	}
	if state.ClaudeDesktopTokenCache || isClaudeDesktopConfig(fileSet.Tool, spec.Path) {
		if _, err := v.quarantineClaudeDesktopTokenCache(profileDir, "restore", spec.Path); err != nil {
			return fmt.Errorf("quarantine stale Claude desktop token cache %s: %w", spec.Path, err)
		}
		return nil
	}
	if _, err := v.quarantineManagedPath(profileDir, "restore", spec.Path, spec.Path); err != nil {
		return fmt.Errorf("quarantine stale live auth file %s: %w", spec.Path, err)
	}
	return nil
}

func (v *Vault) livePathMatchesVaultSnapshot(fileSet AuthFileSet, spec AuthFileSpec) (bool, error) {
	st, err := os.Lstat(spec.Path)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, fmt.Errorf("stat live auth path %s: %w", spec.Path, err)
	}
	if !st.Mode().IsRegular() {
		return false, fmt.Errorf("managed auth path is not a regular file: %s", spec.Path)
	}
	liveHash, ok, err := managedCleanupHash(fileSet.Tool, spec.Path, isClaudeDesktopConfig(fileSet.Tool, spec.Path))
	if err != nil || !ok {
		return false, nil
	}
	profiles, err := v.List(fileSet.Tool)
	if err != nil {
		return false, err
	}
	targetPath := filepath.Clean(spec.Path)
	for _, profile := range profiles {
		profileDir := v.ProfilePath(fileSet.Tool, profile)
		manifest, ok, err := v.managedFileManifest(profileDir)
		if err != nil {
			continue
		}
		if !ok {
			continue
		}
		state, stateKnown := managedStateForRestore(fileSet, manifest, spec)
		if !stateKnown || !state.Present || filepath.Clean(state.OriginalPath) != targetPath {
			continue
		}
		backupPath := filepath.Join(profileDir, state.VaultName)
		if _, err := os.Stat(backupPath); err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return false, fmt.Errorf("stat vault auth file %s: %w", backupPath, err)
		}
		backupHash, ok, err := managedCleanupHash(fileSet.Tool, backupPath, state.ClaudeDesktopTokenCache)
		if err != nil || !ok {
			continue
		}
		if liveHash == backupHash {
			return true, nil
		}
	}
	return false, nil
}

func managedCleanupHash(tool, path string, useClaudeDesktopTokenCache bool) (string, bool, error) {
	if useClaudeDesktopTokenCache || isClaudeDesktopConfig(tool, path) {
		fields, ok, err := claudeDesktopTokenCache(path)
		if err != nil || !ok {
			return "", false, err
		}
		canonical, err := json.Marshal(fields)
		if err != nil {
			return "", false, err
		}
		h := sha256.New()
		h.Write([]byte("claude:desktop:"))
		h.Write(canonical)
		return hex.EncodeToString(h.Sum(nil)), true, nil
	}
	hash, err := hashFile(path)
	return hash, err == nil, err
}

func (v *Vault) copyManagedPathToQuarantine(profileDir, reason, originalPath, path string) (bool, error) {
	st, err := os.Lstat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	if !st.Mode().IsRegular() {
		return false, fmt.Errorf("managed auth path is not a regular file: %s", path)
	}
	dst, err := quarantineDestination(profileDir, reason, originalPath)
	if err != nil {
		return false, err
	}
	if err := copyFile(path, dst); err != nil {
		return false, err
	}
	return true, nil
}

func (v *Vault) quarantineManagedPath(profileDir, reason, originalPath, path string) (bool, error) {
	st, err := os.Lstat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	if !st.Mode().IsRegular() {
		return false, fmt.Errorf("managed auth path is not a regular file: %s", path)
	}
	dst, err := quarantineDestination(profileDir, reason, originalPath)
	if err != nil {
		return false, err
	}
	if err := os.Rename(path, dst); err != nil {
		if !errors.Is(err, syscall.EXDEV) {
			return false, err
		}
		sourceHash, err := hashFile(path)
		if err != nil {
			return false, err
		}
		if err := copyFile(path, dst); err != nil {
			return false, err
		}
		dstHash, err := hashFile(dst)
		if err != nil {
			return false, err
		}
		if dstHash != sourceHash {
			return false, fmt.Errorf("quarantine copy hash mismatch for %s", path)
		}
		current, err := os.Lstat(path)
		if err != nil {
			return false, err
		}
		currentHash, err := hashFile(path)
		if err != nil {
			return false, err
		}
		if !os.SameFile(st, current) || currentHash != sourceHash {
			return false, fmt.Errorf("source changed while quarantining %s", path)
		}
		if err := os.Remove(path); err != nil {
			return false, fmt.Errorf("remove quarantined source %s: %w", path, err)
		}
	}
	return true, nil
}

func (v *Vault) quarantineClaudeDesktopTokenCache(profileDir, reason, livePath string) (bool, error) {
	fields, ok, err := claudeDesktopTokenCache(livePath)
	if err != nil {
		return false, err
	}
	if !ok {
		return false, nil
	}
	dst, err := quarantineDestination(profileDir, reason, livePath)
	if err != nil {
		return false, err
	}
	if err := writeJSONFileAtomic(dst, fields, 0600); err != nil {
		return false, err
	}
	if err := scrubClaudeDesktopTokenCache(livePath); err != nil {
		return false, err
	}
	return true, nil
}

func quarantineDestination(profileDir, reason, originalPath string) (string, error) {
	reason = strings.TrimSpace(reason)
	if reason == "" {
		reason = "managed"
	}
	stamp := time.Now().UTC().Format("20060102T150405.000000000Z")
	dir := filepath.Join(profileDir, quarantineDirName, reason, stamp)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return "", err
	}
	base := quarantineFileName(originalPath)
	dst := filepath.Join(dir, base)
	if _, err := os.Lstat(dst); os.IsNotExist(err) {
		return dst, nil
	} else if err != nil {
		return "", err
	}
	for i := 1; ; i++ {
		candidate := filepath.Join(dir, fmt.Sprintf("%d-%s", i, base))
		if _, err := os.Lstat(candidate); os.IsNotExist(err) {
			return candidate, nil
		} else if err != nil {
			return "", err
		}
	}
}

func quarantineFileName(originalPath string) string {
	cleaned := filepath.Clean(originalPath)
	sum := sha256.Sum256([]byte(cleaned))
	prefix := hex.EncodeToString(sum[:8])
	base := filepath.Base(cleaned)
	if base == "." || base == string(os.PathSeparator) || base == "" {
		base = "managed-auth-file"
	}
	return prefix + "-" + base
}

// writeJSONFileAtomic marshals v (indented) and writes it to path atomically.
func writeJSONFileAtomic(path string, v interface{}, perm os.FileMode) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, filepath.Base(path)+".tmp.*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err := f.Chmod(perm); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// Helper functions

func copyFile(src, dst string) error {
	// Ensure parent directory exists
	dir := filepath.Dir(dst)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}

	srcFile, err := os.Open(src)
	if err != nil {
		return err
	}
	defer srcFile.Close()

	// Create temp file for atomic write using CreateTemp to avoid races
	// Pattern: filename.tmp.RANDOM
	dstFile, err := os.CreateTemp(dir, filepath.Base(dst)+".tmp.*")
	if err != nil {
		return err
	}
	tmpPath := dstFile.Name()

	// Ensure cleanup of temp file if something goes wrong.
	// If rename succeeds, this removal will fail (which is fine).
	defer os.Remove(tmpPath)

	if _, err := io.Copy(dstFile, srcFile); err != nil {
		dstFile.Close()
		return err
	}

	// Enforce 0600 permissions for all auth files
	if err := dstFile.Chmod(0600); err != nil {
		dstFile.Close()
		return err
	}

	if err := dstFile.Sync(); err != nil {
		dstFile.Close()
		return err
	}

	if err := dstFile.Close(); err != nil {
		return err
	}

	// Atomic rename
	return os.Rename(tmpPath, dst)
}

func hashFile(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}

	return hex.EncodeToString(h.Sum(nil)), nil
}

// hashBytes returns a SHA-256 hex digest of raw bytes. Used as a fallback
// when identity extraction fails but we already have the file data in memory,
// avoiding a second disk read (TOCTOU race) that hashFile would require.
func hashBytes(data []byte) string {
	h := sha256.New()
	h.Write(data)
	return hex.EncodeToString(h.Sum(nil))
}

// stableFileHash returns a hash of only the identity-bearing fields in an auth
// file, ignoring volatile metadata that tools write after activation. This
// prevents profile detection from breaking when tools modify non-auth fields.
//
// Falls back to whole-file hashing if identity extraction fails or is not
// implemented for the given tool.
func stableFileHash(tool, path string) (string, error) {
	switch tool {
	case "claude":
		return stableClaudeHash(path)
	case "codex":
		return stableCodexHash(path)
	default:
		return hashFile(path)
	}
}

func stableFileHashes(tool, path string) ([]string, error) {
	hash, err := stableFileHash(tool, path)
	if err != nil {
		return nil, err
	}
	hashes := []string{hash}

	if tool == "claude" && filepath.Base(path) == ".credentials.json" {
		accessHash, err := hashClaudeCredentialKey(path, "accessToken")
		if err == nil && accessHash != "" && accessHash != hash {
			hashes = append(hashes, accessHash)
		}
	}

	return hashes, nil
}

func hashesOverlap(a, b []string) bool {
	if len(a) == 0 || len(b) == 0 {
		return false
	}
	seen := make(map[string]struct{}, len(a))
	for _, hash := range a {
		if hash != "" {
			seen[hash] = struct{}{}
		}
	}
	for _, hash := range b {
		if _, ok := seen[hash]; ok {
			return true
		}
	}
	return false
}

// stableClaudeHash extracts identity-bearing fields from Claude auth files and
// hashes only those fields. This handles two file types:
//
//   - .credentials.json: contains claudeAiOauth with accessToken and
//     refreshToken (the actual auth identity)
//   - .claude.json: settings file with oauthAccount (identity) mixed with
//     volatile fields like changelogLastFetched, numStartups, tipsHistory
//
// For .credentials.json, we hash the refreshToken when present, falling back to
// accessToken for legacy files that do not expose a refresh token.
// For .claude.json, we hash the oauthAccount field only.
// For other files (settings.json, auth.json), we fall back to whole-file hash.
func stableClaudeHash(path string) (string, error) {
	base := filepath.Base(path)

	switch base {
	case ".credentials.json":
		return hashClaudeCredentials(path)
	case ".claude.json":
		return hashClaudeSettings(path)
	case "config.json":
		// The Claude Desktop config.json: hash only its oauth:tokenCache* fields.
		return hashClaudeDesktopConfig(path)
	default:
		return hashFile(path)
	}
}

// hashClaudeCredentials hashes the identity-bearing fields from Claude's
// .credentials.json. Claude access tokens rotate, so refreshToken is the stable
// identity when present. Legacy credentials without refreshToken fall back to
// accessToken. Volatile fields like expiresAt are excluded since they change on
// token refresh without changing the account identity.
func hashClaudeCredentials(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}

	var root map[string]interface{}
	if err := json.Unmarshal(data, &root); err != nil {
		// Not valid JSON; fall back to hashing the bytes we already read
		return hashBytes(data), nil
	}

	oauth, ok := root["claudeAiOauth"].(map[string]interface{})
	if !ok {
		// No claudeAiOauth section; fall back to hashing the bytes we already read
		return hashBytes(data), nil
	}

	// Prefer refreshToken because accessToken rotates. Keep accessToken as a
	// fallback for older or partial credential files.
	identityFields := map[string]interface{}{}
	if v, exists := oauth["refreshToken"]; exists && nonEmptyIdentityValue(v) {
		identityFields["refreshToken"] = v
	} else if v, exists := oauth["accessToken"]; exists && nonEmptyIdentityValue(v) {
		identityFields["accessToken"] = v
	}

	if len(identityFields) == 0 {
		return hashBytes(data), nil
	}

	// Deterministic JSON serialization for hashing
	canonical, err := json.Marshal(identityFields)
	if err != nil {
		return hashBytes(data), nil
	}

	h := sha256.New()
	h.Write([]byte("claude:credentials:"))
	h.Write(canonical)
	return hex.EncodeToString(h.Sum(nil)), nil
}

func hashClaudeCredentialKey(path string, key string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}

	var root map[string]interface{}
	if err := json.Unmarshal(data, &root); err != nil {
		return "", nil
	}

	oauth, ok := root["claudeAiOauth"].(map[string]interface{})
	if !ok {
		return "", nil
	}

	v, exists := oauth[key]
	if !exists || !nonEmptyIdentityValue(v) {
		return "", nil
	}

	canonical, err := json.Marshal(map[string]interface{}{key: v})
	if err != nil {
		return "", nil
	}

	h := sha256.New()
	h.Write([]byte("claude:credentials:"))
	h.Write(canonical)
	return hex.EncodeToString(h.Sum(nil)), nil
}

func nonEmptyIdentityValue(v interface{}) bool {
	if v == nil {
		return false
	}
	if s, ok := v.(string); ok {
		return s != ""
	}
	return true
}

// hashClaudeSettings hashes only the identity-bearing oauthAccount field from
// Claude's .claude.json settings file. This file contains many volatile fields
// (changelogLastFetched, numStartups, tipsHistory, etc.) that change
// frequently and would break profile detection if included in the hash.
func hashClaudeSettings(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}

	var root map[string]interface{}
	if err := json.Unmarshal(data, &root); err != nil {
		return hashBytes(data), nil
	}

	// oauthAccount is the identity-bearing field in .claude.json.
	// All other top-level fields are volatile session/UI state.
	identityFields := map[string]interface{}{}
	for _, key := range []string{"oauthAccount", "userID"} {
		if v, exists := root[key]; exists {
			identityFields[key] = v
		}
	}

	if len(identityFields) == 0 {
		// No identity fields found; the file is purely volatile settings.
		// Return a fixed sentinel hash so all settings-only files match,
		// preventing settings drift from breaking profile detection.
		h := sha256.New()
		h.Write([]byte("claude:settings:no-identity"))
		return hex.EncodeToString(h.Sum(nil)), nil
	}

	canonical, err := json.Marshal(identityFields)
	if err != nil {
		return hashBytes(data), nil
	}

	h := sha256.New()
	h.Write([]byte("claude:settings:"))
	h.Write(canonical)
	return hex.EncodeToString(h.Sum(nil)), nil
}

// stableCodexHash extracts a stable identity from Codex auth files by parsing
// JWT tokens and hashing the identity claims (email, account_id, organization).
// This solves the dedup problem where multiple named profiles with different
// token strings (due to refresh) actually represent the same OpenAI account.
//
// Falls back to whole-file hash if JWT parsing fails.
func stableCodexHash(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}

	var auth map[string]interface{}
	if err := json.Unmarshal(data, &auth); err != nil {
		return hashBytes(data), nil
	}

	// Try to extract stable identity from JWT tokens.
	// Codex stores tokens in various fields; check all candidates.
	identity := extractCodexIdentity(auth)
	if identity != "" {
		h := sha256.New()
		h.Write([]byte("codex:identity:"))
		h.Write([]byte(identity))
		return hex.EncodeToString(h.Sum(nil)), nil
	}

	// JWT parsing failed; fall back to hashing bytes we already read
	return hashBytes(data), nil
}

// extractCodexIdentity extracts a stable identity string from Codex auth data
// by decoding JWT tokens and extracting email/account claims. Returns empty
// string if no identity can be determined.
func extractCodexIdentity(auth map[string]interface{}) string {
	// Ordered by preference: id_token has richer claims than access_token
	tokenFields := []string{"id_token", "idToken"}
	nestedTokenFields := []string{"id_token", "idToken", "access_token", "accessToken"}

	// Check top-level token fields
	for _, field := range tokenFields {
		if token := jsonString(auth, field); token != "" {
			if id := identityFromJWT(token); id != "" {
				return id
			}
		}
	}

	// Check nested tokens object
	if tokens, ok := auth["tokens"].(map[string]interface{}); ok {
		for _, field := range nestedTokenFields {
			if token := jsonString(tokens, field); token != "" {
				if id := identityFromJWT(token); id != "" {
					return id
				}
			}
		}
	}

	// Check top-level access tokens as last resort
	for _, field := range []string{"access_token", "accessToken", "token"} {
		if token := jsonString(auth, field); token != "" {
			if id := identityFromJWT(token); id != "" {
				return id
			}
		}
	}

	return ""
}

// identityFromJWT decodes a JWT token (without signature verification) and
// extracts a stable identity string from its claims. Returns empty string if
// the token is not a valid JWT or contains no identity claims.
func identityFromJWT(token string) string {
	parts := strings.Split(token, ".")
	if len(parts) != 3 || parts[1] == "" {
		return ""
	}

	// Decode the payload segment
	payload, err := decodeBase64Segment(parts[1])
	if err != nil {
		return ""
	}

	var claims map[string]interface{}
	if err := json.Unmarshal(payload, &claims); err != nil {
		return ""
	}

	// Build stable identity from claims, checking known namespaces too.
	// OpenAI/Codex tokens nest some claims under "https://api.openai.com/auth".
	claimMaps := []map[string]interface{}{claims}
	for _, ns := range []string{"https://api.openai.com/auth", "https://api.openai.com/profile"} {
		if nested, ok := claims[ns].(map[string]interface{}); ok {
			claimMaps = append(claimMaps, nested)
		}
	}

	var email, accountID, org string
	for _, m := range claimMaps {
		if email == "" {
			for _, key := range []string{"email", "preferred_username", "upn"} {
				if v := jsonString(m, key); v != "" {
					email = v
					break
				}
			}
		}
		if accountID == "" {
			for _, key := range []string{"sub", "account_id", "accountId", "user_id", "userId"} {
				if v := jsonString(m, key); v != "" {
					accountID = v
					break
				}
			}
		}
		if org == "" {
			for _, key := range []string{"organization", "org", "org_name"} {
				if v := jsonString(m, key); v != "" {
					org = v
					break
				}
			}
		}
	}

	// Build a canonical identity string from whatever we found.
	// At minimum we need email or accountID to have a useful identity.
	if email == "" && accountID == "" {
		return ""
	}

	// Deterministic format: "email|accountID|org"
	return email + "|" + accountID + "|" + org
}

// decodeBase64Segment decodes a base64url-encoded JWT segment, handling
// missing padding.
func decodeBase64Segment(s string) ([]byte, error) {
	// Add padding if needed
	switch len(s) % 4 {
	case 2:
		s += "=="
	case 3:
		s += "="
	}

	// Try URL encoding first (standard for JWTs), then standard encoding
	if decoded, err := base64DecodeURL(s); err == nil {
		return decoded, nil
	}
	return base64DecodeStd(s)
}

// jsonString extracts a string value from a map, returning empty string if
// the key doesn't exist or isn't a string.
func jsonString(m map[string]interface{}, key string) string {
	if m == nil {
		return ""
	}
	v, ok := m[key]
	if !ok {
		return ""
	}
	s, ok := v.(string)
	if !ok {
		return ""
	}
	return s
}

func base64DecodeURL(s string) ([]byte, error) {
	return base64.URLEncoding.DecodeString(s)
}

func base64DecodeStd(s string) ([]byte, error) {
	return base64.StdEncoding.DecodeString(s)
}

func (v *Vault) safeToolDir(tool string) (string, error) {
	if v == nil || strings.TrimSpace(v.basePath) == "" {
		return "", fmt.Errorf("vault base path is empty")
	}
	tool, err := validateVaultSegment("tool", tool)
	if err != nil {
		return "", err
	}

	baseAbs, err := filepath.Abs(v.basePath)
	if err != nil {
		return "", fmt.Errorf("vault base absolute path: %w", err)
	}

	return filepath.Join(baseAbs, tool), nil
}

func (v *Vault) safeProfileDir(tool, profile string) (string, error) {
	if v == nil || strings.TrimSpace(v.basePath) == "" {
		return "", fmt.Errorf("vault base path is empty")
	}
	tool, err := validateVaultSegment("tool", tool)
	if err != nil {
		return "", err
	}
	profile, err = validateVaultSegment("profile", profile)
	if err != nil {
		return "", err
	}

	baseAbs, err := filepath.Abs(v.basePath)
	if err != nil {
		return "", fmt.Errorf("vault base absolute path: %w", err)
	}

	full := filepath.Join(baseAbs, tool, profile)
	fullAbs, err := filepath.Abs(full)
	if err != nil {
		return "", fmt.Errorf("vault profile absolute path: %w", err)
	}

	baseAbs = filepath.Clean(baseAbs)
	if fullAbs != baseAbs && !strings.HasPrefix(fullAbs, baseAbs+string(os.PathSeparator)) {
		return "", fmt.Errorf("vault profile path escapes base directory")
	}

	return fullAbs, nil
}

func validateVaultSegment(kind, val string) (string, error) {
	val = strings.TrimSpace(val)
	if val == "" {
		return "", fmt.Errorf("%s cannot be empty", kind)
	}
	if val == "." || val == ".." {
		return "", fmt.Errorf("invalid %s: %q", kind, val)
	}
	if strings.HasPrefix(val, ".") {
		return "", fmt.Errorf("invalid %s: %q (leading period is reserved for caam internal directories)", kind, val)
	}
	// Only allow safe characters: alphanumeric, underscore, hyphen, period, and @.
	// This prevents shell injection when profile names are used in shell scripts
	// (e.g., claude.go's setupAPIKeyHelper embeds profile name in bash script).
	// The @ and + characters are safe (no special shell meaning) and useful for email-based profile names.
	// Also prevents filesystem issues and unexpected behavior.
	for _, r := range val {
		if !((r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') ||
			(r >= '0' && r <= '9') || r == '_' || r == '-' || r == '.' || r == '@' || r == '+') {
			return "", fmt.Errorf("invalid %s: %q (only alphanumeric, underscore, hyphen, period, @, and + allowed)", kind, val)
		}
	}
	if filepath.IsAbs(val) || filepath.VolumeName(val) != "" {
		return "", fmt.Errorf("invalid %s: %q", kind, val)
	}

	return val, nil
}

// ProfileIdentity returns a human-readable identity string for a vault profile
// by reading its auth files and extracting identity-bearing claims (e.g., email,
// account ID). Returns empty string if the identity cannot be determined.
//
// This is used by the doctor command to detect when a named profile and a
// system/backup profile share the same underlying account.
func (v *Vault) ProfileIdentity(tool, profile string) string {
	profileDir := v.ProfilePath(tool, profile)

	switch tool {
	case "codex":
		return v.codexProfileIdentity(profileDir)
	case "claude":
		return v.claudeProfileIdentity(profileDir)
	case "gemini":
		return v.geminiProfileIdentity(profileDir)
	case "agy":
		return v.agyProfileIdentity(profileDir)
	default:
		return ""
	}
}

// agyProfileIdentity extracts the human-readable identity (active Google account
// email) from an Antigravity (agy) vault profile by reading google_accounts.json.
// It never reads the antigravity-oauth-token bytes.
func (v *Vault) agyProfileIdentity(profileDir string) string {
	accountsPath := filepath.Join(profileDir, "google_accounts.json")
	data, err := os.ReadFile(accountsPath)
	if err != nil {
		return ""
	}
	var parsed struct {
		Active string `json:"active"`
	}
	if err := json.Unmarshal(data, &parsed); err != nil {
		return ""
	}
	return parsed.Active
}

// codexProfileIdentity extracts identity from a Codex vault profile by parsing
// JWT tokens in auth.json and extracting email/account claims.
func (v *Vault) codexProfileIdentity(profileDir string) string {
	authPath := filepath.Join(profileDir, "auth.json")
	data, err := os.ReadFile(authPath)
	if err != nil {
		return ""
	}

	var auth map[string]interface{}
	if err := json.Unmarshal(data, &auth); err != nil {
		return ""
	}

	return extractCodexIdentity(auth)
}

// codexFreshness returns a "how recently were these tokens refreshed" timestamp
// for a Codex auth.json blob, and whether a usable timestamp was found.
//
// ChatGPT/Codex OAuth uses refresh-token rotation with reuse detection: each
// refresh mints a new refresh_token and invalidates the previous one. The live
// ~/.codex/auth.json is therefore the authoritative copy of the current token
// family; a vault snapshot taken before a rotation holds an already-consumed
// refresh_token. Presenting that stale token revokes the whole family and
// bricks the account until interactive re-login.
//
// We derive freshness from, in order of preference:
//  1. the top-level "last_refresh" field that the Codex CLI writes (RFC3339), and
//  2. the maximum JWT "iat" (issued-at) claim across the id_token/access_token.
//
// Returns (zeroTime, false) when no timestamp can be parsed, so callers treat
// an unparseable file as "unknown" and fall back to the normal copy.
func codexFreshness(data []byte) (time.Time, bool) {
	var auth map[string]interface{}
	if err := json.Unmarshal(data, &auth); err != nil {
		return time.Time{}, false
	}

	best := time.Time{}
	found := false

	// 1. Top-level last_refresh (the field the Codex CLI updates on rotation).
	if ts := jsonString(auth, "last_refresh"); ts != "" {
		if t, err := time.Parse(time.RFC3339, ts); err == nil {
			best = t
			found = true
		} else if t, err := time.Parse(time.RFC3339Nano, ts); err == nil {
			best = t
			found = true
		}
	}

	// 2. JWT iat claims from whatever tokens are present.
	tokenSources := []map[string]interface{}{auth}
	if tokens, ok := auth["tokens"].(map[string]interface{}); ok {
		tokenSources = append(tokenSources, tokens)
	}
	for _, src := range tokenSources {
		for _, field := range []string{"id_token", "idToken", "access_token", "accessToken"} {
			token := jsonString(src, field)
			if token == "" {
				continue
			}
			if iat, ok := jwtIssuedAt(token); ok {
				if !found || iat.After(best) {
					best = iat
					found = true
				}
			}
		}
	}

	return best, found
}

// jwtIssuedAt decodes a JWT (without signature verification) and returns its
// "iat" (issued-at) claim as a time.Time. Returns (zeroTime, false) if the
// token is malformed or has no numeric iat claim.
func jwtIssuedAt(token string) (time.Time, bool) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 || parts[1] == "" {
		return time.Time{}, false
	}
	payload, err := decodeBase64Segment(parts[1])
	if err != nil {
		return time.Time{}, false
	}
	var claims map[string]interface{}
	if err := json.Unmarshal(payload, &claims); err != nil {
		return time.Time{}, false
	}
	iat, ok := claims["iat"]
	if !ok {
		return time.Time{}, false
	}
	// JSON numbers decode to float64; some encoders may emit a string.
	switch v := iat.(type) {
	case float64:
		return time.Unix(int64(v), 0).UTC(), true
	case json.Number:
		if n, err := v.Int64(); err == nil {
			return time.Unix(n, 0).UTC(), true
		}
	case string:
		if n, err := time.Parse(time.RFC3339, v); err == nil {
			return n, true
		}
	}
	return time.Time{}, false
}

// codexLiveIsNewer reports whether the LIVE codex auth file at livePath holds
// the same OpenAI identity as the incoming vault snapshot AND was refreshed
// strictly more recently. When true, the restore path must NOT clobber the live
// file: doing so would replay an already-rotated (consumed) refresh_token and
// trip the IdP's reuse detection, revoking the whole token family.
//
// Conservative by construction: any uncertainty (missing/unreadable live file,
// different identity, equal-or-older live timestamp, or an unparseable
// timestamp on either side) returns false so the normal verbatim copy proceeds.
// Real cross-account switches (different identity) and first-time restores
// (no live file) are therefore never blocked.
func codexLiveIsNewer(livePath, snapshotPath string) bool {
	liveData, err := os.ReadFile(livePath)
	if err != nil {
		return false // no live file (or unreadable) -> safe to copy
	}
	snapData, err := os.ReadFile(snapshotPath)
	if err != nil {
		return false // no snapshot -> nothing to compare; let copy fail/handle
	}

	// Only guard when it is unambiguously the SAME account. A different identity
	// is a genuine switch and must overwrite.
	var liveAuth, snapAuth map[string]interface{}
	if json.Unmarshal(liveData, &liveAuth) != nil || json.Unmarshal(snapData, &snapAuth) != nil {
		return false
	}
	liveID := extractCodexIdentity(liveAuth)
	snapID := extractCodexIdentity(snapAuth)
	if liveID == "" || snapID == "" || liveID != snapID {
		return false
	}

	liveTS, liveOK := codexFreshness(liveData)
	snapTS, snapOK := codexFreshness(snapData)
	if !liveOK || !snapOK {
		return false // can't compare -> normal copy
	}

	// Preserve the live file only when it is STRICTLY newer.
	return liveTS.After(snapTS)
}

// claudeProfileIdentity extracts identity from a Claude vault profile.
// It checks .claude.json for the oauthAccount field (typically email),
// then falls back to .credentials.json JWT parsing.
func (v *Vault) claudeProfileIdentity(profileDir string) string {
	// Try .claude.json first -- has oauthAccount field
	settingsPath := filepath.Join(profileDir, ".claude.json")
	if data, err := os.ReadFile(settingsPath); err == nil {
		var root map[string]interface{}
		if err := json.Unmarshal(data, &root); err == nil {
			if acct := jsonString(root, "oauthAccount"); acct != "" {
				return acct
			}
			if uid := jsonString(root, "userID"); uid != "" {
				return uid
			}
		}
	}

	// Try .credentials.json -- parse JWT from claudeAiOauth.accessToken
	credsPath := filepath.Join(profileDir, ".credentials.json")
	if data, err := os.ReadFile(credsPath); err == nil {
		var root map[string]interface{}
		if err := json.Unmarshal(data, &root); err == nil {
			if oauth, ok := root["claudeAiOauth"].(map[string]interface{}); ok {
				// Try to extract identity from the access token JWT
				for _, key := range []string{"accessToken", "idToken"} {
					if token := jsonString(oauth, key); token != "" {
						if id := identityFromJWT(token); id != "" {
							return id
						}
					}
				}
			}
		}
	}

	return ""
}

// geminiProfileIdentity extracts identity from a Gemini vault profile by
// reading settings.json or oauth_creds.json for email/account information.
func (v *Vault) geminiProfileIdentity(profileDir string) string {
	// Try settings.json
	settingsPath := filepath.Join(profileDir, "settings.json")
	if data, err := os.ReadFile(settingsPath); err == nil {
		var root map[string]interface{}
		if err := json.Unmarshal(data, &root); err == nil {
			// Gemini stores identity in various fields depending on version
			for _, key := range []string{"email", "account", "user_email"} {
				if val := jsonString(root, key); val != "" {
					return val
				}
			}
			// Check nested auth object
			if auth, ok := root["auth"].(map[string]interface{}); ok {
				for _, key := range []string{"email", "account"} {
					if val := jsonString(auth, key); val != "" {
						return val
					}
				}
			}
		}
	}

	// Try oauth_creds.json
	credsPath := filepath.Join(profileDir, "oauth_creds.json")
	if data, err := os.ReadFile(credsPath); err == nil {
		var root map[string]interface{}
		if err := json.Unmarshal(data, &root); err == nil {
			for _, key := range []string{"email", "account", "client_email"} {
				if val := jsonString(root, key); val != "" {
					return val
				}
			}
			// Check for JWT id_token
			if token := jsonString(root, "id_token"); token != "" {
				if id := identityFromJWT(token); id != "" {
					return id
				}
			}
		}
	}

	return ""
}
