package actions

import (
	"database/sql"
	"testing"

	"github.com/dotcommander/vybe/internal/store"
)

// setupTestDBWithCleanup creates a test DB with automatic cleanup.
// The returned cleanup function is a no-op — cleanup is registered via
// t.Cleanup internally. Callers may ignore or call the second return value.
func setupTestDBWithCleanup(t *testing.T) (*sql.DB, func()) {
	t.Helper()

	tempDir := t.TempDir()
	testDBPath := tempDir + "/test.db"

	db, err := store.InitDBWithPath(testDBPath)
	if err != nil {
		t.Fatalf("Failed to initialize test database: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	return db, func() {}
}
