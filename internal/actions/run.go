package actions

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/dotcommander/vybe/internal/models"
	"github.com/dotcommander/vybe/internal/store"
)

// RunResult holds the outcome of a single run loop execution.
type RunResult struct {
	Completed int     `json:"completed"`
	Failed    int     `json:"failed"`
	Total     int     `json:"total"`
	Duration  float64 `json:"duration_sec"`
}

// PersistRunResultIdempotent emits a run_completed event with run metrics as metadata.
func PersistRunResultIdempotent(db *sql.DB, agentName, requestID, projectID string, result RunResult) (int64, error) {
	if agentName == "" {
		return 0, errors.New("agent name is required")
	}
	if requestID == "" {
		return 0, errors.New("request id is required")
	}

	metaBytes, err := json.Marshal(result)
	if err != nil {
		return 0, fmt.Errorf("marshal run result metadata: %w", err)
	}

	msg := fmt.Sprintf("Run completed: %d/%d tasks done, %d failed, %.0fs",
		result.Completed, result.Total, result.Failed, result.Duration)

	return store.AppendEventWithProjectAndMetadataIdempotent(
		db, agentName, requestID,
		models.EventKindRunCompleted, projectID, "",
		msg, string(metaBytes),
	)
}
