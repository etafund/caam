package logs

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestNewCodexScanner(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)  // Mock HOME for os.UserHomeDir()
	t.Setenv("CODEX_HOME", "") // Ensure CODEX_HOME is unset

	scanner := NewCodexScanner()
	if scanner == nil {
		t.Fatal("NewCodexScanner() returned nil")
	}

	expected := filepath.Join(tmpHome, ".codex", "logs")
	if scanner.LogDir() != expected {
		t.Errorf("LogDir() = %q, want %q", scanner.LogDir(), expected)
	}
}

func TestNewCodexScannerWithDir(t *testing.T) {
	customDir := "/custom/codex/logs"
	scanner := NewCodexScannerWithDir(customDir)
	if scanner.LogDir() != customDir {
		t.Errorf("LogDir() = %q, want %q", scanner.LogDir(), customDir)
	}
}

func TestCodexScanner_ScanMissingDirectory(t *testing.T) {
	scanner := NewCodexScannerWithDir("/nonexistent/codex/logs")

	ctx := context.Background()
	result, err := scanner.Scan(ctx, "", time.Time{})
	if err != nil {
		t.Fatalf("Scan() error = %v, want nil for missing directory", err)
	}

	if result.Provider != "codex" {
		t.Errorf("Provider = %q, want codex", result.Provider)
	}
	if len(result.Entries) != 0 {
		t.Errorf("Entries = %d, want 0 for missing directory", len(result.Entries))
	}
}

func TestCodexScanner_ScanEmptyDirectory(t *testing.T) {
	tmpDir := t.TempDir()
	scanner := NewCodexScannerWithDir(tmpDir)

	ctx := context.Background()
	result, err := scanner.Scan(ctx, "", time.Time{})
	if err != nil {
		t.Fatalf("Scan() error = %v", err)
	}

	if len(result.Entries) != 0 {
		t.Errorf("Entries = %d, want 0 for empty directory", len(result.Entries))
	}
}

func TestCodexScanner_ScanValidJSONL(t *testing.T) {
	tmpDir := t.TempDir()

	logContent := `{"timestamp":"2025-01-10T12:00:00Z","event":"response","model":"gpt-4o","session_id":"sess-1","request_id":"req-1","usage":{"prompt_tokens":100,"completion_tokens":200,"total_tokens":300}}
{"time":1736510460,"type":"response","request":{"model":"gpt-4o-mini"},"response":{"usage":{"input_tokens":50,"output_tokens":75}},"sessionId":"sess-2","message_id":"msg-2"}
`
	logFile := filepath.Join(tmpDir, "session-test.jsonl")
	if err := os.WriteFile(logFile, []byte(logContent), 0600); err != nil {
		t.Fatalf("Failed to write test file: %v", err)
	}

	scanner := NewCodexScannerWithDir(tmpDir)
	ctx := context.Background()
	result, err := scanner.Scan(ctx, "", time.Time{})
	if err != nil {
		t.Fatalf("Scan() error = %v", err)
	}

	if result.TotalEntries != 2 {
		t.Errorf("TotalEntries = %d, want 2", result.TotalEntries)
	}
	if result.ParsedEntries != 2 {
		t.Errorf("ParsedEntries = %d, want 2", result.ParsedEntries)
	}
	if len(result.Entries) != 2 {
		t.Fatalf("Entries = %d, want 2", len(result.Entries))
	}

	entry := result.Entries[0]
	if entry.Model != "gpt-4o" {
		t.Errorf("Entry[0].Model = %q, want gpt-4o", entry.Model)
	}
	if entry.InputTokens != 100 {
		t.Errorf("Entry[0].InputTokens = %d, want 100", entry.InputTokens)
	}
	if entry.OutputTokens != 200 {
		t.Errorf("Entry[0].OutputTokens = %d, want 200", entry.OutputTokens)
	}
	if entry.TotalTokens != 300 {
		t.Errorf("Entry[0].TotalTokens = %d, want 300", entry.TotalTokens)
	}
	if entry.ConversationID != "sess-1" {
		t.Errorf("Entry[0].ConversationID = %q, want sess-1", entry.ConversationID)
	}
	if entry.MessageID != "req-1" {
		t.Errorf("Entry[0].MessageID = %q, want req-1", entry.MessageID)
	}

	second := result.Entries[1]
	if second.Model != "gpt-4o-mini" {
		t.Errorf("Entry[1].Model = %q, want gpt-4o-mini", second.Model)
	}
	if second.InputTokens != 50 {
		t.Errorf("Entry[1].InputTokens = %d, want 50", second.InputTokens)
	}
	if second.OutputTokens != 75 {
		t.Errorf("Entry[1].OutputTokens = %d, want 75", second.OutputTokens)
	}
	if second.TotalTokens != 125 {
		t.Errorf("Entry[1].TotalTokens = %d, want 125", second.TotalTokens)
	}
	if second.ConversationID != "sess-2" {
		t.Errorf("Entry[1].ConversationID = %q, want sess-2", second.ConversationID)
	}
	if second.MessageID != "msg-2" {
		t.Errorf("Entry[1].MessageID = %q, want msg-2", second.MessageID)
	}
}

func TestCodexScanner_ScanMalformedLines(t *testing.T) {
	tmpDir := t.TempDir()

	logContent := `{"timestamp":"2025-01-10T12:00:00Z","event":"response","model":"gpt-4o","usage":{"prompt_tokens":100,"completion_tokens":200}}
not valid json
{"timestamp":"2025-01-10T12:01:00Z","event":"response","model":"gpt-4o-mini","usage":{"prompt_tokens":50,"completion_tokens":100}}
also not valid {
`
	logFile := filepath.Join(tmpDir, "session-bad.jsonl")
	if err := os.WriteFile(logFile, []byte(logContent), 0600); err != nil {
		t.Fatalf("Failed to write test file: %v", err)
	}

	scanner := NewCodexScannerWithDir(tmpDir)
	ctx := context.Background()
	result, err := scanner.Scan(ctx, "", time.Time{})
	if err != nil {
		t.Fatalf("Scan() error = %v", err)
	}

	if result.TotalEntries != 4 {
		t.Errorf("TotalEntries = %d, want 4", result.TotalEntries)
	}
	if result.ParsedEntries != 2 {
		t.Errorf("ParsedEntries = %d, want 2", result.ParsedEntries)
	}
	if result.ParseErrors != 2 {
		t.Errorf("ParseErrors = %d, want 2", result.ParseErrors)
	}
}

func TestCodexScanner_ScanWithTimestampFilter(t *testing.T) {
	tmpDir := t.TempDir()

	logContent := `{"timestamp":"2025-01-10T10:00:00Z","event":"response","model":"old","usage":{"prompt_tokens":100,"completion_tokens":200}}
{"timestamp":"2025-01-10T12:00:00Z","event":"response","model":"new","usage":{"prompt_tokens":50,"completion_tokens":100}}
`
	logFile := filepath.Join(tmpDir, "session-filter.jsonl")
	if err := os.WriteFile(logFile, []byte(logContent), 0600); err != nil {
		t.Fatalf("Failed to write test file: %v", err)
	}

	scanner := NewCodexScannerWithDir(tmpDir)
	ctx := context.Background()

	since := time.Date(2025, 1, 10, 11, 0, 0, 0, time.UTC)
	result, err := scanner.Scan(ctx, "", since)
	if err != nil {
		t.Fatalf("Scan() error = %v", err)
	}

	if len(result.Entries) != 1 {
		t.Fatalf("Entries = %d, want 1", len(result.Entries))
	}
	if result.Entries[0].Model != "new" {
		t.Errorf("Entry[0].Model = %q, want new", result.Entries[0].Model)
	}
}

func TestCodexScanner_ScanMultipleFiles(t *testing.T) {
	tmpDir := t.TempDir()

	log1 := `{"timestamp":"2025-01-10T12:00:00Z","event":"response","model":"file1","usage":{"prompt_tokens":100,"completion_tokens":200}}
`
	log2 := `{"timestamp":"2025-01-10T12:01:00Z","event":"response","model":"file2","usage":{"prompt_tokens":50,"completion_tokens":100}}
`
	if err := os.WriteFile(filepath.Join(tmpDir, "session-one.jsonl"), []byte(log1), 0600); err != nil {
		t.Fatalf("Failed to write log1: %v", err)
	}
	if err := os.WriteFile(filepath.Join(tmpDir, "session-two.jsonl"), []byte(log2), 0600); err != nil {
		t.Fatalf("Failed to write log2: %v", err)
	}

	scanner := NewCodexScannerWithDir(tmpDir)
	ctx := context.Background()
	result, err := scanner.Scan(ctx, "", time.Time{})
	if err != nil {
		t.Fatalf("Scan() error = %v", err)
	}

	if len(result.Entries) != 2 {
		t.Errorf("Entries = %d, want 2", len(result.Entries))
	}
}

func TestCodexScanner_ParseMissingFields(t *testing.T) {
	scanner := NewCodexScannerWithDir("/ignored")
	entry, err := scanner.parseLine([]byte(`{"event":"response"}`))
	if err != nil {
		t.Fatalf("parseLine() error = %v", err)
	}
	if entry.Model != "" {
		t.Errorf("Model = %q, want empty", entry.Model)
	}
	if !entry.Timestamp.IsZero() {
		t.Errorf("Timestamp = %v, want zero", entry.Timestamp)
	}
}

func TestCodexScanner_TimestampParsing(t *testing.T) {
	scanner := NewCodexScannerWithDir("/ignored")
	entry, err := scanner.parseLine([]byte(`{"time":1736510460}`))
	if err != nil {
		t.Fatalf("parseLine() error = %v", err)
	}
	if entry.Timestamp.IsZero() {
		t.Fatal("Timestamp should be parsed")
	}
}

func TestCodexScannerImplementsScanner(t *testing.T) {
	var _ Scanner = (*CodexScanner)(nil)
}
