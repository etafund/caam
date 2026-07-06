package cmd

import (
	"encoding/json"
	"io"
	"time"

	"github.com/Dicklesworthstone/coding_agent_account_manager/internal/version"
)

const (
	jsonOutputFormatStatus = "caam.status.v1"
	jsonOutputFormatLS     = "caam.ls.v1"
)

type jsonEnvelope struct {
	GeneratedAt  string                 `json:"generated_at"`
	Version      string                 `json:"version"`
	OutputFormat string                 `json:"output_format"`
	Data         any                    `json:"data,omitempty"`
	Error        *jsonEnvelopeError     `json:"error,omitempty"`
	Meta         map[string]interface{} `json:"_meta,omitempty"`
}

type jsonEnvelopeError struct {
	Code    string `json:"code,omitempty"`
	Message string `json:"message"`
}

func newJSONEnvelope(outputFormat string, data any) jsonEnvelope {
	return jsonEnvelope{
		GeneratedAt:  time.Now().UTC().Format(time.RFC3339),
		Version:      version.Info(),
		OutputFormat: outputFormat,
		Data:         data,
	}
}

func encodeJSONEnvelope(w io.Writer, outputFormat string, data any) error {
	return encodeIndentedJSON(w, newJSONEnvelope(outputFormat, data))
}

func encodeIndentedJSON(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}
