package monitor

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/Dicklesworthstone/coding_agent_account_manager/internal/authpool"
	"github.com/Dicklesworthstone/coding_agent_account_manager/internal/health"
	"github.com/Dicklesworthstone/coding_agent_account_manager/internal/usage"
)

func TestTableRendererOutput(t *testing.T) {
	state := buildTestState(42)

	renderer := NewTableRenderer()
	renderer.Width = 60
	renderer.ShowEmoji = true

	out := renderer.Render(state)
	if !strings.Contains(out, "LIVE USAGE MONITOR") {
		t.Fatalf("table output missing header: %q", out)
	}
	if !strings.Contains(out, "CLAUDE") {
		t.Fatalf("table output missing provider: %q", out)
	}
	if !strings.Contains(out, "alice") {
		t.Fatalf("table output missing profile name: %q", out)
	}
}

func TestBriefRendererLength(t *testing.T) {
	state := &MonitorState{
		UpdatedAt: time.Now(),
		Profiles: map[string]*ProfileState{
			"claude/alice": buildProfile("claude", "alice", 42),
			"codex/bob":    buildProfile("codex", "bob", 67),
			"gemini/carl":  buildProfile("gemini", "carl", 12),
		},
	}

	renderer := NewBriefRenderer()
	out := renderer.Render(state)
	if len(out) > 80 {
		t.Fatalf("brief output too long: %d", len(out))
	}
	if !strings.Contains(out, "claude:") {
		t.Fatalf("brief output missing provider: %q", out)
	}
}

func TestJSONRendererValid(t *testing.T) {
	state := buildTestState(55)
	renderer := NewJSONRenderer(false)

	out := renderer.Render(state)
	if !json.Valid([]byte(out)) {
		t.Fatalf("json output invalid: %s", out)
	}

	var payload struct {
		Profiles []map[string]interface{} `json:"profiles"`
	}
	if err := json.Unmarshal([]byte(out), &payload); err != nil {
		t.Fatalf("json unmarshal failed: %v", err)
	}
	if len(payload.Profiles) != 1 {
		t.Fatalf("profiles length = %d, want 1", len(payload.Profiles))
	}
}

func TestJSONRendererOmitsUnknownPoolStatus(t *testing.T) {
	state := &MonitorState{
		UpdatedAt: time.Date(2026, 7, 4, 1, 0, 0, 0, time.UTC),
		Profiles: map[string]*ProfileState{
			"claude/alice": {
				Provider:    "claude",
				ProfileName: "alice",
				Health:      health.StatusHealthy,
				PoolStatus:  authpool.PoolStatusUnknown,
			},
			"codex/bob": {
				Provider:    "codex",
				ProfileName: "bob",
				Health:      health.StatusHealthy,
				PoolStatus:  authpool.PoolStatusReady,
			},
		},
	}

	out := NewJSONRenderer(false).Render(state)
	var payload struct {
		Profiles []map[string]interface{} `json:"profiles"`
	}
	if err := json.Unmarshal([]byte(out), &payload); err != nil {
		t.Fatalf("json unmarshal failed: %v; output=%s", err, out)
	}
	if len(payload.Profiles) != 2 {
		t.Fatalf("profiles length = %d, want 2", len(payload.Profiles))
	}

	byName := make(map[string]map[string]interface{}, len(payload.Profiles))
	for _, profile := range payload.Profiles {
		name, _ := profile["profile_name"].(string)
		byName[name] = profile
	}
	if _, ok := byName["alice"]["pool_status"]; ok {
		t.Fatalf("unknown pool status should be omitted, got profile: %#v", byName["alice"])
	}
	if got := byName["bob"]["pool_status"]; got != "ready" {
		t.Fatalf("ready pool status = %#v, want ready; profile=%#v", got, byName["bob"])
	}
}

func TestJSONRendererModelWindowsPresentWhenSetAndOmittedWhenNil(t *testing.T) {
	now := time.Date(2026, 7, 3, 20, 0, 0, 0, time.UTC)
	state := &MonitorState{
		UpdatedAt: now,
		Profiles: map[string]*ProfileState{
			"codex/with-models": {
				Provider:    "codex",
				ProfileName: "with-models",
				Usage: &usage.UsageInfo{
					Provider:    "codex",
					ProfileName: "with-models",
					PrimaryWindow: &usage.UsageWindow{
						UsedPercent:    31,
						Utilization:    0.31,
						ResetsAt:       now.Add(5 * time.Hour),
						WindowDuration: 5 * time.Hour,
						Label:          "5H",
					},
					SecondaryWindow: &usage.UsageWindow{
						UsedPercent:    44,
						Utilization:    0.44,
						ResetsAt:       now.Add(7 * 24 * time.Hour),
						WindowDuration: 7 * 24 * time.Hour,
						Label:          "WK",
					},
					ModelWindows: map[string]*usage.UsageWindow{
						"gpt-5.3-codex-spark/weekly": {
							UsedPercent:    67,
							Utilization:    0.67,
							ResetsAt:       now.Add(7 * 24 * time.Hour),
							WindowDuration: 7 * 24 * time.Hour,
							Label:          "GPT-5.3-Codex-Spark",
						},
					},
					FetchedAt: now,
				},
				Health:     health.StatusHealthy,
				PoolStatus: authpool.PoolStatusReady,
			},
			"codex/without-models": {
				Provider:    "codex",
				ProfileName: "without-models",
				Usage: &usage.UsageInfo{
					Provider:    "codex",
					ProfileName: "without-models",
					PrimaryWindow: &usage.UsageWindow{
						UsedPercent: 11,
						Utilization: 0.11,
						ResetsAt:    now.Add(5 * time.Hour),
					},
					SecondaryWindow: &usage.UsageWindow{
						UsedPercent: 22,
						Utilization: 0.22,
						ResetsAt:    now.Add(7 * 24 * time.Hour),
					},
					FetchedAt: now,
				},
				Health:     health.StatusHealthy,
				PoolStatus: authpool.PoolStatusReady,
			},
			"codex/empty-models": {
				Provider:    "codex",
				ProfileName: "empty-models",
				Usage: &usage.UsageInfo{
					Provider:      "codex",
					ProfileName:   "empty-models",
					PrimaryWindow: &usage.UsageWindow{UsedPercent: 9, Utilization: 0.09},
					ModelWindows:  map[string]*usage.UsageWindow{},
					FetchedAt:     now,
				},
				Health:     health.StatusHealthy,
				PoolStatus: authpool.PoolStatusReady,
			},
		},
	}

	out := NewJSONRenderer(false).Render(state)
	if !json.Valid([]byte(out)) {
		t.Fatalf("json output invalid: %s", out)
	}

	var payload struct {
		Profiles []struct {
			ProfileName string          `json:"profile_name"`
			Usage       json.RawMessage `json:"usage"`
		} `json:"profiles"`
	}
	if err := json.Unmarshal([]byte(out), &payload); err != nil {
		t.Fatalf("json unmarshal failed: %v", err)
	}
	if len(payload.Profiles) != 3 {
		t.Fatalf("profiles length = %d, want 3", len(payload.Profiles))
	}

	usageByProfile := make(map[string]map[string]json.RawMessage)
	for _, profile := range payload.Profiles {
		var usageFields map[string]json.RawMessage
		if err := json.Unmarshal(profile.Usage, &usageFields); err != nil {
			t.Fatalf("usage unmarshal for %s failed: %v", profile.ProfileName, err)
		}
		usageByProfile[profile.ProfileName] = usageFields
	}

	withModels := usageByProfile["with-models"]
	if len(withModels["primary_window"]) == 0 {
		t.Fatalf("with-models usage missing primary_window: %s", out)
	}
	if len(withModels["secondary_window"]) == 0 {
		t.Fatalf("with-models usage missing secondary_window: %s", out)
	}
	modelWindowsRaw := withModels["model_windows"]
	if len(modelWindowsRaw) == 0 {
		t.Fatalf("with-models usage missing model_windows: %s", out)
	}
	var modelWindows map[string]usage.UsageWindow
	if err := json.Unmarshal(modelWindowsRaw, &modelWindows); err != nil {
		t.Fatalf("model_windows unmarshal failed: %v", err)
	}
	sparkWindow, ok := modelWindows["gpt-5.3-codex-spark/weekly"]
	if !ok {
		t.Fatalf("model_windows missing spark weekly entry: %#v", modelWindows)
	}
	if sparkWindow.UsedPercent != 67 || sparkWindow.Label != "GPT-5.3-Codex-Spark" {
		t.Fatalf("spark weekly window = %+v, want percent 67 with label", sparkWindow)
	}

	withoutModels := usageByProfile["without-models"]
	if len(withoutModels["primary_window"]) == 0 {
		t.Fatalf("without-models usage missing primary_window: %s", out)
	}
	if len(withoutModels["secondary_window"]) == 0 {
		t.Fatalf("without-models usage missing secondary_window: %s", out)
	}
	if _, ok := withoutModels["model_windows"]; ok {
		t.Fatalf("without-models usage should omit model_windows when nil: %s", out)
	}

	emptyModels := usageByProfile["empty-models"]
	if _, ok := emptyModels["model_windows"]; ok {
		t.Fatalf("empty-models usage should omit model_windows when empty: %s", out)
	}
}

func TestBriefRendererOutputIgnoresModelWindows(t *testing.T) {
	withoutModels := buildModelWindowParityState(false)
	withModels := buildModelWindowParityState(true)

	withoutOut := NewBriefRenderer().Render(withoutModels)
	withOut := NewBriefRenderer().Render(withModels)
	if withOut != withoutOut {
		t.Fatalf("brief output changed with ModelWindows:\nwithout: %q\nwith:    %q", withoutOut, withOut)
	}
	if !strings.Contains(withOut, "42%") {
		t.Fatalf("brief output should use primary window percent, got %q", withOut)
	}
	if strings.Contains(withOut, "54%") || strings.Contains(withOut, "99%") {
		t.Fatalf("brief output should ignore secondary/model percentages, got %q", withOut)
	}
}

func TestAlertRendererOutputIgnoresModelWindows(t *testing.T) {
	withoutModels := buildModelWindowParityState(false)
	withModels := buildModelWindowParityState(true)

	withoutOut := NewAlertRenderer(80).Render(withoutModels)
	withOut := NewAlertRenderer(80).Render(withModels)
	if withOut != withoutOut {
		t.Fatalf("alerts output changed with ModelWindows:\nwithout: %q\nwith:    %q", withoutOut, withOut)
	}

	primaryHigh := buildAlertParityState(90, 10, 10)
	primaryOut := NewAlertRenderer(80).Render(primaryHigh)
	if !strings.Contains(primaryOut, "90%") {
		t.Fatalf("primary high usage should trigger alert, got %q", primaryOut)
	}

	modelHigh := buildAlertParityState(10, 90, 99)
	modelOut := NewAlertRenderer(80).Render(modelHigh)
	if strings.TrimSpace(modelOut) != "" {
		t.Fatalf("secondary/model high usage should not trigger primary alert, got %q", modelOut)
	}
}

func TestAlertRendererDedupes(t *testing.T) {
	state := buildTestState(86)
	renderer := NewAlertRenderer(80)

	first := renderer.Render(state)
	if strings.TrimSpace(first) == "" {
		t.Fatal("expected initial alert output")
	}

	second := renderer.Render(state)
	if strings.TrimSpace(second) != "" {
		t.Fatalf("expected deduped alert output, got %q", second)
	}

	state.Profiles["claude/alice"].Usage.PrimaryWindow.UsedPercent = 96
	third := renderer.Render(state)
	if strings.TrimSpace(third) == "" {
		t.Fatal("expected alert output after escalation")
	}
}

func buildTestState(percent int) *MonitorState {
	return &MonitorState{
		UpdatedAt: time.Now(),
		Profiles: map[string]*ProfileState{
			"claude/alice": buildProfile("claude", "alice", percent),
		},
	}
}

func buildProfile(provider, name string, percent int) *ProfileState {
	return &ProfileState{
		Provider:    provider,
		ProfileName: name,
		Usage: &usage.UsageInfo{
			Provider:    provider,
			ProfileName: name,
			PrimaryWindow: &usage.UsageWindow{
				UsedPercent: percent,
			},
		},
		Health:     health.StatusHealthy,
		PoolStatus: authpool.PoolStatusReady,
	}
}

func buildModelWindowParityState(includeModelWindows bool) *MonitorState {
	info := &usage.UsageInfo{
		Provider:    "codex",
		ProfileName: "spark",
		PrimaryWindow: &usage.UsageWindow{
			UsedPercent: 42,
			Utilization: 0.42,
		},
		SecondaryWindow: &usage.UsageWindow{
			UsedPercent: 54,
			Utilization: 0.54,
		},
	}
	if includeModelWindows {
		info.ModelWindows = map[string]*usage.UsageWindow{
			"gpt-5.3-codex-spark/weekly": {
				UsedPercent: 99,
				Utilization: 0.99,
				Label:       "GPT-5.3-Codex-Spark",
			},
		}
	}

	return &MonitorState{
		UpdatedAt: time.Date(2026, 7, 3, 20, 0, 0, 0, time.UTC),
		Profiles: map[string]*ProfileState{
			"codex/spark": {
				Provider:    "codex",
				ProfileName: "spark",
				Usage:       info,
				Health:      health.StatusHealthy,
				PoolStatus:  authpool.PoolStatusReady,
			},
		},
	}
}

func buildAlertParityState(primaryPercent, secondaryPercent, modelPercent int) *MonitorState {
	return &MonitorState{
		UpdatedAt: time.Date(2026, 7, 3, 20, 0, 0, 0, time.UTC),
		Profiles: map[string]*ProfileState{
			"codex/spark": {
				Provider:    "codex",
				ProfileName: "spark",
				Usage: &usage.UsageInfo{
					Provider:        "codex",
					ProfileName:     "spark",
					PrimaryWindow:   &usage.UsageWindow{UsedPercent: primaryPercent, Utilization: float64(primaryPercent) / 100},
					SecondaryWindow: &usage.UsageWindow{UsedPercent: secondaryPercent, Utilization: float64(secondaryPercent) / 100},
					ModelWindows: map[string]*usage.UsageWindow{
						"gpt-5.3-codex-spark/weekly": {
							UsedPercent: modelPercent,
							Utilization: float64(modelPercent) / 100,
							Label:       "GPT-5.3-Codex-Spark",
						},
					},
				},
				Health:     health.StatusHealthy,
				PoolStatus: authpool.PoolStatusReady,
			},
		},
	}
}

// TestUsageUnavailable verifies issue #37: a profile with an errored/empty usage
// fetch reports a human reason instead of looking like genuine 0% usage.
func TestUsageUnavailable(t *testing.T) {
	if r := usageUnavailable(nil); r != "no usage data" {
		t.Errorf("nil info: got %q, want %q", r, "no usage data")
	}

	cases := []struct {
		errMsg string
		want   string
	}{
		{"token expired or invalid", "no usage: auth expired (re-login)"},
		{"401 Unauthorized", "no usage: auth expired (re-login)"},
		{"missing access token", "no usage: not logged in"},
		{"usage not yet supported for opencode", "no usage: usage not supported"},
	}
	for _, tc := range cases {
		got := usageUnavailable(&usage.UsageInfo{Error: tc.errMsg})
		if got != tc.want {
			t.Errorf("usageUnavailable(Error=%q) = %q, want %q", tc.errMsg, got, tc.want)
		}
	}

	// Real data present -> no reason (show the number instead).
	withWindow := &usage.UsageInfo{PrimaryWindow: &usage.UsageWindow{UsedPercent: 17}}
	if r := usageUnavailable(withWindow); r != "" {
		t.Errorf("with window: got %q, want empty", r)
	}
	withCredits := &usage.UsageInfo{Credits: &usage.CreditInfo{HasCredits: true}}
	if r := usageUnavailable(withCredits); r != "" {
		t.Errorf("with credits: got %q, want empty", r)
	}
	rateLimited := &usage.UsageInfo{RateLimited: true}
	if r := usageUnavailable(rateLimited); r != "no usage: rate limited (retrying)" {
		t.Errorf("rate limited: got %q, want no usage: rate limited (retrying)", r)
	}
	secondaryOnly := &usage.UsageInfo{
		SecondaryWindow: &usage.UsageWindow{UsedPercent: 61},
		ModelWindows: map[string]*usage.UsageWindow{
			"spark/weekly": {UsedPercent: 99},
		},
	}
	if r := usageUnavailable(secondaryOnly); r != "no usage: no primary usage data" {
		t.Errorf("secondary/model only: got %q, want no primary usage data", r)
	}
	empty := &usage.UsageInfo{}
	if r := usageUnavailable(empty); r != "no usage data" {
		t.Errorf("empty info: got %q, want no usage data", r)
	}
}

// TestTableRendererShowsReasonNotZeroPercent verifies that the live table shows
// the unavailability reason rather than a misleading 0% bar for a logged-in
// account whose usage fetch failed (issue #37).
func TestTableRendererShowsReasonNotZeroPercent(t *testing.T) {
	state := &MonitorState{
		UpdatedAt: time.Now(),
		Profiles: map[string]*ProfileState{
			"claude/erroracct": {
				Provider:    "claude",
				ProfileName: "erroracct",
				Usage:       &usage.UsageInfo{Provider: "claude", Error: "401 Unauthorized"},
				Health:      health.StatusHealthy,
				PoolStatus:  authpool.PoolStatusReady,
			},
		},
	}

	r := NewTableRenderer()
	r.Width = 75
	r.ShowEmoji = false
	out := r.Render(state)

	if !strings.Contains(out, "no usage: auth expired (re-login)") {
		t.Fatalf("expected unavailability reason in output, got:\n%s", out)
	}
	if strings.Contains(out, "  0%") {
		t.Fatalf("errored account must not render a 0%% bar; got:\n%s", out)
	}
}

func TestTableRendererModelWindowDetailLines(t *testing.T) {
	now := time.Now()
	state := &MonitorState{
		UpdatedAt: now,
		Profiles: map[string]*ProfileState{
			"claude/alice": {
				Provider:    "claude",
				ProfileName: "alice",
				Usage: &usage.UsageInfo{
					Provider:      "claude",
					ProfileName:   "alice",
					PrimaryWindow: &usage.UsageWindow{UsedPercent: 42, ResetsAt: now.Add(72*time.Minute + 45*time.Second)},
					SecondaryWindow: &usage.UsageWindow{
						UsedPercent: 63,
						ResetsAt:    now.Add(52*time.Hour + 45*time.Second),
					},
				},
				Health:     health.StatusHealthy,
				PoolStatus: authpool.PoolStatusUnknown,
			},
			"codex/bob": {
				Provider:    "codex",
				ProfileName: "bob",
				Usage: &usage.UsageInfo{
					Provider:        "codex",
					ProfileName:     "bob",
					PrimaryWindow:   &usage.UsageWindow{UsedPercent: 34},
					SecondaryWindow: &usage.UsageWindow{UsedPercent: 54},
					ModelWindows: map[string]*usage.UsageWindow{
						"codex_bengalfox/5h":     {UsedPercent: 22, Label: "GPT-5.3-Codex-Spark"},
						"codex_bengalfox/weekly": {UsedPercent: 41, Label: "GPT-5.3-Codex-Spark"},
					},
				},
				Health:     health.StatusHealthy,
				PoolStatus: authpool.PoolStatusUnknown,
			},
		},
	}

	r := NewTableRenderer()
	r.Width = 75
	out := r.Render(state)

	assertTableLineAlignment(t, out, r.Width)
	contents := tableLineContents(out)
	assertContainsContent(t, contents, "       5H 42% (resets 1h12m) | WK 63% (resets 2d4h)")
	assertContainsContent(t, contents, "       5H 34% | WK 54% | SPARK 5H 22% WK 41%")
}

func TestTableRendererWindowDetailOmissionCases(t *testing.T) {
	now := time.Now()
	state := &MonitorState{
		UpdatedAt: now,
		Profiles: map[string]*ProfileState{
			"claude/error": {
				Provider:    "claude",
				ProfileName: "error",
				Usage:       &usage.UsageInfo{Provider: "claude", Error: "401 Unauthorized"},
				Health:      health.StatusWarning,
				PoolStatus:  authpool.PoolStatusUnknown,
			},
			"claude/empty": {
				Provider:    "claude",
				ProfileName: "empty",
				Usage:       &usage.UsageInfo{Provider: "claude"},
				Health:      health.StatusHealthy,
				PoolStatus:  authpool.PoolStatusUnknown,
			},
			"claude/only5h": {
				Provider:    "claude",
				ProfileName: "only5h",
				Usage: &usage.UsageInfo{
					Provider:      "claude",
					PrimaryWindow: &usage.UsageWindow{UsedPercent: 42, ResetsAt: now.Add(72*time.Minute + 45*time.Second)},
				},
				Health:     health.StatusHealthy,
				PoolStatus: authpool.PoolStatusUnknown,
			},
			"codex/plain": {
				Provider:    "codex",
				ProfileName: "plain",
				Usage: &usage.UsageInfo{
					Provider:        "codex",
					PrimaryWindow:   &usage.UsageWindow{UsedPercent: 34},
					SecondaryWindow: &usage.UsageWindow{UsedPercent: 54},
				},
				Health:     health.StatusHealthy,
				PoolStatus: authpool.PoolStatusUnknown,
			},
		},
	}

	r := NewTableRenderer()
	r.Width = 75
	out := r.Render(state)

	assertTableLineAlignment(t, out, r.Width)
	details := tableDetailContents(out)
	if len(details) != 2 {
		t.Fatalf("detail line count = %d, want 2: %#v\n%s", len(details), details, out)
	}
	assertContainsContent(t, details, "       5H 42% (resets 1h12m)")
	assertContainsContent(t, details, "       5H 34% | WK 54%")
	if strings.Contains(out, "SPARK") {
		t.Fatalf("codex without Spark model windows should not render Spark segment:\n%s", out)
	}
	if strings.Contains(out, "FABLE") {
		t.Fatalf("claude without fable model window should not render FABLE segment:\n%s", out)
	}
}

func TestTableRendererWindowDetailWidth40AndNoEmoji(t *testing.T) {
	now := time.Now()
	state := &MonitorState{
		UpdatedAt: now,
		Profiles: map[string]*ProfileState{
			"codex/bob": {
				Provider:    "codex",
				ProfileName: "bob",
				Usage: &usage.UsageInfo{
					Provider:        "codex",
					PrimaryWindow:   &usage.UsageWindow{UsedPercent: 34, ResetsAt: now.Add(72*time.Minute + 45*time.Second)},
					SecondaryWindow: &usage.UsageWindow{UsedPercent: 54, ResetsAt: now.Add(52*time.Hour + 45*time.Second)},
					ModelWindows: map[string]*usage.UsageWindow{
						"codex_bengalfox/5h":     {UsedPercent: 22, Label: "GPT-5.3-Codex-Spark"},
						"codex_bengalfox/weekly": {UsedPercent: 41, Label: "GPT-5.3-Codex-Spark"},
					},
				},
				Health:     health.StatusHealthy,
				PoolStatus: authpool.PoolStatusUnknown,
			},
		},
	}

	r := NewTableRenderer()
	r.Width = 40
	r.ShowEmoji = false
	out := r.Render(state)

	assertTableLineAlignment(t, out, r.Width)
	details := tableDetailContents(out)
	if len(details) != 1 {
		t.Fatalf("detail line count = %d, want 1: %#v\n%s", len(details), details, out)
	}
	if !strings.HasPrefix(details[0], "       5H 34%") {
		t.Fatalf("detail indent changed with --no-emoji or width 40: %q\n%s", details[0], out)
	}
	if strings.Contains(details[0], "resets") {
		t.Fatalf("width fallback should drop reset hints before truncation: %q\n%s", details[0], out)
	}
}

func TestTableRendererUnknownPoolSuccessUsesResetAndUsageAlert(t *testing.T) {
	now := time.Now()
	state := &MonitorState{
		UpdatedAt: now,
		Profiles: map[string]*ProfileState{
			"codex/arthur": {
				Provider:    "codex",
				ProfileName: "arthur",
				Usage: &usage.UsageInfo{
					Provider:    "codex",
					ProfileName: "arthur",
					PrimaryWindow: &usage.UsageWindow{
						UsedPercent: 34,
						ResetsAt:    now.Add(2*time.Hour + 10*time.Minute),
					},
				},
				Health:     health.StatusWarning,
				PoolStatus: authpool.PoolStatusUnknown,
			},
		},
	}

	r := NewTableRenderer()
	r.Width = 88
	out := r.Render(state)

	if !strings.Contains(out, "[OK] arthur") {
		t.Fatalf("successful low-usage row should use usage OK indicator, got:\n%s", out)
	}
	if strings.Contains(out, "[WARN] arthur") {
		t.Fatalf("successful low-usage row should not inherit stale health WARN, got:\n%s", out)
	}
	if strings.Contains(out, "unknown") {
		t.Fatalf("unknown pool status should not be printed for successful rows, got:\n%s", out)
	}
	if !strings.Contains(out, " | resets ") {
		t.Fatalf("unknown pool status with reset time should render reset suffix, got:\n%s", out)
	}
	assertTableLineAlignment(t, out, r.Width)
}

func TestTableRendererUnknownPoolSuccessOmitsSuffixWithoutReset(t *testing.T) {
	state := &MonitorState{
		UpdatedAt: time.Now(),
		Profiles: map[string]*ProfileState{
			"codex/saumil": {
				Provider:    "codex",
				ProfileName: "saumil",
				Usage: &usage.UsageInfo{
					Provider:    "codex",
					ProfileName: "saumil",
					PrimaryWindow: &usage.UsageWindow{
						UsedPercent: 54,
					},
				},
				Health:     health.StatusWarning,
				PoolStatus: authpool.PoolStatusUnknown,
			},
		},
	}

	r := NewTableRenderer()
	r.Width = 88
	out := r.Render(state)

	if !strings.Contains(out, "[OK] saumil") {
		t.Fatalf("successful low-usage row should use usage OK indicator, got:\n%s", out)
	}
	if strings.Contains(out, "unknown") || strings.Contains(out, " | resets ") {
		t.Fatalf("unknown pool status without reset should omit suffix, got:\n%s", out)
	}
	assertTableLineAlignment(t, out, r.Width)
}

func TestTableRendererUsageAlertIndicatorOverridesStaleHealth(t *testing.T) {
	now := time.Now()
	info := &usage.UsageInfo{
		Provider:    "codex",
		ProfileName: "busy",
		PrimaryWindow: &usage.UsageWindow{
			UsedPercent: 90,
			ResetsAt:    now.Add(time.Hour),
		},
	}
	state := &MonitorState{
		UpdatedAt: now,
		Profiles: map[string]*ProfileState{
			"codex/busy": {
				Provider:    "codex",
				ProfileName: "busy",
				Usage:       info,
				Health:      health.StatusHealthy,
				PoolStatus:  authpool.PoolStatusUnknown,
				Alert:       evaluateAlert(info, now),
			},
		},
	}

	r := NewTableRenderer()
	r.Width = 88
	out := r.Render(state)

	if !strings.Contains(out, "[CRIT] busy") {
		t.Fatalf("successful high-usage row should use usage alert indicator, got:\n%s", out)
	}
	if strings.Contains(out, "[OK] busy") {
		t.Fatalf("critical usage row should not render OK, got:\n%s", out)
	}
	assertTableLineAlignment(t, out, r.Width)
}

func TestTableRendererFailedUsageKeepsHealthIndicatorAndReason(t *testing.T) {
	state := &MonitorState{
		UpdatedAt: time.Now(),
		Profiles: map[string]*ProfileState{
			"claude/erroracct": {
				Provider:    "claude",
				ProfileName: "erroracct",
				Usage:       &usage.UsageInfo{Provider: "claude", Error: "401 Unauthorized"},
				Health:      health.StatusWarning,
				PoolStatus:  authpool.PoolStatusUnknown,
			},
		},
	}

	r := NewTableRenderer()
	r.Width = 88
	out := r.Render(state)

	if !strings.Contains(out, "[WARN] erroracct") {
		t.Fatalf("failed usage row should keep health-driven indicator, got:\n%s", out)
	}
	if !strings.Contains(out, "no usage: auth expired (re-login)") {
		t.Fatalf("failed usage row should keep unavailability reason, got:\n%s", out)
	}
	if strings.Contains(out, "unknown") {
		t.Fatalf("failed usage row should not append pool status, got:\n%s", out)
	}
	assertTableLineAlignment(t, out, r.Width)
}

func TestTableRendererPoolStatusAndCooldownSuffixes(t *testing.T) {
	now := time.Now()
	cooldownUntil := now.Add(45 * time.Minute)
	state := &MonitorState{
		UpdatedAt: now,
		Profiles: map[string]*ProfileState{
			"codex/cooling": {
				Provider:    "codex",
				ProfileName: "cooling",
				Usage: &usage.UsageInfo{
					Provider:      "codex",
					ProfileName:   "cooling",
					PrimaryWindow: &usage.UsageWindow{UsedPercent: 20},
				},
				Health:        health.StatusHealthy,
				PoolStatus:    authpool.PoolStatusUnknown,
				InCooldown:    true,
				CooldownUntil: &cooldownUntil,
			},
			"codex/ready": {
				Provider:    "codex",
				ProfileName: "ready",
				Usage: &usage.UsageInfo{
					Provider:      "codex",
					ProfileName:   "ready",
					PrimaryWindow: &usage.UsageWindow{UsedPercent: 20},
				},
				Health:     health.StatusHealthy,
				PoolStatus: authpool.PoolStatusReady,
			},
		},
	}

	r := NewTableRenderer()
	r.Width = 88
	out := r.Render(state)

	if !strings.Contains(out, "cooling") || !strings.Contains(out, " | cooldown ") {
		t.Fatalf("cooldown row should keep cooldown suffix, got:\n%s", out)
	}
	if !strings.Contains(out, "ready") || !strings.Contains(out, " | ready") {
		t.Fatalf("real pool status should still render, got:\n%s", out)
	}
	assertTableLineAlignment(t, out, r.Width)
}

func TestTableRendererRateLimitedSuffix(t *testing.T) {
	now := time.Now()
	state := &MonitorState{
		UpdatedAt: now,
		Profiles: map[string]*ProfileState{
			"claude/alice": {
				Provider:    "claude",
				ProfileName: "alice",
				Usage: &usage.UsageInfo{
					Provider:    "claude",
					ProfileName: "alice",
					PrimaryWindow: &usage.UsageWindow{
						UsedPercent: 42,
						ResetsAt:    now.Add(time.Hour),
					},
					RateLimited: true,
					RetryAfter:  30 * time.Second,
				},
				Health:     health.StatusWarning,
				PoolStatus: authpool.PoolStatusReady,
			},
		},
	}

	r := NewTableRenderer()
	r.Width = 88
	out := r.Render(state)

	if !strings.Contains(out, "[OK] alice") {
		t.Fatalf("rate-limited retained row should use calm indicator, got:\n%s", out)
	}
	if !strings.Contains(out, "rate limited (retrying)") {
		t.Fatalf("rate-limited retained row should render retrying suffix, got:\n%s", out)
	}
	if strings.Contains(out, " | ready") || strings.Contains(out, " | resets ") {
		t.Fatalf("rate-limited suffix should take precedence over pool/reset suffixes, got:\n%s", out)
	}
	assertTableLineAlignment(t, out, r.Width)
}

func TestTableRendererRateLimitedWithoutWindowShowsReasonNotZero(t *testing.T) {
	state := &MonitorState{
		UpdatedAt: time.Now(),
		Profiles: map[string]*ProfileState{
			"claude/alice": {
				Provider:    "claude",
				ProfileName: "alice",
				Usage: &usage.UsageInfo{
					Provider:    "claude",
					ProfileName: "alice",
					RateLimited: true,
				},
				Health:     health.StatusWarning,
				PoolStatus: authpool.PoolStatusUnknown,
			},
			"claude/bob": {
				Provider:    "claude",
				ProfileName: "bob",
				Usage: &usage.UsageInfo{
					Provider:    "claude",
					ProfileName: "bob",
					Error:       "API error: status 429",
				},
				Health:     health.StatusWarning,
				PoolStatus: authpool.PoolStatusUnknown,
			},
		},
	}

	r := NewTableRenderer()
	r.Width = 88
	out := r.Render(state)

	if strings.Count(out, "no usage: rate limited (retrying)") != 2 {
		t.Fatalf("rate-limited rows should render calm reason for flag and error forms, got:\n%s", out)
	}
	if strings.Contains(out, "  0%") {
		t.Fatalf("rate-limited no-window rows must not render a misleading 0%% bar, got:\n%s", out)
	}
	assertTableLineAlignment(t, out, r.Width)
}

func TestTableRendererCachedInactiveIsCalm(t *testing.T) {
	state := &MonitorState{
		UpdatedAt: time.Now(),
		Profiles: map[string]*ProfileState{
			"claude/alice": {
				Provider:    "claude",
				ProfileName: "alice",
				Usage: &usage.UsageInfo{
					Provider:       "claude",
					ProfileName:    "alice",
					CachedInactive: true,
					Error:          "401 Unauthorized",
				},
				Health:     health.StatusWarning,
				PoolStatus: authpool.PoolStatusUnknown,
			},
		},
	}

	r := NewTableRenderer()
	r.Width = 88
	out := r.Render(state)

	if !strings.Contains(out, "[OK] alice") {
		t.Fatalf("cached inactive row should use calm indicator, got:\n%s", out)
	}
	if !strings.Contains(out, "cached inactive") {
		t.Fatalf("cached inactive row should render cached inactive status, got:\n%s", out)
	}
	if strings.Contains(out, "[WARN] alice") || strings.Contains(out, "auth expired") {
		t.Fatalf("cached inactive row should not render warning/auth-expired language, got:\n%s", out)
	}
	assertTableLineAlignment(t, out, r.Width)
}

func TestTableRendererSecondaryOrModelOnlyDoesNotRenderZeroBar(t *testing.T) {
	state := &MonitorState{
		UpdatedAt: time.Now(),
		Profiles: map[string]*ProfileState{
			"codex/spark": {
				Provider:    "codex",
				ProfileName: "spark",
				Usage: &usage.UsageInfo{
					Provider:        "codex",
					SecondaryWindow: &usage.UsageWindow{UsedPercent: 78},
					ModelWindows: map[string]*usage.UsageWindow{
						"gpt-5.3-codex-spark/weekly": {UsedPercent: 99},
					},
				},
				Health:     health.StatusHealthy,
				PoolStatus: authpool.PoolStatusUnknown,
			},
		},
	}

	r := NewTableRenderer()
	r.Width = 88
	out := r.Render(state)

	if !strings.Contains(out, "no usage: no primary usage data") {
		t.Fatalf("secondary/model-only row should explain missing primary data, got:\n%s", out)
	}
	if strings.Contains(out, "  0%") {
		t.Fatalf("secondary/model-only row must not render a misleading 0%% bar, got:\n%s", out)
	}
	assertTableLineAlignment(t, out, r.Width)
}

func TestTableRendererCooldownTakesPrecedenceOverRateLimitedSuffix(t *testing.T) {
	now := time.Now()
	cooldownUntil := now.Add(45 * time.Minute)
	state := &MonitorState{
		UpdatedAt: now,
		Profiles: map[string]*ProfileState{
			"claude/alice": {
				Provider:    "claude",
				ProfileName: "alice",
				Usage: &usage.UsageInfo{
					Provider:      "claude",
					PrimaryWindow: &usage.UsageWindow{UsedPercent: 42, ResetsAt: now.Add(time.Hour)},
					RateLimited:   true,
				},
				Health:        health.StatusWarning,
				PoolStatus:    authpool.PoolStatusUnknown,
				InCooldown:    true,
				CooldownUntil: &cooldownUntil,
			},
		},
	}

	r := NewTableRenderer()
	r.Width = 88
	out := r.Render(state)

	if !strings.Contains(out, " | cooldown ") {
		t.Fatalf("cooldown suffix should win over rate-limit suffix, got:\n%s", out)
	}
	if strings.Contains(out, "rate limited (retrying)") {
		t.Fatalf("rate-limit suffix should not replace cooldown suffix, got:\n%s", out)
	}
	assertTableLineAlignment(t, out, r.Width)
}

func TestTableRendererResetSuffixUsesPrimaryWindow(t *testing.T) {
	now := time.Now()
	state := &MonitorState{
		UpdatedAt: now,
		Profiles: map[string]*ProfileState{
			"codex/spark": {
				Provider:    "codex",
				ProfileName: "spark",
				Usage: &usage.UsageInfo{
					Provider: "codex",
					PrimaryWindow: &usage.UsageWindow{
						UsedPercent: 42,
						ResetsAt:    now.Add(5*time.Hour + 10*time.Minute),
					},
					ModelWindows: map[string]*usage.UsageWindow{
						"gpt-5.3-codex-spark/weekly": {
							UsedPercent: 99,
							ResetsAt:    now.Add(7 * 24 * time.Hour),
						},
					},
				},
				Health:     health.StatusHealthy,
				PoolStatus: authpool.PoolStatusUnknown,
			},
		},
	}

	r := NewTableRenderer()
	r.Width = 88
	out := r.Render(state)

	if !strings.Contains(out, " | resets 5h") {
		t.Fatalf("reset suffix should use primary window reset, got:\n%s", out)
	}
	if strings.Contains(out, " | resets 7d") {
		t.Fatalf("reset suffix should not use model/weekly reset, got:\n%s", out)
	}
	assertTableLineAlignment(t, out, r.Width)
}

func TestShortUsageErrorRateLimited(t *testing.T) {
	if got := shortUsageError("API error: status 429"); got != "rate limited (retrying)" {
		t.Fatalf("shortUsageError() = %q, want rate limited (retrying)", got)
	}
	if got := shortUsageError("401 Unauthorized; request id abc429def"); got != "auth expired (re-login)" {
		t.Fatalf("shortUsageError() = %q, want auth expired (re-login)", got)
	}
}

func assertTableLineAlignment(t *testing.T, out string, width int) {
	t.Helper()
	for _, line := range strings.Split(strings.TrimRight(out, "\n"), "\n") {
		if len(line) != width {
			t.Fatalf("line length = %d, want %d for line %q in:\n%s", len(line), width, line, out)
		}
	}
}

func tableLineContents(out string) []string {
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	contents := make([]string, 0, len(lines))
	for _, line := range lines {
		if len(line) < 2 || line[0] != '|' || line[len(line)-1] != '|' {
			continue
		}
		contents = append(contents, strings.TrimRight(line[1:len(line)-1], " "))
	}
	return contents
}

func tableDetailContents(out string) []string {
	contents := tableLineContents(out)
	details := make([]string, 0, len(contents))
	for _, content := range contents {
		if strings.HasPrefix(content, "       ") && len(content) > 7 && content[7] != ' ' {
			details = append(details, content)
		}
	}
	return details
}

func assertContainsContent(t *testing.T, contents []string, want string) {
	t.Helper()
	for _, content := range contents {
		if content == want {
			return
		}
	}
	t.Fatalf("missing table content %q in %#v", want, contents)
}
