package store

import (
	"database/sql"
	"embed"
	"fmt"

	"github.com/pressly/goose/v3"
)

//go:embed migrations/*.sql
var embedMigrations embed.FS

// canonicalReconcileMigrationVersion is the goose migration version that
// introduced canonical_key quality fields. Reconciliation + index creation
// only need to run once when crossing this version boundary.
const canonicalReconcileMigrationVersion int64 = 12

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
// normalizeMemoryKey function, then resolves any collisions among active rows.
// Runs inside a single transaction for crash safety.
func reconcileCanonicalKeys(db *sql.DB) error {
	return Transact(db, func(tx *sql.Tx) error {
		// Phase 1: Re-normalize all canonical_key values.
		// Scan into slice first (SQLite single-connection safety).
		rows, err := tx.Query(`SELECT id, key, canonical_key FROM memory`)
		if err != nil {
			return fmt.Errorf("query memory for reconciliation: %w", err)
		}

		type update struct {
			id           int64
			newCanonical string
		}
		var updates []update
		for rows.Next() {
			var id int64
			var key string
			var canonical sql.NullString
			if err := rows.Scan(&id, &key, &canonical); err != nil {
				rows.Close()
				return fmt.Errorf("scan memory row: %w", err)
			}
			correct := normalizeMemoryKey(key)
			if !canonical.Valid || canonical.String != correct {
				updates = append(updates, update{id: id, newCanonical: correct})
			}
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return fmt.Errorf("iterate memory rows: %w", err)
		}
		rows.Close()

		for _, u := range updates {
			if _, err := tx.Exec(`UPDATE memory SET canonical_key = ? WHERE id = ?`, u.newCanonical, u.id); err != nil {
				return fmt.Errorf("update canonical_key for id %d: %w", u.id, err)
			}
		}

		// Phase 2: Resolve collisions among active rows (superseded_by IS NULL).
		// Scan groups into slice first, then resolve each.
		collisionRows, err := tx.Query(`
			SELECT scope, scope_id, canonical_key
			FROM memory
			WHERE superseded_by IS NULL
			GROUP BY scope, scope_id, canonical_key
			HAVING COUNT(*) > 1
		`)
		if err != nil {
			return fmt.Errorf("query canonical collisions: %w", err)
		}

		type collisionGroup struct {
			scope, scopeID, canonicalKey string
		}
		var groups []collisionGroup
		for collisionRows.Next() {
			var g collisionGroup
			if err := collisionRows.Scan(&g.scope, &g.scopeID, &g.canonicalKey); err != nil {
				collisionRows.Close()
				return fmt.Errorf("scan collision group: %w", err)
			}
			groups = append(groups, g)
		}
		if err := collisionRows.Err(); err != nil {
			collisionRows.Close()
			return fmt.Errorf("iterate collision groups: %w", err)
		}
		collisionRows.Close()

		for _, g := range groups {
			var winnerID int64
			err := tx.QueryRow(`
				SELECT id FROM memory
				WHERE scope = ? AND scope_id = ? AND canonical_key = ? AND superseded_by IS NULL
				ORDER BY confidence DESC, COALESCE(last_seen_at, created_at) DESC, id DESC
				LIMIT 1
			`, g.scope, g.scopeID, g.canonicalKey).Scan(&winnerID)
			if err != nil {
				return fmt.Errorf("find collision winner: %w", err)
			}

			supersededBy := fmt.Sprintf("memory_%d", winnerID)
			_, err = tx.Exec(`
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
	_, err := db.Exec(`
		CREATE UNIQUE INDEX IF NOT EXISTS idx_memory_active_canonical
		ON memory(scope, scope_id, canonical_key)
		WHERE superseded_by IS NULL
	`)
	if err != nil {
		return fmt.Errorf("create canonical unique index: %w", err)
	}
	return nil
}
