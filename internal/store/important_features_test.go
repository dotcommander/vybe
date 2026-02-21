package store

import (
	"database/sql"
	"testing"

	"github.com/dotcommander/vybe/internal/models"
	"github.com/stretchr/testify/require"
)

func TestStartTaskAndFocus_AndIdempotent(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	task, err := CreateTask(db, "start me", "", "", 0)
	require.NoError(t, err)

	statusEventID, focusEventID, err := StartTaskAndFocus(db, "agent-a", task.ID)
	require.NoError(t, err)
	require.Greater(t, statusEventID, int64(0))
	require.Greater(t, focusEventID, int64(0))

	updated, err := GetTask(db, task.ID)
	require.NoError(t, err)
	require.Equal(t, models.TaskStatusInProgress, updated.Status)

	state, err := LoadOrCreateAgentState(db, "agent-a")
	require.NoError(t, err)
	require.Equal(t, task.ID, state.FocusTaskID)

	statusEventID2, focusEventID2, err := StartTaskAndFocusIdempotent(db, "agent-a", "req-start-1", task.ID)
	require.NoError(t, err)
	require.GreaterOrEqual(t, statusEventID2, int64(0))
	require.Greater(t, focusEventID2, int64(0))

	statusEventID3, focusEventID3, err := StartTaskAndFocusIdempotent(db, "agent-a", "req-start-1", task.ID)
	require.NoError(t, err)
	require.Equal(t, statusEventID2, statusEventID3)
	require.Equal(t, focusEventID2, focusEventID3)
}

func TestAgentStateIdempotentAndCursorHelpers(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	state1, err := LoadOrCreateAgentStateIdempotent(db, "agent-a", "req-agent-1")
	require.NoError(t, err)
	state2, err := LoadOrCreateAgentStateIdempotent(db, "agent-a", "req-agent-1")
	require.NoError(t, err)
	require.Equal(t, state1.AgentName, state2.AgentName)

	task, err := CreateTask(db, "focus task", "", "", 0)
	require.NoError(t, err)

	focusEventID, err := SetAgentFocusTaskWithEvent(db, "agent-a", task.ID)
	require.NoError(t, err)
	require.Greater(t, focusEventID, int64(0))

	project, err := CreateProject(db, "proj", "")
	require.NoError(t, err)

	projEvent1, err := SetAgentFocusProjectWithEventIdempotent(db, "agent-a", "req-proj-focus", project.ID)
	require.NoError(t, err)
	projEvent2, err := SetAgentFocusProjectWithEventIdempotent(db, "agent-a", "req-proj-focus", project.ID)
	require.NoError(t, err)
	require.Equal(t, projEvent1, projEvent2)

	require.NoError(t, UpdateAgentStateAtomicWithProject(db, "agent-a", 42, task.ID, project.ID))

	err = Transact(db, func(tx *sql.Tx) error {
		cf, txErr := LoadAgentCursorAndFocusTx(tx, "agent-a")
		require.NoError(t, txErr)
		require.Equal(t, int64(42), cf.Cursor)
		require.Equal(t, task.ID, cf.TaskID)
		require.Equal(t, project.ID, cf.ProjectID)

		require.NoError(t, UpdateAgentStateAtomicTx(tx, "agent-a", 45, task.ID))
		require.NoError(t, UpdateAgentStateAtomicWithProjectTx(tx, "agent-a", 50, task.ID, ""))
		return nil
	})
	require.NoError(t, err)

	st, err := GetAgentState(db, "agent-a")
	require.NoError(t, err)
	require.Equal(t, int64(50), st.LastSeenEventID)
	require.Equal(t, task.ID, st.FocusTaskID)
	require.Empty(t, st.FocusProjectID)
}

func TestEventIdempotentVariantsWithProjectAndMetadata(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	project, err := CreateProject(db, "Project X", "")
	require.NoError(t, err)

	// Insert event with project.
	err = Transact(db, func(tx *sql.Tx) error {
		id, insertErr := InsertEventWithProjectTx(tx, "user_prompt", "agent-a", project.ID, "", "hello", `{"project":"`+project.ID+`"}`)
		require.NoError(t, insertErr)
		require.Greater(t, id, int64(0))
		return nil
	})
	require.NoError(t, err)

	id1, err := AppendEventWithProjectAndMetadataIdempotent(db, "agent-a", "req-evt-proj-1", "user_prompt", project.ID, "", "msg", `{"project":"`+project.ID+`"}`)
	require.NoError(t, err)
	id2, err := AppendEventWithProjectAndMetadataIdempotent(db, "agent-a", "req-evt-proj-1", "user_prompt", project.ID, "", "msg", `{"project":"`+project.ID+`"}`)
	require.NoError(t, err)
	require.Equal(t, id1, id2)

	m1, err := AppendEventWithMetadataIdempotent(db, "agent-a", "req-evt-meta-1", "memory_set", "", "memo", `{"k":"v"}`)
	require.NoError(t, err)
	m2, err := AppendEventWithMetadataIdempotent(db, "agent-a", "req-evt-meta-1", "memory_set", "", "memo", `{"k":"v"}`)
	require.NoError(t, err)
	require.Equal(t, m1, m2)
}

func TestAddArtifactIdempotent_Replay(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	task, err := CreateTask(db, "art task", "", "", 0)
	require.NoError(t, err)

	a1, e1, err := AddArtifactIdempotent(db, "agent-a", "req-art-1", task.ID, "/tmp/a.txt", "text/plain")
	require.NoError(t, err)
	a2, e2, err := AddArtifactIdempotent(db, "agent-a", "req-art-1", task.ID, "/tmp/a.txt", "text/plain")
	require.NoError(t, err)
	require.Equal(t, a1.ID, a2.ID)
	require.Equal(t, e1, e2)
}

func TestDeleteMemoryWithEventIdempotent_Replay(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	require.NoError(t, SetMemory(db, "key-x", "value-x", "string", "global", "", nil))

	e1, err := DeleteMemoryWithEventIdempotent(db, "agent-a", "req-mem-del-1", "key-x", "global", "")
	require.NoError(t, err)
	e2, err := DeleteMemoryWithEventIdempotent(db, "agent-a", "req-mem-del-1", "key-x", "global", "")
	require.NoError(t, err)
	require.Equal(t, e1, e2)
}

func TestTaskTxHelpers_GetTaskTxAndUpdateWithEvent(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	task, err := CreateTask(db, "tx task", "", "", 0)
	require.NoError(t, err)

	err = Transact(db, func(tx *sql.Tx) error {
		fetched, getErr := getTaskTx(tx, task.ID)
		require.NoError(t, getErr)
		require.Equal(t, task.ID, fetched.ID)

		version, verErr := GetTaskVersionTx(tx, task.ID)
		require.NoError(t, verErr)

		eventID, updErr := UpdateTaskStatusWithEventTx(tx, "agent-a", task.ID, "in_progress", version)
		require.NoError(t, updErr)
		require.Greater(t, eventID, int64(0))
		return nil
	})
	require.NoError(t, err)

	updated, err := GetTask(db, task.ID)
	require.NoError(t, err)
	require.Equal(t, models.TaskStatusInProgress, updated.Status)
}
