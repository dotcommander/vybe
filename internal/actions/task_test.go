package actions

import (
	"database/sql"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dotcommander/vybe/internal/models"
	"github.com/dotcommander/vybe/internal/store"
)

func setupTestDB(t *testing.T) (*sql.DB, func()) {
	t.Helper()

	db, err := store.InitDBWithPath(":memory:")
	require.NoError(t, err)

	cleanup := func() {
		db.Close()
	}

	return db, cleanup
}

func TestTaskCreate(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	task, eventID, err := TaskCreate(db, "test-agent", "Test Task", "Test Description", "", 0)
	require.NoError(t, err)
	require.NotNil(t, task)
	assert.Greater(t, eventID, int64(0))

	assert.NotEmpty(t, task.ID)
	assert.Equal(t, "Test Task", task.Title)
	assert.Equal(t, "Test Description", task.Description)
	assert.Equal(t, models.TaskStatusPending, task.Status)
	assert.Equal(t, 1, task.Version)
}

func TestTaskCreateEmptyTitle(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	task, _, err := TaskCreate(db, "test-agent", "", "Description", "", 0)
	assert.Error(t, err)
	assert.Nil(t, task)
	assert.Contains(t, err.Error(), "title is required")
}

func TestTaskGet(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	// Create a task
	created, _, err := TaskCreate(db, "test-agent", "Get Test", "Description", "", 0)
	require.NoError(t, err)

	// Get the task
	task, err := TaskGet(db, created.ID)
	require.NoError(t, err)
	assert.Equal(t, created.ID, task.ID)
	assert.Equal(t, created.Title, task.Title)
}

func TestTaskGetEmptyID(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	task, err := TaskGet(db, "")
	assert.Error(t, err)
	assert.Nil(t, task)
	assert.Contains(t, err.Error(), "task ID is required")
}

func TestTaskSetStatus(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	// Create a task
	created, _, err := TaskCreate(db, "test-agent", "Status Test", "Description", "", 0)
	require.NoError(t, err)

	// Set status
	task, eventID, err := TaskSetStatus(db, "test-agent", created.ID, "in_progress")
	require.NoError(t, err)
	require.NotNil(t, task)
	assert.Greater(t, eventID, int64(0))

	// Verify status change
	assert.Equal(t, models.TaskStatusInProgress, task.Status)
	assert.Equal(t, 2, task.Version)

	// Verify event was created
	var count int
	err = db.QueryRow("SELECT COUNT(*) FROM events WHERE task_id = ? AND kind = ?",
		created.ID, "task_status").Scan(&count)
	require.NoError(t, err)
	assert.Equal(t, 1, count)
}

func TestTaskSetStatusInvalidStatus(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	created, _, err := TaskCreate(db, "test-agent", "Invalid Status Test", "Description", "", 0)
	require.NoError(t, err)

	task, eventID, err := TaskSetStatus(db, "test-agent", created.ID, "invalid_status")
	assert.Error(t, err)
	assert.Nil(t, task)
	assert.Equal(t, int64(0), eventID)
	assert.Contains(t, err.Error(), "invalid status")
}

func TestTaskSetStatusEmptyTaskID(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	task, eventID, err := TaskSetStatus(db, "test-agent", "", "in_progress")
	assert.Error(t, err)
	assert.Nil(t, task)
	assert.Equal(t, int64(0), eventID)
	assert.Contains(t, err.Error(), "task ID is required")
}

func TestTaskSetStatusEmptyStatus(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	created, _, err := TaskCreate(db, "test-agent", "Empty Status Test", "Description", "", 0)
	require.NoError(t, err)

	task, eventID, err := TaskSetStatus(db, "test-agent", created.ID, "")
	assert.Error(t, err)
	assert.Nil(t, task)
	assert.Equal(t, int64(0), eventID)
	assert.Contains(t, err.Error(), "status is required")
}

func TestTaskSetStatusValidTransitions(t *testing.T) {
	tests := []struct {
		name   string
		status string
		valid  bool
	}{
		{"pending", "pending", true},
		{"in_progress", "in_progress", true},
		{"completed", "completed", true},
		{"blocked", "blocked", true},
		{"invalid", "invalid", false},
		{"cancelled", "cancelled", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db, cleanup := setupTestDB(t)
			defer cleanup()

			created, _, err := TaskCreate(db, "test-agent", "Transition Test", "Description", "", 0)
			require.NoError(t, err)

			task, eventID, err := TaskSetStatus(db, "test-agent", created.ID, tt.status)

			if tt.valid {
				require.NoError(t, err)
				require.NotNil(t, task)
				assert.Greater(t, eventID, int64(0))
				assert.Equal(t, models.TaskStatus(tt.status), task.Status)
			} else {
				assert.Error(t, err)
				assert.Nil(t, task)
				assert.Equal(t, int64(0), eventID)
			}
		})
	}
}

func TestValidateTaskStatus(t *testing.T) {
	tests := []struct {
		name    string
		status  string
		wantErr bool
	}{
		{name: "pending", status: "pending", wantErr: false},
		{name: "in_progress", status: "in_progress", wantErr: false},
		{name: "completed", status: "completed", wantErr: false},
		{name: "blocked", status: "blocked", wantErr: false},
		{name: "invalid", status: "invalid", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateTaskStatus(tt.status)
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
		})
	}
}

func TestTaskSetStatusIdempotentParity(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	nonIdemTask, _, err := TaskCreate(db, "test-agent", "Non-idempotent status", "Description", "", 0)
	require.NoError(t, err)

	idemTask, _, err := TaskCreate(db, "test-agent", "Idempotent status", "Description", "", 0)
	require.NoError(t, err)

	nonIdemUpdated, nonIdemEventID, err := TaskSetStatus(db, "test-agent", nonIdemTask.ID, "in_progress")
	require.NoError(t, err)

	idemUpdated, idemEventID, err := TaskSetStatusIdempotent(db, "test-agent", "req_status_parity", idemTask.ID, "in_progress", "")
	require.NoError(t, err)

	require.Equal(t, models.TaskStatusInProgress, nonIdemUpdated.Status)
	require.Equal(t, models.TaskStatusInProgress, idemUpdated.Status)
	require.Equal(t, 2, nonIdemUpdated.Version)
	require.Equal(t, 2, idemUpdated.Version)
	require.Greater(t, nonIdemEventID, int64(0))
	require.Greater(t, idemEventID, int64(0))
}

func TestTaskSetStatusConcurrency(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	// Create a task
	created, _, err := TaskCreate(db, "test-agent", "Concurrency Test", "Description", "", 0)
	require.NoError(t, err)

	// First status change
	task1, eventID1, err := TaskSetStatus(db, "test-agent", created.ID, "in_progress")
	require.NoError(t, err)
	assert.Equal(t, models.TaskStatusInProgress, task1.Status)
	assert.Greater(t, eventID1, int64(0))

	// Second status change (should succeed with retry)
	task2, eventID2, err := TaskSetStatus(db, "test-agent", created.ID, "completed")
	require.NoError(t, err)
	assert.Equal(t, models.TaskStatusCompleted, task2.Status)
	assert.Equal(t, 3, task2.Version)
	assert.Greater(t, eventID2, eventID1)
}

func TestTaskList(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	// Create tasks
	_, _, err := TaskCreate(db, "test-agent", "Task 1", "Desc 1", "", 0)
	require.NoError(t, err)

	task2, _, err := TaskCreate(db, "test-agent", "Task 2", "Desc 2", "", 0)
	require.NoError(t, err)

	// Change status of task2
	_, _, err = TaskSetStatus(db, "test-agent", task2.ID, "completed")
	require.NoError(t, err)

	// List all tasks
	allTasks, err := TaskList(db, "", "", -1)
	require.NoError(t, err)
	assert.Len(t, allTasks, 2)

	// List only pending
	pendingTasks, err := TaskList(db, "pending", "", -1)
	require.NoError(t, err)
	assert.Len(t, pendingTasks, 1)

	// List only completed
	completedTasks, err := TaskList(db, "completed", "", -1)
	require.NoError(t, err)
	assert.Len(t, completedTasks, 1)
}

func TestTaskSetStatusAtomicity(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	// Create a task
	created, _, err := TaskCreate(db, "test-agent", "Atomicity Test", "Description", "", 0)
	require.NoError(t, err)

	// Update status
	task, eventID, err := TaskSetStatus(db, "test-agent", created.ID, "in_progress")
	require.NoError(t, err)

	// Verify both task and event were updated
	// Check task
	updatedTask, err := TaskGet(db, created.ID)
	require.NoError(t, err)
	assert.Equal(t, task.Status, updatedTask.Status)
	assert.Equal(t, task.Version, updatedTask.Version)

	// Check event exists
	var eventMessage string
	err = db.QueryRow("SELECT message FROM events WHERE id = ?", eventID).Scan(&eventMessage)
	require.NoError(t, err)
	assert.Contains(t, eventMessage, "in_progress")
}

func TestTaskRemoveDependency(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	parent, _, err := TaskCreate(db, "test-agent", "Parent", "", "", 0)
	require.NoError(t, err)

	child, _, err := TaskCreate(db, "test-agent", "Child", "", "", 0)
	require.NoError(t, err)

	err = store.AddTaskDependency(db, child.ID, parent.ID)
	require.NoError(t, err)

	err = TaskRemoveDependency(db, "test-agent", child.ID, parent.ID)
	require.NoError(t, err)

	deps, err := store.GetTaskDependencies(db, child.ID)
	require.NoError(t, err)
	assert.Len(t, deps, 0)

	var count int
	err = db.QueryRow(`
		SELECT COUNT(*)
		FROM events
		WHERE kind = 'task_dependency_removed' AND task_id = ?
	`, child.ID).Scan(&count)
	require.NoError(t, err)
	assert.Equal(t, 1, count)
}

func TestTaskSetStatus_CompletedUnblocksInTransaction(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	parent, _, err := TaskCreate(db, "test-agent", "Parent", "", "", 0)
	require.NoError(t, err)

	child, _, err := TaskCreate(db, "test-agent", "Child", "", "", 0)
	require.NoError(t, err)

	// child depends on parent
	err = store.AddTaskDependency(db, child.ID, parent.ID)
	require.NoError(t, err)

	// Verify child is blocked
	childTask, err := TaskGet(db, child.ID)
	require.NoError(t, err)
	assert.Equal(t, models.TaskStatusBlocked, childTask.Status)

	// Complete parent — child should be unblocked atomically in the same tx
	_, _, err = TaskSetStatus(db, "test-agent", parent.ID, "completed")
	require.NoError(t, err)

	// Verify child is now pending (unblocked inside the completion transaction)
	childTask, err = TaskGet(db, child.ID)
	require.NoError(t, err)
	assert.Equal(t, models.TaskStatusPending, childTask.Status)
}

func TestTaskSetStatusIdempotent_CompletedUnblocksInTransaction(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	parent, _, err := TaskCreate(db, "test-agent", "Parent", "", "", 0)
	require.NoError(t, err)

	child, _, err := TaskCreate(db, "test-agent", "Child", "", "", 0)
	require.NoError(t, err)

	// child depends on parent
	err = store.AddTaskDependency(db, child.ID, parent.ID)
	require.NoError(t, err)

	// Complete parent via idempotent path
	_, _, err = TaskSetStatusIdempotent(db, "test-agent", "req_complete_parent", parent.ID, "completed", "")
	require.NoError(t, err)

	// Verify child is now pending
	childTask, err := TaskGet(db, child.ID)
	require.NoError(t, err)
	assert.Equal(t, models.TaskStatusPending, childTask.Status)
}

func TestTaskRemoveDependencyIdempotentReplay(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	parent, _, err := TaskCreate(db, "test-agent", "Parent", "", "", 0)
	require.NoError(t, err)

	child, _, err := TaskCreate(db, "test-agent", "Child", "", "", 0)
	require.NoError(t, err)

	err = store.AddTaskDependency(db, child.ID, parent.ID)
	require.NoError(t, err)

	err = TaskRemoveDependencyIdempotent(db, "test-agent", "req_remove_dep", child.ID, parent.ID)
	require.NoError(t, err)
	err = TaskRemoveDependencyIdempotent(db, "test-agent", "req_remove_dep", child.ID, parent.ID)
	require.NoError(t, err)

	deps, err := store.GetTaskDependencies(db, child.ID)
	require.NoError(t, err)
	assert.Len(t, deps, 0)

	var count int
	err = db.QueryRow(`
		SELECT COUNT(*)
		FROM events
		WHERE kind = 'task_dependency_removed' AND task_id = ?
	`, child.ID).Scan(&count)
	require.NoError(t, err)
	assert.Equal(t, 1, count)
}
