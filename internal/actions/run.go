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

// RunDashboard aggregates run statistics over recent runs.
type RunDashboard struct {
	Runs         int                   `json:"runs"`
	AvgCompleted float64               `json:"avg_completed"`
	AvgFailed    float64               `json:"avg_failed"`
	BlockedRate  float64               `json:"blocked_rate"`
	AvgDuration  float64               `json:"avg_duration_sec"`
	History      []store.RunSummaryRow `json:"history"`
}

// RunStats computes aggregate statistics from recent run_completed events.
func RunStats(db *sql.DB, agentName, projectID string, lastN int) (*RunDashboard, error) {
	rows, err := store.QueryRunCompletedEvents(db, agentName, projectID, lastN)
	if err != nil {
		return nil, fmt.Errorf("query run events: %w", err)
	}

	dash := &RunDashboard{
		Runs:    len(rows),
		History: rows,
	}

	if len(rows) == 0 {
		return dash, nil
	}

	var totalCompleted, totalFailed, totalAll int
	var totalDuration float64

	for _, r := range rows {
		totalCompleted += r.Completed
		totalFailed += r.Failed
		totalAll += r.Total
		totalDuration += r.Duration
	}

	n := float64(len(rows))
	dash.AvgCompleted = float64(totalCompleted) / n
	dash.AvgFailed = float64(totalFailed) / n
	dash.AvgDuration = totalDuration / n

	if totalAll > 0 {
		dash.BlockedRate = float64(totalFailed) / float64(totalAll)
	}

	return dash, nil
}
