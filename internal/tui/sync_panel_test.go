package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/Dicklesworthstone/coding_agent_account_manager/internal/sync"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

func TestSyncPanel_View_Empty(t *testing.T) {
	p := NewSyncPanel()
	p.SetSize(120, 40)

	out := p.View()
	if out == "" {
		t.Fatalf("View() returned empty")
	}
	if want := "No machines"; !strings.Contains(out, want) {
		t.Fatalf("View() output missing %q, got: %s", want, out)
	}
}

func TestSyncPanel_Toggle(t *testing.T) {
	p := NewSyncPanel()

	if p.Visible() {
		t.Fatalf("panel should not be visible initially")
	}

	p.Toggle()
	if !p.Visible() {
		t.Fatalf("panel should be visible after toggle")
	}

	p.Toggle()
	if p.Visible() {
		t.Fatalf("panel should not be visible after second toggle")
	}
}

func TestSyncPanel_Navigation(t *testing.T) {
	p := NewSyncPanel()

	// Can't move up/down with no machines
	p.MoveUp()
	p.MoveDown()
	// Should not panic
}

func TestModel_SyncPanel_ToggleWithKey(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("CAAM_HOME", tmpDir)

	m := New()
	m.width = 120
	m.height = 40

	// Toggle on with 'S'
	model, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("S")})
	m = model.(Model)
	if m.syncPanel == nil || !m.syncPanel.Visible() {
		t.Fatalf("sync panel not visible after toggle")
	}

	// Close with esc
	model, _ = m.Update(tea.KeyMsg{Type: tea.KeyEscape})
	m = model.(Model)
	if m.syncPanel.Visible() {
		t.Fatalf("sync panel still visible after esc")
	}
}

func TestModel_CtrlSStartsSyncFromAnyView(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("CAAM_HOME", tmpDir)

	tests := []struct {
		name        string
		configure   func(*Model)
		wantState   viewState
		wantSyncing bool
		wantVisible bool
		wantStatus  string
		wantCommand bool
	}{
		{
			name:        "list",
			wantState:   stateList,
			wantSyncing: true,
			wantStatus:  "Syncing all machines...",
			wantCommand: true,
		},
		{
			name: "help",
			configure: func(m *Model) {
				m.state = stateHelp
			},
			wantState:   stateHelp,
			wantSyncing: true,
			wantStatus:  "Syncing all machines...",
			wantCommand: true,
		},
		{
			name: "sync log",
			configure: func(m *Model) {
				m.state = stateSyncLog
				m.syncLogText = "already open"
			},
			wantState:   stateSyncLog,
			wantSyncing: true,
			wantStatus:  "Syncing all machines...",
			wantCommand: true,
		},
		{
			name: "visible sync panel",
			configure: func(m *Model) {
				m.syncPanel.Toggle()
			},
			wantState:   stateList,
			wantSyncing: true,
			wantVisible: true,
			wantStatus:  "Syncing all machines...",
			wantCommand: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := New()
			m.width = 120
			m.height = 40
			if tt.configure != nil {
				tt.configure(&m)
			}

			model, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlS})
			m = model.(Model)

			if (cmd != nil) != tt.wantCommand {
				t.Fatalf("cmd nil = %v, want command=%v", cmd == nil, tt.wantCommand)
			}
			if m.state != tt.wantState {
				t.Fatalf("state = %v, want %v", m.state, tt.wantState)
			}
			if m.statusMsg != tt.wantStatus {
				t.Fatalf("statusMsg = %q, want %q", m.statusMsg, tt.wantStatus)
			}
			if m.syncPanel == nil {
				t.Fatalf("sync panel was not initialized")
			}
			if m.syncPanel.Visible() != tt.wantVisible {
				t.Fatalf("sync panel visible = %v, want %v", m.syncPanel.Visible(), tt.wantVisible)
			}
			if m.syncPanel.Syncing() != tt.wantSyncing {
				t.Fatalf("sync panel syncing = %v, want %v", m.syncPanel.Syncing(), tt.wantSyncing)
			}
		})
	}
}

func TestModel_CtrlSBlocksOverlappingSync(t *testing.T) {
	m := New()
	m.width = 120
	m.height = 40

	model, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlS})
	m = model.(Model)
	if cmd == nil {
		t.Fatalf("first ctrl+s did not start sync")
	}
	if !m.syncPanel.Syncing() {
		t.Fatalf("first ctrl+s did not mark sync in progress")
	}

	model, cmd = m.Update(tea.KeyMsg{Type: tea.KeyCtrlS})
	m = model.(Model)
	if cmd != nil {
		t.Fatalf("second ctrl+s returned command while sync already in progress")
	}
	if m.statusMsg != "Sync already in progress" {
		t.Fatalf("statusMsg = %q, want sync-in-progress guard", m.statusMsg)
	}
}

func TestModel_SyncPanelRemoveKeyOpensConfirmation(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("CAAM_HOME", tmpDir)

	state := sync.NewSyncState(tmpDir)
	machine := sync.NewMachine("laptop", "192.0.2.10")
	if err := state.Pool.AddMachine(machine); err != nil {
		t.Fatalf("AddMachine() error = %v", err)
	}

	m := New()
	m.width = 120
	m.height = 40
	m.syncPanel.Toggle()
	m.syncPanel.SetState(state)

	model, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("r")})
	m = model.(Model)

	if cmd != nil {
		t.Fatalf("remove key returned command; removal must wait for confirmation")
	}
	if m.state != stateSyncRemoveConfirm {
		t.Fatalf("state = %v, want stateSyncRemoveConfirm", m.state)
	}
	if m.confirmDialog == nil {
		t.Fatalf("remove key did not open confirmation dialog")
	}
	if m.pendingSyncMachine != machine.ID {
		t.Fatalf("pendingSyncMachine = %q, want %q", m.pendingSyncMachine, machine.ID)
	}
	if got := state.Pool.GetMachine(machine.ID); got == nil {
		t.Fatalf("machine was removed before confirmation")
	}
	if m.statusMsg != "" {
		t.Fatalf("statusMsg = %q, want empty while confirmation is open", m.statusMsg)
	}
}

func TestModel_SyncPanelLogKeyOpensInTUILog(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("CAAM_HOME", tmpDir)

	state := sync.NewSyncState("")
	state.AddToHistory(sync.HistoryEntry{
		Timestamp: time.Date(2026, 7, 4, 1, 2, 3, 0, time.UTC),
		Provider:  "codex",
		Profile:   "primary",
		Machine:   "laptop",
		Action:    "push",
		Success:   true,
	})
	if err := state.Save(); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	m := New()
	m.width = 120
	m.height = 40
	m.syncPanel.Toggle()

	model, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("l")})
	m = model.(Model)

	if cmd != nil {
		msg := cmd()
		model, _ = m.Update(msg)
		m = model.(Model)
	} else {
		t.Fatalf("sync log key did not return load command")
	}
	if m.state != stateSyncLog {
		t.Fatalf("state = %v, want stateSyncLog", m.state)
	}
	for _, want := range []string{"Sync History", "codex/primary", "laptop", "push", "ok"} {
		if !strings.Contains(m.syncLogText, want) {
			t.Fatalf("syncLogText = %q, want substring %q", m.syncLogText, want)
		}
	}

	model, cmd = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("l")})
	m = model.(Model)
	if cmd != nil {
		t.Fatalf("closing sync log returned unexpected command")
	}
	if m.state != stateList {
		t.Fatalf("state = %v, want stateList after toggling log closed", m.state)
	}
	if m.syncLogText != "" {
		t.Fatalf("syncLogText = %q, want empty after closing log", m.syncLogText)
	}
}

func TestModel_SyncPanelEnterOpensSelectedMachineDetails(t *testing.T) {
	state := sync.NewSyncState("")
	machine := sync.NewMachine("laptop", "192.0.2.10")
	machine.Port = 2222
	machine.SSHUser = "alice"
	machine.SSHKeyPath = "~/.ssh/laptop"
	if err := state.Pool.AddMachine(machine); err != nil {
		t.Fatalf("AddMachine() error = %v", err)
	}

	m := New()
	m.width = 120
	m.height = 40
	m.syncPanel.Toggle()
	m.syncPanel.SetState(state)

	model, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = model.(Model)

	if cmd != nil {
		t.Fatalf("enter returned unexpected command")
	}
	if m.state != stateSyncDetail {
		t.Fatalf("state = %v, want stateSyncDetail", m.state)
	}
	view := m.View()
	for _, want := range []string{"Sync Machine Details", "laptop", "192.0.2.10:2222", "alice", "~/.ssh/laptop"} {
		if !strings.Contains(view, want) {
			t.Fatalf("details view missing %q, got: %s", want, view)
		}
	}
}

func TestSyncPanel_SetLoading(t *testing.T) {
	p := NewSyncPanel()
	p.SetSize(120, 40)
	p.SetLoading(true)

	out := p.View()
	if !strings.Contains(out, "Loading") {
		t.Fatalf("View() should show loading state, got: %s", out)
	}

	p.SetLoading(false)
	out = p.View()
	if strings.Contains(out, "Loading") {
		t.Fatalf("View() should not show loading after SetLoading(false)")
	}
}

func TestSyncPanel_SyncingViewShowsProgressOverlayState(t *testing.T) {
	p := NewSyncPanel()
	p.SetSize(120, 40)

	state := &sync.SyncState{Pool: sync.NewSyncPool()}
	machine := sync.NewMachine("office", "192.0.2.11")
	if err := state.Pool.AddMachine(machine); err != nil {
		t.Fatalf("AddMachine() error = %v", err)
	}
	p.SetState(state)
	p.SetSyncing(true)

	out := p.View()
	if !strings.Contains(out, "Syncing") {
		t.Fatalf("syncing view missing progress text, got: %s", out)
	}
	if !strings.Contains(out, "office") {
		t.Fatalf("syncing overlay should show per-machine state, got: %s", out)
	}
	if strings.Contains(out, "%") {
		t.Fatalf("syncing overlay should not show fake percentage progress, got: %s", out)
	}
}

func TestSyncPanel_LoadingSnapshot(t *testing.T) {
	t.Setenv("NO_COLOR", "1")

	theme := NewTheme(ThemeOptionsFromEnv())
	p := NewSyncPanelWithTheme(theme)
	p.SetLoading(true)

	got := normalizeSyncSnapshot(p.View())

	// Verify essential content is present (breadcrumb, title, status, loading indicator)
	if !strings.Contains(got, "Profiles > Sync") {
		t.Errorf("missing breadcrumb 'Profiles > Sync'")
	}
	if !strings.Contains(got, "[Esc] Back") {
		t.Errorf("missing back hint '[Esc] Back'")
	}
	if !strings.Contains(got, "Sync Pool") {
		t.Errorf("missing title 'Sync Pool'")
	}
	if !strings.Contains(got, "Status:") {
		t.Errorf("missing 'Status:' line")
	}
	if !strings.Contains(got, "Loading sync state") {
		t.Errorf("missing loading indicator")
	}
}

func TestSyncPanel_SetSyncing(t *testing.T) {
	p := NewSyncPanel()
	p.SetSyncing(true)
	// Should not panic

	// Test nil receiver
	var nilPanel *SyncPanel
	nilPanel.SetSyncing(true)
	// Should not panic
}

func TestSyncPanel_SetState(t *testing.T) {
	p := NewSyncPanel()
	p.SetSize(120, 40)

	// Set nil state
	p.SetState(nil)
	if p.State() != nil {
		t.Fatal("State should be nil after SetState(nil)")
	}

	// Test nil receiver
	var nilPanel *SyncPanel
	nilPanel.SetState(nil)
	// Should not panic
}

func TestSyncPanel_State(t *testing.T) {
	p := NewSyncPanel()
	if p.State() != nil {
		t.Fatal("State should be nil initially")
	}

	// Test nil receiver
	var nilPanel *SyncPanel
	if nilPanel.State() != nil {
		t.Fatal("State on nil receiver should return nil")
	}
}

func TestSyncPanel_SelectedMachine(t *testing.T) {
	p := NewSyncPanel()

	// No machines - should return nil
	if m := p.SelectedMachine(); m != nil {
		t.Fatal("SelectedMachine should return nil with no machines")
	}

	// Test nil receiver
	var nilPanel *SyncPanel
	if m := nilPanel.SelectedMachine(); m != nil {
		t.Fatal("SelectedMachine on nil receiver should return nil")
	}
}

func normalizeSyncSnapshot(s string) string {
	plain := ansi.Strip(s)
	lines := strings.Split(plain, "\n")
	for i, line := range lines {
		lines[i] = strings.TrimRight(line, " ")
	}
	return strings.Join(lines, "\n")
}

// Note: TestGetStatusIcon, TestFormatTimeAgo, TestTruncateString, TestToMachineInfo
// are defined in sync_test.go
