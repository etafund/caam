package cmd

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/Dicklesworthstone/coding_agent_account_manager/internal/authfile"
	caamdb "github.com/Dicklesworthstone/coding_agent_account_manager/internal/db"
	"github.com/Dicklesworthstone/coding_agent_account_manager/internal/health"
	"github.com/spf13/cobra"
)

var (
	robotActCommandRe      = regexp.MustCompile(`\bcaam robot act\s+([a-z][a-z-]*)\b`)
	robotActHelpCommandRe  = regexp.MustCompile(`(?m)^\s{2}([a-z][a-z-]*)\s+<provider>(?:\s|$)`)
	robotActionBlockLineRe = regexp.MustCompile(`(?m)^caam robot act\s+[a-z][^\n]+$`)
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func TestRobotDocs_AllTopics(t *testing.T) {
	out, _, err := captureOutput(t, createTestCmd(), []string{"robot", "docs"})
	if err != nil {
		t.Fatalf("expected robot docs to succeed, got error: %v", err)
	}

	var envelope struct {
		Success bool          `json:"success"`
		Command string        `json:"command"`
		Data    RobotDocsData `json:"data"`
		Error   *RobotError   `json:"error"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &envelope); err != nil {
		t.Fatalf("invalid JSON output: %v", err)
	}
	if !strings.Contains(out, "\n  \"success\"") {
		t.Fatalf("expected normal robot JSON output to be indented, got %q", out)
	}

	if !envelope.Success {
		t.Fatal("expected success=true")
	}
	if envelope.Command != "docs" {
		t.Fatalf("expected command docs, got %q", envelope.Command)
	}
	if envelope.Data.SchemaVersion != robotDocsSchemaVersion {
		t.Fatalf("unexpected schema version: %d", envelope.Data.SchemaVersion)
	}
	if len(envelope.Data.Topics) == 0 {
		t.Fatal("expected at least one topic")
	}
}

func TestRobotDocs_SingleTopic(t *testing.T) {
	out, _, err := captureOutput(t, createTestCmd(), []string{"robot", "docs", "exit-codes"})
	if err != nil {
		t.Fatalf("expected robot docs topic to succeed, got error: %v", err)
	}

	var envelope struct {
		Success bool          `json:"success"`
		Data    RobotDocsData `json:"data"`
		Error   *RobotError   `json:"error"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &envelope); err != nil {
		t.Fatalf("invalid JSON output: %v", err)
	}
	if !envelope.Success {
		t.Fatal("expected success=true")
	}
	if len(envelope.Data.Topics) != 1 || envelope.Data.Topics[0].Topic != "exit-codes" {
		t.Fatalf("expected only exit-codes topic, got %+v", envelope.Data.Topics)
	}
}

func TestRobotDocs_InvalidTopic(t *testing.T) {
	_, _, err := captureOutput(t, createTestCmd(), []string{"robot", "docs", "missing-topic"})
	if err == nil {
		t.Fatal("expected error for invalid topic")
	}
	if !strings.Contains(err.Error(), "INVALID_TOPIC") {
		t.Fatalf("expected INVALID_TOPIC error, got %v", err)
	}
}

func TestRobotValidateActiveUnsupportedUsesSharedSemantics(t *testing.T) {
	tmp := t.TempDir()
	oldVault := vault
	vault = authfile.NewVault(filepath.Join(tmp, "vault"))
	t.Cleanup(func() { vault = oldVault })

	writeClaudeVaultProfile(t, "healthy", 48*time.Hour, true)

	cmd := &cobra.Command{Use: "validate"}
	cmd.Flags().Bool("active", false, "")
	if err := cmd.Flags().Set("active", "true"); err != nil {
		t.Fatalf("set active flag: %v", err)
	}
	var out strings.Builder
	cmd.SetOut(&out)

	if err := runRobotValidate(cmd, []string{"claude"}); err == nil {
		t.Fatal("runRobotValidate should return an error when active validation is unsupported")
	}

	var envelope struct {
		Success bool              `json:"success"`
		Command string            `json:"command"`
		Data    RobotValidateData `json:"data"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(out.String())), &envelope); err != nil {
		t.Fatalf("invalid JSON output: %v; raw=%q", err, out.String())
	}
	if envelope.Success {
		t.Fatal("expected success=false when active validation is unsupported")
	}
	if envelope.Command != "validate" {
		t.Fatalf("Command = %q, want validate", envelope.Command)
	}
	if envelope.Data.Method != validationMethodPassive {
		t.Fatalf("method = %q, want %q", envelope.Data.Method, validationMethodPassive)
	}
	if envelope.Data.RequestedMethod != validationMethodActive {
		t.Fatalf("requested_method = %q, want %q", envelope.Data.RequestedMethod, validationMethodActive)
	}
	if envelope.Data.Status != validationStatusActiveUnsupported {
		t.Fatalf("status = %q, want %q", envelope.Data.Status, validationStatusActiveUnsupported)
	}
	if envelope.Data.Summary.Unsupported != 1 {
		t.Fatalf("unsupported summary = %d, want 1", envelope.Data.Summary.Unsupported)
	}
	if len(envelope.Data.Profiles) != 1 {
		t.Fatalf("expected one profile, got %+v", envelope.Data.Profiles)
	}

	result := envelope.Data.Profiles[0]
	if result.Method != validationMethodPassive {
		t.Fatalf("profile method = %q, want %q", result.Method, validationMethodPassive)
	}
	if result.Status != validationStatusActiveUnsupported {
		t.Fatalf("profile status = %q, want %q", result.Status, validationStatusActiveUnsupported)
	}
	if result.ErrorCode != validationErrorActiveUnsupported {
		t.Fatalf("profile error_code = %q, want %q", result.ErrorCode, validationErrorActiveUnsupported)
	}
}

func TestRobotDocs_Ndjson(t *testing.T) {
	out, _, err := captureOutput(t, createTestCmd(), []string{"robot", "docs", "commands", "--ndjson"})
	if err != nil {
		t.Fatalf("expected robot docs ndjson to succeed, got error: %v", err)
	}

	line := strings.TrimSpace(out)
	if line == "" {
		t.Fatalf("expected non-empty output")
	}
	if strings.Contains(line, "\n") {
		t.Fatalf("expected NDJSON fragment to stay single-line, got %q", line)
	}

	var envelope struct {
		Timestamp string        `json:"timestamp"`
		Data      RobotDocsData `json:"data"`
	}
	if err := json.Unmarshal([]byte(line), &envelope); err != nil {
		t.Fatalf("invalid NDJSON fragment: %v", err)
	}
	if envelope.Timestamp == "" {
		t.Fatal("expected NDJSON fragment timestamp")
	}
	if _, err := time.Parse(time.RFC3339, envelope.Timestamp); err != nil {
		t.Fatalf("timestamp = %q, want RFC3339: %v", envelope.Timestamp, err)
	}
	if len(envelope.Data.Topics) != 1 || envelope.Data.Topics[0].Topic != "commands" {
		t.Fatalf("expected one commands topic, got %+v", envelope.Data.Topics)
	}
}

func TestRobotLimitsAndPrecheckHelpDoNotOverclaimLiveFetches(t *testing.T) {
	tests := []struct {
		name string
		args []string
	}{
		{name: "quickstart", args: []string{"robot"}},
		{name: "limits", args: []string{"robot", "limits", "--help"}},
		{name: "precheck", args: []string{"robot", "precheck", "--help"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out, stderr, err := captureOutput(t, createTestCmd(), tt.args)
			if err != nil {
				t.Fatalf("help failed: %v", err)
			}
			help := out + stderr
			for _, forbidden := range []string{
				"provider APIs",
				"provider API",
				"API fetch",
				"live provider limits",
				"real-time rate limit",
				"Usage forecasts",
				"burn-rate estimates",
				"depletion forecasts",
				"cached health/cooldown state",
			} {
				if strings.Contains(help, forbidden) {
					t.Fatalf("help overclaims %q:\n%s", forbidden, help)
				}
			}
		})
	}
}

func TestRobotLimitsHelpPromisesOnlyCachedLocalSignals(t *testing.T) {
	out, stderr, err := captureOutput(t, createTestCmd(), []string{"robot", "limits", "--help"})
	if err != nil {
		t.Fatalf("help failed: %v", err)
	}
	help := out + stderr

	for _, required := range []string{
		"cached local availability signals",
		"local-only",
		"local cooldown records",
		"No network calls are performed",
	} {
		if !strings.Contains(help, required) {
			t.Fatalf("limits help missing %q:\n%s", required, help)
		}
	}

	for _, forbidden := range []string{"provider api", "live", "fetch"} {
		if strings.Contains(strings.ToLower(help), forbidden) {
			t.Fatalf("limits help should not mention %q:\n%s", forbidden, help)
		}
	}
}

func TestRobotPrecheckHelpMentionsImplementedCooldownPlanning(t *testing.T) {
	out, stderr, err := captureOutput(t, createTestCmd(), []string{"robot", "precheck", "--help"})
	if err != nil {
		t.Fatalf("help failed: %v", err)
	}
	help := out + stderr

	if !strings.Contains(help, "Profiles in cooldown") {
		t.Fatalf("precheck help should mention local cooldown planning:\n%s", help)
	}
}

func TestRobotPrecheckSortsBackupsByScore(t *testing.T) {
	isolateRobotCommandEnv(t)

	tmp := t.TempDir()
	oldVault := vault
	oldHealthStore := healthStore
	testVault := authfile.NewVault(filepath.Join(tmp, "vault"))
	vault = testVault
	healthStore = health.NewStorage(filepath.Join(tmp, "health.json"))
	t.Cleanup(func() {
		vault = oldVault
		healthStore = oldHealthStore
	})

	writeVaultProfile(t, testVault, "codex", "expired", `{"fixture_profile":"expired"}`)
	writeVaultProfile(t, testVault, "codex", "healthy", `{"fixture_profile":"healthy"}`)
	writeVaultProfile(t, testVault, "codex", "unknown", `{"fixture_profile":"unknown"}`)

	if err := healthStore.UpdateProfile("codex", "expired", &health.ProfileHealth{
		TokenExpiresAt: time.Now().Add(-1 * time.Hour),
	}); err != nil {
		t.Fatalf("seed expired health: %v", err)
	}
	if err := healthStore.UpdateProfile("codex", "healthy", &health.ProfileHealth{
		TokenExpiresAt: time.Now().Add(48 * time.Hour),
	}); err != nil {
		t.Fatalf("seed healthy health: %v", err)
	}

	cmd := &cobra.Command{Use: "precheck"}
	var out strings.Builder
	cmd.SetOut(&out)

	if err := runRobotPrecheck(cmd, []string{"codex"}); err != nil {
		t.Fatalf("runRobotPrecheck: %v", err)
	}

	var envelope struct {
		Success bool              `json:"success"`
		Data    RobotPrecheckData `json:"data"`
		Error   *RobotError       `json:"error"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(out.String())), &envelope); err != nil {
		t.Fatalf("invalid JSON output: %v; raw=%q", err, out.String())
	}
	if !envelope.Success {
		t.Fatalf("expected success=true, got error %+v", envelope.Error)
	}
	if envelope.Data.Recommended == nil {
		t.Fatal("expected recommended profile")
	}
	if envelope.Data.Recommended.Name != "healthy" {
		t.Fatalf("recommended = %q, want healthy (data=%+v)", envelope.Data.Recommended.Name, envelope.Data)
	}
	if len(envelope.Data.Backups) != 2 {
		t.Fatalf("backups = %+v, want two retained backups", envelope.Data.Backups)
	}
	if envelope.Data.Backups[0].Name != "unknown" || envelope.Data.Backups[1].Name != "expired" {
		t.Fatalf("backups order = %+v, want unknown then expired", envelope.Data.Backups)
	}
	if envelope.Data.Backups[0].Score < envelope.Data.Backups[1].Score {
		t.Fatalf("backups not sorted by descending score: %+v", envelope.Data.Backups)
	}
}

func TestRobotLimitsMarksCooldownProfilesUnavailable(t *testing.T) {
	isolateRobotCommandEnv(t)

	tmp := t.TempDir()
	oldVault := vault
	oldHealthStore := healthStore
	testVault := authfile.NewVault(filepath.Join(tmp, "vault"))
	vault = testVault
	healthStore = health.NewStorage(filepath.Join(tmp, "health.json"))
	t.Cleanup(func() {
		vault = oldVault
		healthStore = oldHealthStore
	})

	writeVaultProfile(t, testVault, "codex", "healthy-cooling", `{"fixture_profile":"healthy-cooling"}`)
	if err := healthStore.UpdateProfile("codex", "healthy-cooling", &health.ProfileHealth{
		TokenExpiresAt: time.Now().Add(48 * time.Hour),
	}); err != nil {
		t.Fatalf("seed health: %v", err)
	}

	db, err := caamdb.Open()
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()
	if _, err := db.SetCooldown("codex", "healthy-cooling", time.Now().UTC(), time.Hour, "test cooldown"); err != nil {
		t.Fatalf("set cooldown: %v", err)
	}

	cmd := &cobra.Command{Use: "limits"}
	var out strings.Builder
	cmd.SetOut(&out)

	if err := runRobotLimits(cmd, []string{"codex"}); err != nil {
		t.Fatalf("runRobotLimits: %v", err)
	}

	var envelope struct {
		Success bool            `json:"success"`
		Data    RobotLimitsData `json:"data"`
		Error   *RobotError     `json:"error"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(out.String())), &envelope); err != nil {
		t.Fatalf("invalid JSON output: %v; raw=%q", err, out.String())
	}
	if !envelope.Success {
		t.Fatalf("expected success=true, got error %+v", envelope.Error)
	}
	if len(envelope.Data.Profiles) != 1 {
		t.Fatalf("profiles = %+v, want one profile", envelope.Data.Profiles)
	}

	profile := envelope.Data.Profiles[0]
	if profile.AvailScore != 0 {
		t.Fatalf("availability_score = %d, want 0 for cooldown profile", profile.AvailScore)
	}
	if profile.Cooldown == nil || !profile.Cooldown.Active {
		t.Fatalf("expected active cooldown details, got %+v", profile.Cooldown)
	}
	if !strings.Contains(profile.Recommendation, "wait for cooldown") {
		t.Fatalf("recommendation = %q, want cooldown wait", profile.Recommendation)
	}
}

func TestRobotLimitsUsesOnlyLocalState(t *testing.T) {
	isolateRobotCommandEnv(t)

	tmp := t.TempDir()
	oldVault := vault
	oldHealthStore := healthStore
	testVault := authfile.NewVault(filepath.Join(tmp, "vault"))
	vault = testVault
	healthStore = health.NewStorage(filepath.Join(tmp, "health.json"))
	t.Cleanup(func() {
		vault = oldVault
		healthStore = oldHealthStore
	})

	writeVaultProfile(t, testVault, "codex", "local-only", `{"fixture_profile":"local-only"}`)
	if err := healthStore.UpdateProfile("codex", "local-only", &health.ProfileHealth{
		TokenExpiresAt: time.Now().Add(48 * time.Hour),
	}); err != nil {
		t.Fatalf("seed health: %v", err)
	}

	oldTransport := http.DefaultTransport
	networkCalled := false
	http.DefaultTransport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		networkCalled = true
		return nil, errors.New("unexpected network call from robot limits")
	})
	t.Cleanup(func() { http.DefaultTransport = oldTransport })

	cmd := &cobra.Command{Use: "limits"}
	var out strings.Builder
	cmd.SetOut(&out)

	if err := runRobotLimits(cmd, []string{"codex"}); err != nil {
		t.Fatalf("runRobotLimits: %v", err)
	}
	if networkCalled {
		t.Fatal("robot limits attempted an HTTP request")
	}

	var envelope struct {
		Success bool            `json:"success"`
		Data    RobotLimitsData `json:"data"`
		Error   *RobotError     `json:"error"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(out.String())), &envelope); err != nil {
		t.Fatalf("invalid JSON output: %v; raw=%q", err, out.String())
	}
	if !envelope.Success {
		t.Fatalf("expected success=true, got error %+v", envelope.Error)
	}
	if len(envelope.Data.Profiles) != 1 {
		t.Fatalf("profiles = %+v, want one profile", envelope.Data.Profiles)
	}
	if envelope.Data.Profiles[0].Recommendation != "ready to use" {
		t.Fatalf("recommendation = %q, want ready to use", envelope.Data.Profiles[0].Recommendation)
	}
}

func TestRobotNextLRUUsesLocalActivationHistory(t *testing.T) {
	isolateRobotCommandEnv(t)

	tmp := t.TempDir()
	oldVault := vault
	oldHealthStore := healthStore
	testVault := authfile.NewVault(filepath.Join(tmp, "vault"))
	vault = testVault
	healthStore = health.NewStorage(filepath.Join(tmp, "health.json"))
	t.Cleanup(func() {
		vault = oldVault
		healthStore = oldHealthStore
	})

	writeVaultProfile(t, testVault, "codex", "recent", `{"fixture_profile":"recent"}`)
	writeVaultProfile(t, testVault, "codex", "old", `{"fixture_profile":"old"}`)
	for _, profile := range []string{"recent", "old"} {
		if err := healthStore.UpdateProfile("codex", profile, &health.ProfileHealth{
			TokenExpiresAt: time.Now().Add(48 * time.Hour),
		}); err != nil {
			t.Fatalf("seed health for %s: %v", profile, err)
		}
	}

	db, err := caamdb.Open()
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()
	now := time.Now().UTC()
	if err := db.LogEvent(caamdb.Event{
		Type:        caamdb.EventActivate,
		Provider:    "codex",
		ProfileName: "recent",
		Timestamp:   now.Add(-10 * time.Minute),
	}); err != nil {
		t.Fatalf("log recent activation: %v", err)
	}
	if err := db.LogEvent(caamdb.Event{
		Type:        caamdb.EventActivate,
		Provider:    "codex",
		ProfileName: "old",
		Timestamp:   now.Add(-48 * time.Hour),
	}); err != nil {
		t.Fatalf("log old activation: %v", err)
	}

	cmd := &cobra.Command{Use: "next"}
	cmd.Flags().String("strategy", "lru", "")
	cmd.Flags().Bool("include-cooldown", false, "")
	var out strings.Builder
	cmd.SetOut(&out)

	if err := runRobotNext(cmd, []string{"codex"}); err != nil {
		t.Fatalf("runRobotNext: %v", err)
	}

	var envelope struct {
		Success bool          `json:"success"`
		Data    RobotNextData `json:"data"`
		Error   *RobotError   `json:"error"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(out.String())), &envelope); err != nil {
		t.Fatalf("invalid JSON output: %v; raw=%q", err, out.String())
	}
	if !envelope.Success {
		t.Fatalf("expected success=true, got error %+v", envelope.Error)
	}
	if envelope.Data.Profile != "old" {
		t.Fatalf("recommended profile = %q, want old; data=%+v", envelope.Data.Profile, envelope.Data)
	}
	if !strings.Contains(strings.Join(envelope.Data.Reasons, "; "), "lru bonus") {
		t.Fatalf("reasons = %+v, want LRU bonus", envelope.Data.Reasons)
	}
}

func TestRobotNextLRUNoHistoryIsExplicitlyNeutral(t *testing.T) {
	isolateRobotCommandEnv(t)

	tmp := t.TempDir()
	oldVault := vault
	oldHealthStore := healthStore
	testVault := authfile.NewVault(filepath.Join(tmp, "vault"))
	vault = testVault
	healthStore = health.NewStorage(filepath.Join(tmp, "health.json"))
	t.Cleanup(func() {
		vault = oldVault
		healthStore = oldHealthStore
	})

	writeVaultProfile(t, testVault, "codex", "new", `{"fixture_profile":"new"}`)
	if err := healthStore.UpdateProfile("codex", "new", &health.ProfileHealth{
		TokenExpiresAt: time.Now().Add(48 * time.Hour),
	}); err != nil {
		t.Fatalf("seed health: %v", err)
	}

	cmd := &cobra.Command{Use: "next"}
	cmd.Flags().String("strategy", "lru", "")
	cmd.Flags().Bool("include-cooldown", false, "")
	var out strings.Builder
	cmd.SetOut(&out)

	if err := runRobotNext(cmd, []string{"codex"}); err != nil {
		t.Fatalf("runRobotNext: %v", err)
	}

	var envelope struct {
		Success bool          `json:"success"`
		Data    RobotNextData `json:"data"`
		Error   *RobotError   `json:"error"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(out.String())), &envelope); err != nil {
		t.Fatalf("invalid JSON output: %v; raw=%q", err, out.String())
	}
	if !envelope.Success {
		t.Fatalf("expected success=true, got error %+v", envelope.Error)
	}
	if envelope.Data.Profile != "new" {
		t.Fatalf("recommended profile = %q, want new", envelope.Data.Profile)
	}
	reasons := strings.Join(envelope.Data.Reasons, "; ")
	if !strings.Contains(reasons, "lru unavailable: no local activation history; neutral") {
		t.Fatalf("reasons = %q, want explicit neutral LRU reason", reasons)
	}
}

func TestRobotGuideHelpAndDocsAdvertiseImplementedActions(t *testing.T) {
	isolateRobotCommandEnv(t)
	expected := robotActActionNames()

	guideOut, _, err := captureOutput(t, createTestCmd(), []string{"robot"})
	if err != nil {
		t.Fatalf("expected robot guide to succeed, got error: %v", err)
	}
	requireStringSliceEqual(t, "guide actions", extractRobotActActions(guideOut), expected)

	helpOut := robotActHelpOutput(t)
	helpActions := extractRobotActHelpActions(helpOut)
	if len(helpActions) == 0 {
		t.Fatalf("help output did not advertise robot act actions:\n%s", helpOut)
	}
	requireStringSliceEqual(t, "help actions", helpActions, expected)
	assertNoUnsupportedRobotActExamples(t, helpOut)

	docsOut, _, err := captureOutput(t, createTestCmd(), []string{"robot", "docs", "commands"})
	if err != nil {
		t.Fatalf("expected robot docs commands to succeed, got error: %v", err)
	}
	var envelope struct {
		Success bool          `json:"success"`
		Data    RobotDocsData `json:"data"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(docsOut)), &envelope); err != nil {
		t.Fatalf("invalid JSON docs output: %v", err)
	}
	if !envelope.Success {
		t.Fatal("expected docs success=true")
	}
	requireStringSliceEqual(t, "docs actions", extractRobotDocsActActions(envelope.Data), expected)
}

func TestRobotActDispatchMatchesAdvertisedActions(t *testing.T) {
	isolateRobotCommandEnv(t)

	helpOut := robotActHelpOutput(t)
	advertised := extractRobotActHelpActions(helpOut)

	for _, action := range advertised {
		t.Run("advertised_"+action, func(t *testing.T) {
			got, err := runRobotActForTest(t, []string{action, "codex"})
			if err == nil {
				t.Fatalf("expected validation/runtime error for side-effect-free %q probe", action)
			}
			if got.Error == nil {
				t.Fatalf("expected structured error for %q, got %+v", action, got)
			}
			if got.Error.Code == "INVALID_ACTION" {
				t.Fatalf("advertised action %q was rejected as invalid: %+v", action, got.Error)
			}
		})
	}

	for _, action := range []string{"delete", "refresh", "switch"} {
		t.Run("unadvertised_"+action, func(t *testing.T) {
			got, err := runRobotActForTest(t, []string{action, "codex", "work"})
			if err == nil {
				t.Fatalf("expected INVALID_ACTION for unsupported action %q", action)
			}
			if got.Error == nil {
				t.Fatalf("expected structured error for %q, got %+v", action, got)
			}
			if got.Error.Code != "INVALID_ACTION" {
				t.Fatalf("error code for %q = %q, want INVALID_ACTION", action, got.Error.Code)
			}
			if !strings.Contains(got.Error.Details, "valid actions: activate, cooldown, uncooldown, backup") {
				t.Fatalf("INVALID_ACTION details should list valid actions, got %q", got.Error.Details)
			}
			for _, suggestion := range got.Suggestions {
				if strings.Contains(suggestion, " delete ") || strings.Contains(suggestion, " refresh ") {
					t.Fatalf("unsupported action leaked into suggestions: %q", suggestion)
				}
			}
		})
	}
}

func TestRobotActCooldownRejectsInvalidDuration(t *testing.T) {
	got, err := runRobotActForTest(t, []string{"cooldown", "codex", "work", "soon"})
	if err == nil {
		t.Fatal("expected invalid duration error")
	}
	if got.Error == nil {
		t.Fatalf("expected structured error, got %+v", got)
	}
	if got.Error.Code != "INVALID_DURATION" {
		t.Fatalf("error code = %q, want INVALID_DURATION", got.Error.Code)
	}
}

func TestRobotValidateMissingProfileErrors(t *testing.T) {
	tmp := t.TempDir()
	oldVault := vault
	vault = authfile.NewVault(filepath.Join(tmp, "vault"))
	t.Cleanup(func() { vault = oldVault })

	cmd := &cobra.Command{Use: "validate"}
	cmd.Flags().Bool("active", false, "")
	var out strings.Builder
	cmd.SetOut(&out)

	err := runRobotValidate(cmd, []string{"codex", "missing"})
	if err == nil {
		t.Fatal("expected missing profile error")
	}

	got := decodeRobotOutput(t, out.String())
	if got.Error == nil {
		t.Fatalf("expected structured error, got %+v", got)
	}
	if got.Error.Code != "PROFILE_NOT_FOUND" {
		t.Fatalf("error code = %q, want PROFILE_NOT_FOUND", got.Error.Code)
	}
	if got.Success {
		t.Fatal("expected success=false")
	}
}

func TestRobotValidateNoProfilesErrors(t *testing.T) {
	tmp := t.TempDir()
	oldVault := vault
	vault = authfile.NewVault(filepath.Join(tmp, "vault"))
	t.Cleanup(func() { vault = oldVault })

	tests := []struct {
		name        string
		args        []string
		wantMessage string
	}{
		{name: "all providers", args: nil, wantMessage: "no profiles found"},
		{name: "single provider", args: []string{"codex"}, wantMessage: "no profiles found for codex"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd := &cobra.Command{Use: "validate"}
			cmd.Flags().Bool("active", false, "")
			var out strings.Builder
			cmd.SetOut(&out)

			err := runRobotValidate(cmd, tt.args)
			if err == nil {
				t.Fatal("expected no profiles error")
			}

			got := decodeRobotOutput(t, out.String())
			if got.Error == nil {
				t.Fatalf("expected structured error, got %+v", got)
			}
			if got.Error.Code != "NO_PROFILES" {
				t.Fatalf("error code = %q, want NO_PROFILES", got.Error.Code)
			}
			if !strings.Contains(got.Error.Message, tt.wantMessage) {
				t.Fatalf("error message = %q, want substring %q", got.Error.Message, tt.wantMessage)
			}
			if got.Success {
				t.Fatal("expected success=false")
			}
		})
	}
}

func TestCheckCoordinatorEndpointsPendingRequestHandling(t *testing.T) {
	tests := []struct {
		name        string
		statusCode  int
		pendingCode int
		pendingBody string
		wantHealthy bool
		wantPending int
		wantError   string
	}{
		{
			name:        "healthy with pending count",
			statusCode:  http.StatusOK,
			pendingCode: http.StatusOK,
			pendingBody: `[{},{}]`,
			wantHealthy: true,
			wantPending: 2,
		},
		{
			name:        "pending status error",
			statusCode:  http.StatusOK,
			pendingCode: http.StatusServiceUnavailable,
			pendingBody: `unavailable`,
			wantHealthy: true,
			wantError:   "pending endpoint returned HTTP 503",
		},
		{
			name:        "pending decode error",
			statusCode:  http.StatusOK,
			pendingCode: http.StatusOK,
			pendingBody: `{`,
			wantHealthy: true,
			wantError:   "decode pending response:",
		},
		{
			name:        "status error skips pending",
			statusCode:  http.StatusBadGateway,
			pendingCode: http.StatusOK,
			pendingBody: `[{}]`,
			wantHealthy: false,
			wantError:   "status endpoint returned HTTP 502",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pendingHits := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/status":
					w.WriteHeader(tt.statusCode)
					_, _ = w.Write([]byte(`{}`))
				case "/auth/pending":
					pendingHits++
					w.WriteHeader(tt.pendingCode)
					_, _ = w.Write([]byte(tt.pendingBody))
				default:
					http.NotFound(w, r)
				}
			}))
			defer server.Close()

			got := checkCoordinatorEndpoints(server.Client(), []robotCoordinatorEndpoint{
				{name: "test", url: server.URL},
			})
			if len(got) != 1 {
				t.Fatalf("expected one coordinator, got %+v", got)
			}

			coord := got[0]
			if coord.Healthy != tt.wantHealthy {
				t.Fatalf("Healthy = %v, want %v (coord=%+v)", coord.Healthy, tt.wantHealthy, coord)
			}
			if coord.Pending != tt.wantPending {
				t.Fatalf("Pending = %d, want %d (coord=%+v)", coord.Pending, tt.wantPending, coord)
			}
			if tt.wantError == "" {
				if coord.Error != "" {
					t.Fatalf("Error = %q, want empty", coord.Error)
				}
			} else if !strings.Contains(coord.Error, tt.wantError) {
				t.Fatalf("Error = %q, want substring %q", coord.Error, tt.wantError)
			}
			if tt.statusCode != http.StatusOK && pendingHits != 0 {
				t.Fatalf("pending endpoint was called after unhealthy status: %d hit(s)", pendingHits)
			}
		})
	}
}

func TestCheckCoordinatorsUsesConfiguredAgentConfig(t *testing.T) {
	var statusHits int
	var pendingHits int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer coord-token" {
			t.Fatalf("Authorization = %q, want configured token", got)
		}
		switch r.URL.Path {
		case "/status":
			statusHits++
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"running":true,"backend":"tmux"}`))
		case "/auth/pending":
			pendingHits++
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`[{}]`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "agent.json")
	data := []byte(`{
  "coordinators": [
    {"name": "test-coordinator", "url": "` + server.URL + `", "token": "coord-token"}
  ]
}`)
	if err := os.WriteFile(configPath, data, 0600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	oldConfigPath := robotCoordinatorConfigPath
	robotCoordinatorConfigPath = configPath
	t.Cleanup(func() { robotCoordinatorConfigPath = oldConfigPath })

	got := checkCoordinators()
	if len(got) != 1 {
		t.Fatalf("coordinator count = %d, want 1", len(got))
	}
	coord := got[0]
	if coord.Name != "test-coordinator" || coord.URL != server.URL || !coord.Healthy || coord.Pending != 1 {
		t.Fatalf("coordinator = %+v, want configured healthy endpoint with one pending request", coord)
	}
	if statusHits != 1 || pendingHits != 1 {
		t.Fatalf("hits = status:%d pending:%d, want 1/1", statusHits, pendingHits)
	}
}

func TestSchema_AllOutputs(t *testing.T) {
	out, _, err := captureOutput(t, createTestCmd(), []string{"schema"})
	if err != nil {
		t.Fatalf("expected schema command to succeed, got error: %v", err)
	}

	var envelope struct {
		Query         string `json:"query"`
		SchemaVersion int    `json:"schema_version"`
		Schema        []struct {
			Command string                 `json:"command"`
			Aliases []string               `json:"aliases"`
			Schema  map[string]interface{} `json:"schema"`
		} `json:"schema"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &envelope); err != nil {
		t.Fatalf("invalid JSON output: %v", err)
	}
	if envelope.Query != "all" {
		t.Fatalf("expected query=all, got %q", envelope.Query)
	}
	if envelope.SchemaVersion != schemaSchemaVersion {
		t.Fatalf("unexpected schema version: %d", envelope.SchemaVersion)
	}
	if len(envelope.Schema) != 3 {
		t.Fatalf("expected 3 schema entries, got %d", len(envelope.Schema))
	}

	hasStatus := false
	for _, item := range envelope.Schema {
		if item.Command == "" || item.Schema == nil {
			t.Fatalf("expected command and schema for every entry, got %+v", item)
		}
		if item.Command == "status" {
			hasStatus = true
			assertEnvelopeSchema(t, item.Schema, jsonOutputFormatStatus)
		}
	}
	if !hasStatus {
		t.Fatal("expected status schema entry")
	}
}

func TestSchema_SingleCommandAlias(t *testing.T) {
	out, _, err := captureOutput(t, createTestCmd(), []string{"schema", "list"})
	if err != nil {
		t.Fatalf("expected schema list to succeed, got error: %v", err)
	}

	var envelope struct {
		Query  string `json:"query"`
		Schema []struct {
			Command string                 `json:"command"`
			Schema  map[string]interface{} `json:"schema"`
		} `json:"schema"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &envelope); err != nil {
		t.Fatalf("invalid JSON output: %v", err)
	}

	if envelope.Query != "list" {
		t.Fatalf("expected query=list, got %q", envelope.Query)
	}
	if len(envelope.Schema) != 1 {
		t.Fatalf("expected one schema entry, got %d", len(envelope.Schema))
	}
	if envelope.Schema[0].Command != "ls" {
		t.Fatalf("expected list alias to map to ls, got %q", envelope.Schema[0].Command)
	}
	assertEnvelopeSchema(t, envelope.Schema[0].Schema, jsonOutputFormatLS)
}

func assertEnvelopeSchema(t *testing.T, schema map[string]interface{}, outputFormat string) {
	t.Helper()

	required, ok := schema["required"].([]interface{})
	if !ok {
		t.Fatalf("schema required field has unexpected type: %T", schema["required"])
	}
	for _, field := range []string{"generated_at", "version", "output_format", "data"} {
		found := false
		for _, value := range required {
			if value == field {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("schema required fields %v missing %q", required, field)
		}
	}

	properties, ok := schema["properties"].(map[string]interface{})
	if !ok {
		t.Fatalf("schema properties field has unexpected type: %T", schema["properties"])
	}
	outputFormatSchema, ok := properties["output_format"].(map[string]interface{})
	if !ok {
		t.Fatalf("output_format schema has unexpected type: %T", properties["output_format"])
	}
	if outputFormatSchema["const"] != outputFormat {
		t.Fatalf("output_format const = %v, want %q", outputFormatSchema["const"], outputFormat)
	}
	if _, ok := properties["data"].(map[string]interface{}); !ok {
		t.Fatalf("data schema has unexpected type: %T", properties["data"])
	}
}

func TestSchema_InvalidCommand(t *testing.T) {
	_, _, err := captureOutput(t, createTestCmd(), []string{"schema", "unknown"})
	if err == nil {
		t.Fatal("expected error for unknown schema command")
	}
}

func isolateRobotCommandEnv(t *testing.T) {
	t.Helper()

	tmp := t.TempDir()
	t.Setenv("HOME", filepath.Join(tmp, "home"))
	t.Setenv("CAAM_HOME", filepath.Join(tmp, "caam-home"))
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(tmp, "xdg-config"))
	t.Setenv("XDG_DATA_HOME", filepath.Join(tmp, "xdg-data"))
	t.Setenv("CODEX_HOME", filepath.Join(tmp, "codex-home"))
	t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(tmp, "claude-config"))
	t.Setenv("GEMINI_HOME", filepath.Join(tmp, "gemini-home"))
}

func decodeRobotOutput(t *testing.T, out string) RobotOutput {
	t.Helper()

	var got RobotOutput
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &got); err != nil {
		t.Fatalf("invalid robot JSON output %q: %v", out, err)
	}
	return got
}

func robotActHelpOutput(t *testing.T) string {
	t.Helper()

	out, stderr, err := captureOutput(t, createTestCmd(), []string{"robot", "act", "--help"})
	if err != nil {
		t.Fatalf("robot act help failed: %v", err)
	}
	return out + stderr
}

func runRobotActForTest(t *testing.T, args []string) (RobotOutput, error) {
	t.Helper()

	var out bytes.Buffer
	cmd := &cobra.Command{}
	cmd.SetOut(&out)
	err := runRobotAct(cmd, args)
	return decodeRobotOutput(t, out.String()), err
}

func extractRobotActActions(text string) []string {
	matches := robotActCommandRe.FindAllStringSubmatch(text, -1)
	return uniqueSortedActions(matches)
}

func extractRobotActHelpActions(text string) []string {
	matches := robotActHelpCommandRe.FindAllStringSubmatch(text, -1)
	return uniqueSortedActions(matches)
}

func extractRobotDocsActActions(data RobotDocsData) []string {
	var text strings.Builder
	for _, topic := range data.Topics {
		for _, step := range topic.Steps {
			text.WriteString(step)
			text.WriteByte('\n')
		}
		for _, command := range topic.Commands {
			text.WriteString(command.Command)
			text.WriteByte('\n')
			text.WriteString(command.Description)
			text.WriteByte('\n')
			text.WriteString(command.Notes)
			text.WriteByte('\n')
		}
		for _, example := range topic.Examples {
			text.WriteString(example.Use)
			text.WriteByte('\n')
			text.WriteString(example.Notes)
			text.WriteByte('\n')
		}
		for _, note := range topic.Notes {
			text.WriteString(note)
			text.WriteByte('\n')
		}
	}
	return extractRobotActActions(text.String())
}

func uniqueSortedActions(matches [][]string) []string {
	seen := make(map[string]bool, len(matches))
	for _, match := range matches {
		if len(match) > 1 {
			seen[match[1]] = true
		}
	}
	actions := make([]string, 0, len(seen))
	for action := range seen {
		actions = append(actions, action)
	}
	sort.Strings(actions)
	return actions
}

func requireStringSliceEqual(t *testing.T, label string, got, want []string) {
	t.Helper()

	got = append([]string(nil), got...)
	want = append([]string(nil), want...)
	sort.Strings(got)
	sort.Strings(want)
	if len(got) != len(want) {
		t.Fatalf("%s = %v, want %v", label, got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("%s = %v, want %v", label, got, want)
		}
	}
}

func assertNoUnsupportedRobotActExamples(t *testing.T, text string) {
	t.Helper()

	for _, line := range robotActionBlockLineRe.FindAllString(text, -1) {
		if strings.Contains(line, " delete ") || strings.Contains(line, " refresh ") {
			t.Fatalf("unsupported robot act example advertised: %q", line)
		}
	}
}
