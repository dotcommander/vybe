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
	db := migrateToVersion(t, 23)

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

func TestMigration0016_NormalizeAgentNames(t *testing.T) {
	t.Run("agent_state_winner_selection", func(t *testing.T) {
		db := migrateToVersion(t, 15)
		_, err := db.Exec(`INSERT INTO agent_state (agent_name, last_seen_event_id, focus_task_id, focus_project_id, version, last_active_at) VALUES ('Claude', 10, 't1', NULL, 1, '2025-01-01'), ('claude', 20, 't2', NULL, 1, '2025-01-02'), ('CLAUDE', 5, 't3', NULL, 1, '2025-01-03')`)
		require.NoError(t, err)

		runMigrationTo(t, db, 16)

		var count int
		var lastSeenEventID int64
		var focusTaskID string
		require.NoError(t, db.QueryRow(`SELECT COUNT(*), last_seen_event_id, focus_task_id FROM agent_state WHERE agent_name='claude'`).Scan(&count, &lastSeenEventID, &focusTaskID))
		assert.Equal(t, 1, count, "should have exactly 1 row for agent_name='claude'")
		assert.Equal(t, int64(20), lastSeenEventID, "winner should have last_seen_event_id=20")
		assert.Equal(t, "t2", focusTaskID, "winner should have focus_task_id='t2'")
	})

	t.Run("no_collision_preservation", func(t *testing.T) {
		db := migrateToVersion(t, 15)
		_, err := db.Exec(`INSERT INTO agent_state (agent_name, last_seen_event_id, focus_task_id, focus_project_id, version, last_active_at) VALUES ('agent-a', 5, 't1', NULL, 1, '2025-01-01'), ('agent-b', 10, 't2', NULL, 1, '2025-01-02')`)
		require.NoError(t, err)

		runMigrationTo(t, db, 16)

		var count int
		require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM agent_state`).Scan(&count))
		assert.Equal(t, 2, count, "should preserve both rows when no collision")

		var lseA, lseB int64
		var ftA, ftB string
		require.NoError(t, db.QueryRow(`SELECT last_seen_event_id, focus_task_id FROM agent_state WHERE agent_name='agent-a'`).Scan(&lseA, &ftA))
		require.NoError(t, db.QueryRow(`SELECT last_seen_event_id, focus_task_id FROM agent_state WHERE agent_name='agent-b'`).Scan(&lseB, &ftB))
		assert.Equal(t, int64(5), lseA)
		assert.Equal(t, "t1", ftA)
		assert.Equal(t, int64(10), lseB)
		assert.Equal(t, "t2", ftB)
	})

	t.Run("events_normalization", func(t *testing.T) {
		db := migrateToVersion(t, 15)
		_, err := db.Exec(`INSERT INTO events (kind, agent_name, task_id, message) VALUES ('test', 'Claude', '', 'msg1'), ('test', 'CLAUDE', '', 'msg2'), ('test', 'claude', '', 'msg3')`)
		require.NoError(t, err)

		runMigrationTo(t, db, 16)

		var distinctCount int
		require.NoError(t, db.QueryRow(`SELECT COUNT(DISTINCT agent_name) FROM events`).Scan(&distinctCount))
		assert.Equal(t, 1, distinctCount, "all events should have same normalized agent_name")

		var agentName string
		require.NoError(t, db.QueryRow(`SELECT DISTINCT agent_name FROM events`).Scan(&agentName))
		assert.Equal(t, "claude", agentName)
	})

	t.Run("idempotency_normalization", func(t *testing.T) {
		db := migrateToVersion(t, 15)
		_, err := db.Exec(`INSERT INTO idempotency (agent_name, request_id, command, result_json) VALUES ('Claude', 'req1', 'task.create', '{"ok":true}'), ('claude', 'req2', 'task.close', '{"ok":true}')`)
		require.NoError(t, err)

		runMigrationTo(t, db, 16)

		var total, nonLower int
		require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM idempotency`).Scan(&total))
		require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM idempotency WHERE agent_name != 'claude'`).Scan(&nonLower))
		assert.Equal(t, 2, total)
		assert.Equal(t, 0, nonLower)
	})

	t.Run("memory_agent_scope_normalization", func(t *testing.T) {
		db := migrateToVersion(t, 15)
		_, err := db.Exec(`INSERT INTO memory (key, value, value_type, scope, scope_id, created_at, canonical_key, confidence) VALUES ('pref', 'old', 'string', 'agent', 'Claude', '2025-01-01', 'pref', 0.5), ('pref', 'new', 'string', 'agent', 'claude', '2025-01-02', 'pref', 0.5)`)
		require.NoError(t, err)

		runMigrationTo(t, db, 16)

		var count int
		require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM memory WHERE scope='agent'`).Scan(&count))
		assert.Equal(t, 1, count, "should consolidate to 1 row for agent scope after normalization")

		var value, scopeID string
		require.NoError(t, db.QueryRow(`SELECT value, scope_id FROM memory WHERE scope='agent'`).Scan(&value, &scopeID))
		assert.Equal(t, "new", value)
		assert.Equal(t, "claude", scopeID)
	})
}

func TestMigration0019_SimplifyMemory(t *testing.T) {
	t.Run("updated_at_backfill", func(t *testing.T) {
		db := migrateToVersion(t, 16)
		_, err := db.Exec(`INSERT INTO memory (key, value, value_type, scope, scope_id, created_at, canonical_key, confidence, last_seen_at) VALUES ('k1', 'v1', 'string', 'global', '', '2025-01-01', 'k1', 0.5, '2025-06-15'), ('k2', 'v2', 'string', 'global', '', '2025-02-01', 'k2', 0.5, NULL)`)
		require.NoError(t, err)

		runMigrationTo(t, db, 19)

		var updatedAt1, updatedAt2 string
		require.NoError(t, db.QueryRow(`SELECT updated_at FROM memory WHERE key='k1'`).Scan(&updatedAt1))
		require.NoError(t, db.QueryRow(`SELECT updated_at FROM memory WHERE key='k2'`).Scan(&updatedAt2))
		assert.Contains(t, updatedAt1, "2025-06-15", "k1 updated_at should backfill from last_seen_at")
		assert.Contains(t, updatedAt2, "2025-02-01", "k2 updated_at should backfill from created_at when last_seen_at is NULL")
	})

	t.Run("dropped_columns", func(t *testing.T) {
		db := migrateToVersion(t, 19)
		dropped := []string{"canonical_key", "confidence", "source_event_id", "superseded_by", "last_seen_at"}
		for _, col := range dropped {
			assert.False(t, columnExists(t, db, "memory", col), "column memory.%s should not exist after migration 19", col)
		}
		assert.True(t, columnExists(t, db, "memory", "updated_at"), "column memory.updated_at should exist after migration 19")
	})

	t.Run("dropped_indexes", func(t *testing.T) {
		db := migrateToVersion(t, 19)
		dropped := []string{
			"idx_memory_active_canonical",
			"idx_memory_canonical_unique",
			"idx_memory_scope_canonical_expires",
		}
		for _, idx := range dropped {
			assert.False(t, indexExists(t, db, idx), "index %q should not exist after migration 19", idx)
		}
	})
}

func TestMigration0020_DropTaskClaims(t *testing.T) {
	t.Run("task_survives", func(t *testing.T) {
		db := migrateToVersion(t, 19)
		_, err := db.Exec(`INSERT INTO tasks (id, title, status, version, claimed_by, claimed_at, claim_expires_at, last_heartbeat_at, attempt) VALUES ('task_1', 'Test Task', 'pending', 1, 'agent-a', '2025-01-01', '2025-01-02', '2025-01-01', 3)`)
		require.NoError(t, err)

		runMigrationTo(t, db, 20)

		var id, title, status string
		require.NoError(t, db.QueryRow(`SELECT id, title, status FROM tasks WHERE id='task_1'`).Scan(&id, &title, &status))
		assert.Equal(t, "task_1", id)
		assert.Equal(t, "Test Task", title)
		assert.Equal(t, "pending", status)
	})

	t.Run("columns_dropped", func(t *testing.T) {
		db := migrateToVersion(t, 20)
		dropped := []string{"claimed_by", "claimed_at", "claim_expires_at", "last_heartbeat_at", "attempt"}
		for _, col := range dropped {
			assert.False(t, columnExists(t, db, "tasks", col), "column tasks.%s should not exist after migration 20", col)
		}
	})

	t.Run("indexes_dropped", func(t *testing.T) {
		db := migrateToVersion(t, 20)
		dropped := []string{"idx_tasks_claimed_by", "idx_tasks_claim_expires_at"}
		for _, idx := range dropped {
			assert.False(t, indexExists(t, db, idx), "index %q should not exist after migration 20", idx)
		}
	})
}
