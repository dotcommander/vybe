package store

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"time"
)

// generatePrefixedID creates a globally unique ID in the format:
//
//	{prefix}_{unix_nano}_{12_hex_chars}
//
// The 12 hex characters are derived from 6 cryptographically random bytes,
// giving 48 bits of randomness to avoid collisions at the same nanosecond.
// If crypto/rand fails, the ID omits the random suffix and relies on the
// nanosecond timestamp alone (acceptable for CLI-scale usage).
func generatePrefixedID(prefix string) string {
	timestamp := time.Now().UnixNano()

	var b [6]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("%s_%d", prefix, timestamp)
	}

	return fmt.Sprintf("%s_%d_%s", prefix, timestamp, hex.EncodeToString(b[:]))
}
