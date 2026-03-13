package store

import (
	"strings"
	"testing"
	"time"

	"github.com/dotcommander/vybe/internal/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFetchEventsSince(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	// Create some events
	appendEvent(t, db, "test.event", "agent1", "task1", "Event 1")
	appendEvent(t, db, "test.event", "agent2", "task2", "Event 2")
	eventID3 := appendEvent(t, db, "test.event", "agent3", "task3", "Event 3")

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
	for range 5 {
		appendEvent(t, db, "test.event", "agent", "task", "Event")
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
	result, err := DetermineFocusTask(db, "agent1", task.ID, []*models.Event{}, "")
	if err != nil {
		t.Fatalf("DetermineFocusTask failed: %v", err)
	}

	if result.TaskID != task.ID {
		t.Errorf("Expected focus to remain on in_progress task %s, got %s", task.ID, result.TaskID)
	}
	if !strings.Contains(result.Rule, "rule1") {
		t.Errorf("Expected rule1 in Rule, got %q", result.Rule)
	}
}

func TestDetermineFocusTask_FailureBlockedSkips(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	// Create two tasks: one failure-blocked, one pending
	blockedTask, err := CreateTask(db, "Blocked Task", "Description", "", 0)
	require.NoError(t, err)

	pendingTask, err := CreateTask(db, "Pending Task", "Description", "", 0)
	require.NoError(t, err)

	// Set to blocked with a failure reason — rule1.5 must skip failure-blocked tasks
	err = UpdateTaskStatus(db, blockedTask.ID, "blocked", blockedTask.Version)
	require.NoError(t, err)
	_, err = db.Exec(`UPDATE tasks SET blocked_reason = 'failure:build_error' WHERE id = ?`, blockedTask.ID)
	require.NoError(t, err)

	// Determine focus: should skip failure-blocked and pick pending task
	result, err := DetermineFocusTask(db, "agent1", blockedTask.ID, []*models.Event{}, "")
	require.NoError(t, err)
	require.Equal(t, pendingTask.ID, result.TaskID, "Should skip failure-blocked focus and pick pending task")
}

func TestDetermineFocusTask_TaskAssigned_SkipsInProgressByOther(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	// Create a pending task and set it in_progress by another agent
	task, err := CreateTask(db, "InProgress Task", "Description", "", 0)
	require.NoError(t, err)
	require.NoError(t, UpdateTaskStatus(db, task.ID, "in_progress", 1))

	// Create a fallback pending task
	fallback, err := CreateTask(db, "Fallback Task", "Description", "", 0)
	require.NoError(t, err)

	// task_assigned event points to the in_progress task
	deltas := []*models.Event{{Kind: "task_assigned", TaskID: task.ID}}

	// agent1 should skip the in_progress task (not pending) and fall through to Rule 4
	result, err := DetermineFocusTask(db, "agent1", "", deltas, "")
	require.NoError(t, err)
	require.Equal(t, fallback.ID, result.TaskID, "Should skip non-pending task")
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
	result, err := DetermineFocusTask(db, "agent1", task1.ID, deltas, "")
	if err != nil {
		t.Fatalf("DetermineFocusTask failed: %v", err)
	}

	if result.TaskID != task2.ID {
		t.Errorf("Expected focus on assigned task %s, got %s", task2.ID, result.TaskID)
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
	result, err := DetermineFocusTask(db, "agent1", "", []*models.Event{}, "")
	if err != nil {
		t.Fatalf("DetermineFocusTask failed: %v", err)
	}

	if result.TaskID != task1.ID {
		t.Errorf("Expected focus on oldest task %s, got %s", task1.ID, result.TaskID)
	}
	if !strings.Contains(result.Rule, "rule4") {
		t.Errorf("Expected rule4 in Rule, got %q", result.Rule)
	}
}

func TestDetermineFocusTask_NoWorkAvailable(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	// Determine focus with no tasks
	result, err := DetermineFocusTask(db, "agent1", "", []*models.Event{}, "")
	if err != nil {
		t.Fatalf("DetermineFocusTask failed: %v", err)
	}

	if result.TaskID != "" {
		t.Errorf("Expected empty focus (no work), got %s", result.TaskID)
	}
	if !strings.Contains(result.Rule, "rule5") {
		t.Errorf("Expected rule5 in Rule, got %q", result.Rule)
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
	appendEvent(t, db, "task.started", "agent1", task.ID, "Started work")

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

	appendEvent(t, db, "task.note", "agent1", task.ID, "abcd")
	appendEvent(t, db, "task.note", "agent1", task.ID, "12345")

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
	result1, err := DetermineFocusTask(db, "agent1", "", []*models.Event{}, "")
	if err != nil {
		t.Fatalf("DetermineFocusTask failed: %v", err)
	}

	result2, err := DetermineFocusTask(db, "agent1", "", []*models.Event{}, "")
	if err != nil {
		t.Fatalf("DetermineFocusTask failed: %v", err)
	}

	// Should always return the same result (deterministic)
	if result1.TaskID != result2.TaskID {
		t.Errorf("DetermineFocusTask not deterministic: got %s then %s", result1.TaskID, result2.TaskID)
	}

	// Should be the oldest task
	if result1.TaskID != task1.ID {
		t.Errorf("Expected oldest task %s, got %s", task1.ID, result1.TaskID)
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

func TestFetchRelevantMemory_FiltersExpired(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	task, err := CreateTask(db, "Task", "Desc", "", 0)
	if err != nil {
		t.Fatalf("Failed to create task: %v", err)
	}

	// Active memory, should remain.
	require.NoError(t, SetMemory(db, "active", "value", "string", "task", task.ID, nil))

	// Expired memory, should be filtered out.
	expired := time.Now().UTC().Add(-1 * time.Hour)
	require.NoError(t, SetMemory(db, "expired", "value", "string", "task", task.ID, &expired))

	memories, err := fetchRelevantMemory(db, task.ID, "")
	if err != nil {
		t.Fatalf("fetchRelevantMemory failed: %v", err)
	}

	keys := map[string]bool{}
	for _, m := range memories {
		keys[m.Key] = true
	}

	if !keys["active"] {
		t.Fatalf("expected active memory to be present")
	}
	if keys["expired"] {
		t.Fatalf("did not expect expired memory")
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
	result, err := DetermineFocusTask(db, "agent1", "", []*models.Event{}, "proj_x")
	if err != nil {
		t.Fatalf("DetermineFocusTask failed: %v", err)
	}
	if result.TaskID != projTask.ID {
		t.Errorf("Expected focus on project task %s, got %s", projTask.ID, result.TaskID)
	}

	// If no pending task exists in the active project, do not fall back to global.
	err = UpdateTaskStatus(db, projTask.ID, "completed", projTask.Version)
	if err != nil {
		t.Fatalf("Failed to complete project task: %v", err)
	}

	result, err = DetermineFocusTask(db, "agent1", "", []*models.Event{}, "proj_x")
	if err != nil {
		t.Fatalf("DetermineFocusTask failed on strict project scope: %v", err)
	}
	if result.TaskID != "" {
		t.Errorf("Expected no focus task outside project scope, got %s", result.TaskID)
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

	appendEvent(t, db, "task.note", "agent1", projectTask.ID, "project A event")
	appendEvent(t, db, "task.note", "agent1", globalTask.ID, "global event")
	appendEvent(t, db, "task.note", "agent1", otherTask.ID, "project B event")

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

//nolint:gocyclo // test verifies priority ordering across many task states; each scenario is a distinct branch of the priority algorithm
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
	result, err := DetermineFocusTask(db, "agent1", "", []*models.Event{}, "")
	if err != nil {
		t.Fatalf("DetermineFocusTask failed: %v", err)
	}

	if result.TaskID != highTask.ID {
		t.Errorf("Expected focus on high priority task %s, got %s", highTask.ID, result.TaskID)
	}

	// Mark high priority as completed
	err = UpdateTaskStatus(db, highTask.ID, "completed", highTask.Version)
	if err != nil {
		t.Fatalf("Failed to complete high priority task: %v", err)
	}

	// Should now select normal priority (5)
	result, err = DetermineFocusTask(db, "agent1", "", []*models.Event{}, "")
	if err != nil {
		t.Fatalf("DetermineFocusTask failed: %v", err)
	}

	// Create another task with same priority as lowTask (both priority 0)
	lowTask2, err := CreateTask(db, "Another Low Priority", "Desc", "", 0)
	if err != nil {
		t.Fatalf("Failed to create another low priority task: %v", err)
	}

	// Mark normal priority as completed
	normalTask, err := GetTask(db, result.TaskID)
	if err != nil {
		t.Fatalf("Failed to get normal task: %v", err)
	}
	err = UpdateTaskStatus(db, normalTask.ID, "completed", normalTask.Version)
	if err != nil {
		t.Fatalf("Failed to complete normal priority task: %v", err)
	}

	// Now with two tasks at priority 0, should select oldest (lowTask created first)
	result, err = DetermineFocusTask(db, "agent1", "", []*models.Event{}, "")
	if err != nil {
		t.Fatalf("DetermineFocusTask failed: %v", err)
	}

	if result.TaskID != lowTask.ID {
		t.Errorf("Expected focus on oldest low priority task %s, got %s", lowTask.ID, result.TaskID)
	}

	// Mark oldest low priority as completed
	err = UpdateTaskStatus(db, lowTask.ID, "completed", lowTask.Version)
	if err != nil {
		t.Fatalf("Failed to complete oldest low priority task: %v", err)
	}

	// Should now select the newer low priority task
	result, err = DetermineFocusTask(db, "agent1", "", []*models.Event{}, "")
	if err != nil {
		t.Fatalf("DetermineFocusTask failed: %v", err)
	}

	if result.TaskID != lowTask2.ID {
		t.Errorf("Expected focus on newer low priority task %s, got %s", lowTask2.ID, result.TaskID)
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

	appendEvent(t, db, "reasoning", "agent1", "", "intent: build feature X")
	appendEvent(t, db, "user_prompt", "agent1", "", "not reasoning")
	appendEvent(t, db, "reasoning", "agent1", "", "intent: fix bug Y")

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
	appendEvent(t, db, "reasoning", "agent1", "", "global reasoning")

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

	id := appendEvent(t, db, "reasoning", "agent1", "", "archived reasoning")
	_, err := db.Exec(`UPDATE events SET archived_at = CURRENT_TIMESTAMP WHERE id = ?`, id)
	require.NoError(t, err)
	appendEvent(t, db, "reasoning", "agent1", "", "active reasoning")

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

	appendEvent(t, db, "user_prompt", "agent1", "", "a prompt")
	appendEvent(t, db, "reasoning", "agent1", "", "some reasoning")
	appendEvent(t, db, "tool_failure", "agent1", "", "bash failed")
	appendEvent(t, db, "task_status", "agent1", "", "completed")
	appendEvent(t, db, "progress", "agent1", "", "step done")
	// This kind should be excluded
	appendEvent(t, db, "task.note", "agent1", "", "random note")

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
	appendEvent(t, db, "progress", "agent1", "", "global progress")

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

	// blocked task
	require.NoError(t, UpdateTaskStatus(db, t1.ID, "blocked", t1.Version))

	counts, err := GetTaskStatusCounts(db, "")
	require.NoError(t, err)
	require.Equal(t, 1, counts.Pending)    // Pending 2
	require.Equal(t, 1, counts.InProgress) // In Progress
	require.Equal(t, 1, counts.Completed)  // Completed
	require.Equal(t, 1, counts.Blocked)    // Pending 1 (blocked)
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

func TestFetchPipelineTasks_ExcludesInProgressAndBlocked(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	// Available task
	avail, err := CreateTask(db, "Available", "", "", 0)
	require.NoError(t, err)

	// In_progress task — not in pipeline (not pending)
	inprog, err := CreateTask(db, "InProgress", "", "", 0)
	require.NoError(t, err)
	require.NoError(t, UpdateTaskStatus(db, inprog.ID, "in_progress", 1))

	// Manually blocked task
	blocked, err := CreateTask(db, "Blocked", "", "", 0)
	require.NoError(t, err)
	require.NoError(t, UpdateTaskStatus(db, blocked.ID, "blocked", blocked.Version))

	tasks, err := FetchPipelineTasks(db, "", "agent1", "", 10)
	require.NoError(t, err)

	ids := make(map[string]bool)
	for _, pt := range tasks {
		ids[pt.ID] = true
	}
	require.True(t, ids[avail.ID], "Available task should be in pipeline")
	require.False(t, ids[inprog.ID], "In_progress task should be excluded")
	require.False(t, ids[blocked.ID], "Blocked task should be excluded")
}

func TestFetchRelevantMemory_ACTRScoring(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	require.NoError(t, SetMemory(db, "rarely_used", "val1", "string", "global", "", nil))
	require.NoError(t, SetMemory(db, "frequently_used", "val2", "string", "global", "", nil))

	// Simulate high access for frequently_used
	_, err := db.Exec(`UPDATE memory SET access_count = 10, last_accessed_at = CURRENT_TIMESTAMP WHERE key = 'frequently_used'`)
	require.NoError(t, err)

	memories, err := fetchRelevantMemory(db, "", "")
	require.NoError(t, err)
	require.GreaterOrEqual(t, len(memories), 2)

	var freqIdx, rareIdx int
	for i, m := range memories {
		switch m.Key {
		case "frequently_used":
			freqIdx = i
		case "rarely_used":
			rareIdx = i
		}
	}
	assert.Less(t, freqIdx, rareIdx, "frequently_used should rank higher than rarely_used")

	// Verify relevance is populated
	for _, m := range memories {
		if m.Key == "frequently_used" {
			assert.Greater(t, m.Relevance, 0.0, "relevance should be positive")
		}
	}
}

func TestFetchRelevantMemory_AccessCountIncrement(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	require.NoError(t, SetMemory(db, "counter_test", "val", "string", "global", "", nil))

	// Fetch twice
	_, err := fetchRelevantMemory(db, "", "")
	require.NoError(t, err)
	_, err = fetchRelevantMemory(db, "", "")
	require.NoError(t, err)

	mem, err := GetMemory(db, "counter_test", "global", "")
	require.NoError(t, err)
	assert.Equal(t, 2, mem.AccessCount, "access_count should be 2 after two fetches")
	assert.NotNil(t, mem.LastAccessedAt, "last_accessed_at should be set")
}
