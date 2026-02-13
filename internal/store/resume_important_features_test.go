package store

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestFetchRecentUserPrompts_ByProjectAndMetadata(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	_, err := CreateProject(db, "proj-a", "")
	require.NoError(t, err)

	// Project via project_id column.
	_, err = AppendEventWithProjectAndMetadataIdempotent(
		db,
		"agent-a",
		"req-prompt-1",
		"user_prompt",
		"proj-a",
		"",
		"prompt 1",
		`{"project":"proj-a"}`,
	)
	require.NoError(t, err)

	// Project via metadata only.
	_, err = AppendEventWithMetadata(
		db,
		"user_prompt",
		"agent-a",
		"",
		"prompt 2",
		`{"project":"proj-a"}`,
	)
	require.NoError(t, err)

	filtered, err := FetchRecentUserPrompts(db, "proj-a", 10)
	require.NoError(t, err)
	require.Len(t, filtered, 2)

	all, err := FetchRecentUserPrompts(db, "", 1)
	require.NoError(t, err)
	require.Len(t, all, 1)
}
