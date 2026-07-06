// Package cmd implements the CLI commands for caam.
package cmd

import (
	"errors"
	"fmt"
	"os"
	"sort"
	"time"

	"github.com/spf13/cobra"

	"github.com/Dicklesworthstone/coding_agent_account_manager/internal/authfile"
	"github.com/Dicklesworthstone/coding_agent_account_manager/internal/health"
)

var validateCmd = &cobra.Command{
	Use:   "validate [tool] [profile]",
	Short: "Validate authentication tokens",
	Long: `Validate that authentication tokens actually work.

By default, performs passive validation (no network calls):
  - Check auth file existence
  - Check token format/structure
  - Check expiry timestamps

Use --active to request active validation. Saved vault profiles currently do
not have safe active probes; they report method=passive, requested_method=active,
status=active_unsupported, and error_code=ACTIVE_VALIDATION_UNSUPPORTED instead
of pretending an API call was made.

Examples:
  caam validate                    # Validate all profiles (passive)
  caam validate claude             # Validate all Claude profiles
  caam validate claude work        # Validate specific profile
  caam validate --active           # Request active validation, explicit fallback if unsupported
  caam validate claude work --json # JSON output`,
	Args: cobra.MaximumNArgs(2),
	RunE: runValidate,
}

var (
	validateActive bool
	validateJSON   bool
	validateAll    bool
)

func init() {
	validateCmd.Flags().BoolVar(&validateActive, "active", false, "Request active validation; unsupported providers report status=active_unsupported")
	validateCmd.Flags().BoolVar(&validateJSON, "json", false, "Output in JSON format")
	validateCmd.Flags().BoolVar(&validateAll, "all", false, "Validate all profiles (default behavior)")
	rootCmd.AddCommand(validateCmd)
}

const (
	validationMethodPassive = "passive"
	validationMethodActive  = "active"

	validationStatusValid             = "valid"
	validationStatusInvalid           = "invalid"
	validationStatusActiveUnsupported = "active_unsupported"

	validationErrorActiveUnsupported = "ACTIVE_VALIDATION_UNSUPPORTED"
)

// ValidationOutput represents the JSON output for validation results.
type ValidationOutput struct {
	Provider        string    `json:"provider"`
	Profile         string    `json:"profile"`
	Valid           bool      `json:"valid"`
	Method          string    `json:"method"`
	RequestedMethod string    `json:"requested_method"`
	Status          string    `json:"status"`
	ExpiresAt       string    `json:"expires_at,omitempty"`
	ErrorCode       string    `json:"error_code,omitempty"`
	Error           string    `json:"error,omitempty"`
	CheckedAt       time.Time `json:"checked_at"`
}

func runValidate(cmd *cobra.Command, args []string) error {
	// Ensure the vault is initialized (PersistentPreRunE normally does this, but
	// keep validate robust when invoked directly, e.g. in tests).
	if vault == nil {
		vault = authfile.NewVault(authfile.DefaultVaultPath())
	}

	// validate operates on the SAME saved-profile source of truth as backup,
	// activate, and ls: the vault. Previously it read the isolated profile.Store,
	// so it reported missing/invalid credentials for normal vault-backed profiles
	// (issue #23).
	var toolFilter, profileFilter string
	switch len(args) {
	case 1:
		toolFilter = args[0]
	case 2:
		toolFilter = args[0]
		profileFilter = args[1]
	}

	if toolFilter != "" {
		if _, ok := tools[toolFilter]; !ok {
			return fmt.Errorf("unknown tool: %s (supported: %s)", toolFilter, supportedToolsList())
		}
	}

	results := validateVaultProfiles(toolFilter, profileFilter, validateActive)

	// Output results. Encode empty results as [] (not null) for agent parsing.
	if validateJSON {
		if err := outputJSON(results); err != nil {
			return err
		}
		return validationResultsError(results)
	}
	return outputHuman(results)
}

func validateVaultProfiles(toolFilter, profileFilter string, active bool) []ValidationOutput {
	results := []ValidationOutput{}

	for _, tool := range supportedTools() {
		if toolFilter != "" && tool != toolFilter {
			continue
		}

		profiles, err := vault.List(tool)
		if err != nil {
			continue // No profiles for this tool
		}
		sort.Strings(profiles)

		for _, profileName := range profiles {
			if authfile.IsSystemProfile(profileName) {
				continue // Skip _original / _backup_* system profiles
			}
			if profileFilter != "" && profileName != profileFilter {
				continue
			}
			results = append(results, validateVaultProfile(tool, profileName, active))
		}
	}

	return results
}

// validateVaultProfile passively validates a single saved vault profile. A
// profile is valid when its auth files are present and parseable. An expired
// access token does NOT make the profile invalid if a refresh token is present:
// such profiles are refreshable, and reporting them as hard-expired is
// misleading (issue #22). Only credentials with no refresh capability are
// reported as expired/invalid.
func validateVaultProfile(tool, profileName string, active bool) ValidationOutput {
	out := ValidationOutput{
		Provider:        tool,
		Profile:         profileName,
		Method:          validationMethodPassive,
		RequestedMethod: requestedValidationMethod(active),
		Status:          validationStatusValid,
		CheckedAt:       time.Now(),
	}

	info, err := loadExpiryInfo(tool, profileName)
	if err != nil {
		switch {
		case errors.Is(err, health.ErrNoAuthFile):
			out.Valid = false
			out.Status = validationStatusInvalid
			out.Error = "no auth files found"
		case errors.Is(err, health.ErrNoExpiry):
			// Auth files exist but carry no parseable expiry/refresh metadata.
			// Treat as valid-but-unknown: the credentials are present.
			out.Valid = true
			markActiveUnsupported(&out)
		default:
			out.Valid = false
			out.Status = validationStatusInvalid
			out.Error = err.Error()
		}
		return out
	}

	// Tools without expiry parsing (opencode/cursor/agy) return nil info; the
	// presence of the vault profile dir is the validation signal for them.
	if info == nil {
		out.Valid = true
		markActiveUnsupported(&out)
		return out
	}

	expired := !info.ExpiresAt.IsZero() && time.Until(info.ExpiresAt) <= 0
	switch {
	case expired && info.HasRefreshToken:
		// Refreshable: short-lived access token expired but a refresh token
		// remains. Considered valid/refreshable, not hard-expired. Avoid
		// presenting the access-token expiry as account expiry (issue #22).
		out.Valid = true
		out.ExpiresAt = "refreshable"
	case expired:
		out.Valid = false
		out.Status = validationStatusInvalid
		out.Error = "token expired and no refresh token available"
		out.ExpiresAt = "expired"
	default:
		out.Valid = true
		if !info.ExpiresAt.IsZero() {
			out.ExpiresAt = formatExpiryTime(info.ExpiresAt)
		}
	}

	if out.Valid {
		markActiveUnsupported(&out)
	}

	return out
}

func requestedValidationMethod(active bool) string {
	if active {
		return validationMethodActive
	}
	return validationMethodPassive
}

func markActiveUnsupported(out *ValidationOutput) {
	if out.RequestedMethod != validationMethodActive || !out.Valid {
		return
	}
	out.Status = validationStatusActiveUnsupported
	out.ErrorCode = validationErrorActiveUnsupported
	out.Error = "safe active validation is not implemented for saved vault profiles; passive validation completed"
}

func formatExpiryTime(t time.Time) string {
	now := time.Now()
	diff := t.Sub(now)

	if diff < 0 {
		return "expired"
	}

	if diff < time.Hour {
		return fmt.Sprintf("in %d minutes", int(diff.Minutes()))
	}
	if diff < 24*time.Hour {
		return fmt.Sprintf("in %d hours", int(diff.Hours()))
	}
	return fmt.Sprintf("in %d days", int(diff.Hours()/24))
}

func outputJSON(results []ValidationOutput) error {
	return encodeIndentedJSON(os.Stdout, results)
}

func outputHuman(results []ValidationOutput) error {
	if len(results) == 0 {
		fmt.Println("No profiles to validate.")
		return nil
	}

	fmt.Println("Token Validation Results")
	fmt.Println("========================")
	fmt.Println()

	validCount := 0
	invalidCount := 0
	unsupportedCount := 0

	for _, r := range results {
		status := "✓"
		statusColor := "\033[32m" // Green
		switch {
		case r.Status == validationStatusActiveUnsupported:
			status = "!"
			statusColor = "\033[33m" // Yellow
			unsupportedCount++
		case !r.Valid:
			status = "✗"
			statusColor = "\033[31m" // Red
			invalidCount++
		default:
			validCount++
		}

		// Print result line
		fmt.Printf("%s%s\033[0m %s/%s", statusColor, status, r.Provider, r.Profile)

		if r.Status == validationStatusActiveUnsupported {
			fmt.Printf(" - active validation unsupported; passive result valid")
		} else if r.Valid {
			if r.ExpiresAt != "" {
				fmt.Printf(" (expires %s)", r.ExpiresAt)
			} else {
				fmt.Print(" (valid)")
			}
		} else {
			fmt.Printf(" - %s", r.Error)
		}
		fmt.Println()
	}

	fmt.Println()
	if unsupportedCount > 0 {
		fmt.Printf("Summary: %d valid, %d invalid, %d active unsupported (method: %s, requested: %s)\n",
			validCount, invalidCount, unsupportedCount, results[0].Method, results[0].RequestedMethod)
	} else {
		fmt.Printf("Summary: %d valid, %d invalid (method: %s)\n", validCount, invalidCount, results[0].Method)
	}

	return validationResultsError(results)
}

func validationResultsError(results []ValidationOutput) error {
	invalidCount := 0
	unsupportedCount := 0
	for _, result := range results {
		if result.Status == validationStatusActiveUnsupported {
			unsupportedCount++
			continue
		}
		if !result.Valid {
			invalidCount++
		}
	}

	switch {
	case invalidCount > 0 && unsupportedCount > 0:
		return fmt.Errorf("%d invalid token(s), %d active validation probe(s) unsupported", invalidCount, unsupportedCount)
	case invalidCount > 0:
		return fmt.Errorf("%d invalid token(s) found", invalidCount)
	case unsupportedCount > 0:
		return fmt.Errorf("%d active validation probe(s) unsupported", unsupportedCount)
	default:
		return nil
	}
}
