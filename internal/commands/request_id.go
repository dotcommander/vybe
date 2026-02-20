package commands

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"time"

	"github.com/spf13/cobra"
)

func resolveRequestID(cmd *cobra.Command) string {
	if v, err := cmd.Flags().GetString("request-id"); err == nil && v != "" {
		return v
	}
	return os.Getenv("VYBE_REQUEST_ID")
}

func generateRequestID() string {
	timestamp := time.Now().UnixNano()
	var b [6]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("req_%d", timestamp)
	}
	return fmt.Sprintf("req_%d_%s", timestamp, hex.EncodeToString(b[:]))
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
