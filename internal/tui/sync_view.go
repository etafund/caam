package tui

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/Dicklesworthstone/coding_agent_account_manager/internal/sync"
	"github.com/charmbracelet/lipgloss"
)

// View renders the sync panel.
func (p *SyncPanel) View() string {
	if p == nil {
		return ""
	}

	title := p.styles.Title.Render("Sync Pool")
	statusLine := p.renderStatusLines()

	if p.loading {
		var body string
		if p.loadingSpinner != nil {
			body = p.loadingSpinner.View()
		} else {
			body = p.styles.Empty.Render("Loading sync state...")
		}
		return p.render(title, statusLine, body)
	}

	if p.syncing {
		body := p.renderSyncProgress()
		return p.render(title, statusLine, body)
	}

	body := p.renderMachineList()
	return p.render(title, statusLine, body)
}

func (p *SyncPanel) renderStatusLines() string {
	if p.state == nil || p.state.Pool == nil {
		return p.styles.StatusDisabled.Render("Status: Not configured")
	}

	var status string
	if p.state.Pool.Enabled {
		status = p.styles.StatusEnabled.Render("Status: Enabled")
	} else {
		status = p.styles.StatusDisabled.Render("Status: Disabled")
	}
	if p.state.Pool.AutoSync {
		status += "  " + p.styles.KeyHint.Render("[Auto-sync on]")
	}
	if !p.state.Pool.LastFullSync.IsZero() {
		status += "  " + p.styles.KeyHint.Render("Last full sync: "+formatTimeAgo(p.state.Pool.LastFullSync))
	}

	summary := p.renderQueueSummary()
	if summary == "" {
		return status
	}
	return lipgloss.JoinVertical(lipgloss.Left, status, summary)
}

// renderMachineList renders the machine list.
func (p *SyncPanel) renderMachineList() string {
	if len(p.machines) == 0 {
		empty := p.styles.Empty.Render(
			"No machines configured.\n\n" +
				"Use 'caam sync add <name> <address>' to add a machine.\n" +
				"Or press [a] to add one interactively.",
		)
		return lipgloss.JoinVertical(lipgloss.Left, empty, p.renderKeyHints())
	}

	var rows []string
	rows = append(rows, p.styles.KeyHint.Render("Machines:"))
	for i, m := range p.machines {
		row := p.renderMachineRow(m, i == p.selectedIdx)
		rows = append(rows, row)
	}

	sections := []string{strings.Join(rows, "\n")}
	if details := p.renderSelectedMachineDetails(); details != "" {
		sections = append(sections, details)
	}
	if log := p.renderSyncLog(); log != "" {
		sections = append(sections, log)
	}
	sections = append(sections, p.renderKeyHints())

	return lipgloss.JoinVertical(lipgloss.Left, sections...)
}

// renderMachineRow renders a single machine row.
func (p *SyncPanel) renderMachineRow(m *sync.Machine, selected bool) string {
	statusIcon := getStatusIcon(m.Status)
	style := p.styles.Machine
	if selected {
		style = p.styles.SelectedMachine
	}

	name := truncateString(m.Name, 20)
	addr := m.Address
	if m.Port != 0 && m.Port != sync.DefaultSSHPort {
		addr = fmt.Sprintf("%s:%d", m.Address, m.Port)
	}
	addr = truncateString(addr, 25)

	lastSync := "never"
	if !m.LastSync.IsZero() {
		lastSync = formatTimeAgo(m.LastSync)
	}
	summary := p.machineStatusSummary(m, lastSync)
	selector := "  "
	if selected {
		selector = "> "
	}

	row := fmt.Sprintf("%s%s %-20s  %-25s  %-18s", selector, statusIcon, name, addr, summary)

	// Add error message if present
	if m.Status == sync.StatusError && m.LastError != "" {
		errMsg := truncateString(m.LastError, 40)
		row += "\n      " + p.styles.StatusError.Render(errMsg)
	}

	return style.Render(row)
}

func (p *SyncPanel) renderSelectedMachineDetails() string {
	m := p.SelectedMachine()
	if m == nil {
		return ""
	}

	user := valueOrDash(m.SSHUser)
	keyPath := valueOrDash(m.SSHKeyPath)
	remotePath := valueOrDash(m.RemotePath)
	added := formatDetailTime(m.AddedAt)
	lastSync := formatDetailTime(m.LastSync)
	if m.LastSync.IsZero() {
		lastSync = "never"
	}

	lines := []string{
		"",
		p.styles.KeyHint.Render("Selected machine:"),
		fmt.Sprintf("  Address:     %s", m.HostPort()),
		fmt.Sprintf("  SSH user:    %s", user),
		fmt.Sprintf("  SSH key:     %s", keyPath),
		fmt.Sprintf("  Remote path: %s", remotePath),
		fmt.Sprintf("  Status:      %s", p.machineStatusSummary(m, formatTimeAgo(m.LastSync))),
		fmt.Sprintf("  Added:       %s", added),
		fmt.Sprintf("  Last sync:   %s", lastSync),
		fmt.Sprintf("  Source:      %s", valueOrDash(m.Source)),
	}

	if counts := p.profileCountsForMachine(m); len(counts) > 0 {
		lines = append(lines, "  Profiles synced:")
		for _, provider := range sortedCountKeys(counts) {
			lines = append(lines, fmt.Sprintf("    %s: %d", provider, counts[provider]))
		}
	}

	if queued := p.queueEntriesForMachine(m); len(queued) > 0 {
		lines = append(lines, "  Pending profiles:")
		for i, entry := range queued {
			if i >= 3 {
				lines = append(lines, fmt.Sprintf("    ... %d more", len(queued)-i))
				break
			}
			lines = append(lines, fmt.Sprintf("    %s/%s (%s)", entry.Provider, entry.Profile, queueEntryStatus(entry)))
		}
	}

	return strings.Join(lines, "\n")
}

func (p *SyncPanel) renderSyncLog() string {
	if p.state == nil {
		return ""
	}
	entries := p.state.RecentHistory(5)
	if len(entries) == 0 {
		return ""
	}

	lines := []string{"", p.styles.KeyHint.Render("Recent sync activity:")}
	for _, entry := range entries {
		icon := "✓"
		style := p.styles.StatusOnline
		if !entry.Success {
			icon = "!"
			style = p.styles.StatusError
		}
		profile := truncateString(entry.Provider+"/"+entry.Profile, 28)
		target := truncateString(entry.Machine, 16)
		line := fmt.Sprintf("  %s  %-8s %-28s %-8s %s",
			formatLogTime(entry.Timestamp),
			target,
			profile,
			entry.Action,
			icon,
		)
		if !entry.Success && entry.Error != "" {
			line += " " + truncateString(entry.Error, 28)
		}
		lines = append(lines, style.Render(line))
	}
	return strings.Join(lines, "\n")
}

func (p *SyncPanel) renderSyncProgress() string {
	var lines []string
	if p.syncingSpinner != nil {
		lines = append(lines, p.syncingSpinner.View())
	} else {
		lines = append(lines, p.styles.StatusSyncing.Render("Syncing..."))
	}

	if len(p.machines) == 0 {
		return strings.Join(lines, "\n")
	}

	lines = append(lines, "")
	for _, m := range p.machines {
		label := truncateString(m.Name, 20)
		if strings.TrimSpace(label) == "" {
			label = truncateString(m.HostPort(), 20)
		}
		status := p.machineStatusSummary(m, formatTimeAgo(m.LastSync))
		if p.syncing {
			status = "syncing..."
		}
		lines = append(lines, fmt.Sprintf(
			"%s %-20s %s",
			getStatusIcon(m.Status),
			label,
			status,
		))
		for _, item := range p.machineProgressItems(m, 3) {
			lines = append(lines, "  "+item)
		}
	}

	lines = append(lines, "", p.renderKeyHints())
	return strings.Join(lines, "\n")
}

func (p *SyncPanel) renderQueueSummary() string {
	if p.state == nil || p.state.Queue == nil {
		return ""
	}

	pending := len(p.state.Queue.Entries)
	failed := 0
	for _, entry := range p.state.Queue.Entries {
		if entry.LastError != "" {
			failed++
		}
	}

	online, offline, syncing := 0, 0, 0
	for _, m := range p.machines {
		switch m.Status {
		case sync.StatusOnline:
			online++
		case sync.StatusOffline, sync.StatusError:
			offline++
		case sync.StatusSyncing:
			syncing++
		}
	}

	return p.styles.KeyHint.Render(fmt.Sprintf(
		"Machines: %d online | %d syncing | %d offline/error | Pending: %d | Failed: %d | Queue: %d",
		online,
		syncing,
		offline,
		pending,
		failed,
		pending,
	))
}

func (p *SyncPanel) machineStatusSummary(m *sync.Machine, lastSync string) string {
	switch m.Status {
	case sync.StatusOnline:
		if m.LastSync.IsZero() {
			return "online, never synced"
		}
		return "synced " + lastSync
	case sync.StatusSyncing:
		return "syncing now"
	case sync.StatusOffline:
		return "offline"
	case sync.StatusError:
		if m.LastErrorAt.IsZero() {
			return "error"
		}
		return "error " + formatTimeAgo(m.LastErrorAt)
	default:
		if p.machineQueueCount(m) > 0 {
			return "pending sync"
		}
		return "unknown"
	}
}

func (p *SyncPanel) machineProgressItems(m *sync.Machine, limit int) []string {
	var items []string
	for _, entry := range p.queueEntriesForMachine(m) {
		items = append(items, fmt.Sprintf("→ %s/%s (%s)", entry.Provider, entry.Profile, queueEntryStatus(entry)))
		if len(items) == limit {
			return items
		}
	}
	if p.state == nil {
		return items
	}
	for _, entry := range p.state.RecentHistory(10) {
		if !matchesMachine(m, entry.Machine) {
			continue
		}
		icon := "✓"
		if !entry.Success {
			icon = "!"
		}
		items = append(items, fmt.Sprintf("%s %s/%s (%s)", icon, entry.Provider, entry.Profile, entry.Action))
		if len(items) == limit {
			break
		}
	}
	return items
}

func (p *SyncPanel) queueEntriesForMachine(m *sync.Machine) []sync.QueueEntry {
	if p.state == nil || p.state.Queue == nil || m == nil {
		return nil
	}
	var entries []sync.QueueEntry
	for _, entry := range p.state.Queue.Entries {
		if matchesMachine(m, entry.Machine) {
			entries = append(entries, entry)
		}
	}
	return entries
}

func (p *SyncPanel) machineQueueCount(m *sync.Machine) int {
	return len(p.queueEntriesForMachine(m))
}

func (p *SyncPanel) profileCountsForMachine(m *sync.Machine) map[string]int {
	if p.state == nil || m == nil {
		return nil
	}
	seen := make(map[string]struct{})
	counts := make(map[string]int)
	for _, entry := range p.state.RecentHistory(100) {
		if !entry.Success || !matchesMachine(m, entry.Machine) {
			continue
		}
		key := entry.Provider + "\x00" + entry.Profile
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		counts[entry.Provider]++
	}
	return counts
}

func (p *SyncPanel) renderKeyHints() string {
	return p.styles.KeyHint.Render("[a]dd  [e]dit CSV  [m]achine  [r]emove  [t]est  [s]ync selected  [ctrl+s] sync now  [l]og  [esc] close")
}

// render renders the full panel with title, status, and body.
func (p *SyncPanel) render(title, status, body string) string {
	// Render breadcrumb for navigation context
	contentWidth := p.width - 6 // Account for border and padding
	if contentWidth < 40 {
		contentWidth = 40
	}
	breadcrumb := RenderBreadcrumb("Sync", p.theme, contentWidth)

	inner := lipgloss.JoinVertical(lipgloss.Left, breadcrumb, title, status, "", body)
	if p.width > 0 {
		return p.styles.Border.Width(p.width - 2).Height(p.height - 2).Render(inner)
	}
	return p.styles.Border.Render(inner)
}

func matchesMachine(m *sync.Machine, ref string) bool {
	if m == nil {
		return false
	}
	return ref == m.ID || ref == m.Name || ref == m.Address || ref == m.HostPort()
}

func queueEntryStatus(entry sync.QueueEntry) string {
	if entry.LastError != "" {
		return "retry: " + truncateString(entry.LastError, 24)
	}
	if entry.Attempts > 1 {
		return fmt.Sprintf("queued, %d attempts", entry.Attempts)
	}
	return "queued"
}

func sortedCountKeys(counts map[string]int) []string {
	keys := make([]string, 0, len(counts))
	for key := range counts {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func valueOrDash(s string) string {
	if strings.TrimSpace(s) == "" {
		return "-"
	}
	return s
}

func formatDetailTime(t time.Time) string {
	if t.IsZero() {
		return "-"
	}
	return fmt.Sprintf("%s (%s)", t.Format("2006-01-02 15:04:05"), formatTimeAgo(t))
}

func formatLogTime(t time.Time) string {
	if t.IsZero() {
		return "--:--:--"
	}
	return t.Format("15:04:05")
}
