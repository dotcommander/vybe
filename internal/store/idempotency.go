package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	sqlite "modernc.org/sqlite"
)

// ErrIdempotencyInProgress is returned when a request is still being processed by another agent.
var ErrIdempotencyInProgress = errors.New("idempotency in progress")

// beginIdempotencyTx attempts to claim (agent_name, request_id). If it already exists,
// it returns the previously stored result_json for replay.
//
// This function is intentionally unexported. All callers must use RunIdempotent or
// RunIdempotentWithRetry, which enforce the begin+side-effects+complete-in-one-tx
// invariant. Direct usage risks leaving empty result_json rows on partial commits.
func beginIdempotencyTx(tx *sql.Tx, agentName, requestID, command string) (existingResultJSON string, alreadyDone bool, err error) {
	if agentName == "" {
		return "", false, errors.New("agent name is required")
	}
	if requestID == "" {
		return "", false, errors.New("request id is required")
	}
	if command == "" {
		return "", false, errors.New("idempotency command is required")
	}

	_, err = tx.ExecContext(context.Background(), `
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
	if err := tx.QueryRowContext(context.Background(), `
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
		return "", false, &IdempotencyInProgressError{
			AgentName: agentName,
			RequestID: requestID,
			Command:   command,
		}
	}
	return resultJSON, true, nil
}

func completeIdempotencyTx(tx *sql.Tx, agentName, requestID, resultJSON string) error {
	if resultJSON == "" {
		// Disallow empty: it's indistinguishable from "not completed" in logs/debugging.
		return errors.New("idempotency result json must be non-empty")
	}
	res, err := tx.ExecContext(context.Background(), `
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

// IsUniqueConstraintErr checks for SQLite duplicate-key violations.
// Exported for use by batch operations in actions layer.
//
// Covers both UNIQUE constraints (2067) and PRIMARY KEY constraints (1555),
// since both signal the same semantic: a row with that key already exists.
// Uses typed sqlite.Error code matching first, falling back to string matching
// for wrapped errors that lose the concrete type.
func IsUniqueConstraintErr(err error) bool {
	if err == nil {
		return false
	}
	// Typed detection:
	//   SQLITE_CONSTRAINT_UNIQUE      = 2067  (19 | (11 << 8))
	//   SQLITE_CONSTRAINT_PRIMARYKEY  = 1555  (19 | (6 << 8))
	var sqliteErr *sqlite.Error
	if errors.As(err, &sqliteErr) {
		code := sqliteErr.Code()
		return code == 2067 || code == 1555
	}
	// Fallback for wrapped errors. Baseline: modernc.org/sqlite v1.45+.
	return strings.Contains(err.Error(), "UNIQUE constraint failed") ||
		strings.Contains(err.Error(), "PRIMARY KEY constraint failed")
}
