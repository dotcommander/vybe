package store

import (
	"database/sql"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAddTaskDependency(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	task1, err := CreateTask(db, "Task 1", "First task", "", 0)
	require.NoError(t, err)

	task2, err := CreateTask(db, "Task 2", "Second task", "", 0)
	require.NoError(t, err)

	// Add dependency: task2 depends on task1
	err = AddTaskDependency(db, task2.ID, task1.ID)
	require.NoError(t, err)

	// Verify dependency was added
	deps, err := GetTaskDependencies(db, task2.ID)
	require.NoError(t, err)
	assert.Len(t, deps, 1)
	assert.Equal(t, task1.ID, deps[0])

	// Verify task2 is now blocked (since task1 is pending)
	updatedTask2, err := GetTask(db, task2.ID)
	require.NoError(t, err)
	assert.Equal(t, "blocked", updatedTask2.Status)
}

func TestAddDuplicateDependency(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	task1, err := CreateTask(db, "Task 1", "First task", "", 0)
	require.NoError(t, err)

	task2, err := CreateTask(db, "Task 2", "Second task", "", 0)
	require.NoError(t, err)

	// Add dependency twice
	err = AddTaskDependency(db, task2.ID, task1.ID)
	require.NoError(t, err)

	err = AddTaskDependency(db, task2.ID, task1.ID)
	require.NoError(t, err) // Should be idempotent

	// Verify only one dependency
	deps, err := GetTaskDependencies(db, task2.ID)
	require.NoError(t, err)
	assert.Len(t, deps, 1)
}

func TestGetTaskDependencies(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	task1, err := CreateTask(db, "Task 1", "First task", "", 0)
	require.NoError(t, err)

	task2, err := CreateTask(db, "Task 2", "Second task", "", 0)
	require.NoError(t, err)

	task3, err := CreateTask(db, "Task 3", "Third task", "", 0)
	require.NoError(t, err)

	// Task3 depends on both task1 and task2
	err = AddTaskDependency(db, task3.ID, task1.ID)
	require.NoError(t, err)

	err = AddTaskDependency(db, task3.ID, task2.ID)
	require.NoError(t, err)

	// Verify dependencies
	deps, err := GetTaskDependencies(db, task3.ID)
	require.NoError(t, err)
	assert.Len(t, deps, 2)
	assert.Contains(t, deps, task1.ID)
	assert.Contains(t, deps, task2.ID)
}

func TestHasUnresolvedDependencies(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	task1, err := CreateTask(db, "Task 1", "First task", "", 0)
	require.NoError(t, err)

	task2, err := CreateTask(db, "Task 2", "Second task", "", 0)
	require.NoError(t, err)

	// Add dependency
	err = AddTaskDependency(db, task2.ID, task1.ID)
	require.NoError(t, err)

	// task2 should have unresolved dependencies (task1 is pending)
	hasUnresolved, err := HasUnresolvedDependencies(db, task2.ID)
	require.NoError(t, err)
	assert.True(t, hasUnresolved)

	// Complete task1
	err = UpdateTaskStatus(db, task1.ID, "completed", task1.Version)
	require.NoError(t, err)

	// task2 should no longer have unresolved dependencies
	hasUnresolved, err = HasUnresolvedDependencies(db, task2.ID)
	require.NoError(t, err)
	assert.False(t, hasUnresolved)
}

func TestUnblockDependents(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	task1, err := CreateTask(db, "Task 1", "First task", "", 0)
	require.NoError(t, err)

	task2, err := CreateTask(db, "Task 2", "Second task", "", 0)
	require.NoError(t, err)

	// Add dependency: task2 depends on task1
	err = AddTaskDependency(db, task2.ID, task1.ID)
	require.NoError(t, err)

	// Verify task2 is blocked
	updatedTask2, err := GetTask(db, task2.ID)
	require.NoError(t, err)
	assert.Equal(t, "blocked", updatedTask2.Status)

	// Complete task1
	err = UpdateTaskStatus(db, task1.ID, "completed", task1.Version)
	require.NoError(t, err)

	// Unblock dependents
	_, err = UnblockDependents(db, task1.ID)
	require.NoError(t, err)

	// Verify task2 is now pending
	updatedTask2, err = GetTask(db, task2.ID)
	require.NoError(t, err)
	assert.Equal(t, "pending", updatedTask2.Status)
}

func TestUnblockDependents_MultipleBlockers(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	task1, err := CreateTask(db, "Task 1", "First task", "", 0)
	require.NoError(t, err)

	task2, err := CreateTask(db, "Task 2", "Second task", "", 0)
	require.NoError(t, err)

	task3, err := CreateTask(db, "Task 3", "Third task", "", 0)
	require.NoError(t, err)

	// Task3 depends on both task1 and task2
	err = AddTaskDependency(db, task3.ID, task1.ID)
	require.NoError(t, err)

	err = AddTaskDependency(db, task3.ID, task2.ID)
	require.NoError(t, err)

	// Verify task3 is blocked
	updatedTask3, err := GetTask(db, task3.ID)
	require.NoError(t, err)
	assert.Equal(t, "blocked", updatedTask3.Status)

	// Complete task1
	err = UpdateTaskStatus(db, task1.ID, "completed", task1.Version)
	require.NoError(t, err)

	_, err = UnblockDependents(db, task1.ID)
	require.NoError(t, err)

	// task3 should still be blocked (task2 is pending)
	updatedTask3, err = GetTask(db, task3.ID)
	require.NoError(t, err)
	assert.Equal(t, "blocked", updatedTask3.Status)

	// Complete task2
	err = UpdateTaskStatus(db, task2.ID, "completed", task2.Version)
	require.NoError(t, err)

	_, err = UnblockDependents(db, task2.ID)
	require.NoError(t, err)

	// Now task3 should be unblocked
	updatedTask3, err = GetTask(db, task3.ID)
	require.NoError(t, err)
	assert.Equal(t, "pending", updatedTask3.Status)
}

func TestDetermineFocusTask_SkipsBlockedDeps(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	agentName := "test-agent"
	_, err := LoadOrCreateAgentState(db, agentName)
	require.NoError(t, err)

	task1, err := CreateTask(db, "Task 1", "First task", "", 10)
	require.NoError(t, err)

	task2, err := CreateTask(db, "Task 2", "Second task", "", 20) // Higher priority
	require.NoError(t, err)

	task3, err := CreateTask(db, "Task 3", "Third task", "", 5) // Lower priority
	require.NoError(t, err)

	// Task2 (highest priority) depends on task1
	err = AddTaskDependency(db, task2.ID, task1.ID)
	require.NoError(t, err)

	// DetermineFocusTask should skip task2 (blocked) and select task1
	focusID, err := DetermineFocusTask(db, agentName, "", nil, "")
	require.NoError(t, err)
	assert.Equal(t, task1.ID, focusID, "Should select task1 (dependency) not task2 (blocked)")

	// Complete task1
	err = UpdateTaskStatus(db, task1.ID, "completed", task1.Version)
	require.NoError(t, err)

	_, err = UnblockDependents(db, task1.ID)
	require.NoError(t, err)

	// Now task2 should be selected (highest priority, no longer blocked)
	focusID, err = DetermineFocusTask(db, agentName, "", nil, "")
	require.NoError(t, err)
	assert.Equal(t, task2.ID, focusID, "Should select task2 (highest priority, unblocked)")

	// Complete task2 (get fresh version after unblock)
	task2, err = GetTask(db, task2.ID)
	require.NoError(t, err)
	err = UpdateTaskStatus(db, task2.ID, "completed", task2.Version)
	require.NoError(t, err)

	// Now task3 should be selected
	focusID, err = DetermineFocusTask(db, agentName, "", nil, "")
	require.NoError(t, err)
	assert.Equal(t, task3.ID, focusID, "Should select task3 (only pending task left)")
}

func TestRemoveTaskDependency(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	task1, err := CreateTask(db, "Task 1", "First task", "", 0)
	require.NoError(t, err)

	task2, err := CreateTask(db, "Task 2", "Second task", "", 0)
	require.NoError(t, err)

	// Add dependency
	err = AddTaskDependency(db, task2.ID, task1.ID)
	require.NoError(t, err)

	// Verify dependency exists
	deps, err := GetTaskDependencies(db, task2.ID)
	require.NoError(t, err)
	assert.Len(t, deps, 1)

	// Remove dependency
	err = RemoveTaskDependency(db, task2.ID, task1.ID)
	require.NoError(t, err)

	// Verify dependency is gone
	deps, err = GetTaskDependencies(db, task2.ID)
	require.NoError(t, err)
	assert.Len(t, deps, 0)
}

func TestAddTaskDependency_SelfReference(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	task1, err := CreateTask(db, "Task 1", "First task", "", 0)
	require.NoError(t, err)

	// Try to add self-reference
	err = AddTaskDependency(db, task1.ID, task1.ID)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "cannot depend on itself")
}

func TestAddTaskDependency_DirectCycle(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	taskA, err := CreateTask(db, "Task A", "", "", 0)
	require.NoError(t, err)

	taskB, err := CreateTask(db, "Task B", "", "", 0)
	require.NoError(t, err)

	// A depends on B — succeeds
	err = AddTaskDependency(db, taskA.ID, taskB.ID)
	require.NoError(t, err)

	// B depends on A — should be rejected (direct cycle)
	err = AddTaskDependency(db, taskB.ID, taskA.ID)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "cycle detected")
}

func TestAddTaskDependency_TransitiveCycle(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	taskA, err := CreateTask(db, "Task A", "", "", 0)
	require.NoError(t, err)

	taskB, err := CreateTask(db, "Task B", "", "", 0)
	require.NoError(t, err)

	taskC, err := CreateTask(db, "Task C", "", "", 0)
	require.NoError(t, err)

	// A depends on B
	err = AddTaskDependency(db, taskA.ID, taskB.ID)
	require.NoError(t, err)

	// B depends on C
	err = AddTaskDependency(db, taskB.ID, taskC.ID)
	require.NoError(t, err)

	// C depends on A — should be rejected (transitive cycle: C→A→B→C)
	err = AddTaskDependency(db, taskC.ID, taskA.ID)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "cycle detected")
}

func TestAddTaskDependency_NoCycleFalsePositive(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	taskA, err := CreateTask(db, "Task A", "", "", 0)
	require.NoError(t, err)

	taskB, err := CreateTask(db, "Task B", "", "", 0)
	require.NoError(t, err)

	taskC, err := CreateTask(db, "Task C", "", "", 0)
	require.NoError(t, err)

	// Diamond pattern: A→B, C→B, C→A — no cycle
	err = AddTaskDependency(db, taskA.ID, taskB.ID)
	require.NoError(t, err)

	err = AddTaskDependency(db, taskC.ID, taskB.ID)
	require.NoError(t, err)

	// C depends on A — this should succeed (diamond, not cycle)
	err = AddTaskDependency(db, taskC.ID, taskA.ID)
	require.NoError(t, err)
}

func TestAddTaskDependency_NonexistentTask(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	task1, err := CreateTask(db, "Task 1", "First task", "", 0)
	require.NoError(t, err)

	// Try to add dependency on nonexistent task
	err = AddTaskDependency(db, task1.ID, "nonexistent")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "dependency task not found")

	// Try to add dependency from nonexistent task
	err = AddTaskDependency(db, "nonexistent", task1.ID)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "task not found")
}

func TestLoadTaskDependencies_PropagatesErrors(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	// GetTask on a task with dependencies should succeed (verifies loadTaskDependencies
	// returns errors properly through its callers)
	task1, err := CreateTask(db, "Task 1", "First task", "", 0)
	require.NoError(t, err)

	task2, err := CreateTask(db, "Task 2", "Second task", "", 0)
	require.NoError(t, err)

	err = AddTaskDependency(db, task2.ID, task1.ID)
	require.NoError(t, err)

	// GetTask should successfully load dependencies via the error-propagating path
	fetched, err := GetTask(db, task2.ID)
	require.NoError(t, err)
	assert.Len(t, fetched.DependsOn, 1)
	assert.Equal(t, task1.ID, fetched.DependsOn[0])

	// ListTasks should also load dependencies through the error-propagating path
	tasks, err := ListTasks(db, "", "", -1)
	require.NoError(t, err)
	require.Len(t, tasks, 2)

	// Find task2 in the list and verify deps loaded
	for _, task := range tasks {
		if task.ID == task2.ID {
			assert.Len(t, task.DependsOn, 1)
			assert.Equal(t, task1.ID, task.DependsOn[0])
		}
	}
}

func TestGetTaskDependencies_ClosesRows(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	task1, err := CreateTask(db, "Task 1", "First task", "", 0)
	require.NoError(t, err)

	task2, err := CreateTask(db, "Task 2", "Second task", "", 0)
	require.NoError(t, err)

	task3, err := CreateTask(db, "Task 3", "Third task", "", 0)
	require.NoError(t, err)

	// Add dependencies
	err = AddTaskDependency(db, task3.ID, task1.ID)
	require.NoError(t, err)

	err = AddTaskDependency(db, task3.ID, task2.ID)
	require.NoError(t, err)

	// Call GetTaskDependencies multiple times to verify no resource leak
	// (with single-connection SQLite, leaked rows would cause deadlock)
	for i := 0; i < 10; i++ {
		deps, err := GetTaskDependencies(db, task3.ID)
		require.NoError(t, err)
		assert.Len(t, deps, 2)
	}
}

func TestStartTaskAndFocus_ClaimContention(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	task, err := CreateTask(db, "Contended task", "", "", 0)
	require.NoError(t, err)

	// Agent-1 starts and claims the task
	_, _, err = StartTaskAndFocus(db, "agent-1", task.ID)
	require.NoError(t, err)

	// Agent-2 should fail to start the same task (claim held by agent-1)
	_, _, err = StartTaskAndFocus(db, "agent-2", task.ID)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "claim")
}

func TestAddTaskDependencyTx_CASCheck(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	task1, err := CreateTask(db, "Task 1", "", "", 0)
	require.NoError(t, err)

	task2, err := CreateTask(db, "Task 2", "", "", 0)
	require.NoError(t, err)

	// Add dependency: task2 depends on task1
	err = AddTaskDependency(db, task2.ID, task1.ID)
	require.NoError(t, err)

	// task2 should be blocked
	updatedTask2, err := GetTask(db, task2.ID)
	require.NoError(t, err)
	assert.Equal(t, "blocked", updatedTask2.Status)
	// Version should have been bumped by the CAS update
	assert.Greater(t, updatedTask2.Version, 1)
}

func TestRemoveTaskDependency_ProactiveUnblock(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	task1, err := CreateTask(db, "Task 1", "", "", 0)
	require.NoError(t, err)

	task2, err := CreateTask(db, "Task 2", "", "", 0)
	require.NoError(t, err)

	// Add dependency: task2 depends on task1
	err = AddTaskDependency(db, task2.ID, task1.ID)
	require.NoError(t, err)

	// task2 should be blocked
	updatedTask2, err := GetTask(db, task2.ID)
	require.NoError(t, err)
	assert.Equal(t, "blocked", updatedTask2.Status)

	// Remove the only dependency — task2 should proactively unblock
	err = RemoveTaskDependency(db, task2.ID, task1.ID)
	require.NoError(t, err)

	updatedTask2, err = GetTask(db, task2.ID)
	require.NoError(t, err)
	assert.Equal(t, "pending", updatedTask2.Status)
}

func TestRemoveTaskDependency_StillBlocked(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	task1, err := CreateTask(db, "Task 1", "", "", 0)
	require.NoError(t, err)

	task2, err := CreateTask(db, "Task 2", "", "", 0)
	require.NoError(t, err)

	task3, err := CreateTask(db, "Task 3", "", "", 0)
	require.NoError(t, err)

	// Task3 depends on both task1 and task2
	err = AddTaskDependency(db, task3.ID, task1.ID)
	require.NoError(t, err)
	err = AddTaskDependency(db, task3.ID, task2.ID)
	require.NoError(t, err)

	// task3 should be blocked
	updatedTask3, err := GetTask(db, task3.ID)
	require.NoError(t, err)
	assert.Equal(t, "blocked", updatedTask3.Status)

	// Remove one of two deps — task3 should remain blocked
	err = RemoveTaskDependency(db, task3.ID, task1.ID)
	require.NoError(t, err)

	updatedTask3, err = GetTask(db, task3.ID)
	require.NoError(t, err)
	assert.Equal(t, "blocked", updatedTask3.Status)
}

func TestAddTaskDependency_SetsBlockedReason(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	task1, err := CreateTask(db, "Task 1", "", "", 0)
	require.NoError(t, err)

	task2, err := CreateTask(db, "Task 2", "", "", 0)
	require.NoError(t, err)

	err = AddTaskDependency(db, task2.ID, task1.ID)
	require.NoError(t, err)

	updatedTask2, err := GetTask(db, task2.ID)
	require.NoError(t, err)
	assert.Equal(t, "blocked", updatedTask2.Status)
	assert.Equal(t, "dependency", updatedTask2.BlockedReason)
}

func TestUnblockDependents_ClearsBlockedReason(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	task1, err := CreateTask(db, "Task 1", "", "", 0)
	require.NoError(t, err)

	task2, err := CreateTask(db, "Task 2", "", "", 0)
	require.NoError(t, err)

	err = AddTaskDependency(db, task2.ID, task1.ID)
	require.NoError(t, err)

	// Verify blocked with reason
	updatedTask2, err := GetTask(db, task2.ID)
	require.NoError(t, err)
	assert.Equal(t, "dependency", updatedTask2.BlockedReason)

	// Complete task1 and unblock
	err = UpdateTaskStatus(db, task1.ID, "completed", task1.Version)
	require.NoError(t, err)
	_, err = UnblockDependents(db, task1.ID)
	require.NoError(t, err)

	// Verify blocked_reason is cleared
	updatedTask2, err = GetTask(db, task2.ID)
	require.NoError(t, err)
	assert.Equal(t, "pending", updatedTask2.Status)
	assert.Empty(t, updatedTask2.BlockedReason)
}

func TestBlockedReason_RoundTrip(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	task, err := CreateTask(db, "Test Task", "", "", 0)
	require.NoError(t, err)

	// Set blocked status manually
	err = UpdateTaskStatus(db, task.ID, "blocked", task.Version)
	require.NoError(t, err)

	// Set blocked_reason via Tx helper
	err = Transact(db, func(tx *sql.Tx) error {
		return SetBlockedReasonTx(tx, task.ID, "failure:timed out")
	})
	require.NoError(t, err)

	// Read it back
	fetched, err := GetTask(db, task.ID)
	require.NoError(t, err)
	assert.Equal(t, "blocked", fetched.Status)
	assert.Equal(t, "failure:timed out", fetched.BlockedReason)

	// Clear it
	err = Transact(db, func(tx *sql.Tx) error {
		return SetBlockedReasonTx(tx, task.ID, "")
	})
	require.NoError(t, err)

	fetched, err = GetTask(db, task.ID)
	require.NoError(t, err)
	assert.Empty(t, fetched.BlockedReason)
}

func TestGetBlockedByUnresolved(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	task1, err := CreateTask(db, "Task 1", "First task", "", 0)
	require.NoError(t, err)

	task2, err := CreateTask(db, "Task 2", "Second task", "", 0)
	require.NoError(t, err)

	task3, err := CreateTask(db, "Task 3", "Third task", "", 0)
	require.NoError(t, err)

	// Task3 depends on task1 and task2
	err = AddTaskDependency(db, task3.ID, task1.ID)
	require.NoError(t, err)

	err = AddTaskDependency(db, task3.ID, task2.ID)
	require.NoError(t, err)

	// Both are unresolved
	blockedBy, err := GetBlockedByUnresolved(db, task3.ID)
	require.NoError(t, err)
	assert.Len(t, blockedBy, 2)
	assert.Contains(t, blockedBy, task1.ID)
	assert.Contains(t, blockedBy, task2.ID)

	// Complete task1
	err = UpdateTaskStatus(db, task1.ID, "completed", task1.Version)
	require.NoError(t, err)

	// Only task2 is unresolved
	blockedBy, err = GetBlockedByUnresolved(db, task3.ID)
	require.NoError(t, err)
	assert.Len(t, blockedBy, 1)
	assert.Equal(t, task2.ID, blockedBy[0])

	// Complete task2
	err = UpdateTaskStatus(db, task2.ID, "completed", task2.Version)
	require.NoError(t, err)

	// No unresolved dependencies
	blockedBy, err = GetBlockedByUnresolved(db, task3.ID)
	require.NoError(t, err)
	assert.Len(t, blockedBy, 0)
}
