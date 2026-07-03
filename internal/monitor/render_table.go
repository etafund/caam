package monitor

import (
	"fmt"
	"strings"
	"time"

	"github.com/Dicklesworthstone/coding_agent_account_manager/internal/authpool"
	"github.com/Dicklesworthstone/coding_agent_account_manager/internal/usage"
)

// Render implements the Renderer interface for TableRenderer.
func (r *TableRenderer) Render(state *MonitorState) string {
	if state == nil {
		return "No data available"
	}

	width := r.Width
	if width <= 0 {
		width = 75
	}
	if width < 40 {
		width = 40
	}

	innerWidth := width - 2
	if innerWidth < 1 {
		innerWidth = 1
	}

	var b strings.Builder

	writeBorder(&b, innerWidth)
	writeCentered(&b, innerWidth, "LIVE USAGE MONITOR")
	updated := fmt.Sprintf("Last updated: %s", formatUpdatedAt(state.UpdatedAt))
	writeCentered(&b, innerWidth, updated)
	writeBorder(&b, innerWidth)

	if len(state.Profiles) == 0 {
		writeCentered(&b, innerWidth, "No profiles configured")
	} else {
		keys := sortProfileKeys(state)
		currentProvider := ""
		now := time.Now()

		for _, key := range keys {
			p := state.Profiles[key]
			if p == nil {
				continue
			}

			if p.Provider != currentProvider {
				currentProvider = p.Provider
				provHeader := fmt.Sprintf("  %s", strings.ToUpper(p.Provider))
				writeLine(&b, innerWidth, provHeader)
			}

			indicator := ""
			if r.ShowEmoji {
				indicator = fmt.Sprintf("[%s] ", tableIndicator(p))
			}

			var line string
			if reason := usageUnavailable(p.Usage); reason != "" {
				// The fetch failed or returned no data (e.g. an expired token) —
				// show why instead of a misleading 0% bar, so logged-in accounts
				// don't look idle (issue #37).
				line = fmt.Sprintf("  %s%-20s %s", indicator, truncate(p.ProfileName, 20), reason)
			} else {
				percent := usagePercent(p.Usage)
				bar := progressBar(percent, 20)
				percentStr := fmt.Sprintf("%3.0f%%", percent)
				statusSuffix := tableStatusSuffix(p, now)

				line = fmt.Sprintf("  %s%-20s %s %s%s", indicator, truncate(p.ProfileName, 20), bar, percentStr, statusSuffix)
			}
			writeLine(&b, innerWidth, line)
			if detail := tableUsageDetailLine(p, innerWidth, now); detail != "" {
				writeLine(&b, innerWidth, detail)
			}
		}
	}

	writeBorder(&b, innerWidth)
	if len(state.Errors) > 0 {
		b.WriteString(renderErrors(state))
	}

	return b.String()
}

func tableIndicator(p *ProfileState) string {
	if p == nil {
		return "UNK"
	}
	if p.Usage != nil && p.Usage.CachedInactive {
		return "OK"
	}
	if usage.IsRateLimitedUsage(p.Usage) {
		return "OK"
	}
	if p.Usage != nil && p.Usage.Error == "" && p.Usage.PrimaryWindow != nil {
		if p.Alert != nil {
			if indicator := alertEmoji(p.Alert.Type); indicator != "" {
				return indicator
			}
		}
		return "OK"
	}
	return healthEmoji(p.Health)
}

func tableStatusSuffix(p *ProfileState, now time.Time) string {
	if p == nil {
		return ""
	}
	if p.InCooldown && p.CooldownUntil != nil {
		return fmt.Sprintf(" | cooldown %s", formatCooldown(p.CooldownUntil, now))
	}
	if usage.IsRateLimitedUsage(p.Usage) {
		return " | rate limited (retrying)"
	}
	if p.PoolStatus != authpool.PoolStatusUnknown {
		return " | " + p.PoolStatus.String()
	}
	if p.Usage == nil {
		return ""
	}
	window := p.Usage.PrimaryWindow
	if window == nil || window.ResetsAt.IsZero() {
		return ""
	}
	return fmt.Sprintf(" | resets %s", formatDuration(window.ResetsAt.Sub(now)))
}

func tableUsageDetailLine(p *ProfileState, innerWidth int, now time.Time) string {
	if p == nil || usageUnavailable(p.Usage) != "" {
		return ""
	}

	line := buildTableUsageDetailLine(p, now, true)
	if line != "" && len(line) > innerWidth {
		line = buildTableUsageDetailLine(p, now, false)
	}
	return line
}

func buildTableUsageDetailLine(p *ProfileState, now time.Time, includeResets bool) string {
	if p == nil || p.Usage == nil {
		return ""
	}

	segments := make([]string, 0, 3)
	if p.Usage.PrimaryWindow != nil {
		segments = append(segments, tableWindowSegment("5H", p.Usage.PrimaryWindow, now, includeResets))
	}
	if p.Usage.SecondaryWindow != nil {
		segments = append(segments, tableWindowSegment("WK", p.Usage.SecondaryWindow, now, includeResets))
	}

	switch p.Provider {
	case "claude":
		if w := p.Usage.FindModelWindow("fable", ""); w != nil {
			segments = append(segments, fmt.Sprintf("FABLE %d%%", tableWindowPercent(w)))
		}
	case "codex":
		if segment := tableCodexSparkSegment(p.Usage); segment != "" {
			segments = append(segments, segment)
		}
	}

	if len(segments) == 0 {
		return ""
	}
	return "       " + strings.Join(segments, " | ")
}

func tableWindowSegment(label string, w *usage.UsageWindow, now time.Time, includeReset bool) string {
	segment := fmt.Sprintf("%s %d%%", label, tableWindowPercent(w))
	if includeReset && w != nil && !w.ResetsAt.IsZero() && w.ResetsAt.After(now) {
		segment += fmt.Sprintf(" (resets %s)", formatDuration(w.ResetsAt.Sub(now)))
	}
	return segment
}

func tableCodexSparkSegment(info *usage.UsageInfo) string {
	spark5h := findModelWindowAny(info, "5h", "spark", "codex-spark", "bengalfox")
	sparkWeekly := findModelWindowAny(info, "weekly", "spark", "codex-spark", "bengalfox")
	if spark5h == nil && sparkWeekly == nil {
		return ""
	}

	parts := []string{"SPARK"}
	if spark5h != nil {
		parts = append(parts, fmt.Sprintf("5H %d%%", tableWindowPercent(spark5h)))
	}
	if sparkWeekly != nil {
		parts = append(parts, fmt.Sprintf("WK %d%%", tableWindowPercent(sparkWeekly)))
	}
	return strings.Join(parts, " ")
}

func findModelWindowAny(info *usage.UsageInfo, suffix string, substrs ...string) *usage.UsageWindow {
	if info == nil {
		return nil
	}
	for _, substr := range substrs {
		if w := info.FindModelWindow(substr, suffix); w != nil {
			return w
		}
	}
	return nil
}

func tableWindowPercent(w *usage.UsageWindow) int {
	if w == nil {
		return 0
	}
	if w.UsedPercent != 0 {
		return w.UsedPercent
	}
	return int(w.Utilization * 100)
}

func writeBorder(b *strings.Builder, innerWidth int) {
	if innerWidth < 1 {
		innerWidth = 1
	}
	b.WriteString("+")
	b.WriteString(strings.Repeat("-", innerWidth))
	b.WriteString("+\n")
}

func writeCentered(b *strings.Builder, innerWidth int, text string) {
	writeLine(b, innerWidth, centerText(text, innerWidth))
}

func writeLine(b *strings.Builder, innerWidth int, content string) {
	if innerWidth < 1 {
		innerWidth = 1
	}
	if len(content) > innerWidth {
		content = content[:innerWidth]
	}
	b.WriteString("|")
	b.WriteString(content)
	if pad := innerWidth - len(content); pad > 0 {
		b.WriteString(strings.Repeat(" ", pad))
	}
	b.WriteString("|\n")
}

func centerText(text string, width int) string {
	if width <= 0 {
		return ""
	}
	if len(text) >= width {
		return text[:width]
	}
	left := (width - len(text)) / 2
	right := width - len(text) - left
	return strings.Repeat(" ", left) + text + strings.Repeat(" ", right)
}
