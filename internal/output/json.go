package output

import (
	"encoding/json"
	"os"
)

// Response represents a standard JSON response
type Response struct {
	SchemaVersion string      `json:"schema_version"`
	Success       bool        `json:"success"`
	Data          interface{} `json:"data,omitempty"`
	Error         string      `json:"error,omitempty"`
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

// Print prints a value as JSON to stdout
func Print(v interface{}) error {
	enc := json.NewEncoder(os.Stdout)
	// Default to compact JSON to minimize token/output size for agent consumption.
	// Enable pretty JSON for humans via env var: VYBE_PRETTY_JSON=1.
	if os.Getenv("VYBE_PRETTY_JSON") == "1" || os.Getenv("VYBE_PRETTY_JSON") == "true" {
		enc.SetIndent("", "  ")
	}
	return enc.Encode(v)
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
