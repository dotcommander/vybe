package store

import (
	"database/sql"
	"fmt"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestMemoryCanonicalRace verifies that concurrent agents writing the same canonical_key
// are deduplicated correctly by the unique partial index and race-condition retry logic.
func TestMemoryCanonicalRace(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	// Two concurrent agents write semantically equivalent keys
	// (different surface forms, same canonical form)
	const (
		agent1    = "agent-1"
		agent2    = "agent-2"
		key1      = "API Key"
		key2      = "api_key"
		value     = "secret123"
		valueType = "string"
		scope     = "global"
		scopeID   = ""
	)

	// Both keys should normalize to the same canonical key
	canonical := NormalizeMemoryKey(key1)
	require.Equal(t, canonical, NormalizeMemoryKey(key2), "test precondition: keys must normalize to same canonical form")

	var wg sync.WaitGroup
	results := make(chan error, 2)

	// Agent 1 upserts with key1
	wg.Add(1)
	go func() {
		defer wg.Done()
		_, _, _, _, err := UpsertMemoryWithEventIdempotent(
			db, agent1, "req-1", key1, value, valueType, scope, scopeID, nil, nil, nil,
		)
		results <- err
	}()

	// Agent 2 upserts with key2 (same canonical key)
	wg.Add(1)
	go func() {
		defer wg.Done()
		_, _, _, _, err := UpsertMemoryWithEventIdempotent(
			db, agent2, "req-2", key2, value, valueType, scope, scopeID, nil, nil, nil,
		)
		results <- err
	}()

	wg.Wait()
	close(results)

	// Both should succeed (one inserts, one hits race condition and updates)
	for err := range results {
		require.NoError(t, err, "concurrent upserts should both succeed via race-condition retry")
	}

	// Verify only one active memory row exists with this canonical key
	memories, err := ListMemoryWithOptions(db, scope, scopeID, MemoryReadOptions{})
	require.NoError(t, err)

	activeWithCanonical := 0
	for _, mem := range memories {
		if mem.Canonical == canonical {
			activeWithCanonical++
		}
	}

	assert.Equal(t, 1, activeWithCanonical, "should have exactly one active memory row with canonical key %q", canonical)
}

// TestMemoryCanonicalUniqueness verifies the unique partial index prevents duplicate
// canonical keys when superseded_by IS NULL.
func TestMemoryCanonicalUniqueness(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	const (
		scope   = "global"
		scopeID = ""
	)

	// Insert first memory entry
	_, _, _, _, err := UpsertMemoryWithEventIdempotent(
		db, "agent-1", "req-1", "test_key", "value1", "string", scope, scopeID, nil, nil, nil,
	)
	require.NoError(t, err)

	// Second agent tries to insert the same canonical key (bypassing idempotency)
	// This should trigger the race-condition retry path
	_, _, _, _, err = UpsertMemoryWithEventIdempotent(
		db, "agent-2", "req-2", "Test Key", "value2", "string", scope, scopeID, nil, nil, nil,
	)
	require.NoError(t, err, "second upsert should succeed via race-condition retry")

	// Verify only one active entry
	memories, err := ListMemoryWithOptions(db, scope, scopeID, MemoryReadOptions{})
	require.NoError(t, err)

	canonical := NormalizeMemoryKey("test_key")
	count := 0
	for _, mem := range memories {
		if mem.Canonical == canonical {
			count++
		}
	}

	assert.Equal(t, 1, count, "should have exactly one active memory with canonical key %q", canonical)
}

// TestMemoryCanonicalSupersededAllowed verifies that the unique partial index allows
// multiple rows with the same canonical key when they are superseded (WHERE superseded_by IS NULL).
func TestMemoryCanonicalSupersededAllowed(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	const (
		scope   = "task"
		scopeID = "task_123"
	)

	// Create first memory entry
	_, _, _, _, err := UpsertMemoryWithEventIdempotent(
		db, "agent", "req-1", "test_key", "old_value", "string", scope, scopeID, nil, nil, nil,
	)
	require.NoError(t, err)

	// Get the memory to supersede it manually (simulating compaction)
	mem, err := GetMemory(db, "test_key", scope, scopeID)
	require.NoError(t, err)
	require.NotNil(t, mem)

	// Manually supersede the first entry (simulating memory compaction)
	err = Transact(db, func(tx *sql.Tx) error {
		_, txErr := tx.Exec(`UPDATE memory SET superseded_by = ? WHERE id = ?`, "memory_summary", mem.ID)
		return txErr
	})
	require.NoError(t, err)

	// Now insert a new entry with the same canonical key
	// This should succeed because the first entry is superseded (excluded from unique index)
	_, _, _, _, err = UpsertMemoryWithEventIdempotent(
		db, "agent", "req-2", "Test Key", "new_value", "string", scope, scopeID, nil, nil, nil,
	)
	require.NoError(t, err)

	// Verify both entries exist (one superseded, one active)
	memories, err := ListMemoryWithOptions(db, scope, scopeID, MemoryReadOptions{IncludeSuperseded: true})
	require.NoError(t, err)

	canonical := NormalizeMemoryKey("test_key")
	supersededCount := 0
	activeCount := 0

	for _, mem := range memories {
		if mem.Canonical == canonical {
			if mem.SupersededBy != "" {
				supersededCount++
			} else {
				activeCount++
			}
		}
	}

	assert.Equal(t, 1, supersededCount, "should have one superseded entry")
	assert.Equal(t, 1, activeCount, "should have one active entry")
}

// TestMemoryCanonicalCrossScope verifies that the unique index is scoped per (scope, scope_id)
// and allows same canonical_key across different scopes.
func TestMemoryCanonicalCrossScope(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	const key = "shared_key"
	canonical := NormalizeMemoryKey(key)

	// Insert in global scope
	_, _, _, _, err := UpsertMemoryWithEventIdempotent(
		db, "agent", "req-1", key, "global_value", "string", "global", "", nil, nil, nil,
	)
	require.NoError(t, err)

	// Insert in task scope
	_, _, _, _, err = UpsertMemoryWithEventIdempotent(
		db, "agent", "req-2", key, "task_value", "string", "task", "task_123", nil, nil, nil,
	)
	require.NoError(t, err)

	// Insert in agent scope
	_, _, _, _, err = UpsertMemoryWithEventIdempotent(
		db, "agent", "req-3", key, "agent_value", "string", "agent", "agent_1", nil, nil, nil,
	)
	require.NoError(t, err)

	// Verify each scope has one entry with the canonical key
	scopes := []struct {
		scope   string
		scopeID string
	}{
		{"global", ""},
		{"task", "task_123"},
		{"agent", "agent_1"},
	}

	for _, s := range scopes {
		memories, err := ListMemoryWithOptions(db, s.scope, s.scopeID, MemoryReadOptions{})
		require.NoError(t, err)

		count := 0
		for _, mem := range memories {
			if mem.Canonical == canonical {
				count++
			}
		}

		assert.Equal(t, 1, count, "scope %s/%s should have exactly one entry with canonical key %q", s.scope, s.scopeID, canonical)
	}
}

// TestMemoryRaceConditionRetryPath verifies that when two agents race on the same canonical key,
// the retry path correctly loads the existing entry and updates it.
func TestMemoryRaceConditionRetryPath(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	const (
		key1      = "Config Setting"
		key2      = "config_setting" // same canonical key
		value1    = "initial"
		value2    = "updated"
		scope     = "project"
		scopeID   = "proj_123"
		valueType = "string"
	)

	// First agent inserts
	eventID1, reinforced1, conf1, canonicalKey1, err := UpsertMemoryWithEventIdempotent(
		db, "agent-1", "req-1", key1, value1, valueType, scope, scopeID, nil, nil, nil,
	)
	require.NoError(t, err)
	require.Greater(t, eventID1, int64(0))
	require.False(t, reinforced1, "first insert should not be reinforced")

	// Second agent with different key but same canonical, different value
	// This triggers the unique constraint violation and retry path
	eventID2, reinforced2, conf2, canonicalKey2, err := UpsertMemoryWithEventIdempotent(
		db, "agent-2", "req-2", key2, value2, valueType, scope, scopeID, nil, nil, nil,
	)
	require.NoError(t, err)
	require.Greater(t, eventID2, int64(0))
	require.False(t, reinforced2, "update with different value should not be reinforced")

	// Canonical keys should match
	assert.Equal(t, canonicalKey1, canonicalKey2, "both should resolve to same canonical key")

	// Verify only one memory entry exists with the second agent's value
	// After the race-condition update, the key column will be key2 (the last writer's key)
	mem, err := GetMemory(db, key2, scope, scopeID) // Query by updated key
	require.NoError(t, err)
	require.NotNil(t, mem)

	assert.Equal(t, value2, mem.Value, "value should be updated to second agent's value")
	assert.Equal(t, canonicalKey1, mem.Canonical)

	// Verify confidence tracking works through race path
	assert.Equal(t, conf1, 0.5, "first insert should have default confidence 0.5")
	assert.Equal(t, conf2, conf1, "update without reinforcement should keep existing confidence")
}

// TestMemoryReinforcementThroughRace verifies that reinforcement detection works
// even when the race-condition retry path is triggered.
func TestMemoryReinforcementThroughRace(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	const (
		key1      = "Status"
		key2      = "status" // same canonical
		value     = "active"
		scope     = "global"
		scopeID   = ""
		valueType = "string"
	)

	// First agent inserts
	_, _, conf1, _, err := UpsertMemoryWithEventIdempotent(
		db, "agent-1", "req-1", key1, value, valueType, scope, scopeID, nil, nil, nil,
	)
	require.NoError(t, err)

	// Second agent reinforces (same value, different key form)
	// This should trigger race condition retry → detect reinforcement → bump confidence
	_, reinforced, conf2, _, err := UpsertMemoryWithEventIdempotent(
		db, "agent-2", "req-2", key2, value, valueType, scope, scopeID, nil, nil, nil,
	)
	require.NoError(t, err)
	require.True(t, reinforced, "same value through race path should be detected as reinforcement")
	assert.Greater(t, conf2, conf1, "confidence should increase on reinforcement")

	expectedConf := conf1 + 0.05
	assert.InDelta(t, expectedConf, conf2, 0.001, fmt.Sprintf("confidence should increase by 0.05 (%.3f → %.3f)", conf1, conf2))
}
