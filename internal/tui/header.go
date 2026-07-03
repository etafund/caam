package tui

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/Dicklesworthstone/coding_agent_account_manager/internal/version"
	"github.com/charmbracelet/lipgloss"
)

func (m Model) renderHeader() string {
	width := m.width
	if width <= 0 {
		width = 80
	}

	mode := m.layoutMode()
	lines := []string{m.renderHeaderPrimary(mode, width)}
	if mode == layoutFull {
		lines = append(lines, m.renderHeaderSecondary(width))
	}

	return m.styles.Header.Render(strings.Join(lines, "\n"))
}

func (m Model) renderHeaderPrimary(mode layoutMode, width int) string {
	segments := []string{
		m.styles.HeaderBrand.Render("CAAM"),
		m.styles.HeaderSubtle.Render(m.headerVersionLabel()),
		m.styles.HeaderCrumb.Render(m.headerBreadcrumb()),
	}

	if mode != layoutFull {
		segments = append(segments,
			m.styles.HeaderContext.Render(m.headerProviderLabel()),
			m.styles.HeaderContext.Render(m.headerProfileLabel()),
			m.styles.HeaderSubtle.Render(m.headerProjectLabel()),
		)
	}

	return fitHeaderLine(joinHeaderSegments(segments), width)
}

func (m Model) renderHeaderSecondary(width int) string {
	segments := []string{
		m.styles.HeaderContext.Render(m.headerProviderLabel()),
		m.styles.HeaderContext.Render(m.headerProfileLabel()),
		m.styles.HeaderSubtle.Render(m.headerProjectLabel()),
	}
	return fitHeaderLine(joinHeaderSegments(segments), width)
}

func (m Model) headerVersionLabel() string {
	if version.Short() == "" || version.Short() == "dev" {
		return "dev build"
	}
	return "v" + version.Short()
}

func (m Model) headerBreadcrumb() string {
	parts := []string{"Profiles"}

	switch m.state {
	case stateSearch:
		parts = append(parts, "Search")
	case stateHelp:
		return "Help"
	case stateConfirm:
		parts = append(parts, "Confirm")
	case stateBackupDialog:
		parts = append(parts, "Backup")
	case stateConfirmOverwrite:
		parts = append(parts, "Backup", "Overwrite")
	case stateExportConfirm:
		parts = append(parts, "Export")
	case stateImportPath:
		parts = append(parts, "Import", "Path")
	case stateImportConfirm:
		parts = append(parts, "Import", "Confirm")
	case stateEditProfile:
		parts = append(parts, "Edit")
	case stateSyncAdd:
		parts = append(parts, "Sync", "Add")
	case stateSyncEdit:
		parts = append(parts, "Sync", "Edit")
	case stateCommandPalette:
		return "Command"
	}

	return strings.Join(parts, " > ")
}

func (m Model) headerProviderLabel() string {
	provider := m.currentProvider()
	if provider == "" {
		return "Provider none"
	}
	return "Provider " + capitalizeFirst(provider)
}

func (m Model) headerProfileLabel() string {
	name := m.selectedProfileNameValue()
	if name == "" {
		return "Profile none"
	}
	return "Profile " + name
}

func (m Model) headerProjectLabel() string {
	if m.cwd == "" {
		return "Project none"
	}

	provider := m.currentProvider()
	if provider != "" && m.projectContext != nil {
		profile := m.projectContext.Profiles[provider]
		source := m.projectContext.Sources[provider]
		if profile != "" && source != "" && source != "<default>" {
			return fmt.Sprintf("Project %s -> %s", filepath.Base(source), profile)
		}
	}

	return "Project " + filepath.Base(m.cwd)
}

func joinHeaderSegments(segments []string) string {
	kept := make([]string, 0, len(segments))
	for _, segment := range segments {
		if strings.TrimSpace(lipgloss.NewStyle().Render(segment)) != "" {
			kept = append(kept, segment)
		}
	}
	return strings.Join(kept, "  ")
}

func fitHeaderLine(line string, width int) string {
	if width <= 0 {
		return line
	}
	contentWidth := width - 2 // Header style uses one column of left/right padding.
	if contentWidth <= 0 {
		contentWidth = width
	}
	if lipgloss.Width(line) <= contentWidth {
		return line
	}
	return cutANSI(line, 0, contentWidth)
}
