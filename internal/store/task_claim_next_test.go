package store

import (
	"database/sql"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestClaimNextTaskTx_PriorityOrdering(t *testing.T) {
	db, err := InitDBWithPath(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	// Create tasks with different priorities.
	low, err := CreateTask(db, "low-priority", "", "", 1)
	require.NoError(t, err)
	high, err := CreateTask(db, "high-priority", "", "", 10)
	require.NoError(t, err)

	var result *ClaimNextTaskResult
	require.NoError(t, Transact(db, func(tx *sql.Tx) error {
		r, err := ClaimNextTaskTx(tx, "agent1", "", 5)
		if err != nil {
			return err
		}
		result = r
		return nil
	}))

	require.Equal(t, high.ID, result.TaskID)
	assert.NotZero(t, result.StatusEventID)
	assert.NotZero(t, result.FocusEventID)
	assert.NotZero(t, result.ClaimEventID)

	_ = low // used for setup
}

func TestClaimNextTaskTx_SkipsClaimed(t *testing.T) {
	db, err := InitDBWithPath(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	first, err := CreateTask(db, "first", "", "", 0)
	require.NoError(t, err)
	second, err := CreateTask(db, "second", "", "", 0)
	require.NoError(t, err)

	// Claim the first task.
	require.NoError(t, Transact(db, func(tx *sql.Tx) error { return ClaimTaskTx(tx, "agent1", first.ID, 5) }))

	// Next claim should skip the claimed task.
	var result *ClaimNextTaskResult
	require.NoError(t, Transact(db, func(tx *sql.Tx) error {
		r, err := ClaimNextTaskTx(tx, "agent2", "", 5)
		if err != nil {
			return err
		}
		result = r
		return nil
	}))

	require.Equal(t, second.ID, result.TaskID)
}

func TestClaimNextTaskTx_SkipsTasksWithUnresolvedDeps(t *testing.T) {
	db, err := InitDBWithPath(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	blocker, err := CreateTask(db, "blocker", "", "", 0)
	require.NoError(t, err)
	blocked, err := CreateTask(db, "blocked", "", "", 10) // higher priority but blocked
	require.NoError(t, err)

	require.NoError(t, AddTaskDependency(db, blocked.ID, blocker.ID))

	// Claim blocker first (it's eligible, created first with same priority as any other).
	require.NoError(t, Transact(db, func(tx *sql.Tx) error { return ClaimTaskTx(tx, "agent0", blocker.ID, 5) }))

	// Create a free task that should be claimable.
	free, err := CreateTask(db, "free", "", "", 0)
	require.NoError(t, err)

	var result *ClaimNextTaskResult
	require.NoError(t, Transact(db, func(tx *sql.Tx) error {
		r, err := ClaimNextTaskTx(tx, "agent1", "", 5)
		if err != nil {
			return err
		}
		result = r
		return nil
	}))

	// blocked task is skipped (dependency), blocker is skipped (claimed), free is selected.
	require.Equal(t, free.ID, result.TaskID)
}

func TestClaimNextTaskTx_EmptyQueue(t *testing.T) {
	db, err := InitDBWithPath(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	var result *ClaimNextTaskResult
	require.NoError(t, Transact(db, func(tx *sql.Tx) error {
		r, err := ClaimNextTaskTx(tx, "agent1", "", 5)
		if err != nil {
			return err
		}
		result = r
		return nil
	}))

	assert.Empty(t, result.TaskID)
	assert.Zero(t, result.StatusEventID)
	assert.Zero(t, result.FocusEventID)
	assert.Zero(t, result.ClaimEventID)
}

func TestClaimNextTaskTx_ProjectScoping(t *testing.T) {
	db, err := InitDBWithPath(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	// Insert a project first.
	_, err = db.Exec(`INSERT INTO projects (id, name, created_at) VALUES ('proj1', 'Test', CURRENT_TIMESTAMP)`)
	require.NoError(t, err)

	_, err = CreateTask(db, "global-task", "", "", 0)
	require.NoError(t, err)
	scoped, err := CreateTask(db, "scoped-task", "", "proj1", 0)
	require.NoError(t, err)

	var result *ClaimNextTaskResult
	require.NoError(t, Transact(db, func(tx *sql.Tx) error {
		r, err := ClaimNextTaskTx(tx, "agent1", "proj1", 5)
		if err != nil {
			return err
		}
		result = r
		return nil
	}))

	require.Equal(t, scoped.ID, result.TaskID)
}

func TestClaimNextTaskTx_ExpiredClaimReclaim(t *testing.T) {
	db, err := InitDBWithPath(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	task, err := CreateTask(db, "reclaimable", "", "", 0)
	require.NoError(t, err)

	// Claim then expire.
	require.NoError(t, Transact(db, func(tx *sql.Tx) error { return ClaimTaskTx(tx, "agent1", task.ID, 5) }))
	_, err = db.Exec(`UPDATE tasks SET claim_expires_at = datetime('now', '-1 minute') WHERE id = ?`, task.ID)
	require.NoError(t, err)

	// Another agent should be able to claim via ClaimNextTaskTx.
	var result *ClaimNextTaskResult
	require.NoError(t, Transact(db, func(tx *sql.Tx) error {
		r, err := ClaimNextTaskTx(tx, "agent2", "", 5)
		if err != nil {
			return err
		}
		result = r
		return nil
	}))

	require.Equal(t, task.ID, result.TaskID)
}
