package cmd

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Dicklesworthstone/coding_agent_account_manager/internal/coordinator"
)

func TestLoadCoordinatorConfig(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "coordinator.json")

	data := []byte(`{
  "port": 9999,
  "poll_interval": "750ms",
  "auth_timeout": "45s",
  "state_timeout": "15s",
  "resume_prompt": "resume now",
  "output_lines": 55,
  "backend": "tmux"
}`)
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, port, err := loadCoordinatorConfig(path)
	if err != nil {
		t.Fatalf("loadCoordinatorConfig error: %v", err)
	}
	if port != 9999 {
		t.Fatalf("port = %d, want 9999", port)
	}
	if cfg.PollInterval != 750*time.Millisecond {
		t.Fatalf("PollInterval = %v, want 750ms", cfg.PollInterval)
	}
	if cfg.AuthTimeout != 45*time.Second {
		t.Fatalf("AuthTimeout = %v, want 45s", cfg.AuthTimeout)
	}
	if cfg.StateTimeout != 15*time.Second {
		t.Fatalf("StateTimeout = %v, want 15s", cfg.StateTimeout)
	}
	if cfg.ResumePrompt != "resume now" {
		t.Fatalf("ResumePrompt = %q, want %q", cfg.ResumePrompt, "resume now")
	}
	if cfg.OutputLines != 55 {
		t.Fatalf("OutputLines = %d, want 55", cfg.OutputLines)
	}
	if cfg.Backend != coordinator.BackendTmux {
		t.Fatalf("Backend = %s, want %s", cfg.Backend, coordinator.BackendTmux)
	}
}

func TestCoordinatorStatusCommandExposesConnectionFlags(t *testing.T) {
	for _, name := range []string{"port", "auth-token"} {
		if coordinatorStatusCmd.Flags().Lookup(name) == nil {
			t.Fatalf("status command missing --%s flag", name)
		}
	}
}

func TestFetchCoordinatorStatusQueriesRealEndpoint(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/status" {
			t.Fatalf("path = %s, want /status", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer secret" {
			t.Fatalf("Authorization = %q, want bearer token", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"running": true,
			"backend": "tmux",
			"pane_count": 2,
			"pending_auths": 1,
			"panes": [
				{"pane_id": 7, "state": "waiting_for_auth", "request_id": "req-1", "account": "work"}
			]
		}`))
	}))
	defer server.Close()

	status, err := fetchCoordinatorStatus(context.Background(), server.Client(), testServerPort(t, server.URL), "secret")
	if err != nil {
		t.Fatalf("fetchCoordinatorStatus error: %v", err)
	}
	if !status.Running {
		t.Fatal("Running = false, want true")
	}
	if status.Backend != "tmux" {
		t.Fatalf("Backend = %q, want tmux", status.Backend)
	}
	if status.PaneCount != 2 || status.PendingAuths != 1 {
		t.Fatalf("counts = panes:%d pending:%d, want 2/1", status.PaneCount, status.PendingAuths)
	}
	if len(status.Panes) != 1 || status.Panes[0].PaneID != 7 {
		t.Fatalf("panes = %+v, want pane 7", status.Panes)
	}
}

func TestFetchCoordinatorStatusUsesEnvTokenFallback(t *testing.T) {
	t.Setenv("CAAM_COORDINATOR_TOKEN", "env-secret")

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer env-secret" {
			t.Fatalf("Authorization = %q, want env token", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"running":true,"backend":"wezterm"}`))
	}))
	defer server.Close()

	status, err := fetchCoordinatorStatus(context.Background(), server.Client(), testServerPort(t, server.URL), "")
	if err != nil {
		t.Fatalf("fetchCoordinatorStatus error: %v", err)
	}
	if !status.Running || status.Backend != "wezterm" {
		t.Fatalf("status = %+v, want running wezterm", status)
	}
}

func TestFetchCoordinatorStatusReportsNonOK(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "busy", http.StatusServiceUnavailable)
	}))
	defer server.Close()

	_, err := fetchCoordinatorStatus(context.Background(), server.Client(), testServerPort(t, server.URL), "")
	if err == nil {
		t.Fatal("expected non-OK coordinator status error")
	}
	if !strings.Contains(err.Error(), "HTTP 503") || !strings.Contains(err.Error(), "busy") {
		t.Fatalf("error = %q, want HTTP status and response body", err.Error())
	}
}

func TestFetchCoordinatorStatusReportsClientError(t *testing.T) {
	_, err := fetchCoordinatorStatus(context.Background(), coordinatorStatusClientFunc(func(*http.Request) (*http.Response, error) {
		return nil, context.DeadlineExceeded
	}), 4321, "")
	if err == nil {
		t.Fatal("expected client error")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("error = %v, want wrapped deadline exceeded", err)
	}
	if !strings.Contains(err.Error(), "coordinator status unavailable at http://127.0.0.1:4321/status") {
		t.Fatalf("error = %q, want coordinator status URL", err.Error())
	}
}

type coordinatorStatusClientFunc func(*http.Request) (*http.Response, error)

func (f coordinatorStatusClientFunc) Do(req *http.Request) (*http.Response, error) {
	return f(req)
}

func testServerPort(t *testing.T, rawURL string) int {
	t.Helper()

	u, err := url.Parse(rawURL)
	if err != nil {
		t.Fatalf("parse server URL: %v", err)
	}
	port, err := strconv.Atoi(u.Port())
	if err != nil {
		t.Fatalf("parse server port %q: %v", u.Port(), err)
	}
	return port
}
