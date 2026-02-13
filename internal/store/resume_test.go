package store

import (
	"testing"

	"github.com/dotcommander/vybe/internal/models"
	"github.com/stretchr/testify/require"
)

func TestFetchEventsSince(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	// Create some events
	_, err := AppendEvent(db, "test.event", "agent1", "task1", "Event 1")
	if err != nil {
		t.Fatalf("Failed to append event: %v", err)
	}

	_, err = AppendEvent(db, "test.event", "agent2", "task2", "Event 2")
	if err != nil {
		t.Fatalf("Failed to append event: %v", err)
	}

	eventID3, err := AppendEvent(db, "test.event", "agent3", "task3", "Event 3")
	if err != nil {
		t.Fatalf("Failed to append event: %v", err)
	}

	// Fetch events since cursor 0
	events, err := FetchEventsSince(db, 0, 100, "")
	if err != nil {
		t.Fatalf("FetchEventsSince failed: %v", err)
	}

	if len(events) != 3 {
		t.Errorf("Expected 3 events, got %d", len(events))
	}

	// Fetch events since cursor 1 (should get 2 events)
	events, err = FetchEventsSince(db, 1, 100, "")
	if err != nil {
		t.Fatalf("FetchEventsSince failed: %v", err)
	}

	if len(events) != 2 {
		t.Errorf("Expected 2 events, got %d", len(events))
	}

	// Fetch events since last event (should get 0 events)
	events, err = FetchEventsSince(db, eventID3, 100, "")
	if err != nil {
		t.Fatalf("FetchEventsSince failed: %v", err)
	}

	if len(events) != 0 {
		t.Errorf("Expected 0 events, got %d", len(events))
	}
}

func TestFetchEventsSinceRespectLimit(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	// Create 5 events
	for i := 0; i < 5; i++ {
		_, err := AppendEvent(db, "test.event", "agent", "task", "Event")
		if err != nil {
			t.Fatalf("Failed to append event: %v", err)
		}
	}

	// Fetch with limit 3
	events, err := FetchEventsSince(db, 0, 3, "")
	if err != nil {
		t.Fatalf("FetchEventsSince failed: %v", err)
	}

	if len(events) != 3 {
		t.Errorf("Expected 3 events (limit), got %d", len(events))
	}
}

func TestDetermineFocusTask_KeepInProgress(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	// Create a task with in_progress status
	task, err := CreateTask(db, "Active Task", "Description", "", 0)
	if err != nil {
		t.Fatalf("Failed to create task: %v", err)
	}

	// Update status to in_progress
	err = UpdateTaskStatus(db, task.ID, "in_progress", task.Version)
	if err != nil {
		t.Fatalf("Failed to update task status: %v", err)
	}

	// Determine focus (should keep in_progress task)
	focusID, err := DetermineFocusTask(db, "agent1", task.ID, []*models.Event{}, "")
	if err != nil {
		t.Fatalf("DetermineFocusTask failed: %v", err)
	}

	if focusID != task.ID {
		t.Errorf("Expected focus to remain on in_progress task %s, got %s", task.ID, focusID)
	}
}

func TestDetermineFocusTask_FailureBlockedSkips(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	// Create two tasks: one blocked (no deps = failure-blocked), one pending
	blockedTask, err := CreateTask(db, "Blocked Task", "Description", "", 0)
	require.NoError(t, err)

	pendingTask, err := CreateTask(db, "Pending Task", "Description", "", 0)
	require.NoError(t, err)

	// Update to blocked (but no dependency — simulates failure-blocked)
	err = UpdateTaskStatus(db, blockedTask.ID, "blocked", blockedTask.Version)
	require.NoError(t, err)

	// Determine focus: should skip failure-blocked and pick pending task
	focusID, err := DetermineFocusTask(db, "agent1", blockedTask.ID, []*models.Event{}, "")
	require.NoError(t, err)
	require.Equal(t, pendingTask.ID, focusID, "Should skip failure-blocked focus and pick pending task")
}

func TestDetermineFocusTask_DependencyBlockedKeeps(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	// Create two tasks with a dependency
	depTask, err := CreateTask(db, "Dependency Task", "Description", "", 0)
	require.NoError(t, err)

	blockedTask, err := CreateTask(db, "Blocked Task", "Description", "", 0)
	require.NoError(t, err)

	// blockedTask depends on depTask (will auto-block)
	err = AddTaskDependency(db, blockedTask.ID, depTask.ID)
	require.NoError(t, err)

	// Verify it's blocked
	refreshed, err := GetTask(db, blockedTask.ID)
	require.NoError(t, err)
	require.Equal(t, models.TaskStatusBlocked, refreshed.Status)

	// Determine focus: should KEEP dependency-blocked focus
	focusID, err := DetermineFocusTask(db, "agent1", blockedTask.ID, []*models.Event{}, "")
	require.NoError(t, err)
	require.Equal(t, blockedTask.ID, focusID, "Should keep dependency-blocked focus")
}

func TestDetermineFocusTask_TaskAssigned_SkipsClaimedByOther(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	// Create a pending task and claim it by another agent
	task, err := CreateTask(db, "Claimed Task", "Description", "", 0)
	require.NoError(t, err)
	err = ClaimTask(db, "other-agent", task.ID, 60) // 60 min TTL
	require.NoError(t, err)

	// Create a fallback pending task
	fallback, err := CreateTask(db, "Fallback Task", "Description", "", 0)
	require.NoError(t, err)

	// task_assigned event points to the claimed task
	deltas := []*models.Event{{Kind: "task_assigned", TaskID: task.ID}}

	// agent1 should skip the claimed task and fall through to Rule 4
	focusID, err := DetermineFocusTask(db, "agent1", "", deltas, "")
	require.NoError(t, err)
	require.Equal(t, fallback.ID, focusID, "Should skip task claimed by other agent")
}

func TestDetermineFocusTask_TaskAssignedEvent(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	// Create two tasks
	task1, err := CreateTask(db, "Task 1", "Description", "", 0)
	if err != nil {
		t.Fatalf("Failed to create task 1: %v", err)
	}

	task2, err := CreateTask(db, "Task 2", "Description", "", 0)
	if err != nil {
		t.Fatalf("Failed to create task 2: %v", err)
	}

	// Create task_assigned event for task2
	deltas := []*models.Event{
		{
			Kind:   "task_assigned",
			TaskID: task2.ID,
		},
	}

	// Determine focus (should select task2 from assignment event)
	focusID, err := DetermineFocusTask(db, "agent1", task1.ID, deltas, "")
	if err != nil {
		t.Fatalf("DetermineFocusTask failed: %v", err)
	}

	if focusID != task2.ID {
		t.Errorf("Expected focus on assigned task %s, got %s", task2.ID, focusID)
	}
}

func TestDetermineFocusTask_OldestPending(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	// Create tasks (they'll be ordered by created_at)
	task1, err := CreateTask(db, "Task 1", "Oldest", "", 0)
	if err != nil {
		t.Fatalf("Failed to create task 1: %v", err)
	}

	_, err = CreateTask(db, "Task 2", "Newer", "", 0)
	if err != nil {
		t.Fatalf("Failed to create task 2: %v", err)
	}

	// Determine focus with no current focus (should select oldest pending)
	focusID, err := DetermineFocusTask(db, "agent1", "", []*models.Event{}, "")
	if err != nil {
		t.Fatalf("DetermineFocusTask failed: %v", err)
	}

	if focusID != task1.ID {
		t.Errorf("Expected focus on oldest task %s, got %s", task1.ID, focusID)
	}
}

func TestDetermineFocusTask_NoWorkAvailable(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	// Determine focus with no tasks
	focusID, err := DetermineFocusTask(db, "agent1", "", []*models.Event{}, "")
	if err != nil {
		t.Fatalf("DetermineFocusTask failed: %v", err)
	}

	if focusID != "" {
		t.Errorf("Expected empty focus (no work), got %s", focusID)
	}
}

func TestBuildBrief_EmptyTask(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	// Build brief with no focus task
	brief, err := BuildBrief(db, "", "", "")
	if err != nil {
		t.Fatalf("BuildBrief failed: %v", err)
	}

	if brief.Task != nil {
		t.Errorf("Expected nil task, got %v", brief.Task)
	}
	require.NotNil(t, brief.RelevantMemory)
	require.NotNil(t, brief.RecentEvents)
	require.NotNil(t, brief.Artifacts)
	require.Len(t, brief.RelevantMemory, 0)
	require.Len(t, brief.RecentEvents, 0)
	require.Len(t, brief.Artifacts, 0)
	if brief.ApproxTokens != 0 {
		t.Errorf("Expected approx_tokens=0, got %d", brief.ApproxTokens)
	}
}

func TestBuildBrief_WithTask(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	// Create a task
	task, err := CreateTask(db, "Test Task", "Description", "", 0)
	if err != nil {
		t.Fatalf("Failed to create task: %v", err)
	}

	// Add some memory
	err = SetMemory(db, "key1", "value1", "string", "global", "", nil)
	if err != nil {
		t.Fatalf("Failed to set global memory: %v", err)
	}

	err = SetMemory(db, "key2", "value2", "string", "task", task.ID, nil)
	if err != nil {
		t.Fatalf("Failed to set task memory: %v", err)
	}

	// Add some events
	_, err = AppendEvent(db, "task.started", "agent1", task.ID, "Started work")
	if err != nil {
		t.Fatalf("Failed to append event: %v", err)
	}

	// Build brief
	brief, err := BuildBrief(db, task.ID, "", "")
	if err != nil {
		t.Fatalf("BuildBrief failed: %v", err)
	}

	if brief.Task == nil {
		t.Fatalf("Expected task in brief")
	}

	if brief.Task.ID != task.ID {
		t.Errorf("Expected task ID %s, got %s", task.ID, brief.Task.ID)
	}

	if len(brief.RelevantMemory) != 2 {
		t.Errorf("Expected 2 memory entries, got %d", len(brief.RelevantMemory))
	}

	if len(brief.RecentEvents) != 1 {
		t.Errorf("Expected 1 event, got %d", len(brief.RecentEvents))
	}

	if brief.ApproxTokens != 3 { // len("Started work")=12 -> ceil(12/4)=3
		t.Errorf("Expected approx_tokens=3, got %d", brief.ApproxTokens)
	}
}

func TestBuildBrief_ApproxTokensFromEventMessages(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	task, err := CreateTask(db, "Token Task", "Description", "", 0)
	if err != nil {
		t.Fatalf("Failed to create task: %v", err)
	}

	_, err = AppendEvent(db, "task.note", "agent1", task.ID, "abcd")
	if err != nil {
		t.Fatalf("Failed to append first event: %v", err)
	}
	_, err = AppendEvent(db, "task.note", "agent1", task.ID, "12345")
	if err != nil {
		t.Fatalf("Failed to append second event: %v", err)
	}

	brief, err := BuildBrief(db, task.ID, "", "")
	if err != nil {
		t.Fatalf("BuildBrief failed: %v", err)
	}

	// Total chars = 9, rough tokens = ceil(9/4) = 3
	if brief.ApproxTokens != 3 {
		t.Errorf("Expected approx_tokens=3, got %d", brief.ApproxTokens)
	}
}

func TestUpdateAgentStateAtomic(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	// Create agent state
	_, err := LoadOrCreateAgentState(db, "test-agent")
	if err != nil {
		t.Fatalf("Failed to create agent state: %v", err)
	}

	// Create a task
	task, err := CreateTask(db, "Test Task", "Description", "", 0)
	if err != nil {
		t.Fatalf("Failed to create task: %v", err)
	}

	// Update cursor and focus atomically
	err = UpdateAgentStateAtomic(db, "test-agent", 42, task.ID)
	if err != nil {
		t.Fatalf("UpdateAgentStateAtomic failed: %v", err)
	}

	// Verify update
	state, err := LoadOrCreateAgentState(db, "test-agent")
	if err != nil {
		t.Fatalf("Failed to load agent state: %v", err)
	}

	if state.LastSeenEventID != 42 {
		t.Errorf("Expected cursor 42, got %d", state.LastSeenEventID)
	}

	if state.FocusTaskID != task.ID {
		t.Errorf("Expected focus task %s, got %s", task.ID, state.FocusTaskID)
	}
}

func TestUpdateAgentStateAtomic_MonotonicCursor(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	// Create agent state
	_, err := LoadOrCreateAgentState(db, "test-agent")
	if err != nil {
		t.Fatalf("Failed to create agent state: %v", err)
	}

	// Advance cursor to 100
	err = UpdateAgentStateAtomic(db, "test-agent", 100, "")
	if err != nil {
		t.Fatalf("UpdateAgentStateAtomic failed: %v", err)
	}

	// Try to move cursor backward to 50 (should stay at 100 due to MAX)
	err = UpdateAgentStateAtomic(db, "test-agent", 50, "")
	if err != nil {
		t.Fatalf("UpdateAgentStateAtomic failed: %v", err)
	}

	// Verify cursor didn't go backward
	state, err := LoadOrCreateAgentState(db, "test-agent")
	if err != nil {
		t.Fatalf("Failed to load agent state: %v", err)
	}

	if state.LastSeenEventID != 100 {
		t.Errorf("Expected cursor to remain at 100 (monotonic), got %d", state.LastSeenEventID)
	}
}

func TestFetchRelevantMemory(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	// Create a task
	task, err := CreateTask(db, "Test Task", "Description", "", 0)
	if err != nil {
		t.Fatalf("Failed to create task: %v", err)
	}

	// Add global memory
	err = SetMemory(db, "global_key", "global_value", "string", "global", "", nil)
	if err != nil {
		t.Fatalf("Failed to set global memory: %v", err)
	}

	// Add task-specific memory
	err = SetMemory(db, "task_key", "task_value", "string", "task", task.ID, nil)
	if err != nil {
		t.Fatalf("Failed to set task memory: %v", err)
	}

	// Add agent memory (should NOT be included)
	err = SetMemory(db, "agent_key", "agent_value", "string", "agent", "agent1", nil)
	if err != nil {
		t.Fatalf("Failed to set agent memory: %v", err)
	}

	// Fetch relevant memory
	memories, err := fetchRelevantMemory(db, task.ID, "")
	if err != nil {
		t.Fatalf("fetchRelevantMemory failed: %v", err)
	}

	// Should have global + task memory, but NOT agent memory
	if len(memories) != 2 {
		t.Errorf("Expected 2 memory entries (global + task), got %d", len(memories))
	}

	// Verify agent memory is excluded
	for _, mem := range memories {
		if mem.Scope == models.MemoryScopeAgent {
			t.Errorf("Agent-scoped memory should not be included in brief")
		}
	}
}

func TestDetermineFocusTask_Determinism(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	// Create multiple pending tasks
	task1, _ := CreateTask(db, "Task 1", "First", "", 0)
	_, _ = CreateTask(db, "Task 2", "Second", "", 0)
	_, _ = CreateTask(db, "Task 3", "Third", "", 0)

	// Run determination multiple times with same state
	focusID1, err := DetermineFocusTask(db, "agent1", "", []*models.Event{}, "")
	if err != nil {
		t.Fatalf("DetermineFocusTask failed: %v", err)
	}

	focusID2, err := DetermineFocusTask(db, "agent1", "", []*models.Event{}, "")
	if err != nil {
		t.Fatalf("DetermineFocusTask failed: %v", err)
	}

	// Should always return the same result (deterministic)
	if focusID1 != focusID2 {
		t.Errorf("DetermineFocusTask not deterministic: got %s then %s", focusID1, focusID2)
	}

	// Should be the oldest task
	if focusID1 != task1.ID {
		t.Errorf("Expected oldest task %s, got %s", task1.ID, focusID1)
	}
}

func TestBuildBrief_WithProject(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	project, err := CreateProject(db, "My Project", `{"key":"val"}`)
	if err != nil {
		t.Fatalf("Failed to create project: %v", err)
	}

	task, err := CreateTask(db, "Task", "Desc", project.ID, 0)
	if err != nil {
		t.Fatalf("Failed to create task: %v", err)
	}

	// Add project-scoped memory
	err = SetMemory(db, "pkey", "pval", "string", "project", project.ID, nil)
	if err != nil {
		t.Fatalf("Failed to set project memory: %v", err)
	}

	brief, err := BuildBrief(db, task.ID, project.ID, "")
	if err != nil {
		t.Fatalf("BuildBrief failed: %v", err)
	}
	if brief.Project == nil {
		t.Fatalf("Expected project in brief")
	}
	if brief.Project.ID != project.ID {
		t.Errorf("Expected project ID %s, got %s", project.ID, brief.Project.ID)
	}
	if brief.Project.Name != "My Project" {
		t.Errorf("Expected project name 'My Project', got %s", brief.Project.Name)
	}

	// Memory should include project-scoped
	found := false
	for _, m := range brief.RelevantMemory {
		if m.Scope == models.MemoryScopeProject && m.ScopeID == project.ID {
			found = true
		}
	}
	if !found {
		t.Errorf("Expected project-scoped memory in brief")
	}
}

func TestFetchRelevantMemory_ProjectFiltered(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	task, err := CreateTask(db, "Task", "Desc", "", 0)
	if err != nil {
		t.Fatalf("Failed to create task: %v", err)
	}

	// Two projects
	err = SetMemory(db, "p1key", "p1val", "string", "project", "proj_1", nil)
	if err != nil {
		t.Fatalf("Failed to set project 1 memory: %v", err)
	}
	err = SetMemory(db, "p2key", "p2val", "string", "project", "proj_2", nil)
	if err != nil {
		t.Fatalf("Failed to set project 2 memory: %v", err)
	}

	// Filtered: should only get proj_1
	memories, err := fetchRelevantMemory(db, task.ID, "proj_1")
	if err != nil {
		t.Fatalf("fetchRelevantMemory failed: %v", err)
	}
	for _, m := range memories {
		if m.Scope == models.MemoryScopeProject {
			if m.ScopeID != "proj_1" {
				t.Errorf("Expected only proj_1, got %s", m.ScopeID)
			}
		}
	}

	// Unfiltered: should get both
	memories, err = fetchRelevantMemory(db, task.ID, "")
	if err != nil {
		t.Fatalf("fetchRelevantMemory failed: %v", err)
	}
	projectCount := 0
	for _, m := range memories {
		if m.Scope == models.MemoryScopeProject {
			projectCount++
		}
	}
	if projectCount != 2 {
		t.Errorf("Expected 2 project memories, got %d", projectCount)
	}
}

func TestFetchRelevantMemory_FiltersSupersededAndLowSignal(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	task, err := CreateTask(db, "Task", "Desc", "", 0)
	if err != nil {
		t.Fatalf("Failed to create task: %v", err)
	}

	// High signal, should remain.
	require.NoError(t, SetMemory(db, "high", "value", "string", "task", task.ID, nil))
	_, err = db.Exec(`UPDATE memory SET confidence = 0.9, last_seen_at = CURRENT_TIMESTAMP WHERE key = 'high' AND scope = 'task' AND scope_id = ?`, task.ID)
	require.NoError(t, err)

	// Low confidence and stale, should be filtered out.
	require.NoError(t, SetMemory(db, "stale_low", "value", "string", "task", task.ID, nil))
	_, err = db.Exec(`
		UPDATE memory
		SET confidence = 0.1,
		    last_seen_at = datetime('now', '-30 days')
		WHERE key = 'stale_low' AND scope = 'task' AND scope_id = ?
	`, task.ID)
	require.NoError(t, err)

	// Superseded, should be filtered out.
	require.NoError(t, SetMemory(db, "superseded", "value", "string", "task", task.ID, nil))
	_, err = db.Exec(`UPDATE memory SET superseded_by = 'memory_summary' WHERE key = 'superseded' AND scope = 'task' AND scope_id = ?`, task.ID)
	require.NoError(t, err)

	memories, err := fetchRelevantMemory(db, task.ID, "")
	if err != nil {
		t.Fatalf("fetchRelevantMemory failed: %v", err)
	}

	keys := map[string]bool{}
	for _, m := range memories {
		keys[m.Key] = true
	}

	if !keys["high"] {
		t.Fatalf("expected high memory to be present")
	}
	if keys["stale_low"] {
		t.Fatalf("did not expect stale low-confidence memory")
	}
	if keys["superseded"] {
		t.Fatalf("did not expect superseded memory")
	}
}

func TestDetermineFocusTask_ProjectScoped(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	// Task without project
	_, err := CreateTask(db, "Global Task", "Desc", "", 0)
	if err != nil {
		t.Fatalf("Failed to create global task: %v", err)
	}

	// Task with project
	projTask, err := CreateTask(db, "Project Task", "Desc", "proj_x", 0)
	if err != nil {
		t.Fatalf("Failed to create project task: %v", err)
	}

	// With project filter, should prefer project task
	focusID, err := DetermineFocusTask(db, "agent1", "", []*models.Event{}, "proj_x")
	if err != nil {
		t.Fatalf("DetermineFocusTask failed: %v", err)
	}
	if focusID != projTask.ID {
		t.Errorf("Expected focus on project task %s, got %s", projTask.ID, focusID)
	}

	// If no pending task exists in the active project, do not fall back to global.
	err = UpdateTaskStatus(db, projTask.ID, "completed", projTask.Version)
	if err != nil {
		t.Fatalf("Failed to complete project task: %v", err)
	}

	focusID, err = DetermineFocusTask(db, "agent1", "", []*models.Event{}, "proj_x")
	if err != nil {
		t.Fatalf("DetermineFocusTask failed on strict project scope: %v", err)
	}
	if focusID != "" {
		t.Errorf("Expected no focus task outside project scope, got %s", focusID)
	}
}

func TestFetchEventsSince_ProjectIsolation(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	projectTask, err := CreateTask(db, "Project Task", "Desc", "proj_a", 0)
	if err != nil {
		t.Fatalf("Failed to create project task: %v", err)
	}
	globalTask, err := CreateTask(db, "Global Task", "Desc", "", 0)
	if err != nil {
		t.Fatalf("Failed to create global task: %v", err)
	}
	otherTask, err := CreateTask(db, "Other Task", "Desc", "proj_b", 0)
	if err != nil {
		t.Fatalf("Failed to create other project task: %v", err)
	}

	_, err = AppendEvent(db, "task.note", "agent1", projectTask.ID, "project A event")
	if err != nil {
		t.Fatalf("Failed to append project event: %v", err)
	}
	_, err = AppendEvent(db, "task.note", "agent1", globalTask.ID, "global event")
	if err != nil {
		t.Fatalf("Failed to append global event: %v", err)
	}
	_, err = AppendEvent(db, "task.note", "agent1", otherTask.ID, "project B event")
	if err != nil {
		t.Fatalf("Failed to append other project event: %v", err)
	}

	events, err := FetchEventsSince(db, 0, 20, "proj_a")
	if err != nil {
		t.Fatalf("FetchEventsSince failed: %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("Expected 2 scoped events, got %d", len(events))
	}

	for _, e := range events {
		if e.ProjectID != "" && e.ProjectID != "proj_a" {
			t.Fatalf("Expected only proj_a or global events, got project_id=%q", e.ProjectID)
		}
	}
}

func TestDetermineFocusTask_PriorityOrdering(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	// Create tasks with different priorities
	lowTask, err := CreateTask(db, "Low Priority", "Desc", "", 0)
	if err != nil {
		t.Fatalf("Failed to create low priority task: %v", err)
	}

	_, err = CreateTask(db, "Normal Priority", "Desc", "", 5)
	if err != nil {
		t.Fatalf("Failed to create normal priority task: %v", err)
	}

	highTask, err := CreateTask(db, "High Priority", "Desc", "", 10)
	if err != nil {
		t.Fatalf("Failed to create high priority task: %v", err)
	}

	// Rule 4: Should select highest priority task
	focusID, err := DetermineFocusTask(db, "agent1", "", []*models.Event{}, "")
	if err != nil {
		t.Fatalf("DetermineFocusTask failed: %v", err)
	}

	if focusID != highTask.ID {
		t.Errorf("Expected focus on high priority task %s, got %s", highTask.ID, focusID)
	}

	// Mark high priority as completed
	err = UpdateTaskStatus(db, highTask.ID, "completed", highTask.Version)
	if err != nil {
		t.Fatalf("Failed to complete high priority task: %v", err)
	}

	// Should now select normal priority (5)
	focusID, err = DetermineFocusTask(db, "agent1", "", []*models.Event{}, "")
	if err != nil {
		t.Fatalf("DetermineFocusTask failed: %v", err)
	}

	// Create another task with same priority as lowTask (both priority 0)
	lowTask2, err := CreateTask(db, "Another Low Priority", "Desc", "", 0)
	if err != nil {
		t.Fatalf("Failed to create another low priority task: %v", err)
	}

	// Mark normal priority as completed
	normalTask, err := GetTask(db, focusID)
	if err != nil {
		t.Fatalf("Failed to get normal task: %v", err)
	}
	err = UpdateTaskStatus(db, normalTask.ID, "completed", normalTask.Version)
	if err != nil {
		t.Fatalf("Failed to complete normal priority task: %v", err)
	}

	// Now with two tasks at priority 0, should select oldest (lowTask created first)
	focusID, err = DetermineFocusTask(db, "agent1", "", []*models.Event{}, "")
	if err != nil {
		t.Fatalf("DetermineFocusTask failed: %v", err)
	}

	if focusID != lowTask.ID {
		t.Errorf("Expected focus on oldest low priority task %s, got %s", lowTask.ID, focusID)
	}

	// Mark oldest low priority as completed
	err = UpdateTaskStatus(db, lowTask.ID, "completed", lowTask.Version)
	if err != nil {
		t.Fatalf("Failed to complete oldest low priority task: %v", err)
	}

	// Should now select the newer low priority task
	focusID, err = DetermineFocusTask(db, "agent1", "", []*models.Event{}, "")
	if err != nil {
		t.Fatalf("DetermineFocusTask failed: %v", err)
	}

	if focusID != lowTask2.ID {
		t.Errorf("Expected focus on newer low priority task %s, got %s", lowTask2.ID, focusID)
	}
}

func TestFetchPriorReasoning_Empty(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	events, err := FetchPriorReasoning(db, "", 10)
	require.NoError(t, err)
	require.Empty(t, events)
}

func TestFetchPriorReasoning_WithEvents(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	_, err := AppendEvent(db, "reasoning", "agent1", "", "intent: build feature X")
	require.NoError(t, err)
	_, err = AppendEvent(db, "user_prompt", "agent1", "", "not reasoning")
	require.NoError(t, err)
	_, err = AppendEvent(db, "reasoning", "agent1", "", "intent: fix bug Y")
	require.NoError(t, err)

	events, err := FetchPriorReasoning(db, "", 10)
	require.NoError(t, err)
	require.Len(t, events, 2)
	// Newest first
	require.Equal(t, "intent: fix bug Y", events[0].Message)
	require.Equal(t, "intent: build feature X", events[1].Message)
}

func TestFetchPriorReasoning_ProjectScoped(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	_, err := AppendEventWithProjectAndMetadataIdempotent(
		db, "agent1", "req1", "reasoning", "proj_a", "", "reasoning for A", "",
	)
	require.NoError(t, err)
	_, err = AppendEventWithProjectAndMetadataIdempotent(
		db, "agent1", "req2", "reasoning", "proj_b", "", "reasoning for B", "",
	)
	require.NoError(t, err)
	_, err = AppendEvent(db, "reasoning", "agent1", "", "global reasoning")
	require.NoError(t, err)

	// Scoped to proj_a: should get proj_a + global
	events, err := FetchPriorReasoning(db, "proj_a", 10)
	require.NoError(t, err)
	require.Len(t, events, 2)
	for _, e := range events {
		if e.ProjectID != "" && e.ProjectID != "proj_a" {
			t.Errorf("Expected only proj_a or global events, got project_id=%q", e.ProjectID)
		}
	}
}

func TestFetchPriorReasoning_ExcludesArchived(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	id, err := AppendEvent(db, "reasoning", "agent1", "", "archived reasoning")
	require.NoError(t, err)
	_, err = db.Exec(`UPDATE events SET archived_at = CURRENT_TIMESTAMP WHERE id = ?`, id)
	require.NoError(t, err)

	_, err = AppendEvent(db, "reasoning", "agent1", "", "active reasoning")
	require.NoError(t, err)

	events, err := FetchPriorReasoning(db, "", 10)
	require.NoError(t, err)
	require.Len(t, events, 1)
	require.Equal(t, "active reasoning", events[0].Message)
}

func TestFetchSessionEvents_Empty(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	events, err := FetchSessionEvents(db, 0, "", 200)
	require.NoError(t, err)
	require.Empty(t, events)
}

func TestFetchSessionEvents_FiltersByKind(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	_, err := AppendEvent(db, "user_prompt", "agent1", "", "a prompt")
	require.NoError(t, err)
	_, err = AppendEvent(db, "reasoning", "agent1", "", "some reasoning")
	require.NoError(t, err)
	_, err = AppendEvent(db, "tool_failure", "agent1", "", "bash failed")
	require.NoError(t, err)
	_, err = AppendEvent(db, "task_status", "agent1", "", "completed")
	require.NoError(t, err)
	_, err = AppendEvent(db, "progress", "agent1", "", "step done")
	require.NoError(t, err)
	// This kind should be excluded
	_, err = AppendEvent(db, "task.note", "agent1", "", "random note")
	require.NoError(t, err)

	events, err := FetchSessionEvents(db, 0, "", 200)
	require.NoError(t, err)
	require.Len(t, events, 5)
	// Chronological order
	require.Equal(t, "user_prompt", events[0].Kind)
	require.Equal(t, "progress", events[4].Kind)
}

func TestFetchSessionEvents_ProjectScoped(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	_, err := AppendEventWithProjectAndMetadataIdempotent(
		db, "agent1", "req1", "user_prompt", "proj_a", "", "prompt for A", "",
	)
	require.NoError(t, err)
	_, err = AppendEventWithProjectAndMetadataIdempotent(
		db, "agent1", "req2", "user_prompt", "proj_b", "", "prompt for B", "",
	)
	require.NoError(t, err)
	_, err = AppendEvent(db, "progress", "agent1", "", "global progress")
	require.NoError(t, err)

	events, err := FetchSessionEvents(db, 0, "proj_a", 200)
	require.NoError(t, err)
	require.Len(t, events, 2) // proj_a + global
	for _, e := range events {
		if e.ProjectID != "" && e.ProjectID != "proj_a" {
			t.Errorf("Expected only proj_a or global events, got project_id=%q", e.ProjectID)
		}
	}
}

// --- Discovery context tests ---

func TestGetTaskStatusCounts_Global(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	t1, err := CreateTask(db, "Pending 1", "", "", 0)
	require.NoError(t, err)
	_, err = CreateTask(db, "Pending 2", "", "", 0)
	require.NoError(t, err)

	t3, err := CreateTask(db, "In Progress", "", "", 0)
	require.NoError(t, err)
	require.NoError(t, UpdateTaskStatus(db, t3.ID, "in_progress", t3.Version))

	t4, err := CreateTask(db, "Completed", "", "", 0)
	require.NoError(t, err)
	require.NoError(t, UpdateTaskStatus(db, t4.ID, "completed", t4.Version))

	// blocked via dependency
	require.NoError(t, AddTaskDependency(db, t1.ID, t3.ID))
	refreshed, err := GetTask(db, t1.ID)
	require.NoError(t, err)
	require.Equal(t, models.TaskStatusBlocked, refreshed.Status)

	counts, err := GetTaskStatusCounts(db, "")
	require.NoError(t, err)
	require.Equal(t, 1, counts.Pending)    // Pending 2
	require.Equal(t, 1, counts.InProgress) // In Progress
	require.Equal(t, 1, counts.Completed)  // Completed
	require.Equal(t, 1, counts.Blocked)    // Pending 1 (dep-blocked)
}

func TestGetTaskStatusCounts_ProjectScoped(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	_, err := CreateTask(db, "Global Pending", "", "", 0)
	require.NoError(t, err)
	_, err = CreateTask(db, "Proj A Pending", "", "proj_a", 0)
	require.NoError(t, err)
	tB, err := CreateTask(db, "Proj B Done", "", "proj_b", 0)
	require.NoError(t, err)
	require.NoError(t, UpdateTaskStatus(db, tB.ID, "completed", tB.Version))

	counts, err := GetTaskStatusCounts(db, "proj_a")
	require.NoError(t, err)
	require.Equal(t, 1, counts.Pending)
	require.Equal(t, 0, counts.InProgress)
	require.Equal(t, 0, counts.Completed)
	require.Equal(t, 0, counts.Blocked)
}

func TestFetchPipelineTasks_Ordering(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	low, err := CreateTask(db, "Low Priority", "", "", 1)
	require.NoError(t, err)
	high, err := CreateTask(db, "High Priority", "", "", 10)
	require.NoError(t, err)
	med, err := CreateTask(db, "Med Priority", "", "", 5)
	require.NoError(t, err)

	tasks, err := FetchPipelineTasks(db, "", "agent1", "", 5)
	require.NoError(t, err)
	require.Len(t, tasks, 3)
	require.Equal(t, high.ID, tasks[0].ID)
	require.Equal(t, med.ID, tasks[1].ID)
	require.Equal(t, low.ID, tasks[2].ID)
}

func TestFetchPipelineTasks_ExcludesClaimedAndBlocked(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	// Available task
	avail, err := CreateTask(db, "Available", "", "", 0)
	require.NoError(t, err)

	// Claimed by another agent
	claimed, err := CreateTask(db, "Claimed", "", "", 0)
	require.NoError(t, err)
	require.NoError(t, ClaimTask(db, "other-agent", claimed.ID, 60))

	// Blocked by dependency
	blocker, err := CreateTask(db, "Blocker", "", "", 0)
	require.NoError(t, err)
	blocked, err := CreateTask(db, "Blocked", "", "", 0)
	require.NoError(t, err)
	require.NoError(t, AddTaskDependency(db, blocked.ID, blocker.ID))

	tasks, err := FetchPipelineTasks(db, "", "agent1", "", 10)
	require.NoError(t, err)

	ids := make(map[string]bool)
	for _, pt := range tasks {
		ids[pt.ID] = true
	}
	require.True(t, ids[avail.ID], "Available task should be in pipeline")
	require.True(t, ids[blocker.ID], "Blocker task should be in pipeline (it's pending, no deps)")
	require.False(t, ids[claimed.ID], "Claimed task should be excluded")
	require.False(t, ids[blocked.ID], "Blocked task should be excluded")
}

func TestFetchUnlockedByCompletion_SingleDep(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	focus, err := CreateTask(db, "Focus Task", "", "", 0)
	require.NoError(t, err)

	waiting, err := CreateTask(db, "Waiting Task", "", "", 0)
	require.NoError(t, err)
	require.NoError(t, AddTaskDependency(db, waiting.ID, focus.ID))

	unlocked, err := FetchUnlockedByCompletion(db, focus.ID)
	require.NoError(t, err)
	require.Len(t, unlocked, 1)
	require.Equal(t, waiting.ID, unlocked[0].ID)
	require.Equal(t, "Waiting Task", unlocked[0].Title)
}

func TestFetchUnlockedByCompletion_MultipleDeps(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	focus, err := CreateTask(db, "Focus Task", "", "", 0)
	require.NoError(t, err)
	other, err := CreateTask(db, "Other Dep", "", "", 0)
	require.NoError(t, err)

	waiting, err := CreateTask(db, "Waiting Task", "", "", 0)
	require.NoError(t, err)
	require.NoError(t, AddTaskDependency(db, waiting.ID, focus.ID))
	require.NoError(t, AddTaskDependency(db, waiting.ID, other.ID))

	// focus is NOT the only dep — other is also unresolved
	unlocked, err := FetchUnlockedByCompletion(db, focus.ID)
	require.NoError(t, err)
	require.Empty(t, unlocked, "Should not unlock task with other unresolved deps")
}

func TestBuildBrief_PopulatesDiscoveryContext(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	// Focus task
	focus, err := CreateTask(db, "Focus Task", "Do the thing", "", 5)
	require.NoError(t, err)

	// Pipeline tasks
	_, err = CreateTask(db, "Next Task", "", "", 3)
	require.NoError(t, err)
	_, err = CreateTask(db, "Later Task", "", "", 1)
	require.NoError(t, err)

	// Task that depends on focus (should appear in Unlocks)
	waiting, err := CreateTask(db, "Blocked On Focus", "", "", 0)
	require.NoError(t, err)
	require.NoError(t, AddTaskDependency(db, waiting.ID, focus.ID))

	brief, err := BuildBrief(db, focus.ID, "", "agent1")
	require.NoError(t, err)

	// Counts
	require.NotNil(t, brief.Counts)
	total := brief.Counts.Pending + brief.Counts.InProgress + brief.Counts.Completed + brief.Counts.Blocked
	require.Equal(t, 4, total, "Should count all 4 tasks")

	// Pipeline: should not include focus or blocked-on-focus
	require.NotEmpty(t, brief.Pipeline)
	for _, pt := range brief.Pipeline {
		require.NotEqual(t, focus.ID, pt.ID, "Pipeline should exclude focus task")
		require.NotEqual(t, waiting.ID, pt.ID, "Pipeline should exclude dep-blocked task")
	}

	// Unlocks
	require.Len(t, brief.Unlocks, 1)
	require.Equal(t, waiting.ID, brief.Unlocks[0].ID)
}
