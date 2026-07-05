package workflows

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/Dicklesworthstone/coding_agent_account_manager/internal/testutil"
	"github.com/stretchr/testify/require"
)

func TestDaemonSignals(t *testing.T) {
	h := testutil.NewExtendedHarness(t)
	defer h.Close()

	// 1. Setup
	h.StartStep("Setup", "Initialize environment")
	rootDir := h.TempDir
	homeDir := filepath.Join(rootDir, "home")
	xdgDataDir := filepath.Join(rootDir, "xdg-data")
	xdgConfigDir := filepath.Join(rootDir, "xdg-config")
	pidFile := filepath.Join(rootDir, "caam-daemon.pid")
	require.NoError(t, os.MkdirAll(homeDir, 0755))
	require.NoError(t, os.MkdirAll(xdgDataDir, 0755))
	require.NoError(t, os.MkdirAll(xdgConfigDir, 0755))

	// Create config file with initial settings
	configDir := filepath.Join(rootDir, "caam")
	require.NoError(t, os.MkdirAll(configDir, 0755))
	configPath := filepath.Join(configDir, "config.yaml")

	require.NoError(t, os.WriteFile(configPath, []byte(daemonSignalConfig(pidFile, false)), 0600))

	baseEnv := daemonSignalEnv(os.Environ(), homeDir, xdgDataDir, xdgConfigDir, configDir)
	daemonEnv := append([]string{}, baseEnv...)
	daemonEnv = append(daemonEnv, "GO_WANT_DAEMON_HELPER=1")
	cliEnv := append([]string{}, baseEnv...)
	cliEnv = append(cliEnv, "GO_WANT_CLI_HELPER=1")

	logPath := filepath.Join(rootDir, "daemon.log")

	h.EndStep("Setup")

	// 2. Start Daemon
	h.StartStep("Start", "Start daemon process")

	exe, err := os.Executable()
	require.NoError(t, err)

	cmd := exec.Command(exe, "-test.run=^TestDaemonHelper$")
	cmd.Env = daemonEnv
	// Run the daemon as the leader of its OWN process group so teardown can
	// signal the ENTIRE tree — the daemon AND any grandchild it (or a tool it
	// drives, e.g. a Codex plugin-clone helper) may spawn — not just the direct
	// child. Reaping only the direct child left grandchildren writing files
	// under the temp home, racing t.TempDir() RemoveAll and failing cleanup with
	// "directory not empty" (issue #48).
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	// Redirect stdout/stderr to a file we can read
	logFile, err := os.Create(logPath)
	require.NoError(t, err)
	defer logFile.Close()
	cmd.Stdout = logFile
	cmd.Stderr = logFile

	err = cmd.Start()
	require.NoError(t, err)

	waitCh := make(chan error, 1)
	go func() {
		waitCh <- cmd.Wait()
	}()
	daemonExited := false

	var stopOnce sync.Once
	pgid := cmd.Process.Pid // process-group id, because the child is the group leader
	signalGroup := func(sig syscall.Signal) {
		if pgid > 0 {
			_ = syscall.Kill(-pgid, sig)
			return
		}
		if cmd.Process != nil {
			_ = cmd.Process.Signal(sig)
		}
	}

	// stopDaemon deterministically tears the daemon's whole process group down.
	// It is idempotent and registered as a t.Cleanup so it runs before the
	// temp-home cleanup even if the test fails early.
	stopDaemon := func() {
		stopOnce.Do(func() {
			if daemonExited || cmd.Process == nil {
				return
			}
			signalGroup(syscall.SIGTERM)
			select {
			case <-waitCh:
				daemonExited = true
			case <-time.After(3 * time.Second):
				signalGroup(syscall.SIGKILL)
				select {
				case <-waitCh:
					daemonExited = true
				case <-time.After(3 * time.Second):
					t.Log("timed out waiting for daemon cleanup after SIGKILL")
				}
			}
		})
	}
	t.Cleanup(stopDaemon)

	daemonPID := waitForDaemonSignalPID(t, pidFile, logPath, logFile, waitCh, &daemonExited)
	require.Equal(t, cmd.Process.Pid, daemonPID, "PID file must target the isolated daemon subprocess")

	proc, err := os.FindProcess(daemonPID)
	require.NoError(t, err)
	require.NoError(t, proc.Signal(syscall.Signal(0)), "PID from file should be running")

	h.LogInfo("PID check", "cmd_pid", cmd.Process.Pid, "file_pid", daemonPID)

	h.EndStep("Start")

	// 3. Reload (SIGHUP)
	h.StartStep("Reload", "Send SIGHUP via CLI and verify")

	require.NoError(t, os.WriteFile(configPath, []byte(daemonSignalConfig(pidFile, true)), 0600))
	require.Equal(t, daemonPID, readDaemonSignalPID(t, pidFile), "config rewrite must preserve isolated PID file")

	reloadOutput := runDaemonSignalCLI(t, cliEnv, "reload", "--pid-file", pidFile)
	require.Contains(t, reloadOutput, fmt.Sprintf("Sent reload signal to PID %d", daemonPID))

	logs := waitForDaemonSignalLogs(t, logPath, logFile, waitCh, &daemonExited,
		"Received SIGHUP, reloading config...",
		"Config reloaded (runtime settings applied)",
	)
	require.Equal(t, daemonPID, readDaemonSignalPID(t, pidFile), "daemon reload must keep using isolated PID file")

	h.LogInfo("Daemon logs", "content", string(logs))

	select {
	case err := <-waitCh:
		daemonExited = true
		t.Fatalf("daemon exited after product reload command: %v\nlogs:\n%s", err, logs)
	default:
	}
	require.NoError(t, proc.Signal(syscall.Signal(0)), "daemon should still be running after reload")

	h.EndStep("Reload")

	// 4. Stop
	h.StartStep("Stop", "Stop daemon via CLI")

	stopOutput := runDaemonSignalCLI(t, cliEnv, "daemon", "stop")
	require.Contains(t, stopOutput, fmt.Sprintf("Stopping daemon (pid %d)", daemonPID))

	select {
	case err := <-waitCh:
		daemonExited = true
		require.NoError(t, err, "daemon helper should exit cleanly after product stop command")
	case <-time.After(5 * time.Second):
		logs := readDaemonSignalLogs(t, logPath, logFile)
		t.Fatalf("daemon did not exit after product stop command\nstop output:\n%s\nlogs:\n%s", stopOutput, logs)
	}

	logs = readDaemonSignalLogs(t, logPath, logFile)
	require.Contains(t, logs, "shutting down...")
	require.Contains(t, logs, "Daemon stopped gracefully")

	// Product stop reaps the daemon leader; clean any remaining process-group
	// descendants before temp-directory cleanup runs.
	signalGroup(syscall.SIGTERM)
	time.Sleep(200 * time.Millisecond)
	signalGroup(syscall.SIGKILL)
	stopOnce.Do(func() {})

	h.EndStep("Stop")
}

func daemonSignalConfig(pidFile string, verbose bool) string {
	return fmt.Sprintf(`
runtime:
  reload_on_sighup: true
  pid_file: true
  pid_file_path: %s
daemon:
  verbose: %t
`, pidFile, verbose)
}

func daemonSignalEnv(base []string, homeDir, xdgDataDir, xdgConfigDir, configDir string) []string {
	env := withoutDaemonSignalEnv(base,
		"GO_WANT_DAEMON_HELPER",
		"GO_WANT_CLI_HELPER",
		"HOME",
		"XDG_DATA_HOME",
		"XDG_CONFIG_HOME",
		"CAAM_HOME",
	)
	return append(env,
		fmt.Sprintf("HOME=%s", homeDir),
		fmt.Sprintf("XDG_DATA_HOME=%s", xdgDataDir),
		fmt.Sprintf("XDG_CONFIG_HOME=%s", xdgConfigDir),
		fmt.Sprintf("CAAM_HOME=%s", configDir),
	)
}

func withoutDaemonSignalEnv(env []string, keys ...string) []string {
	removed := make(map[string]struct{}, len(keys))
	for _, key := range keys {
		removed[key] = struct{}{}
	}

	filtered := make([]string, 0, len(env))
	for _, entry := range env {
		key, _, ok := strings.Cut(entry, "=")
		if !ok {
			key = entry
		}
		if _, remove := removed[key]; remove {
			continue
		}
		filtered = append(filtered, entry)
	}
	return filtered
}

func runDaemonSignalCLI(t *testing.T, env []string, args ...string) string {
	t.Helper()

	exe, err := os.Executable()
	require.NoError(t, err)

	helperArgs := append([]string{"-test.run=^TestCLIHelper$", "--"}, args...)
	cmd := exec.Command(exe, helperArgs...)
	cmd.Env = env

	output, err := cmd.CombinedOutput()
	require.NoError(t, err, "caam %s failed:\n%s", strings.Join(args, " "), string(output))
	return string(output)
}

func waitForDaemonSignalPID(t *testing.T, pidFile, logPath string, logFile *os.File, waitCh <-chan error, daemonExited *bool) int {
	t.Helper()

	var lastErr error
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		select {
		case err := <-waitCh:
			*daemonExited = true
			logs := readDaemonSignalLogs(t, logPath, logFile)
			t.Fatalf("daemon exited before writing a readable PID file: %v\nlogs:\n%s", err, logs)
		default:
		}

		pid, err := parseDaemonSignalPID(pidFile)
		if err == nil {
			return pid
		}
		lastErr = err
		time.Sleep(100 * time.Millisecond)
	}

	logs := readDaemonSignalLogs(t, logPath, logFile)
	t.Fatalf("PID file %s was not readable within timeout: %v\nlogs:\n%s", pidFile, lastErr, logs)
	return 0
}

func readDaemonSignalPID(t *testing.T, pidFile string) int {
	t.Helper()

	pid, err := parseDaemonSignalPID(pidFile)
	require.NoError(t, err)
	return pid
}

func parseDaemonSignalPID(pidFile string) (int, error) {
	content, err := os.ReadFile(pidFile)
	if err != nil {
		return 0, err
	}

	pid, err := strconv.Atoi(strings.TrimSpace(string(content)))
	if err != nil {
		return 0, fmt.Errorf("parse pid file %s: %w", pidFile, err)
	}
	if pid <= 0 {
		return 0, fmt.Errorf("pid file %s contained invalid pid %d", pidFile, pid)
	}
	return pid, nil
}

func waitForDaemonSignalLogs(t *testing.T, logPath string, logFile *os.File, waitCh <-chan error, daemonExited *bool, substrings ...string) string {
	t.Helper()

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		select {
		case err := <-waitCh:
			*daemonExited = true
			logs := readDaemonSignalLogs(t, logPath, logFile)
			t.Fatalf("daemon exited while waiting for log assertions: %v\nlogs:\n%s", err, logs)
		default:
		}

		logs := readDaemonSignalLogs(t, logPath, logFile)
		foundAll := true
		for _, substring := range substrings {
			if !strings.Contains(logs, substring) {
				foundAll = false
				break
			}
		}
		if foundAll {
			return logs
		}
		time.Sleep(100 * time.Millisecond)
	}

	logs := readDaemonSignalLogs(t, logPath, logFile)
	t.Fatalf("daemon logs did not contain expected substrings %q\nlogs:\n%s", substrings, logs)
	return ""
}

func readDaemonSignalLogs(t *testing.T, logPath string, logFile *os.File) string {
	t.Helper()

	require.NoError(t, logFile.Sync())
	logs, err := os.ReadFile(logPath)
	require.NoError(t, err)
	return string(logs)
}
