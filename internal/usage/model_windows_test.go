package usage

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestCodexFetcher_Fetch_ModelWindowsAdditionalRateLimits(t *testing.T) {
	const reset5h = 1893474000
	const resetWeekly = 1893970800
	payload := `{
		"plan_type": "pro",
		"rate_limit": {
			"primary_window": {"used_percent": 11, "limit_window_seconds": 18000, "reset_at": 1893470400},
			"secondary_window": {"used_percent": 33, "limit_window_seconds": 604800, "reset_at": 1893974400}
		},
		"additional_rate_limits": [
			{
				"limit_name": "GPT-5.3-Codex-Spark",
				"metered_feature": "codex_bengalfox",
				"rate_limit": {
					"primary_window": {"used_percent": 22, "limit_window_seconds": 18000, "reset_at": 1893474000},
					"secondary_window": {"used_percent": 41, "limit_window_seconds": 604800, "reset_at": 1893970800}
				}
			}
		]
	}`

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != CodexUsagePath {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, payload)
	}))
	defer server.Close()

	fetcher := NewCodexFetcher()
	fetcher.baseURL = server.URL

	info, err := fetcher.Fetch(context.Background(), "offline-token")
	if err != nil {
		t.Fatalf("Fetch() error = %v", err)
	}
	if info.ModelWindows == nil {
		t.Fatal("expected model windows")
	}
	if len(info.ModelWindows) != 2 {
		t.Fatalf("ModelWindows len = %d, want 2", len(info.ModelWindows))
	}

	fiveHour := info.ModelWindows["codex_bengalfox/5h"]
	if fiveHour == nil {
		t.Fatalf("missing codex_bengalfox/5h: %#v", info.ModelWindows)
	}
	if fiveHour.UsedPercent != 22 || fiveHour.Utilization != 0.22 {
		t.Fatalf("5h window usage = %d/%v, want 22/0.22", fiveHour.UsedPercent, fiveHour.Utilization)
	}
	if fiveHour.WindowDuration != 5*time.Hour {
		t.Fatalf("5h duration = %v, want 5h", fiveHour.WindowDuration)
	}
	if !fiveHour.ResetsAt.Equal(time.Unix(reset5h, 0)) {
		t.Fatalf("5h reset = %v, want %v", fiveHour.ResetsAt, time.Unix(reset5h, 0))
	}
	if fiveHour.Label != "GPT-5.3-Codex-Spark" {
		t.Fatalf("5h label = %q", fiveHour.Label)
	}

	weekly := info.ModelWindows["codex_bengalfox/weekly"]
	if weekly == nil {
		t.Fatalf("missing codex_bengalfox/weekly: %#v", info.ModelWindows)
	}
	if weekly.UsedPercent != 41 || weekly.Utilization != 0.41 {
		t.Fatalf("weekly window usage = %d/%v, want 41/0.41", weekly.UsedPercent, weekly.Utilization)
	}
	if weekly.WindowDuration != 7*24*time.Hour {
		t.Fatalf("weekly duration = %v, want 168h", weekly.WindowDuration)
	}
	if !weekly.ResetsAt.Equal(time.Unix(resetWeekly, 0)) {
		t.Fatalf("weekly reset = %v, want %v", weekly.ResetsAt, time.Unix(resetWeekly, 0))
	}

	if got := info.FindModelWindow("spark", "5h"); got != fiveHour {
		t.Fatalf("FindModelWindow(spark, 5h) = %#v, want 5h window", got)
	}
	if got := info.FindModelWindow("spark", "weekly"); got != weekly {
		t.Fatalf("FindModelWindow(spark, weekly) = %#v, want weekly window", got)
	}
}

func TestCodexFetcher_Fetch_ModelWindowsNilWithoutAdditionalRateLimits(t *testing.T) {
	payload := `{"plan_type":"pro","rate_limit":{"primary_window":{"used_percent":7,"limit_window_seconds":18000,"reset_at":1893470400}}}`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, payload)
	}))
	defer server.Close()

	fetcher := NewCodexFetcher()
	fetcher.baseURL = server.URL

	info, err := fetcher.Fetch(context.Background(), "offline-token")
	if err != nil {
		t.Fatalf("Fetch() error = %v", err)
	}
	if info.ModelWindows != nil {
		t.Fatalf("ModelWindows = %#v, want nil", info.ModelWindows)
	}
}

func TestClaudeFetcher_Fetch_ModelWindowsExtraWindows(t *testing.T) {
	payload := `{
		"five_hour": {"utilization": 25, "resets_at": "2030-01-01T05:00:00Z"},
		"seven_day": {"utilization": 0.4, "resets_at": "2030-01-07T00:00:00Z"},
		"seven_day_opus": {"utilization": 66, "resets_at": "2030-01-07T01:00:00Z"},
		"seven_day_sonnet": {"utilization": 12, "resets_at": "2030-01-07T02:00:00Z"},
		"plan": "max",
		"internal_codename": null
	}`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, payload)
	}))
	defer server.Close()

	fetcher := NewClaudeFetcher()
	fetcher.baseURL = server.URL

	info, err := fetcher.Fetch(context.Background(), "offline-token")
	if err != nil {
		t.Fatalf("Fetch() error = %v", err)
	}

	if info.PrimaryWindow == nil || info.PrimaryWindow.UsedPercent != 25 || info.PrimaryWindow.WindowDuration != 5*time.Hour {
		t.Fatalf("primary window = %#v, want 25%%/5h", info.PrimaryWindow)
	}
	if info.SecondaryWindow == nil || info.SecondaryWindow.UsedPercent != 40 || info.SecondaryWindow.WindowDuration != 7*24*time.Hour {
		t.Fatalf("secondary window = %#v, want 40%%/168h", info.SecondaryWindow)
	}
	if len(info.ModelWindows) != 2 {
		t.Fatalf("ModelWindows len = %d, want 2: %#v", len(info.ModelWindows), info.ModelWindows)
	}
	if _, ok := info.ModelWindows["plan"]; ok {
		t.Fatal("plan string key should not be captured as a model window")
	}

	opus := info.ModelWindows["seven_day_opus"]
	if opus == nil {
		t.Fatalf("missing seven_day_opus: %#v", info.ModelWindows)
	}
	if opus.UsedPercent != 66 || opus.Utilization != 0.66 || opus.WindowDuration != 7*24*time.Hour {
		t.Fatalf("opus window = %#v, want 66%%/168h", opus)
	}
	if info.TertiaryWindow != opus {
		t.Fatalf("TertiaryWindow = %#v, want seven_day_opus window %#v", info.TertiaryWindow, opus)
	}

	sonnet := info.ModelWindows["seven_day_sonnet"]
	if sonnet == nil {
		t.Fatalf("missing seven_day_sonnet: %#v", info.ModelWindows)
	}
	if sonnet.UsedPercent != 12 || sonnet.Utilization != 0.12 || sonnet.WindowDuration != 7*24*time.Hour {
		t.Fatalf("sonnet window = %#v, want 12%%/168h", sonnet)
	}
}

func TestClaudeFetcher_Fetch_ModelWindowsNilWithoutExtraWindows(t *testing.T) {
	payload := `{
		"five_hour": {"utilization": 25, "resets_at": "2030-01-01T05:00:00Z"},
		"seven_day": {"utilization": 40, "resets_at": "2030-01-07T00:00:00Z"}
	}`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, payload)
	}))
	defer server.Close()

	fetcher := NewClaudeFetcher()
	fetcher.baseURL = server.URL

	info, err := fetcher.Fetch(context.Background(), "offline-token")
	if err != nil {
		t.Fatalf("Fetch() error = %v", err)
	}
	if info.ModelWindows != nil {
		t.Fatalf("ModelWindows = %#v, want nil", info.ModelWindows)
	}
	if info.TertiaryWindow != nil {
		t.Fatalf("TertiaryWindow = %#v, want nil", info.TertiaryWindow)
	}
}

func TestUsageInfo_FindModelWindow(t *testing.T) {
	t.Run("nil safe", func(t *testing.T) {
		var info *UsageInfo
		if got := info.FindModelWindow("spark", "weekly"); got != nil {
			t.Fatalf("nil info returned %#v, want nil", got)
		}
		if got := (&UsageInfo{}).FindModelWindow("spark", "weekly"); got != nil {
			t.Fatalf("empty info returned %#v, want nil", got)
		}
	})

	t.Run("matches label case-insensitively and suffix", func(t *testing.T) {
		fiveHour := &UsageWindow{UsedPercent: 22, Label: "GPT-5.3-Codex-Spark"}
		weekly := &UsageWindow{UsedPercent: 41, Label: "GPT-5.3-Codex-Spark"}
		info := &UsageInfo{ModelWindows: map[string]*UsageWindow{
			"codex_bengalfox/5h":     fiveHour,
			"codex_bengalfox/weekly": weekly,
		}}

		if got := info.FindModelWindow("SPARK", "5H"); got != fiveHour {
			t.Fatalf("FindModelWindow(SPARK, 5H) = %#v, want five-hour", got)
		}
		if got := info.FindModelWindow("spark", "weekly"); got != weekly {
			t.Fatalf("FindModelWindow(spark, weekly) = %#v, want weekly", got)
		}
	})

	t.Run("tie breaks by key", func(t *testing.T) {
		first := &UsageWindow{UsedPercent: 1}
		second := &UsageWindow{UsedPercent: 2}
		info := &UsageInfo{ModelWindows: map[string]*UsageWindow{
			"z-spark/weekly": second,
			"a-spark/weekly": first,
		}}
		if got := info.FindModelWindow("spark", "weekly"); got != first {
			t.Fatalf("FindModelWindow tie = %#v, want lexicographically first", got)
		}
	})

	t.Run("matches claude window family prefix", func(t *testing.T) {
		opus := &UsageWindow{UsedPercent: 66}
		info := &UsageInfo{ModelWindows: map[string]*UsageWindow{
			"seven_day_opus": opus,
		}}
		if got := info.FindModelWindow("opus", "seven_day"); got != opus {
			t.Fatalf("FindModelWindow(opus, seven_day) = %#v, want opus", got)
		}
	})
}
