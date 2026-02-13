package store

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGetStatusCounts_SingleAtomicQuery(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	// Empty DB should return all zeros
	counts, err := GetStatusCounts(db)
	require.NoError(t, err)
	assert.Equal(t, 0, counts.Tasks.Pending)
	assert.Equal(t, 0, counts.Tasks.InProgress)
	assert.Equal(t, 0, counts.Tasks.Completed)
	assert.Equal(t, 0, counts.Tasks.Blocked)
	assert.Equal(t, 0, counts.Events)
	assert.Equal(t, 0, counts.Memory)
	assert.Equal(t, 0, counts.Agents)
	assert.Equal(t, 0, counts.Projects)

	// Create some data
	task1, err := CreateTask(db, "Task 1", "desc", "", 0)
	require.NoError(t, err)

	task2, err := CreateTask(db, "Task 2", "desc", "", 0)
	require.NoError(t, err)

	err = UpdateTaskStatus(db, task1.ID, "completed", task1.Version)
	require.NoError(t, err)

	err = UpdateTaskStatus(db, task2.ID, "in_progress", task2.Version)
	require.NoError(t, err)

	// Create an agent
	_, err = LoadOrCreateAgentState(db, "test-agent")
	require.NoError(t, err)

	// Create a project
	_, err = CreateProject(db, "Test Project", "")
	require.NoError(t, err)

	// Create memory
	err = SetMemory(db, "key1", "val1", "string", "global", "", nil)
	require.NoError(t, err)

	// Log an event
	_, err = AppendEvent(db, "test_event", "test-agent", "", "test message")
	require.NoError(t, err)

	// Verify all counts are correct in a single read
	counts, err = GetStatusCounts(db)
	require.NoError(t, err)
	assert.Equal(t, 0, counts.Tasks.Pending, "pending tasks")
	assert.Equal(t, 1, counts.Tasks.InProgress, "in_progress tasks")
	assert.Equal(t, 1, counts.Tasks.Completed, "completed tasks")
	assert.Equal(t, 0, counts.Tasks.Blocked, "blocked tasks")
	assert.GreaterOrEqual(t, counts.Events, 1, "events count")
	assert.Equal(t, 1, counts.Memory, "memory count")
	assert.Equal(t, 1, counts.Agents, "agents count")
	assert.Equal(t, 1, counts.Projects, "projects count")

	// Detail structs should always be populated
	require.NotNil(t, counts.EventsDetail)
	require.NotNil(t, counts.MemoryDetail)
	require.NotNil(t, counts.AgentsDetail)
	require.NotNil(t, counts.TasksDetail)

	assert.GreaterOrEqual(t, counts.EventsDetail.Active, 1, "active events")
	assert.Equal(t, 0, counts.EventsDetail.Archived, "archived events")
	assert.Equal(t, 1, counts.MemoryDetail.Active, "active memory")
	assert.Equal(t, 0, counts.MemoryDetail.Superseded, "superseded memory")
	assert.Equal(t, 1, counts.AgentsDetail.Active7d, "agents active 7d")
	assert.Equal(t, 2, counts.TasksDetail.Total, "total tasks")
	assert.Equal(t, 0, counts.TasksDetail.Unknown, "unknown status tasks")
}
