package store

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/dotcommander/vybe/internal/models"
)

// RunSummaryRow represents a parsed run_completed event.
type RunSummaryRow struct {
	EventID   int64     `json:"event_id"`
	AgentName string    `json:"agent_name"`
	ProjectID string    `json:"project_id,omitempty"`
	Completed int       `json:"completed"`
	Failed    int       `json:"failed"`
	Total     int       `json:"total"`
	Duration  float64   `json:"duration_sec"`
	CreatedAt time.Time `json:"created_at"`
}

// runCompletedMetadata mirrors the JSON stored in event metadata for run_completed events.
type runCompletedMetadata struct {
	Completed int     `json:"completed"`
	Failed    int     `json:"failed"`
	Total     int     `json:"total"`
	Duration  float64 `json:"duration_sec"`
}

// QueryRunCompletedEvents returns the most recent run_completed events, newest first.
func QueryRunCompletedEvents(db *sql.DB, agentName, projectID string, limit int) ([]RunSummaryRow, error) {
	if limit <= 0 {
		limit = 10
	}
	if limit > 100 {
		limit = 100
	}

	var out []RunSummaryRow
	err := RetryWithBackoff(func() error {
		where := []string{"kind = ?"}
		args := []interface{}{models.EventKindRunCompleted}

		if agentName != "" {
			where = append(where, "agent_name = ?")
			args = append(args, agentName)
		}
		if projectID != "" {
			where = append(where, "(project_id = ? OR project_id IS NULL)")
			args = append(args, projectID)
		}

		query := "SELECT id, agent_name, project_id, metadata, created_at FROM events WHERE "
		for i, w := range where {
			if i > 0 {
				query += " AND "
			}
			query += w
		}
		query += " ORDER BY id DESC LIMIT ?"
		args = append(args, limit)

		rows, err := db.Query(query, args...)
		if err != nil {
			return fmt.Errorf("query run_completed events: %w", err)
		}
		defer rows.Close()

		out = make([]RunSummaryRow, 0, limit)
		for rows.Next() {
			var row RunSummaryRow
			var projID sql.NullString
			var meta sql.NullString
			if err := rows.Scan(&row.EventID, &row.AgentName, &projID, &meta, &row.CreatedAt); err != nil {
				return fmt.Errorf("scan run_completed event: %w", err)
			}
			if projID.Valid {
				row.ProjectID = projID.String
			}
			if meta.Valid && meta.String != "" {
				var m runCompletedMetadata
				if err := json.Unmarshal([]byte(meta.String), &m); err == nil {
					row.Completed = m.Completed
					row.Failed = m.Failed
					row.Total = m.Total
					row.Duration = m.Duration
				}
			}
			out = append(out, row)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}
