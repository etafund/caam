package tui

import (
	"fmt"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

func TestDetailPanelViewportScrollsLongContentWithinBounds(t *testing.T) {
	panel := NewDetailPanel()
	panel.SetSize(70, 8)
	panel.SetProfile(&DetailInfo{
		Name:        "work",
		Provider:    "claude",
		AuthMode:    "oauth",
		Description: strings.Join(numberedLines("note", 24), "\n"),
		Path:        "/tmp/caam/work/auth.json",
	})

	before := panel.View()
	if got := lipgloss.Height(before); got > 8 {
		t.Fatalf("detail panel overflowed viewport height: got %d, want <= 8\n%s", got, before)
	}
	if panel.scrollOffset != 0 {
		t.Fatalf("initial detail scroll offset = %d, want 0", panel.scrollOffset)
	}

	panel.ScrollDown(4)
	after := panel.View()
	if panel.scrollOffset == 0 {
		t.Fatal("ScrollDown did not advance detail scroll offset")
	}
	if after == before {
		t.Fatal("ScrollDown did not change rendered detail content")
	}
	if got := lipgloss.Height(after); got > 8 {
		t.Fatalf("scrolled detail panel overflowed viewport height: got %d, want <= 8\n%s", got, after)
	}

	panel.ScrollUp(100)
	if panel.scrollOffset != 0 {
		t.Fatalf("ScrollUp should clamp detail offset to top, got %d", panel.scrollOffset)
	}
}

func TestProfilesPanelViewportScrollKeepsSelectionVisible(t *testing.T) {
	panel := NewProfilesPanel()
	panel.SetProvider("claude")
	panel.SetSize(80, 8)
	panel.SetProfiles(profileInfos(18))

	initial := panel.View()
	if got := lipgloss.Height(initial); got > 8 {
		t.Fatalf("profiles panel overflowed viewport height: got %d, want <= 8\n%s", got, initial)
	}
	if panel.scrollOffset != 0 {
		t.Fatalf("initial profile scroll offset = %d, want 0", panel.scrollOffset)
	}

	panel.ScrollDown(10)
	if panel.GetSelected() != 10 {
		t.Fatalf("ScrollDown selected index = %d, want 10", panel.GetSelected())
	}
	if panel.scrollOffset == 0 {
		t.Fatal("ScrollDown did not advance profile scroll offset")
	}
	scrolled := panel.View()
	if !strings.Contains(scrolled, "profile-10") {
		t.Fatalf("scrolled viewport should include selected profile, got:\n%s", scrolled)
	}
	if strings.Contains(scrolled, "profile-00") {
		t.Fatalf("scrolled viewport should not include first profile after offset, got:\n%s", scrolled)
	}
	if got := lipgloss.Height(scrolled); got > 8 {
		t.Fatalf("scrolled profiles panel overflowed viewport height: got %d, want <= 8\n%s", got, scrolled)
	}

	panel.ScrollUp(100)
	if panel.GetSelected() != 0 || panel.scrollOffset != 0 {
		t.Fatalf("ScrollUp should clamp profiles to top, selected=%d offset=%d", panel.GetSelected(), panel.scrollOffset)
	}
}

func TestMouseWheelScrollsProfilesWhenEnabled(t *testing.T) {
	m := NewWithProviders([]string{"claude"})
	m.mouseEnabled = true
	m.width = 120
	m.height = 30
	m.profiles = map[string][]Profile{"claude": profiles(12)}
	m.syncProfilesPanel()

	updated, _ := m.Update(tea.MouseMsg{
		Button: tea.MouseButtonWheelDown,
		X:      30,
		Y:      8,
	})
	got := updated.(Model)
	if got.selected != mouseWheelRows {
		t.Fatalf("mouse wheel selected index = %d, want %d", got.selected, mouseWheelRows)
	}
	if got.selectedProfileName != "profile-03" {
		t.Fatalf("mouse wheel selected profile = %q, want profile-03", got.selectedProfileName)
	}

	got.mouseEnabled = false
	updated, _ = got.Update(tea.MouseMsg{
		Button: tea.MouseButtonWheelDown,
		X:      30,
		Y:      8,
	})
	disabled := updated.(Model)
	if disabled.selected != got.selected {
		t.Fatalf("disabled mouse changed selection: got %d, want %d", disabled.selected, got.selected)
	}
}

func TestMouseProgramOptionsFollowPreference(t *testing.T) {
	disabled := Model{}
	if got := len(tuiProgramOptions(disabled)); got != 1 {
		t.Fatalf("disabled mouse program options = %d, want 1", got)
	}

	enabled := Model{mouseEnabled: true}
	if got := len(tuiProgramOptions(enabled)); got != 2 {
		t.Fatalf("enabled mouse program options = %d, want 2", got)
	}
}

func TestHelpViewportScrollsWithKeysAndMouse(t *testing.T) {
	t.Setenv("NO_COLOR", "1")

	m := New()
	m.state = stateHelp
	m.mouseEnabled = true
	m.width = 80
	m.height = 12

	before := m.helpView()
	if got := lipgloss.Height(before); got > m.height {
		t.Fatalf("help view overflowed viewport height: got %d, want <= %d\n%s", got, m.height, before)
	}
	if !strings.Contains(strings.ToLower(before), "keyboard shortcuts") {
		t.Fatalf("initial help viewport missing top content:\n%s", before)
	}

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyPgDown})
	scrolled := updated.(Model)
	if scrolled.state != stateHelp {
		t.Fatalf("page-down should keep help open, got state %v", scrolled.state)
	}
	if scrolled.helpScrollOffset == 0 {
		t.Fatal("page-down did not advance help scroll offset")
	}
	after := scrolled.helpView()
	if after == before {
		t.Fatal("page-down did not change rendered help viewport")
	}
	if got := lipgloss.Height(after); got > scrolled.height {
		t.Fatalf("scrolled help view overflowed viewport height: got %d, want <= %d\n%s", got, scrolled.height, after)
	}

	updated, _ = scrolled.Update(tea.MouseMsg{
		Action: tea.MouseActionPress,
		Button: tea.MouseButtonWheelUp,
		X:      10,
		Y:      5,
	})
	wheeled := updated.(Model)
	if wheeled.helpScrollOffset >= scrolled.helpScrollOffset {
		t.Fatalf("wheel-up did not reduce help scroll offset: before=%d after=%d", scrolled.helpScrollOffset, wheeled.helpScrollOffset)
	}

	updated, _ = wheeled.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("x")})
	closed := updated.(Model)
	if closed.state != stateList {
		t.Fatalf("non-scroll help key should close help, got state %v", closed.state)
	}
}

func TestMouseHoverHighlightsProfileWithoutChangingSelection(t *testing.T) {
	m := NewWithProviders([]string{"claude"})
	m.mouseEnabled = true
	m.width = 120
	m.height = 30
	m.profiles = map[string][]Profile{"claude": profiles(8)}
	m.syncProfilesPanel()
	beforeSelection := m.selected
	beforeName := m.selectedProfileName

	updated, _ := m.Update(tea.MouseMsg{
		Action: tea.MouseActionMotion,
		Button: tea.MouseButtonNone,
		X:      30,
		Y:      m.panelsTopY() + profilesPanelRowsStartY + 1,
	})
	hovered := updated.(Model)
	if hovered.selected != beforeSelection || hovered.selectedProfileName != beforeName {
		t.Fatalf("hover changed selection: selected %d->%d name %q->%q", beforeSelection, hovered.selected, beforeName, hovered.selectedProfileName)
	}
	if hovered.profilesPanel.hoverIndex != 1 {
		t.Fatalf("hover index = %d, want 1", hovered.profilesPanel.hoverIndex)
	}

	updated, _ = hovered.Update(tea.MouseMsg{
		Action: tea.MouseActionMotion,
		Button: tea.MouseButtonNone,
		X:      0,
		Y:      0,
	})
	cleared := updated.(Model)
	if cleared.profilesPanel.hoverIndex != -1 {
		t.Fatalf("hover should clear outside profiles panel, got %d", cleared.profilesPanel.hoverIndex)
	}
	if cleared.selected != beforeSelection || cleared.selectedProfileName != beforeName {
		t.Fatalf("clearing hover changed selection: selected %d->%d name %q->%q", beforeSelection, cleared.selected, beforeName, cleared.selectedProfileName)
	}
}

func numberedLines(prefix string, count int) []string {
	lines := make([]string, count)
	for i := range lines {
		lines[i] = fmt.Sprintf("%s-%02d", prefix, i)
	}
	return lines
}

func profiles(count int) []Profile {
	profiles := make([]Profile, count)
	for i := range profiles {
		profiles[i] = Profile{
			Name:     fmt.Sprintf("profile-%02d", i),
			Provider: "claude",
		}
	}
	return profiles
}

func profileInfos(count int) []ProfileInfo {
	infos := make([]ProfileInfo, count)
	for i := range infos {
		infos[i] = ProfileInfo{
			Name:     fmt.Sprintf("profile-%02d", i),
			AuthMode: "oauth",
		}
	}
	return infos
}
