package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Dicklesworthstone/coding_agent_account_manager/internal/authfile"
	caamdb "github.com/Dicklesworthstone/coding_agent_account_manager/internal/db"
	"github.com/Dicklesworthstone/coding_agent_account_manager/internal/health"
	"github.com/Dicklesworthstone/coding_agent_account_manager/internal/version"
)

func TestFormatDuration(t *testing.T) {
	tests := []struct {
		duration time.Duration
		want     string
	}{
		{30 * time.Second, "<1m"},
		{1 * time.Minute, "1m"},
		{5 * time.Minute, "5m"},
		{59 * time.Minute, "59m"},
		{1 * time.Hour, "1h 0m"},
		{1*time.Hour + 30*time.Minute, "1h 30m"},
		{2 * time.Hour, "2h 0m"},
		{2*time.Hour + 45*time.Minute, "2h 45m"},
	}

	for _, tt := range tests {
		t.Run(tt.duration.String(), func(t *testing.T) {
			got := formatDuration(tt.duration)
			if got != tt.want {
				t.Errorf("formatDuration(%v) = %q, want %q", tt.duration, got, tt.want)
			}
		})
	}
}

func TestNewHandlers(t *testing.T) {
	// Test with nil dependencies
	h := NewHandlers(nil, nil, nil)
	if h == nil {
		t.Fatal("NewHandlers() returned nil")
	}
}

func TestGetStatusWithNilDeps(t *testing.T) {
	h := NewHandlers(nil, nil, nil)

	// Should not panic with nil vault
	// Note: This will return empty tools since vault is nil
	status, err := h.GetStatus()
	if err != nil {
		t.Fatalf("GetStatus() error = %v", err)
	}
	if status == nil {
		t.Fatal("GetStatus() returned nil")
	}
	if status.Version != version.Info() {
		t.Fatalf("GetStatus() version = %q, want %q", status.Version, version.Info())
	}
	if status.Timestamp == "" {
		t.Error("GetStatus() timestamp is empty")
	}
}

func TestGetProfilesWithNilVault(t *testing.T) {
	h := NewHandlers(nil, nil, nil)

	// With nil vault, should return error when trying to list
	_, err := h.GetProfiles("")
	if err == nil {
		t.Error("GetProfiles() expected error with nil vault")
	}
}

func TestGetProfilesWithUnknownTool(t *testing.T) {
	h := NewHandlers(nil, nil, nil)

	_, err := h.GetProfiles("unknown-tool")
	if err == nil || !strings.Contains(err.Error(), "unknown tool") {
		t.Errorf("GetProfiles() expected unknown tool error, got %v", err)
	}
}

func TestGetUsageWithNilDeps(t *testing.T) {
	h := NewHandlers(nil, nil, nil)

	// Should return empty usage without error
	usage, err := h.GetUsage("")
	if err != nil {
		t.Fatalf("GetUsage() error = %v", err)
	}
	if usage == nil {
		t.Fatal("GetUsage() returned nil")
	}
	if usage.Metric != "health_error_count" {
		t.Fatalf("Metric = %q, want health_error_count", usage.Metric)
	}
	if usage.Window != "1h" {
		t.Fatalf("Window = %q, want 1h", usage.Window)
	}
	if len(usage.Entries) != 0 {
		t.Errorf("GetUsage() with nil deps should return empty, got %d entries", len(usage.Entries))
	}
}

func TestGetUsageRejectsUnknownTool(t *testing.T) {
	h := NewHandlers(nil, nil, nil)

	usage, err := h.GetUsage("unknown")
	if err == nil || !strings.Contains(err.Error(), "unknown tool") {
		t.Fatalf("GetUsage() error = %v, want unknown tool", err)
	}
	if usage != nil {
		t.Fatalf("GetUsage() usage = %+v, want nil", usage)
	}
}

func TestGetUsageReturnsHealthErrorCountersOnly(t *testing.T) {
	tmpDir := t.TempDir()
	vaultDir := filepath.Join(tmpDir, "vault")
	if err := os.MkdirAll(filepath.Join(vaultDir, "codex", "work"), 0700); err != nil {
		t.Fatalf("create profile dir: %v", err)
	}

	healthStore := health.NewStorage(filepath.Join(tmpDir, "health.json"))
	lastChecked := time.Date(2026, 7, 3, 12, 0, 0, 0, time.UTC)
	if err := healthStore.UpdateProfile("codex", "work", &health.ProfileHealth{
		ErrorCount1h: 2,
		LastChecked:  lastChecked,
	}); err != nil {
		t.Fatalf("UpdateProfile: %v", err)
	}

	h := NewHandlers(authfile.NewVault(vaultDir), healthStore, nil)
	usage, err := h.GetUsage("codex")
	if err != nil {
		t.Fatalf("GetUsage() error = %v", err)
	}
	if usage.Metric != "health_error_count" || usage.Window != "1h" {
		t.Fatalf("usage metadata = %s/%s, want health_error_count/1h", usage.Metric, usage.Window)
	}
	if len(usage.Entries) != 1 {
		t.Fatalf("GetUsage() entries = %d, want 1", len(usage.Entries))
	}
	entry := usage.Entries[0]
	if entry.Tool != "codex" || entry.Profile != "work" {
		t.Fatalf("usage entry = %s/%s, want codex/work", entry.Tool, entry.Profile)
	}
	if entry.HealthErrorCount1h != 2 {
		t.Fatalf("HealthErrorCount1h = %d, want 2", entry.HealthErrorCount1h)
	}
	if entry.LastChecked != lastChecked.Format(time.RFC3339) {
		t.Fatalf("LastChecked = %q, want %q", entry.LastChecked, lastChecked.Format(time.RFC3339))
	}

	body, err := json.Marshal(usage)
	if err != nil {
		t.Fatalf("Marshal usage: %v", err)
	}
	if strings.Contains(string(body), "total_calls") {
		t.Fatalf("usage JSON contains untracked total_calls field: %s", body)
	}
	if strings.Contains(string(body), "last_used") {
		t.Fatalf("usage JSON contains misleading last_used field: %s", body)
	}
}

func TestGetActivityWithNilDB(t *testing.T) {
	h := NewHandlers(nil, nil, nil)

	activity, err := h.GetActivity(20)
	if err != nil {
		t.Fatalf("GetActivity() error = %v", err)
	}
	if activity == nil {
		t.Fatal("GetActivity() returned nil")
	}
	if len(activity.Events) != 0 || activity.Count != 0 {
		t.Fatalf("GetActivity() = %+v, want empty", activity)
	}
}

func TestGetActivityReturnsRedactedRecentEvents(t *testing.T) {
	db, err := caamdb.OpenAt(filepath.Join(t.TempDir(), "activity.db"))
	if err != nil {
		t.Fatalf("OpenAt: %v", err)
	}
	defer db.Close()

	ts := time.Date(2026, 7, 3, 13, 0, 0, 0, time.UTC)
	privateMarker := "must-not-leak"
	if err := db.LogEvent(caamdb.Event{
		Timestamp:   ts,
		Type:        caamdb.EventActivate,
		Provider:    "codex",
		ProfileName: "work",
		Details:     map[string]any{"private_detail": privateMarker},
		Duration:    2 * time.Minute,
	}); err != nil {
		t.Fatalf("LogEvent: %v", err)
	}

	h := NewHandlers(nil, nil, db)
	activity, err := h.GetActivity(10)
	if err != nil {
		t.Fatalf("GetActivity() error = %v", err)
	}
	if activity.Count != 1 || len(activity.Events) != 1 {
		t.Fatalf("activity = %+v, want one event", activity)
	}
	event := activity.Events[0]
	if event.Timestamp != ts.Format(time.RFC3339) {
		t.Fatalf("Timestamp = %q, want %q", event.Timestamp, ts.Format(time.RFC3339))
	}
	if event.Type != caamdb.EventActivate || event.Tool != "codex" || event.Profile != "work" {
		t.Fatalf("event identity = %+v, want activate codex/work", event)
	}
	if event.Message != "Activated codex/work" {
		t.Fatalf("Message = %q, want activated message", event.Message)
	}
	if event.DurationSeconds != 120 {
		t.Fatalf("DurationSeconds = %d, want 120", event.DurationSeconds)
	}
	body, err := json.Marshal(activity)
	if err != nil {
		t.Fatalf("Marshal activity: %v", err)
	}
	if strings.Contains(string(body), privateMarker) || strings.Contains(string(body), "details") {
		t.Fatalf("activity JSON leaked raw details: %s", body)
	}
}

func TestGetCoordinators(t *testing.T) {
	h := NewHandlers(nil, nil, nil)

	coords, err := h.GetCoordinators(context.Background())
	if err != nil {
		t.Fatalf("GetCoordinators() error = %v", err)
	}
	if len(coords.Coordinators) != 0 {
		t.Fatalf("GetCoordinators() = %+v, want empty configured list", coords.Coordinators)
	}
}

func TestGetCoordinatorsQueriesConfiguredEndpoint(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/status" {
			t.Fatalf("path = %s, want /status", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer token-1" {
			t.Fatalf("Authorization = %q, want configured bearer token", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"running":true,"backend":"tmux","pane_count":3,"pending_auths":2}`))
	}))
	defer server.Close()

	h := NewHandlers(nil, nil, nil)
	h.SetCoordinatorHTTPClient(server.Client())
	h.SetCoordinatorEndpoints([]CoordinatorEndpoint{{
		ID:       "csd",
		Endpoint: server.URL,
		Token:    "token-1",
	}})

	coords, err := h.GetCoordinators(context.Background())
	if err != nil {
		t.Fatalf("GetCoordinators() error = %v", err)
	}
	if len(coords.Coordinators) != 1 {
		t.Fatalf("coordinator count = %d, want 1", len(coords.Coordinators))
	}
	coord := coords.Coordinators[0]
	if coord.ID != "csd" || coord.Endpoint != server.URL || coord.Status != "healthy" || coord.Backend != "tmux" {
		t.Fatalf("coordinator = %+v, want healthy csd/tmux at test URL", coord)
	}
	if coord.PaneCount != 3 || coord.PendingAuths != 2 {
		t.Fatalf("counts = pane:%d pending:%d, want 3/2", coord.PaneCount, coord.PendingAuths)
	}
	if coord.LastSeen == "" {
		t.Fatal("LastSeen empty, want probe timestamp")
	}
}

func TestActivateWithUnknownTool(t *testing.T) {
	h := NewHandlers(nil, nil, nil)

	req := ActivateRequest{
		Tool:    "unknown",
		Profile: "test",
	}

	_, err := h.Activate(req)
	if err == nil {
		t.Error("Activate() expected error for unknown tool")
	}
}

func TestActivateWithMissingProfile(t *testing.T) {
	h := NewHandlers(nil, nil, nil)

	req := ActivateRequest{
		Tool:    "codex",
		Profile: "",
	}

	_, err := h.Activate(req)
	if err == nil || !strings.Contains(err.Error(), "profile is required") {
		t.Errorf("Activate() expected profile required error, got %v", err)
	}
}

func TestBackupWithUnknownTool(t *testing.T) {
	h := NewHandlers(nil, nil, nil)

	req := BackupRequest{
		Tool:    "unknown",
		Profile: "test",
	}

	_, err := h.Backup(req)
	if err == nil {
		t.Error("Backup() expected error for unknown tool")
	}
}

func TestBackupWithMissingProfile(t *testing.T) {
	h := NewHandlers(nil, nil, nil)

	req := BackupRequest{
		Tool:    "codex",
		Profile: "",
	}

	_, err := h.Backup(req)
	if err == nil || !strings.Contains(err.Error(), "profile is required") {
		t.Errorf("Backup() expected profile required error, got %v", err)
	}
}

func TestDeleteProfileWithUnknownTool(t *testing.T) {
	h := NewHandlers(nil, nil, nil)

	err := h.DeleteProfile("unknown", "test")
	if err == nil || !strings.Contains(err.Error(), "unknown tool") {
		t.Errorf("DeleteProfile() expected unknown tool error, got %v", err)
	}
}

func TestDeleteProfileWithMissingProfile(t *testing.T) {
	h := NewHandlers(nil, nil, nil)

	err := h.DeleteProfile("codex", "")
	if err == nil || !strings.Contains(err.Error(), "profile is required") {
		t.Errorf("DeleteProfile() expected profile required error, got %v", err)
	}
}

func TestDeleteProfileWithNilVault(t *testing.T) {
	h := NewHandlers(nil, nil, nil)

	err := h.DeleteProfile("codex", "test")
	if err == nil {
		t.Error("DeleteProfile() expected error with nil vault")
	}
}

func TestGetProfileWithUnknownTool(t *testing.T) {
	h := NewHandlers(nil, nil, nil)

	_, err := h.GetProfile("unknown-tool", "test")
	if err == nil || !strings.Contains(err.Error(), "unknown tool") {
		t.Errorf("GetProfile() expected unknown tool error, got %v", err)
	}
}

func TestGetProfileWithNilVault(t *testing.T) {
	h := NewHandlers(nil, nil, nil)

	_, err := h.GetProfile("codex", "test")
	if err == nil {
		t.Error("GetProfile() expected error with nil vault")
	}
}

func TestGetProfileHealthWithNilStore(t *testing.T) {
	h := NewHandlers(nil, nil, nil)

	// Should return nil without panic
	health := h.getProfileHealth("claude", "test")
	if health != nil {
		t.Errorf("getProfileHealth() with nil store should return nil, got %v", health)
	}
}

func TestGetProfileIdentityWithNilVault(t *testing.T) {
	h := NewHandlers(nil, nil, nil)

	// Should return nil without panic
	id := h.getProfileIdentity("claude", "test")
	if id != nil {
		t.Errorf("getProfileIdentity() with nil vault should return nil, got %v", id)
	}
}

func TestToolsMapContainsExpectedTools(t *testing.T) {
	expectedTools := []string{"codex", "claude", "gemini"}

	for _, tool := range expectedTools {
		if _, ok := tools[tool]; !ok {
			t.Errorf("tools map missing %q", tool)
		}
	}
}
