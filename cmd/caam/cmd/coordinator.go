package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/Dicklesworthstone/coding_agent_account_manager/internal/coordinator"
	"github.com/spf13/cobra"
)

var coordinatorCmd = &cobra.Command{
	Use:   "auth-coordinator",
	Short: "Run the distributed auth recovery coordinator daemon",
	Long: `Monitor terminal panes for Claude Code rate limits and coordinate authentication.

The coordinator watches terminal panes for rate limit messages. When detected, it:
1. Auto-injects /login command
2. Selects Claude subscription login method
3. Extracts the OAuth URL
4. Exposes the URL via HTTP API for the local auth-agent
5. Receives auth codes from the agent and injects them
6. Resumes the session automatically

TERMINAL BACKENDS:
  WezTerm (PREFERRED) - Use WezTerm's native mux-server for best integration.
    Benefits: integrated multiplexing, domain awareness, rich metadata.

  tmux (FALLBACK) - For Ghostty, Alacritty, iTerm2, or other terminals.
    Requires: tmux server running (tmux new-session -d)
    Limitations: no domain awareness, extra process layer, less metadata.

This daemon should run on the remote machine where Claude Code sessions are running.
The local auth-agent connects to this coordinator to complete OAuth flows.

Examples:
  # Start coordinator (auto-detects best backend)
  caam auth-coordinator

  # Force WezTerm backend
  caam auth-coordinator --backend wezterm

  # Force tmux backend (for Ghostty/Alacritty/iTerm2)
  caam auth-coordinator --backend tmux

  # Custom port and verbose logging
  caam auth-coordinator --port 7891 --verbose

SSH Tunnel Setup (run on local Mac):
  ssh -R 7890:localhost:7891 user@remote-server -N`,
	RunE: runCoordinator,
}

var (
	coordinatorPort         int
	coordinatorPollMs       int
	coordinatorResumePrompt string
	coordinatorVerbose      bool
	coordinatorJSONLogs     bool
	coordinatorBackend      string
	coordinatorConfigPath   string
	coordinatorAuthToken    string
)

const coordinatorStatusMaxBytes = 1 << 20

func init() {
	rootCmd.AddCommand(coordinatorCmd)

	coordinatorCmd.Flags().IntVar(&coordinatorPort, "port", 7890, "API server port")
	coordinatorCmd.Flags().IntVar(&coordinatorPollMs, "poll-interval", 500, "Pane poll interval in milliseconds")
	coordinatorCmd.Flags().StringVar(&coordinatorResumePrompt, "resume-prompt",
		"proceed. Reread AGENTS.md so it's still fresh in your mind. Use ultrathink.\n",
		"Text to inject after successful auth")
	coordinatorCmd.Flags().BoolVar(&coordinatorVerbose, "verbose", false, "Verbose output (debug level)")
	coordinatorCmd.Flags().BoolVar(&coordinatorJSONLogs, "json", false, "Output logs in JSON format")
	coordinatorCmd.Flags().StringVar(&coordinatorBackend, "backend", "auto",
		"Terminal multiplexer backend: wezterm (preferred), tmux, or auto")
	coordinatorCmd.Flags().StringVar(&coordinatorConfigPath, "config", "", "Path to JSON config file")
	coordinatorCmd.Flags().StringVar(&coordinatorAuthToken, "auth-token", "", "Auth token for coordinator API (shared secret)")

	coordinatorStatusCmd.Flags().IntVar(&coordinatorPort, "port", 7890, "API server port")
	coordinatorStatusCmd.Flags().StringVar(&coordinatorAuthToken, "auth-token", "", "Auth token for coordinator API (shared secret)")
}

func runCoordinator(cmd *cobra.Command, args []string) error {
	// Setup logger
	logLevel := slog.LevelInfo
	if coordinatorVerbose {
		logLevel = slog.LevelDebug
	}

	// Choose log format: JSON or text
	var logHandler slog.Handler
	logOpts := &slog.HandlerOptions{Level: logLevel}
	if coordinatorJSONLogs {
		logHandler = slog.NewJSONHandler(os.Stderr, logOpts)
	} else {
		logHandler = slog.NewTextHandler(os.Stderr, logOpts)
	}
	logger := slog.New(logHandler)

	config := coordinator.DefaultConfig()
	apiPort := coordinatorPort

	if coordinatorConfigPath != "" {
		loadedConfig, loadedPort, err := loadCoordinatorConfig(coordinatorConfigPath)
		if err != nil {
			return err
		}
		config = loadedConfig
		apiPort = loadedPort
	}

	if cmd.Flags().Changed("backend") {
		backend, err := parseBackend(coordinatorBackend)
		if err != nil {
			return err
		}
		config.Backend = backend
	}
	if cmd.Flags().Changed("poll-interval") {
		config.PollInterval = time.Duration(coordinatorPollMs) * time.Millisecond
	}
	if cmd.Flags().Changed("resume-prompt") {
		config.ResumePrompt = coordinatorResumePrompt
	}
	if cmd.Flags().Changed("port") {
		apiPort = coordinatorPort
	}
	if cmd.Flags().Changed("auth-token") {
		config.AuthToken = coordinatorAuthToken
	} else if envToken := strings.TrimSpace(os.Getenv("CAAM_COORDINATOR_TOKEN")); envToken != "" {
		config.AuthToken = envToken
	}

	config.Logger = logger

	// Create coordinator
	coord := coordinator.New(config)

	// Set up callbacks
	coord.OnAuthRequest = func(req *coordinator.AuthRequest) {
		fmt.Printf("[%s] AUTH NEEDED pane=%d url=%s\n",
			time.Now().Format("15:04:05"),
			req.PaneID,
			truncateURL(req.URL))
	}

	coord.OnAuthComplete = func(paneID int, account string) {
		fmt.Printf("[%s] AUTH COMPLETE pane=%d account=%s\n",
			time.Now().Format("15:04:05"),
			paneID,
			account)
	}

	coord.OnAuthFailed = func(paneID int, err error) {
		fmt.Printf("[%s] AUTH FAILED pane=%d error=%s\n",
			time.Now().Format("15:04:05"),
			paneID,
			err)
	}

	// Create API server
	api := coordinator.NewAPIServer(coord, apiPort, logger)

	// Start coordinator
	ctx, cancel := context.WithCancel(cmd.Context())
	defer cancel()

	if err := coord.Start(ctx); err != nil {
		return fmt.Errorf("start coordinator: %w", err)
	}

	// Handle signals
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	// Start API server in background
	errCh := make(chan error, 1)
	go func() {
		errCh <- api.Start()
	}()

	fmt.Printf("Auth coordinator started\n")
	fmt.Printf("  Backend: %s\n", coord.Backend())
	fmt.Printf("  API: http://localhost:%d\n", apiPort)
	fmt.Printf("  Poll interval: %dms\n", int(config.PollInterval.Milliseconds()))
	if config.AuthToken != "" {
		fmt.Println("  Auth: token required")
	}
	if coord.Backend() == "tmux" {
		fmt.Println("\nNote: Using tmux fallback. WezTerm is recommended for better integration.")
	}
	fmt.Println("\nWaiting for rate limits...")
	fmt.Println("Press Ctrl+C to stop.")

	// Wait for signal or error
	select {
	case <-sigCh:
		fmt.Println("\nShutting down...")
	case err := <-errCh:
		if err != nil {
			return fmt.Errorf("API server error: %w", err)
		}
	case <-ctx.Done():
	}

	// Graceful shutdown
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer shutdownCancel()

	if err := api.Shutdown(shutdownCtx); err != nil {
		logger.Warn("API shutdown error", "error", err)
	}

	if err := coord.Stop(); err != nil {
		logger.Warn("coordinator stop error", "error", err)
	}

	fmt.Println("Coordinator stopped.")
	return nil
}

func truncateURL(url string) string {
	if len(url) > 80 {
		return url[:77] + "..."
	}
	return url
}

type coordinatorFileConfig struct {
	Port           int    `json:"port"`
	PollInterval   string `json:"poll_interval"`
	AuthTimeout    string `json:"auth_timeout"`
	StateTimeout   string `json:"state_timeout"`
	ResumePrompt   string `json:"resume_prompt"`
	ResumeCooldown string `json:"resume_cooldown"`
	OutputLines    int    `json:"output_lines"`
	Backend        string `json:"backend"`
	AuthToken      string `json:"auth_token"`
}

func loadCoordinatorConfig(path string) (coordinator.Config, int, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return coordinator.Config{}, 0, fmt.Errorf("read config: %w", err)
	}

	var raw coordinatorFileConfig
	if err := json.Unmarshal(data, &raw); err != nil {
		return coordinator.Config{}, 0, fmt.Errorf("parse config: %w", err)
	}

	cfg := coordinator.DefaultConfig()
	apiPort := coordinatorPort
	if raw.Port != 0 {
		apiPort = raw.Port
	}
	if raw.PollInterval != "" {
		if d, err := time.ParseDuration(raw.PollInterval); err == nil {
			cfg.PollInterval = d
		} else {
			return coordinator.Config{}, 0, fmt.Errorf("parse poll_interval: %w", err)
		}
	}
	if raw.AuthTimeout != "" {
		if d, err := time.ParseDuration(raw.AuthTimeout); err == nil {
			cfg.AuthTimeout = d
		} else {
			return coordinator.Config{}, 0, fmt.Errorf("parse auth_timeout: %w", err)
		}
	}
	if raw.StateTimeout != "" {
		if d, err := time.ParseDuration(raw.StateTimeout); err == nil {
			cfg.StateTimeout = d
		} else {
			return coordinator.Config{}, 0, fmt.Errorf("parse state_timeout: %w", err)
		}
	}
	if raw.ResumePrompt != "" {
		cfg.ResumePrompt = raw.ResumePrompt
	}
	if raw.ResumeCooldown != "" {
		if d, err := time.ParseDuration(raw.ResumeCooldown); err == nil {
			cfg.ResumeCooldown = d
		} else {
			return coordinator.Config{}, 0, fmt.Errorf("parse resume_cooldown: %w", err)
		}
	}
	if raw.OutputLines != 0 {
		cfg.OutputLines = raw.OutputLines
	}
	if raw.Backend != "" {
		backend, err := parseBackend(raw.Backend)
		if err != nil {
			return coordinator.Config{}, 0, err
		}
		cfg.Backend = backend
	}
	if raw.AuthToken != "" {
		cfg.AuthToken = raw.AuthToken
	}

	return cfg, apiPort, nil
}

func parseBackend(value string) (coordinator.Backend, error) {
	switch strings.ToLower(value) {
	case "wezterm":
		return coordinator.BackendWezTerm, nil
	case "tmux":
		return coordinator.BackendTmux, nil
	case "auto", "":
		return coordinator.BackendAuto, nil
	default:
		return "", fmt.Errorf("invalid backend %q: use wezterm, tmux, or auto", value)
	}
}

// statusCmd shows coordinator status
var coordinatorStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show auth-coordinator status",
	RunE: func(cmd *cobra.Command, args []string) error {
		status, err := fetchCoordinatorStatus(cmd.Context(), &http.Client{Timeout: 2 * time.Second}, coordinatorPort, coordinatorAuthToken)
		if err != nil {
			return err
		}

		out := cmd.OutOrStdout()
		fmt.Fprintf(out, "Auth coordinator status\n")
		fmt.Fprintf(out, "  Running: %t\n", status.Running)
		fmt.Fprintf(out, "  Backend: %s\n", status.Backend)
		fmt.Fprintf(out, "  Panes: %d\n", status.PaneCount)
		fmt.Fprintf(out, "  Pending auths: %d\n", status.PendingAuths)
		if len(status.Panes) > 0 {
			fmt.Fprintln(out, "\nPanes:")
			for _, pane := range status.Panes {
				details := []string{fmt.Sprintf("state=%s", pane.State)}
				if pane.RequestID != "" {
					details = append(details, "request_id="+pane.RequestID)
				}
				if pane.Account != "" {
					details = append(details, "account="+pane.Account)
				}
				if pane.Error != "" {
					details = append(details, "error="+pane.Error)
				}
				fmt.Fprintf(out, "  %d: %s\n", pane.PaneID, strings.Join(details, " "))
			}
		}
		return nil
	},
}

func init() {
	coordinatorCmd.AddCommand(coordinatorStatusCmd)
}

// filterClaudePanes returns true for panes likely running Claude Code.
func filterClaudePanes(pane coordinator.Pane) bool {
	title := strings.ToLower(pane.Title)
	return strings.Contains(title, "claude") ||
		strings.Contains(title, "cc") ||
		strings.Contains(title, "anthropic")
}

type coordinatorStatusResponse struct {
	Running      bool                            `json:"running"`
	Backend      string                          `json:"backend"`
	PaneCount    int                             `json:"pane_count"`
	PendingAuths int                             `json:"pending_auths"`
	Panes        []coordinatorPaneStatusResponse `json:"panes"`
}

type coordinatorPaneStatusResponse struct {
	PaneID       int       `json:"pane_id"`
	State        string    `json:"state"`
	StateEntered time.Time `json:"state_entered"`
	RequestID    string    `json:"request_id,omitempty"`
	Account      string    `json:"account,omitempty"`
	Error        string    `json:"error,omitempty"`
}

type coordinatorStatusHTTPClient interface {
	Do(*http.Request) (*http.Response, error)
}

func fetchCoordinatorStatus(ctx context.Context, client coordinatorStatusHTTPClient, port int, token string) (*coordinatorStatusResponse, error) {
	if client == nil {
		client = http.DefaultClient
	}
	if port <= 0 || port > 65535 {
		return nil, fmt.Errorf("invalid coordinator port %d", port)
	}

	url := fmt.Sprintf("http://127.0.0.1:%d/status", port)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("create coordinator status request: %w", err)
	}
	if token = strings.TrimSpace(token); token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	} else if envToken := strings.TrimSpace(os.Getenv("CAAM_COORDINATOR_TOKEN")); envToken != "" {
		req.Header.Set("Authorization", "Bearer "+envToken)
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("coordinator status unavailable at %s: %w", url, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, readErr := io.ReadAll(io.LimitReader(resp.Body, 4096))
		if readErr != nil {
			return nil, fmt.Errorf("read coordinator status error response from %s: %w", url, readErr)
		}
		message := strings.TrimSpace(string(body))
		if message == "" {
			message = resp.Status
		}
		return nil, fmt.Errorf("coordinator status unavailable at %s: HTTP %d: %s", url, resp.StatusCode, message)
	}

	var status coordinatorStatusResponse
	if err := json.NewDecoder(io.LimitReader(resp.Body, coordinatorStatusMaxBytes)).Decode(&status); err != nil {
		return nil, fmt.Errorf("decode coordinator status response: %w", err)
	}

	return &status, nil
}
