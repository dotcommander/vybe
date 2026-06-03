package store

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/dotcommander/vybe/internal/models"
)

// FetchRecentUserPrompts retrieves the most recent user_prompt events for a project.
func FetchRecentUserPrompts(db *sql.DB, projectDir string, limit int) ([]*models.Event, error) {
	if limit <= 0 {
		limit = 5
	}

	var events []*models.Event
	err := RetryWithBackoff(context.Background(), func() error {
		var query string
		var args []any

		if projectDir != "" {
			query = `
				SELECT id, kind, agent_name, project_id, task_id, message, metadata, created_at
				FROM events
				WHERE kind = 'user_prompt' AND archived_at IS NULL
				  AND (project_id = ? OR json_extract(metadata, '$.project') = ?)
				ORDER BY id DESC LIMIT ?
			`
			args = []any{projectDir, projectDir, limit}
		} else {
			query = `
				SELECT id, kind, agent_name, project_id, task_id, message, metadata, created_at
				FROM events
				WHERE kind = 'user_prompt' AND archived_at IS NULL
				ORDER BY id DESC LIMIT ?
			`
			args = []any{limit}
		}

		rows, err := db.QueryContext(context.Background(), query, args...)
		if err != nil {
			return fmt.Errorf("failed to fetch user prompts: %w", err)
		}
		defer func() { _ = rows.Close() }()

		events, err = scanEventRows(rows)
		return err
	})
	if err != nil {
		return nil, err
	}

	return events, nil
}

// FetchPriorReasoning retrieves the most recent reasoning events for a project.
func FetchPriorReasoning(db *sql.DB, projectID string, limit int) ([]*models.Event, error) {
	if limit <= 0 {
		limit = 10
	}

	var events []*models.Event
	err := RetryWithBackoff(context.Background(), func() error {
		var query string
		var args []any

		if projectID != "" {
			query = `
				SELECT id, kind, agent_name, project_id, task_id, message, metadata, created_at
				FROM events
				WHERE kind = 'reasoning' AND archived_at IS NULL
				  AND ` + ProjectOrGlobalScopeClause + `
				ORDER BY id DESC LIMIT ?
			`
			args = []any{projectID, limit}
		} else {
			query = `
				SELECT id, kind, agent_name, project_id, task_id, message, metadata, created_at
				FROM events
				WHERE kind = 'reasoning' AND archived_at IS NULL
				ORDER BY id DESC LIMIT ?
			`
			args = []any{limit}
		}

		rows, err := db.QueryContext(context.Background(), query, args...)
		if err != nil {
			return fmt.Errorf("failed to fetch prior reasoning: %w", err)
		}
		defer func() { _ = rows.Close() }()

		events, err = scanEventRows(rows)
		return err
	})
	if err != nil {
		return nil, err
	}

	return events, nil
}

func fetchRecentEvents(db *sql.DB, taskID string) ([]*models.Event, error) {
	var events []*models.Event
	err := RetryWithBackoff(context.Background(), func() error {
		rows, err := db.QueryContext(context.Background(), `
			SELECT id, kind, agent_name, project_id, task_id, message, metadata, created_at
			FROM events
			WHERE task_id = ? AND archived_at IS NULL
			ORDER BY id DESC
			LIMIT 20
		`, taskID)
		if err != nil {
			return fmt.Errorf("failed to query events: %w", err)
		}
		defer func() { _ = rows.Close() }()

		events, err = scanEventRows(rows)
		return err
	})
	if err != nil {
		return nil, err
	}

	return events, nil
}
