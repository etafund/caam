package tui

import (
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Dicklesworthstone/coding_agent_account_manager/internal/health"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

// ProfileInfo represents a profile with all displayable information.
type ProfileInfo struct {
	Name           string
	Badge          string
	ProjectDefault bool
	AuthMode       string
	LoggedIn       bool
	Locked         bool
	LastUsed       time.Time
	Account        string
	Description    string // Free-form notes about this profile's purpose
	IsActive       bool
	HealthStatus   health.HealthStatus
	TokenExpiry    time.Time
	ErrorCount     int
	Penalty        float64
	SyncStatus     string // Optional sync state: synced, syncing, pending, error
	SyncDetail     string // Optional short detail such as target machine or error summary
}

// ProfilesPanel renders the center panel showing profiles for the selected provider.
type ProfilesPanel struct {
	provider     string
	profiles     []ProfileInfo
	selected     int
	scrollOffset int
	hoverIndex   int
	width        int
	height       int
	styles       ProfilesPanelStyles
}

// ProfilesPanelStyles holds the styles for the profiles panel.
type ProfilesPanelStyles struct {
	Border          lipgloss.Style
	Title           lipgloss.Style
	Header          lipgloss.Style
	Row             lipgloss.Style
	RowAlt          lipgloss.Style // Zebra stripe - alternate row background
	SelectedRow     lipgloss.Style
	HoveredRow      lipgloss.Style
	ActiveIndicator lipgloss.Style
	StatusOK        lipgloss.Style
	StatusWarn      lipgloss.Style
	StatusBad       lipgloss.Style
	StatusMuted     lipgloss.Style
	LockIcon        lipgloss.Style
	ProjectBadge    lipgloss.Style
	Empty           lipgloss.Style
	// Row anatomy styles
	RowIcon         lipgloss.Style // Left icon area
	RowLabel        lipgloss.Style // Primary label (profile name)
	RowMetadata     lipgloss.Style // Secondary metadata (auth mode, last used)
	StatusBadge     lipgloss.Style // Status chip with padding
	StatusBadgeOK   lipgloss.Style
	StatusBadgeWarn lipgloss.Style
	StatusBadgeBad  lipgloss.Style
	RowSeparator    lipgloss.Style // Subtle separator between rows
}

// DefaultProfilesPanelStyles returns the default styles for the profiles panel.
func DefaultProfilesPanelStyles() ProfilesPanelStyles {
	return NewProfilesPanelStyles(DefaultTheme())
}

// NewProfilesPanelStyles returns themed styles for the profiles panel.
func NewProfilesPanelStyles(theme Theme) ProfilesPanelStyles {
	p := theme.Palette

	return ProfilesPanelStyles{
		Border: lipgloss.NewStyle().
			Border(theme.Border).
			BorderForeground(p.BorderMuted).
			Background(p.Surface).
			Padding(0, 1),

		Title: lipgloss.NewStyle().
			Bold(true).
			Foreground(p.Accent).
			MarginBottom(1),

		Header: lipgloss.NewStyle().
			Bold(true).
			Foreground(p.Muted).
			BorderStyle(lipgloss.NormalBorder()).
			BorderBottom(true).
			BorderForeground(p.BorderMuted),

		Row: lipgloss.NewStyle().
			Foreground(p.Text),

		RowAlt: lipgloss.NewStyle().
			Foreground(p.Text).
			Background(p.SurfaceMuted),

		SelectedRow: lipgloss.NewStyle().
			Foreground(p.Text).
			Bold(true).
			Background(p.Selection),

		HoveredRow: lipgloss.NewStyle().
			Foreground(p.Text).
			Bold(true).
			Background(p.SurfaceMuted),

		ActiveIndicator: lipgloss.NewStyle().
			Foreground(p.Success).
			Bold(true),

		StatusOK: lipgloss.NewStyle().
			Foreground(p.Success),

		StatusWarn: lipgloss.NewStyle().
			Foreground(p.Warning),

		StatusBad: lipgloss.NewStyle().
			Foreground(p.Danger),

		StatusMuted: lipgloss.NewStyle().
			Foreground(p.Muted),

		LockIcon: lipgloss.NewStyle().
			Foreground(p.Warning),

		ProjectBadge: lipgloss.NewStyle().
			Foreground(p.Info).
			Bold(true),

		Empty: lipgloss.NewStyle().
			Foreground(p.Muted).
			Italic(true).
			Padding(2, 2),

		// Row anatomy styles
		RowIcon: lipgloss.NewStyle().
			Width(2),

		RowLabel: lipgloss.NewStyle().
			Foreground(p.Text).
			Bold(true),

		RowMetadata: lipgloss.NewStyle().
			Foreground(p.Muted),

		// Status badge styles with consistent padding and rounded appearance
		StatusBadge: lipgloss.NewStyle().
			Padding(0, 1),

		StatusBadgeOK: lipgloss.NewStyle().
			Foreground(p.Success).
			Background(p.Surface).
			Padding(0, 1).
			Bold(true),

		StatusBadgeWarn: lipgloss.NewStyle().
			Foreground(p.Warning).
			Background(p.Surface).
			Padding(0, 1).
			Bold(true),

		StatusBadgeBad: lipgloss.NewStyle().
			Foreground(p.Danger).
			Background(p.Surface).
			Padding(0, 1).
			Bold(true),

		RowSeparator: lipgloss.NewStyle().
			Foreground(p.BorderMuted),
	}
}

// StatusStyle returns the style for a given health status.
func (s ProfilesPanelStyles) StatusStyle(status health.HealthStatus) lipgloss.Style {
	switch status {
	case health.StatusHealthy:
		return s.StatusOK
	case health.StatusWarning:
		return s.StatusWarn
	case health.StatusCritical:
		return s.StatusBad
	default:
		return s.StatusMuted
	}
}

// statusBadgeStyle returns the badge style for a given health status.
func (p *ProfilesPanel) statusBadgeStyle(status health.HealthStatus) lipgloss.Style {
	switch status {
	case health.StatusHealthy:
		return p.styles.StatusBadgeOK
	case health.StatusWarning:
		return p.styles.StatusBadgeWarn
	case health.StatusCritical:
		return p.styles.StatusBadgeBad
	default:
		return p.styles.StatusBadge.Foreground(p.styles.StatusMuted.GetForeground())
	}
}

// truncateWithEllipsis truncates a string and adds ellipsis if needed.
func truncateWithEllipsis(s string, maxWidth int) string {
	if maxWidth <= 0 {
		return ""
	}
	if lipgloss.Width(s) <= maxWidth {
		return s
	}
	if maxWidth <= 3 {
		return s[:maxWidth]
	}
	// Use runes to properly handle Unicode
	runes := []rune(s)
	for i := len(runes) - 1; i >= 0; i-- {
		candidate := string(runes[:i]) + "..."
		if lipgloss.Width(candidate) <= maxWidth {
			return candidate
		}
	}
	return "..."
}

// formatTUIStatus formats the health status string.
func formatTUIStatus(pi *ProfileInfo) string {
	icon := pi.HealthStatus.Icon()

	if pi.TokenExpiry.IsZero() {
		return icon + " " + formatStatusLabel(pi.HealthStatus)
	}

	ttl := time.Until(pi.TokenExpiry)
	if ttl <= 0 {
		return icon + " Expired"
	}

	return icon + " " + formatDuration(ttl)
}

func formatStatusLabel(status health.HealthStatus) string {
	label := status.String()
	if label == "" {
		return "Unknown"
	}
	return strings.ToUpper(label[:1]) + label[1:]
}

// formatDuration formats a duration concisely for TUI.
func formatDuration(d time.Duration) string {
	if d < time.Hour {
		return fmt.Sprintf("%dm left", int(d.Minutes()))
	}
	if d < 24*time.Hour {
		return fmt.Sprintf("%dh left", int(d.Hours()))
	}
	return fmt.Sprintf("%dd left", int(d.Hours()/24))
}

// NewProfilesPanel creates a new profiles panel.
func NewProfilesPanel() *ProfilesPanel {
	return NewProfilesPanelWithTheme(DefaultTheme())
}

// NewProfilesPanelWithTheme creates a new profiles panel using a theme.
func NewProfilesPanelWithTheme(theme Theme) *ProfilesPanel {
	return &ProfilesPanel{
		profiles:   []ProfileInfo{},
		hoverIndex: -1,
		styles:     NewProfilesPanelStyles(theme),
	}
}

// SetProvider sets the currently displayed provider.
func (p *ProfilesPanel) SetProvider(provider string) {
	p.provider = provider
}

// SetProfiles sets the profiles to display, sorted by last used.
func (p *ProfilesPanel) SetProfiles(profiles []ProfileInfo) {
	// Sort by last used (most recent first), then by name
	sorted := make([]ProfileInfo, len(profiles))
	copy(sorted, profiles)
	sort.Slice(sorted, func(i, j int) bool {
		if sorted[i].LastUsed.Equal(sorted[j].LastUsed) {
			return sorted[i].Name < sorted[j].Name
		}
		return sorted[i].LastUsed.After(sorted[j].LastUsed)
	})
	p.profiles = sorted

	// Reset selection if out of bounds
	if p.selected >= len(p.profiles) {
		p.selected = max(0, len(p.profiles)-1)
	}
	if p.hoverIndex >= len(p.profiles) {
		p.hoverIndex = -1
	}
	p.ensureSelectedVisible()
}

// SetSelected sets the currently selected profile index.
func (p *ProfilesPanel) SetSelected(index int) {
	if index >= 0 && index < len(p.profiles) {
		p.selected = index
		p.ensureSelectedVisible()
	}
}

// SetSelectedByName sets the selected profile by name.
// Returns true if the profile was found.
func (p *ProfilesPanel) SetSelectedByName(name string) bool {
	for i := range p.profiles {
		if p.profiles[i].Name == name {
			p.selected = i
			p.ensureSelectedVisible()
			return true
		}
	}
	return false
}

// Count returns the number of profiles in the panel.
func (p *ProfilesPanel) Count() int {
	return len(p.profiles)
}

// GetSelected returns the currently selected profile index.
func (p *ProfilesPanel) GetSelected() int {
	return p.selected
}

// GetSelectedProfile returns the currently selected profile, or nil if none.
func (p *ProfilesPanel) GetSelectedProfile() *ProfileInfo {
	if p.selected >= 0 && p.selected < len(p.profiles) {
		return &p.profiles[p.selected]
	}
	return nil
}

// SetHoveredIndex sets the row currently under the mouse without changing selection.
func (p *ProfilesPanel) SetHoveredIndex(index int) {
	if p == nil {
		return
	}
	if index < 0 || index >= len(p.profiles) {
		p.hoverIndex = -1
		return
	}
	p.hoverIndex = index
}

// SetHoveredVisibleRow sets the hovered row from a visible row offset.
func (p *ProfilesPanel) SetHoveredVisibleRow(row int) {
	if p == nil || row < 0 || row >= p.visibleRowCapacity() {
		p.SetHoveredIndex(-1)
		return
	}
	p.SetHoveredIndex(p.scrollOffset + row)
}

// MoveUp moves selection up.
func (p *ProfilesPanel) MoveUp() {
	if p.selected > 0 {
		p.selected--
	}
	p.ensureSelectedVisible()
}

// MoveDown moves selection down.
func (p *ProfilesPanel) MoveDown() {
	if p.selected < len(p.profiles)-1 {
		p.selected++
	}
	p.ensureSelectedVisible()
}

// ScrollUp moves selection upward by the requested number of rows.
func (p *ProfilesPanel) ScrollUp(rows int) {
	if p == nil || rows <= 0 {
		return
	}
	p.selected = max(0, p.selected-rows)
	p.ensureSelectedVisible()
}

// ScrollDown moves selection downward by the requested number of rows.
func (p *ProfilesPanel) ScrollDown(rows int) {
	if p == nil || rows <= 0 {
		return
	}
	p.selected = min(len(p.profiles)-1, p.selected+rows)
	p.ensureSelectedVisible()
}

// SetSize sets the panel dimensions.
func (p *ProfilesPanel) SetSize(width, height int) {
	p.width = width
	p.height = height
	p.ensureSelectedVisible()
}

func (p *ProfilesPanel) visibleRowCapacity() int {
	if p == nil {
		return 0
	}
	if p.height <= 0 {
		return len(p.profiles)
	}

	// Border top/bottom, title margin, and header border consume fixed space.
	return max(1, p.height-6)
}

func (p *ProfilesPanel) maxScrollOffset() int {
	return max(0, len(p.profiles)-p.visibleRowCapacity())
}

func (p *ProfilesPanel) ensureSelectedVisible() {
	if p == nil {
		return
	}
	if len(p.profiles) == 0 {
		p.selected = 0
		p.scrollOffset = 0
		return
	}

	p.selected = max(0, min(p.selected, len(p.profiles)-1))
	capacity := p.visibleRowCapacity()
	if p.selected < p.scrollOffset {
		p.scrollOffset = p.selected
	}
	if p.selected >= p.scrollOffset+capacity {
		p.scrollOffset = p.selected - capacity + 1
	}
	p.scrollOffset = max(0, min(p.scrollOffset, p.maxScrollOffset()))
}

// View renders the profiles panel.
func (p *ProfilesPanel) View() string {
	// Title
	title := p.styles.Title.Render(capitalizeFirst(p.provider) + " Profiles")

	if len(p.profiles) == 0 {
		empty := p.styles.Empty.Render(emptyProfilesMessage(p.provider))
		inner := lipgloss.JoinVertical(lipgloss.Left, title, empty)
		if p.width > 0 {
			return p.styles.Border.Width(p.width - 2).Render(inner)
		}
		return p.styles.Border.Render(inner)
	}

	availableWidth := p.width
	if availableWidth > 0 {
		availableWidth = availableWidth - 4
	}

	layout := "full"
	if availableWidth > 0 {
		switch {
		case availableWidth < 56:
			layout = "narrow"
		case availableWidth < 80:
			layout = "compact"
		}
	}

	colWidths := struct {
		name     int
		auth     int
		status   int
		sync     int
		lastUsed int
		account  int
	}{
		name:     18,
		auth:     8,
		status:   14,
		sync:     13,
		lastUsed: 12,
		account:  16,
	}

	showSync := p.hasSyncStatus() && layout != "narrow"
	switch layout {
	case "compact":
		colWidths.name = 22
		colWidths.status = 14
		colWidths.sync = 12
		colWidths.lastUsed = 12
		colWidths.auth = 0
		colWidths.account = 0
	case "narrow":
		colWidths.name = 26
		colWidths.status = 12
		colWidths.lastUsed = 0
		colWidths.auth = 0
		colWidths.account = 0
	}

	columnCount := 2
	if layout == "full" {
		columnCount = 5
	} else if layout == "compact" {
		columnCount = 3
	}
	if showSync {
		columnCount++
	}

	sumWidths := colWidths.name + colWidths.status
	if showSync {
		sumWidths += colWidths.sync
	}
	if layout == "full" {
		sumWidths += colWidths.auth + colWidths.lastUsed + colWidths.account
	} else if layout == "compact" {
		sumWidths += colWidths.lastUsed
	}
	sumWidths += columnCount - 1
	if availableWidth > 0 && sumWidths > availableWidth {
		reduce := sumWidths - availableWidth
		minName := 12
		if layout == "full" {
			minName = 10
		}
		if colWidths.name-reduce < minName {
			colWidths.name = minName
		} else {
			colWidths.name -= reduce
		}
	}

	// Header row
	headerCells := []string{padRight("Name", colWidths.name)}
	if layout == "full" {
		headerCells = append(headerCells, padRight("Auth", colWidths.auth))
	}
	headerCells = append(headerCells, padRight("Status", colWidths.status))
	if showSync {
		headerCells = append(headerCells, padRight("Sync", colWidths.sync))
	}
	if layout != "narrow" {
		headerCells = append(headerCells, padRight("Last Used", colWidths.lastUsed))
	}
	if layout == "full" {
		headerCells = append(headerCells, padRight("Account", colWidths.account))
	}
	header := p.styles.Header.Render(strings.Join(headerCells, " "))

	// Profile rows with zebra striping
	var rows []string
	p.ensureSelectedVisible()
	start := p.scrollOffset
	end := min(len(p.profiles), start+p.visibleRowCapacity())
	for i := start; i < end; i++ {
		prof := p.profiles[i]
		// Left icon indicator for active profile
		indicator := "  "
		if prof.IsActive {
			indicator = p.styles.ActiveIndicator.Render("● ")
		}

		// Status badge with icon and consistent styling
		statusText := formatTUIStatus(&prof)
		statusBadgeStyle := p.statusBadgeStyle(prof.HealthStatus)
		if prof.Locked && layout == "full" {
			statusText += " " + p.styles.LockIcon.Render("🔒")
		}

		// Last used - relative time (right-aligned in display)
		lastUsed := formatRelativeTime(prof.LastUsed)

		// Account (truncate with ellipsis if needed)
		account := prof.Account
		if account == "" {
			account = "-"
		}
		account = truncateWithEllipsis(account, colWidths.account)

		// Build row cells with proper padding
		// Row anatomy: [Icon] [Label] [Metadata...] [Status Badge]
		paddedName := padRight(formatNameWithBadge(prof.Name, prof.Badge, colWidths.name-2), colWidths.name-2)
		paddedStatusText := padRight(statusText, colWidths.status)
		renderedStatus := statusBadgeStyle.Render(paddedStatusText)
		syncText, syncStyle := p.formatProfileSyncStatus(prof, colWidths.sync)

		rowParts := []string{indicator + paddedName}
		if layout == "full" {
			// Auth mode as secondary metadata
			rowParts = append(rowParts, p.styles.RowMetadata.Render(padRight(prof.AuthMode, colWidths.auth)))
		}
		rowParts = append(rowParts, renderedStatus)
		if showSync {
			rowParts = append(rowParts, syncStyle.Render(padRight(syncText, colWidths.sync)))
		}
		if layout != "narrow" {
			// Right-aligned time value
			rowParts = append(rowParts, p.styles.RowMetadata.Render(padRight(lastUsed, colWidths.lastUsed)))
		}
		if layout == "full" {
			rowParts = append(rowParts, p.styles.RowMetadata.Render(padRight(account, colWidths.account)))
		}

		rowStr := strings.Join(rowParts, " ")
		if prof.ProjectDefault && layout == "full" {
			rowStr += " " + p.styles.ProjectBadge.Render("[PROJECT DEFAULT]")
		}

		// Apply row style with zebra striping
		var style lipgloss.Style
		if i == p.selected {
			style = p.styles.SelectedRow
		} else if i == p.hoverIndex {
			style = p.styles.HoveredRow
		} else if i%2 == 1 {
			// Alternate rows get subtle background
			style = p.styles.RowAlt
		} else {
			style = p.styles.Row
		}
		rows = append(rows, style.Render(rowStr))
	}

	// Combine header and rows
	content := lipgloss.JoinVertical(lipgloss.Left, append([]string{header}, rows...)...)

	// Combine title and content
	inner := lipgloss.JoinVertical(lipgloss.Left, title, content)

	// Apply border
	if p.width > 0 {
		return p.styles.Border.Width(p.width - 2).Render(inner)
	}
	return p.styles.Border.Render(inner)
}

func (p *ProfilesPanel) hasSyncStatus() bool {
	for _, prof := range p.profiles {
		if prof.SyncStatus != "" || prof.SyncDetail != "" {
			return true
		}
	}
	return false
}

func (p *ProfilesPanel) formatProfileSyncStatus(prof ProfileInfo, width int) (string, lipgloss.Style) {
	label := strings.TrimSpace(prof.SyncStatus)
	detail := strings.TrimSpace(prof.SyncDetail)
	if label == "" && detail == "" {
		return "-", p.styles.StatusMuted
	}

	style := p.styles.StatusMuted
	switch strings.ToLower(label) {
	case "synced", "ok", "complete":
		label = "🟢 synced"
		style = p.styles.StatusOK
	case "syncing", "active", "running":
		label = "🔄 syncing"
		style = p.styles.StatusWarn
	case "pending", "queued", "dirty":
		label = "⚠️ pending"
		style = p.styles.StatusWarn
	case "error", "failed":
		label = "🔴 error"
		style = p.styles.StatusBad
	default:
		if label == "" {
			label = detail
			detail = ""
		}
	}

	text := label
	if detail != "" {
		text += " " + detail
	}
	return truncateWithEllipsis(text, width), style
}

func emptyProfilesMessage(provider string) string {
	label := capitalizeFirst(provider)
	return fmt.Sprintf("📭 No profiles for %s yet\n\nRun: caam backup %s <email>", label, provider)
}

// formatRelativeTime formats a time as a relative string (e.g., "2h ago", "1d ago").
func formatRelativeTime(t time.Time) string {
	if t.IsZero() {
		return "never"
	}

	duration := time.Since(t)

	switch {
	case duration < time.Minute:
		return "now"
	case duration < time.Hour:
		mins := int(duration.Minutes())
		return fmt.Sprintf("%dm ago", mins)
	case duration < 24*time.Hour:
		hours := int(duration.Hours())
		return fmt.Sprintf("%dh ago", hours)
	case duration < 7*24*time.Hour:
		days := int(duration.Hours() / 24)
		return fmt.Sprintf("%dd ago", days)
	case duration < 30*24*time.Hour:
		weeks := int(duration.Hours() / (24 * 7))
		return fmt.Sprintf("%dw ago", weeks)
	default:
		months := int(duration.Hours() / (24 * 30))
		if months == 0 {
			months = 1
		}
		return fmt.Sprintf("%dmo ago", months)
	}
}

// padRight pads a string to the right with spaces.
// Uses lipgloss.Width for proper visual width handling (emojis, CJK).
func padRight(s string, width int) string {
	w := lipgloss.Width(s)
	if w >= width {
		return s
	}
	return s + strings.Repeat(" ", width-w)
}

// truncate truncates a string to the given width in runes.
// Uses rune handling for proper Unicode support.
func truncate(s string, width int) string {
	runes := []rune(s)
	if len(runes) <= width {
		return s
	}
	if width <= 3 {
		return string(runes[:width])
	}
	return string(runes[:width-3]) + "..."
}

func formatNameWithBadge(name, badge string, width int) string {
	if badge == "" {
		return truncate(name, width)
	}
	if width <= 0 {
		return ""
	}

	badgePlain := ansi.Strip(badge)
	badgeRunes := utf8.RuneCountInString(badgePlain)
	if badgeRunes >= width {
		return truncate(badgePlain, width)
	}

	nameWidth := width - 1 - badgeRunes
	if nameWidth < 0 {
		nameWidth = 0
	}

	return truncate(name, nameWidth) + " " + badge
}
