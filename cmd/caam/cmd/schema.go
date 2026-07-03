package cmd

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/Dicklesworthstone/coding_agent_account_manager/internal/version"
	"github.com/spf13/cobra"
)

const schemaSchemaVersion = 1

type schemaEnvelope struct {
	GeneratedAt   string            `json:"generated_at"`
	Version       string            `json:"version"`
	OutputFormat  string            `json:"output_format"`
	SchemaVersion int               `json:"schema_version"`
	Schema        []schemaForOutput `json:"schema"`
	Query         string            `json:"query,omitempty"`
}

type schemaForOutput struct {
	Command string                 `json:"command"`
	Schema  map[string]interface{} `json:"schema"`
	Aliases []string               `json:"aliases,omitempty"`
}

var schemaCmd = &cobra.Command{
	Use:   "schema [command]",
	Short: "Emit JSON Schema for key machine-readable command outputs",
	Long: `Emit JSON Schema for structured outputs.

Supported commands:
  status
  ls
  activate
  all

Use with no argument to emit all schemas.`,
	Args: cobra.MaximumNArgs(1),
	RunE: runSchema,
}

func init() {
	rootCmd.AddCommand(schemaCmd)
}

func runSchema(cmd *cobra.Command, args []string) error {
	query := "all"
	if len(args) == 1 {
		query = strings.ToLower(strings.TrimSpace(args[0]))
	}

	builders := map[string]func() map[string]interface{}{
		"status":   statusOutputSchema,
		"ls":       lsOutputSchema,
		"list":     lsOutputSchema,
		"activate": activateOutputSchema,
		"all":      nil,
	}

	if query != "all" && query != "" {
		if _, ok := builders[query]; !ok {
			return fmt.Errorf("unknown command %q for schema (expected: status, ls/list, activate, all)", query)
		}
	}

	var targets []string
	if query == "all" || query == "" {
		targets = []string{"status", "ls", "activate"}
	} else if query == "list" {
		targets = []string{"ls"}
	} else {
		targets = []string{query}
	}

	output := schemaEnvelope{
		GeneratedAt:   time.Now().UTC().Format(time.RFC3339),
		Version:       version.Info(),
		OutputFormat:  "json-schema-2020-12",
		SchemaVersion: schemaSchemaVersion,
		Schema:        make([]schemaForOutput, 0, len(targets)),
		Query:         query,
	}

	for _, target := range targets {
		builder, ok := builders[target]
		if !ok || builder == nil {
			continue
		}
		output.Schema = append(output.Schema, schemaForOutput{
			Command: target,
			Schema:  builder(),
			Aliases: commandAliases(target),
		})
	}

	enc := json.NewEncoder(cmd.OutOrStdout())
	enc.SetIndent("", "  ")
	return enc.Encode(output)
}

func commandAliases(command string) []string {
	switch command {
	case "ls":
		return []string{"list"}
	case "status", "activate":
		return nil
	default:
		return nil
	}
}

func statusOutputSchema() map[string]interface{} {
	return map[string]interface{}{
		"$schema": "https://json-schema.org/draft/2020-12/schema",
		"title":   "caam status output",
		"type":    "object",
		"required": []string{
			"tools",
		},
		"additionalProperties": false,
		"properties": map[string]interface{}{
			"tools": map[string]interface{}{
				"type": "array",
				"items": map[string]interface{}{
					"type": "object",
					"required": []string{
						"tool",
						"logged_in",
					},
					"properties": map[string]interface{}{
						"tool": map[string]interface{}{
							"type":        "string",
							"examples":    []string{"codex", "claude", "gemini"},
							"description": "Provider name",
						},
						"logged_in": map[string]interface{}{
							"type":        "boolean",
							"description": "True when auth files exist for the provider",
						},
						"active_profile": map[string]interface{}{
							"type":        "string",
							"description": "Active profile name when it matches a vault profile",
						},
						"saved_profiles": map[string]interface{}{
							"type":        "integer",
							"description": "Saved profile count when live auth has no match",
							"minimum":     0,
						},
						"error": map[string]interface{}{
							"type":        "string",
							"description": "Error message when status resolution fails",
						},
						"health": map[string]interface{}{
							"type": "object",
							"properties": map[string]interface{}{
								"status": map[string]interface{}{
									"type": "string",
								},
								"reason": map[string]interface{}{
									"type": "string",
								},
								"expires_at": map[string]interface{}{
									"type":        "string",
									"format":      "date-time",
									"description": "RFC3339 token expiry timestamp",
								},
								"error_count": map[string]interface{}{
									"type":    "integer",
									"minimum": 0,
								},
								"cooldown_remaining": map[string]interface{}{
									"type": "string",
								},
							},
						},
						"identity": map[string]interface{}{
							"type": "object",
							"properties": map[string]interface{}{
								"email": map[string]interface{}{
									"type": "string",
								},
								"organization": map[string]interface{}{
									"type": "string",
								},
								"plan_type": map[string]interface{}{
									"type": "string",
								},
								"account_id": map[string]interface{}{
									"type": "string",
								},
								"expires_at": map[string]interface{}{
									"type":        "string",
									"format":      "date-time",
									"description": "RFC3339 expiry metadata",
								},
								"provider": map[string]interface{}{
									"type":        "string",
									"description": "Source provider name",
								},
							},
						},
					},
					"additionalProperties": false,
				},
			},
			"warnings": map[string]interface{}{
				"type":  "array",
				"items": map[string]interface{}{"type": "string"},
			},
			"recommendations": map[string]interface{}{
				"type":  "array",
				"items": map[string]interface{}{"type": "string"},
			},
		},
	}
}

func lsOutputSchema() map[string]interface{} {
	return map[string]interface{}{
		"$schema": "https://json-schema.org/draft/2020-12/schema",
		"title":   "caam ls output",
		"type":    "object",
		"required": []string{
			"profiles",
			"count",
		},
		"additionalProperties": false,
		"properties": map[string]interface{}{
			"profiles": map[string]interface{}{
				"type": "array",
				"items": map[string]interface{}{
					"type": "object",
					"required": []string{
						"tool",
						"name",
						"active",
						"system",
						"health",
					},
					"properties": map[string]interface{}{
						"tool": map[string]interface{}{
							"type":        "string",
							"description": "Provider name",
						},
						"name": map[string]interface{}{
							"type":        "string",
							"description": "Profile name",
						},
						"active": map[string]interface{}{
							"type":        "boolean",
							"description": "True when this profile is active",
						},
						"system": map[string]interface{}{
							"type":        "boolean",
							"description": "True when profile is an internal system profile",
						},
						"health": map[string]interface{}{
							"type": "object",
							"required": []string{
								"status",
								"error_count",
							},
							"properties": map[string]interface{}{
								"status": map[string]interface{}{
									"type": "string",
								},
								"expires_at": map[string]interface{}{
									"type":        "string",
									"format":      "date-time",
									"description": "RFC3339 token expiry timestamp",
								},
								"error_count": map[string]interface{}{
									"type":    "integer",
									"minimum": 0,
								},
							},
						},
						"identity": map[string]interface{}{
							"type": "object",
							"properties": map[string]interface{}{
								"email": map[string]interface{}{
									"type": "string",
								},
								"organization": map[string]interface{}{
									"type": "string",
								},
								"plan_type": map[string]interface{}{
									"type": "string",
								},
								"account_id": map[string]interface{}{
									"type": "string",
								},
								"expires_at": map[string]interface{}{
									"type":   "string",
									"format": "date-time",
								},
								"provider": map[string]interface{}{
									"type": "string",
								},
							},
						},
					},
					"additionalProperties": false,
				},
			},
			"count": map[string]interface{}{
				"type":        "integer",
				"minimum":     0,
				"description": "Number of profile entries",
			},
		},
	}
}

func activateOutputSchema() map[string]interface{} {
	return map[string]interface{}{
		"$schema": "https://json-schema.org/draft/2020-12/schema",
		"title":   "caam activate output",
		"type":    "object",
		"required": []string{
			"success",
			"tool",
			"profile",
		},
		"additionalProperties": false,
		"properties": map[string]interface{}{
			"success": map[string]interface{}{
				"type":        "boolean",
				"description": "True when activation succeeds",
			},
			"tool": map[string]interface{}{
				"type":        "string",
				"description": "Target provider",
			},
			"profile": map[string]interface{}{
				"type":        "string",
				"description": "Activated profile",
			},
			"previous_profile": map[string]interface{}{
				"type":        "string",
				"description": "Profile replaced by activation",
			},
			"source": map[string]interface{}{
				"type":        "string",
				"description": "Selection source (manual/default/rotation)",
			},
			"auto_backup": map[string]interface{}{
				"type":        "string",
				"description": "Name used when current state was auto-backed up",
			},
			"refreshed": map[string]interface{}{
				"type":        "boolean",
				"description": "True if token refresh was triggered",
			},
			"rotation": map[string]interface{}{
				"type": "object",
				"required": []string{
					"algorithm",
					"selected",
				},
				"properties": map[string]interface{}{
					"algorithm": map[string]interface{}{
						"type": "string",
					},
					"selected": map[string]interface{}{
						"type": "string",
					},
					"alternatives": map[string]interface{}{
						"type": "array",
						"items": map[string]interface{}{
							"type": "object",
							"required": []string{
								"profile",
								"score",
							},
							"properties": map[string]interface{}{
								"profile": map[string]interface{}{
									"type": "string",
								},
								"score": map[string]interface{}{
									"type":    "number",
									"minimum": 0,
								},
							},
						},
					},
				},
				"additionalProperties": false,
			},
			"codex_daemon": map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"detected": map[string]interface{}{
						"type": "boolean",
					},
					"pids": map[string]interface{}{
						"type":  "array",
						"items": map[string]interface{}{"type": "integer"},
					},
					"subcommands": map[string]interface{}{
						"type":  "array",
						"items": map[string]interface{}{"type": "string"},
					},
					"message": map[string]interface{}{
						"type": "string",
					},
					"reloaded": map[string]interface{}{
						"type": "boolean",
					},
				},
				"additionalProperties": false,
			},
			"error": map[string]interface{}{
				"type":        "string",
				"description": "Error message on failed activation",
			},
		},
	}
}
