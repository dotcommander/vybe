package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/dotcommander/vybe/internal/models"
)

// GCMemoryWithEventIdempotent removes expired memory entries, emitting a gc event.
// Pinned entries are never deleted regardless of expires_at.
// Idempotent per (agentName, requestID).
func GCMemoryWithEventIdempotent(db *sql.DB, agentName, requestID string, limit int) (int64, int, error) {
	if limit <= 0 {
		limit = 100
	}

	type idemResult struct {
		EventID int64 `json:"event_id"`
		Deleted int   `json:"deleted"`
	}

	r, err := RunIdempotent(context.Background(), db, agentName, requestID, "memory.gc", func(tx *sql.Tx) (idemResult, error) {
		result, err := tx.ExecContext(context.Background(), `
			DELETE FROM memory WHERE id IN (
				SELECT id FROM memory
				WHERE pinned = 0
				AND expires_at IS NOT NULL AND expires_at <= CURRENT_TIMESTAMP
				LIMIT ?
			)
		`, limit)
		if err != nil {
			return idemResult{}, fmt.Errorf("failed to gc memory: %w", err)
		}
		deleted, err := result.RowsAffected()
		if err != nil {
			return idemResult{}, fmt.Errorf("failed to check rows affected: %w", err)
		}

		meta, _ := json.Marshal(map[string]any{"deleted": deleted, "limit": limit})
		eventID, err := InsertEventTx(tx, models.EventKindMemoryGC, agentName, "", fmt.Sprintf("Memory GC deleted %d rows", deleted), string(meta))
		if err != nil {
			return idemResult{}, fmt.Errorf("failed to append memory_gc event: %w", err)
		}
		return idemResult{EventID: eventID, Deleted: int(deleted)}, nil
	})
	if err != nil {
		return 0, 0, err
	}
	return r.EventID, r.Deleted, nil
}

// DeleteMemoryTx deletes a memory entry and appends an event within an existing transaction.
// Returns (eventID, found, error). found is false when no row matched the key/scope/scopeID.
func DeleteMemoryTx(ctx context.Context, tx *sql.Tx, agentName, key, scope, scopeID string) (int64, bool, error) {
	taskID := ""
	if scope == "task" {
		taskID = scopeID
	}

	result, err := tx.ExecContext(ctx, `DELETE FROM memory WHERE key = ? AND scope = ? AND scope_id = ?`, key, scope, scopeID)
	if err != nil {
		return 0, false, fmt.Errorf("failed to delete memory: %w", err)
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return 0, false, fmt.Errorf("failed to get rows affected: %w", err)
	}
	if rowsAffected == 0 {
		return 0, false, nil
	}

	meta := struct {
		Key     string `json:"key"`
		Scope   string `json:"scope"`
		ScopeID string `json:"scope_id,omitempty"`
	}{
		Key:     key,
		Scope:   scope,
		ScopeID: scopeID,
	}
	metaBytes, _ := json.Marshal(meta)

	eventID, err := InsertEventTx(tx, models.EventKindMemoryDelete, agentName, taskID, fmt.Sprintf("Memory deleted: %s", key), string(metaBytes))
	if err != nil {
		return 0, false, fmt.Errorf("failed to append event: %w", err)
	}
	return eventID, true, nil
}

// DeleteMemoryWithEvent deletes memory and appends an event in the same transaction.
// Returns the event ID.
func DeleteMemoryWithEvent(ctx context.Context, db *sql.DB, agentName, key, scope, scopeID string) (int64, error) {
	if err := validateScope(scope, scopeID); err != nil {
		return 0, err
	}

	var eventID int64
	err := Transact(ctx, db, func(tx *sql.Tx) error {
		id, found, txErr := DeleteMemoryTx(ctx, tx, agentName, key, scope, scopeID)
		if txErr != nil {
			return txErr
		}
		if !found {
			return errors.New("memory entry not found")
		}
		eventID = id
		return nil
	})
	if err != nil {
		return 0, err
	}
	return eventID, nil
}

// DeleteMemoryWithEventIdempotent performs DeleteMemoryWithEvent once per (agent_name, request_id).
// On retries with the same request id, returns the originally created event id.
//
//nolint:revive // argument-limit: all params (agent, req, key, scope, scope_id) are required
func DeleteMemoryWithEventIdempotent(ctx context.Context, db *sql.DB, agentName, requestID, key, scope, scopeID string) (int64, error) {
	if err := validateScope(scope, scopeID); err != nil {
		return 0, err
	}

	type idemResult struct {
		EventID int64 `json:"event_id"`
	}
	r, err := RunIdempotent(ctx, db, agentName, requestID, "memory.delete", func(tx *sql.Tx) (idemResult, error) {
		eventID, found, txErr := DeleteMemoryTx(ctx, tx, agentName, key, scope, scopeID)
		if txErr != nil {
			return idemResult{}, txErr
		}
		if !found {
			return idemResult{}, fmt.Errorf("memory key not found: %s (scope=%s, scope_id=%s)", key, scope, scopeID)
		}
		return idemResult{EventID: eventID}, nil
	})
	if err != nil {
		return 0, err
	}
	return r.EventID, nil
}
