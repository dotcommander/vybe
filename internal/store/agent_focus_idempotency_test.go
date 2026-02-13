package store

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSetAgentFocusTaskWithEventIdempotent_Replay(t *testing.T) {
	db, err := InitDBWithPath(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	agent := "agent1"
	_, err = LoadOrCreateAgentState(db, agent)
	require.NoError(t, err)

	// Need a real task row to focus.
	_, err = db.Exec(`
		INSERT INTO tasks (id, title, description, status, version, created_at, updated_at)
		VALUES ('task_1', 't', 'd', 'pending', 1, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
	`)
	require.NoError(t, err)

	req := "req_focus"
	e1, err := SetAgentFocusTaskWithEventIdempotent(db, agent, req, "task_1")
	require.NoError(t, err)
	e2, err := SetAgentFocusTaskWithEventIdempotent(db, agent, req, "task_1")
	require.NoError(t, err)
	require.Equal(t, e1, e2)

	var focusEvents int
	require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM events WHERE kind = 'agent_focus' AND agent_name = ?`, agent).Scan(&focusEvents))
	require.Equal(t, 1, focusEvents)
}
