package store

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
)

var ErrIdempotencyInProgress = errors.New("idempotency in progress")

// beginIdempotencyTx attempts to claim (agent_name, request_id). If it already exists,
// it returns the previously stored result_json for replay.
//
// This function is intentionally unexported. All callers must use RunIdempotent or
// RunIdempotentWithRetry, which enforce the begin+side-effects+complete-in-one-tx
// invariant. Direct usage risks leaving empty result_json rows on partial commits.
func beginIdempotencyTx(tx *sql.Tx, agentName, requestID, command string) (existingResultJSON string, alreadyDone bool, err error) {
	if agentName == "" {
		return "", false, fmt.Errorf("agent name is required")
	}
	if requestID == "" {
		return "", false, fmt.Errorf("request id is required")
	}
	if command == "" {
		return "", false, fmt.Errorf("idempotency command is required")
	}

	_, err = tx.Exec(`
		INSERT INTO idempotency (agent_name, request_id, command, result_json)
		VALUES (?, ?, ?, '')
	`, agentName, requestID, command)
	if err == nil {
		return "", false, nil
	}
	if !IsUniqueConstraintErr(err) {
		return "", false, fmt.Errorf("failed to insert idempotency row: %w", err)
	}

	var existingCommand string
	var resultJSON string
	if err := tx.QueryRow(`
		SELECT command, result_json
		FROM idempotency
		WHERE agent_name = ? AND request_id = ?
	`, agentName, requestID).Scan(&existingCommand, &resultJSON); err != nil {
		return "", false, fmt.Errorf("failed to load idempotency row: %w", err)
	}
	if existingCommand != command {
		return "", false, fmt.Errorf("idempotency key collision: request_id %q already used for command %q (new: %q)", requestID, existingCommand, command)
	}
	if strings.TrimSpace(resultJSON) == "" {
		// We should never see this if callers keep begin+work+complete in one tx,
		// but handle it defensively so concurrent workers can back off.
		return "", false, fmt.Errorf("%w: agent=%q request_id=%q command=%q", ErrIdempotencyInProgress, agentName, requestID, command)
	}
	return resultJSON, true, nil
}

func completeIdempotencyTx(tx *sql.Tx, agentName, requestID, resultJSON string) error {
	if resultJSON == "" {
		// Disallow empty: it's indistinguishable from "not completed" in logs/debugging.
		return fmt.Errorf("idempotency result json must be non-empty")
	}
	res, err := tx.Exec(`
		UPDATE idempotency
		SET result_json = ?
		WHERE agent_name = ? AND request_id = ?
	`, resultJSON, agentName, requestID)
	if err != nil {
		return fmt.Errorf("failed to update idempotency row: %w", err)
	}
	ra, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to check idempotency rows affected: %w", err)
	}
	if ra != 1 {
		return fmt.Errorf("idempotency row not found for agent=%q request_id=%q", agentName, requestID)
	}
	return nil
}

// IsUniqueConstraintErr checks for SQLite unique constraint violations.
// Exported for use by batch operations in actions layer.
//
// Detection relies on modernc.org/sqlite error message format (v1.45+):
//
//	"constraint failed: UNIQUE constraint failed: table.col (2067)"
//
// If modernc changes its error format in a major version bump, update
// the string match below.
func IsUniqueConstraintErr(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "UNIQUE constraint failed")
}
