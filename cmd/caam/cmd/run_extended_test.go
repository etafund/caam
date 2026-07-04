package cmd

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Dicklesworthstone/coding_agent_account_manager/internal/authfile"
	"github.com/Dicklesworthstone/coding_agent_account_manager/internal/config"
	caamdb "github.com/Dicklesworthstone/coding_agent_account_manager/internal/db"
	caamexec "github.com/Dicklesworthstone/coding_agent_account_manager/internal/exec"
	"github.com/Dicklesworthstone/coding_agent_account_manager/internal/notify"
	"github.com/Dicklesworthstone/coding_agent_account_manager/internal/profile"
	"github.com/Dicklesworthstone/coding_agent_account_manager/internal/provider"
	"github.com/Dicklesworthstone/coding_agent_account_manager/internal/rotation"
	"github.com/Dicklesworthstone/coding_agent_account_manager/internal/testutil"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"
)

// TestHelperProcess_Run is the entry point for the mock process for run tests.
func TestHelperProcess_Run(t *testing.T) {
	if os.Getenv("GO_WANT_HELPER_PROCESS") != "1" {
		return
	}

	mode := os.Getenv("MOCK_RUN_MODE")
	switch mode {
	case "success":
		fmt.Println("Command success")
		os.Exit(0)
	case "ratelimit":
		fmt.Println("Error: rate limit exceeded")
		os.Exit(1)
	case "failover":
		// Fail first time (if profile is work), succeed second time (if profile is personal)
		authPath := os.Getenv("MOCK_AUTH_PATH")
		content, _ := os.ReadFile(authPath)
		if strings.Contains(string(content), `"token":"work"`) {
			fmt.Println("Error: rate limit exceeded")
			os.Exit(1)
		} else {
			fmt.Println("Command success (failover)")
			os.Exit(0)
		}
	case "handoff-success":
		runExtendedMockHandoffSuccess()
	case "early-exit":
		runExtendedMockEarlyExit()
	case "stall-login":
		runExtendedMockStallLogin()
	default:
		fmt.Fprintln(os.Stderr, "Unknown mock run mode")
		os.Exit(1)
	}
}

func TestRunCommand_Extended(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("SmartRunner PTY handoff requires a Unix PTY; the Windows controller returns ErrNotSupported")
	}

	t.Run("successful handoff preserves output", func(t *testing.T) {
		fixture := newRunExtendedFixture(t, "handoff-success")

		var releaseOnce sync.Once
		var exitOnce sync.Once
		var observed strings.Builder
		var releaseMu sync.Mutex
		var releaseErr error
		recordReleaseErr := func(err error) {
			if err == nil {
				return
			}
			releaseMu.Lock()
			defer releaseMu.Unlock()
			if releaseErr == nil {
				releaseErr = err
			}
		}
		out, err := captureRunExtendedStdout(t, func(chunk string) {
			observed.WriteString(chunk)
			output := observed.String()
			if strings.Contains(output, "successfully authenticated") {
				releaseOnce.Do(func() {
					recordReleaseErr(os.WriteFile(fixture.loginReleasePath, []byte("ok"), 0600))
				})
			}
			if strings.Contains(output, "mock-output: after-handoff") {
				exitOnce.Do(func() {
					recordReleaseErr(os.WriteFile(fixture.exitReleasePath, []byte("ok"), 0600))
				})
			}
		}, func() error {
			return runWrap(newRunExtendedCommand(context.Background()), []string{"gemini", "prompt"})
		})
		require.NoError(t, err)
		releaseMu.Lock()
		err = releaseErr
		releaseMu.Unlock()
		require.NoError(t, err)

		require.Contains(t, out, "mock-output: before-handoff")
		require.Contains(t, out, "Error: rate limit exceeded")
		require.Contains(t, out, "successfully authenticated")
		require.Contains(t, out, "mock-output: after-handoff")
		require.Equal(t, "backup", runExtendedAuthLabel(fixture.authPath))

		events := fixture.readEvents(t)
		require.Contains(t, events, "rate_limit_emitted")
		require.Contains(t, events, "login_received:/auth")
		require.Contains(t, events, "auth_label_after_login:backup")

		db := fixture.openDB(t)
		defer db.Close()

		cooldown, err := db.ActiveCooldown("gemini", "active-profile", time.Now())
		require.NoError(t, err)
		require.NotNil(t, cooldown, "active profile should be put in cooldown")

		sessions, err := db.GetWrapSessions("gemini", time.Now().Add(-time.Hour), 10)
		require.NoError(t, err)
		require.NotEmpty(t, sessions)
		require.Equal(t, "backup-profile", sessions[0].ProfileName)
		require.True(t, sessions[0].RateLimitHit)
	})

	t.Run("early child exit leaves active auth restored", func(t *testing.T) {
		fixture := newRunExtendedFixture(t, "early-exit")

		out, err := captureStdout(t, func() error {
			return runWrap(newRunExtendedCommand(context.Background()), []string{"gemini", "prompt"})
		})
		require.NoError(t, err)

		require.Contains(t, out, "mock-output: early-before-exit")
		require.Contains(t, out, "Error: rate limit exceeded")
		require.NotContains(t, fixture.readEvents(t), "login_received:")
		require.Equal(t, "active", runExtendedAuthLabel(fixture.authPath))
	})

	t.Run("cancel during login rolls back auth", func(t *testing.T) {
		fixture := newRunExtendedFixture(t, "stall-login")

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		out, err := captureStdout(t, func() error {
			errCh := make(chan error, 1)
			go func() {
				errCh <- fixture.runSmartRunner(ctx)
			}()

			waitForRunExtendedEvent(t, fixture.eventLog, "login_received:/auth", 5*time.Second)
			cancel()

			select {
			case err := <-errCh:
				return err
			case <-time.After(5 * time.Second):
				t.Fatalf("SmartRunner did not return after context cancellation; events:\n%s", fixture.readEvents(t))
				return nil
			}
		})
		require.Error(t, err)
		require.Contains(t, out, "waiting for authentication")
		require.Equal(t, "active", runExtendedAuthLabel(fixture.authPath))
	})
}

type runExtendedFixture struct {
	rootDir          string
	caamHome         string
	vaultDir         string
	profilesDir      string
	geminiHome       string
	authPath         string
	eventLog         string
	loginReleasePath string
	exitReleasePath  string
	profileStore     *profile.Store
}

func newRunExtendedFixture(t *testing.T, mode string) *runExtendedFixture {
	t.Helper()

	h := testutil.NewExtendedHarness(t)
	t.Cleanup(h.Close)

	rootDir := h.TempDir
	caamHome := filepath.Join(rootDir, "caam-home")
	vaultDir := filepath.Join(caamHome, "data", "vault")
	profilesDir := filepath.Join(caamHome, "data", "profiles")
	geminiHome := filepath.Join(rootDir, "gemini-home")
	authPath := filepath.Join(geminiHome, "settings.json")
	eventLog := filepath.Join(rootDir, "mock-events.log")
	loginReleasePath := filepath.Join(rootDir, "login-release")
	exitReleasePath := filepath.Join(rootDir, "exit-release")

	h.SetEnv("HOME", filepath.Join(rootDir, "home"))
	h.SetEnv("CAAM_HOME", caamHome)
	h.SetEnv("GEMINI_HOME", geminiHome)
	h.SetEnv("GO_WANT_HELPER_PROCESS", "1")
	h.SetEnv("MOCK_RUN_MODE", mode)
	h.SetEnv("MOCK_AUTH_PATH", authPath)
	h.SetEnv("MOCK_EVENT_LOG", eventLog)
	h.SetEnv("MOCK_LOGIN_RELEASE_FILE", loginReleasePath)
	h.SetEnv("MOCK_EXIT_RELEASE_FILE", exitReleasePath)

	require.NoError(t, os.MkdirAll(filepath.Dir(authPath), 0700))
	require.NoError(t, os.WriteFile(authPath, []byte(runExtendedActiveAuth), 0600))
	require.NoError(t, os.MkdirAll(filepath.Join(vaultDir, "gemini", "active-profile"), 0700))
	require.NoError(t, os.MkdirAll(filepath.Join(vaultDir, "gemini", "backup-profile"), 0700))
	require.NoError(t, os.WriteFile(filepath.Join(vaultDir, "gemini", "active-profile", "settings.json"), []byte(runExtendedActiveAuth), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(vaultDir, "gemini", "backup-profile", "settings.json"), []byte(runExtendedBackupAuth), 0600))

	store := profile.NewStore(profilesDir)
	_, err := store.Create("gemini", "active-profile", "oauth")
	require.NoError(t, err)
	_, err = store.Create("gemini", "backup-profile", "oauth")
	require.NoError(t, err)

	originalVault := vault
	originalTools := make(map[string]func() authfile.AuthFileSet, len(tools))
	for k, v := range tools {
		originalTools[k] = v
	}
	originalRegistry := registry
	originalRunner := runner
	originalExecCommand := caamexec.ExecCommand
	originalGetWd := getWd
	originalProfileStore := profileStore

	if globalDB != nil {
		_ = globalDB.Close()
		globalDB = nil
	}

	vault = authfile.NewVault(vaultDir)
	profileStore = store
	registry = provider.NewRegistry()
	registry.Register(&MockProvider{id: "gemini"})
	runner = caamexec.NewRunner(registry)
	getWd = func() (string, error) {
		return rootDir, nil
	}
	caamexec.ExecCommand = func(ctx context.Context, name string, args ...string) *exec.Cmd {
		cs := []string{"-test.run=^TestHelperProcess_Run$", "--", name}
		cs = append(cs, args...)
		return exec.CommandContext(ctx, os.Args[0], cs...)
	}

	t.Cleanup(func() {
		if globalDB != nil {
			_ = globalDB.Close()
			globalDB = nil
		}
		vault = originalVault
		tools = originalTools
		registry = originalRegistry
		runner = originalRunner
		caamexec.ExecCommand = originalExecCommand
		getWd = originalGetWd
		profileStore = originalProfileStore
	})

	fixture := &runExtendedFixture{
		rootDir:          rootDir,
		caamHome:         caamHome,
		vaultDir:         vaultDir,
		profilesDir:      profilesDir,
		geminiHome:       geminiHome,
		authPath:         authPath,
		eventLog:         eventLog,
		loginReleasePath: loginReleasePath,
		exitReleasePath:  exitReleasePath,
		profileStore:     store,
	}
	t.Cleanup(func() {
		if t.Failed() {
			t.Logf("SmartRunner mock events:\n%s", fixture.readEvents(t))
		}
	})

	return fixture
}

func newRunExtendedCommand(ctx context.Context) *cobra.Command {
	cmd := &cobra.Command{}
	cmd.SetContext(ctx)
	cmd.Flags().Int("max-retries", 1, "")
	cmd.Flags().Duration("cooldown", 30*time.Minute, "")
	cmd.Flags().Bool("quiet", false, "")
	cmd.Flags().String("algorithm", "round_robin", "")
	cmd.Flags().Bool("precheck", false, "")
	cmd.Flags().Float64("precheck-threshold", 0.8, "")
	return cmd
}

func (f *runExtendedFixture) openDB(t *testing.T) *caamdb.DB {
	t.Helper()
	db, err := caamdb.OpenAt(filepath.Join(f.caamHome, "data", "caam.db"))
	require.NoError(t, err)
	return db
}

func (f *runExtendedFixture) runSmartRunner(ctx context.Context) error {
	db, err := caamdb.OpenAt(filepath.Join(f.caamHome, "data", "caam.db"))
	if err != nil {
		return err
	}
	defer db.Close()

	prof, err := f.profileStore.Load("gemini", "active-profile")
	if err != nil {
		return err
	}

	cfg := config.DefaultSPMConfig().Handoff
	selector := rotation.NewSelector(rotation.AlgorithmRoundRobin, nil, db)
	smartRunner := caamexec.NewSmartRunner(caamexec.NewRunner(provider.NewRegistry()), caamexec.SmartRunnerOptions{
		HandoffConfig:    &cfg,
		Notifier:         runExtendedNoopNotifier{},
		Vault:            authfile.NewVault(f.vaultDir),
		DB:               db,
		Rotation:         selector,
		CooldownDuration: 30 * time.Minute,
	})

	return smartRunner.Run(ctx, caamexec.RunOptions{
		Profile:  prof,
		Provider: &MockProvider{id: "gemini"},
	})
}

type runExtendedNoopNotifier struct{}

func (runExtendedNoopNotifier) Notify(*notify.Alert) error { return nil }
func (runExtendedNoopNotifier) Name() string               { return "run-extended-noop" }
func (runExtendedNoopNotifier) Available() bool            { return true }

func (f *runExtendedFixture) readEvents(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile(f.eventLog)
	if os.IsNotExist(err) {
		return ""
	}
	require.NoError(t, err)
	return string(data)
}

type runExtendedObservedWriter struct {
	buf     *bytes.Buffer
	observe func(string)
}

func (w *runExtendedObservedWriter) Write(p []byte) (int, error) {
	if w.observe != nil {
		w.observe(string(p))
	}
	return w.buf.Write(p)
}

func captureRunExtendedStdout(t *testing.T, observe func(string), f func() error) (string, error) {
	t.Helper()

	oldStdout := os.Stdout
	r, w, err := os.Pipe()
	require.NoError(t, err)

	os.Stdout = w

	var buf bytes.Buffer
	readDone := make(chan error, 1)
	go func() {
		_, copyErr := io.Copy(&runExtendedObservedWriter{buf: &buf, observe: observe}, r)
		if copyErr == io.ErrClosedPipe {
			copyErr = nil
		}
		readDone <- copyErr
	}()

	var runErr error
	func() {
		defer func() {
			_ = w.Close()
			os.Stdout = oldStdout
		}()
		runErr = f()
	}()

	require.NoError(t, <-readDone)
	require.NoError(t, r.Close())
	return strings.TrimSpace(buf.String()), runErr
}

const (
	runExtendedActiveAuth = `{"selectedAuthType":"oauth-personal","account":"active@example.test"}`
	runExtendedBackupAuth = `{"selectedAuthType":"oauth-personal","account":"backup@example.test"}`
)

func runExtendedMockHandoffSuccess() {
	recordRunExtendedEvent("start:handoff-success")
	fmt.Println("mock-output: before-handoff")
	fmt.Println("Error: rate limit exceeded")
	recordRunExtendedEvent("rate_limit_emitted")

	line, err := bufio.NewReader(os.Stdin).ReadString('\n')
	if err != nil {
		recordRunExtendedEvent("stdin_closed_before_login")
		fmt.Println("mock-error: stdin closed before login")
		os.Exit(3)
	}
	command := strings.TrimSpace(line)
	recordRunExtendedEvent("login_received:" + command)
	if command != "/auth" {
		fmt.Println("mock-error: unexpected login command")
		os.Exit(4)
	}

	label := runExtendedAuthLabel(os.Getenv("MOCK_AUTH_PATH"))
	recordRunExtendedEvent("auth_label_after_login:" + label)
	switch label {
	case "backup":
	default:
		fmt.Println("mock-error: auth was not swapped before login")
		os.Exit(5)
	}

	fmt.Println("successfully authenticated")
	recordRunExtendedEvent("login_success_printed")
	waitForRunExtendedHelperFile("MOCK_LOGIN_RELEASE_FILE", "login_release_seen")
	fmt.Println("mock-output: after-handoff")
	recordRunExtendedEvent("after_handoff_printed")
	waitForRunExtendedHelperFile("MOCK_EXIT_RELEASE_FILE", "exit_release_seen")
	recordRunExtendedEvent("exit:0")
	os.Exit(0)
}

func runExtendedMockEarlyExit() {
	recordRunExtendedEvent("start:early-exit")
	fmt.Println("mock-output: early-before-exit")
	fmt.Println("Error: rate limit exceeded")
	recordRunExtendedEvent("rate_limit_emitted")
	recordRunExtendedEvent("exit:0_early")
	os.Exit(0)
}

func runExtendedMockStallLogin() {
	recordRunExtendedEvent("start:stall-login")
	fmt.Println("mock-output: stall-before-handoff")
	fmt.Println("Error: rate limit exceeded")
	recordRunExtendedEvent("rate_limit_emitted")

	line, err := bufio.NewReader(os.Stdin).ReadString('\n')
	if err != nil {
		recordRunExtendedEvent("stdin_closed_before_login")
		os.Exit(3)
	}
	command := strings.TrimSpace(line)
	recordRunExtendedEvent("login_received:" + command)
	fmt.Println("waiting for authentication")
	recordRunExtendedEvent("stalling_after_login")
	select {}
}

func recordRunExtendedEvent(event string) {
	path := os.Getenv("MOCK_EVENT_LOG")
	if path == "" {
		return
	}
	line := fmt.Sprintf("%s\t%s\n", time.Now().UTC().Format(time.RFC3339Nano), event)
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		return
	}
	defer file.Close()
	_, _ = file.WriteString(line)
}

func waitForRunExtendedHelperFile(envKey, event string) {
	path := os.Getenv(envKey)
	if path == "" {
		return
	}

	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()

	for {
		if _, err := os.Stat(path); err == nil {
			recordRunExtendedEvent(event)
			return
		}

		select {
		case <-deadline.C:
			recordRunExtendedEvent(event + ":timeout")
			os.Exit(6)
		case <-ticker.C:
		}
	}
}

func runExtendedAuthLabel(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return "missing"
	}
	switch {
	case strings.Contains(string(data), "backup@example.test"):
		return "backup"
	case strings.Contains(string(data), "active@example.test"):
		return "active"
	default:
		return "unknown"
	}
}

func waitForRunExtendedEvent(t *testing.T, path, want string, timeout time.Duration) {
	t.Helper()
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()

	for {
		data, err := os.ReadFile(path)
		if err == nil && strings.Contains(string(data), want) {
			return
		}
		if err != nil && !os.IsNotExist(err) {
			require.NoError(t, err)
		}

		select {
		case <-deadline.C:
			t.Fatalf("timed out waiting for mock event %q; events:\n%s", want, string(data))
		case <-ticker.C:
		}
	}
}
