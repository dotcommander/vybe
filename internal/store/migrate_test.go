package store

import (
	"database/sql"
	"testing"

	"github.com/pressly/goose/v3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func migrateToVersion(t *testing.T, version int64) *sql.DB {
	t.Helper()
	dbPath := t.TempDir() + "/migrate_test.db"
	db, err := OpenDB(dbPath)
	require.NoError(t, err, "OpenDB failed")
	goose.SetBaseFS(embedMigrations)
	goose.SetVerbose(false)
	goose.SetLogger(goose.NopLogger())
	require.NoError(t, goose.SetDialect("sqlite3"))
	require.NoError(t, goose.UpTo(db, "migrations", version), "goose.UpTo(%d) failed", version)
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func runMigrationTo(t *testing.T, db *sql.DB, version int64) {
	t.Helper()
	goose.SetBaseFS(embedMigrations)
	goose.SetVerbose(false)
	goose.SetLogger(goose.NopLogger())
	require.NoError(t, goose.SetDialect("sqlite3"))
	require.NoError(t, goose.UpTo(db, "migrations", version), "goose.UpTo(%d) failed", version)
}

func columnExists(t *testing.T, db *sql.DB, table, column string) bool {
	t.Helper()
	rows, err := db.Query("PRAGMA table_info(" + table + ")")
	require.NoError(t, err)
	defer rows.Close() //nolint:errcheck // test helper
	for rows.Next() {
		var cid int
		var name, ctype string
		var notnull int
		var dfltValue sql.NullString
		var pk int
		require.NoError(t, rows.Scan(&cid, &name, &ctype, &notnull, &dfltValue, &pk))
		if name == column {
			return true
		}
	}
	return false
}

func indexExists(t *testing.T, db *sql.DB, indexName string) bool {
	t.Helper()
	var count int
	err := db.QueryRow("SELECT COUNT(*) FROM sqlite_master WHERE type='index' AND name=?", indexName).Scan(&count)
	require.NoError(t, err)
	return count > 0
}

func tableExists(t *testing.T, db *sql.DB, tableName string) bool {
	t.Helper()
	var count int
	err := db.QueryRow("SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?", tableName).Scan(&count)
	require.NoError(t, err)
	return count > 0
}

func TestSchemaInvariants_FullMigration(t *testing.T) {
	db := migrateToVersion(t, 25)

	t.Run("tables_exist", func(t *testing.T) {
		tables := []string{
			"events", "tasks", "agent_state", "memory", "artifacts",
			"projects", "idempotency", "task_dependencies", "goose_db_version",
		}
		for _, tbl := range tables {
			assert.True(t, tableExists(t, db, tbl), "table %q should exist", tbl)
		}
	})

	t.Run("dropped_tables_absent", func(t *testing.T) {
		assert.False(t, tableExists(t, db, "retrospective_jobs"), "table retrospective_jobs should not exist")
	})

	t.Run("indexes_exist", func(t *testing.T) {
		indexes := []string{
			"idx_events_id",
			"idx_events_agent_name",
			"idx_events_task_id",
			"idx_events_archived_at",
			"idx_events_project_cursor",
			"idx_events_kind_archived",
			"idx_tasks_status",
			"idx_tasks_project_id",
			"idx_tasks_priority",
			"idx_tasks_focus_selection",
			"idx_memory_scope_key",
			"idx_memory_kind",
			"idx_idempotency_agent",
			"idx_artifacts_project_id",
			"idx_task_deps_depends_on",
			"idx_task_deps_task_id",
		}
		for _, idx := range indexes {
			assert.True(t, indexExists(t, db, idx), "index %q should exist", idx)
		}
	})

	t.Run("dropped_indexes_absent", func(t *testing.T) {
		dropped := []string{
			"idx_tasks_claimed_by",
			"idx_tasks_claim_expires_at",
			"idx_memory_scope_canonical_expires",
			"idx_memory_canonical_unique",
			"idx_memory_active_canonical",
		}
		for _, idx := range dropped {
			assert.False(t, indexExists(t, db, idx), "index %q should not exist", idx)
		}
	})

	t.Run("columns_exist", func(t *testing.T) {
		cases := []struct{ table, column string }{
			{"memory", "access_count"},
			{"memory", "last_accessed_at"},
			{"memory", "updated_at"},
			{"memory", "kind"},
			{"tasks", "blocked_reason"},
			{"tasks", "priority"},
		}
		for _, c := range cases {
			assert.True(t, columnExists(t, db, c.table, c.column), "column %s.%s should exist", c.table, c.column)
		}
	})

	t.Run("dropped_columns_absent", func(t *testing.T) {
		cases := []struct{ table, column string }{
			{"memory", "canonical_key"},
			{"memory", "confidence"},
			{"memory", "source_event_id"},
			{"memory", "superseded_by"},
			{"memory", "last_seen_at"},
			{"tasks", "claimed_by"},
			{"tasks", "claimed_at"},
			{"tasks", "claim_expires_at"},
			{"tasks", "last_heartbeat_at"},
			{"tasks", "attempt"},
		}
		for _, c := range cases {
			assert.False(t, columnExists(t, db, c.table, c.column), "column %s.%s should not exist", c.table, c.column)
		}
	})
}

func TestMigration0025_MemoryKind(t *testing.T) {
	t.Run("column_and_index_added", func(t *testing.T) {
		db := migrateToVersion(t, 25)
		assert.True(t, columnExists(t, db, "memory", "kind"), "memory.kind must exist after migration 25")
		assert.True(t, indexExists(t, db, "idx_memory_kind"), "idx_memory_kind must exist after migration 25")
	})

	t.Run("existing_rows_default_to_fact", func(t *testing.T) {
		db := migrateToVersion(t, 24)
		_, err := db.Exec(`INSERT INTO memory (key, value, value_type, scope, scope_id) VALUES ('k1', 'v1', 'string', 'global', '')`)
		require.NoError(t, err)

		runMigrationTo(t, db, 25)

		var kind string
		require.NoError(t, db.QueryRow(`SELECT kind FROM memory WHERE key='k1'`).Scan(&kind))
		assert.Equal(t, "fact", kind, "pre-existing rows must default to 'fact'")
	})

	t.Run("check_constraint_rejects_invalid_kind", func(t *testing.T) {
		db := migrateToVersion(t, 25)
		_, err := db.Exec(`INSERT INTO memory (key, value, value_type, scope, scope_id, kind) VALUES ('bad', 'v', 'string', 'global', '', 'opinion')`)
		assert.Error(t, err, "CHECK constraint must reject kind='opinion'")
	})

	t.Run("down_removes_column_and_index", func(t *testing.T) {
		db := migrateToVersion(t, 25)
		goose.SetBaseFS(embedMigrations)
		goose.SetVerbose(false)
		goose.SetLogger(goose.NopLogger())
		require.NoError(t, goose.SetDialect("sqlite3"))
		require.NoError(t, goose.Down(db, "migrations"))
		assert.False(t, indexExists(t, db, "idx_memory_kind"), "idx_memory_kind must be gone after Down")
		assert.False(t, columnExists(t, db, "memory", "kind"), "memory.kind must be gone after Down")
	})
}
