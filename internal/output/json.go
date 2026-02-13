package output

import (
	"encoding/json"
	"io"
	"os"
)

// Response represents a standard JSON response
type Response struct {
	SchemaVersion string      `json:"schema_version"`
	Success       bool        `json:"success"`
	Data          interface{} `json:"data,omitempty"`
	Error         string      `json:"error,omitempty"`
}

// Config holds output configuration
type Config struct {
	Writer io.Writer
	Pretty bool
}

// DefaultConfig returns configuration using stdout and environment
func DefaultConfig() Config {
	pretty := os.Getenv("VYBE_PRETTY_JSON") == "1" || os.Getenv("VYBE_PRETTY_JSON") == "true"
	return Config{
		Writer: os.Stdout,
		Pretty: pretty,
	}
}

// Success wraps a successful response with data
func Success(data interface{}) Response {
	return Response{
		SchemaVersion: "v1",
		Success:       true,
		Data:          data,
	}
}

// Error wraps an error in a response
func Error(err error) Response {
	return Response{
		SchemaVersion: "v1",
		Success:       false,
		Error:         err.Error(),
	}
}

// PrintWith prints a value as JSON to the configured writer
func PrintWith(cfg Config, v interface{}) error {
	enc := json.NewEncoder(cfg.Writer)
	if cfg.Pretty {
		enc.SetIndent("", "  ")
	}
	return enc.Encode(v)
}

// Print prints a value as JSON to stdout
// Default to compact JSON to minimize token/output size for agent consumption.
// Enable pretty JSON for humans via env var: VYBE_PRETTY_JSON=1.
func Print(v interface{}) error {
	return PrintWith(DefaultConfig(), v)
}

// PrintSuccess prints a success response
func PrintSuccess(data interface{}) error {
	return Print(Success(data))
}

// PrintError prints an error response
func PrintError(err error) error {
	return Print(Error(err))
}

// Keep output package focused: commands should handle human-readable formatting.
