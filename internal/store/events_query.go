package store

import (
	"database/sql"
	"fmt"
	"strings"

	"github.com/dotcommander/vybe/internal/models"
)

type ListEventsParams struct {
	AgentName       string
	ProjectID       string
	TaskID          string
	Kind            string
	SinceID         int64
	Limit           int
	Desc            bool
	IncludeArchived bool
}

func ListEvents(db *sql.DB, p ListEventsParams) ([]*models.Event, error) {
	if p.Limit <= 0 {
		p.Limit = 50
	}
	if p.Limit > 1000 {
		p.Limit = 1000
	}

	where := make([]string, 0, 4)
	args := make([]interface{}, 0, 6)

	if p.AgentName != "" {
		where = append(where, "agent_name = ?")
		args = append(args, p.AgentName)
	}
	if p.TaskID != "" {
		where = append(where, "task_id = ?")
		args = append(args, p.TaskID)
	}
	if p.ProjectID != "" {
		where = append(where, "(project_id = ? OR project_id IS NULL)")
		args = append(args, p.ProjectID)
	}
	if p.Kind != "" {
		where = append(where, "kind = ?")
		args = append(args, p.Kind)
	}
	if p.SinceID > 0 {
		where = append(where, "id > ?")
		args = append(args, p.SinceID)
	}
	if !p.IncludeArchived {
		where = append(where, "archived_at IS NULL")
	}

	query := `
		SELECT id, kind, agent_name, project_id, task_id, message, metadata, created_at
		FROM events
	`
	if len(where) > 0 {
		query += " WHERE " + strings.Join(where, " AND ")
	}
	if p.Desc {
		query += " ORDER BY id DESC"
	} else {
		query += " ORDER BY id ASC"
	}
	query += " LIMIT ?"
	args = append(args, p.Limit)

	var out []*models.Event
	err := RetryWithBackoff(func() error {
		rows, err := db.Query(query, args...)
		if err != nil {
			return fmt.Errorf("failed to list events: %w", err)
		}
		defer rows.Close()

		out = make([]*models.Event, 0)
		for rows.Next() {
			var e models.Event
			var projectID sql.NullString
			var meta sql.NullString
			var taskID sql.NullString
			if err := rows.Scan(&e.ID, &e.Kind, &e.AgentName, &projectID, &taskID, &e.Message, &meta, &e.CreatedAt); err != nil {
				return fmt.Errorf("failed to scan event: %w", err)
			}
			if projectID.Valid {
				e.ProjectID = projectID.String
			}
			if taskID.Valid {
				e.TaskID = taskID.String
			}
			e.Metadata = decodeEventMetadata(meta)
			out = append(out, &e)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}
