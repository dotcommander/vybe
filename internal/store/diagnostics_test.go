package store

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRunDiagnostics_Clean(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	diags, err := RunDiagnostics(db)
	require.NoError(t, err)
	require.Empty(t, diags)
}

func TestRunDiagnostics_StaleFocus(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	_, err := db.Exec(`
		INSERT INTO tasks (id, title, status, version, created_at, updated_at)
		VALUES ('task_done_001', 'finished task', 'completed', 1, datetime('now'), datetime('now'))
	`)
	require.NoError(t, err)

	_, err = db.Exec(`
		INSERT INTO agent_state (agent_name, last_seen_event_id, focus_task_id, last_active_at)
		VALUES ('agent-stale', 0, 'task_done_001', datetime('now'))
	`)
	require.NoError(t, err)

	diags, err := RunDiagnostics(db)
	require.NoError(t, err)
	require.Len(t, diags, 1)
	require.Equal(t, "STALE_FOCUS", diags[0].Code)
	require.Equal(t, "warning", diags[0].Level)
	require.Contains(t, diags[0].Message, "agent-stale")
	require.Contains(t, diags[0].Message, "task_done_001")
	require.NotEmpty(t, diags[0].SuggestedAction)
}

func TestRunDiagnostics_StaleInProgress(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	_, err := db.Exec(`
		INSERT INTO tasks (id, title, status, version, created_at, updated_at)
		VALUES ('task_stale_ip', 'stuck task', 'in_progress', 1,
		        datetime('now', '-2 hours'), datetime('now', '-2 hours'))
	`)
	require.NoError(t, err)

	diags, err := RunDiagnostics(db)
	require.NoError(t, err)
	require.Len(t, diags, 1)
	require.Equal(t, "STALE_IN_PROGRESS", diags[0].Code)
	require.Equal(t, "warning", diags[0].Level)
	require.Contains(t, diags[0].Message, "task_stale_ip")
	require.NotEmpty(t, diags[0].SuggestedAction)
}

func TestRunDiagnostics_NoDiagnosticsForHealthyState(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	_, err := db.Exec(`
		INSERT INTO tasks (id, title, status, version, created_at, updated_at)
		VALUES ('task_healthy_001', 'active task', 'in_progress', 1,
		        datetime('now'), datetime('now'))
	`)
	require.NoError(t, err)

	diags, err := RunDiagnostics(db)
	require.NoError(t, err)
	require.Empty(t, diags)
}
