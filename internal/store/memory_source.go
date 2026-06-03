package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/dotcommander/vybe/internal/models"
)

// ListMemoryBySource returns memories whose provenance matches the given filters.
// A filter is "active" when its argument is non-empty: pass sourceEventID != nil to
// filter by source_event_id, sourceTaskID != "" to filter by source_task_id. When
// both are active they are AND-combined. At least one filter must be active.
// Results are ordered updated_at DESC. Expired/unpinned rows are excluded, matching ListMemory.
func ListMemoryBySource(db *sql.DB, sourceEventID *int64, sourceTaskID string) ([]*models.Memory, error) {
	if sourceEventID == nil && sourceTaskID == "" {
		return nil, errors.New("ListMemoryBySource requires at least one of sourceEventID or sourceTaskID")
	}
	var memories []*models.Memory
	err := RetryWithBackoff(context.Background(), func() error {
		query := `SELECT id, key, value, value_type, scope, scope_id, expires_at, updated_at, created_at, access_count, last_accessed_at, pinned, kind, half_life_days, source_event_id, source_task_id
			FROM memory
			WHERE (pinned = 1 OR expires_at IS NULL OR expires_at > CURRENT_TIMESTAMP)`
		var args []any
		if sourceEventID != nil {
			query += ` AND source_event_id = ?`
			args = append(args, *sourceEventID)
		}
		if sourceTaskID != "" {
			query += ` AND source_task_id = ?`
			args = append(args, sourceTaskID)
		}
		query += ` ORDER BY updated_at DESC`
		rows, err := db.QueryContext(context.Background(), query, args...)
		if err != nil {
			return fmt.Errorf("failed to list memory by source: %w", err)
		}
		defer func() { _ = rows.Close() }()
		memories = make([]*models.Memory, 0)
		for rows.Next() {
			var mem models.Memory
			var sourceTaskNull sql.NullString
			if err := rows.Scan(&mem.ID, &mem.Key, &mem.Value, &mem.ValueType, &mem.Scope, &mem.ScopeID, &mem.ExpiresAt, &mem.UpdatedAt, &mem.CreatedAt, &mem.AccessCount, &mem.LastAccessedAt, &mem.Pinned, &mem.Kind, &mem.HalfLifeDays, &mem.SourceEventID, &sourceTaskNull); err != nil {
				return fmt.Errorf("failed to scan memory: %w", err)
			}
			mem.SourceTaskID = sourceTaskNull.String
			memories = append(memories, &mem)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, err
	}
	return memories, nil
}
