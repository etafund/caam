package tui

import (
	"strings"
	"testing"

	"github.com/Dicklesworthstone/coding_agent_account_manager/internal/project"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

func TestRenderHeaderFullIncludesBrandContextAndBreadcrumb(t *testing.T) {
	m := headerTestModel(120, 32)

	got := m.renderHeader()
	snapshot := normalizeHeaderSnapshot(got)
	want := strings.Join([]string{
		"CAAM  dev build  Profiles",
		"Provider Claude  Profile alice  Project caam -> alice",
	}, "\n")
	if snapshot != want {
		t.Fatalf("full header snapshot mismatch\nwant:\n%s\n\ngot:\n%s", want, snapshot)
	}
	assertHeaderWidth(t, got, m.width)
	logHeaderSnapshot(t, m, got)
}

func TestRenderHeaderCompactAndTinyStayWithinWidth(t *testing.T) {
	tests := []struct {
		name   string
		width  int
		height int
		want   string
	}{
		{
			name:   "compact",
			width:  72,
			height: 20,
			want:   "CAAM  dev build  Profiles  Provider Claude  Profile alice  Project caa",
		},
		{
			name:   "tiny",
			width:  44,
			height: 12,
			want:   "CAAM  dev build  Profiles  Provider Claude",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := headerTestModel(tt.width, tt.height)
			got := m.renderHeader()
			snapshot := normalizeHeaderSnapshot(got)

			if snapshot != tt.want {
				t.Fatalf("%s header snapshot mismatch\nwant:\n%s\n\ngot:\n%s", tt.name, tt.want, snapshot)
			}
			assertHeaderWidth(t, got, tt.width)
			logHeaderSnapshot(t, m, got)
		})
	}
}

func TestRenderHeaderNoColor(t *testing.T) {
	m := headerTestModel(100, 28)
	theme := NewTheme(ThemeOptions{NoColor: true})
	m.theme = theme
	m.styles = NewStyles(theme)

	got := m.renderHeader()
	if strings.Contains(got, "\x1b[") {
		t.Fatalf("no-color header should not contain ANSI escapes: %q", got)
	}
	snapshot := normalizeHeaderSnapshot(got)
	if !strings.Contains(snapshot, "CAAM") || !strings.Contains(snapshot, "dev build") || !strings.Contains(snapshot, "Provider Claude") {
		t.Fatalf("no-color header missing expected text:\n%s", snapshot)
	}
	assertHeaderWidth(t, got, m.width)
	logHeaderSnapshot(t, m, got)
}

func TestRenderHeaderStateBreadcrumbs(t *testing.T) {
	tests := []struct {
		state viewState
		want  string
	}{
		{state: stateList, want: "Profiles"},
		{state: stateSearch, want: "Profiles > Search"},
		{state: stateHelp, want: "Help"},
		{state: stateBackupDialog, want: "Profiles > Backup"},
		{state: stateCommandPalette, want: "Command"},
	}

	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			m := headerTestModel(120, 32)
			m.state = tt.state
			got := ansi.Strip(m.renderHeader())
			if !strings.Contains(got, tt.want) {
				t.Fatalf("renderHeader() missing breadcrumb %q in:\n%s", tt.want, got)
			}
		})
	}
}

func TestMainViewWithHeaderBranding(t *testing.T) {
	m := headerTestModel(110, 30)

	got := m.mainView()
	plain := ansi.Strip(got)
	if !strings.Contains(plain, "CAAM") {
		t.Fatalf("mainView() missing header brand:\n%s", plain)
	}
	if !strings.Contains(plain, "Provider Claude") {
		t.Fatalf("mainView() missing provider context:\n%s", plain)
	}
	assertHeaderWidth(t, m.renderHeader(), m.width)
}

func headerTestModel(width, height int) Model {
	m := NewWithProviders([]string{"claude", "codex"})
	m.width = width
	m.height = height
	m.cwd = "/data/projects/caam"
	m.profiles = map[string][]Profile{
		"claude": {
			{Name: "alice", Provider: "claude", IsActive: true},
			{Name: "backup", Provider: "claude"},
		},
		"codex": {
			{Name: "codex-main", Provider: "codex"},
		},
	}
	m.projectContext = &project.Resolved{
		Profiles: map[string]string{"claude": "alice"},
		Sources:  map[string]string{"claude": "/data/projects/caam"},
	}
	m.syncProfilesPanel()
	return m
}

func assertHeaderWidth(t *testing.T, rendered string, width int) {
	t.Helper()
	for _, line := range strings.Split(rendered, "\n") {
		if got := lipgloss.Width(line); got > width {
			t.Fatalf("header line width = %d, want <= %d; line=%q", got, width, ansi.Strip(line))
		}
	}
}

func normalizeHeaderSnapshot(rendered string) string {
	normalized := make([]string, 0, lipgloss.Height(rendered))
	for _, line := range strings.Split(rendered, "\n") {
		line = strings.TrimSpace(ansi.Strip(line))
		if line != "" {
			normalized = append(normalized, line)
		}
	}
	return strings.Join(normalized, "\n")
}

func logHeaderSnapshot(t *testing.T, m Model, rendered string) {
	t.Helper()
	t.Logf("header_snapshot width=%d height=%d rendered_height=%d layout_mode=%s state=%v",
		m.width,
		m.height,
		lipgloss.Height(rendered),
		headerLayoutModeName(m.layoutMode()),
		m.state,
	)
}

func headerLayoutModeName(mode layoutMode) string {
	switch mode {
	case layoutFull:
		return "full"
	case layoutCompact:
		return "compact"
	case layoutTiny:
		return "tiny"
	default:
		return "unknown"
	}
}
