package store

import (
	"database/sql"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDeleteProject(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	project, err := CreateProject(db, "Delete me", "")
	require.NoError(t, err)

	err = Transact(db, func(tx *sql.Tx) error {
		return DeleteProjectTx(tx, project.ID)
	})
	require.NoError(t, err)

	// Verify project is gone
	_, err = GetProject(db, project.ID)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}

func TestDeleteProject_ClearsReferences(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	project, err := CreateProject(db, "Project X", "")
	require.NoError(t, err)

	// Create a task in the project
	task, err := CreateTask(db, "Task in project", "", project.ID, 0)
	require.NoError(t, err)

	// Create an event linked to the project
	appendEvent(t, db, "test.event", "agent1", task.ID, "event in project")

	// Create agent state with focus on project
	_, err = LoadOrCreateAgentState(db, "agent1")
	require.NoError(t, err)
	_, err = SetAgentFocusProjectWithEventIdempotent(db, "agent1", "req-del-proj-focus", project.ID)
	require.NoError(t, err)

	// Delete the project
	err = Transact(db, func(tx *sql.Tx) error {
		return DeleteProjectTx(tx, project.ID)
	})
	require.NoError(t, err)

	// task.project_id should be cleared
	updatedTask, err := GetTask(db, task.ID)
	require.NoError(t, err)
	assert.Empty(t, updatedTask.ProjectID)

	// agent_state.focus_project_id should be cleared
	state, err := LoadOrCreateAgentState(db, "agent1")
	require.NoError(t, err)
	assert.Empty(t, state.FocusProjectID)
}
