package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/dotcommander/vybe/internal/models"
)

// SetMemory creates or updates a memory entry with type inference and scoping.
// Uses UPSERT on conflict to handle updates.
//
//nolint:revive // argument-limit: all memory params (key, value, type, scope, scope_id, expires, pinned) are required and distinct
func SetMemory(db *sql.DB, key, value, valueType, scope, scopeID string, expiresAt *time.Time, pinned bool) error {
	if err := validateValueType(valueType); err != nil {
		return err
	}
	if valueType == "" {
		valueType = inferValueType(value)
	}
	if err := validateScope(scope, scopeID); err != nil {
		return err
	}
	return RetryWithBackoff(context.Background(), func() error {
		_, err := db.ExecContext(context.Background(), `
			INSERT INTO memory (key, value, value_type, scope, scope_id, expires_at, pinned, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP)
			ON CONFLICT(scope, scope_id, key) DO UPDATE SET
				value = excluded.value,
				value_type = excluded.value_type,
				expires_at = excluded.expires_at,
				pinned = CASE WHEN excluded.pinned = 1 THEN 1 ELSE pinned END,
				updated_at = CURRENT_TIMESTAMP
				-- access_count and last_accessed_at intentionally preserved on conflict
				-- pinned: upsert can only pin, never unpin; use PinMemoryTx(pin=false) to unpin
		`, key, value, valueType, scope, scopeID, expiresAt, pinned)
		if err != nil {
			return fmt.Errorf("failed to set memory: %w", err)
		}
		return nil
	})
}

// UpsertMemoryTx performs memory upsert within an existing transaction.
// Returns (eventID, error).
// Exported for use by batch operations (e.g., push).
func UpsertMemoryTx(tx *sql.Tx, agentName, key, value, valueType, scope, scopeID string, expiresAt *time.Time, pinned bool) (int64, error) {
	if key == "" {
		return 0, errors.New("memory key is required")
	}
	if err := validateValueType(valueType); err != nil {
		return 0, err
	}
	if valueType == "" {
		valueType = inferValueType(value)
	}
	if err := validateScope(scope, scopeID); err != nil {
		return 0, err
	}

	taskID := ""
	if scope == string(models.MemoryScopeTask) {
		taskID = scopeID
	}

	// Check for value conflict before upsert
	var oldValue sql.NullString
	_ = tx.QueryRowContext(context.Background(),
		`SELECT value FROM memory WHERE scope = ? AND scope_id = ? AND key = ? AND (pinned = 1 OR expires_at IS NULL OR expires_at > CURRENT_TIMESTAMP)`,
		scope, scopeID, key,
	).Scan(&oldValue)

	if oldValue.Valid && oldValue.String != value {
		// Emit conflict event — best effort, don't fail the upsert
		oldVal := truncateRunes(oldValue.String, 512)
		newVal := truncateRunes(value, 512)
		conflictMeta, _ := json.Marshal(map[string]string{
			"key":       key,
			"scope":     scope,
			"scope_id":  scopeID,
			"old_value": oldVal,
			"new_value": newVal,
		})
		_, _ = InsertEventTx(tx, models.EventKindMemoryConflict, agentName, taskID, fmt.Sprintf("Memory conflict: %s", key), string(conflictMeta))
	}

	_, err := tx.ExecContext(context.Background(), `
		INSERT INTO memory (key, value, value_type, scope, scope_id, expires_at, pinned, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP)
		ON CONFLICT(scope, scope_id, key) DO UPDATE SET
			value = excluded.value,
			value_type = excluded.value_type,
			expires_at = excluded.expires_at,
			-- Pin sticky: upsert can only pin, never unpin; use PinMemoryTx(pin=false) to unpin.
			pinned = CASE WHEN excluded.pinned = 1 THEN 1 ELSE pinned END,
			updated_at = CURRENT_TIMESTAMP
	`, key, value, valueType, scope, scopeID, expiresAt, pinned)
	if err != nil {
		return 0, fmt.Errorf("failed to upsert memory: %w", err)
	}

	meta := struct {
		Key       string     `json:"key"`
		ValueType string     `json:"value_type"`
		Scope     string     `json:"scope"`
		ScopeID   string     `json:"scope_id,omitempty"`
		ExpiresAt *time.Time `json:"expires_at,omitempty"`
		Pinned    bool       `json:"pinned"`
	}{Key: key, ValueType: valueType, Scope: scope, ScopeID: scopeID, ExpiresAt: expiresAt, Pinned: pinned}
	metaBytes, _ := json.Marshal(meta)

	eventID, err := InsertEventTx(tx, models.EventKindMemoryUpserted, agentName, taskID, fmt.Sprintf("Memory upserted: %s", key), string(metaBytes))
	if err != nil {
		return 0, fmt.Errorf("failed to append event: %w", err)
	}
	return eventID, nil
}

// UpsertMemoryWithEventIdempotent performs memory upsert once per (agent_name, request_id).
func UpsertMemoryWithEventIdempotent(db *sql.DB, agentName, requestID, key, value, valueType, scope, scopeID string, expiresAt *time.Time, pinned bool) (int64, error) {
	type idemResult struct {
		EventID int64 `json:"event_id"`
	}
	r, err := RunIdempotent(context.Background(), db, agentName, requestID, "memory.upsert", func(tx *sql.Tx) (idemResult, error) {
		eid, txErr := UpsertMemoryTx(tx, agentName, key, value, valueType, scope, scopeID, expiresAt, pinned)
		if txErr != nil {
			return idemResult{}, txErr
		}
		return idemResult{EventID: eid}, nil
	})
	if err != nil {
		return 0, err
	}
	return r.EventID, nil
}

// GetMemory retrieves a memory entry by key, scope, and scope_id.
// Returns nil if not found or expired.
func GetMemory(db *sql.DB, key, scope, scopeID string) (*models.Memory, error) {
	if err := validateScope(scope, scopeID); err != nil {
		return nil, err
	}
	var mem models.Memory
	err := RetryWithBackoff(context.Background(), func() error {
		return db.QueryRowContext(context.Background(), `
			SELECT id, key, value, value_type, scope, scope_id, expires_at, updated_at, created_at, access_count, last_accessed_at, pinned
			FROM memory
			WHERE key = ? AND scope = ? AND scope_id = ?
			AND (pinned = 1 OR expires_at IS NULL OR expires_at > CURRENT_TIMESTAMP)
		`, key, scope, scopeID).Scan(
			&mem.ID, &mem.Key, &mem.Value, &mem.ValueType, &mem.Scope, &mem.ScopeID,
			&mem.ExpiresAt, &mem.UpdatedAt, &mem.CreatedAt, &mem.AccessCount, &mem.LastAccessedAt, &mem.Pinned,
		)
	})
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get memory: %w", err)
	}

	// Best-effort access tracking — don't fail the read if the update fails.
	if _, err := db.ExecContext(context.Background(),
		`UPDATE memory SET access_count = access_count + 1, last_accessed_at = CURRENT_TIMESTAMP WHERE id = ?`,
		mem.ID,
	); err != nil {
		slog.Warn("failed to update memory access count", "key", key, "error", err)
	}

	return &mem, nil
}

// ListMemory retrieves all active memory entries for a scope and scope_id, ordered by updated_at DESC.
func ListMemory(db *sql.DB, scope, scopeID string) ([]*models.Memory, error) {
	if err := validateScope(scope, scopeID); err != nil {
		return nil, err
	}
	var memories []*models.Memory
	err := RetryWithBackoff(context.Background(), func() error {
		rows, err := db.QueryContext(context.Background(), `
			SELECT id, key, value, value_type, scope, scope_id, expires_at, updated_at, created_at, access_count, last_accessed_at, pinned
			FROM memory
			WHERE scope = ? AND scope_id = ?
			AND (pinned = 1 OR expires_at IS NULL OR expires_at > CURRENT_TIMESTAMP)
			ORDER BY updated_at DESC
		`, scope, scopeID)
		if err != nil {
			return fmt.Errorf("failed to list memory: %w", err)
		}
		defer func() { _ = rows.Close() }()
		memories = make([]*models.Memory, 0)
		for rows.Next() {
			var mem models.Memory
			if err := rows.Scan(&mem.ID, &mem.Key, &mem.Value, &mem.ValueType, &mem.Scope, &mem.ScopeID, &mem.ExpiresAt, &mem.UpdatedAt, &mem.CreatedAt, &mem.AccessCount, &mem.LastAccessedAt, &mem.Pinned); err != nil {
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
