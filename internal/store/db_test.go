package store

import (
	"os"
	"testing"
)

func TestInitDB(t *testing.T) {
	// Create a temporary test database
	tempDir := t.TempDir()
	testDBPath := tempDir + "/test.db"

	// Initialize database
	db, err := InitDBWithPath(testDBPath)
	if err != nil {
		t.Fatalf("InitDBWithPath failed: %v", err)
	}
	defer db.Close()

	// Verify database file exists
	_, statErr := os.Stat(testDBPath)
	if os.IsNotExist(statErr) {
		t.Fatalf("Database file was not created at %s", testDBPath)
	}

	// Verify tables were created
	tables := []string{"events", "tasks", "agent_state", "memory", "artifacts", "projects"}
	for _, table := range tables {
		var name string
		scanErr := db.QueryRow("SELECT name FROM sqlite_master WHERE type='table' AND name=?", table).Scan(&name)
		if scanErr != nil {
			t.Errorf("Table %s was not created: %v", table, scanErr)
		}
	}

	// Verify WAL mode is enabled
	var journalMode string
	err = db.QueryRow("PRAGMA journal_mode").Scan(&journalMode)
	if err != nil {
		t.Fatalf("Failed to query journal_mode: %v", err)
	}
	if journalMode != "wal" {
		t.Errorf("Expected journal_mode=wal, got %s", journalMode)
	}

	// Verify foreign keys are enabled
	var foreignKeys int
	err = db.QueryRow("PRAGMA foreign_keys").Scan(&foreignKeys)
	if err != nil {
		t.Fatalf("Failed to query foreign_keys: %v", err)
	}
	if foreignKeys != 1 {
		t.Errorf("Expected foreign_keys=1, got %d", foreignKeys)
	}
}
