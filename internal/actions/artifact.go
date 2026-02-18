package actions

import (
	"database/sql"
	"errors"

	"github.com/dotcommander/vybe/internal/models"
	"github.com/dotcommander/vybe/internal/store"
)

func ArtifactAddIdempotent(db *sql.DB, agentName, requestID, taskID, filePath, contentType string) (*models.Artifact, int64, error) { //nolint:revive // argument-limit: all artifact params are required and distinct
	if agentName == "" {
		return nil, 0, errors.New("agent name is required")
	}
	if requestID == "" {
		return nil, 0, errors.New("request id is required")
	}
	artifact, eventID, err := store.AddArtifactIdempotent(db, agentName, requestID, taskID, filePath, contentType)
	if err != nil {
		return nil, 0, err
	}
	return artifact, eventID, nil
}

// ArtifactGet retrieves a single artifact by ID.
func ArtifactGet(db *sql.DB, id string) (*models.Artifact, error) {
	return store.GetArtifact(db, id)
}

// ArtifactListByTask returns artifacts linked to a task, newest first.
func ArtifactListByTask(db *sql.DB, taskID string, limit int) ([]*models.Artifact, error) {
	return store.ListArtifactsByTask(db, taskID, limit)
}
