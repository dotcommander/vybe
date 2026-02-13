package store

import (
	"os"
	"strings"
	"testing"

	"github.com/dotcommander/vybe/internal/app"
)

// TestInitDBIntegration tests that the database can be initialized at the actual config path
func TestInitDBIntegration(t *testing.T) {
	configPath, err := app.GetDBPath()
	if err != nil {
		t.Skipf("Cannot determine database path: %v", err)
	}

	// Clean up any existing test database
	t.Cleanup(func() {
		// Note: In production, we don't want to delete the database
		// This cleanup is only for integration tests
		if os.Getenv("VYBE_TEST_CLEANUP") == "true" {
			os.Remove(configPath)
			os.Remove(configPath + "-shm")
			os.Remove(configPath + "-wal")
		}
	})

	// Initialize database
	db, err := InitDB()
	if err != nil {
		// Some environments (including sandboxed runners) may not allow
		// opening/creating files under the resolved config path.
		if strings.Contains(err.Error(), "unable to open database file") {
			t.Skipf("InitDB not supported in this environment (db_path=%s): %v", configPath, err)
		}
		t.Fatalf("InitDB failed (db_path=%s): %v", configPath, err)
	}
	defer db.Close()

	// Verify we can append an event
	eventID, err := AppendEvent(db, "test.integration", "test-agent", "task-1", "Integration test message")
	if err != nil {
		t.Fatalf("AppendEvent failed: %v", err)
	}

	if eventID <= 0 {
		t.Errorf("Expected positive event ID, got %d", eventID)
	}

	// Verify we can load agent state
	state, err := LoadOrCreateAgentState(db, "test-agent")
	if err != nil {
		t.Fatalf("LoadOrCreateAgentState failed: %v", err)
	}

	if state.AgentName != "test-agent" {
		t.Errorf("Expected agent_name=test-agent, got %s", state.AgentName)
	}

	// Verify we can advance cursor
	err = AdvanceAgentCursor(db, "test-agent", eventID)
	if err != nil {
		t.Fatalf("AdvanceAgentCursor failed: %v", err)
	}

	// Verify cursor was advanced
	state, err = LoadOrCreateAgentState(db, "test-agent")
	if err != nil {
		t.Fatalf("LoadOrCreateAgentState (second) failed: %v", err)
	}

	if state.LastSeenEventID != eventID {
		t.Errorf("Expected cursor=%d, got %d", eventID, state.LastSeenEventID)
	}

	t.Logf("Integration test passed. Database initialized at: %s", configPath)
}
