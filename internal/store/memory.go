package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/dotcommander/vybe/internal/models"
)

// SetMemory creates or updates a memory entry with type inference and scoping.
// Uses UPSERT on conflict to handle updates.
//
//nolint:revive // argument-limit: all memory params (key, value, type, scope, scope_id, expires) are required and distinct
func SetMemory(db *sql.DB, key, value, valueType, scope, scopeID string, expiresAt *time.Time) error {
	if err := validateValueType(valueType); err != nil {
		return err
	}
	if valueType == "" {
		valueType = inferValueType(value)
	}
	if err := validateScope(scope, scopeID); err != nil {
		return err
	}
	return RetryWithBackoff(func() error {
		_, err := db.ExecContext(context.Background(), `
			INSERT INTO memory (key, value, value_type, scope, scope_id, expires_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP)
			ON CONFLICT(scope, scope_id, key) DO UPDATE SET
				value = excluded.value,
				value_type = excluded.value_type,
				expires_at = excluded.expires_at,
				updated_at = CURRENT_TIMESTAMP
		`, key, value, valueType, scope, scopeID, expiresAt)
		if err != nil {
			return fmt.Errorf("failed to set memory: %w", err)
		}
		return nil
	})
}

// UpsertMemoryTx performs memory upsert within an existing transaction.
// Returns (eventID, error).
// Exported for use by batch operations (e.g., push).
func UpsertMemoryTx(tx *sql.Tx, agentName, key, value, valueType, scope, scopeID string, expiresAt *time.Time) (int64, error) {
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

	_, err := tx.ExecContext(context.Background(), `
		INSERT INTO memory (key, value, value_type, scope, scope_id, expires_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP)
		ON CONFLICT(scope, scope_id, key) DO UPDATE SET
			value = excluded.value,
			value_type = excluded.value_type,
			expires_at = excluded.expires_at,
			updated_at = CURRENT_TIMESTAMP
	`, key, value, valueType, scope, scopeID, expiresAt)
	if err != nil {
		return 0, fmt.Errorf("failed to upsert memory: %w", err)
	}

	meta := struct {
		Key       string     `json:"key"`
		ValueType string     `json:"value_type"`
		Scope     string     `json:"scope"`
		ScopeID   string     `json:"scope_id,omitempty"`
		ExpiresAt *time.Time `json:"expires_at,omitempty"`
	}{Key: key, ValueType: valueType, Scope: scope, ScopeID: scopeID, ExpiresAt: expiresAt}
	metaBytes, _ := json.Marshal(meta)

	eventID, err := InsertEventTx(tx, models.EventKindMemoryUpserted, agentName, taskID, fmt.Sprintf("Memory upserted: %s", key), string(metaBytes))
	if err != nil {
		return 0, fmt.Errorf("failed to append event: %w", err)
	}
	return eventID, nil
}

// UpsertMemoryWithEventIdempotent performs memory upsert once per (agent_name, request_id).
func UpsertMemoryWithEventIdempotent(db *sql.DB, agentName, requestID, key, value, valueType, scope, scopeID string, expiresAt *time.Time) (int64, error) {
	type idemResult struct {
		EventID int64 `json:"event_id"`
	}
	r, err := RunIdempotent(db, agentName, requestID, "memory.upsert", func(tx *sql.Tx) (idemResult, error) {
		eid, txErr := UpsertMemoryTx(tx, agentName, key, value, valueType, scope, scopeID, expiresAt)
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

// GCMemoryWithEventIdempotent removes expired memory entries, emitting a gc event.
// Idempotent per (agentName, requestID).
func GCMemoryWithEventIdempotent(db *sql.DB, agentName, requestID string, limit int) (int64, int, error) {
	if limit <= 0 {
		limit = 100
	}

	type idemResult struct {
		EventID int64 `json:"event_id"`
		Deleted int   `json:"deleted"`
	}

	r, err := RunIdempotent(db, agentName, requestID, "memory.gc", func(tx *sql.Tx) (idemResult, error) {
		result, err := tx.ExecContext(context.Background(), `
			DELETE FROM memory WHERE id IN (
				SELECT id FROM memory WHERE expires_at IS NOT NULL AND expires_at <= CURRENT_TIMESTAMP LIMIT ?
			)
		`, limit)
		if err != nil {
			return idemResult{}, fmt.Errorf("failed to gc memory: %w", err)
		}
		deleted, _ := result.RowsAffected()

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

// GetMemory retrieves a memory entry by key, scope, and scope_id.
// Returns nil if not found or expired.
func GetMemory(db *sql.DB, key, scope, scopeID string) (*models.Memory, error) {
	if err := validateScope(scope, scopeID); err != nil {
		return nil, err
	}
	var mem models.Memory
	err := RetryWithBackoff(func() error {
		return db.QueryRowContext(context.Background(), `
			SELECT id, key, value, value_type, scope, scope_id, expires_at, updated_at, created_at
			FROM memory
			WHERE key = ? AND scope = ? AND scope_id = ?
			AND (expires_at IS NULL OR expires_at > CURRENT_TIMESTAMP)
		`, key, scope, scopeID).Scan(
			&mem.ID, &mem.Key, &mem.Value, &mem.ValueType, &mem.Scope, &mem.ScopeID,
			&mem.ExpiresAt, &mem.UpdatedAt, &mem.CreatedAt,
		)
	})
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get memory: %w", err)
	}
	return &mem, nil
}

// ListMemory retrieves all active memory entries for a scope and scope_id, ordered by updated_at DESC.
func ListMemory(db *sql.DB, scope, scopeID string) ([]*models.Memory, error) {
	if err := validateScope(scope, scopeID); err != nil {
		return nil, err
	}
	var memories []*models.Memory
	err := RetryWithBackoff(func() error {
		rows, err := db.QueryContext(context.Background(), `
			SELECT id, key, value, value_type, scope, scope_id, expires_at, updated_at, created_at
			FROM memory
			WHERE scope = ? AND scope_id = ?
			AND (expires_at IS NULL OR expires_at > CURRENT_TIMESTAMP)
			ORDER BY updated_at DESC
		`, scope, scopeID)
		if err != nil {
			return fmt.Errorf("failed to list memory: %w", err)
		}
		defer func() { _ = rows.Close() }()
		memories = make([]*models.Memory, 0)
		for rows.Next() {
			var mem models.Memory
			if err := rows.Scan(&mem.ID, &mem.Key, &mem.Value, &mem.ValueType, &mem.Scope, &mem.ScopeID, &mem.ExpiresAt, &mem.UpdatedAt, &mem.CreatedAt); err != nil {
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

// QueryMemory searches memory entries by key LIKE pattern, ordered by updated_at DESC.
func QueryMemory(db *sql.DB, scope, scopeID, pattern string, limit int) ([]*models.Memory, error) {
	if err := validateScope(scope, scopeID); err != nil {
		return nil, err
	}
	var memories []*models.Memory
	err := RetryWithBackoff(func() error {
		rows, err := db.QueryContext(context.Background(), `
			SELECT id, key, value, value_type, scope, scope_id, expires_at, updated_at, created_at
			FROM memory
			WHERE scope = ? AND scope_id = ?
			AND (expires_at IS NULL OR expires_at > CURRENT_TIMESTAMP)
			AND key LIKE ?
			ORDER BY updated_at DESC
			LIMIT ?
		`, scope, scopeID, pattern, limit)
		if err != nil {
			return fmt.Errorf("failed to query memory: %w", err)
		}
		defer func() { _ = rows.Close() }()
		memories = make([]*models.Memory, 0)
		for rows.Next() {
			var mem models.Memory
			if err := rows.Scan(&mem.ID, &mem.Key, &mem.Value, &mem.ValueType, &mem.Scope, &mem.ScopeID, &mem.ExpiresAt, &mem.UpdatedAt, &mem.CreatedAt); err != nil {
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

// DeleteMemoryWithEvent deletes memory and appends an event in the same transaction.
// Returns the event ID.
func DeleteMemoryWithEvent(db *sql.DB, agentName, key, scope, scopeID string) (int64, error) {
	if err := validateScope(scope, scopeID); err != nil {
		return 0, err
	}

	taskID := ""
	if scope == "task" {
		taskID = scopeID
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
	metaBytes, err := json.Marshal(meta)
	if err != nil {
		return 0, fmt.Errorf("failed to marshal memory delete event metadata: %w", err)
	}

	var eventID int64
	err = Transact(db, func(tx *sql.Tx) error {
		result, txErr := tx.ExecContext(context.Background(), `DELETE FROM memory WHERE key = ? AND scope = ? AND scope_id = ?`, key, scope, scopeID)
		if txErr != nil {
			return fmt.Errorf("failed to delete memory: %w", txErr)
		}

		rowsAffected, txErr := result.RowsAffected()
		if txErr != nil {
			return fmt.Errorf("failed to get rows affected: %w", txErr)
		}
		if rowsAffected == 0 {
			return errors.New("memory entry not found")
		}

		id, txErr := InsertEventTx(tx, models.EventKindMemoryDelete, agentName, taskID, fmt.Sprintf("Memory deleted: %s", key), string(metaBytes))
		if txErr != nil {
			return fmt.Errorf("failed to append event: %w", txErr)
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
func DeleteMemoryWithEventIdempotent(db *sql.DB, agentName, requestID, key, scope, scopeID string) (int64, error) {
	if err := validateScope(scope, scopeID); err != nil {
		return 0, err
	}

	taskID := ""
	if scope == "task" {
		taskID = scopeID
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
	metaBytes, err := json.Marshal(meta)
	if err != nil {
		return 0, fmt.Errorf("failed to marshal memory delete event metadata: %w", err)
	}

	type idemResult struct {
		EventID int64 `json:"event_id"`
	}
	r, err := RunIdempotent(db, agentName, requestID, "memory.delete", func(tx *sql.Tx) (idemResult, error) {
		result, txErr := tx.ExecContext(context.Background(), `DELETE FROM memory WHERE key = ? AND scope = ? AND scope_id = ?`, key, scope, scopeID)
		if txErr != nil {
			return idemResult{}, fmt.Errorf("failed to delete memory: %w", txErr)
		}

		rowsAffected, txErr := result.RowsAffected()
		if txErr != nil {
			return idemResult{}, fmt.Errorf("failed to get rows affected: %w", txErr)
		}
		if rowsAffected == 0 {
			return idemResult{}, errors.New("memory entry not found")
		}

		eventID, txErr := InsertEventTx(tx, models.EventKindMemoryDelete, agentName, taskID, fmt.Sprintf("Memory deleted: %s", key), string(metaBytes))
		if txErr != nil {
			return idemResult{}, fmt.Errorf("failed to append event: %w", txErr)
		}

		return idemResult{EventID: eventID}, nil
	})
	if err != nil {
		return 0, err
	}
	return r.EventID, nil
}

// inferValueType attempts to detect the value type from the input string.
func inferValueType(value string) string {
	value = strings.TrimSpace(value)

	// Check for boolean
	if value == "true" || value == "false" {
		return "boolean"
	}

	// Check for number
	if _, err := strconv.ParseFloat(value, 64); err == nil {
		return "number"
	}

	// Check for JSON object or array
	if (strings.HasPrefix(value, "{") && strings.HasSuffix(value, "}")) ||
		(strings.HasPrefix(value, "[") && strings.HasSuffix(value, "]")) {
		// Try to parse as JSON
		var js any
		if err := json.Unmarshal([]byte(value), &js); err == nil {
			switch js.(type) {
			case []any:
				return "array"
			case map[string]any:
				return "json"
			}
		}
	}

	// Default to string
	return "string"
}

// isValidValueType reports whether vt is an allowed memory value type.
func isValidValueType(vt string) bool {
	switch vt {
	case "string", "number", "boolean", "json", "array":
		return true
	}
	return false
}

// validateValueType checks that an explicit value type is in the allowed set.
// Empty string is allowed (triggers inference).
func validateValueType(valueType string) error {
	if valueType == "" {
		return nil
	}
	if !isValidValueType(valueType) {
		return fmt.Errorf("invalid value_type: %q (must be one of: string, number, boolean, json, array)", valueType)
	}
	return nil
}

// validateScope ensures scope and scope_id are valid.
func validateScope(scope, scopeID string) error {
	switch scope {
	case "global", "project", "task", "agent":
		// valid
	default:
		return fmt.Errorf("invalid scope: %s (must be one of: global, project, task, agent)", scope)
	}

	// Global scope should not have a scope_id
	if scope == "global" && scopeID != "" {
		return errors.New("global scope cannot have a scope_id")
	}

	// Non-global scopes require a scope_id
	if scope != "global" && scopeID == "" {
		return fmt.Errorf("%s scope requires a scope_id", scope)
	}

	return nil
}
