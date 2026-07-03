package cmd

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestRobotDocs_AllTopics(t *testing.T) {
	out, _, err := captureOutput(t, createTestCmd(), []string{"robot", "docs"})
	if err != nil {
		t.Fatalf("expected robot docs to succeed, got error: %v", err)
	}

	var envelope struct {
		Success bool          `json:"success"`
		Command string        `json:"command"`
		Data    RobotDocsData `json:"data"`
		Error   *RobotError   `json:"error"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &envelope); err != nil {
		t.Fatalf("invalid JSON output: %v", err)
	}

	if !envelope.Success {
		t.Fatal("expected success=true")
	}
	if envelope.Command != "docs" {
		t.Fatalf("expected command docs, got %q", envelope.Command)
	}
	if envelope.Data.SchemaVersion != robotDocsSchemaVersion {
		t.Fatalf("unexpected schema version: %d", envelope.Data.SchemaVersion)
	}
	if len(envelope.Data.Topics) == 0 {
		t.Fatal("expected at least one topic")
	}
}

func TestRobotDocs_SingleTopic(t *testing.T) {
	out, _, err := captureOutput(t, createTestCmd(), []string{"robot", "docs", "exit-codes"})
	if err != nil {
		t.Fatalf("expected robot docs topic to succeed, got error: %v", err)
	}

	var envelope struct {
		Success bool          `json:"success"`
		Data    RobotDocsData `json:"data"`
		Error   *RobotError   `json:"error"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &envelope); err != nil {
		t.Fatalf("invalid JSON output: %v", err)
	}
	if !envelope.Success {
		t.Fatal("expected success=true")
	}
	if len(envelope.Data.Topics) != 1 || envelope.Data.Topics[0].Topic != "exit-codes" {
		t.Fatalf("expected only exit-codes topic, got %+v", envelope.Data.Topics)
	}
}

func TestRobotDocs_InvalidTopic(t *testing.T) {
	_, _, err := captureOutput(t, createTestCmd(), []string{"robot", "docs", "missing-topic"})
	if err == nil {
		t.Fatal("expected error for invalid topic")
	}
	if !strings.Contains(err.Error(), "INVALID_TOPIC") {
		t.Fatalf("expected INVALID_TOPIC error, got %v", err)
	}
}

func TestRobotDocs_Ndjson(t *testing.T) {
	out, _, err := captureOutput(t, createTestCmd(), []string{"robot", "docs", "commands", "--ndjson"})
	if err != nil {
		t.Fatalf("expected robot docs ndjson to succeed, got error: %v", err)
	}

	line := strings.TrimSpace(out)
	if line == "" {
		t.Fatalf("expected non-empty output")
	}

	var envelope struct {
		Data RobotDocsData `json:"data"`
	}
	if err := json.Unmarshal([]byte(line), &envelope); err != nil {
		t.Fatalf("invalid NDJSON fragment: %v", err)
	}
	if len(envelope.Data.Topics) != 1 || envelope.Data.Topics[0].Topic != "commands" {
		t.Fatalf("expected one commands topic, got %+v", envelope.Data.Topics)
	}
}

func TestSchema_AllOutputs(t *testing.T) {
	out, _, err := captureOutput(t, createTestCmd(), []string{"schema"})
	if err != nil {
		t.Fatalf("expected schema command to succeed, got error: %v", err)
	}

	var envelope struct {
		Query         string `json:"query"`
		SchemaVersion int    `json:"schema_version"`
		Schema        []struct {
			Command string                 `json:"command"`
			Aliases []string               `json:"aliases"`
			Schema  map[string]interface{} `json:"schema"`
		} `json:"schema"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &envelope); err != nil {
		t.Fatalf("invalid JSON output: %v", err)
	}
	if envelope.Query != "all" {
		t.Fatalf("expected query=all, got %q", envelope.Query)
	}
	if envelope.SchemaVersion != schemaSchemaVersion {
		t.Fatalf("unexpected schema version: %d", envelope.SchemaVersion)
	}
	if len(envelope.Schema) != 3 {
		t.Fatalf("expected 3 schema entries, got %d", len(envelope.Schema))
	}

	hasStatus := false
	for _, item := range envelope.Schema {
		if item.Command == "" || item.Schema == nil {
			t.Fatalf("expected command and schema for every entry, got %+v", item)
		}
		if item.Command == "status" {
			hasStatus = true
			assertEnvelopeSchema(t, item.Schema, jsonOutputFormatStatus)
		}
	}
	if !hasStatus {
		t.Fatal("expected status schema entry")
	}
}

func TestSchema_SingleCommandAlias(t *testing.T) {
	out, _, err := captureOutput(t, createTestCmd(), []string{"schema", "list"})
	if err != nil {
		t.Fatalf("expected schema list to succeed, got error: %v", err)
	}

	var envelope struct {
		Query  string `json:"query"`
		Schema []struct {
			Command string                 `json:"command"`
			Schema  map[string]interface{} `json:"schema"`
		} `json:"schema"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &envelope); err != nil {
		t.Fatalf("invalid JSON output: %v", err)
	}

	if envelope.Query != "list" {
		t.Fatalf("expected query=list, got %q", envelope.Query)
	}
	if len(envelope.Schema) != 1 {
		t.Fatalf("expected one schema entry, got %d", len(envelope.Schema))
	}
	if envelope.Schema[0].Command != "ls" {
		t.Fatalf("expected list alias to map to ls, got %q", envelope.Schema[0].Command)
	}
	assertEnvelopeSchema(t, envelope.Schema[0].Schema, jsonOutputFormatLS)
}

func assertEnvelopeSchema(t *testing.T, schema map[string]interface{}, outputFormat string) {
	t.Helper()

	required, ok := schema["required"].([]interface{})
	if !ok {
		t.Fatalf("schema required field has unexpected type: %T", schema["required"])
	}
	for _, field := range []string{"generated_at", "version", "output_format", "data"} {
		found := false
		for _, value := range required {
			if value == field {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("schema required fields %v missing %q", required, field)
		}
	}

	properties, ok := schema["properties"].(map[string]interface{})
	if !ok {
		t.Fatalf("schema properties field has unexpected type: %T", schema["properties"])
	}
	outputFormatSchema, ok := properties["output_format"].(map[string]interface{})
	if !ok {
		t.Fatalf("output_format schema has unexpected type: %T", properties["output_format"])
	}
	if outputFormatSchema["const"] != outputFormat {
		t.Fatalf("output_format const = %v, want %q", outputFormatSchema["const"], outputFormat)
	}
	if _, ok := properties["data"].(map[string]interface{}); !ok {
		t.Fatalf("data schema has unexpected type: %T", properties["data"])
	}
}

func TestSchema_InvalidCommand(t *testing.T) {
	_, _, err := captureOutput(t, createTestCmd(), []string{"schema", "unknown"})
	if err == nil {
		t.Fatal("expected error for unknown schema command")
	}
}
