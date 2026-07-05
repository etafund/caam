package cmd

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	caamdb "github.com/Dicklesworthstone/coding_agent_account_manager/internal/db"
)

func TestUsage_EmptyDatabase_Table(t *testing.T) {
	setupUsageCommandTest(t)

	out, err := executeCommand("usage", "--days", "7")
	if err != nil {
		t.Fatalf("executeCommand() error = %v", err)
	}
	if !strings.Contains(out, "No usage data") {
		t.Fatalf("output = %q, want to mention no usage data", out)
	}
}

func TestUsage_Summary_JSON(t *testing.T) {
	setupUsageCommandTest(t)

	dbPath := caamdb.DefaultPath()
	if err := os.MkdirAll(filepath.Dir(dbPath), 0700); err != nil {
		t.Fatalf("mkdir db dir: %v", err)
	}

	db, err := caamdb.Open()
	if err != nil {
		t.Fatalf("db.Open() error = %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	// claude/work: one 1h session
	if err := db.LogEvent(caamdb.Event{Type: caamdb.EventActivate, Provider: "claude", ProfileName: "work"}); err != nil {
		t.Fatalf("LogEvent activate: %v", err)
	}
	if err := db.LogEvent(caamdb.Event{Type: caamdb.EventDeactivate, Provider: "claude", ProfileName: "work", Duration: time.Hour}); err != nil {
		t.Fatalf("LogEvent deactivate: %v", err)
	}

	// codex/main: two 30m sessions
	for i := 0; i < 2; i++ {
		if err := db.LogEvent(caamdb.Event{Type: caamdb.EventActivate, Provider: "codex", ProfileName: "main"}); err != nil {
			t.Fatalf("LogEvent activate: %v", err)
		}
		if err := db.LogEvent(caamdb.Event{Type: caamdb.EventDeactivate, Provider: "codex", ProfileName: "main", Duration: 30 * time.Minute}); err != nil {
			t.Fatalf("LogEvent deactivate: %v", err)
		}
	}

	out, err := executeCommand("usage", "--format", "json", "--since", "1970-01-01")
	if err != nil {
		t.Fatalf("executeCommand() error = %v", err)
	}

	var rows []usageSummaryRow
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &rows); err != nil {
		t.Fatalf("Unmarshal() error = %v; out=%q", err, out)
	}

	if len(rows) != 2 {
		t.Fatalf("rows len = %d, want 2", len(rows))
	}

	// Rows are ordered by active_seconds desc then sessions; both have 1h total.
	byKey := make(map[string]usageSummaryRow)
	for _, r := range rows {
		byKey[r.Provider+"/"+r.Profile] = r
	}

	if got := byKey["claude/work"].Sessions; got != 1 {
		t.Fatalf("claude/work sessions = %d, want 1", got)
	}
	if got := byKey["codex/main"].Sessions; got != 2 {
		t.Fatalf("codex/main sessions = %d, want 2", got)
	}
}

func TestUsage_Detailed_JSON(t *testing.T) {
	setupUsageCommandTest(t)

	db, err := caamdb.Open()
	if err != nil {
		t.Fatalf("db.Open() error = %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	if err := db.LogEvent(caamdb.Event{Type: caamdb.EventDeactivate, Provider: "claude", ProfileName: "work", Duration: 2 * time.Hour}); err != nil {
		t.Fatalf("LogEvent deactivate: %v", err)
	}

	out, err := executeCommand("usage", "--profile", "claude/work", "--detailed", "--format", "json", "--since", "1970-01-01")
	if err != nil {
		t.Fatalf("executeCommand() error = %v", err)
	}

	var rows []sessionHistoryRow
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &rows); err != nil {
		t.Fatalf("Unmarshal() error = %v; out=%q", err, out)
	}
	if len(rows) != 1 {
		t.Fatalf("rows len = %d, want 1", len(rows))
	}
	if rows[0].DurationSeconds != int64((2 * time.Hour).Seconds()) {
		t.Fatalf("DurationSeconds = %d, want %d", rows[0].DurationSeconds, int64((2 * time.Hour).Seconds()))
	}
}

func setupUsageCommandTest(t *testing.T) {
	t.Helper()

	root := t.TempDir()
	t.Setenv("HOME", filepath.Join(root, "home"))
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(root, "xdg-config"))
	t.Setenv("XDG_DATA_HOME", filepath.Join(root, "xdg-data"))
	t.Setenv("CAAM_HOME", filepath.Join(root, "caam-home"))

	resetUsageCommandFlags(t)
}

func resetUsageCommandFlags(t *testing.T) {
	t.Helper()

	flags := usageCmd.Flags()
	for _, name := range []string{"profile", "detailed", "days", "since", "format"} {
		flag := flags.Lookup(name)
		if flag == nil {
			t.Fatalf("usage flag %q not found", name)
		}
		if err := flag.Value.Set(flag.DefValue); err != nil {
			t.Fatalf("reset usage flag %q: %v", name, err)
		}
		flag.Changed = false
	}
}
