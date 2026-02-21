package store

import (
	"database/sql"
	"encoding/json"
	"testing"

	"github.com/dotcommander/vybe/internal/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCloseTaskTx_Done(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()
	var err error

	task, err := CreateTask(db, "closable", "", "", 0)
	require.NoError(t, err)

	// Claim and start the task first.
	_, _, err = StartTaskAndFocus(db, "agent1", task.ID)
	require.NoError(t, err)

	var result *CloseTaskResult
	require.NoError(t, Transact(db, func(tx *sql.Tx) error {
		r, txErr := CloseTaskTx(tx, CloseTaskParams{
			AgentName: "agent1", TaskID: task.ID,
			Status: "completed", Summary: "All done",
		})
		if txErr != nil {
			return txErr
		}
		result = r
		return nil
	}))

	assert.NotZero(t, result.StatusEventID)
	assert.NotZero(t, result.CloseEventID)

	// Verify task is completed.
	updated, err := GetTask(db, task.ID)
	require.NoError(t, err)
	assert.Equal(t, models.TaskStatusCompleted, updated.Status)
}

func TestCloseTaskTx_DoneUnblocksDependents(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()
	var err error

	blocker, err := CreateTask(db, "blocker", "", "", 0)
	require.NoError(t, err)
	dependent, err := CreateTask(db, "dependent", "", "", 0)
	require.NoError(t, err)
	require.NoError(t, AddTaskDependency(db, dependent.ID, blocker.ID))

	// Verify dependent is blocked.
	dep, err := GetTask(db, dependent.ID)
	require.NoError(t, err)
	assert.Equal(t, models.TaskStatusBlocked, dep.Status)

	// Start then close the blocker.
	_, _, err = StartTaskAndFocus(db, "agent1", blocker.ID)
	require.NoError(t, err)

	require.NoError(t, Transact(db, func(tx *sql.Tx) error {
		_, txErr := CloseTaskTx(tx, CloseTaskParams{
			AgentName: "agent1", TaskID: blocker.ID,
			Status: "completed", Summary: "Done blocking",
		})
		return txErr
	}))

	// Verify dependent is unblocked.
	dep, err = GetTask(db, dependent.ID)
	require.NoError(t, err)
	assert.Equal(t, models.TaskStatusPending, dep.Status)
}

func TestCloseTaskTx_Blocked(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()
	var err error

	task, err := CreateTask(db, "will-block", "", "", 0)
	require.NoError(t, err)

	_, _, err = StartTaskAndFocus(db, "agent1", task.ID)
	require.NoError(t, err)

	var result *CloseTaskResult
	require.NoError(t, Transact(db, func(tx *sql.Tx) error {
		r, txErr := CloseTaskTx(tx, CloseTaskParams{
			AgentName: "agent1", TaskID: task.ID,
			Status: "blocked", Summary: "Waiting on external API",
			BlockedReason: "failure:api_timeout",
		})
		if txErr != nil {
			return txErr
		}
		result = r
		return nil
	}))

	assert.NotZero(t, result.StatusEventID)
	assert.NotZero(t, result.CloseEventID)

	updated, err := GetTask(db, task.ID)
	require.NoError(t, err)
	assert.Equal(t, models.TaskStatusBlocked, updated.Status)
	assert.Equal(t, newBlockedReasonFailure("api_timeout"), updated.BlockedReason)
}

func TestCloseTaskTx_BlockedClearsStaleReason(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()
	var err error

	task, err := CreateTask(db, "stale-reason", "", "", 0)
	require.NoError(t, err)

	// First: start, close as blocked with a reason.
	_, _, err = StartTaskAndFocus(db, "agent1", task.ID)
	require.NoError(t, err)

	require.NoError(t, Transact(db, func(tx *sql.Tx) error {
		_, txErr := CloseTaskTx(tx, CloseTaskParams{
			AgentName: "agent1", TaskID: task.ID,
			Status: "blocked", Summary: "First block",
			BlockedReason: "failure:old_reason",
		})
		return txErr
	}))

	updated, err := GetTask(db, task.ID)
	require.NoError(t, err)
	assert.Equal(t, newBlockedReasonFailure("old_reason"), updated.BlockedReason)

	// Re-open the task so we can close it again.
	err = UpdateTaskStatus(db, task.ID, "in_progress", updated.Version)
	require.NoError(t, err)

	// Second: close as blocked WITHOUT a reason — stale value must be cleared.
	require.NoError(t, Transact(db, func(tx *sql.Tx) error {
		_, txErr := CloseTaskTx(tx, CloseTaskParams{
			AgentName: "agent1", TaskID: task.ID,
			Status: "blocked", Summary: "Second block, no reason",
		})
		return txErr
	}))

	updated, err = GetTask(db, task.ID)
	require.NoError(t, err)
	assert.Equal(t, models.TaskStatusBlocked, updated.Status)
	assert.Empty(t, updated.BlockedReason, "stale blocked_reason should be cleared")
}

func TestCloseTaskTx_EventMetadataShape(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()
	var err error

	task, err := CreateTask(db, "meta-test", "", "", 0)
	require.NoError(t, err)

	_, _, err = StartTaskAndFocus(db, "agent1", task.ID)
	require.NoError(t, err)

	var result *CloseTaskResult
	require.NoError(t, Transact(db, func(tx *sql.Tx) error {
		r, txErr := CloseTaskTx(tx, CloseTaskParams{
			AgentName: "agent1", TaskID: task.ID,
			Status: "completed", Summary: "Feature shipped",
			Label: "v1.2.0",
		})
		if txErr != nil {
			return txErr
		}
		result = r
		return nil
	}))

	// Read the close event metadata.
	var metaStr sql.NullString
	err = db.QueryRow(`SELECT metadata FROM events WHERE id = ?`, result.CloseEventID).Scan(&metaStr)
	require.NoError(t, err)
	require.True(t, metaStr.Valid)

	var meta map[string]any
	require.NoError(t, json.Unmarshal([]byte(metaStr.String), &meta))

	assert.Equal(t, "completed", meta["outcome"])
	assert.Equal(t, "Feature shipped", meta["summary"])
	assert.Equal(t, "v1.2.0", meta["label"])
}

func TestCloseTaskTx_InvalidStatus(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()
	var err error

	task, err := CreateTask(db, "invalid", "", "", 0)
	require.NoError(t, err)

	err = Transact(db, func(tx *sql.Tx) error {
		_, txErr := CloseTaskTx(tx, CloseTaskParams{
			AgentName: "agent1", TaskID: task.ID,
			Status: "pending", Summary: "nope",
		})
		return txErr
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "close status must be completed or blocked")
}
