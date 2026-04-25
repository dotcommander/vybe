package store

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSetMemory_DefaultKindIsFact(t *testing.T) {
	t.Parallel()
	db, cleanup := setupMemoryTestDB(t)
	t.Cleanup(cleanup)

	require.NoError(t, SetMemory(db, "k", "v", "string", "global", "", nil, false, "", nil))

	var kind string
	require.NoError(t, db.QueryRow(`SELECT kind FROM memory WHERE key='k'`).Scan(&kind))
	assert.Equal(t, "fact", kind, "empty kind arg must default to 'fact'")
}

func TestSetMemory_DirectiveKind(t *testing.T) {
	t.Parallel()
	db, cleanup := setupMemoryTestDB(t)
	t.Cleanup(cleanup)

	require.NoError(t, SetMemory(db, "rule", "always respond in JSON", "string", "global", "", nil, false, "directive", nil))

	var kind string
	require.NoError(t, db.QueryRow(`SELECT kind FROM memory WHERE key='rule'`).Scan(&kind))
	assert.Equal(t, "directive", kind)
}

func TestSetMemory_RejectsInvalidKind(t *testing.T) {
	t.Parallel()
	db, cleanup := setupMemoryTestDB(t)
	t.Cleanup(cleanup)

	err := SetMemory(db, "k", "v", "string", "global", "", nil, false, "opinion", nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid kind")
}

func TestUpsertMemoryWithEventIdempotent_ReplayPreservesKind(t *testing.T) {
	t.Parallel()
	db, cleanup := setupMemoryTestDB(t)
	t.Cleanup(cleanup)

	_, err := UpsertMemoryWithEventIdempotent(db, "agent", "req-kind-replay-1", "rule", "be concise", "string", "global", "", nil, false, "directive", nil)
	require.NoError(t, err)

	// Replay must return same event and not alter kind
	_, err = UpsertMemoryWithEventIdempotent(db, "agent", "req-kind-replay-1", "rule", "be concise", "string", "global", "", nil, false, "directive", nil)
	require.NoError(t, err)

	var kind string
	require.NoError(t, db.QueryRow(`SELECT kind FROM memory WHERE key='rule'`).Scan(&kind))
	assert.Equal(t, "directive", kind, "idempotent replay must preserve kind")
}

func TestSetMemory_LessonKind(t *testing.T) {
	t.Parallel()
	db, cleanup := setupMemoryTestDB(t)
	t.Cleanup(cleanup)

	require.NoError(t, SetMemory(db, "lesson-key", "always wrap errors with context", "string", "global", "", nil, false, "lesson", nil))

	var kind string
	require.NoError(t, db.QueryRow(`SELECT kind FROM memory WHERE key='lesson-key'`).Scan(&kind))
	assert.Equal(t, "lesson", kind, "kind='lesson' must survive the CHECK constraint and read back correctly")
}

func TestUpsertMemoryWithEventIdempotent_LessonKind(t *testing.T) {
	t.Parallel()
	db, cleanup := setupMemoryTestDB(t)
	t.Cleanup(cleanup)

	_, err := UpsertMemoryWithEventIdempotent(db, "agent", "req-lesson-kind-1", "lesson-key", "prefer table-driven tests", "string", "global", "", nil, false, "lesson", nil)
	require.NoError(t, err)

	// Replay must return same event and not alter kind
	_, err = UpsertMemoryWithEventIdempotent(db, "agent", "req-lesson-kind-1", "lesson-key", "prefer table-driven tests", "string", "global", "", nil, false, "lesson", nil)
	require.NoError(t, err)

	var kind string
	require.NoError(t, db.QueryRow(`SELECT kind FROM memory WHERE key='lesson-key'`).Scan(&kind))
	assert.Equal(t, "lesson", kind, "idempotent replay must preserve kind=lesson")
}

func TestListMemory_HydratesKind(t *testing.T) {
	t.Parallel()
	db, cleanup := setupMemoryTestDB(t)
	t.Cleanup(cleanup)

	require.NoError(t, SetMemory(db, "fact-key", "v1", "string", "global", "", nil, false, "fact", nil))
	require.NoError(t, SetMemory(db, "dir-key", "v2", "string", "global", "", nil, false, "directive", nil))

	mems, err := ListMemory(db, "global", "")
	require.NoError(t, err)
	require.Len(t, mems, 2)

	byKey := make(map[string]string, 2)
	for _, m := range mems {
		byKey[m.Key] = m.Kind
	}
	assert.Equal(t, "fact", byKey["fact-key"], "ListMemory must hydrate kind=fact")
	assert.Equal(t, "directive", byKey["dir-key"], "ListMemory must hydrate kind=directive")
}
