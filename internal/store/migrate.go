package store

import (
	"database/sql"
	"embed"
	"fmt"
	"strconv"
	"strings"

	"github.com/pressly/goose/v3"
)

//go:embed migrations/*.sql
var embedMigrations embed.FS

// MigrateDB runs all pending migrations with a file lock to prevent concurrent
// migration races. For in-memory databases (tests), the lock is skipped.
func MigrateDB(db *sql.DB, dbPath string) error {
	if dbPath != ":memory:" && !strings.Contains(dbPath, ":memory:") {
		lockF, err := lockFile(dbPath)
		if err != nil {
			return fmt.Errorf("migration lock: %w", err)
		}
		defer unlockFile(lockF)
	}
	return RunMigrations(db)
}

// SchemaVersion returns the current and latest migration versions.
// current comes from goose_db_version; latest is the highest version
// in the embedded migration files. Returns (0, latest, nil) for a fresh DB.
func SchemaVersion(db *sql.DB) (current int64, latest int64, err error) {
	goose.SetBaseFS(embedMigrations)
	goose.SetVerbose(false)
	goose.SetLogger(goose.NopLogger())
	if err := goose.SetDialect("sqlite3"); err != nil {
		return 0, 0, fmt.Errorf("set dialect: %w", err)
	}

	current, err = goose.GetDBVersion(db)
	if err != nil {
		// Fresh DB with no goose_db_version table: treat as version 0
		current = 0
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
	goose.SetBaseFS(embedMigrations)
	goose.SetVerbose(false) // Suppress migration logs for clean JSON output
	goose.SetLogger(goose.NopLogger())

	// goose uses "sqlite3" as its dialect name regardless of the underlying driver.
	// We use modernc.org/sqlite (registered as "sqlite"), but goose's dialect
	// controls SQL generation (e.g., CREATE TABLE syntax), not the driver name.
	if err := goose.SetDialect("sqlite3"); err != nil {
		return err
	}

	if err := goose.Up(db, "migrations"); err != nil {
		return err
	}

	return nil
}
