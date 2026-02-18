package commands

import (
	"errors"
	"os"

	"github.com/spf13/cobra"
)

func resolveRequestID(cmd *cobra.Command) string {
	if v, err := cmd.Flags().GetString("request-id"); err == nil && v != "" {
		return v
	}
	return os.Getenv("VYBE_REQUEST_ID")
}

// requireRequestID enforces agents-only idempotency for mutating commands.
func requireRequestID(cmd *cobra.Command) (string, error) {
	rid := resolveRequestID(cmd)
	if rid == "" {
		return "", errors.New("request id is required for mutating operations (set --request-id or VYBE_REQUEST_ID)")
	}
	return rid, nil
}
