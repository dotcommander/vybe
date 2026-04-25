package store

import (
	"context"
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

	err = Transact(context.Background(), db, func(tx *sql.Tx) error {
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

	// Add an artifact linked to the task (which is in the project)
	artifact, _, err := AddArtifact(db, "agent1", task.ID, "/tmp/test-artifact.txt", "text/plain")
	require.NoError(t, err)

	// Add project-scoped memory
	err = SetMemory(db, "proj-key", "proj-value", "string", "project", project.ID, nil, false, "", nil)
	require.NoError(t, err)

	// Delete the project
	err = Transact(context.Background(), db, func(tx *sql.Tx) error {
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

	// artifact.project_id should be NULL after project deletion
	var projectIDVal sql.NullString
	err = db.QueryRowContext(context.Background(), `SELECT project_id FROM artifacts WHERE id = ?`, artifact.ID).Scan(&projectIDVal)
	require.NoError(t, err)
	assert.False(t, projectIDVal.Valid, "artifact.project_id should be NULL after project deletion")

	// project-scoped memory should be deleted (GetMemory returns nil, nil when not found)
	mem, memErr := GetMemory(db, "proj-key", "project", project.ID)
	require.NoError(t, memErr)
	assert.Nil(t, mem, "project-scoped memory should be deleted after project deletion")
}
