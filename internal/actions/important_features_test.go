package actions

import (
	"testing"
	"time"

	"github.com/dotcommander/vybe/internal/models"
	"github.com/dotcommander/vybe/internal/store"
	"github.com/stretchr/testify/require"
)

func TestArtifactActions_AddGetList(t *testing.T) {
	db, cleanup := setupTestDBWithCleanup(t)
	defer cleanup()

	task, err := store.CreateTask(db, "task", "desc", "", 0)
	require.NoError(t, err)

	artifact, eventID, err := ArtifactAdd(db, "agent-a", task.ID, "/tmp/out.txt", "text/plain")
	require.NoError(t, err)
	require.NotNil(t, artifact)
	require.Greater(t, eventID, int64(0))

	got, err := ArtifactGet(db, artifact.ID)
	require.NoError(t, err)
	require.Equal(t, artifact.ID, got.ID)

	list, err := ArtifactListByTask(db, task.ID, 10)
	require.NoError(t, err)
	require.NotEmpty(t, list)
}

func TestArtifactAdd_RequiresAgent(t *testing.T) {
	db, cleanup := setupTestDBWithCleanup(t)
	defer cleanup()

	_, _, err := ArtifactAdd(db, "", "task-1", "/tmp/out.txt", "")
	require.ErrorContains(t, err, "agent name is required")
}

func TestMemoryActions_SetGetListDelete(t *testing.T) {
	db, cleanup := setupTestDBWithCleanup(t)
	defer cleanup()

	eventID, err := MemorySet(db, "agent-a", "k1", "v1", "", "global", "", nil)
	require.NoError(t, err)
	require.Greater(t, eventID, int64(0))

	mem, err := MemoryGet(db, "k1", "global", "", false)
	require.NoError(t, err)
	require.Equal(t, "k1", mem.Key)

	list, err := MemoryList(db, "global", "", false)
	require.NoError(t, err)
	require.NotEmpty(t, list)

	deleteEventID, err := MemoryDelete(db, "agent-a", "k1", "global", "")
	require.NoError(t, err)
	require.Greater(t, deleteEventID, int64(0))

	_, err = MemoryGet(db, "k1", "global", "", false)
	require.ErrorContains(t, err, "memory entry not found")
}

func TestMemoryDeleteIdempotent_ReplayWrapper(t *testing.T) {
	db, cleanup := setupTestDBWithCleanup(t)
	defer cleanup()

	_, err := MemorySet(db, "agent-a", "k2", "v2", "", "global", "", nil)
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

func TestProjectActions_CreateGetListAndFocus(t *testing.T) {
	db, cleanup := setupTestDBWithCleanup(t)
	defer cleanup()

	project, eventID, err := ProjectCreate(db, "agent-a", "proj-1", `{"x":1}`)
	require.NoError(t, err)
	require.NotNil(t, project)
	require.Greater(t, eventID, int64(0))

	got, err := ProjectGet(db, project.ID)
	require.NoError(t, err)
	require.Equal(t, project.ID, got.ID)

	projects, err := ProjectList(db)
	require.NoError(t, err)
	require.NotEmpty(t, projects)

	focusEventID, err := ProjectFocus(db, "agent-a", project.ID)
	require.NoError(t, err)
	require.Greater(t, focusEventID, int64(0))
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

	project, _, err := ProjectCreate(db, "agent-a", "proj-focus", "")
	require.NoError(t, err)

	e1, err := ProjectFocusIdempotent(db, "agent-a", "req-focus-1", project.ID)
	require.NoError(t, err)
	e2, err := ProjectFocusIdempotent(db, "agent-a", "req-focus-1", project.ID)
	require.NoError(t, err)
	require.Equal(t, e1, e2)
}

func TestTaskStartAndDependencyActions(t *testing.T) {
	db, cleanup := setupTestDBWithCleanup(t)
	defer cleanup()

	a, err := store.CreateTask(db, "a", "", "", 0)
	require.NoError(t, err)
	b, err := store.CreateTask(db, "b", "", "", 0)
	require.NoError(t, err)

	updated, statusEventID, focusEventID, err := TaskStart(db, "agent-a", a.ID)
	require.NoError(t, err)
	require.Equal(t, models.TaskStatusInProgress, updated.Status)
	require.GreaterOrEqual(t, statusEventID, int64(0))
	require.Greater(t, focusEventID, int64(0))

	err = TaskAddDependency(db, "agent-a", a.ID, b.ID)
	require.NoError(t, err)

	err = TaskRemoveDependency(db, "agent-a", a.ID, b.ID)
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
