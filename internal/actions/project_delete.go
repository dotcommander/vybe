package actions

import (
	"database/sql"
	"errors"
	"fmt"

	"github.com/dotcommander/vybe/internal/models"
	"github.com/dotcommander/vybe/internal/store"
)

// ProjectDeleteIdempotent deletes a project and appends a project_deleted event, idempotent on request_id.
func ProjectDeleteIdempotent(db *sql.DB, agentName, requestID, projectID string) (int64, error) {
	if agentName == "" {
		return 0, errors.New("agent name is required")
	}
	if requestID == "" {
		return 0, errors.New("request id is required")
	}
	if projectID == "" {
		return 0, errors.New("project ID is required")
	}

	type idemResult struct {
		EventID int64 `json:"event_id"`
	}

	r, err := store.RunIdempotent(db, agentName, requestID, "project.delete", func(tx *sql.Tx) (idemResult, error) {
		if err := store.DeleteProjectTx(tx, projectID); err != nil {
			return idemResult{}, err
		}

		eventID, err := store.InsertEventTx(tx, models.EventKindProjectDeleted, agentName, "",
			fmt.Sprintf("Project deleted: %s", projectID), "")
		if err != nil {
			return idemResult{}, fmt.Errorf("failed to append event: %w", err)
		}

		return idemResult{EventID: eventID}, nil
	})
	if err != nil {
		return 0, fmt.Errorf("failed to delete project: %w", err)
	}

	return r.EventID, nil
}
