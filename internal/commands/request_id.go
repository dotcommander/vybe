package commands

import (
	"os"

	"github.com/spf13/cobra"

	"github.com/dotcommander/vybe/internal/store"
)

func resolveRequestID(cmd *cobra.Command) string {
	if v, err := cmd.Flags().GetString("request-id"); err == nil && v != "" {
		return v
	}
	return os.Getenv("VYBE_REQUEST_ID")
}

// requireRequestID returns the request ID from flag/env, or auto-generates a
// fresh one when neither is set. An explicit ID (flag or VYBE_REQUEST_ID) keeps
// exactly-once dedup semantics; an auto-generated ID is unique per call, so
// omitting request-id means at-least-once (no dedup). The error return is kept
// for signature stability with requireMutationParams; it is currently always nil.
func requireRequestID(cmd *cobra.Command) (string, error) {
	rid := resolveRequestID(cmd)
	if rid == "" {
		rid = store.NewRequestID()
	}
	return rid, nil
}
