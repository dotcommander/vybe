package store

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPinSurvivesTTL(t *testing.T) {
	t.Parallel()
	db, cleanup := setupMemoryTestDB(t)
	t.Cleanup(cleanup)

	// Write with a past expires_at and pinned=true
	past := time.Now().Add(-1 * time.Hour)
	require.NoError(t, SetMemory(db, "arch", "monolith", "string", "global", "", &past, true))

	// Should still be returned despite expired TTL
	mem, err := GetMemory(db, "arch", "global", "")
	require.NoError(t, err)
	require.NotNil(t, mem, "pinned memory must survive TTL expiry")
	assert.Equal(t, "monolith", mem.Value)
	assert.True(t, mem.Pinned)
}

func TestGCSkipsPinned(t *testing.T) {
	t.Parallel()
	db, cleanup := setupMemoryTestDB(t)
	t.Cleanup(cleanup)

	past := time.Now().Add(-1 * time.Hour)

	// One pinned-expired row, one unpinned-expired row
	require.NoError(t, SetMemory(db, "pinned-expired", "v1", "string", "global", "", &past, true))
	require.NoError(t, SetMemory(db, "unpinned-expired", "v2", "string", "global", "", &past, false))

	_, deleted, err := GCMemoryWithEventIdempotent(db, "agent-gc", "req-gc-skip-pinned", 100)
	require.NoError(t, err)
	assert.Equal(t, 1, deleted, "GC must delete unpinned expired rows only")

	// Pinned row must still exist
	mem, err := GetMemory(db, "pinned-expired", "global", "")
	require.NoError(t, err)
	require.NotNil(t, mem, "pinned row must survive GC")
}

func TestPinMemoryTxNotFound(t *testing.T) {
	t.Parallel()
	db, cleanup := setupMemoryTestDB(t)
	t.Cleanup(cleanup)

	var found bool
	err := Transact(context.Background(), db, func(tx *sql.Tx) error {
		_, f, txErr := PinMemoryTx(context.Background(), tx, "agent", "missing-key", "global", "", true)
		found = f
		return txErr
	})
	require.NoError(t, err)
	assert.False(t, found, "PinMemoryTx must return found=false for absent key")
}

func TestPinIdempotentReplay(t *testing.T) {
	t.Parallel()
	db, cleanup := setupMemoryTestDB(t)
	t.Cleanup(cleanup)

	require.NoError(t, SetMemory(db, "replay-key", "val", "string", "global", "", nil, false))

	eid1, err := PinMemoryIdempotent(context.Background(), db, "agent", "req-pin-replay-1", "replay-key", "global", "", true)
	require.NoError(t, err)
	require.Greater(t, eid1, int64(0))

	// Same request-id must return same event_id
	eid2, err := PinMemoryIdempotent(context.Background(), db, "agent", "req-pin-replay-1", "replay-key", "global", "", true)
	require.NoError(t, err)
	assert.Equal(t, eid1, eid2, "idempotent replay must return original event_id")
}
