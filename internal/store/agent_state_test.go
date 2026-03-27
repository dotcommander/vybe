package store

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestLoadOrCreateAgentState(t *testing.T) {
	t.Parallel()
	db, cleanup := setupTestDB(t)
	defer cleanup()

	state, err := LoadOrCreateAgentState(db, "test-agent")
	require.NoError(t, err)
	require.Equal(t, "test-agent", state.AgentName)
	require.Equal(t, int64(0), state.LastSeenEventID)
	require.Equal(t, 1, state.Version)

	state2, err := LoadOrCreateAgentState(db, "test-agent")
	require.NoError(t, err)
	require.Equal(t, state.AgentName, state2.AgentName)
}

func TestLoadOrCreateAgentState_FocusProjectIDEmpty(t *testing.T) {
	t.Parallel()
	db, cleanup := setupTestDB(t)
	defer cleanup()

	state, err := LoadOrCreateAgentState(db, "test-agent")
	require.NoError(t, err)
	require.Empty(t, state.FocusProjectID)
}

func TestUpdateAgentStateAtomic_CursorMonotonicity(t *testing.T) {
	t.Parallel()
	db, cleanup := setupTestDB(t)
	defer cleanup()

	// Create agent state
	_, err := LoadOrCreateAgentState(db, "mono-agent")
	require.NoError(t, err)

	task, err := CreateTask(db, "mono task", "", "", 0)
	require.NoError(t, err)

	// Advance cursor to 100
	require.NoError(t, UpdateAgentStateAtomic(db, "mono-agent", 100, task.ID))

	st, err := LoadOrCreateAgentState(db, "mono-agent")
	require.NoError(t, err)
	require.Equal(t, int64(100), st.LastSeenEventID)

	// Attempt backward move to 50 — cursor must stay at 100 (MAX semantics)
	require.NoError(t, UpdateAgentStateAtomic(db, "mono-agent", 50, task.ID))

	st, err = LoadOrCreateAgentState(db, "mono-agent")
	require.NoError(t, err)
	require.Equal(t, int64(100), st.LastSeenEventID, "cursor must not move backward")

	// Forward move to 200 — cursor advances
	require.NoError(t, UpdateAgentStateAtomic(db, "mono-agent", 200, task.ID))

	st, err = LoadOrCreateAgentState(db, "mono-agent")
	require.NoError(t, err)
	require.Equal(t, int64(200), st.LastSeenEventID)
}

func TestUpdateAgentStateAtomicWithProject_CursorMonotonicity(t *testing.T) {
	t.Parallel()
	db, cleanup := setupTestDB(t)
	defer cleanup()

	_, err := LoadOrCreateAgentState(db, "mono-proj-agent")
	require.NoError(t, err)

	task, err := CreateTask(db, "mono proj task", "", "", 0)
	require.NoError(t, err)

	project, err := CreateProject(db, "Mono Project", "")
	require.NoError(t, err)

	// Advance to 100 with project
	require.NoError(t, UpdateAgentStateAtomicWithProject(db, "mono-proj-agent", 100, task.ID, project.ID))

	st, err := LoadOrCreateAgentState(db, "mono-proj-agent")
	require.NoError(t, err)
	require.Equal(t, int64(100), st.LastSeenEventID)
	require.Equal(t, project.ID, st.FocusProjectID)

	// Backward move — cursor stays, project clears (empty string = clear)
	require.NoError(t, UpdateAgentStateAtomicWithProject(db, "mono-proj-agent", 50, task.ID, ""))

	st, err = LoadOrCreateAgentState(db, "mono-proj-agent")
	require.NoError(t, err)
	require.Equal(t, int64(100), st.LastSeenEventID, "cursor must not move backward")
	require.Empty(t, st.FocusProjectID, "project should be cleared")
}
