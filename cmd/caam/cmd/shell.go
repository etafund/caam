package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/spf13/cobra"
)

// shellCmd is the parent command for shell integration.
var shellCmd = &cobra.Command{
	Use:   "shell",
	Short: "Shell integration commands",
	Long: `Commands for integrating caam with your shell.

Use 'caam shell-init' to set up automatic profile management.`,
}

// shellInitCmd outputs shell initialization code.
var shellInitCmd = &cobra.Command{
	Use:   "init [bash|zsh|fish]",
	Short: "Output shell initialization code",
	Long: `Outputs shell initialization code that wraps AI CLI commands.

Add this to your shell's rc file:

  # For bash (~/.bashrc):
  eval "$(caam shell init)"

  # For zsh (~/.zshrc):
  eval "$(caam shell init)"

  # For fish (~/.config/fish/config.fish):
  caam shell init --fish | source

This creates wrapper functions for claude, codex, and gemini that:
- Automatically use the best available profile
- Handle rate limits transparently
- Record usage for analytics

After setup, just use the tools normally:
  claude "explain this code"
  codex "write tests"
  gemini "summarize this file"

Rate limits are handled automatically!`,
	Args: cobra.MaximumNArgs(1),
	RunE: runShellInit,
}

func init() {
	addShellInitFlags(shellInitCmd)
	shellCmd.AddCommand(shellInitCmd)

	shellInitCompatCmd := &cobra.Command{
		Use:   "shell-init [bash|zsh|fish]",
		Short: shellInitCmd.Short,
		Long:  shellInitCmd.Long,
		Args:  shellInitCmd.Args,
		RunE:  runShellInit,
	}
	addShellInitFlags(shellInitCompatCmd)
	rootCmd.AddCommand(shellInitCompatCmd)

	rootCmd.AddCommand(shellCmd)
}

func addShellInitFlags(cmd *cobra.Command) {
	cmd.Flags().Bool("fish", false, "output fish shell syntax")
	cmd.Flags().Bool("bash", false, "output bash syntax (default)")
	cmd.Flags().Bool("zsh", false, "output zsh syntax")
	cmd.Flags().Bool("posix", false, "output POSIX shell syntax")
	cmd.Flags().Bool("no-wrap", false, "disable tool wrapping (only completions)")
	cmd.Flags().String("tools", "claude,codex,gemini", "comma-separated list of tools to wrap")
	cmd.Flags().Bool("install", false, "append shell integration to the detected shell rc file")
	cmd.Flags().String("rc-file", "", "shell rc file to update with --install")
}

func runShellInit(cmd *cobra.Command, args []string) error {
	fish, _ := cmd.Flags().GetBool("fish")
	bash, _ := cmd.Flags().GetBool("bash")
	zsh, _ := cmd.Flags().GetBool("zsh")
	posix, _ := cmd.Flags().GetBool("posix")
	noWrap, _ := cmd.Flags().GetBool("no-wrap")
	install, _ := cmd.Flags().GetBool("install")
	rcFile, _ := cmd.Flags().GetString("rc-file")
	toolsStr, _ := cmd.Flags().GetString("tools")

	// Parse tools list
	var tools []string
	for _, t := range strings.Split(toolsStr, ",") {
		t = strings.TrimSpace(t)
		if t != "" {
			tools = append(tools, t)
		}
	}

	// Detect shell if not specified.
	shell := detectShell()
	if len(args) == 1 {
		shell = strings.ToLower(args[0])
	}
	if fish {
		shell = "fish"
	} else if zsh {
		shell = "zsh"
	} else if bash {
		shell = "bash"
	} else if posix {
		shell = "sh"
	}
	switch shell {
	case "bash", "zsh", "fish", "sh":
	default:
		return fmt.Errorf("unsupported shell: %s (supported: bash, zsh, fish)", shell)
	}

	// Find caam binary path
	caamPath, err := findCaamPath()
	if err != nil {
		caamPath = "caam" // Fallback to PATH lookup
	}

	if install {
		if rcFile == "" {
			var err error
			rcFile, err = defaultShellRC(shell)
			if err != nil {
				return err
			}
		}
		line := shellInitInstallLine(caamPath, shell)
		changed, err := installShellInitLine(rcFile, line)
		if err != nil {
			return err
		}
		if changed {
			fmt.Fprintf(cmd.OutOrStdout(), "Added caam shell integration to %s\n", rcFile)
		} else {
			fmt.Fprintf(cmd.OutOrStdout(), "caam shell integration already present in %s\n", rcFile)
		}
		return nil
	}

	// Generate output
	var output string
	switch shell {
	case "fish":
		output = generateFishInit(caamPath, tools, noWrap)
	default:
		output = generateBashInit(caamPath, tools, noWrap)
	}

	fmt.Fprint(cmd.OutOrStdout(), output)
	return nil
}

func defaultShellRC(shell string) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("find home directory: %w", err)
	}
	switch shell {
	case "fish":
		return filepath.Join(home, ".config", "fish", "config.fish"), nil
	case "zsh":
		return filepath.Join(home, ".zshrc"), nil
	default:
		return filepath.Join(home, ".bashrc"), nil
	}
}

func shellInitInstallLine(caamPath, shell string) string {
	switch shell {
	case "fish":
		return fmt.Sprintf("%s shell init fish | source", fishQuote(caamPath))
	case "zsh":
		return fmt.Sprintf("eval \"$(%s shell init zsh)\"", shellQuote(caamPath))
	default:
		return fmt.Sprintf("eval \"$(%s shell init bash)\"", shellQuote(caamPath))
	}
}

func installShellInitLine(rcFile, line string) (bool, error) {
	var existing []byte
	if data, err := os.ReadFile(rcFile); err == nil {
		existing = data
		if strings.Contains(string(existing), line) {
			return false, nil
		}
	} else if !os.IsNotExist(err) {
		return false, fmt.Errorf("read %s: %w", rcFile, err)
	}

	if err := os.MkdirAll(filepath.Dir(rcFile), 0700); err != nil {
		return false, fmt.Errorf("create rc parent dir: %w", err)
	}
	f, err := os.OpenFile(rcFile, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		return false, fmt.Errorf("open %s: %w", rcFile, err)
	}

	prefix := ""
	if len(existing) > 0 && !strings.HasSuffix(string(existing), "\n") {
		prefix = "\n"
	}
	if _, err := fmt.Fprintf(f, "%s%s\n", prefix, line); err != nil {
		_ = f.Close()
		return false, fmt.Errorf("append %s: %w", rcFile, err)
	}
	if err := f.Close(); err != nil {
		return false, fmt.Errorf("close %s: %w", rcFile, err)
	}
	return true, nil
}

func detectShell() string {
	// Check SHELL environment variable
	shell := os.Getenv("SHELL")
	if shell != "" {
		base := filepath.Base(shell)
		switch base {
		case "fish":
			return "fish"
		case "zsh":
			return "zsh"
		case "bash":
			return "bash"
		}
	}

	// Check parent process on Unix
	if runtime.GOOS != "windows" {
		ppid := os.Getppid()
		procPath := fmt.Sprintf("/proc/%d/comm", ppid)
		if data, err := os.ReadFile(procPath); err == nil {
			name := strings.TrimSpace(string(data))
			switch name {
			case "fish":
				return "fish"
			case "zsh":
				return "zsh"
			case "bash":
				return "bash"
			}
		}
	}

	return "bash" // Default
}

func findCaamPath() (string, error) {
	// Try to find the current executable
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	return exe, nil
}

// shellQuote returns a properly shell-quoted string for bash/zsh.
// This prevents command injection when paths contain spaces or special characters.
func shellQuote(s string) string {
	// If the string contains no special characters, return as-is
	needsQuote := false
	for _, c := range s {
		if c == ' ' || c == '\'' || c == '"' || c == '\\' || c == '$' ||
			c == '`' || c == '!' || c == '*' || c == '?' || c == '[' ||
			c == ']' || c == '(' || c == ')' || c == '{' || c == '}' ||
			c == '|' || c == '&' || c == ';' || c == '<' || c == '>' ||
			c == '\n' || c == '\t' {
			needsQuote = true
			break
		}
	}
	if !needsQuote {
		return s
	}
	// Use single quotes and escape any embedded single quotes
	return "'" + strings.ReplaceAll(s, "'", "'\"'\"'") + "'"
}

func generateBashInit(caamPath string, tools []string, noWrap bool) string {
	var sb strings.Builder

	sb.WriteString("# caam shell integration\n")
	sb.WriteString("# Add to your ~/.bashrc or ~/.zshrc:\n")
	sb.WriteString("#   eval \"$(caam shell init)\"\n\n")

	// Quote the caam path for safe shell interpolation
	quotedPath := shellQuote(caamPath)

	// Tool wrapper functions
	if !noWrap {
		for _, tool := range tools {
			sb.WriteString(fmt.Sprintf(`# %s wrapper with automatic rate limit handling
%s() {
  %s run %s -- "$@"
}

`, tool, tool, quotedPath, tool))
		}
	}

	sb.WriteString(fmt.Sprintf(`# Prompt helper: include "$(_caam_prompt)" in PS1/RPROMPT if desired.
_caam_prompt() {
  %s status --format=prompt 2>/dev/null
}

# Auto-activate project associations when the current directory changes.
_caam_project_check() {
  %s project check --quiet 2>/dev/null || true
}

if [ -n "${ZSH_VERSION:-}" ]; then
  autoload -Uz add-zsh-hook 2>/dev/null || true
  if typeset -f add-zsh-hook >/dev/null 2>&1; then
    add-zsh-hook chpwd _caam_project_check
  elif [[ " ${chpwd_functions[*]} " != *" _caam_project_check "* ]]; then
    chpwd_functions+=(_caam_project_check)
  fi
else
  case ";${PROMPT_COMMAND:-};" in
    *";_caam_project_check;"*) ;;
    *) PROMPT_COMMAND="_caam_project_check${PROMPT_COMMAND:+;$PROMPT_COMMAND}" ;;
  esac
fi
_caam_project_check

`, quotedPath, quotedPath))

	// Completion for caam command
	sb.WriteString(fmt.Sprintf(`# caam command completion
_caam_completions() {
  local cur prev words cword
  _init_completion || return

  case "$prev" in
    caam)
      COMPREPLY=($(compgen -W "activate backup clear cooldown delete exec export import init login ls paths profile project refresh reload resume run shell status sync tui uninstall usage" -- "$cur"))
      return
      ;;
    activate|backup|clear|delete|ls|paths|status)
      COMPREPLY=($(compgen -W "claude codex gemini" -- "$cur"))
      return
      ;;
  esac

  # Profile completion for activate
  if [[ "${words[1]}" == "activate" && ${#words[@]} -ge 3 ]]; then
    local tool="${words[2]}"
    local profiles
    profiles=$(%s ls "$tool" 2>/dev/null | tr '\n' ' ')
    COMPREPLY=($(compgen -W "$profiles" -- "$cur"))
    return
  fi
}

complete -F _caam_completions caam
`, quotedPath))

	return sb.String()
}

// fishQuote returns a properly quoted string for fish shell.
func fishQuote(s string) string {
	// If the string contains no special characters, return as-is
	needsQuote := false
	for _, c := range s {
		if c == ' ' || c == '\'' || c == '"' || c == '\\' || c == '$' ||
			c == '(' || c == ')' || c == '{' || c == '}' || c == '[' ||
			c == ']' || c == '*' || c == '?' || c == '~' || c == '#' ||
			c == '|' || c == '&' || c == ';' || c == '<' || c == '>' ||
			c == '\n' || c == '\t' {
			needsQuote = true
			break
		}
	}
	if !needsQuote {
		return s
	}
	// Fish uses single quotes and escapes single quotes with \'
	return "'" + strings.ReplaceAll(s, "'", "\\'") + "'"
}

func generateFishInit(caamPath string, tools []string, noWrap bool) string {
	var sb strings.Builder

	sb.WriteString("# caam shell integration for fish\n")
	sb.WriteString("# Add to your ~/.config/fish/config.fish:\n")
	sb.WriteString("#   caam shell init --fish | source\n\n")

	// Quote the caam path for safe shell interpolation
	quotedPath := fishQuote(caamPath)

	// Tool wrapper functions
	if !noWrap {
		for _, tool := range tools {
			sb.WriteString(fmt.Sprintf(`# %s wrapper with automatic rate limit handling
function %s
  %s run %s -- $argv
end

`, tool, tool, quotedPath, tool))
		}
	}

	sb.WriteString(fmt.Sprintf(`# Prompt helper: call (_caam_prompt) from fish_prompt/right_prompt if desired.
function _caam_prompt
  %s status --format=prompt 2>/dev/null
end

# Auto-activate project associations when the current directory changes.
function __caam_project_check --on-variable PWD
  %s project check --quiet 2>/dev/null; or true
end
__caam_project_check

`, quotedPath, quotedPath))

	// Completion for caam command
	sb.WriteString(fmt.Sprintf(`# caam command completion
complete -c caam -f
complete -c caam -n "__fish_use_subcommand" -a "activate" -d "Activate a profile"
complete -c caam -n "__fish_use_subcommand" -a "backup" -d "Backup current auth"
complete -c caam -n "__fish_use_subcommand" -a "clear" -d "Clear auth files"
complete -c caam -n "__fish_use_subcommand" -a "cooldown" -d "Manage cooldowns"
complete -c caam -n "__fish_use_subcommand" -a "delete" -d "Delete a profile"
complete -c caam -n "__fish_use_subcommand" -a "ls" -d "List profiles"
complete -c caam -n "__fish_use_subcommand" -a "paths" -d "Show auth file paths"
complete -c caam -n "__fish_use_subcommand" -a "run" -d "Run with auto-switching"
complete -c caam -n "__fish_use_subcommand" -a "shell" -d "Shell integration"
complete -c caam -n "__fish_use_subcommand" -a "status" -d "Show current status"
complete -c caam -n "__fish_use_subcommand" -a "tui" -d "Open terminal UI"

# Tool completion
complete -c caam -n "__fish_seen_subcommand_from activate backup clear delete ls paths status" -a "claude codex gemini"

# Profile completion for activate
complete -c caam -n "__fish_seen_subcommand_from activate; and __fish_seen_subcommand_from claude codex gemini" -a "(%s ls (commandline -opc | tail -n1) 2>/dev/null)"
`, quotedPath))

	return sb.String()
}

// shellCompletionCmd generates completion scripts.
var shellCompletionCmd = &cobra.Command{
	Use:   "completion [bash|zsh|fish|powershell]",
	Short: "Generate completion script",
	Long: `Generate shell completion script.

To load completions:

Bash:
  source <(caam shell completion bash)

  # To load completions for each session, execute once:
  # Linux:
  caam shell completion bash > /etc/bash_completion.d/caam
  # macOS:
  caam shell completion bash > /usr/local/etc/bash_completion.d/caam

Zsh:
  # If shell completion is not already enabled, enable it:
  echo "autoload -U compinit; compinit" >> ~/.zshrc

  # To load completions for each session:
  caam shell completion zsh > "${fpath[1]}/_caam"

Fish:
  caam shell completion fish > ~/.config/fish/completions/caam.fish
`,
	ValidArgs: []string{"bash", "zsh", "fish", "powershell"},
	Args:      cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		switch args[0] {
		case "bash":
			return rootCmd.GenBashCompletion(os.Stdout)
		case "zsh":
			return rootCmd.GenZshCompletion(os.Stdout)
		case "fish":
			return rootCmd.GenFishCompletion(os.Stdout, true)
		case "powershell":
			return rootCmd.GenPowerShellCompletionWithDesc(os.Stdout)
		default:
			return fmt.Errorf("unknown shell: %s", args[0])
		}
	},
}

func init() {
	shellCmd.AddCommand(shellCompletionCmd)
}
