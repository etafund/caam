// Package cmd implements the CLI commands for caam.
package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"time"

	"github.com/spf13/cobra"

	"github.com/Dicklesworthstone/coding_agent_account_manager/internal/update"
	"github.com/Dicklesworthstone/coding_agent_account_manager/internal/version"
)

// UpdateOutput represents the JSON output for update commands.
type UpdateOutput struct {
	Action          string `json:"action"` // "check", "update"
	CurrentVersion  string `json:"current_version"`
	LatestVersion   string `json:"latest_version,omitempty"`
	VersionFrom     string `json:"version_from,omitempty"`
	VersionTo       string `json:"version_to,omitempty"`
	UpdateAvailable bool   `json:"update_available"`
	Updated         bool   `json:"updated"`
	BackupPath      string `json:"backup_path,omitempty"`
	ReleaseURL      string `json:"release_url,omitempty"`
	DownloadURL     string `json:"download_url,omitempty"`
	DownloadSize    int64  `json:"download_size,omitempty"`
	ChecksumOK      *bool  `json:"checksum_ok,omitempty"`
	SignatureOK     *bool  `json:"signature_ok,omitempty"`
	Channel         string `json:"channel"`
	Error           string `json:"error,omitempty"`
}

type updateChecker interface {
	Check(context.Context) (*update.CheckResult, error)
}

type updateInstaller interface {
	updateChecker
	Update(context.Context) (*update.UpdateResult, error)
}

var updateCmd = &cobra.Command{
	Use:   "update",
	Short: "Self-update caam to the latest version",
	Long: `Updates caam to the latest version from GitHub releases.

The update process:
  1. Fetches release metadata from GitHub
  2. Verifies cosign signature on SHA256SUMS
  3. Verifies SHA256 checksum of the binary archive
  4. Creates a backup of the current binary
  5. Atomically replaces the binary

Flags:
  --check     Check for updates without installing
  --channel   Update channel: "stable" (default) or "beta"
  --version   Update to a specific version (e.g., "1.2.3")
  --json      Output results in JSON format
  --force     Force update even if already at latest version

Examples:
  caam update              # Update to latest stable version
  caam update --check      # Check if updates are available
  caam update --channel=beta  # Update to latest beta version
  caam update --version=1.2.0 # Update to specific version`,
	RunE: runUpdate,
}

func init() {
	rootCmd.AddCommand(updateCmd)
	updateCmd.Flags().Bool("check", false, "check for updates without installing")
	updateCmd.Flags().String("channel", "stable", "update channel (stable or beta)")
	updateCmd.Flags().String("version", "", "update to a specific version")
	updateCmd.Flags().Bool("json", false, "output in JSON format")
	updateCmd.Flags().Bool("force", false, "force update even if at latest version")
}

func runUpdate(cmd *cobra.Command, args []string) error {
	checkOnly, _ := cmd.Flags().GetBool("check")
	channel, _ := cmd.Flags().GetString("channel")
	targetVersion, _ := cmd.Flags().GetString("version")
	jsonOutput, _ := cmd.Flags().GetBool("json")
	force, _ := cmd.Flags().GetBool("force")
	action := "update"
	if checkOnly {
		action = "check"
	}

	// Build update config
	config := update.DefaultConfig()

	switch channel {
	case "stable":
		config.Channel = update.ChannelStable
	case "beta":
		config.Channel = update.ChannelBeta
	default:
		return emitUpdateValidationError(cmd, jsonOutput, UpdateOutput{
			Action:         action,
			CurrentVersion: version.Short(),
			Channel:        channel,
		}, fmt.Errorf("invalid channel: %s (use 'stable' or 'beta')", channel))
	}

	if targetVersion != "" {
		config.TargetVersion = targetVersion
	}
	config.Force = force

	if checkOnly && targetVersion != "" {
		return emitUpdateValidationError(cmd, jsonOutput, UpdateOutput{
			Action:         action,
			CurrentVersion: version.Short(),
			LatestVersion:  targetVersion,
			Channel:        channel,
		}, fmt.Errorf("--check cannot be combined with --version; use 'caam update --version %s' to install that release", targetVersion))
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	updater := update.New(config)
	out := cmd.OutOrStdout()

	var err error
	if checkOnly {
		err = runUpdateCheck(ctx, updater, channel, jsonOutput, out)
	} else {
		err = runUpdateInstall(ctx, updater, channel, jsonOutput, force, targetVersion, out)
	}

	if err != nil && jsonOutput {
		cmd.SilenceErrors = true
		cmd.SilenceUsage = true
	}
	return err
}

func emitUpdateValidationError(cmd *cobra.Command, jsonOutput bool, output UpdateOutput, err error) error {
	if jsonOutput {
		output.Error = err.Error()
		_ = printJSON(cmd.OutOrStdout(), output)
		cmd.SilenceErrors = true
		cmd.SilenceUsage = true
	}
	return err
}

func runUpdateCheck(ctx context.Context, updater updateChecker, channel string, jsonOutput bool, out io.Writer) error {
	result, err := updater.Check(ctx)

	output := UpdateOutput{
		Action:         "check",
		CurrentVersion: version.Short(),
		Channel:        channel,
	}

	if err != nil {
		output.Error = err.Error()
		if jsonOutput {
			_ = printJSON(out, output)
		}
		return fmt.Errorf("check for updates: %w", err)
	}

	output.LatestVersion = result.LatestVersion
	output.UpdateAvailable = result.UpdateAvailable
	if result.Release != nil {
		output.ReleaseURL = result.Release.HTMLURL
	}

	if jsonOutput {
		return printJSON(out, output)
	}

	fmt.Fprintf(out, "Current version: %s\n", output.CurrentVersion)
	fmt.Fprintf(out, "Latest version:  %s\n", output.LatestVersion)
	fmt.Fprintf(out, "Channel:         %s\n", channel)

	if result.UpdateAvailable {
		fmt.Fprintln(out, "\nUpdate available! Run 'caam update' to install.")
		if result.Release != nil {
			fmt.Fprintf(out, "Release notes: %s\n", result.Release.HTMLURL)
		}
	} else {
		fmt.Fprintln(out, "\nYou're running the latest version.")
	}

	return nil
}

func runUpdateInstall(ctx context.Context, updater updateInstaller, channel string, jsonOutput bool, force bool, targetVersion string, out io.Writer) error {
	if targetVersion != "" {
		if !jsonOutput {
			fmt.Fprintf(out, "Updating caam from %s to %s...\n", version.Short(), targetVersion)
		}

		result, err := updater.Update(ctx)
		output := UpdateOutput{
			Action:         "update",
			CurrentVersion: version.Short(),
			LatestVersion:  targetVersion,
			Channel:        channel,
		}
		applyUpdateResult(&output, result)
		if err != nil {
			output.Error = err.Error()
			if jsonOutput {
				_ = printJSON(out, output)
			}
			return fmt.Errorf("update: %w", err)
		}
		if jsonOutput {
			return printJSON(out, output)
		}
		printUpdateResult(out, result)
		return nil
	}

	// First check if update is available
	check, err := updater.Check(ctx)
	if err != nil {
		output := UpdateOutput{
			Action:         "update",
			CurrentVersion: version.Short(),
			Channel:        channel,
			Error:          err.Error(),
		}
		if jsonOutput {
			_ = printJSON(out, output)
		}
		return fmt.Errorf("check for updates: %w", err)
	}

	if !check.UpdateAvailable && !force {
		output := UpdateOutput{
			Action:          "update",
			CurrentVersion:  version.Short(),
			LatestVersion:   check.LatestVersion,
			UpdateAvailable: false,
			Updated:         false,
			Channel:         channel,
		}
		if jsonOutput {
			return printJSON(out, output)
		}
		fmt.Fprintf(out, "Already at latest version (%s). Use --force to reinstall.\n", check.LatestVersion)
		return nil
	}

	if !jsonOutput {
		fmt.Fprintf(out, "Updating caam from %s to %s...\n", check.CurrentVersion, check.LatestVersion)
	}

	result, err := updater.Update(ctx)

	output := UpdateOutput{
		Action:          "update",
		CurrentVersion:  check.CurrentVersion,
		LatestVersion:   check.LatestVersion,
		UpdateAvailable: check.UpdateAvailable,
		Channel:         channel,
	}

	if err != nil {
		applyUpdateResult(&output, result)
		output.Error = err.Error()
		if jsonOutput {
			_ = printJSON(out, output)
		}
		return fmt.Errorf("update: %w", err)
	}

	applyUpdateResult(&output, result)

	if jsonOutput {
		return printJSON(out, output)
	}

	if result.Updated {
		printUpdateResult(out, result)
	} else {
		fmt.Fprintln(out, "No update performed.")
	}

	return nil
}

func applyUpdateResult(output *UpdateOutput, result *update.UpdateResult) {
	if result == nil {
		return
	}
	if result.FromVersion != "" {
		output.CurrentVersion = result.FromVersion
		output.VersionFrom = result.FromVersion
	}
	if result.ToVersion != "" {
		output.LatestVersion = result.ToVersion
		output.VersionTo = result.ToVersion
	}
	output.UpdateAvailable = result.UpdateAvailable
	output.Updated = result.Updated
	output.BackupPath = result.BackupPath
	output.ReleaseURL = result.ReleaseURL
	output.DownloadURL = result.DownloadURL
	output.DownloadSize = result.DownloadSize
	output.ChecksumOK = &result.ChecksumOK
	output.SignatureOK = &result.SignatureOK
}

func printUpdateResult(out io.Writer, result *update.UpdateResult) {
	if result != nil && result.Updated {
		fmt.Fprintln(out, "\n✓ Update successful!")
		fmt.Fprintf(out, "  From:   %s\n", result.FromVersion)
		fmt.Fprintf(out, "  To:     %s\n", result.ToVersion)
		fmt.Fprintf(out, "  Backup: %s\n", result.BackupPath)
		fmt.Fprintln(out, "\nRun 'caam version' to verify the update.")
		return
	}
	fmt.Fprintln(out, "No update performed.")
}

func printJSON(out io.Writer, v any) error {
	enc := json.NewEncoder(out)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}
