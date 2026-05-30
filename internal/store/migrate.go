package store

import (
	"database/sql"
	"embed"
	"fmt"
	"strconv"
	"strings"
	"sync"

	"github.com/pressly/goose/v3"
)

//go:embed migrations/*.sql
var embedMigrations embed.FS

// gooseInit guards the package-global goose configuration calls (SetBaseFS,
// SetVerbose, SetLogger, SetDialect). All four calls use identical arguments
// across every call site, so once-per-process is correct. Without this guard,
// parallel tests that each call MigrateDB or SchemaVersion race on goose globals.
var (
	gooseInit    sync.Once
	gooseInitErr error
)

func initGoose() error {
	gooseInit.Do(func() {
		goose.SetBaseFS(embedMigrations)
		goose.SetVerbose(false)
		goose.SetLogger(goose.NopLogger())
		gooseInitErr = goose.SetDialect("sqlite3")
	})
	return gooseInitErr
}

// MigrateDB runs all pending migrations with a file lock to prevent concurrent
// migration races. For in-memory databases (tests), the lock is skipped.
func MigrateDB(db *sql.DB, dbPath string) error {
	// Repair any memory column desyncs before running migrations.
	if err := repairMemoryProvenanceColumns(db, dbPath); err != nil {
		return err
	}

	// Fast path: skip lock + goose.Up when schema is already current.
	current, latest, err := SchemaVersion(db)
	if err == nil && current >= latest && latest > 0 {
		return nil
	}

	if dbPath != ":memory:" && !strings.Contains(dbPath, ":memory:") {
		lockF, err := LockFile(dbPath + ".migrate.lock")
		if err != nil {
			return fmt.Errorf("migration lock: %w", err)
		}
		defer UnlockFile(lockF)
	}
	return RunMigrations(db)
}

// SchemaVersion returns the current and latest migration versions.
// current comes from goose_db_version; latest is the highest version
// in the embedded migration files. Returns (0, latest, nil) for a fresh DB.
func SchemaVersion(db *sql.DB) (current int64, latest int64, err error) {
	if err := initGoose(); err != nil {
		return 0, 0, fmt.Errorf("set dialect: %w", err)
	}

	current, err = goose.GetDBVersion(db)
	if err != nil {
		if strings.Contains(err.Error(), "no such table") {
			current = 0
		} else {
			return 0, 0, fmt.Errorf("get db version: %w", err)
		}
	}

	latest, err = latestMigrationVersion()
	if err != nil {
		return current, 0, fmt.Errorf("determine latest version: %w", err)
	}
	return current, latest, nil
}

// latestMigrationVersion reads the embedded migrations directory and returns
// the highest version number found.
func latestMigrationVersion() (int64, error) {
	entries, err := embedMigrations.ReadDir("migrations")
	if err != nil {
		return 0, fmt.Errorf("read migrations dir: %w", err)
	}
	var max int64
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		// Parse version from filename prefix "00016_name.sql" -> 16
		idx := strings.IndexByte(name, '_')
		if idx <= 0 {
			continue
		}
		v, err := strconv.ParseInt(name[:idx], 10, 64)
		if err != nil {
			continue
		}
		if v > max {
			max = v
		}
	}
	return max, nil
}

// RunMigrations runs all pending migrations using goose.
func RunMigrations(db *sql.DB) error {
	if err := initGoose(); err != nil {
		return err
	}

	// goose uses "sqlite3" as its dialect name regardless of the underlying driver.
	// We use modernc.org/sqlite (registered as "sqlite"), but goose's dialect
	// controls SQL generation (e.g., CREATE TABLE syntax), not the driver name.
	if err := goose.Up(db, "migrations"); err != nil {
		return err
	}

	return nil
}

// repairMemoryProvenanceColumns handles the desync case where migration 00028
// was marked as applied (e.g. locally in dev), but the source_task_id column
// was not successfully added.
func repairMemoryProvenanceColumns(db *sql.DB, dbPath string) error {
	var exists bool
	err := db.QueryRow(`SELECT EXISTS(SELECT 1 FROM sqlite_master WHERE type='table' AND name='memory')`).Scan(&exists)
	if err != nil {
		return fmt.Errorf("failed to check if memory table exists: %w", err)
	}
	if !exists {
		return nil
	}

	rows, err := db.Query(`PRAGMA table_info(memory)`)
	if err != nil {
		return fmt.Errorf("failed to read memory table info: %w", err)
	}
	defer rows.Close()

	hasSourceEventID := false
	hasSourceTaskID := false
	for rows.Next() {
		var cid int
		var name, typeStr string
		var notNull int
		var dfltVal any
		var pk int
		if err := rows.Scan(&cid, &name, &typeStr, &notNull, &dfltVal, &pk); err != nil {
			return fmt.Errorf("failed to scan table info row: %w", err)
		}
		if name == "source_event_id" {
			hasSourceEventID = true
		}
		if name == "source_task_id" {
			hasSourceTaskID = true
		}
	}

	if hasSourceEventID && hasSourceTaskID {
		return nil
	}

	if dbPath != ":memory:" && !strings.Contains(dbPath, ":memory:") {
		lockF, err := LockFile(dbPath + ".migrate.lock")
		if err != nil {
			return fmt.Errorf("migration lock for repair: %w", err)
		}
		defer UnlockFile(lockF)
	}

	if !hasSourceEventID {
		if _, err := db.Exec(`ALTER TABLE memory ADD COLUMN source_event_id INTEGER`); err != nil {
			return fmt.Errorf("failed to add source_event_id column: %w", err)
		}
	}
	if !hasSourceTaskID {
		if _, err := db.Exec(`ALTER TABLE memory ADD COLUMN source_task_id TEXT`); err != nil {
			return fmt.Errorf("failed to add source_task_id column: %w", err)
		}
	}

	return nil
}
