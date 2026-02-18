package store

import (
	"database/sql"
	"testing"

	"github.com/dotcommander/vybe/internal/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDeleteTask(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	task, err := CreateTask(db, "Delete me", "", "", 0)
	require.NoError(t, err)

	err = Transact(db, func(tx *sql.Tx) error {
		return DeleteTaskTx(tx, "agent1", task.ID)
	})
	require.NoError(t, err)

	// Verify task is gone
	_, err = GetTask(db, task.ID)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}

func TestDeleteTask_NotFound(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	err := Transact(db, func(tx *sql.Tx) error {
		return DeleteTaskTx(tx, "agent1", "nonexistent")
	})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}

func TestDeleteTask_CascadesDeps(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	task1, err := CreateTask(db, "Task 1", "", "", 0)
	require.NoError(t, err)

	task2, err := CreateTask(db, "Task 2", "", "", 0)
	require.NoError(t, err)

	// task2 depends on task1
	err = AddTaskDependency(db, task2.ID, task1.ID)
	require.NoError(t, err)

	// Delete task1 — CASCADE should remove the dependency row
	err = Transact(db, func(tx *sql.Tx) error {
		return DeleteTaskTx(tx, "agent1", task1.ID)
	})
	require.NoError(t, err)

	// task2 should have no dependencies left
	deps, err := GetTaskDependencies(db, task2.ID)
	require.NoError(t, err)
	assert.Len(t, deps, 0)

	// task2 was blocked — should now be pending (unblocked by delete)
	task2Updated, err := GetTask(db, task2.ID)
	require.NoError(t, err)
	assert.Equal(t, models.TaskStatusPending, task2Updated.Status)
	assert.Empty(t, task2Updated.BlockedReason)
}

func TestDeleteTask_UnblocksOrphanedDependents(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	blocker, err := CreateTask(db, "Blocker", "", "", 0)
	require.NoError(t, err)

	dependent, err := CreateTask(db, "Dependent", "", "", 0)
	require.NoError(t, err)

	// dependent blocked on blocker
	err = AddTaskDependency(db, dependent.ID, blocker.ID)
	require.NoError(t, err)

	// Verify dependent is blocked
	dep, err := GetTask(db, dependent.ID)
	require.NoError(t, err)
	assert.Equal(t, models.TaskStatusBlocked, dep.Status)

	// Delete blocker
	err = Transact(db, func(tx *sql.Tx) error {
		return DeleteTaskTx(tx, "agent1", blocker.ID)
	})
	require.NoError(t, err)

	// Dependent should be unblocked (pending)
	dep, err = GetTask(db, dependent.ID)
	require.NoError(t, err)
	assert.Equal(t, models.TaskStatusPending, dep.Status)
	assert.Empty(t, dep.BlockedReason)
}

func TestDeleteTask_PartialUnblock(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	blocker1, err := CreateTask(db, "Blocker 1", "", "", 0)
	require.NoError(t, err)

	blocker2, err := CreateTask(db, "Blocker 2", "", "", 0)
	require.NoError(t, err)

	dependent, err := CreateTask(db, "Dependent", "", "", 0)
	require.NoError(t, err)

	// dependent blocked on both
	err = AddTaskDependency(db, dependent.ID, blocker1.ID)
	require.NoError(t, err)
	err = AddTaskDependency(db, dependent.ID, blocker2.ID)
	require.NoError(t, err)

	// Delete blocker1 — dependent still has blocker2
	err = Transact(db, func(tx *sql.Tx) error {
		return DeleteTaskTx(tx, "agent1", blocker1.ID)
	})
	require.NoError(t, err)

	// Dependent should STAY blocked (blocker2 still exists)
	dep, err := GetTask(db, dependent.ID)
	require.NoError(t, err)
	assert.Equal(t, models.TaskStatusBlocked, dep.Status)
}

func TestDeleteTask_RefusesInProgressClaimedByOther(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	task, err := CreateTask(db, "Active task", "", "", 0)
	require.NoError(t, err)

	// agent1 claims and starts the task
	err = Transact(db, func(tx *sql.Tx) error { return ClaimTaskTx(tx, "agent1", task.ID, 60) })
	require.NoError(t, err)
	err = UpdateTaskStatus(db, task.ID, "in_progress", 1)
	require.NoError(t, err)

	// agent2 tries to delete — should fail
	err = Transact(db, func(tx *sql.Tx) error {
		return DeleteTaskTx(tx, "agent2", task.ID)
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "in_progress")
	assert.Contains(t, err.Error(), "claimed by agent1")

	// agent1 can still delete their own in_progress task
	err = Transact(db, func(tx *sql.Tx) error {
		return DeleteTaskTx(tx, "agent1", task.ID)
	})
	require.NoError(t, err)
}

func TestDeleteTask_RefusesClaimedByOther(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	task, err := CreateTask(db, "Claimed task", "", "", 0)
	require.NoError(t, err)

	// agent1 claims with long TTL
	err = Transact(db, func(tx *sql.Tx) error { return ClaimTaskTx(tx, "agent1", task.ID, 60) })
	require.NoError(t, err)

	// agent2 tries to delete — should fail (active claim)
	err = Transact(db, func(tx *sql.Tx) error {
		return DeleteTaskTx(tx, "agent2", task.ID)
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "claimed by agent1")
}

func TestDeleteTask_DoesNotUnblockFailureBlocked(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	blocker, err := CreateTask(db, "Blocker", "", "", 0)
	require.NoError(t, err)

	dependent, err := CreateTask(db, "Failure-blocked dependent", "", "", 0)
	require.NoError(t, err)

	// dependent depends on blocker (sets status=blocked, blocked_reason=dependency)
	err = AddTaskDependency(db, dependent.ID, blocker.ID)
	require.NoError(t, err)

	// Override blocked_reason to simulate a failure block
	_, err = db.Exec(`UPDATE tasks SET blocked_reason = 'failure:timeout' WHERE id = ?`, dependent.ID)
	require.NoError(t, err)

	// Delete blocker — dependent should NOT be unblocked because it's failure-blocked
	err = Transact(db, func(tx *sql.Tx) error {
		return DeleteTaskTx(tx, "agent1", blocker.ID)
	})
	require.NoError(t, err)

	dep, err := GetTask(db, dependent.ID)
	require.NoError(t, err)
	assert.Equal(t, models.TaskStatusBlocked, dep.Status, "failure-blocked task should stay blocked")
	assert.Equal(t, newBlockedReasonFailure("timeout"), dep.BlockedReason)
}

func TestDeleteTask_AllowsExpiredClaim(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	task, err := CreateTask(db, "Stale claim", "", "", 0)
	require.NoError(t, err)

	// agent1 claims with short TTL
	err = Transact(db, func(tx *sql.Tx) error { return ClaimTaskTx(tx, "agent1", task.ID, 1) })
	require.NoError(t, err)

	// Expire the claim manually
	_, err = db.Exec(`UPDATE tasks SET claim_expires_at = datetime('now', '-1 minute') WHERE id = ?`, task.ID)
	require.NoError(t, err)

	// agent2 can delete — claim is expired
	err = Transact(db, func(tx *sql.Tx) error {
		return DeleteTaskTx(tx, "agent2", task.ID)
	})
	require.NoError(t, err)

	_, err = GetTask(db, task.ID)
	assert.Error(t, err)
}
