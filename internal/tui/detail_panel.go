package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/Dicklesworthstone/coding_agent_account_manager/internal/health"
	"github.com/charmbracelet/lipgloss"
)

// DetailInfo represents the detailed information for a profile.
type DetailInfo struct {
	Name         string
	Provider     string
	AuthMode     string
	LoggedIn     bool
	Locked       bool
	Path         string
	CreatedAt    time.Time
	LastUsedAt   time.Time
	Account      string
	Description  string // Free-form notes about this profile's purpose
	BrowserCmd   string
	BrowserProf  string
	HealthStatus health.HealthStatus
	TokenExpiry  time.Time
	ErrorCount   int
	Penalty      float64
}

// DetailPanel renders the right panel showing profile details and available actions.
type DetailPanel struct {
	profile      *DetailInfo
	scrollOffset int
	width        int
	height       int
	styles       DetailPanelStyles
}

// DetailPanelStyles holds the styles for the detail panel.
type DetailPanelStyles struct {
	Border         lipgloss.Style
	Title          lipgloss.Style
	Label          lipgloss.Style
	Value          lipgloss.Style
	ValueNumeric   lipgloss.Style // Right-aligned numeric values
	StatusOK       lipgloss.Style
	StatusWarn     lipgloss.Style
	StatusBad      lipgloss.Style
	StatusMuted    lipgloss.Style
	LockIcon       lipgloss.Style
	Divider        lipgloss.Style
	ActionHeader   lipgloss.Style
	ActionKey      lipgloss.Style
	ActionDesc     lipgloss.Style
	Empty          lipgloss.Style
	SectionHeader  lipgloss.Style // Header for grouped sections
	SectionDivider lipgloss.Style // Subtle divider between sections
}

// DefaultDetailPanelStyles returns the default styles for the detail panel.
func DefaultDetailPanelStyles() DetailPanelStyles {
	return NewDetailPanelStyles(DefaultTheme())
}

// NewDetailPanelStyles returns themed styles for the detail panel.
func NewDetailPanelStyles(theme Theme) DetailPanelStyles {
	p := theme.Palette
	keycap := keycapStyle(theme, true).Width(8).Align(lipgloss.Center)

	return DetailPanelStyles{
		Border: lipgloss.NewStyle().
			Border(theme.Border).
			BorderForeground(p.BorderMuted).
			Background(p.Surface).
			Padding(0, 1),

		Title: lipgloss.NewStyle().
			Bold(true).
			Foreground(p.Accent).
			MarginBottom(1),

		Label: lipgloss.NewStyle().
			Foreground(p.Muted).
			Width(12),

		Value: lipgloss.NewStyle().
			Foreground(p.Text),

		ValueNumeric: lipgloss.NewStyle().
			Foreground(p.Text).
			Align(lipgloss.Right),

		StatusOK: lipgloss.NewStyle().
			Foreground(p.Success).
			Bold(true),

		StatusWarn: lipgloss.NewStyle().
			Foreground(p.Warning),

		StatusBad: lipgloss.NewStyle().
			Foreground(p.Danger),

		StatusMuted: lipgloss.NewStyle().
			Foreground(p.Muted),

		LockIcon: lipgloss.NewStyle().
			Foreground(p.Warning),

		Divider: lipgloss.NewStyle().
			Foreground(p.BorderMuted),

		ActionHeader: lipgloss.NewStyle().
			Bold(true).
			Foreground(p.Info).
			MarginTop(1).
			MarginBottom(1),

		ActionKey: keycap,

		ActionDesc: lipgloss.NewStyle().
			Foreground(p.Muted),

		Empty: lipgloss.NewStyle().
			Foreground(p.Muted).
			Italic(true).
			Padding(2, 2),

		SectionHeader: lipgloss.NewStyle().
			Bold(true).
			Foreground(p.Accent).
			MarginTop(1),

		SectionDivider: lipgloss.NewStyle().
			Foreground(p.BorderMuted).
			MarginTop(1),
	}
}

// NewDetailPanel creates a new detail panel.
func NewDetailPanel() *DetailPanel {
	return NewDetailPanelWithTheme(DefaultTheme())
}

// NewDetailPanelWithTheme creates a new detail panel using a theme.
func NewDetailPanelWithTheme(theme Theme) *DetailPanel {
	return &DetailPanel{
		styles: NewDetailPanelStyles(theme),
	}
}

// SetProfile sets the profile to display.
func (p *DetailPanel) SetProfile(profile *DetailInfo) {
	if !sameDetailProfile(p.profile, profile) {
		p.scrollOffset = 0
	}
	p.profile = profile
	p.clampScrollOffset()
}

// SetSize sets the panel dimensions.
func (p *DetailPanel) SetSize(width, height int) {
	p.width = width
	p.height = height
	p.clampScrollOffset()
}

// ScrollUp scrolls long detail content upward.
func (p *DetailPanel) ScrollUp(lines int) {
	if p == nil || lines <= 0 {
		return
	}
	p.scrollOffset = max(0, p.scrollOffset-lines)
}

// ScrollDown scrolls long detail content downward.
func (p *DetailPanel) ScrollDown(lines int) {
	if p == nil || lines <= 0 {
		return
	}
	p.scrollOffset = min(p.maxScrollOffset(), p.scrollOffset+lines)
}

// View renders the detail panel with grouped sections.
func (p *DetailPanel) View() string {
	if p.profile == nil {
		empty := p.styles.Empty.Render("Select a profile to view details")
		style := p.styles.Border
		if p.width > 0 {
			style = style.Width(p.width - 2)
		}
		if p.height > 0 {
			style = style.Height(max(1, p.height-2))
		}
		return style.Render(empty)
	}

	inner := p.renderContent()
	inner = p.scrolledContent(inner)

	// Apply border
	style := p.styles.Border
	if p.width > 0 {
		style = style.Width(p.width - 2)
	}
	if p.height > 0 {
		style = style.Height(max(1, p.height-2))
	}
	return style.Render(inner)
}

func (p *DetailPanel) renderContent() string {
	prof := p.profile
	dividerWidth := p.width - 6
	if dividerWidth < 20 {
		dividerWidth = 20
	}
	thinDivider := p.styles.SectionDivider.Render(strings.Repeat("─", dividerWidth))

	// Title
	title := p.styles.Title.Render(fmt.Sprintf("Profile: %s", prof.Name))

	var sections []string

	// ═══ PROFILE SECTION ═══
	profileHeader := p.styles.SectionHeader.Render("Profile")
	var profileRows []string
	profileRows = append(profileRows, p.renderRow("Provider", capitalizeFirst(prof.Provider)))
	if prof.Account != "" {
		profileRows = append(profileRows, p.renderRow("Account", prof.Account))
	}
	if prof.Description != "" {
		profileRows = append(profileRows, p.renderRow("Notes", prof.Description))
	}
	sections = append(sections, lipgloss.JoinVertical(lipgloss.Left,
		profileHeader,
		lipgloss.JoinVertical(lipgloss.Left, profileRows...),
	))

	// ═══ AUTH SECTION ═══
	authHeader := p.styles.SectionHeader.Render("Auth")
	var authRows []string
	authRows = append(authRows, p.renderRow("Mode", prof.AuthMode))

	// Status with icon and text
	statusText := prof.HealthStatus.Icon() + " " + prof.HealthStatus.String()
	var statusStyle lipgloss.Style
	switch prof.HealthStatus {
	case health.StatusHealthy:
		statusStyle = p.styles.StatusOK
	case health.StatusWarning:
		statusStyle = p.styles.StatusWarn
	case health.StatusCritical:
		statusStyle = p.styles.StatusBad
	default:
		statusStyle = p.styles.StatusMuted
	}
	authRows = append(authRows, p.renderRow("Status", statusStyle.Render(statusText)))

	// Token Expiry
	if !prof.TokenExpiry.IsZero() {
		ttl := time.Until(prof.TokenExpiry)
		expiryStr := ""
		if ttl < 0 {
			expiryStr = p.styles.StatusBad.Render("Expired")
		} else {
			expiryStr = fmt.Sprintf("Expires in %s", formatDurationFull(ttl))
		}
		authRows = append(authRows, p.renderRow("Token", expiryStr))
	}

	// Lock status
	if prof.Locked {
		authRows = append(authRows, p.renderRow("Lock", p.styles.LockIcon.Render("🔒 Locked")))
	}

	sections = append(sections, lipgloss.JoinVertical(lipgloss.Left,
		thinDivider,
		authHeader,
		lipgloss.JoinVertical(lipgloss.Left, authRows...),
	))

	// ═══ USAGE SECTION ═══
	usageHeader := p.styles.SectionHeader.Render("Usage")
	var usageRows []string

	// Errors (numeric, right-aligned conceptually but we show context)
	if prof.ErrorCount > 0 {
		errorStr := fmt.Sprintf("%d in last hour", prof.ErrorCount)
		if prof.ErrorCount >= 3 {
			errorStr = p.styles.StatusBad.Render(errorStr)
		} else {
			errorStr = p.styles.StatusWarn.Render(errorStr)
		}
		usageRows = append(usageRows, p.renderRow("Errors", errorStr))
	} else {
		usageRows = append(usageRows, p.renderRow("Errors", p.styles.StatusOK.Render("None")))
	}

	// Penalty (numeric value)
	if prof.Penalty > 0 {
		penaltyStr := fmt.Sprintf("%.2f", prof.Penalty)
		usageRows = append(usageRows, p.renderRow("Penalty", penaltyStr))
	}

	// Last used
	if !prof.LastUsedAt.IsZero() {
		usageRows = append(usageRows, p.renderRow("Last used", formatRelativeTime(prof.LastUsedAt)))
	} else {
		usageRows = append(usageRows, p.renderRow("Last used", "never"))
	}

	// Created
	if !prof.CreatedAt.IsZero() {
		usageRows = append(usageRows, p.renderRow("Created", prof.CreatedAt.Format("2006-01-02")))
	}

	sections = append(sections, lipgloss.JoinVertical(lipgloss.Left,
		thinDivider,
		usageHeader,
		lipgloss.JoinVertical(lipgloss.Left, usageRows...),
	))

	// ═══ PATHS SECTION ═══
	var pathRows []string

	// Path (truncate if too long)
	pathDisplay := prof.Path
	maxPathLen := p.width - 16
	if maxPathLen > 0 && len(pathDisplay) > maxPathLen {
		pathDisplay = "~" + pathDisplay[len(pathDisplay)-maxPathLen+1:]
	}
	if pathDisplay != "" {
		pathRows = append(pathRows, p.renderRow("Path", pathDisplay))
	}

	// Browser config
	if prof.BrowserCmd != "" || prof.BrowserProf != "" {
		browserStr := prof.BrowserCmd
		if prof.BrowserProf != "" {
			if browserStr != "" {
				browserStr += " (" + prof.BrowserProf + ")"
			} else {
				browserStr = prof.BrowserProf
			}
		}
		pathRows = append(pathRows, p.renderRow("Browser", browserStr))
	}

	if len(pathRows) > 0 {
		pathsHeader := p.styles.SectionHeader.Render("Paths")
		sections = append(sections, lipgloss.JoinVertical(lipgloss.Left,
			thinDivider,
			pathsHeader,
			lipgloss.JoinVertical(lipgloss.Left, pathRows...),
		))
	}

	// ═══ ACTIONS SECTION ═══
	divider := p.styles.Divider.Render(strings.Repeat("─", dividerWidth))
	actionsHeader := p.styles.ActionHeader.Render("Actions")

	actions := []struct {
		key  string
		desc string
	}{
		{"Enter", "Activate profile"},
		{"l", "Login/refresh"},
		{"e", "Edit profile"},
		{"o", "Open in browser"},
		{"d", "Delete profile"},
		{"/", "Search profiles"},
	}

	var actionRows []string
	for _, action := range actions {
		key := p.styles.ActionKey.Render(action.key)
		desc := p.styles.ActionDesc.Render(action.desc)
		actionRows = append(actionRows, fmt.Sprintf("%s %s", key, desc))
	}
	actionsContent := lipgloss.JoinVertical(lipgloss.Left, actionRows...)

	// Combine all sections
	allSections := []string{title}
	allSections = append(allSections, sections...)
	allSections = append(allSections, "", divider, actionsHeader, actionsContent)

	return lipgloss.JoinVertical(lipgloss.Left, allSections...)
}

func (p *DetailPanel) visibleContentHeight() int {
	if p == nil || p.height <= 0 {
		return 0
	}
	return max(1, p.height-2)
}

func (p *DetailPanel) maxScrollOffset() int {
	if p == nil || p.profile == nil {
		return 0
	}
	return p.maxScrollOffsetForContent(p.renderContent())
}

func (p *DetailPanel) maxScrollOffsetForContent(content string) int {
	height := p.visibleContentHeight()
	if height <= 0 {
		return 0
	}
	return max(0, len(strings.Split(content, "\n"))-height)
}

func (p *DetailPanel) clampScrollOffset() {
	if p == nil {
		return
	}
	p.scrollOffset = max(0, min(p.scrollOffset, p.maxScrollOffset()))
}

func (p *DetailPanel) scrolledContent(content string) string {
	height := p.visibleContentHeight()
	if height <= 0 {
		return content
	}

	lines := strings.Split(content, "\n")
	maxOffset := p.maxScrollOffsetForContent(content)
	p.scrollOffset = max(0, min(p.scrollOffset, maxOffset))
	start := p.scrollOffset
	end := min(len(lines), start+height)
	return strings.Join(lines[start:end], "\n")
}

func sameDetailProfile(a, b *DetailInfo) bool {
	if a == nil || b == nil {
		return a == b
	}
	return a.Provider == b.Provider && a.Name == b.Name
}

// formatDurationFull formats duration for details view.
func formatDurationFull(d time.Duration) string {
	if d < time.Minute {
		return "less than a minute"
	}
	if d < time.Hour {
		return fmt.Sprintf("%d minutes", int(d.Minutes()))
	}
	hours := int(d.Hours())
	minutes := int(d.Minutes()) % 60
	return fmt.Sprintf("%d hours %d minutes", hours, minutes)
}

// renderRow renders a label-value row.
func (p *DetailPanel) renderRow(label, value string) string {
	labelStr := p.styles.Label.Render(label + ":")
	valueStr := p.styles.Value.Render(value)
	return labelStr + " " + valueStr
}
