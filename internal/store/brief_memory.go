package store

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/dotcommander/vybe/internal/models"
)

// fetchRelevantMemory retrieves memory relevant to a task and/or project, ranked by ACT-R score.
// asOf controls both the decay clock and the expiry cutoff; callers must pass a non-zero time.
// modernc.org/sqlite does not convert time.Time for julianday(?); we format to a string here.
func fetchRelevantMemory(db *sql.DB, taskID, projectID string, asOf time.Time) ([]*models.Memory, error) {
	// Format as SQLite datetime string so julianday(?) returns a valid float rather than NULL.
	asOfStr := asOf.UTC().Format("2006-01-02 15:04:05")

	var memories []*models.Memory
	var ids []int64

	err := RetryWithBackoff(context.Background(), func() error {
		var query string
		var args []any

		// Half-life decay formula: relevance halves every half_life_days days.
		// Per-entry half_life_days overrides kind defaults (directive→∞, lesson→14d, fact→90d).
		// Pinned entries sort first; formula is tiebreaker only.
		// julianday(?) binds asOf — must be the FIRST positional arg in both branches.
		relevanceExpr := `(1.0 + access_count) / (1.0 + MAX(
  (julianday(?) - julianday(COALESCE(last_accessed_at, updated_at)))
  / COALESCE(
      NULLIF(half_life_days, 0),
      CASE kind
        WHEN 'directive' THEN 1e9
        WHEN 'lesson'    THEN 14.0
        ELSE                  90.0
      END
    ),
  0.0
)) AS relevance`

		if projectID != "" {
			// Placeholder order: ?1=asOf (decay), ?2=taskID, ?3=projectID, ?4=asOf (expiry)
			query = `
				SELECT id, key, value, value_type, scope, scope_id, expires_at, updated_at, created_at, access_count, last_accessed_at, pinned, kind, half_life_days, ` + relevanceExpr + `
				FROM memory
				WHERE (
					scope = 'global'
					OR (scope = 'task' AND scope_id = ?)
					OR (scope = 'project' AND scope_id = ?)
				)
				AND (pinned = 1 OR expires_at IS NULL OR expires_at > ?)
				ORDER BY pinned DESC, relevance DESC
				LIMIT 50
			`
			args = []any{asOfStr, taskID, projectID, asOfStr}
		} else {
			// Placeholder order: ?1=asOf (decay), ?2=taskID, ?3=asOf (expiry)
			query = `
				SELECT id, key, value, value_type, scope, scope_id, expires_at, updated_at, created_at, access_count, last_accessed_at, pinned, kind, half_life_days, ` + relevanceExpr + `
				FROM memory
				WHERE (
					scope = 'global'
					OR (scope = 'task' AND scope_id = ?)
					OR scope = 'project'
				)
				AND (pinned = 1 OR expires_at IS NULL OR expires_at > ?)
				ORDER BY pinned DESC, relevance DESC
				LIMIT 50
			`
			args = []any{asOfStr, taskID, asOfStr}
		}

		rows, err := db.QueryContext(context.Background(), query, args...)
		if err != nil {
			return fmt.Errorf("failed to query memory: %w", err)
		}
		defer func() { _ = rows.Close() }()

		memories = make([]*models.Memory, 0, memoryBriefLimit)
		ids = make([]int64, 0, memoryBriefLimit)
		for rows.Next() {
			var mem models.Memory
			if err := rows.Scan(
				&mem.ID, &mem.Key, &mem.Value, &mem.ValueType, &mem.Scope, &mem.ScopeID,
				&mem.ExpiresAt, &mem.UpdatedAt, &mem.CreatedAt, &mem.AccessCount, &mem.LastAccessedAt,
				&mem.Pinned, &mem.Kind, &mem.HalfLifeDays, &mem.Relevance,
			); err != nil {
				return fmt.Errorf("failed to scan memory: %w", err)
			}
			memories = append(memories, &mem)
			ids = append(ids, mem.ID)
		}

		return rows.Err()
	})
	if err != nil {
		return nil, err
	}

	if len(ids) > 0 {
		placeholders := strings.Repeat("?,", len(ids))
		placeholders = placeholders[:len(placeholders)-1]
		updateQuery := fmt.Sprintf(`UPDATE memory SET access_count = access_count + 1, last_accessed_at = ? WHERE id IN (%s)`, placeholders) //nolint:gosec // G201: placeholders are safe "?,?" repetitions
		args := make([]any, len(ids)+1)
		args[0] = asOfStr
		for i, id := range ids {
			args[i+1] = id
		}
		if _, err := db.ExecContext(context.Background(), updateQuery, args...); err != nil {
			slog.Warn("failed to update memory access counts", "error", err)
		}
	}

	return memories, nil
}
