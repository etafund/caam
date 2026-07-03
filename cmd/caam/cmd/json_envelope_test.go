package cmd

import (
	"bytes"
	"encoding/json"
	"testing"
	"time"

	"github.com/Dicklesworthstone/coding_agent_account_manager/internal/version"
	"github.com/stretchr/testify/require"
)

func TestEncodeJSONEnvelope(t *testing.T) {
	var buf bytes.Buffer
	payload := map[string]string{"ok": "true"}

	require.NoError(t, encodeJSONEnvelope(&buf, "caam.test.v1", payload))

	var got struct {
		GeneratedAt  string            `json:"generated_at"`
		Version      string            `json:"version"`
		OutputFormat string            `json:"output_format"`
		Data         map[string]string `json:"data"`
	}
	require.NoError(t, json.Unmarshal(buf.Bytes(), &got))
	require.Equal(t, version.Info(), got.Version)
	require.Equal(t, "caam.test.v1", got.OutputFormat)
	require.Equal(t, payload, got.Data)

	parsed, err := time.Parse(time.RFC3339, got.GeneratedAt)
	require.NoError(t, err)
	require.Equal(t, time.UTC, parsed.Location())
}
