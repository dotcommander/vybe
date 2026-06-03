package store

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/dotcommander/vybe/internal/models"
)

// fetchRelevantMemory retrieves memory relevant to a task and/or project, ordered by pinned DESC, updated_at DESC.
// asOf controls the expiry cutoff; callers must pass a non-zero time.
func fetchRelevantMemory(db *sql.DB, taskID, projectID string, asOf time.Time) ([]*models.Memory, error) {
	asOfStr := asOf.UTC().Format("2006-01-02 15:04:05")

	var memories []*models.Memory

	err := RetryWithBackoff(context.Background(), func() error {
		var query string
		var args []any

		if projectID != "" {
			query = `
				SELECT id, key, value, value_type, scope, scope_id, expires_at, updated_at, created_at, pinned, kind
				FROM memory
				WHERE (
					scope = 'global'
					OR (scope = 'task' AND scope_id = ?)
					OR (scope = 'project' AND scope_id = ?)
				)
				AND (pinned = 1 OR expires_at IS NULL OR expires_at > ?)
				ORDER BY pinned DESC, updated_at DESC
				LIMIT 50
			`
			args = []any{taskID, projectID, asOfStr}
		} else {
			query = `
				SELECT id, key, value, value_type, scope, scope_id, expires_at, updated_at, created_at, pinned, kind
				FROM memory
				WHERE (
					scope = 'global'
					OR (scope = 'task' AND scope_id = ?)
					OR scope = 'project'
				)
				AND (pinned = 1 OR expires_at IS NULL OR expires_at > ?)
				ORDER BY pinned DESC, updated_at DESC
				LIMIT 50
			`
			args = []any{taskID, asOfStr}
		}

		rows, err := db.QueryContext(context.Background(), query, args...)
		if err != nil {
			return fmt.Errorf("failed to query memory: %w", err)
		}
		defer func() { _ = rows.Close() }()

		memories = make([]*models.Memory, 0, memoryBriefLimit)
		for rows.Next() {
			var mem models.Memory
			if err := rows.Scan(
				&mem.ID, &mem.Key, &mem.Value, &mem.ValueType, &mem.Scope, &mem.ScopeID,
				&mem.ExpiresAt, &mem.UpdatedAt, &mem.CreatedAt, &mem.Pinned, &mem.Kind,
			); err != nil {
				return fmt.Errorf("failed to scan memory: %w", err)
			}
			memories = append(memories, &mem)
		}

		return rows.Err()
	})
	if err != nil {
		return nil, err
	}

	return memories, nil
}
