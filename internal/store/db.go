package store

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/dotcommander/vybe/internal/app"
	_ "modernc.org/sqlite"
)

// defaultBusyTimeoutMS is the SQLite busy_timeout in milliseconds.
// Override with VYBE_BUSY_TIMEOUT_MS for environments with high contention.
const defaultBusyTimeoutMS = 5000

// InitDB initializes the database connection with SQLite + WAL mode
// and runs migrations automatically.
func InitDB() (*sql.DB, error) {
	dbPath, err := app.GetDBPath()
	if err != nil {
		return nil, err
	}
	return InitDBWithPath(dbPath)
}

// InitDBWithPath initializes a database at a specific path (useful for testing)
func InitDBWithPath(dbPath string) (*sql.DB, error) {
	if _, err := app.EnsureDBDir(dbPath); err != nil {
		return nil, err
	}

	// Open database connection
	//
	// modernc.org/sqlite is strict about DSNs. Use a file: URI with mode=rwc
	// so the database can be created/written consistently across platforms.
	db, err := sql.Open("sqlite", normalizeSQLiteDSN(dbPath))
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %w", err)
	}

	// Configure connection pool for CLI tool scale
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)

	busyTimeout := defaultBusyTimeoutMS
	if v := os.Getenv("VYBE_BUSY_TIMEOUT_MS"); v != "" {
		if parsed, err := strconv.Atoi(v); err == nil && parsed > 0 {
			busyTimeout = parsed
		}
	}

	// Set SQLite pragmas for WAL mode and concurrent access.
	//
	// Requires SQLite 3.7.0+ (WAL support). modernc.org/sqlite bundles 3.46+.
	//
	// Trade-offs:
	//   busy_timeout  — blocks writers up to N ms instead of failing immediately.
	//   synchronous=NORMAL — skips fsync on every commit (WAL still provides
	//                        crash safety for committed txns; risk is losing the
	//                        last WAL checkpoint on OS crash, not data corruption).
	//   journal_mode=WAL   — allows concurrent readers + one writer; required
	//                        for multi-agent access to the same DB file.
	pragmas := []string{
		// Set busy_timeout first so subsequent pragmas (including WAL) will wait on locks.
		fmt.Sprintf("PRAGMA busy_timeout=%d", busyTimeout),
		"PRAGMA foreign_keys=ON",
		"PRAGMA synchronous=NORMAL",
		"PRAGMA journal_mode=WAL",
	}

	for _, pragma := range pragmas {
		if err := RetryWithBackoff(func() error {
			_, err := db.ExecContext(context.Background(), pragma)
			return err
		}); err != nil {
			_ = db.Close()
			return nil, fmt.Errorf("failed to set pragma %q: %w", pragma, err)
		}
	}

	// Run migrations
	if err := RetryWithBackoff(func() error { return RunMigrations(db) }); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("failed to run migrations: %w", err)
	}

	return db, nil
}

func normalizeSQLiteDSN(dbPath string) string {
	// Support an explicit file: DSN as-is.
	if strings.HasPrefix(dbPath, "file:") {
		return dbPath
	}

	// Provide a predictable in-memory option when callers use the common token.
	if dbPath == ":memory:" {
		return "file::memory:?cache=shared"
	}

	// Default to a writeable file URI.
	// mode=rwc => read/write/create. Without this, some environments open read-only.
	return "file:" + dbPath + "?mode=rwc"
}
