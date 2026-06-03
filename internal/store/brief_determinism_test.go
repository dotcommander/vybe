package store

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestFetchRelevantMemory_DeterministicAsOf verifies that fetchRelevantMemory:
//  1. Returns identical results across two calls with the same asOf (determinism).
//  2. Excludes entries whose expires_at is before asOf, regardless of wall clock.
//  3. Includes entries whose expires_at is after asOf, regardless of wall clock.
func TestFetchRelevantMemory_DeterministicAsOf(t *testing.T) {
	t.Parallel()
	db, cleanup := setupMemoryTestDB(t)
	t.Cleanup(cleanup)

	asOf := time.Date(2026, 6, 3, 0, 0, 0, 0, time.UTC)

	// Pinned memory — must always appear regardless of expiry.
	require.NoError(t, SetMemory(db, "pinned-det", "pinned-value", "string", "global", "", nil, true, "directive"))

	// Non-expired fact — should appear. Set updated_at to 2026-06-01 (recent).
	require.NoError(t, SetMemory(db, "fact-recent", "recent", "string", "global", "", nil, false, "fact"))
	_, err := db.Exec(`UPDATE memory SET updated_at = datetime('2026-06-01') WHERE key = 'fact-recent'`)
	require.NoError(t, err)

	// Non-expired lesson, older updated_at — should appear but rank lower than fact-recent.
	require.NoError(t, SetMemory(db, "lesson-old", "old-lesson", "string", "global", "", nil, false, "lesson"))
	_, err = db.Exec(`UPDATE memory SET updated_at = datetime('2025-01-01') WHERE key = 'lesson-old'`)
	require.NoError(t, err)

	// Expires BEFORE asOf (2026-06-02 < 2026-06-03) — must be EXCLUDED (not pinned).
	expiredBefore := time.Date(2026, 6, 2, 0, 0, 0, 0, time.UTC)
	require.NoError(t, SetMemory(db, "expires-before", "gone", "string", "global", "", &expiredBefore, false, "fact"))

	// Expires AFTER asOf (2026-06-04 > 2026-06-03) — must be INCLUDED.
	expiresAfter := time.Date(2026, 6, 4, 0, 0, 0, 0, time.UTC)
	require.NoError(t, SetMemory(db, "expires-after", "present", "string", "global", "", &expiresAfter, false, "fact"))

	// Call twice with identical asOf — no sleep between calls.
	first, err := fetchRelevantMemory(db, "", "", asOf)
	require.NoError(t, err)
	second, err := fetchRelevantMemory(db, "", "", asOf)
	require.NoError(t, err)

	// Determinism: same length and same key order.
	require.Equal(t, len(first), len(second), "two calls with same asOf must return same number of entries")
	for i := range first {
		assert.Equal(t, first[i].Key, second[i].Key, "entry at index %d must be the same key across calls", i)
	}

	// Clock-pinned membership: collect keys.
	keys := make(map[string]bool, len(first))
	for _, m := range first {
		keys[m.Key] = true
	}

	assert.False(t, keys["expires-before"], "entry expiring before asOf must be excluded")
	assert.True(t, keys["expires-after"], "entry expiring after asOf must be included")
	assert.True(t, keys["pinned-det"], "pinned entry must always be included")
	assert.True(t, keys["fact-recent"], "non-expired fact must be included")
	assert.True(t, keys["lesson-old"], "non-expired lesson must be included")
}
