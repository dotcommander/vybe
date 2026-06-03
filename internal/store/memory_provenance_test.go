package store

import (
	"context"
	"database/sql"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// seedMemoryWithProvenance upserts a memory row with provenance via UpsertMemoryTx inside Transact.
func seedMemoryWithProvenance(t *testing.T, db *sql.DB, key, value, scope, scopeID string, pinned bool, sourceEventID *int64, sourceTaskID string) {
	t.Helper()
	err := Transact(context.Background(), db, func(tx *sql.Tx) error {
		_, err := UpsertMemoryTx(tx, "test-agent", key, value, "string", scope, scopeID, nil, pinned, "fact", nil, sourceEventID, sourceTaskID)
		return err
	})
	require.NoError(t, err)
}

// insertTaskWithStatus creates a task row and optionally sets status + blocked_reason directly.
// It uses CreateTask for insertion, then a direct SQL UPDATE for status/blocked_reason so tests
// don't depend on the full status-update action chain.
func insertTaskWithStatus(t *testing.T, db *sql.DB, taskID, status, blockedReason string) {
	t.Helper()
	_, err := db.ExecContext(context.Background(), `
		INSERT INTO tasks (id, title, description, status, priority, version, created_at, updated_at)
		VALUES (?, ?, '', ?, 0, 1, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
	`, taskID, taskID, status)
	require.NoError(t, err)
	if blockedReason != "" {
		_, err = db.ExecContext(context.Background(), `UPDATE tasks SET blocked_reason = ? WHERE id = ?`, blockedReason, taskID)
		require.NoError(t, err)
	}
}

func TestListMemoryBySource_FilterByTaskID(t *testing.T) {
	t.Parallel()
	db, cleanup := setupMemoryTestDB(t)
	t.Cleanup(cleanup)

	seedMemoryWithProvenance(t, db, "key1", "val1", "global", "", false, nil, "task_A")
	seedMemoryWithProvenance(t, db, "key2", "val2", "global", "", false, nil, "task_A")
	seedMemoryWithProvenance(t, db, "key3", "val3", "global", "", false, nil, "task_B")

	results, err := ListMemoryBySource(db, nil, "task_A")
	require.NoError(t, err)
	assert.Len(t, results, 2)
	for _, m := range results {
		assert.Equal(t, "task_A", m.SourceTaskID)
	}
}

func TestListMemoryBySource_FilterByEventID(t *testing.T) {
	t.Parallel()
	db, cleanup := setupMemoryTestDB(t)
	t.Cleanup(cleanup)

	// Use two distinct event IDs by seeding in separate transactions and capturing the returned eventID.
	var eid1 int64
	err := Transact(context.Background(), db, func(tx *sql.Tx) error {
		id, txErr := UpsertMemoryTx(tx, "test-agent", "evkey1", "v1", "string", "global", "", nil, false, "fact", nil, nil, "")
		eid1 = id
		return txErr
	})
	require.NoError(t, err)
	require.NotZero(t, eid1)

	// Seed a second memory with source_event_id = eid1
	err = Transact(context.Background(), db, func(tx *sql.Tx) error {
		_, txErr := UpsertMemoryTx(tx, "test-agent", "evkey2", "v2", "string", "global", "", nil, false, "fact", nil, &eid1, "")
		return txErr
	})
	require.NoError(t, err)

	// Seed a third memory with a different source_event_id
	var eid2 int64
	err = Transact(context.Background(), db, func(tx *sql.Tx) error {
		id, txErr := UpsertMemoryTx(tx, "test-agent", "evkey3", "v3", "string", "global", "", nil, false, "fact", nil, nil, "")
		eid2 = id
		return txErr
	})
	require.NoError(t, err)
	require.NotZero(t, eid2)

	// Now update evkey3 to point at eid2 (seeded with provenance from the start)
	// Actually, re-seed a fresh key with eid2 as source directly
	err = Transact(context.Background(), db, func(tx *sql.Tx) error {
		_, txErr := UpsertMemoryTx(tx, "test-agent", "evkey4", "v4", "string", "global", "", nil, false, "fact", nil, &eid2, "")
		return txErr
	})
	require.NoError(t, err)

	results, err := ListMemoryBySource(db, &eid1, "")
	require.NoError(t, err)
	// evkey2 was seeded with source_event_id=eid1; evkey1 has no source_event_id
	assert.Len(t, results, 1)
	assert.Equal(t, "evkey2", results[0].Key)
}

func TestListMemoryBySource_NoFilter_Errors(t *testing.T) {
	t.Parallel()
	db, cleanup := setupMemoryTestDB(t)
	t.Cleanup(cleanup)

	_, err := ListMemoryBySource(db, nil, "")
	assert.Error(t, err)
}

func TestGCOrphanedMemory_ReapsAbsentTask(t *testing.T) {
	t.Parallel()
	db, cleanup := setupMemoryTestDB(t)
	t.Cleanup(cleanup)

	// Memory pointing to non-existent task — should be reaped.
	seedMemoryWithProvenance(t, db, "orphan_key", "val", "global", "", false, nil, "task_gone")

	// Create a real task and attach a memory to it — should NOT be reaped.
	insertTaskWithStatus(t, db, "task_real", "pending", "")
	seedMemoryWithProvenance(t, db, "live_key", "val", "global", "", false, nil, "task_real")

	eventID, deleted, err := GCOrphanedMemoryWithEventIdempotent(db, "agent", "req1", 100, false)
	require.NoError(t, err)
	assert.Equal(t, 1, deleted)
	assert.NotZero(t, eventID)

	// Orphaned row gone.
	mem, err := GetMemory(db, "orphan_key", "global", "")
	require.NoError(t, err)
	assert.Nil(t, mem)

	// Live row survives.
	live, err := GetMemory(db, "live_key", "global", "")
	require.NoError(t, err)
	assert.NotNil(t, live)
}

func TestGCOrphanedMemory_PreservesPinned(t *testing.T) {
	t.Parallel()
	db, cleanup := setupMemoryTestDB(t)
	t.Cleanup(cleanup)

	// Pinned memory whose source task does not exist — must NOT be reaped.
	seedMemoryWithProvenance(t, db, "pinned_orphan", "val", "global", "", true, nil, "task_missing")

	_, deleted, err := GCOrphanedMemoryWithEventIdempotent(db, "agent", "req_pinned", 100, false)
	require.NoError(t, err)
	assert.Equal(t, 0, deleted)

	mem, err := GetMemory(db, "pinned_orphan", "global", "")
	require.NoError(t, err)
	assert.NotNil(t, mem)
}

func TestGCOrphanedMemory_IncludeFailed(t *testing.T) {
	t.Parallel()
	db, cleanup := setupMemoryTestDB(t)
	t.Cleanup(cleanup)

	// Task exists but is failure-blocked.
	insertTaskWithStatus(t, db, "task_failed", "blocked", "failure:boom")
	seedMemoryWithProvenance(t, db, "failed_mem", "val", "global", "", false, nil, "task_failed")

	// With includeFailed=false: memory survives.
	_, deleted, err := GCOrphanedMemoryWithEventIdempotent(db, "agent", "req_nofail", 100, false)
	require.NoError(t, err)
	assert.Equal(t, 0, deleted)

	mem, err := GetMemory(db, "failed_mem", "global", "")
	require.NoError(t, err)
	assert.NotNil(t, mem, "memory should survive when includeFailed=false")

	// With includeFailed=true (fresh requestID): memory is reaped.
	_, deleted2, err := GCOrphanedMemoryWithEventIdempotent(db, "agent", "req_fail", 100, true)
	require.NoError(t, err)
	assert.Equal(t, 1, deleted2)

	mem2, err := GetMemory(db, "failed_mem", "global", "")
	require.NoError(t, err)
	assert.Nil(t, mem2, "memory should be reaped when includeFailed=true")
}

func TestGCOrphanedMemory_Idempotent(t *testing.T) {
	t.Parallel()
	db, cleanup := setupMemoryTestDB(t)
	t.Cleanup(cleanup)

	seedMemoryWithProvenance(t, db, "idem_key", "val", "global", "", false, nil, "task_noexist")

	eid1, deleted1, err := GCOrphanedMemoryWithEventIdempotent(db, "agent", "req_idem", 100, false)
	require.NoError(t, err)
	assert.Equal(t, 1, deleted1)

	// Same agent + requestID: replayed result, no double-delete.
	eid2, deleted2, err := GCOrphanedMemoryWithEventIdempotent(db, "agent", "req_idem", 100, false)
	require.NoError(t, err)
	assert.Equal(t, eid1, eid2, "replayed call must return same eventID")
	assert.Equal(t, deleted1, deleted2, "replayed call must return same deleted count")
}

func TestGCOrphanedMemory_IgnoresNoProvenance(t *testing.T) {
	t.Parallel()
	db, cleanup := setupMemoryTestDB(t)
	t.Cleanup(cleanup)

	// Memory with no source_task_id at all — must never be touched.
	err := Transact(context.Background(), db, func(tx *sql.Tx) error {
		_, txErr := UpsertMemoryTx(tx, "test-agent", "noprov_key", "val", "string", "global", "", nil, false, "fact", nil, nil, "")
		return txErr
	})
	require.NoError(t, err)

	_, deleted, err := GCOrphanedMemoryWithEventIdempotent(db, "agent", "req_noprov", 100, false)
	require.NoError(t, err)
	assert.Equal(t, 0, deleted)

	mem, err := GetMemory(db, "noprov_key", "global", "")
	require.NoError(t, err)
	assert.NotNil(t, mem)
}
