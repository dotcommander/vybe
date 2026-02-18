package actions

import (
	"testing"
	"time"

	"github.com/dotcommander/vybe/internal/store"
	"github.com/stretchr/testify/require"
)

func TestMemoryDeleteIdempotent_ReplayWrapper(t *testing.T) {
	db, cleanup := setupTestDBWithCleanup(t)
	defer cleanup()

	_, err := MemorySetIdempotent(db, "agent-a", "req-mem-setup-1", "k2", "v2", "", "global", "", nil)
	require.NoError(t, err)

	first, err := MemoryDeleteIdempotent(db, "agent-a", "req-del-1", "k2", "global", "")
	require.NoError(t, err)
	second, err := MemoryDeleteIdempotent(db, "agent-a", "req-del-1", "k2", "global", "")
	require.NoError(t, err)
	require.Equal(t, first, second)
}

func TestParseMaxAge_SupportsShorthandAndEmpty(t *testing.T) {
	d, err := ParseMaxAge("14d")
	require.NoError(t, err)
	require.Equal(t, 14*24*time.Hour, d)

	d, err = ParseMaxAge("")
	require.NoError(t, err)
	require.Equal(t, time.Duration(0), d)
}

func TestProjectCreateIdempotent_Replay(t *testing.T) {
	db, cleanup := setupTestDBWithCleanup(t)
	defer cleanup()

	p1, e1, err := ProjectCreateIdempotent(db, "agent-a", "req-proj-1", "proj-x", "")
	require.NoError(t, err)
	p2, e2, err := ProjectCreateIdempotent(db, "agent-a", "req-proj-1", "proj-x", "")
	require.NoError(t, err)
	require.Equal(t, p1.ID, p2.ID)
	require.Equal(t, e1, e2)
}

func TestProjectFocusIdempotent_Replay(t *testing.T) {
	db, cleanup := setupTestDBWithCleanup(t)
	defer cleanup()

	project, _, err := ProjectCreateIdempotent(db, "agent-a", "req-proj-focus-setup", "proj-focus", "")
	require.NoError(t, err)

	e1, err := ProjectFocusIdempotent(db, "agent-a", "req-focus-1", project.ID)
	require.NoError(t, err)
	e2, err := ProjectFocusIdempotent(db, "agent-a", "req-focus-1", project.ID)
	require.NoError(t, err)
	require.Equal(t, e1, e2)
}

func TestTaskStartIdempotentAndDependencyActions(t *testing.T) {
	db, cleanup := setupTestDBWithCleanup(t)
	defer cleanup()

	a, err := store.CreateTask(db, "a", "", "", 0)
	require.NoError(t, err)
	b, err := store.CreateTask(db, "b", "", "", 0)
	require.NoError(t, err)

	startResult, err := TaskStartIdempotent(db, "agent-a", "req-start-1", a.ID)
	require.NoError(t, err)
	require.GreaterOrEqual(t, startResult.StatusEventID, int64(0))
	require.Greater(t, startResult.FocusEventID, int64(0))

	err = TaskAddDependencyIdempotent(db, "agent-a", "req-dep-add-1", a.ID, b.ID)
	require.NoError(t, err)

	err = TaskRemoveDependencyIdempotent(db, "agent-a", "req-dep-rm-1", a.ID, b.ID)
	require.NoError(t, err)
}

func TestTaskAddDependencyIdempotent_Replay(t *testing.T) {
	db, cleanup := setupTestDBWithCleanup(t)
	defer cleanup()

	a, err := store.CreateTask(db, "a", "", "", 0)
	require.NoError(t, err)
	b, err := store.CreateTask(db, "b", "", "", 0)
	require.NoError(t, err)

	err = TaskAddDependencyIdempotent(db, "agent-a", "req-dep-1", a.ID, b.ID)
	require.NoError(t, err)
	err = TaskAddDependencyIdempotent(db, "agent-a", "req-dep-1", a.ID, b.ID)
	require.NoError(t, err)
}
