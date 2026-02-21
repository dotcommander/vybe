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
			_ = os.Remove(configPath)
			_ = os.Remove(configPath + "-shm")
			_ = os.Remove(configPath + "-wal")
		}
	})

	// Initialize database
	db, err := InitDBWithPath(configPath)
	if err != nil {
		// Some environments (including sandboxed runners) may not allow
		// opening/creating files under the resolved config path.
		// Also skip if the database is locked (another process holds it).
		errMsg := err.Error()
		if strings.Contains(errMsg, "unable to open database file") ||
			strings.Contains(errMsg, "SQLITE_BUSY") ||
			strings.Contains(errMsg, "database is locked") {
			t.Skipf("InitDBWithPath not available in this environment (db_path=%s): %v", configPath, err)
		}
		t.Fatalf("InitDBWithPath failed (db_path=%s): %v", configPath, err)
	}
	defer func() { _ = db.Close() }()

	// Verify we can append an event
	eventID := appendEvent(t, db, "test.integration", "test-agent", "task-1", "Integration test message")
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
