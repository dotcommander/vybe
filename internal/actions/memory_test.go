package actions

import (
	"testing"
	"time"

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

func TestMemorySetIdempotent_RejectsInvalidValueType(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	_, err := MemorySetIdempotent(db, "agent1", "req_bad_vt", "k", "v", "invalid_type", "global", "", nil)
	require.Error(t, err)
	require.Contains(t, err.Error(), "invalid value_type")
}

func TestMemoryGet_NotFound(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	_, err := MemoryGet(db, "nonexistent", "global", "")
	require.ErrorContains(t, err, "not found")
}

func TestMemoryGet_Found(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	_, err := MemorySetIdempotent(db, "agent-a", "req-mem-get-1", "k1", "v1", "", "global", "", nil)
	require.NoError(t, err)

	mem, err := MemoryGet(db, "k1", "global", "")
	require.NoError(t, err)
	require.Equal(t, "v1", mem.Value)
}

func TestMemoryList_Basic(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	_, err := MemorySetIdempotent(db, "agent-a", "req-mem-list-1", "x1", "v1", "", "global", "", nil)
	require.NoError(t, err)
	_, err = MemorySetIdempotent(db, "agent-a", "req-mem-list-2", "x2", "v2", "", "global", "", nil)
	require.NoError(t, err)

	list, err := MemoryList(db, "global", "")
	require.NoError(t, err)
	require.Len(t, list, 2)
}

func TestMemoryGCIdempotent(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	expired := time.Now().UTC().Add(-1 * time.Hour)
	_, err := MemorySetIdempotent(db, "agent1", "req_expire_setup", "expired", "v", "string", "global", "", &expired)
	require.NoError(t, err)

	gc, err := MemoryGCIdempotent(db, "agent1", "req_gc_action", 100)
	require.NoError(t, err)
	require.NotNil(t, gc)
	assert.GreaterOrEqual(t, gc.Deleted, 1)
}
