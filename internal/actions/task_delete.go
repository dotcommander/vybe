package actions

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/dotcommander/vybe/internal/models"
	"github.com/dotcommander/vybe/internal/store"
)

// TaskDeleteIdempotent deletes a task and appends a task_deleted event, idempotent on request_id.
func TaskDeleteIdempotent(db *sql.DB, agentName, requestID, taskID string) (int64, error) {
	if agentName == "" {
		return 0, errors.New("agent name is required")
	}
	if requestID == "" {
		return 0, errors.New("request id is required")
	}
	if taskID == "" {
		return 0, errors.New("task ID is required")
	}

	type idemResult struct {
		EventID int64 `json:"event_id"`
	}

	r, err := store.RunIdempotent(context.Background(), db, agentName, requestID, "task.delete", func(tx *sql.Tx) (idemResult, error) {
		if err := store.DeleteTaskTx(tx, agentName, taskID); err != nil {
			return idemResult{}, err
		}

		eventID, err := store.InsertEventTx(tx, models.EventKindTaskDeleted, agentName, taskID,
			fmt.Sprintf("Task deleted: %s", taskID), "")
		if err != nil {
			return idemResult{}, fmt.Errorf("failed to append event: %w", err)
		}

		return idemResult{EventID: eventID}, nil
	})
	if err != nil {
		return 0, fmt.Errorf("failed to delete task: %w", err)
	}

	return r.EventID, nil
}
