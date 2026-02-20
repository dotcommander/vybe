package store

import (
	"os"
	"strings"
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
	defer func() { _ = db.Close() }()

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

func TestOpenDB(t *testing.T) {
	tempDir := t.TempDir()
	testDBPath := tempDir + "/test_open.db"

	db, err := OpenDB(testDBPath)
	if err != nil {
		t.Fatalf("OpenDB failed: %v", err)
	}
	defer func() { _ = db.Close() }()

	// Verify WAL mode
	var journalMode string
	err = db.QueryRow("PRAGMA journal_mode").Scan(&journalMode)
	if err != nil {
		t.Fatalf("Failed to query journal_mode: %v", err)
	}
	if journalMode != "wal" {
		t.Errorf("Expected journal_mode=wal, got %s", journalMode)
	}

	// Verify NO tables exist (migrations not run)
	var count int
	err = db.QueryRow("SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%'").Scan(&count)
	if err != nil {
		t.Fatalf("Failed to count tables: %v", err)
	}
	if count != 0 {
		t.Errorf("Expected 0 tables (no migrations), got %d", count)
	}
}

func TestSchemaVersion_Fresh(t *testing.T) {
	tempDir := t.TempDir()
	testDBPath := tempDir + "/test_version.db"

	db, err := OpenDB(testDBPath)
	if err != nil {
		t.Fatalf("OpenDB failed: %v", err)
	}
	defer func() { _ = db.Close() }()

	current, latest, err := SchemaVersion(db)
	if err != nil {
		t.Fatalf("SchemaVersion failed: %v", err)
	}
	if current != 0 {
		t.Errorf("Expected current=0, got %d", current)
	}
	if latest < 16 {
		t.Errorf("Expected latest>=16, got %d", latest)
	}
}

func TestSchemaVersion_AfterMigrate(t *testing.T) {
	tempDir := t.TempDir()
	testDBPath := tempDir + "/test_migrated.db"

	db, err := InitDBWithPath(testDBPath)
	if err != nil {
		t.Fatalf("InitDBWithPath failed: %v", err)
	}
	defer func() { _ = db.Close() }()

	current, latest, err := SchemaVersion(db)
	if err != nil {
		t.Fatalf("SchemaVersion failed: %v", err)
	}
	if current != latest {
		t.Errorf("Expected current=%d after migration, got %d", latest, current)
	}
}

func TestCheckSchemaVersion_FailsOnFreshDB(t *testing.T) {
	tempDir := t.TempDir()
	testDBPath := tempDir + "/test_check_fail.db"

	db, err := OpenDB(testDBPath)
	if err != nil {
		t.Fatalf("OpenDB failed: %v", err)
	}
	defer func() { _ = db.Close() }()

	err = CheckSchemaVersion(db)
	if err == nil {
		t.Fatal("Expected CheckSchemaVersion to fail on fresh DB")
	}
	if !strings.Contains(err.Error(), "vybe upgrade") {
		t.Errorf("Expected error to mention 'vybe upgrade', got: %s", err.Error())
	}
}

func TestCheckSchemaVersion_PassesAfterMigrate(t *testing.T) {
	tempDir := t.TempDir()
	testDBPath := tempDir + "/test_check_pass.db"

	db, err := InitDBWithPath(testDBPath)
	if err != nil {
		t.Fatalf("InitDBWithPath failed: %v", err)
	}
	defer func() { _ = db.Close() }()

	err = CheckSchemaVersion(db)
	if err != nil {
		t.Errorf("Expected CheckSchemaVersion to pass after migration, got: %v", err)
	}
}
