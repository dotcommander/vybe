package actions

import (
	"database/sql"
	"fmt"

	"github.com/dotcommander/vibe/internal/models"
	"github.com/dotcommander/vibe/internal/store"
)

func ArtifactAdd(db *sql.DB, agentName, taskID, filePath, contentType string) (*models.Artifact, int64, error) {
	if agentName == "" {
		return nil, 0, fmt.Errorf("agent name is required")
	}
	artifact, eventID, err := store.AddArtifact(db, agentName, taskID, filePath, contentType)
	if err != nil {
		return nil, 0, err
	}
	return artifact, eventID, nil
}

func ArtifactAddIdempotent(db *sql.DB, agentName, requestID, taskID, filePath, contentType string) (*models.Artifact, int64, error) {
	if agentName == "" {
		return nil, 0, fmt.Errorf("agent name is required")
	}
	if requestID == "" {
		return nil, 0, fmt.Errorf("request id is required")
	}
	artifact, eventID, err := store.AddArtifactIdempotent(db, agentName, requestID, taskID, filePath, contentType)
	if err != nil {
		return nil, 0, err
	}
	return artifact, eventID, nil
}

func ArtifactGet(db *sql.DB, id string) (*models.Artifact, error) {
	return store.GetArtifact(db, id)
}

func ArtifactListByTask(db *sql.DB, taskID string, limit int) ([]*models.Artifact, error) {
	return store.ListArtifactsByTask(db, taskID, limit)
}
