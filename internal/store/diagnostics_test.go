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

func TestRunDiagnostics_StaleClaim(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	_, err := db.Exec(`
		INSERT INTO tasks (id, title, status, claimed_by, claim_expires_at, version, created_at, updated_at)
		VALUES ('task_stale_001', 'stale task', 'in_progress', 'agent-x',
		        datetime('now', '-1 hour'), 1, datetime('now'), datetime('now'))
	`)
	require.NoError(t, err)

	diags, err := RunDiagnostics(db)
	require.NoError(t, err)
	require.Len(t, diags, 1)
	require.Equal(t, "STALE_CLAIM", diags[0].Code)
	require.Equal(t, "warning", diags[0].Level)
	require.Contains(t, diags[0].Message, "task_stale_001")
	require.Contains(t, diags[0].Message, "agent-x")
	require.NotEmpty(t, diags[0].SuggestedAction)
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

func TestRunDiagnostics_ZombieClaim(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	_, err := db.Exec(`
		INSERT INTO tasks (id, title, status, claimed_by, claim_expires_at, version, created_at, updated_at)
		VALUES ('task_zombie_001', 'zombie task', 'pending', 'agent-z',
		        datetime('now', '-1 hour'), 1, datetime('now'), datetime('now'))
	`)
	require.NoError(t, err)

	diags, err := RunDiagnostics(db)
	require.NoError(t, err)
	require.Len(t, diags, 1)
	require.Equal(t, "ZOMBIE_CLAIM", diags[0].Code)
	require.Equal(t, "warning", diags[0].Level)
	require.Contains(t, diags[0].Message, "task_zombie_001")
	require.Contains(t, diags[0].Message, "agent-z")
	require.Contains(t, diags[0].Message, "pending")
	require.NotEmpty(t, diags[0].SuggestedAction)
}

func TestRunDiagnostics_NoDiagnosticsForHealthyState(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	_, err := db.Exec(`
		INSERT INTO tasks (id, title, status, claimed_by, claim_expires_at, version, created_at, updated_at)
		VALUES ('task_healthy_001', 'active task', 'in_progress', 'agent-healthy',
		        datetime('now', '+1 hour'), 1, datetime('now'), datetime('now'))
	`)
	require.NoError(t, err)

	diags, err := RunDiagnostics(db)
	require.NoError(t, err)
	require.Empty(t, diags)
}
