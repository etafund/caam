package api

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/Dicklesworthstone/coding_agent_account_manager/internal/authfile"
	caamdb "github.com/Dicklesworthstone/coding_agent_account_manager/internal/db"
	"github.com/Dicklesworthstone/coding_agent_account_manager/internal/health"
	"github.com/Dicklesworthstone/coding_agent_account_manager/internal/identity"
	"github.com/Dicklesworthstone/coding_agent_account_manager/internal/version"
)

const coordinatorProbeMaxBytes = 1 << 20

type coordinatorHTTPClient interface {
	Do(*http.Request) (*http.Response, error)
}

func sameProfileName(a, b string) bool {
	return strings.Compare(a, b) == 0
}

// Handlers provides the business logic for API endpoints.
type Handlers struct {
	vault                *authfile.Vault
	healthStore          *health.Storage
	db                   *caamdb.DB
	coordinatorEndpoints []CoordinatorEndpoint
	coordinatorClient    coordinatorHTTPClient
}

// NewHandlers creates a new Handlers instance.
func NewHandlers(vault *authfile.Vault, healthStore *health.Storage, db *caamdb.DB) *Handlers {
	return &Handlers{
		vault:             vault,
		healthStore:       healthStore,
		db:                db,
		coordinatorClient: &http.Client{Timeout: 2 * time.Second},
	}
}

// SetCoordinatorEndpoints configures the coordinator endpoints that the API probes.
func (h *Handlers) SetCoordinatorEndpoints(endpoints []CoordinatorEndpoint) {
	h.coordinatorEndpoints = append([]CoordinatorEndpoint(nil), endpoints...)
}

// SetCoordinatorHTTPClient sets the HTTP client used to probe coordinator endpoints.
func (h *Handlers) SetCoordinatorHTTPClient(client coordinatorHTTPClient) {
	if client == nil {
		h.coordinatorClient = &http.Client{Timeout: 2 * time.Second}
		return
	}
	h.coordinatorClient = client
}

// Event represents a server-sent event.
type Event struct {
	Type      string      `json:"type"`
	Timestamp time.Time   `json:"timestamp"`
	Data      interface{} `json:"data"`
}

// StatusResponse is the response for GET /status.
type StatusResponse struct {
	Version   string       `json:"version"`
	Timestamp string       `json:"timestamp"`
	Tools     []ToolStatus `json:"tools"`
}

// ToolStatus represents the status of a single tool.
type ToolStatus struct {
	Tool          string             `json:"tool"`
	LoggedIn      bool               `json:"logged_in"`
	ActiveProfile string             `json:"active_profile,omitempty"`
	Health        *HealthStatus      `json:"health,omitempty"`
	Identity      *identity.Identity `json:"identity,omitempty"`
}

// HealthStatus represents profile health.
type HealthStatus struct {
	Status            string `json:"status"`
	ExpiresAt         string `json:"expires_at,omitempty"`
	ErrorCount        int    `json:"error_count"`
	CooldownRemaining string `json:"cooldown_remaining,omitempty"`
}

// ProfilesResponse is the response for GET /profiles.
type ProfilesResponse struct {
	Profiles []ProfileInfo `json:"profiles"`
	Count    int           `json:"count"`
}

// ProfileInfo represents a profile.
type ProfileInfo struct {
	Tool     string             `json:"tool"`
	Name     string             `json:"name"`
	Active   bool               `json:"active"`
	System   bool               `json:"system"`
	Health   *HealthStatus      `json:"health,omitempty"`
	Identity *identity.Identity `json:"identity,omitempty"`
}

// UsageResponse is the response for GET /usage.
//
// The route reports health-derived error counters. It deliberately does not
// expose synthetic API-call totals or token usage because those are not tracked
// by this API surface.
type UsageResponse struct {
	Tool    string       `json:"tool,omitempty"`
	Window  string       `json:"window"`
	Metric  string       `json:"metric"`
	Entries []UsageEntry `json:"entries"`
}

// UsageEntry represents usage for a profile.
type UsageEntry struct {
	Tool               string `json:"tool"`
	Profile            string `json:"profile"`
	HealthErrorCount1h int    `json:"health_error_count_1h"`
	LastChecked        string `json:"last_checked,omitempty"`
}

// ActivityResponse is the response for GET /activity.
type ActivityResponse struct {
	Events []ActivityEntry `json:"events"`
	Count  int             `json:"count"`
}

// ActivityEntry represents a redacted activity-log event.
type ActivityEntry struct {
	Timestamp       string `json:"timestamp"`
	Type            string `json:"type"`
	Tool            string `json:"tool"`
	Profile         string `json:"profile"`
	Message         string `json:"message"`
	DurationSeconds int64  `json:"duration_seconds,omitempty"`
}

// CoordinatorsResponse is the response for GET /coordinators.
type CoordinatorsResponse struct {
	Coordinators []CoordinatorStatus `json:"coordinators"`
}

// CoordinatorEndpoint is a configured remote auth-coordinator endpoint.
type CoordinatorEndpoint struct {
	ID       string
	Endpoint string
	Token    string
}

// CoordinatorStatus represents coordinator health.
type CoordinatorStatus struct {
	ID           string `json:"id"`
	Endpoint     string `json:"endpoint"`
	Status       string `json:"status"`
	Backend      string `json:"backend,omitempty"`
	LastSeen     string `json:"last_seen,omitempty"`
	PaneCount    int    `json:"pane_count,omitempty"`
	PendingAuths int    `json:"pending_auths,omitempty"`
	Error        string `json:"error,omitempty"`
}

// ActivateRequest is the request for POST /actions/activate.
type ActivateRequest struct {
	Tool    string `json:"tool"`
	Profile string `json:"profile"`
	Force   bool   `json:"force,omitempty"`
}

// ActivateResponse is the response for POST /actions/activate.
type ActivateResponse struct {
	Success bool   `json:"success"`
	Tool    string `json:"tool"`
	Profile string `json:"profile"`
	Message string `json:"message,omitempty"`
}

// BackupRequest is the request for POST /actions/backup.
type BackupRequest struct {
	Tool    string `json:"tool"`
	Profile string `json:"profile"`
}

// BackupResponse is the response for POST /actions/backup.
type BackupResponse struct {
	Success bool   `json:"success"`
	Tool    string `json:"tool"`
	Profile string `json:"profile"`
	Path    string `json:"path,omitempty"`
	Message string `json:"message,omitempty"`
}

// Tools supported for auth file swapping.
var tools = map[string]func() authfile.AuthFileSet{
	"codex":  authfile.CodexAuthFiles,
	"claude": authfile.ClaudeAuthFiles,
	"gemini": authfile.GeminiAuthFiles,
}

// GetStatus returns overall caam status.
func (h *Handlers) GetStatus() (*StatusResponse, error) {
	resp := &StatusResponse{
		Version:   version.Info(),
		Timestamp: time.Now().Format(time.RFC3339),
		Tools:     []ToolStatus{},
	}

	for tool, getFileSet := range tools {
		fileSet := getFileSet()
		hasAuth := authfile.HasAuthFiles(fileSet)

		ts := ToolStatus{
			Tool:     tool,
			LoggedIn: hasAuth,
		}

		if hasAuth && h.vault != nil {
			activeProfile, err := h.vault.ActiveProfile(fileSet)
			if err == nil && activeProfile != "" {
				ts.ActiveProfile = activeProfile
				ts.Health = h.getProfileHealth(tool, activeProfile)
				ts.Identity = h.getProfileIdentity(tool, activeProfile)
			}
		}

		resp.Tools = append(resp.Tools, ts)
	}

	return resp, nil
}

// GetProfiles returns profiles, optionally filtered by tool.
func (h *Handlers) GetProfiles(tool string) (*ProfilesResponse, error) {
	resp := &ProfilesResponse{
		Profiles: []ProfileInfo{},
	}

	if tool != "" {
		getFileSet, ok := tools[tool]
		if !ok {
			return nil, fmt.Errorf("unknown tool: %s", tool)
		}
		if h.vault == nil {
			return nil, fmt.Errorf("vault not available")
		}
		profiles, err := h.vault.List(tool)
		if err != nil {
			return nil, err
		}

		fileSet := getFileSet()
		activeProfile, _ := h.vault.ActiveProfile(fileSet)

		for _, name := range profiles {
			pi := ProfileInfo{
				Tool:     tool,
				Name:     name,
				Active:   sameProfileName(name, activeProfile),
				System:   authfile.IsSystemProfile(name),
				Health:   h.getProfileHealth(tool, name),
				Identity: h.getProfileIdentity(tool, name),
			}
			resp.Profiles = append(resp.Profiles, pi)
		}
	} else {
		if h.vault == nil {
			return nil, fmt.Errorf("vault not available")
		}
		allProfiles, err := h.vault.ListAll()
		if err != nil {
			return nil, err
		}

		for tool, profiles := range allProfiles {
			getFileSet, ok := tools[tool]
			if !ok {
				continue
			}
			fileSet := getFileSet()
			activeProfile, _ := h.vault.ActiveProfile(fileSet)

			for _, name := range profiles {
				pi := ProfileInfo{
					Tool:     tool,
					Name:     name,
					Active:   sameProfileName(name, activeProfile),
					System:   authfile.IsSystemProfile(name),
					Health:   h.getProfileHealth(tool, name),
					Identity: h.getProfileIdentity(tool, name),
				}
				resp.Profiles = append(resp.Profiles, pi)
			}
		}
	}

	resp.Count = len(resp.Profiles)
	return resp, nil
}

// GetProfile returns a single profile.
func (h *Handlers) GetProfile(tool, name string) (*ProfileInfo, error) {
	if _, ok := tools[tool]; !ok {
		return nil, fmt.Errorf("unknown tool: %s", tool)
	}
	if h.vault == nil {
		return nil, fmt.Errorf("vault not available")
	}
	profiles, err := h.vault.List(tool)
	if err != nil {
		return nil, err
	}

	found := false
	for _, p := range profiles {
		if sameProfileName(p, name) {
			found = true
			break
		}
	}
	if !found {
		return nil, fmt.Errorf("profile not found: %s/%s", tool, name)
	}

	fileSet := tools[tool]()
	activeProfile, _ := h.vault.ActiveProfile(fileSet)

	return &ProfileInfo{
		Tool:     tool,
		Name:     name,
		Active:   sameProfileName(name, activeProfile),
		System:   authfile.IsSystemProfile(name),
		Health:   h.getProfileHealth(tool, name),
		Identity: h.getProfileIdentity(tool, name),
	}, nil
}

// DeleteProfile deletes a profile.
func (h *Handlers) DeleteProfile(tool, name string) error {
	if _, ok := tools[tool]; !ok {
		return fmt.Errorf("unknown tool: %s", tool)
	}
	if name == "" {
		return fmt.Errorf("profile is required")
	}
	if h.vault == nil {
		return fmt.Errorf("vault not available")
	}
	if authfile.IsSystemProfile(name) {
		return fmt.Errorf("cannot delete system profile: %s/%s", tool, name)
	}
	return h.vault.Delete(tool, name)
}

// GetUsage returns health-derived usage counters.
func (h *Handlers) GetUsage(tool string) (*UsageResponse, error) {
	if tool != "" {
		if _, ok := tools[tool]; !ok {
			return nil, fmt.Errorf("unknown tool: %s", tool)
		}
	}

	resp := &UsageResponse{
		Tool:    tool,
		Window:  "1h",
		Metric:  "health_error_count",
		Entries: []UsageEntry{},
	}

	if h.healthStore == nil {
		return resp, nil
	}
	if h.vault == nil {
		return nil, fmt.Errorf("vault not available")
	}

	// Get all health data
	toolsToCheck := []string{"codex", "claude", "gemini"}
	if tool != "" {
		toolsToCheck = []string{tool}
	}

	for _, t := range toolsToCheck {
		profiles, err := h.vault.List(t)
		if err != nil {
			return nil, err
		}

		for _, name := range profiles {
			ph, err := h.healthStore.GetProfile(t, name)
			if err != nil || ph == nil {
				continue
			}

			entry := UsageEntry{
				Tool:               t,
				Profile:            name,
				HealthErrorCount1h: ph.ErrorCount1h,
			}
			if !ph.LastChecked.IsZero() {
				entry.LastChecked = ph.LastChecked.Format(time.RFC3339)
			}
			resp.Entries = append(resp.Entries, entry)
		}
	}

	return resp, nil
}

// GetActivity returns recent redacted activity-log events.
func (h *Handlers) GetActivity(limit int) (*ActivityResponse, error) {
	resp := &ActivityResponse{
		Events: []ActivityEntry{},
	}
	if h.db == nil {
		return resp, nil
	}
	if limit <= 0 {
		limit = 20
	}
	if limit > 100 {
		limit = 100
	}

	events, err := h.db.ListRecentEvents(limit)
	if err != nil {
		return nil, err
	}
	for _, event := range events {
		entry := ActivityEntry{
			Timestamp:       event.Timestamp.Format(time.RFC3339),
			Type:            event.Type,
			Tool:            event.Provider,
			Profile:         event.ProfileName,
			Message:         formatActivityMessage(event),
			DurationSeconds: int64(event.Duration / time.Second),
		}
		resp.Events = append(resp.Events, entry)
	}
	resp.Count = len(resp.Events)
	return resp, nil
}

// GetCoordinators returns live status for configured coordinator endpoints.
func (h *Handlers) GetCoordinators(ctx context.Context) (*CoordinatorsResponse, error) {
	if ctx == nil {
		ctx = context.Background()
	}

	resp := &CoordinatorsResponse{
		Coordinators: []CoordinatorStatus{},
	}
	for _, endpoint := range h.coordinatorEndpoints {
		resp.Coordinators = append(resp.Coordinators, h.probeCoordinator(ctx, endpoint))
	}
	return resp, nil
}

func (h *Handlers) probeCoordinator(ctx context.Context, endpoint CoordinatorEndpoint) CoordinatorStatus {
	status := CoordinatorStatus{
		ID:       endpoint.ID,
		Endpoint: endpoint.Endpoint,
		Status:   "unhealthy",
	}
	baseURL := strings.TrimRight(strings.TrimSpace(endpoint.Endpoint), "/")
	if baseURL == "" {
		status.Error = "coordinator endpoint URL is empty"
		return status
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/status", nil)
	if err != nil {
		status.Error = fmt.Sprintf("create status request: %v", err)
		return status
	}
	if token := strings.TrimSpace(endpoint.Token); token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	client := h.coordinatorClient
	if client == nil {
		client = &http.Client{Timeout: 2 * time.Second}
	}
	httpResp, err := client.Do(req)
	if err != nil {
		status.Error = err.Error()
		return status
	}
	defer httpResp.Body.Close()

	if httpResp.StatusCode != http.StatusOK {
		body, readErr := io.ReadAll(io.LimitReader(httpResp.Body, 4096))
		if readErr != nil {
			status.Error = fmt.Sprintf("HTTP %d; read error body: %v", httpResp.StatusCode, readErr)
			return status
		}
		message := strings.TrimSpace(string(body))
		if message == "" {
			message = httpResp.Status
		}
		status.Error = fmt.Sprintf("HTTP %d: %s", httpResp.StatusCode, message)
		return status
	}

	var body struct {
		Running      bool   `json:"running"`
		Backend      string `json:"backend"`
		PaneCount    int    `json:"pane_count"`
		PendingAuths int    `json:"pending_auths"`
	}
	if err := json.NewDecoder(io.LimitReader(httpResp.Body, coordinatorProbeMaxBytes)).Decode(&body); err != nil {
		status.Error = fmt.Sprintf("decode status response: %v", err)
		return status
	}

	status.Backend = body.Backend
	status.PaneCount = body.PaneCount
	status.PendingAuths = body.PendingAuths
	status.LastSeen = time.Now().Format(time.RFC3339)
	if body.Running {
		status.Status = "healthy"
	} else {
		status.Status = "stopped"
	}
	return status
}

// Activate activates a profile.
func (h *Handlers) Activate(req ActivateRequest) (*ActivateResponse, error) {
	getFileSet, ok := tools[req.Tool]
	if !ok {
		return nil, fmt.Errorf("unknown tool: %s", req.Tool)
	}
	if req.Profile == "" {
		return nil, fmt.Errorf("profile is required")
	}
	if h.vault == nil {
		return nil, fmt.Errorf("vault not available")
	}

	fileSet := getFileSet()

	// Check cooldown if not forcing
	if !req.Force && h.db != nil {
		now := time.Now()
		cooldown, err := h.db.ActiveCooldown(req.Tool, req.Profile, now)
		if err == nil && cooldown != nil {
			remaining := cooldown.CooldownUntil.Sub(now)
			if remaining > 0 {
				return &ActivateResponse{
					Success: false,
					Tool:    req.Tool,
					Profile: req.Profile,
					Message: fmt.Sprintf("profile in cooldown (%s remaining), use force to override", formatDuration(remaining)),
				}, nil
			}
		}
	}

	// Restore the profile (activating it)
	if err := h.vault.Restore(fileSet, req.Profile); err != nil {
		return nil, fmt.Errorf("activate failed: %w", err)
	}

	return &ActivateResponse{
		Success: true,
		Tool:    req.Tool,
		Profile: req.Profile,
		Message: fmt.Sprintf("activated %s/%s", req.Tool, req.Profile),
	}, nil
}

// Backup backs up current auth to a profile.
func (h *Handlers) Backup(req BackupRequest) (*BackupResponse, error) {
	getFileSet, ok := tools[req.Tool]
	if !ok {
		return nil, fmt.Errorf("unknown tool: %s", req.Tool)
	}
	if req.Profile == "" {
		return nil, fmt.Errorf("profile is required")
	}
	if h.vault == nil {
		return nil, fmt.Errorf("vault not available")
	}

	fileSet := getFileSet()

	if !authfile.HasAuthFiles(fileSet) {
		return &BackupResponse{
			Success: false,
			Tool:    req.Tool,
			Profile: req.Profile,
			Message: fmt.Sprintf("no auth files found for %s", req.Tool),
		}, nil
	}

	if err := h.vault.Backup(fileSet, req.Profile); err != nil {
		return nil, fmt.Errorf("backup failed: %w", err)
	}

	return &BackupResponse{
		Success: true,
		Tool:    req.Tool,
		Profile: req.Profile,
		Path:    h.vault.ProfilePath(req.Tool, req.Profile),
		Message: fmt.Sprintf("backed up %s to %s", req.Tool, req.Profile),
	}, nil
}

// getProfileHealth returns health status for a profile.
func (h *Handlers) getProfileHealth(tool, name string) *HealthStatus {
	if h.healthStore == nil {
		return nil
	}

	ph, err := h.healthStore.GetProfile(tool, name)
	if err != nil {
		ph = nil
	}
	if ph == nil {
		return nil
	}

	status := health.CalculateStatus(ph)
	hs := &HealthStatus{
		Status:     status.String(),
		ErrorCount: ph.ErrorCount1h,
	}

	if !ph.TokenExpiresAt.IsZero() {
		hs.ExpiresAt = ph.TokenExpiresAt.Format(time.RFC3339)
	}

	// Check cooldown
	if h.db != nil {
		now := time.Now()
		cooldown, err := h.db.ActiveCooldown(tool, name, now)
		if err == nil && cooldown != nil {
			remaining := cooldown.CooldownUntil.Sub(now)
			if remaining > 0 {
				hs.CooldownRemaining = formatDuration(remaining)
			}
		}
	}

	return hs
}

// getProfileIdentity returns identity info for a profile.
func (h *Handlers) getProfileIdentity(tool, name string) *identity.Identity {
	if h.vault == nil {
		return nil
	}

	vaultPath := h.vault.ProfilePath(tool, name)
	var id *identity.Identity
	var err error

	switch tool {
	case "codex":
		id, err = identity.ExtractFromCodexAuth(vaultPath + "/auth.json")
	case "claude":
		id, err = identity.ExtractFromClaudeCredentials(vaultPath + "/.credentials.json")
	case "gemini":
		// Migrate legacy vault filename before reading.
		_ = authfile.MigrateGeminiVaultDir(vaultPath)
		id, err = identity.ExtractFromGeminiConfig(vaultPath + "/settings.json")
		if err != nil {
			id, err = identity.ExtractFromGeminiConfig(vaultPath + "/oauth_creds.json")
		}
	case "opencode":
		id, err = identity.ExtractFromGenericAuth(vaultPath + "/auth.json")
	case "cursor":
		id, err = identity.ExtractFromGenericAuth(vaultPath + "/auth.json")
		if err != nil {
			id, err = identity.ExtractFromGenericAuth(vaultPath + "/settings.json")
		}
	}

	if err != nil {
		id = nil
	}

	// Identity fields are already safe for API responses
	// (no sensitive tokens are included in the Identity struct)

	return id
}

func formatActivityMessage(event caamdb.Event) string {
	target := strings.TrimSpace(event.Provider)
	if profile := strings.TrimSpace(event.ProfileName); profile != "" {
		if target != "" {
			target += "/"
		}
		target += profile
	}
	if target == "" {
		target = "profile"
	}

	switch event.Type {
	case caamdb.EventActivate:
		return fmt.Sprintf("Activated %s", target)
	case caamdb.EventLogin:
		return fmt.Sprintf("Logged in %s", target)
	case caamdb.EventRefresh:
		return fmt.Sprintf("Refreshed %s", target)
	case caamdb.EventError:
		return fmt.Sprintf("Recorded error for %s", target)
	case caamdb.EventSwitch:
		return fmt.Sprintf("Switched to %s", target)
	case caamdb.EventDeactivate:
		return fmt.Sprintf("Deactivated %s", target)
	default:
		return fmt.Sprintf("%s event for %s", event.Type, target)
	}
}

// formatDuration formats a duration for display.
func formatDuration(d time.Duration) string {
	if d >= time.Hour {
		hours := int(d.Hours())
		mins := int(d.Minutes()) % 60
		return fmt.Sprintf("%dh %dm", hours, mins)
	}
	mins := int(d.Minutes())
	if mins < 1 {
		return "<1m"
	}
	return fmt.Sprintf("%dm", mins)
}
