package store

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFetchRelevantMemoryPinnedRanksFirst(t *testing.T) {
	t.Parallel()
	db, cleanup := setupMemoryTestDB(t)
	t.Cleanup(cleanup)

	// Write pinned entry with access_count=0 (freshly inserted, never read)
	require.NoError(t, SetMemory(db, "pinned-arch", "decision", "string", "global", "", nil, true, "", nil))

	// Write unpinned entry then simulate high access_count via direct SQL
	require.NoError(t, SetMemory(db, "hot-key", "frequent", "string", "global", "", nil, false, "", nil))
	_, err := db.Exec(`UPDATE memory SET access_count = 100 WHERE key = 'hot-key'`)
	require.NoError(t, err)

	mems, err := fetchRelevantMemory(db, "", "")
	require.NoError(t, err)
	require.GreaterOrEqual(t, len(mems), 2)

	// Pinned entry must appear before the high-access unpinned entry
	pinnedIdx, hotIdx := -1, -1
	for i, m := range mems {
		if m.Key == "pinned-arch" {
			pinnedIdx = i
		}
		if m.Key == "hot-key" {
			hotIdx = i
		}
	}
	require.NotEqual(t, -1, pinnedIdx, "pinned-arch must appear in results")
	require.NotEqual(t, -1, hotIdx, "hot-key must appear in results")
	assert.Less(t, pinnedIdx, hotIdx, "pinned entry must rank before high-access unpinned entry")
}

func TestFetchRelevantMemoryPinnedBypassesTTL(t *testing.T) {
	t.Parallel()
	db, cleanup := setupMemoryTestDB(t)
	t.Cleanup(cleanup)

	past := time.Now().Add(-2 * time.Hour)

	// Pinned entry with past expires_at
	require.NoError(t, SetMemory(db, "pinned-expired", "value", "string", "global", "", &past, true, "", nil))
	// Unpinned entry with past expires_at — should NOT appear
	require.NoError(t, SetMemory(db, "unpinned-expired", "value", "string", "global", "", &past, false, "", nil))

	mems, err := fetchRelevantMemory(db, "", "")
	require.NoError(t, err)

	var foundPinned, foundUnpinned bool
	for _, m := range mems {
		if m.Key == "pinned-expired" {
			foundPinned = true
		}
		if m.Key == "unpinned-expired" {
			foundUnpinned = true
		}
	}
	assert.True(t, foundPinned, "pinned expired entry must appear in brief")
	assert.False(t, foundUnpinned, "unpinned expired entry must not appear in brief")
}
