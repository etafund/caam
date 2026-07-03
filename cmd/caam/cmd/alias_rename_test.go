package cmd

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/Dicklesworthstone/coding_agent_account_manager/internal/authfile"
	"github.com/Dicklesworthstone/coding_agent_account_manager/internal/config"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"
)

func TestAliasCommandCreateListRemoveAndConflict(t *testing.T) {
	testVault := setupAliasRenameCommandTest(t)
	writeVaultProfile(t, testVault, "codex", "work-account", `{"fixture_profile":"work"}`)
	writeVaultProfile(t, testVault, "codex", "personal-account", `{"fixture_profile":"personal"}`)

	createCmd := newAliasCommandForTest(t)
	require.NoError(t, createCmd.Flags().Set("json", "true"))
	createOut, err := captureStdout(t, func() error {
		return runAlias(createCmd, []string{"codex", "work-account", "work"})
	})
	require.NoError(t, err)
	require.JSONEq(t, `{"alias":"work","profile":"work-account","status":"created","tool":"codex"}`, createOut)

	saved, err := config.Load()
	require.NoError(t, err)
	require.Equal(t, []string{"work"}, saved.GetAliases("codex", "work-account"))
	t.Logf("alias mapping after create: %s -> %s/%s", "work", "codex", saved.ResolveAliasForProvider("codex", "work"))

	listCmd := newAliasCommandForTest(t)
	require.NoError(t, listCmd.Flags().Set("list", "true"))
	require.NoError(t, listCmd.Flags().Set("json", "true"))
	listOut, err := captureStdout(t, func() error {
		return runAlias(listCmd, nil)
	})
	require.NoError(t, err)
	var listed map[string][]string
	require.NoError(t, json.Unmarshal([]byte(listOut), &listed))
	require.Equal(t, map[string][]string{"codex/work-account": {"work"}}, listed)

	conflictCmd := newAliasCommandForTest(t)
	err = runAlias(conflictCmd, []string{"codex", "personal-account", "work"})
	require.Error(t, err)
	require.Contains(t, err.Error(), `alias "work" already used for codex/work-account`)

	afterConflict, err := config.Load()
	require.NoError(t, err)
	require.Equal(t, []string{"work"}, afterConflict.GetAliases("codex", "work-account"))
	require.Empty(t, afterConflict.GetAliases("codex", "personal-account"))
	t.Logf("alias mapping after conflict: %s -> %s/%s", "work", "codex", afterConflict.ResolveAliasForProvider("codex", "work"))

	removeCmd := newAliasCommandForTest(t)
	require.NoError(t, removeCmd.Flags().Set("remove", "work"))
	require.NoError(t, removeCmd.Flags().Set("json", "true"))
	removeOut, err := captureStdout(t, func() error {
		return runAlias(removeCmd, nil)
	})
	require.NoError(t, err)
	require.JSONEq(t, `{"removed":"work","success":true}`, removeOut)

	afterRemove, err := config.Load()
	require.NoError(t, err)
	require.Empty(t, afterRemove.ResolveAliasForProvider("codex", "work"))
	t.Logf("alias mapping after remove: %s -> %q", "work", afterRemove.ResolveAliasForProvider("codex", "work"))
}

func TestResolveProfileNameUsesAliasMapping(t *testing.T) {
	setupAliasRenameCommandTest(t)
	cfg := config.DefaultConfig()
	cfg.AddAlias("codex", "work-account", "work")
	require.NoError(t, cfg.Save())

	out, err := captureStdout(t, func() error {
		got := resolveProfileName("codex", "work", []string{"work-account", "personal-account"}, false)
		require.Equal(t, "work-account", got)
		return nil
	})
	require.NoError(t, err)
	require.Contains(t, out, "Using alias: work -> work-account")
	t.Logf("activation alias resolution: %s -> %s/%s", "work", "codex", "work-account")
}

func TestRenameCopiesProfilePreservesOldAndMigratesAliases(t *testing.T) {
	testVault := setupAliasRenameCommandTest(t)
	writeVaultProfile(t, testVault, "codex", "auto-20260121-143022", `{"fixture_profile":"auto"}`)

	cfg := config.DefaultConfig()
	cfg.AddAlias("codex", "auto-20260121-143022", "work")
	cfg.AddAlias("codex", "auto-20260121-143022", "primary")
	require.NoError(t, cfg.Save())

	renameCmd := newRenameCommandForTest(t)
	require.NoError(t, renameCmd.Flags().Set("json", "true"))
	out, err := captureStdout(t, func() error {
		return runRename(renameCmd, []string{"codex", "auto-20260121-143022", "work-account"})
	})
	require.NoError(t, err)

	var result map[string]any
	require.NoError(t, json.Unmarshal([]byte(out), &result))
	require.Equal(t, "codex", result["tool"])
	require.Equal(t, "auto-20260121-143022", result["old_name"])
	require.Equal(t, "work-account", result["new_name"])
	require.Equal(t, true, result["copied"])
	require.Equal(t, false, result["deleted"])
	require.ElementsMatch(t, []any{"work", "primary"}, result["migrated_aliases"])

	requireProfileExists(t, testVault, "codex", "auto-20260121-143022")
	requireProfileExists(t, testVault, "codex", "work-account")
	requireFileContent(t, filepath.Join(testVault.ProfilePath("codex", "work-account"), "auth.json"), `{"fixture_profile":"auto"}`)

	saved, err := config.Load()
	require.NoError(t, err)
	require.Empty(t, saved.GetAliases("codex", "auto-20260121-143022"))
	require.ElementsMatch(t, []string{"work", "primary"}, saved.GetAliases("codex", "work-account"))
	require.Equal(t, "work-account", saved.ResolveAliasForProvider("codex", "work"))
	require.Equal(t, "work-account", saved.ResolveAliasForProvider("codex", "primary"))
	t.Logf("alias mappings migrated on rename: work, primary -> codex/%s", saved.ResolveAliasForProvider("codex", "work"))
}

func TestRenameDeleteOldDeclinedPreservesOldProfile(t *testing.T) {
	testVault := setupAliasRenameCommandTest(t)
	writeVaultProfile(t, testVault, "codex", "auto-20260121-143022", `{"fixture_profile":"auto"}`)

	renameCmd := newRenameCommandForTest(t)
	require.NoError(t, renameCmd.Flags().Set("delete-old", "true"))

	var errOut bytes.Buffer
	renameCmd.SetErr(&errOut)
	var out string
	var err error
	withStdin(t, "n\n", func() {
		out, err = captureStdout(t, func() error {
			return runRename(renameCmd, []string{"codex", "auto-20260121-143022", "work-account"})
		})
	})
	require.NoError(t, err)
	require.Contains(t, errOut.String(), "Delete old profile codex/auto-20260121-143022?")
	require.Contains(t, out, "Skipped deletion. Old profile preserved.")
	require.Contains(t, out, "Profile copied: codex/auto-20260121-143022 -> codex/work-account")

	requireProfileExists(t, testVault, "codex", "auto-20260121-143022")
	requireProfileExists(t, testVault, "codex", "work-account")
	t.Logf("delete-old declined: preserved codex/%s and copied codex/%s", "auto-20260121-143022", "work-account")
}

func setupAliasRenameCommandTest(t *testing.T) *authfile.Vault {
	t.Helper()

	tmpDir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(tmpDir, "xdg-config"))
	t.Setenv("CAAM_HOME", filepath.Join(tmpDir, "caam-home"))
	t.Setenv("CODEX_HOME", filepath.Join(tmpDir, "codex-home"))
	require.NoError(t, os.MkdirAll(os.Getenv("CODEX_HOME"), 0700))

	oldVault := vault
	testVault := authfile.NewVault(filepath.Join(tmpDir, "vault"))
	vault = testVault
	t.Cleanup(func() { vault = oldVault })

	return testVault
}

func newAliasCommandForTest(t *testing.T) *cobra.Command {
	t.Helper()

	cmd := &cobra.Command{}
	cmd.Flags().Bool("list", false, "")
	cmd.Flags().StringP("remove", "r", "", "")
	cmd.Flags().Bool("json", false, "")
	return cmd
}

func newRenameCommandForTest(t *testing.T) *cobra.Command {
	t.Helper()

	cmd := &cobra.Command{}
	cmd.Flags().Bool("delete-old", false, "")
	cmd.Flags().Bool("migrate-aliases", true, "")
	cmd.Flags().Bool("json", false, "")
	cmd.Flags().BoolP("yes", "y", false, "")
	addPromptModeFlags(cmd)
	return cmd
}

func writeVaultProfile(t *testing.T, testVault *authfile.Vault, tool, profileName, authJSON string) {
	t.Helper()

	profileDir := testVault.ProfilePath(tool, profileName)
	require.NoError(t, os.MkdirAll(profileDir, 0700))
	require.NoError(t, os.WriteFile(filepath.Join(profileDir, "auth.json"), []byte(authJSON), 0600))

	meta := map[string]any{
		"tool":    tool,
		"profile": profileName,
	}
	metaJSON, err := json.MarshalIndent(meta, "", "  ")
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(profileDir, "meta.json"), metaJSON, 0600))
}

func requireProfileExists(t *testing.T, testVault *authfile.Vault, tool, profileName string) {
	t.Helper()

	info, err := os.Stat(testVault.ProfilePath(tool, profileName))
	require.NoError(t, err)
	require.True(t, info.IsDir())
}

func requireFileContent(t *testing.T, path, want string) {
	t.Helper()

	got, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, want, string(got))
}
