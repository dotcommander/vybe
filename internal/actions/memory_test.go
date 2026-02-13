package actions

import (
	"strconv"
	"testing"
	"time"

	"github.com/dotcommander/vybe/internal/store"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseExpiresIn_Valid(t *testing.T) {
	tests := []struct {
		duration string
		want     time.Duration
	}{
		{"1h", 1 * time.Hour},
		{"24h", 24 * time.Hour},
		{"30m", 30 * time.Minute},
		{"168h", 7 * 24 * time.Hour}, // 7 days
		{"1h30m", 90 * time.Minute},
	}

	for _, tt := range tests {
		t.Run(tt.duration, func(t *testing.T) {
			result, err := ParseExpiresIn(tt.duration)
			require.NoError(t, err)
			require.NotNil(t, result)

			// Check that the expiration is approximately correct (within 1 second)
			expectedTime := time.Now().Add(tt.want)
			diff := result.Sub(expectedTime).Abs()
			assert.Less(t, diff, 1*time.Second)
		})
	}
}

func TestParseExpiresIn_Empty(t *testing.T) {
	result, err := ParseExpiresIn("")
	assert.NoError(t, err)
	assert.Nil(t, result)
}

func TestParseExpiresIn_Invalid(t *testing.T) {
	tests := []string{
		"invalid",
		"1x",
		"abc",
		"",
	}

	for _, duration := range tests {
		t.Run(duration, func(t *testing.T) {
			if duration == "" {
				// Empty is valid (returns nil)
				return
			}
			result, err := ParseExpiresIn(duration)
			assert.Error(t, err)
			assert.Nil(t, result)
		})
	}
}

func TestMemoryUpsertIdempotent(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	result, err := MemoryUpsertIdempotent(db, "agent1", "req_mem_upsert_1", " API Key ", "secret", "", "global", "", nil, nil, nil)
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, "api_key", result.CanonicalKey)
	assert.False(t, result.Reinforced)

	reinforced, err := MemoryUpsertIdempotent(db, "agent1", "req_mem_upsert_2", "api_key", "secret", "", "global", "", nil, nil, nil)
	require.NoError(t, err)
	require.NotNil(t, reinforced)
	assert.True(t, reinforced.Reinforced)

	mem, err := store.GetMemory(db, "api_key", "global", "")
	require.NoError(t, err)
	require.NotNil(t, mem)
	assert.Equal(t, "api_key", mem.Canonical)
}

func TestMemoryUpsertIdempotent_RejectsInvalidConfidence(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	bad := 1.5
	result, err := MemoryUpsertIdempotent(db, "agent1", "req_mem_upsert_bad", "k", "v", "", "global", "", nil, &bad, nil)
	require.Error(t, err)
	assert.Nil(t, result)
}

func TestMemoryGet_IncludeSuperseded(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	_, err := MemorySet(db, "agent-a", "k1", "v1", "", "global", "", nil)
	require.NoError(t, err)

	// Supersede it
	_, err = db.Exec(`UPDATE memory SET superseded_by = 'memory_x' WHERE key = 'k1' AND scope = 'global'`)
	require.NoError(t, err)

	// Not found without include-superseded
	_, err = MemoryGet(db, "k1", "global", "", false)
	require.ErrorContains(t, err, "not found")

	// Found with include-superseded
	mem, err := MemoryGet(db, "k1", "global", "", true)
	require.NoError(t, err)
	require.Equal(t, "v1", mem.Value)
}

func TestMemoryList_IncludeSuperseded(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	_, err := MemorySet(db, "agent-a", "x1", "v1", "", "global", "", nil)
	require.NoError(t, err)
	_, err = MemorySet(db, "agent-a", "x2", "v2", "", "global", "", nil)
	require.NoError(t, err)

	_, err = db.Exec(`UPDATE memory SET superseded_by = 'memory_x' WHERE key = 'x2' AND scope = 'global'`)
	require.NoError(t, err)

	// Without superseded
	list, err := MemoryList(db, "global", "", false)
	require.NoError(t, err)
	require.Len(t, list, 1)

	// With superseded
	list, err = MemoryList(db, "global", "", true)
	require.NoError(t, err)
	require.Len(t, list, 2)
}

func TestMemoryUpsertIdempotent_RejectsInvalidValueType(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	_, err := MemoryUpsertIdempotent(db, "agent1", "req_bad_vt", "k", "v", "invalid_type", "global", "", nil, nil, nil)
	require.Error(t, err)
	require.Contains(t, err.Error(), "invalid value_type")
}

func TestMemoryTouchIdempotent(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	_, err := MemorySet(db, "agent-a", "t1", "v", "", "global", "", nil)
	require.NoError(t, err)

	result, err := MemoryTouchIdempotent(db, "agent-a", "req_touch_act_1", "t1", "global", "", 0.1)
	require.NoError(t, err)
	require.Greater(t, result.Confidence, 0.5)
	require.Greater(t, result.EventID, int64(0))
}

func TestMemoryTouchIdempotent_RequiresAgent(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	_, err := MemoryTouchIdempotent(db, "", "req1", "k", "global", "", 0.1)
	require.ErrorContains(t, err, "agent name is required")

	_, err = MemoryTouchIdempotent(db, "agent", "", "k", "global", "", 0.1)
	require.ErrorContains(t, err, "request id is required")
}

func TestMemoryQuery(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	_, err := MemorySet(db, "agent-a", "q_alpha", "a", "", "global", "", nil)
	require.NoError(t, err)
	_, err = MemorySet(db, "agent-a", "q_beta", "b", "", "global", "", nil)
	require.NoError(t, err)
	_, err = MemorySet(db, "agent-a", "other", "c", "", "global", "", nil)
	require.NoError(t, err)

	results, err := MemoryQuery(db, "global", "", "q_%", 10)
	require.NoError(t, err)
	require.Len(t, results, 2)
}

func TestMemoryCompactAndGCIdempotent(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	for i := 0; i < 4; i++ {
		require.NoError(t, store.SetMemory(db, "k"+strconv.Itoa(i), "v", "string", "project", "proj-a", nil))
	}

	compact, err := MemoryCompactIdempotent(db, "agent1", "req_compact_action", "project", "proj-a", 0, 1)
	require.NoError(t, err)
	require.NotNil(t, compact)
	assert.Equal(t, 3, compact.Compacted)

	expired := time.Now().UTC().Add(-1 * time.Hour)
	require.NoError(t, store.SetMemory(db, "expired", "v", "string", "global", "", &expired))

	gc, err := MemoryGCIdempotent(db, "agent1", "req_gc_action", 100)
	require.NoError(t, err)
	require.NotNil(t, gc)
	assert.GreaterOrEqual(t, gc.Deleted, 1)
}
