package cmd

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestShallowSpawnBinaryE2E exercises the REAL syscall.Exec path that the
// in-process tests fake: it builds the actual caam binary, stages a real HOME +
// vault via env vars, creates a shallow claude profile, then spawns an env-dump
// command under it. This proves the real exec path SETS HOME/SHALLOW_PROFILE and
// STRIPS the repointing/override vars end-to-end (no mocks).
func TestShallowSpawnBinaryE2E(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping binary E2E in -short mode")
	}
	if runtime.GOOS == "windows" {
		t.Skip("shallow-spawn execs via syscall.Exec (Unix-only)")
	}

	// Resolve the repo root from this test file's location (…/cmd/caam/cmd).
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatalf("runtime.Caller failed")
	}
	repoRoot := filepath.Clean(filepath.Join(filepath.Dir(thisFile), "..", "..", ".."))
	if _, err := os.Stat(filepath.Join(repoRoot, "go.mod")); err != nil {
		t.Fatalf("could not locate repo root (go.mod) from %s: %v", thisFile, err)
	}

	// Build the real binary.
	bin := filepath.Join(t.TempDir(), "caam")
	buildCmd := exec.Command("go", "build", "-o", bin, "./cmd/caam")
	buildCmd.Dir = repoRoot
	if out, err := buildCmd.CombinedOutput(); err != nil {
		t.Fatalf("go build caam failed: %v\n%s", err, out)
	}

	// Pick an env-dump command available on this machine.
	envBin := ""
	for _, cand := range []string{"/usr/bin/env", "env", "printenv"} {
		if filepath.IsAbs(cand) {
			if _, err := os.Stat(cand); err == nil {
				envBin = cand
				break
			}
			continue
		}
		if p, err := exec.LookPath(cand); err == nil {
			envBin = p
			break
		}
	}
	if envBin == "" {
		t.Skip("no env/printenv binary available for the E2E env dump")
	}

	// Stage a real HOME with ~/.claude/.credentials.json and a CAAM_HOME vault
	// holding a claude profile (mirrors how shallowEnv stages things, but for the
	// REAL binary via env vars rather than the in-process vault pointer).
	realHome := t.TempDir()
	claudeDir := filepath.Join(realHome, ".claude")
	if err := os.MkdirAll(claudeDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(claudeDir, ".credentials.json"), []byte(`{"real":"home-creds"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(realHome, ".claude.json"), []byte(`{"x":1}`), 0o600); err != nil {
		t.Fatal(err)
	}

	caamHome := t.TempDir()
	base := filepath.Join(t.TempDir(), "orch-homes")
	vaultDir := filepath.Join(caamHome, "data", "vault", "claude", "alice")
	if err := os.MkdirAll(vaultDir, 0o700); err != nil {
		t.Fatal(err)
	}
	credBody := `{"claudeAiOauth":{"accessToken":"vaulted-alice"}}`
	if err := os.WriteFile(filepath.Join(vaultDir, ".credentials.json"), []byte(credBody), 0o600); err != nil {
		t.Fatal(err)
	}

	// Parent env carries leaky vars the spawn path MUST strip.
	childEnv := append(os.Environ(),
		"HOME="+realHome,
		"CAAM_HOME="+caamHome,
		"CAAM_SHALLOW_HOMES_DIR="+base,
		"CLAUDE_CONFIG_DIR=/leak",
		"OPENAI_API_KEY=leak",
	)

	run := func(args ...string) (string, string, error) {
		var stdout, stderr bytes.Buffer
		c := exec.Command(bin, args...)
		c.Env = childEnv
		c.Stdout = &stdout
		c.Stderr = &stderr
		err := c.Run()
		return stdout.String(), stderr.String(), err
	}

	// Create the shallow profile from the vault.
	if out, errOut, err := run("shallow-profile", "create", "alice", "--from-vault", "claude/alice", "--json"); err != nil {
		t.Fatalf("create failed: %v\nstdout=%s\nstderr=%s", err, out, errOut)
	}
	wantHome := filepath.Join(base, "alice")
	if _, err := os.Stat(filepath.Join(wantHome, ".claude", ".credentials.json")); err != nil {
		t.Fatalf("expected shallow credential file: %v", err)
	}

	// Spawn the env dump under the shallow profile via the REAL exec path.
	stdout, stderr, err := run("shallow-spawn", "alice", "--", envBin)
	if err != nil {
		t.Fatalf("shallow-spawn env dump failed: %v\nstdout=%s\nstderr=%s", err, stdout, stderr)
	}

	envMap := map[string]string{}
	for _, line := range strings.Split(stdout, "\n") {
		if idx := strings.IndexByte(line, '='); idx > 0 {
			envMap[line[:idx]] = line[idx+1:]
		}
	}

	if got := envMap["HOME"]; got != wantHome {
		t.Fatalf("HOME=%q, want %q\nfull env:\n%s", got, wantHome, stdout)
	}
	if got := envMap["SHALLOW_PROFILE"]; got != "alice" {
		t.Fatalf("SHALLOW_PROFILE=%q, want alice\nfull env:\n%s", got, stdout)
	}
	if _, present := envMap["CLAUDE_CONFIG_DIR"]; present {
		t.Fatalf("CLAUDE_CONFIG_DIR leaked into shallow env: %q\nfull env:\n%s", envMap["CLAUDE_CONFIG_DIR"], stdout)
	}
	if _, present := envMap["OPENAI_API_KEY"]; present {
		t.Fatalf("OPENAI_API_KEY leaked into shallow env: %q\nfull env:\n%s", envMap["OPENAI_API_KEY"], stdout)
	}
}
