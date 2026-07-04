package usage

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestFetchAllProfilesSerializesAndSpacesSameProvider(t *testing.T) {
	var inFlight int64
	var maxInFlight int64
	var mu sync.Mutex
	starts := make([]time.Time, 0, 3)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		current := atomic.AddInt64(&inFlight, 1)
		defer atomic.AddInt64(&inFlight, -1)
		for {
			maxSeen := atomic.LoadInt64(&maxInFlight)
			if current <= maxSeen || atomic.CompareAndSwapInt64(&maxInFlight, maxSeen, current) {
				break
			}
		}

		mu.Lock()
		starts = append(starts, time.Now())
		mu.Unlock()

		time.Sleep(10 * time.Millisecond)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"five_hour":{"utilization":10,"resets_at":"2030-01-01T00:00:00Z"}}`))
	}))
	defer server.Close()

	fetcher := NewMultiProfileFetcher(WithSameProviderPacing(20*time.Millisecond, 0))
	fetcher.claudeFetcher.baseURL = server.URL

	results := fetcher.FetchAllProfiles(context.Background(), "claude", map[string]string{
		"charlie": "tok-c",
		"alice":   "tok-a",
		"bob":     "tok-b",
	})

	if len(results) != 3 {
		t.Fatalf("results len = %d, want 3", len(results))
	}
	if got := atomic.LoadInt64(&maxInFlight); got > 1 {
		t.Fatalf("max in-flight requests = %d, want <= 1", got)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(starts) != 3 {
		t.Fatalf("request starts = %d, want 3", len(starts))
	}
	for i := 1; i < len(starts); i++ {
		if delta := starts[i].Sub(starts[i-1]); delta < 20*time.Millisecond {
			t.Fatalf("request %d started after %s, want at least 20ms", i, delta)
		}
	}
}

func TestFetchAllProfilesCancellationDuringSpacing(t *testing.T) {
	var requests int64
	var cancel context.CancelFunc

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt64(&requests, 1) == 1 {
			cancel()
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"five_hour":{"utilization":10,"resets_at":"2030-01-01T00:00:00Z"}}`))
	}))
	defer server.Close()

	ctx, cancelFn := context.WithCancel(context.Background())
	defer cancelFn()
	cancel = cancelFn
	fetcher := NewMultiProfileFetcher(WithSameProviderPacing(250*time.Millisecond, 0))
	fetcher.claudeFetcher.baseURL = server.URL

	start := time.Now()
	results := fetcher.FetchAllProfiles(ctx, "claude", map[string]string{
		"alice":   "tok-a",
		"bob":     "tok-b",
		"charlie": "tok-c",
	})
	elapsed := time.Since(start)

	if len(results) != 1 {
		t.Fatalf("results len = %d, want 1 after cancellation", len(results))
	}
	if got := atomic.LoadInt64(&requests); got != 1 {
		t.Fatalf("requests = %d, want 1", got)
	}
	if elapsed >= 150*time.Millisecond {
		t.Fatalf("FetchAllProfiles took %s after cancellation, want under 150ms", elapsed)
	}
}

func TestGetProfilesAboveThresholdExcludesUnavailableUsage(t *testing.T) {
	var requestCount int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch atomic.AddInt64(&requestCount, 1) {
		case 1:
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"five_hour":{"utilization":10,"resets_at":"2030-01-01T00:00:00Z"}}`))
		case 2:
			w.WriteHeader(http.StatusInternalServerError)
		default:
			w.WriteHeader(http.StatusTooManyRequests)
		}
	}))
	defer server.Close()

	fetcher := NewMultiProfileFetcher(WithSameProviderPacing(0, 0))
	fetcher.claudeFetcher.baseURL = server.URL

	got := fetcher.GetProfilesAboveThreshold(context.Background(), "claude", map[string]string{
		"available":    "tok-ok",
		"rate-limited": "tok-rate",
		"errored":      "tok-error",
	}, 0.8)

	if len(got) != 1 {
		t.Fatalf("available profiles = %+v, want exactly one", got)
	}
	if got[0].ProfileName != "available" {
		t.Fatalf("available profile = %q, want available", got[0].ProfileName)
	}
}
