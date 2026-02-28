package store

import (
	"context"
	"database/sql"
	"regexp"
	"testing"

	"github.com/dotcommander/vybe/internal/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var taskIDPattern = regexp.MustCompile(`^task_\d+(_[0-9a-f]{12})?$`)

func TestCreateTask(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	task, err := CreateTask(db, "Test Task", "Test Description", "", 0)
	require.NoError(t, err)
	require.NotNil(t, task)

	assert.NotEmpty(t, task.ID)
	assert.Equal(t, "Test Task", task.Title)
	assert.Equal(t, "Test Description", task.Description)
	assert.Equal(t, models.TaskStatusPending, task.Status)
	assert.Equal(t, 1, task.Version)
	assert.False(t, task.CreatedAt.IsZero())
	assert.False(t, task.UpdatedAt.IsZero())
}

func TestGetTask(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	// Create a task first
	created, err := CreateTask(db, "Get Test", "Description", "", 0)
	require.NoError(t, err)

	// Get the task
	task, err := GetTask(db, created.ID)
	require.NoError(t, err)
	require.NotNil(t, task)

	assert.Equal(t, created.ID, task.ID)
	assert.Equal(t, created.Title, task.Title)
	assert.Equal(t, created.Description, task.Description)
	assert.Equal(t, created.Status, task.Status)
	assert.Equal(t, created.Version, task.Version)
}

func TestGetTaskNotFound(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	task, err := GetTask(db, "nonexistent")
	assert.Error(t, err)
	assert.Nil(t, task)
	assert.Contains(t, err.Error(), "task not found")
}

func TestUpdateTaskStatus(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	// Create a task
	task, err := CreateTask(db, "Update Test", "Description", "", 0)
	require.NoError(t, err)

	// Update status
	err = UpdateTaskStatus(db, task.ID, "in_progress", task.Version)
	require.NoError(t, err)

	// Verify update
	updated, err := GetTask(db, task.ID)
	require.NoError(t, err)
	assert.Equal(t, models.TaskStatusInProgress, updated.Status)
	assert.Equal(t, 2, updated.Version)
	// UpdatedAt should be >= CreatedAt (SQLite timestamps may have same precision)
	assert.False(t, updated.UpdatedAt.Before(task.UpdatedAt))
}

func TestUpdateTaskStatusVersionConflict(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	// Create a task
	task, err := CreateTask(db, "Conflict Test", "Description", "", 0)
	require.NoError(t, err)

	// Update with correct version
	err = UpdateTaskStatus(db, task.ID, "in_progress", task.Version)
	require.NoError(t, err)

	// Try to update with old version (should fail)
	err = UpdateTaskStatus(db, task.ID, "completed", task.Version)
	assert.Error(t, err)
	assert.ErrorIs(t, err, ErrVersionConflict)
}

func TestListTasks(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	// Create multiple tasks
	_, err := CreateTask(db, "Task 1", "Desc 1", "", 0)
	require.NoError(t, err)

	task2, err := CreateTask(db, "Task 2", "Desc 2", "", 0)
	require.NoError(t, err)

	task3, err := CreateTask(db, "Task 3", "Desc 3", "", 0)
	require.NoError(t, err)

	// Update some statuses
	err = UpdateTaskStatus(db, task2.ID, "in_progress", task2.Version)
	require.NoError(t, err)

	err = UpdateTaskStatus(db, task3.ID, "completed", task3.Version)
	require.NoError(t, err)

	// List all tasks
	allTasks, err := ListTasks(db, "", "", -1)
	require.NoError(t, err)
	assert.Len(t, allTasks, 3)

	// List pending tasks
	pendingTasks, err := ListTasks(db, "pending", "", -1)
	require.NoError(t, err)
	assert.Len(t, pendingTasks, 1)
	assert.Equal(t, models.TaskStatusPending, pendingTasks[0].Status)

	// List in_progress tasks
	inProgressTasks, err := ListTasks(db, "in_progress", "", -1)
	require.NoError(t, err)
	assert.Len(t, inProgressTasks, 1)
	assert.Equal(t, models.TaskStatusInProgress, inProgressTasks[0].Status)

	// List completed tasks
	completedTasks, err := ListTasks(db, "completed", "", -1)
	require.NoError(t, err)
	assert.Len(t, completedTasks, 1)
	assert.Equal(t, models.TaskStatusCompleted, completedTasks[0].Status)
}

func TestListTasksEmpty(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	tasks, err := ListTasks(db, "", "", -1)
	require.NoError(t, err)
	assert.Empty(t, tasks)
}

func TestConcurrentTaskUpdates(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	// Create a task
	task, err := CreateTask(db, "Concurrent Test", "Description", "", 0)
	require.NoError(t, err)

	// Simulate concurrent updates
	// First update succeeds
	err = UpdateTaskStatus(db, task.ID, "in_progress", task.Version)
	require.NoError(t, err)

	// Second update with old version fails
	err = UpdateTaskStatus(db, task.ID, "completed", task.Version)
	assert.Error(t, err)
	assert.ErrorIs(t, err, ErrVersionConflict)

	// Verify final state
	final, err := GetTask(db, task.ID)
	require.NoError(t, err)
	assert.Equal(t, models.TaskStatusInProgress, final.Status)
	assert.Equal(t, 2, final.Version)
}

func TestGenerateTaskIDFormat(t *testing.T) {
	id := generateTaskID()
	require.True(t, taskIDPattern.MatchString(id), "unexpected task id format: %s", id)
}

func TestCreateTaskWithProject(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	task, err := CreateTask(db, "Project Task", "Desc", "proj_123", 0)
	require.NoError(t, err)
	assert.Equal(t, "proj_123", task.ProjectID)

	fetched, err := GetTask(db, task.ID)
	require.NoError(t, err)
	assert.Equal(t, "proj_123", fetched.ProjectID)
}

func TestListTasksByProject(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	_, err := CreateTask(db, "Task A", "", "proj_a", 0)
	require.NoError(t, err)
	_, err = CreateTask(db, "Task B", "", "proj_b", 0)
	require.NoError(t, err)
	_, err = CreateTask(db, "Task C", "", "proj_a", 0)
	require.NoError(t, err)

	tasks, err := ListTasks(db, "", "proj_a", -1)
	require.NoError(t, err)
	assert.Len(t, tasks, 2)

	tasks, err = ListTasks(db, "", "proj_b", -1)
	require.NoError(t, err)
	assert.Len(t, tasks, 1)

	// No filter returns all
	tasks, err = ListTasks(db, "", "", -1)
	require.NoError(t, err)
	assert.Len(t, tasks, 3)
}

func TestListTasks_PriorityFilter(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	_, err := CreateTask(db, "Low", "", "", 0)
	require.NoError(t, err)
	_, err = CreateTask(db, "Medium", "", "", 5)
	require.NoError(t, err)
	_, err = CreateTask(db, "High", "", "", 10)
	require.NoError(t, err)

	// Filter by priority 5
	tasks, err := ListTasks(db, "", "", 5)
	require.NoError(t, err)
	require.Len(t, tasks, 1)
	assert.Equal(t, "Medium", tasks[0].Title)
	assert.Equal(t, 5, tasks[0].Priority)

	// Filter by priority 10
	tasks, err = ListTasks(db, "", "", 10)
	require.NoError(t, err)
	require.Len(t, tasks, 1)
	assert.Equal(t, "High", tasks[0].Title)

	// No filter (-1) returns all, ordered by priority DESC
	tasks, err = ListTasks(db, "", "", -1)
	require.NoError(t, err)
	require.Len(t, tasks, 3)
	assert.Equal(t, "High", tasks[0].Title)
	assert.Equal(t, "Medium", tasks[1].Title)
	assert.Equal(t, "Low", tasks[2].Title)

	// Filter by nonexistent priority returns empty
	tasks, err = ListTasks(db, "", "", 99)
	require.NoError(t, err)
	assert.Empty(t, tasks)
}

func TestUpdateTaskPriorityWithEventTx(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	task, err := CreateTask(db, "Priority Test", "", "", 0)
	require.NoError(t, err)
	assert.Equal(t, 0, task.Priority)

	// Update priority inside transaction
	var eventID int64
	err = Transact(context.Background(), db, func(tx *sql.Tx) error {
		eid, txErr := UpdateTaskPriorityWithEventTx(tx, "test-agent", task.ID, 5, task.Version)
		if txErr != nil {
			return txErr
		}
		eventID = eid
		return nil
	})
	require.NoError(t, err)
	assert.Greater(t, eventID, int64(0))

	// Verify task updated
	updated, err := GetTask(db, task.ID)
	require.NoError(t, err)
	assert.Equal(t, 5, updated.Priority)
	assert.Equal(t, 2, updated.Version)

	// Verify event emitted
	events, err := FetchEventsSince(db, 0, 100, "")
	require.NoError(t, err)
	var found bool
	for _, e := range events {
		if e.Kind == "task_priority_changed" && e.TaskID == task.ID {
			found = true
			assert.Contains(t, e.Message, "5")
		}
	}
	assert.True(t, found, "expected task_priority_changed event")
}

func TestUpdateTaskPriorityWithEventTx_VersionConflict(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	task, err := CreateTask(db, "Conflict Test", "", "", 0)
	require.NoError(t, err)

	// First update succeeds
	err = Transact(context.Background(), db, func(tx *sql.Tx) error {
		_, txErr := UpdateTaskPriorityWithEventTx(tx, "test-agent", task.ID, 5, task.Version)
		return txErr
	})
	require.NoError(t, err)

	// Second update with stale version fails
	err = Transact(context.Background(), db, func(tx *sql.Tx) error {
		_, txErr := UpdateTaskPriorityWithEventTx(tx, "test-agent", task.ID, 10, task.Version)
		return txErr
	})
	assert.ErrorIs(t, err, ErrVersionConflict)
}
