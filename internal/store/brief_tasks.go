package store

import (
	"context"
	"database/sql"
	"fmt"
)

// GetTaskStatusCounts returns task status aggregation, optionally scoped to a project.
func GetTaskStatusCounts(db *sql.DB, projectID string) (*TaskStatusCounts, error) {
	counts := &TaskStatusCounts{}
	err := RetryWithBackoff(context.Background(), func() error {
		query, args := scopedQuery(`
				SELECT
					COALESCE(SUM(CASE WHEN status = 'pending' THEN 1 ELSE 0 END), 0),
					COALESCE(SUM(CASE WHEN status = 'in_progress' THEN 1 ELSE 0 END), 0),
					COALESCE(SUM(CASE WHEN status = 'completed' THEN 1 ELSE 0 END), 0),
					COALESCE(SUM(CASE WHEN status = 'blocked' THEN 1 ELSE 0 END), 0)
				FROM tasks`, projectID)

		return db.QueryRowContext(context.Background(), query, args...).Scan(
			&counts.Pending,
			&counts.InProgress,
			&counts.Completed,
			&counts.Blocked,
		)
	})
	if err != nil {
		return nil, fmt.Errorf("failed to get task status counts: %w", err)
	}

	return counts, nil
}

// FetchPipelineTasks returns the next pending tasks in queue order, excluding the current focus task.
func FetchPipelineTasks(db *sql.DB, excludeTaskID, _ /*agentName*/, projectID string, limit int) ([]PipelineTask, error) {
	if limit <= 0 {
		limit = 5
	}

	var tasks []PipelineTask
	err := RetryWithBackoff(context.Background(), func() error {
		query := `
			SELECT id, title, priority FROM tasks
			WHERE status = 'pending'
			  AND id != ?
		`
		args := []any{excludeTaskID}
		if projectID != "" {
			query += andProjectIDFilter
			args = append(args, projectID)
		}
		query += " ORDER BY priority DESC, created_at ASC LIMIT ?"
		args = append(args, limit)

		rows, err := db.QueryContext(context.Background(), query, args...)
		if err != nil {
			return fmt.Errorf("failed to query pipeline tasks: %w", err)
		}
		defer func() { _ = rows.Close() }()

		tasks = make([]PipelineTask, 0, limit)
		for rows.Next() {
			var pt PipelineTask
			if err := rows.Scan(&pt.ID, &pt.Title, &pt.Priority); err != nil {
				return fmt.Errorf("failed to scan pipeline task: %w", err)
			}
			tasks = append(tasks, pt)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, err
	}

	return tasks, nil
}
