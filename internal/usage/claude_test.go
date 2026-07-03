package usage

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestClaudeFetchRateLimitRetryAfterSeconds(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "30")
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer server.Close()

	fetcher := NewClaudeFetcher()
	fetcher.baseURL = server.URL

	info, err := fetcher.Fetch(context.Background(), "token")
	if !errors.Is(err, ErrRateLimited) {
		t.Fatalf("Fetch() error = %v, want ErrRateLimited", err)
	}
	if info == nil {
		t.Fatal("Fetch() info is nil")
	}
	if !info.RateLimited {
		t.Fatalf("RateLimited = false, want true")
	}
	if info.RetryAfter != 30*time.Second {
		t.Fatalf("RetryAfter = %s, want 30s", info.RetryAfter)
	}
}

func TestClaudeFetchRateLimitRetryAfterHTTPDate(t *testing.T) {
	retryAt := time.Now().Add(90 * time.Second).Truncate(time.Second)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", retryAt.UTC().Format(http.TimeFormat))
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer server.Close()

	fetcher := NewClaudeFetcher()
	fetcher.baseURL = server.URL

	info, err := fetcher.Fetch(context.Background(), "token")
	if !errors.Is(err, ErrRateLimited) {
		t.Fatalf("Fetch() error = %v, want ErrRateLimited", err)
	}
	if info == nil || !info.RateLimited {
		t.Fatalf("rate limit info missing: %+v", info)
	}
	want := retryAt.Sub(info.FetchedAt)
	if info.RetryAfter <= 0 || info.RetryAfter < want-2*time.Second || info.RetryAfter > want+2*time.Second {
		t.Fatalf("RetryAfter = %s, want around %s", info.RetryAfter, want)
	}
}
