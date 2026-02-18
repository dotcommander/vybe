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
		_ = db.Close()
	}

	return db, cleanup
}

func TestTaskCreate(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	task, eventID, err := TaskCreateIdempotent(db, "test-agent", "req-create-1", "Test Task", "Test Description", "", 0)
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

	task, _, err := TaskCreateIdempotent(db, "test-agent", "req-create-empty-1", "", "Description", "", 0)
	assert.Error(t, err)
	assert.Nil(t, task)
	assert.Contains(t, err.Error(), "title is required")
}

func TestTaskGet(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	// Create a task
	created, _, err := TaskCreateIdempotent(db, "test-agent", "req-get-1", "Get Test", "Description", "", 0)
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
	created, _, err := TaskCreateIdempotent(db, "test-agent", "req-setstatus-create-1", "Status Test", "Description", "", 0)
	require.NoError(t, err)

	// Set status
	task, eventID, err := TaskSetStatusIdempotent(db, "test-agent", "req-setstatus-1", created.ID, "in_progress", "")
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

	created, _, err := TaskCreateIdempotent(db, "test-agent", "req-invalid-status-create-1", "Invalid Status Test", "Description", "", 0)
	require.NoError(t, err)

	task, eventID, err := TaskSetStatusIdempotent(db, "test-agent", "req-invalid-status-1", created.ID, "invalid_status", "")
	assert.Error(t, err)
	assert.Nil(t, task)
	assert.Equal(t, int64(0), eventID)
	assert.Contains(t, err.Error(), "invalid status")
}

func TestTaskSetStatusEmptyTaskID(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	task, eventID, err := TaskSetStatusIdempotent(db, "test-agent", "req-empty-id-1", "", "in_progress", "")
	assert.Error(t, err)
	assert.Nil(t, task)
	assert.Equal(t, int64(0), eventID)
	assert.Contains(t, err.Error(), "task ID is required")
}

func TestTaskSetStatusEmptyStatus(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	created, _, err := TaskCreateIdempotent(db, "test-agent", "req-empty-status-create-1", "Empty Status Test", "Description", "", 0)
	require.NoError(t, err)

	task, eventID, err := TaskSetStatusIdempotent(db, "test-agent", "req-empty-status-1", created.ID, "", "")
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

			created, _, err := TaskCreateIdempotent(db, "test-agent", "req-trans-create-"+tt.name, "Transition Test", "Description", "", 0)
			require.NoError(t, err)

			task, eventID, err := TaskSetStatusIdempotent(db, "test-agent", "req-trans-status-"+tt.name, created.ID, tt.status, "")

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

func TestTaskSetStatusConcurrency(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	// Create a task
	created, _, err := TaskCreateIdempotent(db, "test-agent", "req-conc-create-1", "Concurrency Test", "Description", "", 0)
	require.NoError(t, err)

	// First status change
	task1, eventID1, err := TaskSetStatusIdempotent(db, "test-agent", "req-conc-status-1", created.ID, "in_progress", "")
	require.NoError(t, err)
	assert.Equal(t, models.TaskStatusInProgress, task1.Status)
	assert.Greater(t, eventID1, int64(0))

	// Second status change (should succeed with retry)
	task2, eventID2, err := TaskSetStatusIdempotent(db, "test-agent", "req-conc-status-2", created.ID, "completed", "")
	require.NoError(t, err)
	assert.Equal(t, models.TaskStatusCompleted, task2.Status)
	assert.Equal(t, 3, task2.Version)
	assert.Greater(t, eventID2, eventID1)
}

func TestTaskList(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	// Create tasks
	_, _, err := TaskCreateIdempotent(db, "test-agent", "req-list-create-1", "Task 1", "Desc 1", "", 0)
	require.NoError(t, err)

	task2, _, err := TaskCreateIdempotent(db, "test-agent", "req-list-create-2", "Task 2", "Desc 2", "", 0)
	require.NoError(t, err)

	// Change status of task2
	_, _, err = TaskSetStatusIdempotent(db, "test-agent", "req-list-status-1", task2.ID, "completed", "")
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
	created, _, err := TaskCreateIdempotent(db, "test-agent", "req-atomic-create-1", "Atomicity Test", "Description", "", 0)
	require.NoError(t, err)

	// Update status
	task, eventID, err := TaskSetStatusIdempotent(db, "test-agent", "req-atomic-status-1", created.ID, "in_progress", "")
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

func TestTaskSetStatusIdempotent_CompletedUnblocksInTransaction(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	parent, _, err := TaskCreateIdempotent(db, "test-agent", "req-unblock-create-1", "Parent", "", "", 0)
	require.NoError(t, err)

	child, _, err := TaskCreateIdempotent(db, "test-agent", "req-unblock-create-2", "Child", "", "", 0)
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

	parent, _, err := TaskCreateIdempotent(db, "test-agent", "req-rmreplay-create-1", "Parent", "", "", 0)
	require.NoError(t, err)

	child, _, err := TaskCreateIdempotent(db, "test-agent", "req-rmreplay-create-2", "Child", "", "", 0)
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
