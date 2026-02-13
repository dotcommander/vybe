package actions

import (
	"testing"

	"github.com/dotcommander/vybe/internal/models"
	"github.com/dotcommander/vybe/internal/store"
)

// TestResumeInterruptionScenario simulates an agent being interrupted and resuming later
func TestResumeInterruptionScenario(t *testing.T) {
	db, cleanup := setupTestDBWithCleanup(t)
	defer cleanup()

	agentName := "worker-agent"

	// === Phase 1: Initial work session ===

	// Create some tasks (using actions layer to log events)
	task1, _, err := TaskCreate(db, agentName, "Task 1", "First task", "", 0)
	if err != nil {
		t.Fatalf("Failed to create task 1: %v", err)
	}

	task2, _, err := TaskCreate(db, agentName, "Task 2", "Second task", "", 0)
	if err != nil {
		t.Fatalf("Failed to create task 2: %v", err)
	}

	// Agent resumes for the first time
	response1, err := Resume(db, agentName)
	if err != nil {
		t.Fatalf("First resume failed: %v", err)
	}

	// Should get focus on task1 (oldest pending)
	if response1.FocusTaskID != task1.ID {
		t.Errorf("Expected focus on task1 %s, got %s", task1.ID, response1.FocusTaskID)
	}

	// Should have 2 deltas (task creation events)
	if len(response1.Deltas) != 2 {
		t.Errorf("Expected 2 deltas, got %d", len(response1.Deltas))
		for i, delta := range response1.Deltas {
			t.Logf("Delta %d: %s - %s", i, delta.Kind, delta.Message)
		}
	}

	// Agent starts working on task1 (using actions layer to log events)
	_, _, err = TaskSetStatus(db, agentName, task1.ID, "in_progress")
	if err != nil {
		t.Fatalf("Failed to update task status: %v", err)
	}

	// Log some work
	_, err = store.AppendEvent(db, "progress", agentName, task1.ID, "Made progress on task 1")
	if err != nil {
		t.Fatalf("Failed to append event: %v", err)
	}

	// === Interruption occurs ===
	// Agent process crashes or is terminated

	// === Phase 2: Resume after interruption ===

	// Agent resumes after restart
	response2, err := Resume(db, agentName)
	if err != nil {
		t.Fatalf("Second resume failed: %v", err)
	}

	// Should maintain focus on task1 (still in_progress)
	if response2.FocusTaskID != task1.ID {
		t.Errorf("Expected focus to persist on task1 %s, got %s", task1.ID, response2.FocusTaskID)
	}

	// Should see the status change and progress event in deltas
	if len(response2.Deltas) != 2 {
		t.Errorf("Expected 2 deltas (status change + progress), got %d", len(response2.Deltas))
		for i, delta := range response2.Deltas {
			t.Logf("Delta %d: %s - %s", i, delta.Kind, delta.Message)
		}
	}

	// Brief should include the in_progress task
	if response2.Brief.Task == nil {
		t.Fatalf("Expected brief with task")
	}

	if response2.Brief.Task.Status != models.TaskStatusInProgress {
		t.Errorf("Expected task status in_progress, got %s", response2.Brief.Task.Status)
	}

	// === Phase 3: Complete task and pick up next ===

	// Agent completes task1 (using actions layer)
	_, _, err = TaskSetStatus(db, agentName, task1.ID, "completed")
	if err != nil {
		t.Fatalf("Failed to complete task: %v", err)
	}

	// Resume again
	response3, err := Resume(db, agentName)
	if err != nil {
		t.Fatalf("Third resume failed: %v", err)
	}

	// Should switch focus to task2 (next pending task)
	if response3.FocusTaskID != task2.ID {
		t.Errorf("Expected focus to switch to task2 %s, got %s", task2.ID, response3.FocusTaskID)
	}

	// Verify brief has task2
	if response3.Brief.Task == nil || response3.Brief.Task.ID != task2.ID {
		t.Errorf("Expected brief for task2, got %v", response3.Brief.Task)
	}

	// === Phase 4: No more work ===

	// Complete task2 (using actions layer)
	_, _, err = TaskSetStatus(db, agentName, task2.ID, "completed")
	if err != nil {
		t.Fatalf("Failed to complete task2: %v", err)
	}

	// Resume with no pending work
	response4, err := Resume(db, agentName)
	if err != nil {
		t.Fatalf("Fourth resume failed: %v", err)
	}

	// Should have no focus task
	if response4.FocusTaskID != "" {
		t.Errorf("Expected no focus task, got %s", response4.FocusTaskID)
	}

	// Brief should be empty
	if response4.Brief.Task != nil {
		t.Errorf("Expected empty brief, got task %v", response4.Brief.Task)
	}
}

// TestResumeWithMemoryContext tests that brief includes relevant memory
func TestResumeWithMemoryContext(t *testing.T) {
	db, cleanup := setupTestDBWithCleanup(t)
	defer cleanup()

	agentName := "memory-agent"

	// Create a task (using actions layer)
	task, _, err := TaskCreate(db, agentName, "Task with context", "Description", "", 0)
	if err != nil {
		t.Fatalf("Failed to create task: %v", err)
	}

	// Add global memory
	err = store.SetMemory(db, "api_url", "https://api.example.com", "string", "global", "", nil)
	if err != nil {
		t.Fatalf("Failed to set global memory: %v", err)
	}

	// Add task-specific memory
	err = store.SetMemory(db, "checkpoint", "step_3", "string", "task", task.ID, nil)
	if err != nil {
		t.Fatalf("Failed to set task memory: %v", err)
	}

	// Add agent-specific memory (should NOT be included in brief)
	err = store.SetMemory(db, "local_cache", "/tmp/cache", "string", "agent", agentName, nil)
	if err != nil {
		t.Fatalf("Failed to set agent memory: %v", err)
	}

	// Resume
	response, err := Resume(db, agentName)
	if err != nil {
		t.Fatalf("Resume failed: %v", err)
	}

	// Verify focus on the task
	if response.FocusTaskID != task.ID {
		t.Errorf("Expected focus on task %s, got %s", task.ID, response.FocusTaskID)
	}

	// Verify brief includes global and task memory, but NOT agent memory
	if len(response.Brief.RelevantMemory) != 2 {
		t.Errorf("Expected 2 memory entries (global + task), got %d", len(response.Brief.RelevantMemory))
	}

	// Check that agent memory is excluded
	for _, mem := range response.Brief.RelevantMemory {
		if mem.Scope == models.MemoryScopeAgent {
			t.Errorf("Agent-scoped memory should not be in brief")
		}
	}

	// Verify we have both global and task memory
	hasGlobal := false
	hasTask := false
	for _, mem := range response.Brief.RelevantMemory {
		if mem.Scope == models.MemoryScopeGlobal && mem.Key == "api_url" {
			hasGlobal = true
		}
		if mem.Scope == models.MemoryScopeTask && mem.Key == "checkpoint" {
			hasTask = true
		}
	}

	if !hasGlobal {
		t.Error("Expected global memory in brief")
	}
	if !hasTask {
		t.Error("Expected task memory in brief")
	}
}

// TestMonotonicCursorAdvancement ensures cursor never goes backward
func TestMonotonicCursorAdvancement(t *testing.T) {
	db, cleanup := setupTestDBWithCleanup(t)
	defer cleanup()

	agentName := "monotonic-agent"

	// Create events
	for i := 0; i < 5; i++ {
		_, err := store.AppendEvent(db, "test.event", agentName, "", "Event")
		if err != nil {
			t.Fatalf("Failed to append event: %v", err)
		}
	}

	// First resume (cursor 0 -> 5)
	response1, err := Resume(db, agentName)
	if err != nil {
		t.Fatalf("First resume failed: %v", err)
	}

	if response1.OldCursor != 0 {
		t.Errorf("Expected old cursor 0, got %d", response1.OldCursor)
	}

	if response1.NewCursor != 5 {
		t.Errorf("Expected new cursor 5, got %d", response1.NewCursor)
	}

	// Second resume immediately (no new events, cursor stays at 5)
	response2, err := Resume(db, agentName)
	if err != nil {
		t.Fatalf("Second resume failed: %v", err)
	}

	if response2.OldCursor != 5 {
		t.Errorf("Expected old cursor 5, got %d", response2.OldCursor)
	}

	if response2.NewCursor != 5 {
		t.Errorf("Expected new cursor to remain 5, got %d", response2.NewCursor)
	}

	if len(response2.Deltas) != 0 {
		t.Errorf("Expected 0 deltas, got %d", len(response2.Deltas))
	}

	// Add more events
	for i := 0; i < 3; i++ {
		_, appendErr := store.AppendEvent(db, "test.event", agentName, "", "New Event")
		if appendErr != nil {
			t.Fatalf("Failed to append event: %v", appendErr)
		}
	}

	// Third resume (cursor 5 -> 8)
	response3, err := Resume(db, agentName)
	if err != nil {
		t.Fatalf("Third resume failed: %v", err)
	}

	if response3.OldCursor != 5 {
		t.Errorf("Expected old cursor 5, got %d", response3.OldCursor)
	}

	if response3.NewCursor != 8 {
		t.Errorf("Expected new cursor 8, got %d", response3.NewCursor)
	}

	if len(response3.Deltas) != 3 {
		t.Errorf("Expected 3 deltas, got %d", len(response3.Deltas))
	}

	// Verify cursor is monotonically increasing
	if response3.NewCursor < response3.OldCursor {
		t.Error("Cursor went backward!")
	}
}
