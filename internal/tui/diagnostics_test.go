package tui

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"
)

func TestTUIDiagnosticsStringIncludesUsefulFieldsAndRedactsSecrets(t *testing.T) {
	sensitiveValue := strings.Join([]string{"sk", "secret", "token"}, "-")
	m := NewWithProviders([]string{"claude", "codex"})
	m.activeProvider = 1
	m.selected = 1
	m.state = stateSearch
	m.width = 120
	m.height = 32
	m.searchQuery = sensitiveValue
	m.profiles = map[string][]Profile{
		"claude": {
			{Name: "alice@example.com", Provider: "claude", IsActive: true},
		},
		"codex": {
			{Name: sensitiveValue, Provider: "codex"},
			{Name: "bob@example.com", Provider: "codex"},
		},
	}
	m.theme.NoColor = true
	m.theme.ReducedMotion = true
	m.usagePanel.Toggle()
	m.syncPanel.Toggle()

	got := m.DiagnosticsString()
	wantFields := []string{
		"tui_diagnostics",
		"provider=codex",
		"provider_count=2",
		"active_provider_index=1",
		"current_profiles=2",
		"total_profiles=3",
		"selected=1",
		"state=search",
		"width=120",
		"height=32",
		"layout=full",
		"search_active=true",
		"search_query_len=15",
		"usage_visible=true",
		"sync_visible=true",
		"no_color=true",
		"reduced_motion=true",
		"profiles_scroll_offset=0",
		"profiles_visible_rows=",
		"profiles_hovered_index=-1",
		"detail_scroll_offset=0",
		"help_scroll_offset=0",
		"focus=search",
	}
	for _, want := range wantFields {
		if !strings.Contains(got, want) {
			t.Fatalf("DiagnosticsString() missing %q in %q", want, got)
		}
	}

	for _, secret := range []string{"alice@example.com", "bob@example.com", sensitiveValue, "cwd="} {
		if strings.Contains(got, secret) {
			t.Fatalf("DiagnosticsString() leaked %q in %q", secret, got)
		}
	}
}

func TestTUIDebugDiagnosticsLogRespectsEnvAndRedactsSecrets(t *testing.T) {
	unsetEnv(t, "CAAM_TUI_DEBUG")
	unsetEnv(t, "CAAM_DEBUG")
	unsetEnv(t, "DEBUG")

	sensitiveValue := strings.Join([]string{"sk", "secret", "token"}, "-")
	logs := captureTUIDiagnosticsLoggerForTest(t)
	m := NewWithProviders([]string{"claude"})
	m.width = 72
	m.height = 20
	m.searchQuery = sensitiveValue
	m.profiles = map[string][]Profile{
		"claude": {
			{Name: "alice@example.com", Provider: "claude", IsActive: true},
		},
	}

	_, _ = m.Update(diagnosticDebugMsg{secret: sensitiveValue})
	if got := logs.String(); got != "" {
		t.Fatalf("debug diagnostics logged while disabled: %q", got)
	}

	t.Setenv("CAAM_TUI_DEBUG", "1")
	_, _ = m.Update(diagnosticDebugMsg{secret: sensitiveValue})
	got := logs.String()
	wantFields := []string{
		"tui diagnostics",
		"event=update",
		"provider=claude",
		"provider_count=1",
		"current_profiles=1",
		"total_profiles=1",
		"state=list",
		"width=72",
		"height=20",
		"layout=compact",
		"msg_type=tui.diagnosticDebugMsg",
		"profiles_scroll_offset=0",
		"profiles_visible_rows=",
		"profiles_hovered_index=-1",
		"detail_scroll_offset=0",
		"help_scroll_offset=0",
		"focus=profiles",
	}
	for _, want := range wantFields {
		if !strings.Contains(got, want) {
			t.Fatalf("debug diagnostics log missing %q in %q", want, got)
		}
	}

	for _, secret := range []string{"alice@example.com", sensitiveValue} {
		if strings.Contains(got, secret) {
			t.Fatalf("debug diagnostics log leaked %q in %q", secret, got)
		}
	}
}

func TestTUIDebugRenderTimingLogRespectsEnvAndRedactsSecrets(t *testing.T) {
	unsetEnv(t, "CAAM_TUI_DEBUG")
	unsetEnv(t, "CAAM_DEBUG")
	unsetEnv(t, "DEBUG")

	sensitiveValue := strings.Join([]string{"sk", "secret", "token"}, "-")
	logs := captureTUIDiagnosticsLoggerForTest(t)
	m := NewWithProviders([]string{"claude"})
	m.width = 100
	m.height = 30
	m.searchQuery = sensitiveValue
	m.profiles = map[string][]Profile{
		"claude": {
			{Name: "alice@example.com", Provider: "claude", IsActive: true},
			{Name: sensitiveValue, Provider: "claude"},
		},
	}
	m.syncProfilesPanel()

	_ = m.View()
	if got := logs.String(); got != "" {
		t.Fatalf("render timing logged while disabled: %q", got)
	}

	t.Setenv("CAAM_TUI_DEBUG", "1")
	_ = m.View()
	got := logs.String()

	wantFields := []string{
		"tui render timing",
		"event=view",
		"render_duration=",
		"render_duration_ms=",
		"rendered_bytes=",
		"rendered_lines=",
		"provider=claude",
		"current_profiles=2",
		"total_profiles=2",
		"state=list",
		"width=100",
		"height=30",
		"layout=full",
		"profiles_scroll_offset=0",
		"profiles_visible_rows=",
		"profiles_hovered_index=-1",
		"detail_scroll_offset=0",
		"help_scroll_offset=0",
		"focus=profiles",
	}
	for _, want := range wantFields {
		if !strings.Contains(got, want) {
			t.Fatalf("render timing log missing %q in %q", want, got)
		}
	}

	for _, secret := range []string{"alice@example.com", sensitiveValue} {
		if strings.Contains(got, secret) {
			t.Fatalf("render timing log leaked %q in %q", secret, got)
		}
	}
}

func TestTUIDebugDisabledDoesNotAlterStatusHints(t *testing.T) {
	unsetEnv(t, "CAAM_TUI_DEBUG")
	unsetEnv(t, "CAAM_DEBUG")
	unsetEnv(t, "DEBUG")

	m := New()
	m.width = 120
	layout := layoutSpec{
		Mode:          layoutFull,
		ProviderWidth: 20,
		ProfilesWidth: 60,
		DetailWidth:   34,
	}

	got := m.statusKeyHints(layout)
	if strings.Contains(got, "layout=") {
		t.Fatalf("status hints included debug layout while disabled: %q", got)
	}

	t.Setenv("CAAM_TUI_DEBUG", "1")
	got = m.statusKeyHints(layout)
	if !strings.Contains(got, "layout=full") {
		t.Fatalf("status hints did not include debug layout while enabled: %q", got)
	}
}

type diagnosticDebugMsg struct {
	secret string
}

func captureTUIDiagnosticsLoggerForTest(t *testing.T) *bytes.Buffer {
	t.Helper()

	var logs bytes.Buffer
	previous := tuiDiagnosticsLogger
	tuiDiagnosticsLogger = func() *slog.Logger {
		return slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	}
	t.Cleanup(func() {
		tuiDiagnosticsLogger = previous
	})
	return &logs
}
