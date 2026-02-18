package store

import (
	"testing"
)

func TestLoadOrCreateAgentState(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	// Load non-existent agent (should create)
	state, err := LoadOrCreateAgentState(db, "test-agent")
	if err != nil {
		t.Fatalf("LoadOrCreateAgentState failed: %v", err)
	}

	if state.AgentName != "test-agent" {
		t.Errorf("Expected agent_name=test-agent, got %s", state.AgentName)
	}
	if state.LastSeenEventID != 0 {
		t.Errorf("Expected initial cursor=0, got %d", state.LastSeenEventID)
	}
	if state.Version != 1 {
		t.Errorf("Expected initial version=1, got %d", state.Version)
	}

	// Load existing agent
	state2, err := LoadOrCreateAgentState(db, "test-agent")
	if err != nil {
		t.Fatalf("LoadOrCreateAgentState (second call) failed: %v", err)
	}

	if state2.AgentName != state.AgentName {
		t.Errorf("Agent name mismatch on second load")
	}
}

func TestAdvanceAgentCursor(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	// Create agent state
	_, err := LoadOrCreateAgentState(db, "test-agent")
	if err != nil {
		t.Fatalf("Failed to create agent state: %v", err)
	}

	// Advance cursor
	err = AdvanceAgentCursor(db, "test-agent", 10)
	if err != nil {
		t.Fatalf("AdvanceAgentCursor failed: %v", err)
	}

	// Verify cursor was advanced
	state, err := LoadOrCreateAgentState(db, "test-agent")
	if err != nil {
		t.Fatalf("Failed to load agent state: %v", err)
	}

	if state.LastSeenEventID != 10 {
		t.Errorf("Expected cursor=10, got %d", state.LastSeenEventID)
	}
	if state.Version != 2 {
		t.Errorf("Expected version=2 after update, got %d", state.Version)
	}
}

func TestAdvanceAgentCursorMonotonic(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	// Create agent and advance to 10
	_, err := LoadOrCreateAgentState(db, "test-agent")
	if err != nil {
		t.Fatalf("Failed to create agent state: %v", err)
	}

	err = AdvanceAgentCursor(db, "test-agent", 10)
	if err != nil {
		t.Fatalf("AdvanceAgentCursor failed: %v", err)
	}

	// Try to advance backward (should not decrease)
	err = AdvanceAgentCursor(db, "test-agent", 5)
	if err != nil {
		t.Fatalf("AdvanceAgentCursor (backward) failed: %v", err)
	}

	// Verify cursor stayed at 10
	state, err := LoadOrCreateAgentState(db, "test-agent")
	if err != nil {
		t.Fatalf("Failed to load agent state: %v", err)
	}

	if state.LastSeenEventID != 10 {
		t.Errorf("Expected cursor=10 (monotonic), got %d", state.LastSeenEventID)
	}
}


func TestLoadOrCreateAgentState_FocusProjectIDEmpty(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	state, err := LoadOrCreateAgentState(db, "test-agent")
	if err != nil {
		t.Fatalf("Failed to create agent state: %v", err)
	}

	if state.FocusProjectID != "" {
		t.Errorf("Expected empty focus_project_id, got %s", state.FocusProjectID)
	}
}
