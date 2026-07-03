package tui

import (
	"context"
	"fmt"
	"log/slog"
)

// TUIDiagnostics is a sanitized snapshot of TUI state for debugging.
type TUIDiagnostics struct {
	Provider            string
	ProviderCount       int
	ActiveProviderIndex int
	CurrentProfiles     int
	TotalProfiles       int
	SelectedIndex       int
	State               string
	Width               int
	Height              int
	Layout              string
	SearchActive        bool
	SearchQueryLength   int
	UsageVisible        bool
	SyncVisible         bool
	NoColor             bool
	ReducedMotion       bool
}

var tuiDiagnosticsLogger = func() *slog.Logger {
	return slog.Default()
}

// Diagnostics returns non-sensitive TUI state useful for support and tests.
func (m Model) Diagnostics() TUIDiagnostics {
	totalProfiles := 0
	for _, profiles := range m.profiles {
		totalProfiles += len(profiles)
	}

	currentProfiles := 0
	if provider := m.currentProvider(); provider != "" {
		currentProfiles = len(m.profiles[provider])
	}

	return TUIDiagnostics{
		Provider:            m.currentProvider(),
		ProviderCount:       len(m.providers),
		ActiveProviderIndex: m.activeProvider,
		CurrentProfiles:     currentProfiles,
		TotalProfiles:       totalProfiles,
		SelectedIndex:       m.selected,
		State:               viewStateName(m.state),
		Width:               m.width,
		Height:              m.height,
		Layout:              diagnosticLayoutModeName(m.layoutMode()),
		SearchActive:        m.state == stateSearch,
		SearchQueryLength:   len(m.searchQuery),
		UsageVisible:        m.usagePanel != nil && m.usagePanel.Visible(),
		SyncVisible:         m.syncPanel != nil && m.syncPanel.Visible(),
		NoColor:             m.theme.NoColor,
		ReducedMotion:       m.theme.ReducedMotion,
	}
}

// DiagnosticsString returns a stable, sanitized one-line diagnostics summary.
func (m Model) DiagnosticsString() string {
	d := m.Diagnostics()
	return fmt.Sprintf(
		"tui_diagnostics provider=%s provider_count=%d active_provider_index=%d current_profiles=%d total_profiles=%d selected=%d state=%s width=%d height=%d layout=%s search_active=%t search_query_len=%d usage_visible=%t sync_visible=%t no_color=%t reduced_motion=%t",
		d.Provider,
		d.ProviderCount,
		d.ActiveProviderIndex,
		d.CurrentProfiles,
		d.TotalProfiles,
		d.SelectedIndex,
		d.State,
		d.Width,
		d.Height,
		d.Layout,
		d.SearchActive,
		d.SearchQueryLength,
		d.UsageVisible,
		d.SyncVisible,
		d.NoColor,
		d.ReducedMotion,
	)
}

func (m Model) logDebugDiagnostics(event string, msg any) {
	if !tuiDebugEnabled() {
		return
	}

	d := m.Diagnostics()
	logger := tuiDiagnosticsLogger()
	if logger == nil {
		return
	}
	logger.LogAttrs(
		context.Background(),
		slog.LevelDebug,
		"tui diagnostics",
		slog.String("event", event),
		slog.String("msg_type", fmt.Sprintf("%T", msg)),
		slog.String("provider", d.Provider),
		slog.Int("provider_count", d.ProviderCount),
		slog.Int("active_provider_index", d.ActiveProviderIndex),
		slog.Int("current_profiles", d.CurrentProfiles),
		slog.Int("total_profiles", d.TotalProfiles),
		slog.Int("selected", d.SelectedIndex),
		slog.String("state", d.State),
		slog.Int("width", d.Width),
		slog.Int("height", d.Height),
		slog.String("layout", d.Layout),
		slog.Bool("search_active", d.SearchActive),
		slog.Int("search_query_len", d.SearchQueryLength),
		slog.Bool("usage_visible", d.UsageVisible),
		slog.Bool("sync_visible", d.SyncVisible),
		slog.Bool("no_color", d.NoColor),
		slog.Bool("reduced_motion", d.ReducedMotion),
	)
}

func tuiDebugEnabled() bool {
	return envBool("CAAM_TUI_DEBUG") || envBool("CAAM_DEBUG") || envBool("DEBUG")
}

func viewStateName(state viewState) string {
	switch state {
	case stateList:
		return "list"
	case stateDetail:
		return "detail"
	case stateConfirm:
		return "confirm"
	case stateSearch:
		return "search"
	case stateHelp:
		return "help"
	case stateBackupDialog:
		return "backup_dialog"
	case stateConfirmOverwrite:
		return "confirm_overwrite"
	case stateExportConfirm:
		return "export_confirm"
	case stateImportPath:
		return "import_path"
	case stateImportConfirm:
		return "import_confirm"
	case stateEditProfile:
		return "edit_profile"
	case stateSyncAdd:
		return "sync_add"
	case stateSyncEdit:
		return "sync_edit"
	case stateCommandPalette:
		return "command_palette"
	default:
		return "unknown"
	}
}

func diagnosticLayoutModeName(mode layoutMode) string {
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
