package actions

import (
	"database/sql"

	"github.com/dotcommander/vybe/internal/models"
	"github.com/dotcommander/vybe/internal/store"
)

// ArtifactListByTask returns artifacts linked to a task, newest first.
func ArtifactListByTask(db *sql.DB, taskID string, limit int) ([]*models.Artifact, error) {
	return store.ListArtifactsByTask(db, taskID, limit)
}
