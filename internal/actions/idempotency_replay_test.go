package actions

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dotcommander/vybe/internal/models"
	"github.com/dotcommander/vybe/internal/store"
)

func TestTaskCreateIdempotent_Replay(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	agent := "agent1"
	req := "req_task_create"

	task1, eid1, err := TaskCreateIdempotent(db, agent, req, "t1", "d1", "", 0)
	require.NoError(t, err)
	task2, eid2, err := TaskCreateIdempotent(db, agent, req, "t1", "d1", "", 0)
	require.NoError(t, err)

	require.Equal(t, task1.ID, task2.ID)
	require.Equal(t, eid1, eid2)

	var taskCount int
	require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM tasks`).Scan(&taskCount))
	require.Equal(t, 1, taskCount)

	var eventCount int
	require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM events WHERE kind = 'task_created'`).Scan(&eventCount))
	require.Equal(t, 1, eventCount)
}

func TestMemorySetIdempotent_Replay(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	agent := "agent1"
	req := "req_mem_set"

	eid1, err := MemorySetIdempotent(db, agent, req, "k", "v", "", "global", "", nil)
	require.NoError(t, err)
	eid2, err := MemorySetIdempotent(db, agent, req, "k", "v", "", "global", "", nil)
	require.NoError(t, err)
	require.Equal(t, eid1, eid2)

	var memCount int
	require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM memory WHERE scope = 'global' AND key = 'k'`).Scan(&memCount))
	require.Equal(t, 1, memCount)

	var eventCount int
	require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM events WHERE kind = 'memory_upserted'`).Scan(&eventCount))
	require.Equal(t, 1, eventCount)
}

func TestResumeIdempotent_Replay(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	agent := "agent1"

	// Seed: agent state + one task + one event so resume has deltas.
	_, err := store.LoadOrCreateAgentState(db, agent)
	require.NoError(t, err)
	task, _, err := TaskCreateIdempotent(db, agent, "req_seed_task", "t1", "d1", "", 0)
	require.NoError(t, err)
	_, err = store.AppendEventIdempotent(db, agent, "req_seed_event", "progress", task.ID, "p1")
	require.NoError(t, err)

	req := "req_resume"
	r1, err := ResumeWithOptionsIdempotent(db, agent, req, ResumeOptions{EventLimit: 1000})
	require.NoError(t, err)
	r2, err := ResumeWithOptionsIdempotent(db, agent, req, ResumeOptions{EventLimit: 1000})
	require.NoError(t, err)

	require.Equal(t, r1.NewCursor, r2.NewCursor)
	require.Equal(t, r1.FocusTaskID, r2.FocusTaskID)
	require.Equal(t, len(r1.Deltas), len(r2.Deltas))
}

func TestTaskStartIdempotent_Replay(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	agent := "agent1"
	task, _, err := TaskCreateIdempotent(db, agent, "req_seed_taskstart", "t1", "d1", "", 0)
	require.NoError(t, err)

	req := "req_task_start"
	r1, err := TaskStartIdempotent(db, agent, req, task.ID)
	require.NoError(t, err)
	r2, err := TaskStartIdempotent(db, agent, req, task.ID)
	require.NoError(t, err)

	require.Equal(t, r1.Task.ID, r2.Task.ID)
	require.Equal(t, models.TaskStatusInProgress, r2.Task.Status)
	require.Equal(t, r1.StatusEventID, r2.StatusEventID)
	require.Equal(t, r1.FocusEventID, r2.FocusEventID)

	var focusEvents int
	require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM events WHERE kind = 'agent_focus' AND task_id = ?`, task.ID).Scan(&focusEvents))
	require.Equal(t, 1, focusEvents)
}

func TestTaskSetStatusIdempotent_Replay(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	agent := "agent1"
	task, _, err := TaskCreateIdempotent(db, agent, "req_seed_taskstatus", "t1", "d1", "", 0)
	require.NoError(t, err)

	req := "req_task_set_status"
	t1, eid1, err := TaskSetStatusIdempotent(db, agent, req, task.ID, "blocked", "")
	require.NoError(t, err)
	t2, eid2, err := TaskSetStatusIdempotent(db, agent, req, task.ID, "blocked", "")
	require.NoError(t, err)

	require.Equal(t, t1.ID, t2.ID)
	require.Equal(t, models.TaskStatusBlocked, t2.Status)
	require.Equal(t, eid1, eid2)

	var cnt int
	require.NoError(t, db.QueryRow(
		`SELECT COUNT(*) FROM events WHERE kind = 'task_status' AND task_id = ? AND message = ?`,
		task.ID,
		"Status changed to: blocked",
	).Scan(&cnt))
	require.Equal(t, 1, cnt)
}

func TestMemoryDeleteIdempotent_Replay(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	agent := "agent1"
	_, err := MemorySetIdempotent(db, agent, "req_seed_memdel", "k", "v", "", "global", "", nil)
	require.NoError(t, err)

	req := "req_mem_delete"
	eid1, err := MemoryDeleteIdempotent(db, agent, req, "k", "global", "")
	require.NoError(t, err)
	eid2, err := MemoryDeleteIdempotent(db, agent, req, "k", "global", "")
	require.NoError(t, err)
	require.Equal(t, eid1, eid2)

	var memCount int
	require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM memory WHERE scope = 'global' AND key = 'k'`).Scan(&memCount))
	require.Equal(t, 0, memCount)

	var eventCount int
	require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM events WHERE kind = 'memory_delete'`).Scan(&eventCount))
	require.Equal(t, 1, eventCount)
}

func TestArtifactAddIdempotent_Replay(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	agent := "agent1"
	task, _, err := TaskCreateIdempotent(db, agent, "req_seed_artifact", "t1", "d1", "", 0)
	require.NoError(t, err)

	req := "req_artifact_add"
	a1, eid1, err := ArtifactAddIdempotent(db, agent, req, task.ID, "/tmp/out.txt", "text/plain")
	require.NoError(t, err)
	a2, eid2, err := ArtifactAddIdempotent(db, agent, req, task.ID, "/tmp/out.txt", "text/plain")
	require.NoError(t, err)

	require.Equal(t, a1.ID, a2.ID)
	require.Equal(t, eid1, eid2)

	var artCount int
	require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM artifacts WHERE task_id = ?`, task.ID).Scan(&artCount))
	require.Equal(t, 1, artCount)

	var eventCount int
	require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM events WHERE kind = 'artifact_added' AND task_id = ?`, task.ID).Scan(&eventCount))
	require.Equal(t, 1, eventCount)
}

func TestTaskDeleteIdempotent_Replay(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	agent := "agent1"
	task, _, err := TaskCreateIdempotent(db, agent, "req_seed_task_delete", "t1", "d1", "", 0)
	require.NoError(t, err)

	req := "req_task_delete"
	eid1, err := TaskDeleteIdempotent(db, agent, req, task.ID)
	require.NoError(t, err)
	eid2, err := TaskDeleteIdempotent(db, agent, req, task.ID)
	require.NoError(t, err)
	require.Equal(t, eid1, eid2)

	var taskCount int
	require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM tasks WHERE id = ?`, task.ID).Scan(&taskCount))
	require.Equal(t, 0, taskCount)

	var eventCount int
	require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM events WHERE kind = 'task_deleted' AND task_id = ?`, task.ID).Scan(&eventCount))
	require.Equal(t, 1, eventCount)
}

func TestProjectDeleteIdempotent_Replay(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	agent := "agent1"
	project, _, err := ProjectCreateIdempotent(db, agent, "req_seed_project_delete", "proj1", "")
	require.NoError(t, err)

	req := "req_project_delete"
	eid1, err := ProjectDeleteIdempotent(db, agent, req, project.ID)
	require.NoError(t, err)
	eid2, err := ProjectDeleteIdempotent(db, agent, req, project.ID)
	require.NoError(t, err)
	require.Equal(t, eid1, eid2)

	var projectCount int
	require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM projects WHERE id = ?`, project.ID).Scan(&projectCount))
	require.Equal(t, 0, projectCount)

	var eventCount int
	require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM events WHERE kind = 'project_deleted'`).Scan(&eventCount))
	require.Equal(t, 1, eventCount)
}

func TestTaskCreateIdempotent_ReplayAfterDeletion(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	agent := "agent1"
	req := "req_task_create_del"

	task1, eid1, err := TaskCreateIdempotent(db, agent, req, "t1", "d1", "", 0)
	require.NoError(t, err)
	require.NotNil(t, task1)

	// Delete the task directly, simulating a deletion after creation.
	_, err = db.Exec(`DELETE FROM tasks WHERE id = ?`, task1.ID)
	require.NoError(t, err)

	// Replay with the same request-id: must return the original snapshot, not an error.
	task2, eid2, err := TaskCreateIdempotent(db, agent, req, "t1", "d1", "", 0)
	require.NoError(t, err)
	require.NotNil(t, task2)

	require.Equal(t, task1.ID, task2.ID)
	require.Equal(t, task1.Title, task2.Title)
	require.Equal(t, task1.Description, task2.Description)
	require.Equal(t, models.TaskStatusPending, task2.Status)
	require.Equal(t, eid1, eid2)
}

func TestProjectCreateIdempotent_ReplayAfterDeletion(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	agent := "agent1"
	req := "req_project_create_del"

	proj1, eid1, err := ProjectCreateIdempotent(db, agent, req, "MyProject", "")
	require.NoError(t, err)
	require.NotNil(t, proj1)

	// Delete the project directly, simulating a deletion after creation.
	_, err = db.Exec(`DELETE FROM projects WHERE id = ?`, proj1.ID)
	require.NoError(t, err)

	// Replay with the same request-id: must return the original snapshot, not an error.
	proj2, eid2, err := ProjectCreateIdempotent(db, agent, req, "MyProject", "")
	require.NoError(t, err)
	require.NotNil(t, proj2)

	require.Equal(t, proj1.ID, proj2.ID)
	require.Equal(t, proj1.Name, proj2.Name)
	require.Equal(t, eid1, eid2)
}
