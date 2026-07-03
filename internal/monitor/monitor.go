package monitor

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Dicklesworthstone/coding_agent_account_manager/internal/authfile"
	"github.com/Dicklesworthstone/coding_agent_account_manager/internal/authpool"
	caamdb "github.com/Dicklesworthstone/coding_agent_account_manager/internal/db"
	"github.com/Dicklesworthstone/coding_agent_account_manager/internal/health"
	"github.com/Dicklesworthstone/coding_agent_account_manager/internal/usage"
)

const monitorRefreshSkew = 2 * time.Minute

// ProfileFetcher fetches usage data for multiple profiles.
type ProfileFetcher interface {
	FetchAllProfiles(ctx context.Context, provider string, profiles map[string]string) []usage.ProfileUsage
}

type accessTokenInfo struct {
	token          string
	expiresAt      time.Time
	cachedInactive bool
}

// Monitor manages real-time usage monitoring across profiles.
type Monitor struct {
	interval  time.Duration
	providers []string
	fetcher   ProfileFetcher
	vault     *authfile.Vault
	health    *health.Storage
	db        *caamdb.DB
	pool      *authpool.AuthPool

	mu    sync.RWMutex
	state *MonitorState
}

// MonitorOption configures a Monitor.
type MonitorOption func(*Monitor)

func WithInterval(d time.Duration) MonitorOption {
	return func(m *Monitor) {
		if d > 0 {
			m.interval = d
		}
	}
}

func WithProviders(providers []string) MonitorOption {
	return func(m *Monitor) {
		if len(providers) > 0 {
			m.providers = append([]string(nil), providers...)
		}
	}
}

func WithFetcher(fetcher ProfileFetcher) MonitorOption {
	return func(m *Monitor) {
		m.fetcher = fetcher
	}
}

func WithVault(v *authfile.Vault) MonitorOption {
	return func(m *Monitor) {
		m.vault = v
	}
}

func WithHealthStore(store *health.Storage) MonitorOption {
	return func(m *Monitor) {
		m.health = store
	}
}

func WithDB(db *caamdb.DB) MonitorOption {
	return func(m *Monitor) {
		m.db = db
	}
}

func WithAuthPool(pool *authpool.AuthPool) MonitorOption {
	return func(m *Monitor) {
		m.pool = pool
	}
}

// NewMonitor creates a new monitor with default settings.
func NewMonitor(opts ...MonitorOption) *Monitor {
	m := &Monitor{
		interval:  30 * time.Second,
		providers: []string{"claude", "codex", "gemini", "opencode", "cursor"},
		fetcher:   usage.NewMultiProfileFetcher(),
		vault:     authfile.NewVault(authfile.DefaultVaultPath()),
		health:    health.NewStorage(""),
		state: &MonitorState{
			Profiles: make(map[string]*ProfileState),
		},
	}

	for _, opt := range opts {
		opt(m)
	}

	if m.state == nil {
		m.state = &MonitorState{Profiles: make(map[string]*ProfileState)}
	}
	if m.interval <= 0 {
		m.interval = 30 * time.Second
	}

	return m
}

// Start runs the monitor refresh loop until ctx is cancelled.
func (m *Monitor) Start(ctx context.Context) error {
	ticker := time.NewTicker(m.interval)
	defer ticker.Stop()

	for {
		_ = m.Refresh(ctx)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

// Refresh performs a one-shot update of monitor state.
func (m *Monitor) Refresh(ctx context.Context) error {
	previousState := m.GetState()
	state := &MonitorState{
		Profiles:  make(map[string]*ProfileState),
		UpdatedAt: time.Now(),
	}

	var errs []error
	if m.vault == nil {
		errs = append(errs, fmt.Errorf("vault is nil"))
		m.setState(state, errs)
		return errors.Join(errs...)
	}

	profilesByProvider, err := m.vault.ListAll()
	if err != nil {
		errs = append(errs, err)
		m.setState(state, errs)
		return errors.Join(errs...)
	}

	cooldowns, cdErrs := m.loadCooldowns()
	if len(cdErrs) > 0 {
		errs = append(errs, cdErrs...)
	}

	type providerResult struct {
		provider string
		results  []usage.ProfileUsage
	}

	var wg sync.WaitGroup
	resultsCh := make(chan providerResult, len(m.providers))
	activeClaudeProfile := m.activeClaudeProfile()

	for _, provider := range m.providers {
		profiles := profilesByProvider[provider]
		if len(profiles) == 0 {
			continue
		}
		if m.fetcher == nil {
			continue
		}

		tokens := make(map[string]string)
		for _, name := range profiles {
			if authfile.IsSystemProfile(name) {
				continue
			}
			tokenInfo, err := m.readAccessToken(provider, name, activeClaudeProfile, state.UpdatedAt)
			if err != nil {
				state.Profiles[profileKey(provider, name)] = m.buildProfileState(provider, name, &usage.UsageInfo{
					Provider:    provider,
					ProfileName: name,
					Error:       err.Error(),
					FetchedAt:   time.Now(),
				}, cooldowns)
				continue
			}
			if tokenInfo.cachedInactive {
				state.Profiles[profileKey(provider, name)] = m.buildProfileState(provider, name, &usage.UsageInfo{
					Provider:       provider,
					ProfileName:    name,
					CachedInactive: true,
					FetchedAt:      time.Now(),
				}, cooldowns)
				continue
			}
			if shouldRefreshAccessToken(tokenInfo.expiresAt, state.UpdatedAt) {
				state.Profiles[profileKey(provider, name)] = m.buildProfileState(provider, name, &usage.UsageInfo{
					Provider:    provider,
					ProfileName: name,
					Error:       "access token expired or near expiry",
					FetchedAt:   time.Now(),
				}, cooldowns)
				continue
			}
			if tokenInfo.token == "" {
				state.Profiles[profileKey(provider, name)] = m.buildProfileState(provider, name, &usage.UsageInfo{
					Provider:    provider,
					ProfileName: name,
					Error:       "missing access token",
					FetchedAt:   time.Now(),
				}, cooldowns)
				continue
			}
			tokens[name] = tokenInfo.token
		}

		if len(tokens) == 0 || m.fetcher == nil {
			continue
		}

		wg.Add(1)
		go func(provider string, tokens map[string]string) {
			defer wg.Done()
			results := m.fetcher.FetchAllProfiles(ctx, provider, tokens)
			resultsCh <- providerResult{provider: provider, results: results}
		}(provider, tokens)
	}

	wg.Wait()
	close(resultsCh)

	for res := range resultsCh {
		for _, item := range res.results {
			info := item.Usage
			if info == nil {
				info = &usage.UsageInfo{
					Provider:    res.provider,
					ProfileName: item.ProfileName,
					Error:       "usage fetch returned nil",
					FetchedAt:   time.Now(),
				}
			}
			if usage.IsRateLimitedUsage(info) {
				info = carryForwardRateLimitedUsage(previousState, res.provider, item.ProfileName, info)
			}
			if info.ProfileName == "" {
				info.ProfileName = item.ProfileName
			}
			state.Profiles[profileKey(res.provider, item.ProfileName)] = m.buildProfileState(res.provider, item.ProfileName, info, cooldowns)
		}
	}

	m.setState(state, errs)
	if len(errs) > 0 {
		return errors.Join(errs...)
	}
	return nil
}

// GetState returns a snapshot of the current monitor state.
func (m *Monitor) GetState() *MonitorState {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.state.Clone()
}

// GetProfile returns a snapshot of a specific profile state.
func (m *Monitor) GetProfile(provider, name string) *ProfileState {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.state == nil || m.state.Profiles == nil {
		return nil
	}
	if profile, ok := m.state.Profiles[profileKey(provider, name)]; ok && profile != nil {
		cp := *profile
		return &cp
	}
	return nil
}

func (m *Monitor) setState(state *MonitorState, errs []error) {
	if state == nil {
		return
	}
	for _, err := range errs {
		if err == nil {
			continue
		}
		state.Errors = append(state.Errors, err.Error())
	}

	m.mu.Lock()
	m.state = state
	m.mu.Unlock()
}

func (m *Monitor) loadCooldowns() (map[string]time.Time, []error) {
	out := make(map[string]time.Time)
	if m.db == nil {
		return out, nil
	}

	events, err := m.db.ListActiveCooldowns(time.Now())
	if err != nil {
		return out, []error{err}
	}
	for _, ev := range events {
		out[profileKey(ev.Provider, ev.ProfileName)] = ev.CooldownUntil
	}
	return out, nil
}

func (m *Monitor) activeClaudeProfile() string {
	if m == nil || m.vault == nil {
		return ""
	}
	active, err := m.vault.ActiveProfile(authfile.ClaudeAuthFiles())
	if err != nil || authfile.IsSystemProfile(active) {
		return ""
	}
	return active
}

func (m *Monitor) readAccessToken(provider, name, activeClaudeProfile string, now time.Time) (accessTokenInfo, error) {
	if m.vault == nil {
		return accessTokenInfo{}, fmt.Errorf("vault is nil")
	}

	switch provider {
	case "claude":
		if name == activeClaudeProfile {
			if tokenInfo, err := readLiveClaudeAccessToken(); err == nil && tokenInfo.token != "" {
				return tokenInfo, nil
			}
		}

		profilePath := m.vault.ProfilePath(provider, name)
		creds := filepath.Join(profilePath, ".credentials.json")
		token, _, err := usage.ReadClaudeCredentials(creds)
		if err != nil {
			oldPath := filepath.Join(profilePath, ".claude.json")
			creds = oldPath
			token, _, err = usage.ReadClaudeCredentials(oldPath)
			if err != nil {
				authPath := filepath.Join(profilePath, "auth.json")
				creds = authPath
				token, _, err = usage.ReadClaudeCredentials(authPath)
			}
		}
		if err != nil {
			return accessTokenInfo{}, err
		}
		expiresAt := readJSONExpiry(creds, token)
		if name != activeClaudeProfile && shouldRefreshAccessToken(expiresAt, now) {
			return accessTokenInfo{cachedInactive: true, expiresAt: expiresAt}, nil
		}
		return accessTokenInfo{token: token, expiresAt: expiresAt}, nil
	case "codex":
		authPath := filepath.Join(m.vault.ProfilePath(provider, name), "auth.json")
		token, _, err := usage.ReadCodexCredentials(authPath)
		if err != nil {
			return accessTokenInfo{}, err
		}
		expiry := readJSONExpiry(authPath, token)
		if expiry.IsZero() {
			expiry = jwtExpiry(token)
		}
		return accessTokenInfo{token: token, expiresAt: expiry}, nil
	case "opencode", "cursor":
		return accessTokenInfo{}, fmt.Errorf("usage fetch not yet supported for provider %s", provider)
	default:
		return accessTokenInfo{}, fmt.Errorf("usage fetch unsupported for provider %s", provider)
	}
}

func readLiveClaudeAccessToken() (accessTokenInfo, error) {
	var lastErr error
	for _, spec := range authfile.ClaudeAuthFiles().Files {
		token, _, err := usage.ReadClaudeCredentials(spec.Path)
		if err != nil {
			lastErr = err
			continue
		}
		return accessTokenInfo{
			token:     token,
			expiresAt: readJSONExpiry(spec.Path, token),
		}, nil
	}
	if lastErr != nil {
		return accessTokenInfo{}, lastErr
	}
	return accessTokenInfo{}, fmt.Errorf("no live Claude credentials found")
}

func shouldRefreshAccessToken(expiresAt time.Time, now time.Time) bool {
	if expiresAt.IsZero() {
		return false
	}
	return !expiresAt.After(now.Add(monitorRefreshSkew))
}

func carryForwardRateLimitedUsage(previousState *MonitorState, provider, name string, rateLimited *usage.UsageInfo) *usage.UsageInfo {
	if previousState == nil {
		return rateLimited
	}
	previousProfile := previousState.Profiles[profileKey(provider, name)]
	if previousProfile == nil || previousProfile.Usage == nil {
		return rateLimited
	}
	previousUsage := previousProfile.Usage
	if previousUsage.Error != "" || previousUsage.MostConstrainedWindow() == nil {
		return rateLimited
	}

	carried := *previousUsage
	carried.Provider = provider
	carried.ProfileName = name
	carried.Error = ""
	carried.RateLimited = true
	if rateLimited != nil {
		carried.RetryAfter = rateLimited.RetryAfter
	}
	return &carried
}

func readJSONExpiry(path string, token string) time.Time {
	data, err := os.ReadFile(path)
	if err != nil {
		return time.Time{}
	}

	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return time.Time{}
	}

	for _, key := range []string{"expiresAt", "expires_at", "expiry", "expires"} {
		if expiresAt := parseExpiry(raw[key]); !expiresAt.IsZero() {
			return expiresAt
		}
	}

	if oauthRaw := raw["claudeAiOauth"]; len(oauthRaw) > 0 {
		if expiresAt := expiryFromObject(oauthRaw); !expiresAt.IsZero() {
			return expiresAt
		}
	}
	if oauthRaw := raw["oauthToken"]; len(oauthRaw) > 0 {
		if expiresAt := expiryFromObject(oauthRaw); !expiresAt.IsZero() {
			return expiresAt
		}
	}
	if tokensRaw := raw["tokens"]; len(tokensRaw) > 0 {
		if expiresAt := expiryFromObject(tokensRaw); !expiresAt.IsZero() {
			return expiresAt
		}
	}

	return jwtExpiry(token)
}

func expiryFromObject(raw json.RawMessage) time.Time {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return time.Time{}
	}
	for _, key := range []string{"expiresAt", "expires_at", "expiry", "expires"} {
		if expiresAt := parseExpiry(fields[key]); !expiresAt.IsZero() {
			return expiresAt
		}
	}
	return time.Time{}
}

func parseExpiry(raw json.RawMessage) time.Time {
	if len(raw) == 0 || string(raw) == "null" {
		return time.Time{}
	}

	var n json.Number
	if err := json.Unmarshal(raw, &n); err == nil {
		return unixExpiry(n.String())
	}

	var s string
	if err := json.Unmarshal(raw, &s); err != nil || strings.TrimSpace(s) == "" {
		return time.Time{}
	}
	s = strings.TrimSpace(s)
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t
	}
	return unixExpiry(s)
}

func unixExpiry(s string) time.Time {
	value, err := strconv.ParseInt(s, 10, 64)
	if err != nil || value <= 0 {
		return time.Time{}
	}
	if value > 1_000_000_000_000 {
		return time.UnixMilli(value)
	}
	return time.Unix(value, 0)
}

func jwtExpiry(token string) time.Time {
	parts := strings.Split(token, ".")
	if len(parts) < 2 {
		return time.Time{}
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return time.Time{}
	}
	var claims struct {
		ExpiresAt int64 `json:"exp"`
	}
	if err := json.Unmarshal(payload, &claims); err != nil || claims.ExpiresAt <= 0 {
		return time.Time{}
	}
	return time.Unix(claims.ExpiresAt, 0)
}

func (m *Monitor) buildProfileState(provider, name string, info *usage.UsageInfo, cooldowns map[string]time.Time) *ProfileState {
	state := &ProfileState{
		Provider:    provider,
		ProfileName: name,
		Usage:       info,
		Health:      health.StatusUnknown,
		PoolStatus:  authpool.PoolStatusUnknown,
	}

	if m.health != nil {
		if h, err := m.health.GetProfile(provider, name); err == nil && h != nil {
			state.Health = health.CalculateStatus(h)
		}
	}

	if m.pool != nil {
		state.PoolStatus = m.pool.GetStatus(provider, name)
	}

	if until, ok := cooldowns[profileKey(provider, name)]; ok {
		state.InCooldown = true
		state.CooldownUntil = &until
	}

	state.Alert = evaluateAlert(info, time.Now())
	return state
}
