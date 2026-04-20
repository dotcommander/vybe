package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"

	"github.com/dotcommander/vybe/internal/models"
)

// PinMemoryTx sets or clears the pinned flag on an existing memory entry within a tx.
// Returns (eventID, found, error). found is false when no row matched the key/scope/scopeID.
func PinMemoryTx(ctx context.Context, tx *sql.Tx, agentName, key, scope, scopeID string, pin bool) (int64, bool, error) {
	taskID := ""
	if scope == "task" {
		taskID = scopeID
	}
	result, err := tx.ExecContext(ctx,
		`UPDATE memory SET pinned = ?, updated_at = CURRENT_TIMESTAMP WHERE key = ? AND scope = ? AND scope_id = ?`,
		boolToInt(pin), key, scope, scopeID,
	)
	if err != nil {
		return 0, false, fmt.Errorf("failed to pin memory: %w", err)
	}
	n, err := result.RowsAffected()
	if err != nil {
		return 0, false, fmt.Errorf("failed to check rows affected: %w", err)
	}
	if n == 0 {
		return 0, false, nil
	}
	action := "pinned"
	if !pin {
		action = "unpinned"
	}
	meta, err := json.Marshal(map[string]any{"key": key, "scope": scope, "scope_id": scopeID, "pinned": pin})
	if err != nil {
		return 0, false, fmt.Errorf("failed to marshal event metadata: %w", err)
	}
	eid, err := InsertEventTx(tx, models.EventKindMemoryPin, agentName, taskID,
		fmt.Sprintf("Memory %s: %s", action, key), string(meta))
	if err != nil {
		return 0, false, fmt.Errorf("failed to append event: %w", err)
	}
	return eid, true, nil
}

// PinMemoryIdempotent sets or clears the pinned flag once per (agentName, requestID).
func PinMemoryIdempotent(ctx context.Context, db *sql.DB, agentName, requestID, key, scope, scopeID string, pin bool) (int64, error) {
	if err := validateScope(scope, scopeID); err != nil {
		return 0, err
	}
	type idemResult struct {
		EventID int64 `json:"event_id"`
	}
	r, err := RunIdempotent(ctx, db, agentName, requestID, "memory.pin", func(tx *sql.Tx) (idemResult, error) {
		eid, found, txErr := PinMemoryTx(ctx, tx, agentName, key, scope, scopeID, pin)
		if txErr != nil {
			return idemResult{}, txErr
		}
		if !found {
			return idemResult{}, fmt.Errorf("memory key not found: %s (scope=%s, scope_id=%s)", key, scope, scopeID)
		}
		return idemResult{EventID: eid}, nil
	})
	if err != nil {
		return 0, err
	}
	return r.EventID, nil
}
