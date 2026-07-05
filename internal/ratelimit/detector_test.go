package ratelimit

import (
	"strings"
	"testing"
)

func TestNewDetector(t *testing.T) {
	tests := []struct {
		name     string
		provider Provider
		patterns []string
		wantErr  bool
	}{
		{
			name:     "claude with defaults",
			provider: ProviderClaude,
			patterns: nil,
			wantErr:  false,
		},
		{
			name:     "codex with defaults",
			provider: ProviderCodex,
			patterns: nil,
			wantErr:  false,
		},
		{
			name:     "gemini with defaults",
			provider: ProviderGemini,
			patterns: nil,
			wantErr:  false,
		},
		{
			name:     "custom patterns",
			provider: ProviderClaude,
			patterns: []string{`test pattern`, `\d+`},
			wantErr:  false,
		},
		{
			name:     "invalid regex",
			provider: ProviderClaude,
			patterns: []string{`[invalid`},
			wantErr:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d, err := NewDetector(tt.provider, tt.patterns)
			if (err != nil) != tt.wantErr {
				t.Errorf("NewDetector() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !tt.wantErr && d == nil {
				t.Error("NewDetector() returned nil detector without error")
			}
		})
	}
}

func TestDetector_Check(t *testing.T) {
	tests := []struct {
		name     string
		provider Provider
		texts    []string
		want     bool
	}{
		{
			name:     "claude rate limit",
			provider: ProviderClaude,
			texts:    []string{"Error: rate limit exceeded"},
			want:     true,
		},
		{
			name:     "claude usage limit",
			provider: ProviderClaude,
			texts:    []string{"usage limit reached"},
			want:     true,
		},
		{
			name:     "claude 429",
			provider: ProviderClaude,
			texts:    []string{"HTTP 429 Too Many Requests, retry later"},
			want:     true,
		},
		{
			name:     "claude capacity",
			provider: ProviderClaude,
			texts:    []string{"Over capacity, please try again"},
			want:     true,
		},
		{
			name:     "codex rate limit",
			provider: ProviderCodex,
			texts:    []string{"rate-limit hit"},
			want:     true,
		},
		{
			name:     "codex quota exceeded",
			provider: ProviderCodex,
			texts:    []string{"quota exceeded for this model"},
			want:     true,
		},
		{
			name:     "gemini resource exhausted",
			provider: ProviderGemini,
			texts:    []string{"RESOURCE_EXHAUSTED: Too many requests"},
			want:     true,
		},
		{
			name:     "gemini quota",
			provider: ProviderGemini,
			texts:    []string{"Quota exceeded for project"},
			want:     true,
		},
		{
			name:     "normal output",
			provider: ProviderClaude,
			texts:    []string{"Here is the code you requested", "function foo() { return 42; }"},
			want:     false,
		},
		{
			name:     "multiple lines with rate limit at end",
			provider: ProviderClaude,
			texts:    []string{"Starting...", "Processing...", "Error: rate limit"},
			want:     true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d, err := NewDetector(tt.provider, nil)
			if err != nil {
				t.Fatalf("NewDetector() error = %v", err)
			}

			var got bool
			for _, text := range tt.texts {
				got = d.Check(text)
			}

			if got != tt.want {
				t.Errorf("Check() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestDetector_DefaultPatternCorpus(t *testing.T) {
	tests := []struct {
		name     string
		provider Provider
		text     string
		want     bool
	}{
		{
			name:     "claude rate limiter prose is not a limit",
			provider: ProviderClaude,
			text:     "Refactor the rate limiter implementation and add tests.",
			want:     false,
		},
		{
			name:     "claude rate limits docs are not a limit",
			provider: ProviderClaude,
			text:     "Document API rate limits in the README.",
			want:     false,
		},
		{
			name:     "claude capacity planning is not saturation",
			provider: ProviderClaude,
			text:     "Capacity planning notes: add two more workers next week.",
			want:     false,
		},
		{
			name:     "claude estimated capacity is not saturation",
			provider: ProviderClaude,
			text:     "Estimated capacity planning notes for next quarter.",
			want:     false,
		},
		{
			name:     "claude metadata capacity field is not saturation",
			provider: ProviderClaude,
			text:     "The metadata capacity fields are optional.",
			want:     false,
		},
		{
			name:     "claude http 429 fixture is not enough",
			provider: ProviderClaude,
			text:     "Unit test fixture for HTTP 429 responses.",
			want:     false,
		},
		{
			name:     "claude too many requests docs are not enough",
			provider: ProviderClaude,
			text:     "Document how the API maps Too Many Requests to retry logic.",
			want:     false,
		},
		{
			name:     "claude plain 429 too many requests docs are not enough",
			provider: ProviderClaude,
			text:     "HTTP 429 Too Many Requests is documented in the fixture table.",
			want:     false,
		},
		{
			name:     "claude capacity saturation",
			provider: ProviderClaude,
			text:     "Claude is over capacity, please try again later.",
			want:     true,
		},
		{
			name:     "claude rate limit error machine string",
			provider: ProviderClaude,
			text:     `{"type":"rate_limit_error","message":"retry later"}`,
			want:     true,
		},
		{
			name:     "claude usage limit exceeded machine string",
			provider: ProviderClaude,
			text:     "usage_limit_exceeded",
			want:     true,
		},
		{
			name:     "claude contextual 429 response",
			provider: ProviderClaude,
			text:     `{"error":{"status":429,"message":"Too Many Requests, try again later"}}`,
			want:     true,
		},
		{
			name:     "claude contextual too many requests response",
			provider: ProviderClaude,
			text:     "Error: Too Many Requests, retry later.",
			want:     true,
		},
		{
			name:     "claude hit limit",
			provider: ProviderClaude,
			text:     "You've hit your limit · resets at 5pm.",
			want:     true,
		},
		{
			name:     "ansi wrapped rate limit",
			provider: ProviderClaude,
			text:     "\x1b[31mrate\x1b[0m \x1b[1mlimit\x1b[0m exceeded",
			want:     true,
		},
		{
			name:     "codex rate limited",
			provider: ProviderCodex,
			text:     "Request failed: rate limited, try again later.",
			want:     true,
		},
		{
			name:     "codex rate-limit hit",
			provider: ProviderCodex,
			text:     "rate-limit hit for this account",
			want:     true,
		},
		{
			name:     "codex rate limiter prose is not a limit",
			provider: ProviderCodex,
			text:     "This package implements a rate limiter.",
			want:     false,
		},
		{
			name:     "codex rate limiting middleware is not a limit",
			provider: ProviderCodex,
			text:     "This package implements rate limiting middleware.",
			want:     false,
		},
		{
			name:     "codex too many requests fixture is not enough",
			provider: ProviderCodex,
			text:     "Use an HTTP 429 Too Many Requests fixture in this test.",
			want:     false,
		},
		{
			name:     "codex rate limit exceeded machine string",
			provider: ProviderCodex,
			text:     `{"code":"rate_limit_exceeded","message":"try again later"}`,
			want:     true,
		},
		{
			name:     "codex rate limit error class",
			provider: ProviderCodex,
			text:     "RateLimitError: retry later",
			want:     true,
		},
		{
			name:     "codex contextual 429 response",
			provider: ProviderCodex,
			text:     `{"status":429,"error":"too many requests; retry later"}`,
			want:     true,
		},
		{
			name:     "gemini quota config is not exhaustion",
			provider: ProviderGemini,
			text:     "Loaded quota configuration from project settings.",
			want:     false,
		},
		{
			name:     "gemini quota limit config is not exhaustion",
			provider: ProviderGemini,
			text:     "Quota limits are configured per project.",
			want:     false,
		},
		{
			name:     "gemini quota limit setting is not exhaustion",
			provider: ProviderGemini,
			text:     "Loaded quota limit configuration from project settings.",
			want:     false,
		},
		{
			name:     "gemini 429 docs are not enough",
			provider: ProviderGemini,
			text:     "The docs mention 429 Too Many Requests as a possible response.",
			want:     false,
		},
		{
			name:     "gemini quota exceeded",
			provider: ProviderGemini,
			text:     "Quota exceeded for project location us-central1.",
			want:     true,
		},
		{
			name:     "gemini rate limit exceeded machine string",
			provider: ProviderGemini,
			text:     `reason: "rateLimitExceeded"`,
			want:     true,
		},
		{
			name:     "gemini quota exceeded machine string",
			provider: ProviderGemini,
			text:     `reason: "quotaExceeded"`,
			want:     true,
		},
		{
			name:     "gemini quota limit exceeded machine string",
			provider: ProviderGemini,
			text:     "quota_limit_exceeded",
			want:     true,
		},
		{
			name:     "gemini contextual too many requests response",
			provider: ProviderGemini,
			text:     "Request failed: Too Many Requests, try again later.",
			want:     true,
		},
		{
			name:     "gemini resource exhausted",
			provider: ProviderGemini,
			text:     "RESOURCE_EXHAUSTED: quota exhausted",
			want:     true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d, err := NewDetector(tt.provider, nil)
			if err != nil {
				t.Fatalf("NewDetector() error = %v", err)
			}
			if got := d.Check(tt.text); got != tt.want {
				t.Fatalf("Check(%q) = %v, want %v; reason=%q", tt.text, got, tt.want, d.Reason())
			}
		})
	}
}

func TestDetector_StickyDetection(t *testing.T) {
	d, err := NewDetector(ProviderClaude, nil)
	if err != nil {
		t.Fatalf("NewDetector() error = %v", err)
	}

	// Initially not detected
	if d.Detected() {
		t.Error("Detected() = true before any check")
	}

	// Normal output
	d.Check("Hello, world!")
	if d.Detected() {
		t.Error("Detected() = true after normal output")
	}

	// Rate limit detected
	d.Check("rate limit exceeded")
	if !d.Detected() {
		t.Error("Detected() = false after rate limit")
	}

	// Should remain true after more checks
	d.Check("normal text again")
	if !d.Detected() {
		t.Error("Detected() = false, should be sticky")
	}

	// Reset should clear
	d.Reset()
	if d.Detected() {
		t.Error("Detected() = true after Reset()")
	}
}

func TestDetector_Reason(t *testing.T) {
	d, err := NewDetector(ProviderClaude, nil)
	if err != nil {
		t.Fatalf("NewDetector() error = %v", err)
	}

	// No reason before detection
	if d.Reason() != "" {
		t.Errorf("Reason() = %q before detection, want empty", d.Reason())
	}

	// Detect rate limit
	d.Check("Error: rate limit exceeded")

	reason := d.Reason()
	if reason == "" {
		t.Error("Reason() is empty after detection")
	}
	if !strings.Contains(strings.ToLower(reason), "rate") {
		t.Errorf("Reason() = %q, expected to contain 'rate'", reason)
	}
}

func TestObservingWriter(t *testing.T) {
	d, err := NewDetector(ProviderClaude, nil)
	if err != nil {
		t.Fatalf("NewDetector() error = %v", err)
	}

	var lines []string
	w := NewObservingWriter(d, func(line string) {
		lines = append(lines, line)
	})

	// Write some data with newlines
	input := "Line 1\nLine 2\nrate limit error\nLine 4\n"
	n, err := w.Write([]byte(input))
	if err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	if n != len(input) {
		t.Errorf("Write() = %d, want %d", n, len(input))
	}

	// Should have detected rate limit
	if !d.Detected() {
		t.Error("Detected() = false after rate limit in stream")
	}

	// Should have captured all lines
	if len(lines) != 4 {
		t.Errorf("callback called %d times, want 4", len(lines))
	}
}

func TestObservingWriter_PartialLines(t *testing.T) {
	d, err := NewDetector(ProviderClaude, nil)
	if err != nil {
		t.Fatalf("NewDetector() error = %v", err)
	}

	var lines []string
	w := NewObservingWriter(d, func(line string) {
		lines = append(lines, line)
	})

	// Write partial data
	w.Write([]byte("Hello "))
	w.Write([]byte("rate "))
	w.Write([]byte("limit reached\n"))
	w.Write([]byte("Done"))
	w.Flush()

	// Should have detected rate limit
	if !d.Detected() {
		t.Error("Detected() = false after rate limit across writes")
	}

	// Should have two lines
	if len(lines) != 2 {
		t.Errorf("callback called %d times, want 2", len(lines))
	}
}

func TestObservingWriterDetectsNoNewlinePrompt(t *testing.T) {
	d, err := NewDetector(ProviderClaude, nil)
	if err != nil {
		t.Fatalf("NewDetector() error = %v", err)
	}

	w := NewObservingWriter(d, nil)
	if _, err := w.Write([]byte("You've hit your limit · resets at 5pm")); err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	if !d.Detected() {
		t.Fatal("Detector did not catch no-newline rate-limit prompt")
	}
}

func TestProviderFromString(t *testing.T) {
	tests := []struct {
		input string
		want  Provider
	}{
		{"claude", ProviderClaude},
		{"Claude", ProviderClaude},
		{"CLAUDE", ProviderClaude},
		{"codex", ProviderCodex},
		{"Codex", ProviderCodex},
		{"gemini", ProviderGemini},
		{"Gemini", ProviderGemini},
		{"unknown", ProviderClaude}, // default
		{"", ProviderClaude},        // default
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := ProviderFromString(tt.input)
			if got != tt.want {
				t.Errorf("ProviderFromString(%q) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}

func TestDefaultPatterns(t *testing.T) {
	patterns := DefaultPatterns()

	// Should have patterns for all providers
	if len(patterns[ProviderClaude]) == 0 {
		t.Error("No default patterns for Claude")
	}
	if len(patterns[ProviderCodex]) == 0 {
		t.Error("No default patterns for Codex")
	}
	if len(patterns[ProviderGemini]) == 0 {
		t.Error("No default patterns for Gemini")
	}
}
