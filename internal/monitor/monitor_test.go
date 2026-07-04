package monitor

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/Dicklesworthstone/coding_agent_account_manager/internal/authfile"
	"github.com/Dicklesworthstone/coding_agent_account_manager/internal/usage"
)

type fakeFetcher struct {
	usages map[string]*usage.UsageInfo
}

func (f *fakeFetcher) FetchAllProfiles(ctx context.Context, provider string, profiles map[string]string) []usage.ProfileUsage {
	results := make([]usage.ProfileUsage, 0, len(profiles))
	for name := range profiles {
		key := provider + "/" + name
		info := f.usages[key]
		if info == nil {
			info = &usage.UsageInfo{
				Provider:    provider,
				ProfileName: name,
				Error:       "missing usage",
				FetchedAt:   time.Now(),
			}
		}
		results = append(results, usage.ProfileUsage{
			Provider:    provider,
			ProfileName: name,
			Usage:       info,
		})
	}
	return results
}

type fetchCall struct {
	provider string
	profiles map[string]string
}

type scriptedFetcher struct {
	mu        sync.Mutex
	calls     []fetchCall
	responses []map[string]*usage.UsageInfo
}

func (f *scriptedFetcher) FetchAllProfiles(ctx context.Context, provider string, profiles map[string]string) []usage.ProfileUsage {
	f.mu.Lock()
	callIndex := len(f.calls)
	copied := make(map[string]string, len(profiles))
	for name, token := range profiles {
		copied[name] = token
	}
	f.calls = append(f.calls, fetchCall{provider: provider, profiles: copied})
	var response map[string]*usage.UsageInfo
	if callIndex < len(f.responses) {
		response = f.responses[callIndex]
	}
	f.mu.Unlock()

	results := make([]usage.ProfileUsage, 0, len(profiles))
	for name := range profiles {
		key := profileKey(provider, name)
		info := response[key]
		if info == nil {
			info = &usage.UsageInfo{
				Provider:      provider,
				ProfileName:   name,
				PrimaryWindow: &usage.UsageWindow{UsedPercent: 10},
				FetchedAt:     time.Now(),
			}
		}
		results = append(results, usage.ProfileUsage{
			Provider:    provider,
			ProfileName: name,
			Usage:       info,
		})
	}
	return results
}

func (f *scriptedFetcher) callSnapshot() []fetchCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]fetchCall, len(f.calls))
	for i, call := range f.calls {
		out[i] = fetchCall{
			provider: call.provider,
			profiles: make(map[string]string, len(call.profiles)),
		}
		for name, token := range call.profiles {
			out[i].profiles[name] = token
		}
	}
	return out
}

func TestMonitorRefreshBuildsState(t *testing.T) {
	tmpDir := t.TempDir()
	vault := authfile.NewVault(tmpDir)

	writeProfileFile(t, vault, "claude", "alice", ".credentials.json", `{"claudeAiOauth":{"accessToken":"tok-claude"}}`)
	writeProfileFile(t, vault, "codex", "bob", "auth.json", `{"tokens":{"access_token":"tok-codex"}}`)

	fetcher := &fakeFetcher{
		usages: map[string]*usage.UsageInfo{
			"claude/alice": {
				Provider:    "claude",
				ProfileName: "alice",
				PrimaryWindow: &usage.UsageWindow{
					UsedPercent: 80,
				},
			},
			"codex/bob": {
				Provider:    "codex",
				ProfileName: "bob",
				PrimaryWindow: &usage.UsageWindow{
					UsedPercent: 20,
				},
			},
		},
	}

	mon := NewMonitor(
		WithVault(vault),
		WithFetcher(fetcher),
		WithProviders([]string{"claude", "codex"}),
		WithHealthStore(nil),
	)

	if err := mon.Refresh(context.Background()); err != nil {
		t.Fatalf("Refresh() error: %v", err)
	}

	state := mon.GetState()
	if state == nil {
		t.Fatal("GetState() returned nil")
	}
	if len(state.Profiles) != 2 {
		t.Fatalf("Profiles count = %d, want 2", len(state.Profiles))
	}

	claude := state.Profiles["claude/alice"]
	if claude == nil {
		t.Fatal("missing claude/alice profile")
	}
	if claude.Alert == nil || claude.Alert.Type != AlertWarning {
		t.Fatalf("claude alert = %v, want warning", claude.Alert)
	}

	codex := state.Profiles["codex/bob"]
	if codex == nil {
		t.Fatal("missing codex/bob profile")
	}
	if codex.Alert != nil {
		t.Fatalf("codex alert = %v, want nil", codex.Alert)
	}
}

func TestMonitorRefreshUnsupportedProvider(t *testing.T) {
	tmpDir := t.TempDir()
	vault := authfile.NewVault(tmpDir)
	writeProfileDir(t, vault, "gemini", "carol")

	mon := NewMonitor(
		WithVault(vault),
		WithFetcher(&fakeFetcher{}),
		WithProviders([]string{"gemini"}),
		WithHealthStore(nil),
	)

	if err := mon.Refresh(context.Background()); err != nil {
		t.Fatalf("Refresh() error: %v", err)
	}

	state := mon.GetState()
	profile := state.Profiles["gemini/carol"]
	if profile == nil || profile.Usage == nil || profile.Usage.Error == "" {
		t.Fatalf("expected usage error for gemini profile, got %+v", profile)
	}
}

func TestMonitorRefreshClaudeFallbackAuth(t *testing.T) {
	tmpDir := t.TempDir()
	vault := authfile.NewVault(tmpDir)

	writeProfileFile(t, vault, "claude", "legacy", ".claude.json", `{"oauthToken":"tok-legacy"}`)

	fetcher := &fakeFetcher{
		usages: map[string]*usage.UsageInfo{
			"claude/legacy": {
				Provider:    "claude",
				ProfileName: "legacy",
				PrimaryWindow: &usage.UsageWindow{
					UsedPercent: 10,
				},
			},
		},
	}

	mon := NewMonitor(
		WithVault(vault),
		WithFetcher(fetcher),
		WithProviders([]string{"claude"}),
		WithHealthStore(nil),
	)

	if err := mon.Refresh(context.Background()); err != nil {
		t.Fatalf("Refresh() error: %v", err)
	}

	state := mon.GetState()
	profile := state.Profiles["claude/legacy"]
	if profile == nil || profile.Usage == nil || profile.Usage.Error != "" {
		t.Fatalf("expected legacy claude profile usage, got %+v", profile)
	}
}

func TestMonitorRefreshInactiveExpiredClaudeIsCachedInactive(t *testing.T) {
	tmpDir := t.TempDir()
	vault := authfile.NewVault(tmpDir)
	expired := time.Now().Add(-time.Minute)
	writeClaudeCredential(t, vault, "alice", "old-token", expired)

	fetcher := &scriptedFetcher{}
	mon := NewMonitor(
		WithVault(vault),
		WithFetcher(fetcher),
		WithProviders([]string{"claude"}),
		WithHealthStore(nil),
	)

	if err := mon.Refresh(context.Background()); err != nil {
		t.Fatalf("Refresh() error: %v", err)
	}
	calls := fetcher.callSnapshot()
	if len(calls) != 0 {
		t.Fatalf("fetch calls = %d, want 0 for inactive expired Claude cache", len(calls))
	}

	state := mon.GetState()
	profile := state.Profiles["claude/alice"]
	if profile == nil || profile.Usage == nil {
		t.Fatalf("missing profile state: %+v", profile)
	}
	if !profile.Usage.CachedInactive || profile.Usage.Error != "" {
		t.Fatalf("usage = %+v, want cached inactive without error", profile.Usage)
	}
	if profile.Alert != nil {
		t.Fatalf("cached inactive profile alert = %+v, want nil", profile.Alert)
	}
	if got := usageUnavailable(profile.Usage); got != "cached inactive" {
		t.Fatalf("usageUnavailable = %q, want cached inactive", got)
	}
}

func TestMonitorRefreshActiveClaudeUsesLiveCredential(t *testing.T) {
	tmpDir := t.TempDir()
	vault := authfile.NewVault(tmpDir)
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	vaultToken := "old-access-token"
	liveToken := "rotated-live-token"
	writeClaudeCredential(t, vault, "alice", vaultToken, time.Now().Add(-time.Minute))
	writeLiveClaudeCredential(t, home, liveToken, time.Now().Add(time.Hour))

	fetcher := &scriptedFetcher{
		responses: []map[string]*usage.UsageInfo{
			{
				"claude/alice": {
					Provider:    "claude",
					ProfileName: "alice",
					PrimaryWindow: &usage.UsageWindow{
						UsedPercent: 22,
					},
					FetchedAt: time.Now(),
				},
			},
		},
	}

	mon := NewMonitor(
		WithVault(vault),
		WithFetcher(fetcher),
		WithProviders([]string{"claude"}),
		WithHealthStore(nil),
	)

	if err := mon.Refresh(context.Background()); err != nil {
		t.Fatalf("Refresh() error: %v", err)
	}
	calls := fetcher.callSnapshot()
	if len(calls) != 1 {
		t.Fatalf("fetch calls = %d, want 1", len(calls))
	}
	if got := calls[0].profiles["alice"]; got != liveToken {
		t.Fatalf("fetch token = %q, want live token", got)
	}
	state := mon.GetState()
	profile := state.Profiles["claude/alice"]
	if profile == nil || profile.Usage == nil || profile.Usage.Error != "" || profile.Usage.CachedInactive {
		t.Fatalf("expected active Claude live usage, got %+v", profile)
	}
	if got := usagePercent(profile.Usage); got != 22 {
		t.Fatalf("usage percent = %.0f, want 22", got)
	}
}

func TestMonitorRefreshExpiredCodexJWTDoesNotFetch(t *testing.T) {
	tmpDir := t.TempDir()
	vault := authfile.NewVault(tmpDir)
	expiredToken := makeJWT(t, time.Now().Add(-time.Minute))
	writeProfileFile(t, vault, "codex", "bob", "auth.json", fmt.Sprintf(`{"tokens":{"access_token":%q}}`, expiredToken))

	fetcher := &scriptedFetcher{}
	mon := NewMonitor(
		WithVault(vault),
		WithFetcher(fetcher),
		WithProviders([]string{"codex"}),
		WithHealthStore(nil),
	)

	if err := mon.Refresh(context.Background()); err != nil {
		t.Fatalf("Refresh() error: %v", err)
	}
	if calls := fetcher.callSnapshot(); len(calls) != 0 {
		t.Fatalf("fetch calls = %d, want 0 for expired Codex JWT", len(calls))
	}
	state := mon.GetState()
	profile := state.Profiles["codex/bob"]
	if profile == nil || profile.Usage == nil || profile.Usage.Error != "access token expired or near expiry" {
		t.Fatalf("expected expired-token usage error, got %+v", profile)
	}
}

func TestExpiryParsingAndSkew(t *testing.T) {
	now := time.Now().Round(time.Second)
	raw, err := json.Marshal(now.Add(time.Hour).Format(time.RFC3339))
	if err != nil {
		t.Fatal(err)
	}
	if got := parseExpiry(raw); !got.Equal(now.Add(time.Hour)) {
		t.Fatalf("parseExpiry RFC3339 = %s, want %s", got, now.Add(time.Hour))
	}

	raw, err = json.Marshal(now.Add(time.Hour).UnixMilli())
	if err != nil {
		t.Fatal(err)
	}
	if got := parseExpiry(raw); !got.Equal(now.Add(time.Hour)) {
		t.Fatalf("parseExpiry unix millis = %s, want %s", got, now.Add(time.Hour))
	}

	if !shouldRefreshAccessToken(now.Add(time.Minute), now) {
		t.Fatal("near-expiry token should be treated as expired within skew")
	}
	if shouldRefreshAccessToken(now.Add(3*time.Minute), now) {
		t.Fatal("token outside skew should not be treated as expired")
	}
}

func TestMonitorRefreshRetainsPriorGoodUsageOnRateLimit(t *testing.T) {
	tmpDir := t.TempDir()
	vault := authfile.NewVault(tmpDir)
	writeProfileFile(t, vault, "claude", "alice", ".credentials.json", `{"claudeAiOauth":{"accessToken":"tok-claude"}}`)

	firstFetchedAt := time.Now().Add(-5 * time.Minute).Round(0)
	fetcher := &scriptedFetcher{
		responses: []map[string]*usage.UsageInfo{
			{
				"claude/alice": {
					Provider:    "claude",
					ProfileName: "alice",
					PrimaryWindow: &usage.UsageWindow{
						UsedPercent: 33,
					},
					FetchedAt: firstFetchedAt,
				},
			},
			{
				"claude/alice": {
					Provider:    "claude",
					ProfileName: "alice",
					Error:       usage.ErrRateLimited.Error(),
					RateLimited: true,
					RetryAfter:  30 * time.Second,
					FetchedAt:   time.Now(),
				},
			},
		},
	}

	mon := NewMonitor(
		WithVault(vault),
		WithFetcher(fetcher),
		WithProviders([]string{"claude"}),
		WithHealthStore(nil),
	)

	if err := mon.Refresh(context.Background()); err != nil {
		t.Fatalf("first Refresh() error: %v", err)
	}
	if err := mon.Refresh(context.Background()); err != nil {
		t.Fatalf("second Refresh() error: %v", err)
	}

	state := mon.GetState()
	profile := state.Profiles["claude/alice"]
	if profile == nil || profile.Usage == nil {
		t.Fatalf("missing retained profile usage: %+v", profile)
	}
	if profile.Usage.Error != "" {
		t.Fatalf("retained usage Error = %q, want empty success-shaped usage", profile.Usage.Error)
	}
	if !profile.Usage.RateLimited {
		t.Fatal("retained usage RateLimited = false, want true")
	}
	if profile.Usage.RetryAfter != 30*time.Second {
		t.Fatalf("retained usage RetryAfter = %s, want 30s", profile.Usage.RetryAfter)
	}
	if !profile.Usage.FetchedAt.Equal(firstFetchedAt) {
		t.Fatalf("retained usage FetchedAt = %s, want original %s", profile.Usage.FetchedAt, firstFetchedAt)
	}
	if got := usagePercent(profile.Usage); got != 33 {
		t.Fatalf("retained usage percent = %.0f, want 33", got)
	}
}

func writeProfileFile(t *testing.T, vault *authfile.Vault, provider, profile, name, contents string) {
	t.Helper()
	dir := vault.ProfilePath(provider, profile)
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(contents), 0600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
}

func writeProfileDir(t *testing.T, vault *authfile.Vault, provider, profile string) {
	t.Helper()
	dir := vault.ProfilePath(provider, profile)
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
}

func writeClaudeCredential(t *testing.T, vault *authfile.Vault, profile string, token string, expiresAt time.Time) {
	t.Helper()
	writeProfileFile(t, vault, "claude", profile, ".credentials.json",
		fmt.Sprintf(`{"claudeAiOauth":{"accessToken":%q,"refreshToken":%q,"expiresAt":%d}}`, token, "refresh-"+profile, expiresAt.UnixMilli()))
}

func writeLiveClaudeCredential(t *testing.T, home string, token string, expiresAt time.Time) {
	t.Helper()
	dir := filepath.Join(home, ".claude")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatalf("MkdirAll live Claude dir: %v", err)
	}
	path := filepath.Join(dir, ".credentials.json")
	body := fmt.Sprintf(`{"claudeAiOauth":{"accessToken":%q,"refreshToken":"refresh-alice","expiresAt":%d}}`, token, expiresAt.UnixMilli())
	if err := os.WriteFile(path, []byte(body), 0600); err != nil {
		t.Fatalf("WriteFile live Claude credential: %v", err)
	}
}

func makeJWT(t *testing.T, expiresAt time.Time) string {
	t.Helper()
	encode := func(v any) string {
		data, err := json.Marshal(v)
		if err != nil {
			t.Fatalf("Marshal JWT part: %v", err)
		}
		return base64.RawURLEncoding.EncodeToString(data)
	}
	return encode(map[string]string{"alg": "none"}) + "." +
		encode(map[string]int64{"exp": expiresAt.Unix()}) + ".sig"
}
