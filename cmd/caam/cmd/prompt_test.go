package cmd

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

func TestConfirmPromptGumAbsentFallsBackToPlain(t *testing.T) {
	restore := stubPromptGlobals(t)
	defer restore()

	promptIsTerminal = func(int) bool { return true }
	promptLookPath = func(string) (string, error) {
		return "", exec.ErrNotFound
	}
	promptRunGumConfirm = func(context.Context, string, string, bool, io.Reader, io.Writer, io.Writer) (bool, error) {
		t.Fatal("gum should not run when it is absent")
		return false, nil
	}

	var errOut bytes.Buffer
	ok, err := confirmPrompt(context.Background(), confirmPromptOptions{
		Prompt: "Delete profile?",
		In:     strings.NewReader("y\n"),
		Out:    io.Discard,
		Err:    &errOut,
	})
	if err != nil {
		t.Fatalf("confirmPrompt returned error: %v", err)
	}
	if !ok {
		t.Fatal("confirmPrompt returned false, want true")
	}
	if got, want := errOut.String(), "Delete profile? [y/N]: "; got != want {
		t.Fatalf("plain prompt = %q, want %q", got, want)
	}
}

func TestConfirmPromptCIFailsClosedWithoutExplicitPlainMode(t *testing.T) {
	restore := stubPromptGlobals(t)
	defer restore()
	if err := os.Setenv("CI", "true"); err != nil {
		t.Fatal(err)
	}

	promptIsTerminal = func(int) bool { return true }
	promptLookPath = func(string) (string, error) {
		return "/usr/bin/gum", nil
	}
	promptRunGumConfirm = func(context.Context, string, string, bool, io.Reader, io.Writer, io.Writer) (bool, error) {
		t.Fatal("gum should not run in CI")
		return false, nil
	}

	var errOut bytes.Buffer
	ok, err := confirmPrompt(context.Background(), confirmPromptOptions{
		Prompt:     "Proceed anyway?",
		DefaultYes: true,
		In:         strings.NewReader("\n"),
		Out:        io.Discard,
		Err:        &errOut,
	})
	if !errors.Is(err, errNonInteractivePrompt) {
		t.Fatalf("confirmPrompt error = %v, want %v", err, errNonInteractivePrompt)
	}
	if ok {
		t.Fatal("CI non-interactive prompt confirmed without explicit plain mode")
	}
	if got := errOut.String(); got != "" {
		t.Fatalf("CI fail-closed prompt wrote %q, want empty", got)
	}
}

func TestConfirmPromptNoColorFallsBackEvenWhenGumExists(t *testing.T) {
	restore := stubPromptGlobals(t)
	defer restore()
	if err := os.Setenv("NO_COLOR", "1"); err != nil {
		t.Fatal(err)
	}

	promptIsTerminal = func(int) bool { return true }
	promptLookPath = func(string) (string, error) {
		return "/usr/bin/gum", nil
	}
	promptRunGumConfirm = func(context.Context, string, string, bool, io.Reader, io.Writer, io.Writer) (bool, error) {
		t.Fatal("gum should not run when NO_COLOR is set")
		return false, nil
	}

	var errOut bytes.Buffer
	ok, err := confirmPrompt(context.Background(), confirmPromptOptions{
		Prompt: "Proceed anyway?",
		In:     strings.NewReader("n\n"),
		Out:    io.Discard,
		Err:    &errOut,
	})
	if err != nil {
		t.Fatalf("confirmPrompt returned error: %v", err)
	}
	if ok {
		t.Fatal("confirmPrompt returned true, want false")
	}
	if got, want := errOut.String(), "Proceed anyway? [y/N]: "; got != want {
		t.Fatalf("plain prompt = %q, want %q", got, want)
	}
}

func TestConfirmPromptUsesGumWhenInteractiveAndAvailable(t *testing.T) {
	restore := stubPromptGlobals(t)
	defer restore()

	promptIsTerminal = func(int) bool { return true }
	promptLookPath = func(string) (string, error) {
		return "/usr/bin/gum", nil
	}

	called := false
	promptRunGumConfirm = func(_ context.Context, gumPath string, prompt string, defaultYes bool, _ io.Reader, _ io.Writer, _ io.Writer) (bool, error) {
		called = true
		if gumPath != "/usr/bin/gum" {
			t.Fatalf("gumPath = %q, want /usr/bin/gum", gumPath)
		}
		if prompt != "Proceed?" {
			t.Fatalf("prompt = %q, want Proceed?", prompt)
		}
		if defaultYes {
			t.Fatal("defaultYes = true, want false")
		}
		return true, nil
	}

	ok, err := confirmPrompt(context.Background(), confirmPromptOptions{
		Prompt: "Proceed?",
		In:     os.Stdin,
		Out:    io.Discard,
		Err:    os.Stderr,
	})
	if err != nil {
		t.Fatalf("confirmPrompt returned error: %v", err)
	}
	if !ok || !called {
		t.Fatalf("gum path not used: ok=%v called=%v", ok, called)
	}
}

func TestConfirmPromptCommandPathPlainFlag(t *testing.T) {
	restore := stubPromptGlobals(t)
	defer restore()

	promptIsTerminal = func(int) bool { return true }
	promptLookPath = func(string) (string, error) {
		return "/usr/bin/gum", nil
	}
	promptRunGumConfirm = func(context.Context, string, string, bool, io.Reader, io.Writer, io.Writer) (bool, error) {
		t.Fatal("gum should not run with --plain")
		return false, nil
	}

	var errOut bytes.Buffer
	var confirmed bool
	cmd := &cobra.Command{
		Use: "test",
		RunE: func(cmd *cobra.Command, args []string) error {
			var err error
			confirmed, err = confirmPromptFromCommand(context.Background(), cmd, "Continue?", false)
			return err
		},
	}
	addPromptModeFlags(cmd)
	cmd.SetArgs([]string{"--plain"})
	cmd.SetIn(strings.NewReader("yes\n"))
	cmd.SetOut(io.Discard)
	cmd.SetErr(&errOut)

	if err := cmd.Execute(); err != nil {
		t.Fatalf("command returned error: %v", err)
	}
	if !confirmed {
		t.Fatal("confirmed = false, want true")
	}
	if got, want := errOut.String(), "Continue? [y/N]: "; got != want {
		t.Fatalf("command prompt output = %q, want %q", got, want)
	}
}

func TestConfirmPromptCommandPathNoGumFlag(t *testing.T) {
	restore := stubPromptGlobals(t)
	defer restore()

	promptIsTerminal = func(int) bool { return true }
	promptLookPath = func(string) (string, error) {
		return "/usr/bin/gum", nil
	}
	promptRunGumConfirm = func(context.Context, string, string, bool, io.Reader, io.Writer, io.Writer) (bool, error) {
		t.Fatal("gum should not run with --no-gum")
		return false, nil
	}

	var errOut bytes.Buffer
	var confirmed bool
	cmd := &cobra.Command{
		Use: "test",
		RunE: func(cmd *cobra.Command, args []string) error {
			var err error
			confirmed, err = confirmPromptFromCommand(context.Background(), cmd, "Continue?", false)
			return err
		},
	}
	addPromptModeFlags(cmd)
	cmd.SetArgs([]string{"--no-gum"})
	cmd.SetIn(strings.NewReader("yes\n"))
	cmd.SetOut(io.Discard)
	cmd.SetErr(&errOut)

	if err := cmd.Execute(); err != nil {
		t.Fatalf("command returned error: %v", err)
	}
	if !confirmed {
		t.Fatal("confirmed = false, want true")
	}
	if got, want := errOut.String(), "Continue? [y/N]: "; got != want {
		t.Fatalf("command prompt output = %q, want %q", got, want)
	}
}

func TestConfirmPromptCIPlainModeRequiresExplicitInput(t *testing.T) {
	restore := stubPromptGlobals(t)
	defer restore()
	if err := os.Setenv("CI", "true"); err != nil {
		t.Fatal(err)
	}

	promptIsTerminal = func(int) bool { return true }
	promptLookPath = func(string) (string, error) {
		return "/usr/bin/gum", nil
	}
	promptRunGumConfirm = func(context.Context, string, string, bool, io.Reader, io.Writer, io.Writer) (bool, error) {
		t.Fatal("gum should not run with --plain")
		return false, nil
	}

	var errOut bytes.Buffer
	ok, err := confirmPrompt(context.Background(), confirmPromptOptions{
		Prompt:     "Proceed anyway?",
		DefaultYes: true,
		In:         strings.NewReader("yes\n"),
		Out:        io.Discard,
		Err:        &errOut,
		Mode:       promptMode{Plain: true},
	})
	if err != nil {
		t.Fatalf("confirmPrompt returned error: %v", err)
	}
	if !ok {
		t.Fatal("explicit yes input should confirm in CI plain mode")
	}
	if got, want := errOut.String(), "Proceed anyway? [Y/n]: "; got != want {
		t.Fatalf("plain CI prompt = %q, want %q", got, want)
	}
}

func TestDestructiveCommandsExposePromptModeFlags(t *testing.T) {
	for _, cmd := range []*cobra.Command{deleteCmd, clearCmd, profileDeleteCmd, profileUnlockCmd, renameCmd} {
		if cmd.Flags().Lookup("plain") == nil {
			t.Fatalf("%s missing --plain flag", cmd.Use)
		}
		if cmd.Flags().Lookup("no-gum") == nil {
			t.Fatalf("%s missing --no-gum flag", cmd.Use)
		}
	}
}

func TestConfirmPromptGumErrorPropagates(t *testing.T) {
	restore := stubPromptGlobals(t)
	defer restore()

	promptIsTerminal = func(int) bool { return true }
	promptLookPath = func(string) (string, error) {
		return "/usr/bin/gum", nil
	}
	wantErr := errors.New("gum failed")
	promptRunGumConfirm = func(context.Context, string, string, bool, io.Reader, io.Writer, io.Writer) (bool, error) {
		return false, wantErr
	}

	_, err := confirmPrompt(context.Background(), confirmPromptOptions{
		Prompt: "Proceed?",
		In:     os.Stdin,
		Out:    io.Discard,
		Err:    os.Stderr,
	})
	if !errors.Is(err, wantErr) {
		t.Fatalf("error = %v, want %v", err, wantErr)
	}
}

func TestPlainConfirmPromptEOFDefaultYesDoesNotConfirm(t *testing.T) {
	var errOut bytes.Buffer
	ok, err := plainConfirmPrompt(context.Background(), strings.NewReader(""), &errOut, "Proceed?", true, true)
	if err != nil {
		t.Fatalf("plainConfirmPrompt returned error: %v", err)
	}
	if ok {
		t.Fatal("EOF with default yes confirmed")
	}
	if got, want := errOut.String(), "Proceed? [Y/n]: "; got != want {
		t.Fatalf("prompt output = %q, want %q", got, want)
	}
}

func TestPlainConfirmPromptHonorsContextCancellation(t *testing.T) {
	release := make(chan struct{})
	reader := blockingPromptReader{release: release}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := plainConfirmPrompt(ctx, reader, io.Discard, "Proceed?", false, true)
	close(release)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("plainConfirmPrompt error = %v, want context.Canceled", err)
	}
}

func TestPlainConfirmPromptDoesNotOverreadRepeatedPipedAnswers(t *testing.T) {
	input := strings.NewReader("yes\nno\n")

	first, err := plainConfirmPrompt(context.Background(), input, io.Discard, "First?", false, false)
	if err != nil {
		t.Fatalf("first prompt returned error: %v", err)
	}
	second, err := plainConfirmPrompt(context.Background(), input, io.Discard, "Second?", true, false)
	if err != nil {
		t.Fatalf("second prompt returned error: %v", err)
	}

	if !first {
		t.Fatal("first prompt = false, want true")
	}
	if second {
		t.Fatal("second prompt = true, want false")
	}
}

type blockingPromptReader struct {
	release <-chan struct{}
}

func (r blockingPromptReader) Read([]byte) (int, error) {
	<-r.release
	return 0, io.EOF
}

func stubPromptGlobals(t *testing.T) func() {
	t.Helper()

	oldIsTerminal := promptIsTerminal
	oldLookPath := promptLookPath
	oldRunGumConfirm := promptRunGumConfirm
	oldCI, hadCI := os.LookupEnv("CI")
	oldNoColor, hadNoColor := os.LookupEnv("NO_COLOR")
	if err := os.Unsetenv("CI"); err != nil {
		t.Fatal(err)
	}
	if err := os.Unsetenv("NO_COLOR"); err != nil {
		t.Fatal(err)
	}

	return func() {
		promptIsTerminal = oldIsTerminal
		promptLookPath = oldLookPath
		promptRunGumConfirm = oldRunGumConfirm
		restoreEnv("CI", oldCI, hadCI)
		restoreEnv("NO_COLOR", oldNoColor, hadNoColor)
	}
}

func TestRunGumConfirmExitOneIsNegative(t *testing.T) {
	tmpDir := t.TempDir()
	gumPath := filepath.Join(tmpDir, "gum")
	if err := os.WriteFile(gumPath, []byte("#!/bin/sh\nexit 1\n"), 0755); err != nil {
		t.Fatalf("write fake gum: %v", err)
	}

	ctx := context.Background()
	ok, err := runGumConfirm(ctx, gumPath, "Proceed?", false, strings.NewReader(""), io.Discard, io.Discard)
	if err != nil {
		t.Fatalf("runGumConfirm returned error: %v", err)
	}
	if ok {
		t.Fatal("exit 1 should be treated as a negative confirmation")
	}
}

func TestRunGumConfirmPassesExplicitDefaultAndPromptSeparator(t *testing.T) {
	tmpDir := t.TempDir()
	gumPath := filepath.Join(tmpDir, "gum")
	argsPath := filepath.Join(tmpDir, "args.txt")
	script := "#!/bin/sh\nprintf '%s\\n' \"$@\" > \"$GUM_ARGS_FILE\"\nexit 0\n"
	if err := os.WriteFile(gumPath, []byte(script), 0755); err != nil {
		t.Fatalf("write fake gum: %v", err)
	}
	t.Setenv("GUM_ARGS_FILE", argsPath)

	ok, err := runGumConfirm(context.Background(), gumPath, "Really delete?", false, strings.NewReader(""), io.Discard, io.Discard)
	if err != nil {
		t.Fatalf("runGumConfirm returned error: %v", err)
	}
	if !ok {
		t.Fatal("runGumConfirm returned false, want true")
	}

	data, err := os.ReadFile(argsPath)
	if err != nil {
		t.Fatalf("read gum args: %v", err)
	}
	if got, want := string(data), "confirm\n--default=false\n--\nReally delete?\n"; got != want {
		t.Fatalf("gum args = %q, want %q", got, want)
	}
}

func restoreEnv(key, value string, hadValue bool) {
	if hadValue {
		_ = os.Setenv(key, value)
		return
	}
	_ = os.Unsetenv(key)
}
