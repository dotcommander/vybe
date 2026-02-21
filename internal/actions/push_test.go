package actions

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dotcommander/vybe/internal/store"
)

func TestPushIdempotent_EventOnly(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	result, err := PushIdempotent(db, "test-agent", "push_event_only", PushInput{
		Event: &PushEventInput{
			Kind:    "progress",
			Message: "doing work",
		},
	})
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Greater(t, result.EventID, int64(0))
	assert.Empty(t, result.Memories)
	assert.Empty(t, result.Artifacts)
	assert.Nil(t, result.TaskStatus)
}

func TestPushIdempotent_MemoryOnly(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	result, err := PushIdempotent(db, "test-agent", "push_mem_only", PushInput{
		Memories: []PushMemoryInput{
			{
				Key:   "foo",
				Value: "bar",
				Scope: "global",
			},
		},
	})
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Len(t, result.Memories, 1)
	assert.NotEmpty(t, result.Memories[0].Key)
	assert.Greater(t, result.Memories[0].EventID, int64(0))
}

func TestPushIdempotent_ArtifactsOnly(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	task, _, err := TaskCreateIdempotent(db, "test-agent", "req_create_art", "Artifact Task", "", "", 0)
	require.NoError(t, err)

	result, err := PushIdempotent(db, "test-agent", "push_artifact_only", PushInput{
		TaskID: task.ID,
		Artifacts: []PushArtifactInput{
			{FilePath: "/tmp/output.txt"},
		},
	})
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Len(t, result.Artifacts, 1)
	assert.NotEmpty(t, result.Artifacts[0].ArtifactID)
	assert.Equal(t, "/tmp/output.txt", result.Artifacts[0].FilePath)
	assert.Greater(t, result.Artifacts[0].EventID, int64(0))
}

func TestPushIdempotent_TaskStatusOnly(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	task, _, err := TaskCreateIdempotent(db, "test-agent", "req_create_status", "Status Task", "", "", 0)
	require.NoError(t, err)

	result, err := PushIdempotent(db, "test-agent", "push_task_status_only", PushInput{
		TaskID: task.ID,
		TaskStatus: &PushTaskStatusInput{
			Status: "in_progress",
		},
	})
	require.NoError(t, err)
	require.NotNil(t, result)
	require.NotNil(t, result.TaskStatus)
	assert.Equal(t, "in_progress", result.TaskStatus.Status)
	assert.Greater(t, result.TaskStatus.StatusEventID, int64(0))

	updated, err := store.GetTask(db, task.ID)
	require.NoError(t, err)
	assert.Equal(t, "in_progress", string(updated.Status))
}

func TestPushIdempotent_AllFour(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	task, _, err := TaskCreateIdempotent(db, "test-agent", "req_create_all4", "All Four Task", "", "", 0)
	require.NoError(t, err)

	result, err := PushIdempotent(db, "test-agent", "push_all_four", PushInput{
		TaskID: task.ID,
		Event: &PushEventInput{
			Kind:    "progress",
			Message: "all four",
		},
		Memories: []PushMemoryInput{
			{Key: "all_key", Value: "all_val", Scope: "global"},
		},
		Artifacts: []PushArtifactInput{
			{FilePath: "/tmp/all.txt"},
		},
		TaskStatus: &PushTaskStatusInput{
			Status:  "completed",
			Summary: "Done",
		},
	})
	require.NoError(t, err)
	require.NotNil(t, result)

	assert.Greater(t, result.EventID, int64(0))
	require.Len(t, result.Memories, 1)
	assert.NotEmpty(t, result.Memories[0].Key)
	require.Len(t, result.Artifacts, 1)
	assert.Equal(t, "/tmp/all.txt", result.Artifacts[0].FilePath)
	require.NotNil(t, result.TaskStatus)
	assert.Equal(t, "completed", result.TaskStatus.Status)

	updated, err := store.GetTask(db, task.ID)
	require.NoError(t, err)
	assert.Equal(t, "completed", string(updated.Status))
}

func TestPushIdempotent_Replay(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	input := PushInput{
		Event: &PushEventInput{
			Kind:    "progress",
			Message: "replay test",
		},
	}

	first, err := PushIdempotent(db, "test-agent", "push_replay_req", input)
	require.NoError(t, err)
	require.NotNil(t, first)

	second, err := PushIdempotent(db, "test-agent", "push_replay_req", input)
	require.NoError(t, err)
	require.NotNil(t, second)

	assert.Equal(t, first.EventID, second.EventID)
}

func TestPushIdempotent_ArtifactRequiresTaskID(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	_, err := PushIdempotent(db, "test-agent", "push_art_no_task", PushInput{
		Artifacts: []PushArtifactInput{
			{FilePath: "/tmp/no_task.txt"},
		},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "task_id is required")
}

func TestPushIdempotent_TaskStatusRequiresTaskID(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	_, err := PushIdempotent(db, "test-agent", "push_status_no_task", PushInput{
		TaskStatus: &PushTaskStatusInput{
			Status: "in_progress",
		},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "task_id is required")
}

func TestPushIdempotent_EmptyPush(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	_, err := PushIdempotent(db, "test-agent", "push_empty", PushInput{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "at least one operation")
}

func TestPushIdempotent_UnblocksDependentsOnComplete(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	taskA, _, err := TaskCreateIdempotent(db, "test-agent", "req_create_A", "Task A", "", "", 0)
	require.NoError(t, err)

	taskB, _, err := TaskCreateIdempotent(db, "test-agent", "req_create_B", "Task B", "", "", 0)
	require.NoError(t, err)

	// B depends on A
	err = TaskAddDependencyIdempotent(db, "test-agent", "req_dep_B_on_A", taskB.ID, taskA.ID)
	require.NoError(t, err)

	// Block B
	_, _, err = TaskSetStatusIdempotent(db, "test-agent", "req_block_B", taskB.ID, "blocked", "dependency")
	require.NoError(t, err)

	// Complete A via push
	_, err = PushIdempotent(db, "test-agent", "push_complete_A", PushInput{
		TaskID: taskA.ID,
		TaskStatus: &PushTaskStatusInput{
			Status:  "completed",
			Summary: "Done",
		},
	})
	require.NoError(t, err)

	// B should now be pending (unblocked)
	bTask, err := store.GetTask(db, taskB.ID)
	require.NoError(t, err)
	assert.Equal(t, "pending", string(bTask.Status))
}
