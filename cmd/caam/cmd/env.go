// Package cmd implements the CLI commands for caam.
package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/spf13/cobra"
)

var envCmd = &cobra.Command{
	Use:   "env <tool> <profile>",
	Short: "Print environment variables for shell eval",
	Long: `Prints environment variables that can be eval'd in your shell.

This allows you to set up the environment once and run multiple commands
with the same profile, instead of using 'caam exec' wrapper each time.

The output is valid shell syntax (bash/zsh compatible).

Examples:
  # Set up environment for codex work profile
  eval "$(caam env codex work)"
  codex "implement feature X"
  codex "add tests"

  # Set up environment for claude personal profile
  eval "$(caam env claude personal)"
  claude

  # Unset the variables when done
  eval "$(caam env codex work --unset)"

Use --unset to print unset commands instead of export commands.
Use --json for machine-readable output without shell quoting.
Use --export-prefix to change the export syntax (default: "export").`,
	Args: cobra.ExactArgs(2),
	RunE: runEnv,
}

func init() {
	rootCmd.AddCommand(envCmd)
	addEnvFlags(envCmd)
}

func addEnvFlags(cmd *cobra.Command) {
	cmd.Flags().Bool("unset", false, "print unset commands instead of export")
	cmd.Flags().Bool("json", false, "output environment as JSON")
	cmd.Flags().Bool("print", false, "print shell commands (default)")
	cmd.Flags().String("export-prefix", "export", "export syntax prefix (default: export)")
	cmd.Flags().Bool("fish", false, "use fish shell syntax")
}

type envOutputOptions struct {
	Unset        bool
	JSON         bool
	Print        bool
	ExportPrefix string
	Fish         bool
}

type envJSONOutput struct {
	Provider string            `json:"provider"`
	Profile  string            `json:"profile"`
	Env      map[string]string `json:"env,omitempty"`
	Unset    []string          `json:"unset,omitempty"`
}

func runEnv(cmd *cobra.Command, args []string) error {
	tool := strings.ToLower(args[0])
	name := args[1]

	envVars, err := profileEnvFor(tool, name)
	if err != nil {
		return err
	}

	opts, err := envOptionsFromFlags(cmd)
	if err != nil {
		return err
	}
	return writeEnvOutput(cmd.OutOrStdout(), tool, name, envVars, opts)
}

func profileEnvFor(tool, name string) (map[string]string, error) {
	prov, ok := registry.Get(tool)
	if !ok {
		return nil, fmt.Errorf("unknown provider: %s (supported: %s)", tool, supportedToolsList())
	}

	prof, err := profileStore.Load(tool, name)
	if err != nil {
		return nil, err
	}

	envVars, err := prov.Env(context.Background(), prof)
	if err != nil {
		return nil, fmt.Errorf("get environment: %w", err)
	}
	return envVars, nil
}

func envOptionsFromFlags(cmd *cobra.Command) (envOutputOptions, error) {
	unset, _ := cmd.Flags().GetBool("unset")
	jsonOut, _ := cmd.Flags().GetBool("json")
	printOut, _ := cmd.Flags().GetBool("print")
	exportPrefix, _ := cmd.Flags().GetString("export-prefix")
	fishMode, _ := cmd.Flags().GetBool("fish")

	if jsonOut && printOut {
		return envOutputOptions{}, fmt.Errorf("--json and --print are mutually exclusive")
	}
	if exportPrefix == "" {
		exportPrefix = "export"
	}

	return envOutputOptions{
		Unset:        unset,
		JSON:         jsonOut,
		Print:        printOut,
		ExportPrefix: exportPrefix,
		Fish:         fishMode,
	}, nil
}

func writeEnvOutput(w io.Writer, tool, name string, envVars map[string]string, opts envOutputOptions) error {
	keys := sortedEnvKeys(envVars)

	if opts.JSON {
		out := envJSONOutput{
			Provider: tool,
			Profile:  name,
		}
		if opts.Unset {
			out.Unset = keys
		} else {
			out.Env = envVars
		}
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		return enc.Encode(out)
	}

	for _, k := range keys {
		if opts.Unset {
			if opts.Fish {
				fmt.Fprintf(w, "set -e %s\n", k)
			} else {
				fmt.Fprintf(w, "unset %s\n", k)
			}
			continue
		}
		if opts.Fish {
			fmt.Fprintf(w, "set -gx %s %s\n", k, fishQuote(envVars[k]))
		} else {
			fmt.Fprintf(w, "%s %s=%s\n", opts.ExportPrefix, k, shellQuote(envVars[k]))
		}
	}

	if !opts.Unset {
		fmt.Fprintf(w, "# Environment set for %s profile %q\n", tool, name)
		fmt.Fprintf(w, "# Run 'eval \"$(caam env %s %s --unset)\"' to unset\n", tool, name)
	} else {
		fmt.Fprintf(w, "# Environment unset for %s profile %q\n", tool, name)
	}
	return nil
}

func sortedEnvKeys(envVars map[string]string) []string {
	keys := make([]string, 0, len(envVars))
	for k := range envVars {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
