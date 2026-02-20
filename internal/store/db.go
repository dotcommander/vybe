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

// CloseDB runs PRAGMA optimize then closes the connection.
// Use this instead of db.Close() for proper SQLite lifecycle management.
// PRAGMA optimize updates query planner statistics accumulated during the session.
func CloseDB(db *sql.DB) error {
	_, _ = db.ExecContext(context.Background(), "PRAGMA optimize")
	return db.Close()
}

// validCheckpointModes is the allowlist of accepted WAL checkpoint modes.
var validCheckpointModes = map[string]bool{
	"PASSIVE":  true,
	"FULL":     true,
	"TRUNCATE": true,
	"RESTART":  true,
}

// CheckpointWAL triggers a WAL checkpoint.
// mode must be one of: PASSIVE, FULL, TRUNCATE, RESTART.
// Use "PASSIVE" for non-blocking, "FULL" to block until complete,
// "TRUNCATE" to reset WAL file size back to zero,
// "RESTART" to block and reset the WAL write position.
func CheckpointWAL(ctx context.Context, db *sql.DB, mode string) error {
	if !validCheckpointModes[mode] {
		return fmt.Errorf("invalid WAL checkpoint mode %q: must be one of PASSIVE, FULL, TRUNCATE, RESTART", mode)
	}
	_, err := db.ExecContext(ctx, "PRAGMA wal_checkpoint("+mode+")")
	return err
}

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

// OpenDB opens a database connection and configures SQLite pragmas, but does
// NOT run migrations. Use InitDBWithPath for test/upgrade scenarios that need
// automatic migration, or pair with CheckSchemaVersion for production commands.
func OpenDB(dbPath string) (*sql.DB, error) {
	absPath, err := app.EnsureDBDir(dbPath)
	if err != nil {
		return nil, err
	}

	// Open database connection
	//
	// modernc.org/sqlite is strict about DSNs. Use a file: URI with mode=rwc
	// so the database can be created/written consistently across platforms.
	db, err := sql.Open("sqlite", normalizeSQLiteDSN(absPath))
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
	//   temp_store=MEMORY  — keeps temp tables/indices in RAM instead of disk files.
	//   mmap_size          — 64MB virtual memory mapping for faster reads (no physical RAM cost).
	//   cache_size         — ~8MB page cache (default is ~2MB); reduces disk I/O for repeated access.
	//   wal_autocheckpoint — explicit default of 1000 pages; documents intent and prevents surprises.
	pragmas := []string{
		// Set busy_timeout first so subsequent pragmas (including WAL) will wait on locks.
		fmt.Sprintf("PRAGMA busy_timeout=%d", busyTimeout),
		"PRAGMA foreign_keys=ON",
		"PRAGMA synchronous=NORMAL",
		"PRAGMA journal_mode=WAL",
		"PRAGMA temp_store=MEMORY",
		"PRAGMA mmap_size=67108864",      // 64MB — virtual memory, not physical
		"PRAGMA cache_size=-8000",        // ~8MB page cache (negative = kibibytes)
		"PRAGMA wal_autocheckpoint=1000", // explicit default, documents intent
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

	return db, nil
}

// CheckSchemaVersion verifies the database schema is up to date.
// Returns an error with remediation instructions if migrations are pending.
func CheckSchemaVersion(db *sql.DB) error {
	current, latest, err := SchemaVersion(db)
	if err != nil {
		return fmt.Errorf("check schema version: %w", err)
	}
	if current < latest {
		return fmt.Errorf("schema version %d, expected %d: run 'vybe upgrade' to apply migrations", current, latest)
	}
	return nil
}

// InitDBWithPath opens a database and runs migrations. Used by tests and the
// upgrade command. Production commands should use OpenDB + CheckSchemaVersion.
func InitDBWithPath(dbPath string) (*sql.DB, error) {
	db, err := OpenDB(dbPath)
	if err != nil {
		return nil, err
	}
	if err := MigrateDB(db, dbPath); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("failed to run migrations: %w", err)
	}
	return db, nil
}

func normalizeSQLiteDSN(dbPath string) string {
	// Support an explicit file: DSN, appending _txlock=immediate if not already set.
	// _txlock=immediate makes all BeginTx calls use BEGIN IMMEDIATE automatically,
	// which prevents writer starvation and deadlocks under concurrent access.
	//
	// Exception: file::memory: DSNs must not get _txlock=immediate — IMMEDIATE
	// locking can deadlock when migrations run nested queries on the same
	// shared-cache connection.
	if strings.HasPrefix(dbPath, "file:") {
		if strings.Contains(dbPath, ":memory:") {
			return dbPath
		}
		if strings.Contains(dbPath, "_txlock=") {
			return dbPath
		}
		if strings.Contains(dbPath, "?") {
			return dbPath + "&_txlock=immediate"
		}
		return dbPath + "?_txlock=immediate"
	}

	// Provide a predictable in-memory option when callers use the common token.
	// No _txlock=immediate for in-memory DBs: IMMEDIATE locking can deadlock
	// when migrations run nested queries on the same shared-cache connection.
	if dbPath == ":memory:" {
		return "file::memory:?cache=shared"
	}

	// Default to a writeable file URI.
	// mode=rwc => read/write/create. Without this, some environments open read-only.
	return "file:" + dbPath + "?mode=rwc&_txlock=immediate"
}
