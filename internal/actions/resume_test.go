package actions

import (
	"database/sql"
	"strings"
	"testing"

	"github.com/dotcommander/vybe/internal/models"
	"github.com/dotcommander/vybe/internal/store"
)

func TestResume_NewAgent(t *testing.T) {
	db, cleanup := setupTestDBWithCleanup(t)
	defer cleanup()

	response, err := ResumeWithOptionsIdempotent(db, "new-agent", "req-new-agent-1", ResumeOptions{EventLimit: 1000})
	if err != nil {
		t.Fatalf("Resume failed: %v", err)
	}

	if response.AgentName != "new-agent" {
		t.Errorf("Expected agent name 'new-agent', got %s", response.AgentName)
	}

	if response.OldCursor != 0 {
		t.Errorf("Expected old cursor 0, got %d", response.OldCursor)
	}

	if response.NewCursor != 0 {
		t.Errorf("Expected new cursor 0 (no events), got %d", response.NewCursor)
	}

	if len(response.Deltas) != 0 {
		t.Errorf("Expected 0 deltas, got %d", len(response.Deltas))
	}

	if response.FocusTaskID != "" {
		t.Errorf("Expected no focus task, got %s", response.FocusTaskID)
	}
}

func TestResume_WithEvents(t *testing.T) {
	db, cleanup := setupTestDBWithCleanup(t)
	defer cleanup()

	// Create agent state
	_, err := store.LoadOrCreateAgentState(db, "agent1")
	if err != nil {
		t.Fatalf("Failed to create agent state: %v", err)
	}

	// Create some events
	if err = store.Transact(db, func(tx *sql.Tx) error {
		_, e := store.InsertEventTx(tx, "test.event", "agent1", "", "Event 1", "")
		return e
	}); err != nil {
		t.Fatalf("Failed to append event: %v", err)
	}
	if err = store.Transact(db, func(tx *sql.Tx) error {
		_, e := store.InsertEventTx(tx, "test.event", "agent1", "", "Event 2", "")
		return e
	}); err != nil {
		t.Fatalf("Failed to append event: %v", err)
	}

	// Resume
	response, err := ResumeWithOptionsIdempotent(db, "agent1", "req-with-events-1", ResumeOptions{EventLimit: 1000})
	if err != nil {
		t.Fatalf("Resume failed: %v", err)
	}

	if response.OldCursor != 0 {
		t.Errorf("Expected old cursor 0, got %d", response.OldCursor)
	}

	if response.NewCursor != 2 {
		t.Errorf("Expected new cursor 2, got %d", response.NewCursor)
	}

	if len(response.Deltas) != 2 {
		t.Errorf("Expected 2 deltas, got %d", len(response.Deltas))
	}
}

func TestResume_WithPendingTask(t *testing.T) {
	db, cleanup := setupTestDBWithCleanup(t)
	defer cleanup()

	// Create a task
	task, err := store.CreateTask(db, "Test Task", "Description", "", 0)
	if err != nil {
		t.Fatalf("Failed to create task: %v", err)
	}

	// Resume
	response, err := ResumeWithOptionsIdempotent(db, "agent1", "req-pending-task-1", ResumeOptions{EventLimit: 1000})
	if err != nil {
		t.Fatalf("Resume failed: %v", err)
	}

	if response.FocusTaskID != task.ID {
		t.Errorf("Expected focus on task %s, got %s", task.ID, response.FocusTaskID)
	}

	if response.Brief.Task == nil {
		t.Fatalf("Expected brief with task")
	}

	if response.Brief.Task.ID != task.ID {
		t.Errorf("Expected brief task %s, got %s", task.ID, response.Brief.Task.ID)
	}

	if !strings.Contains(response.Prompt, "--agent=agent1") {
		t.Errorf("Expected prompt commands to use agent agent1, got prompt: %s", response.Prompt)
	}
}

func TestResume_CursorAdvancement(t *testing.T) {
	db, cleanup := setupTestDBWithCleanup(t)
	defer cleanup()

	// Create events
	if err := store.Transact(db, func(tx *sql.Tx) error {
		_, e := store.InsertEventTx(tx, "test.event", "agent1", "", "Event 1", "")
		return e
	}); err != nil {
		t.Fatalf("Failed to append event: %v", err)
	}

	// First resume
	response1, err := ResumeWithOptionsIdempotent(db, "agent1", "req-cursor-adv-1", ResumeOptions{EventLimit: 1000})
	if err != nil {
		t.Fatalf("Resume failed: %v", err)
	}

	if response1.OldCursor != 0 {
		t.Errorf("First resume: expected old cursor 0, got %d", response1.OldCursor)
	}

	if response1.NewCursor != 1 {
		t.Errorf("First resume: expected new cursor 1, got %d", response1.NewCursor)
	}

	// Create another event
	if err = store.Transact(db, func(tx *sql.Tx) error {
		_, e := store.InsertEventTx(tx, "test.event", "agent1", "", "Event 2", "")
		return e
	}); err != nil {
		t.Fatalf("Failed to append event: %v", err)
	}

	// Second resume — distinct request ID so it computes fresh state
	response2, err := ResumeWithOptionsIdempotent(db, "agent1", "req-cursor-adv-2", ResumeOptions{EventLimit: 1000})
	if err != nil {
		t.Fatalf("Resume failed: %v", err)
	}

	if response2.OldCursor != 1 {
		t.Errorf("Second resume: expected old cursor 1, got %d", response2.OldCursor)
	}

	if response2.NewCursor != 2 {
		t.Errorf("Second resume: expected new cursor 2, got %d", response2.NewCursor)
	}

	if len(response2.Deltas) != 1 {
		t.Errorf("Second resume: expected 1 delta, got %d", len(response2.Deltas))
	}
}

func TestResume_FocusTaskPersistence(t *testing.T) {
	db, cleanup := setupTestDBWithCleanup(t)
	defer cleanup()

	// Create a task
	task, err := store.CreateTask(db, "Test Task", "Description", "", 0)
	if err != nil {
		t.Fatalf("Failed to create task: %v", err)
	}

	// First resume (should select the task)
	response1, err := ResumeWithOptionsIdempotent(db, "agent1", "req-focus-persist-1", ResumeOptions{EventLimit: 1000})
	if err != nil {
		t.Fatalf("Resume failed: %v", err)
	}

	if response1.FocusTaskID != task.ID {
		t.Errorf("First resume: expected focus on task %s, got %s", task.ID, response1.FocusTaskID)
	}

	// Update task to in_progress
	err = store.UpdateTaskStatus(db, task.ID, "in_progress", task.Version)
	if err != nil {
		t.Fatalf("Failed to update task status: %v", err)
	}

	// Second resume (should keep focus on in_progress task) — distinct request ID
	response2, err := ResumeWithOptionsIdempotent(db, "agent1", "req-focus-persist-2", ResumeOptions{EventLimit: 1000})
	if err != nil {
		t.Fatalf("Resume failed: %v", err)
	}

	if response2.FocusTaskID != task.ID {
		t.Errorf("Second resume: expected focus to persist on task %s, got %s", task.ID, response2.FocusTaskID)
	}
}

func TestResume_RequiresAgentName(t *testing.T) {
	db, cleanup := setupTestDBWithCleanup(t)
	defer cleanup()

	_, err := ResumeWithOptionsIdempotent(db, "", "req-empty-agent", ResumeOptions{EventLimit: 1000})
	if err == nil {
		t.Error("Expected error for empty agent name")
	}
}

func TestBrief_ExistingFocus(t *testing.T) {
	db, cleanup := setupTestDBWithCleanup(t)
	defer cleanup()

	// Create task and agent with focus
	task, err := store.CreateTask(db, "Test Task", "Description", "", 0)
	if err != nil {
		t.Fatalf("Failed to create task: %v", err)
	}

	state, err := store.LoadOrCreateAgentState(db, "agent1")
	if err != nil {
		t.Fatalf("Failed to create agent state: %v", err)
	}

	err = store.UpdateAgentStateAtomic(db, state.AgentName, 0, task.ID)
	if err != nil {
		t.Fatalf("Failed to set focus: %v", err)
	}

	// Get brief
	brief, err := Brief(db, "agent1")
	if err != nil {
		t.Fatalf("Brief failed: %v", err)
	}

	if brief.Task == nil {
		t.Fatalf("Expected task in brief")
	}

	if brief.Task.ID != task.ID {
		t.Errorf("Expected brief for task %s, got %s", task.ID, brief.Task.ID)
	}
}

func TestBrief_NoFocus(t *testing.T) {
	db, cleanup := setupTestDBWithCleanup(t)
	defer cleanup()

	// Create agent with no focus
	_, err := store.LoadOrCreateAgentState(db, "agent1")
	if err != nil {
		t.Fatalf("Failed to create agent state: %v", err)
	}

	// Get brief
	brief, err := Brief(db, "agent1")
	if err != nil {
		t.Fatalf("Brief failed: %v", err)
	}

	if brief.Task != nil {
		t.Errorf("Expected no task in brief, got %v", brief.Task)
	}
}

func TestBrief_RequiresAgentName(t *testing.T) {
	db, cleanup := setupTestDBWithCleanup(t)
	defer cleanup()

	// Brief with empty agent name
	_, err := Brief(db, "")
	if err == nil {
		t.Error("Expected error for empty agent name")
	}
}

func TestBrief_DoesNotAdvanceCursor(t *testing.T) {
	db, cleanup := setupTestDBWithCleanup(t)
	defer cleanup()

	// Create agent and events
	_, err := store.LoadOrCreateAgentState(db, "agent1")
	if err != nil {
		t.Fatalf("Failed to create agent state: %v", err)
	}

	if err = store.Transact(db, func(tx *sql.Tx) error {
		_, e := store.InsertEventTx(tx, "test.event", "agent1", "", "Event 1", "")
		return e
	}); err != nil {
		t.Fatalf("Failed to append event: %v", err)
	}

	// Get brief (should not advance cursor)
	_, err = Brief(db, "agent1")
	if err != nil {
		t.Fatalf("Brief failed: %v", err)
	}

	// Check cursor is still at 0
	state, err := store.GetAgentState(db, "agent1")
	if err != nil {
		t.Fatalf("Failed to get agent state: %v", err)
	}

	if state.LastSeenEventID != 0 {
		t.Errorf("Brief should not advance cursor: expected 0, got %d", state.LastSeenEventID)
	}
}

func TestBuildPrompt_IncludesPriorReasoning(t *testing.T) {
	db, cleanup := setupTestDBWithCleanup(t)
	defer cleanup()

	task, err := store.CreateTask(db, "Test Task", "Desc", "", 0)
	if err != nil {
		t.Fatalf("Failed to create task: %v", err)
	}

	// Insert reasoning events
	if err = store.Transact(db, func(tx *sql.Tx) error {
		_, e := store.InsertEventTx(tx, "reasoning", "agent1", "", "intent summary", `{"intent":"build auth","approach":"jwt tokens"}`)
		return e
	}); err != nil {
		t.Fatalf("Failed to append reasoning event: %v", err)
	}

	brief := &store.BriefPacket{
		Task:           task,
		RelevantMemory: []*models.Memory{},
		RecentEvents:   []*models.Event{},
		Artifacts:      []*models.Artifact{},
	}

	// Fetch reasoning events to populate brief
	reasoning, err := store.FetchPriorReasoning(db, "", 10)
	if err != nil {
		t.Fatalf("Failed to fetch reasoning: %v", err)
	}
	brief.PriorReasoning = reasoning

	prompt := buildPrompt("agent1", brief, nil)

	if !strings.Contains(prompt, "Prior reasoning") {
		t.Errorf("Expected prompt to include 'Prior reasoning' section, got:\n%s", prompt)
	}
	if !strings.Contains(prompt, "build auth") {
		t.Errorf("Expected prompt to include intent 'build auth', got:\n%s", prompt)
	}
	if !strings.Contains(prompt, "jwt tokens") {
		t.Errorf("Expected prompt to include approach 'jwt tokens', got:\n%s", prompt)
	}
	if !strings.Contains(prompt, "THINK") {
		t.Errorf("Expected prompt to include THINK command, got:\n%s", prompt)
	}
}

func TestResumeWithProjectScope_FirstUseAgent(t *testing.T) {
	db, cleanup := setupTestDBWithCleanup(t)
	defer cleanup()

	project, err := store.CreateProject(db, "Scoped", "")
	if err != nil {
		t.Fatalf("Failed to create project: %v", err)
	}

	projectTask, err := store.CreateTask(db, "Project Task", "", project.ID, 0)
	if err != nil {
		t.Fatalf("Failed to create project task: %v", err)
	}

	_, err = store.CreateTask(db, "Global Task", "", "", 0)
	if err != nil {
		t.Fatalf("Failed to create global task: %v", err)
	}

	response, err := ResumeWithOptionsIdempotent(db, "new-project-agent", "req_proj_resume", ResumeOptions{
		EventLimit: 100,
		ProjectDir: project.ID,
	})
	if err != nil {
		t.Fatalf("ResumeWithOptionsIdempotent failed: %v", err)
	}

	if response.FocusProjectID != project.ID {
		t.Fatalf("Expected focus project %s, got %s", project.ID, response.FocusProjectID)
	}

	if response.FocusTaskID != projectTask.ID {
		t.Fatalf("Expected focus task %s, got %s", projectTask.ID, response.FocusTaskID)
	}

	state, err := store.GetAgentState(db, "new-project-agent")
	if err != nil {
		t.Fatalf("Failed to get agent state: %v", err)
	}

	if state.FocusProjectID != project.ID {
		t.Fatalf("Expected persisted focus project %s, got %s", project.ID, state.FocusProjectID)
	}
}
