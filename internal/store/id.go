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
// Panics if crypto/rand is unavailable — entropy loss would silently reduce
// collision resistance to timestamp-only, which is unacceptable.
func generatePrefixedID(prefix string) string {
	timestamp := time.Now().UnixNano()

	var b [6]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(fmt.Sprintf("crypto/rand unavailable: %v", err))
	}

	return fmt.Sprintf("%s_%d_%s", prefix, timestamp, hex.EncodeToString(b[:]))
}
