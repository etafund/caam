package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"strings"

	"github.com/spf13/cobra"
	"golang.org/x/term"
)

var (
	promptIsTerminal    = term.IsTerminal
	promptLookPath      = exec.LookPath
	promptRunGumConfirm = runGumConfirm
)

var errNonInteractivePrompt = errors.New("confirmation requires an interactive terminal or explicit input; pass --plain with yes/no on stdin, or use --yes/--force to skip confirmation when safe")

type promptMode struct {
	Plain   bool
	NoGum   bool
	Machine bool
}

type confirmPromptOptions struct {
	Prompt     string
	DefaultYes bool
	In         io.Reader
	Out        io.Writer
	Err        io.Writer
	Mode       promptMode
}

func addPromptModeFlags(cmd *cobra.Command) {
	if cmd.Flags().Lookup("plain") == nil {
		cmd.Flags().Bool("plain", false, "use plain text prompts and stable script-friendly output")
	}
	if cmd.Flags().Lookup("no-gum") == nil {
		cmd.Flags().Bool("no-gum", false, "disable gum-enhanced prompts")
	}
}

func promptModeFromCommand(cmd *cobra.Command) promptMode {
	plain := false
	noGum := false
	if flag := cmd.Flags().Lookup("plain"); flag != nil {
		plain, _ = cmd.Flags().GetBool("plain")
	}
	if flag := cmd.Flags().Lookup("no-gum"); flag != nil {
		noGum, _ = cmd.Flags().GetBool("no-gum")
	}
	machine := false
	if flag := cmd.Flags().Lookup("json"); flag != nil {
		machine, _ = cmd.Flags().GetBool("json")
	}
	return promptMode{
		Plain:   plain,
		NoGum:   noGum,
		Machine: machine,
	}
}

func confirmPromptFromCommand(ctx context.Context, cmd *cobra.Command, prompt string, defaultYes bool) (bool, error) {
	return confirmPrompt(ctx, confirmPromptOptions{
		Prompt:     prompt,
		DefaultYes: defaultYes,
		In:         cmd.InOrStdin(),
		Out:        cmd.OutOrStdout(),
		Err:        cmd.ErrOrStderr(),
		Mode:       promptModeFromCommand(cmd),
	})
}

func confirmPrompt(ctx context.Context, opts confirmPromptOptions) (bool, error) {
	if opts.Prompt == "" {
		opts.Prompt = "Proceed?"
	}
	if opts.In == nil {
		opts.In = os.Stdin
	}
	if opts.Out == nil {
		opts.Out = os.Stdout
	}
	if opts.Err == nil {
		opts.Err = os.Stderr
	}

	mode := "plain"
	interactive := promptStreamsInteractive(opts.In, opts.Err)
	if opts.Mode.Machine {
		slog.Debug("prompt confirm", "mode", mode, "prompt", opts.Prompt, "result", false, "error", errNonInteractivePrompt.Error())
		return false, errNonInteractivePrompt
	}
	if !interactive && isCIEnvironment() && !opts.Mode.Plain && !opts.Mode.NoGum {
		slog.Debug("prompt confirm", "mode", mode, "prompt", opts.Prompt, "result", false, "error", errNonInteractivePrompt.Error())
		return false, errNonInteractivePrompt
	}
	if !interactive && !opts.Mode.Plain && !opts.Mode.NoGum && !promptReaderMayHaveInput(opts.In) {
		slog.Debug("prompt confirm", "mode", mode, "prompt", opts.Prompt, "result", false, "error", errNonInteractivePrompt.Error())
		return false, errNonInteractivePrompt
	}
	if gumPath, ok := selectGumPrompt(opts.Mode, opts.In, opts.Err); ok {
		mode = "gum"
		ok, err := promptRunGumConfirm(ctx, gumPath, opts.Prompt, opts.DefaultYes, opts.In, opts.Err, opts.Err)
		slog.Debug("prompt confirm", "mode", mode, "prompt", opts.Prompt, "result", ok, "error", errorString(err))
		return ok, err
	}

	ok, err := plainConfirmPrompt(ctx, opts.In, opts.Err, opts.Prompt, opts.DefaultYes, interactive)
	slog.Debug("prompt confirm", "mode", mode, "prompt", opts.Prompt, "result", ok, "error", errorString(err))
	return ok, err
}

func selectGumPrompt(mode promptMode, in io.Reader, uiOut io.Writer) (string, bool) {
	if mode.Plain || mode.NoGum || mode.Machine {
		return "", false
	}
	if isCIEnvironment() || hasNoColor() || isDumbTerminal() || hasNoTUI() {
		return "", false
	}
	if !promptStreamsInteractive(in, uiOut) {
		return "", false
	}
	path, err := promptLookPath("gum")
	return path, err == nil
}

func promptStreamsInteractive(in io.Reader, uiOut io.Writer) bool {
	inFile, ok := in.(*os.File)
	if !ok || inFile == nil {
		return false
	}
	outFile, ok := uiOut.(*os.File)
	if !ok || outFile == nil {
		return false
	}
	return promptIsTerminal(int(inFile.Fd())) && promptIsTerminal(int(outFile.Fd()))
}

func promptReaderMayHaveInput(r io.Reader) bool {
	type lenReader interface {
		Len() int
	}
	if lr, ok := r.(lenReader); ok {
		return lr.Len() > 0
	}
	return true
}

func isCIEnvironment() bool {
	return envTruthy("CI")
}

func envTruthy(key string) bool {
	value := strings.TrimSpace(strings.ToLower(os.Getenv(key)))
	return value != "" && value != "0" && value != "false"
}

func isDumbTerminal() bool {
	return strings.EqualFold(strings.TrimSpace(os.Getenv("TERM")), "dumb")
}

func hasNoColor() bool {
	value, ok := os.LookupEnv("NO_COLOR")
	return ok && value != ""
}

func hasNoTUI() bool {
	return envTruthy("CAAM_NO_TUI") || envTruthy("NO_TUI")
}

func plainConfirmPrompt(ctx context.Context, r io.Reader, w io.Writer, prompt string, defaultYes bool, interactive bool) (bool, error) {
	if _, err := fmt.Fprintf(w, "%s %s: ", prompt, yesNoSuffix(defaultYes)); err != nil {
		return false, err
	}

	line, hadInput, err := readPromptLine(ctx, r)
	if err != nil {
		return false, err
	}
	answer := strings.ToLower(strings.TrimSpace(line))
	switch answer {
	case "y", "yes":
		return true, nil
	case "n", "no":
		return false, nil
	case "":
		if !hadInput {
			if !interactive {
				return false, errNonInteractivePrompt
			}
			return false, nil
		}
		if !interactive {
			return false, errNonInteractivePrompt
		}
		return defaultYes, nil
	default:
		return false, nil
	}
}

func readPromptLine(ctx context.Context, r io.Reader) (string, bool, error) {
	if ctx == nil {
		ctx = context.Background()
	}

	type result struct {
		line     string
		hadInput bool
		err      error
	}
	ch := make(chan result, 1)
	go func() {
		var b strings.Builder
		var one [1]byte
		hadInput := false
		for {
			n, err := r.Read(one[:])
			if n > 0 {
				hadInput = true
				if one[0] == '\n' {
					ch <- result{line: b.String(), hadInput: hadInput}
					return
				}
				b.WriteByte(one[0])
			}
			if err != nil {
				if errors.Is(err, io.EOF) {
					ch <- result{line: b.String(), hadInput: hadInput}
				} else {
					ch <- result{err: err}
				}
				return
			}
		}
	}()

	select {
	case <-ctx.Done():
		return "", false, ctx.Err()
	case res := <-ch:
		return res.line, res.hadInput, res.err
	}
}

func yesNoSuffix(defaultYes bool) string {
	if defaultYes {
		return "[Y/n]"
	}
	return "[y/N]"
}

func runGumConfirm(ctx context.Context, gumPath string, prompt string, defaultYes bool, stdin io.Reader, stdout, stderr io.Writer) (bool, error) {
	if gumPath == "" {
		gumPath = "gum"
	}
	args := []string{"confirm", fmt.Sprintf("--default=%t", defaultYes), "--", prompt}

	cmd := exec.CommandContext(ctx, gumPath, args...)
	cmd.Stdin = stdin
	cmd.Stdout = stdout
	cmd.Stderr = stderr

	err := cmd.Run()
	if err == nil {
		return true, nil
	}

	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 {
		return false, nil
	}
	return false, err
}

func errorString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
