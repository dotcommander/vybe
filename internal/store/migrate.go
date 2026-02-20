package store

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"strconv"
	"strings"

	"github.com/pressly/goose/v3"
)

//go:embed migrations/*.sql
var embedMigrations embed.FS

// canonicalReconcileMigrationVersion is the goose migration version that
// introduced canonical_key quality fields. Reconciliation + index creation
// only need to run once when crossing this version boundary.
const canonicalReconcileMigrationVersion int64 = 12

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

// RunMigrations runs all pending migrations using goose. When migration 00012
// is newly applied, runs Go-side canonical reconciliation and index creation.
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

	// Capture version before migration to detect if 00012 is newly applied.
	versionBefore, _ := goose.GetDBVersion(db)

	if err := goose.Up(db, "migrations"); err != nil {
		return err
	}

	versionAfter, _ := goose.GetDBVersion(db)

	// Only run the heavy reconciliation + index creation when migration 12
	// was applied in this run (version crossed the boundary).
	if versionBefore < canonicalReconcileMigrationVersion && versionAfter >= canonicalReconcileMigrationVersion {
		if err := reconcileCanonicalKeys(db); err != nil {
			return fmt.Errorf("canonical key reconciliation: %w", err)
		}
		if err := ensureCanonicalIndex(db); err != nil {
			return fmt.Errorf("canonical index creation: %w", err)
		}
	}

	return nil
}

// reconcileCanonicalKeys re-normalizes all canonical_key values using the runtime
// NormalizeMemoryKey function, then resolves any collisions among active rows.
// Runs inside a single transaction for crash safety.
//
//nolint:gocognit,gocyclo,funlen,revive // two-phase migration (normalize then resolve collisions) requires many branches for safe per-row handling
func reconcileCanonicalKeys(db *sql.DB) error {
	return Transact(db, func(tx *sql.Tx) error {
		// Phase 1: Re-normalize all canonical_key values.
		// Scan into slice first (SQLite single-connection safety).
		rows, err := tx.QueryContext(context.Background(), `SELECT id, key, canonical_key FROM memory`)
		if err != nil {
			return fmt.Errorf("query memory for reconciliation: %w", err)
		}

		type update struct {
			id           int64
			newCanonical string
		}
		var updates []update
		func() {
			defer func() { _ = rows.Close() }()
			for rows.Next() {
				var id int64
				var key string
				var canonical sql.NullString
				if scanErr := rows.Scan(&id, &key, &canonical); scanErr != nil {
					err = fmt.Errorf("scan memory row: %w", scanErr)
					return
				}
				correct := NormalizeMemoryKey(key)
				if !canonical.Valid || canonical.String != correct {
					updates = append(updates, update{id: id, newCanonical: correct})
				}
			}
			if rowsErr := rows.Err(); rowsErr != nil {
				err = fmt.Errorf("iterate memory rows: %w", rowsErr)
			}
		}()
		if err != nil {
			return err
		}

		for _, u := range updates {
			if _, execErr := tx.ExecContext(context.Background(), `UPDATE memory SET canonical_key = ? WHERE id = ?`, u.newCanonical, u.id); execErr != nil {
				return fmt.Errorf("update canonical_key for id %d: %w", u.id, execErr)
			}
		}

		// Phase 2: Resolve collisions among active rows (superseded_by IS NULL).
		// Scan groups into slice first, then resolve each.
		collisionRows, collisionErr := tx.QueryContext(context.Background(), `
			SELECT scope, scope_id, canonical_key
			FROM memory
			WHERE superseded_by IS NULL
			GROUP BY scope, scope_id, canonical_key
			HAVING COUNT(*) > 1
		`)
		if collisionErr != nil {
			return fmt.Errorf("query canonical collisions: %w", collisionErr)
		}

		type collisionGroup struct {
			scope, scopeID, canonicalKey string
		}
		var groups []collisionGroup
		func() {
			defer func() { _ = collisionRows.Close() }()
			for collisionRows.Next() {
				var g collisionGroup
				if scanErr := collisionRows.Scan(&g.scope, &g.scopeID, &g.canonicalKey); scanErr != nil {
					err = fmt.Errorf("scan collision group: %w", scanErr)
					return
				}
				groups = append(groups, g)
			}
			if rowsErr := collisionRows.Err(); rowsErr != nil {
				err = fmt.Errorf("iterate collision groups: %w", rowsErr)
			}
		}()
		if err != nil {
			return err
		}

		for _, g := range groups {
			var winnerID int64
			err := tx.QueryRowContext(context.Background(), `
				SELECT id FROM memory
				WHERE scope = ? AND scope_id = ? AND canonical_key = ? AND superseded_by IS NULL
				ORDER BY confidence DESC, COALESCE(last_seen_at, created_at) DESC, id DESC
				LIMIT 1
			`, g.scope, g.scopeID, g.canonicalKey).Scan(&winnerID)
			if err != nil {
				return fmt.Errorf("find collision winner: %w", err)
			}

			supersededBy := fmt.Sprintf("memory_%d", winnerID)
			_, err = tx.ExecContext(context.Background(), `
				UPDATE memory
				SET superseded_by = ?
				WHERE scope = ? AND scope_id = ? AND canonical_key = ?
				  AND superseded_by IS NULL AND id != ?
			`, supersededBy, g.scope, g.scopeID, g.canonicalKey, winnerID)
			if err != nil {
				return fmt.Errorf("supersede collision losers: %w", err)
			}
		}

		return nil
	})
}

// ensureCanonicalIndex creates the partial unique index on active memory rows.
// Safe to call repeatedly (IF NOT EXISTS).
func ensureCanonicalIndex(db *sql.DB) error {
	_, err := db.ExecContext(context.Background(), `
		CREATE UNIQUE INDEX IF NOT EXISTS idx_memory_active_canonical
		ON memory(scope, scope_id, canonical_key)
		WHERE superseded_by IS NULL
	`)
	if err != nil {
		return fmt.Errorf("create canonical unique index: %w", err)
	}
	return nil
}
