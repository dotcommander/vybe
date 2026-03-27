package commands

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

func resolveRequestID(cmd *cobra.Command) string {
	if v, err := cmd.Flags().GetString("request-id"); err == nil && v != "" {
		return v
	}
	return os.Getenv("VYBE_REQUEST_ID")
}

// requireRequestID returns the request ID from flag/env, or errors if neither is set.
// Callers must supply deterministic IDs for idempotency (retries with the same ID are deduplicated).
func requireRequestID(cmd *cobra.Command) (string, error) {
	rid := resolveRequestID(cmd)
	if rid == "" {
		return "", fmt.Errorf("--request-id or VYBE_REQUEST_ID is required for idempotent operations")
	}
	return rid, nil
}
