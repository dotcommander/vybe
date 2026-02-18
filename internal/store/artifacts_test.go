package store

import (
	"regexp"
	"testing"

	"github.com/stretchr/testify/require"
)

var artifactIDPattern = regexp.MustCompile(`^artifact_\d+(_[0-9a-f]{12})?$`)

func TestAddArtifactAndList(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	task, err := CreateTask(db, "t", "d", "", 0)
	require.NoError(t, err)

	artifact, eventID, err := AddArtifact(db, "agent1", task.ID, "/tmp/out.txt", "text/plain")
	require.NoError(t, err)
	require.NotNil(t, artifact)
	require.Greater(t, eventID, int64(0))
	require.Equal(t, task.ID, artifact.TaskID)

	got, err := GetArtifact(db, artifact.ID)
	require.NoError(t, err)
	require.Equal(t, artifact.ID, got.ID)

	list, err := ListArtifactsByTask(db, task.ID, 10)
	require.NoError(t, err)
	require.Len(t, list, 1)
	require.Equal(t, artifact.ID, list[0].ID)
}

func TestGenerateArtifactIDFormat(t *testing.T) {
	id := generateArtifactID()
	require.True(t, artifactIDPattern.MatchString(id), "unexpected artifact id format: %s", id)
}
