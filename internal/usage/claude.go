package usage

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// Claude API constants.
const (
	ClaudeUsageURL  = "https://api.anthropic.com/api/oauth/usage"
	ClaudeAPIBeta   = "oauth-2025-04-20"
	ClaudeUserAgent = "caam/1.0"
	claudeTimeout   = 30 * time.Second
)

// ClaudeFetcher fetches usage data from Claude's OAuth API.
type ClaudeFetcher struct {
	client  *http.Client
	baseURL string // For testing
}

// NewClaudeFetcher creates a new Claude usage fetcher.
func NewClaudeFetcher() *ClaudeFetcher {
	return &ClaudeFetcher{
		client: &http.Client{Timeout: claudeTimeout},
	}
}

type claudeWindow struct {
	Utilization float64 `json:"utilization"` // 0-100 percentage (normalized to 0-1 in parsing)
	ResetsAt    string  `json:"resets_at"`   // ISO8601 timestamp
}

// Fetch retrieves usage data from Claude's API.
func (f *ClaudeFetcher) Fetch(ctx context.Context, accessToken string) (*UsageInfo, error) {
	if accessToken == "" {
		return nil, fmt.Errorf("access token is empty")
	}

	url := ClaudeUsageURL
	if f.baseURL != "" {
		url = f.baseURL
	}

	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("anthropic-beta", ClaudeAPIBeta)
	req.Header.Set("User-Agent", ClaudeUserAgent)
	req.Header.Set("Accept", "application/json")

	resp, err := f.client.Do(req)
	if err != nil {
		return &UsageInfo{
			Provider:  "claude",
			FetchedAt: time.Now(),
			Error:     fmt.Sprintf("request failed: %v", err),
		}, err
	}
	defer resp.Body.Close()

	info := &UsageInfo{
		Provider:  "claude",
		FetchedAt: time.Now(),
	}

	switch resp.StatusCode {
	case http.StatusOK:
		// Success - parse response
	case http.StatusTooManyRequests:
		info.RateLimited = true
		info.RetryAfter = parseRetryAfter(resp.Header.Get("Retry-After"), info.FetchedAt)
		info.Error = ErrRateLimited.Error()
		return info, fmt.Errorf("%w: status %d", ErrRateLimited, resp.StatusCode)
	case http.StatusUnauthorized, http.StatusForbidden:
		info.Error = "unauthorized: token expired or invalid"
		return info, fmt.Errorf("unauthorized: status %d", resp.StatusCode)
	default:
		info.Error = fmt.Sprintf("API error: status %d", resp.StatusCode)
		return info, fmt.Errorf("API error: status %d", resp.StatusCode)
	}

	var raw map[string]json.RawMessage
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		info.Error = fmt.Sprintf("decode error: %v", err)
		return info, fmt.Errorf("decode response: %w", err)
	}

	// Convert to UsageInfo
	// Note: Claude API returns utilization as 0-100 percentage, not 0-1 fraction
	if window, ok := claudeUsageWindow(raw["five_hour"], 5*time.Hour); ok {
		info.PrimaryWindow = window
	}

	if window, ok := claudeUsageWindow(raw["seven_day"], 7*24*time.Hour); ok {
		info.SecondaryWindow = window
	}

	for key, value := range raw {
		if key == "five_hour" || key == "seven_day" {
			continue
		}

		window, ok := claudeUsageWindow(value, claudeModelWindowDuration(key))
		if !ok {
			continue
		}
		if info.ModelWindows == nil {
			info.ModelWindows = make(map[string]*UsageWindow)
		}
		info.ModelWindows[key] = window
		if key == "seven_day_opus" {
			info.TertiaryWindow = window
		}
	}

	return info, nil
}

func claudeUsageWindow(raw json.RawMessage, duration time.Duration) (*UsageWindow, bool) {
	if len(raw) == 0 {
		return nil, false
	}

	var parsed struct {
		Utilization *float64 `json:"utilization"`
		ResetsAt    string   `json:"resets_at"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil || parsed.Utilization == nil {
		return nil, false
	}

	util := *parsed.Utilization
	if util > 1 {
		util = util / 100.0
	}

	return &UsageWindow{
		Utilization:    util,
		UsedPercent:    int(util * 100),
		ResetsAt:       parseISO8601(parsed.ResetsAt),
		WindowDuration: duration,
	}, true
}

func claudeModelWindowDuration(key string) time.Duration {
	key = strings.ToLower(key)
	switch {
	case strings.HasPrefix(key, "seven_day_"):
		return 7 * 24 * time.Hour
	case strings.HasPrefix(key, "five_hour_"):
		return 5 * time.Hour
	default:
		return 0
	}
}

// parseISO8601 parses an ISO8601 timestamp string.
func parseISO8601(s string) time.Time {
	if s == "" {
		return time.Time{}
	}

	// Try various ISO8601 formats
	formats := []string{
		time.RFC3339,
		time.RFC3339Nano,
		"2006-01-02T15:04:05Z",
		"2006-01-02T15:04:05-07:00",
	}

	for _, format := range formats {
		if t, err := time.Parse(format, s); err == nil {
			return t
		}
	}

	return time.Time{}
}
