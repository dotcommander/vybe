package store

import (
	"database/sql"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestClaimNextTaskTx_PriorityOrdering(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()
	var err error

	// Create tasks with different priorities.
	low, err := CreateTask(db, "low-priority", "", "", 1)
	require.NoError(t, err)
	high, err := CreateTask(db, "high-priority", "", "", 10)
	require.NoError(t, err)

	var result *ClaimNextTaskResult
	require.NoError(t, Transact(db, func(tx *sql.Tx) error {
		r, err := ClaimNextTaskTx(tx, "agent1", "")
		if err != nil {
			return err
		}
		result = r
		return nil
	}))

	require.Equal(t, high.ID, result.TaskID)
	assert.NotZero(t, result.StatusEventID)
	assert.NotZero(t, result.FocusEventID)

	_ = low // used for setup
}

func TestClaimNextTaskTx_SkipsInProgress(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()
	var err error

	first, err := CreateTask(db, "first", "", "", 0)
	require.NoError(t, err)
	second, err := CreateTask(db, "second", "", "", 0)
	require.NoError(t, err)

	// Set the first task to in_progress directly.
	require.NoError(t, UpdateTaskStatus(db, first.ID, "in_progress", 1))

	// Next claim should skip the in_progress task and pick second.
	var result *ClaimNextTaskResult
	require.NoError(t, Transact(db, func(tx *sql.Tx) error {
		r, err := ClaimNextTaskTx(tx, "agent2", "")
		if err != nil {
			return err
		}
		result = r
		return nil
	}))

	require.Equal(t, second.ID, result.TaskID)
}

func TestClaimNextTaskTx_SkipsTasksWithUnresolvedDeps(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()
	var err error

	blocker, err := CreateTask(db, "blocker", "", "", 0)
	require.NoError(t, err)
	blocked, err := CreateTask(db, "blocked", "", "", 10) // higher priority but blocked
	require.NoError(t, err)

	require.NoError(t, AddTaskDependency(db, blocked.ID, blocker.ID))

	var result *ClaimNextTaskResult
	require.NoError(t, Transact(db, func(tx *sql.Tx) error {
		r, err := ClaimNextTaskTx(tx, "agent1", "")
		if err != nil {
			return err
		}
		result = r
		return nil
	}))

	// blocker is eligible (oldest pending with no deps at priority 0)
	// blocked is skipped (dependency on blocker)
	// Result should be blocker (oldest pending task without unresolved deps)
	require.NotEmpty(t, result.TaskID)
	require.NotEqual(t, blocked.ID, result.TaskID)
}

func TestClaimNextTaskTx_EmptyQueue(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	var result *ClaimNextTaskResult
	require.NoError(t, Transact(db, func(tx *sql.Tx) error {
		r, err := ClaimNextTaskTx(tx, "agent1", "")
		if err != nil {
			return err
		}
		result = r
		return nil
	}))

	assert.Empty(t, result.TaskID)
	assert.Zero(t, result.StatusEventID)
	assert.Zero(t, result.FocusEventID)
}

func TestClaimNextTaskTx_ProjectScoping(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()
	var err error

	// Insert a project first.
	_, err = db.Exec(`INSERT INTO projects (id, name, created_at) VALUES ('proj1', 'Test', CURRENT_TIMESTAMP)`)
	require.NoError(t, err)

	_, err = CreateTask(db, "global-task", "", "", 0)
	require.NoError(t, err)
	scoped, err := CreateTask(db, "scoped-task", "", "proj1", 0)
	require.NoError(t, err)

	var result *ClaimNextTaskResult
	require.NoError(t, Transact(db, func(tx *sql.Tx) error {
		r, err := ClaimNextTaskTx(tx, "agent1", "proj1")
		if err != nil {
			return err
		}
		result = r
		return nil
	}))

	require.Equal(t, scoped.ID, result.TaskID)
}
