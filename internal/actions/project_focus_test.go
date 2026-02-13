package actions

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dotcommander/vibe/internal/store"
)

func TestProjectFocusIdempotent_ReplaysAfterProjectDeleted(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	project, err := store.CreateProject(db, "Replay Project", "")
	require.NoError(t, err)

	eventID1, err := ProjectFocusIdempotent(db, "agent-project", "req_project_focus", project.ID)
	require.NoError(t, err)
	require.Greater(t, eventID1, int64(0))

	_, err = db.Exec(`DELETE FROM projects WHERE id = ?`, project.ID)
	require.NoError(t, err)

	eventID2, err := ProjectFocusIdempotent(db, "agent-project", "req_project_focus", project.ID)
	require.NoError(t, err)
	require.Equal(t, eventID1, eventID2)
}

func TestProjectFocusIdempotent_RejectsUnknownProject(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	_, err := ProjectFocusIdempotent(db, "agent-project", "req_project_focus_missing", "project_missing")
	require.Error(t, err)
	require.Contains(t, err.Error(), "project not found")
}
