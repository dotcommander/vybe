package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// ErrClaimContention is returned when a claim fails because another agent
// holds an active (non-expired) claim on the task.
var ErrClaimContention = errors.New("task already claimed by another agent")

// ErrClaimNotOwned is returned when lease heartbeat is attempted by an agent
// that does not hold an active claim.
var ErrClaimNotOwned = errors.New("task claim is not owned by agent")

// ClaimTaskTx is the in-transaction variant.
func ClaimTaskTx(tx *sql.Tx, agentName, taskID string, ttlMinutes int) error {
	if agentName == "" {
		return errors.New("agent name is required")
	}
	if taskID == "" {
		return errors.New("task ID is required")
	}
	if ttlMinutes <= 0 {
		ttlMinutes = 5
	}
	if ttlMinutes > 1440 {
		ttlMinutes = 1440
	}

	result, err := tx.ExecContext(context.Background(), `
		UPDATE tasks
		SET claimed_by = ?,
		    claimed_at = CURRENT_TIMESTAMP,
		    claim_expires_at = datetime(CURRENT_TIMESTAMP, '+' || ? || ' minutes'),
		    last_heartbeat_at = CURRENT_TIMESTAMP,
		    attempt = CASE
		        WHEN claimed_by = ? AND claim_expires_at IS NOT NULL AND claim_expires_at >= CURRENT_TIMESTAMP THEN attempt
		        ELSE attempt + 1
		    END
		WHERE id = ?
		  AND (claimed_by IS NULL OR claimed_by = ? OR claim_expires_at < CURRENT_TIMESTAMP)
	`, agentName, ttlMinutes, agentName, taskID, agentName)
	if err != nil {
		return fmt.Errorf("failed to claim task: %w", err)
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to check rows affected: %w", err)
	}

	if rowsAffected == 0 {
		return ErrClaimContention
	}

	return nil
}

// ReleaseTaskClaimTx is the in-transaction variant.
func ReleaseTaskClaimTx(tx *sql.Tx, agentName, taskID string) error {
	if agentName == "" {
		return errors.New("agent name is required")
	}
	if taskID == "" {
		return errors.New("task ID is required")
	}

	_, err := tx.ExecContext(context.Background(), `
		UPDATE tasks
		SET claimed_by = NULL, claimed_at = NULL, claim_expires_at = NULL, last_heartbeat_at = NULL
		WHERE id = ? AND claimed_by = ?
	`, taskID, agentName)
	if err != nil {
		return fmt.Errorf("failed to release task claim: %w", err)
	}

	// Note: We don't check rowsAffected here. If the claim was already released
	// or never existed, that's fine - the desired end state is achieved.

	return nil
}

// HeartbeatTaskTx refreshes lease expiry for a currently owned claim in-tx.
func HeartbeatTaskTx(tx *sql.Tx, agentName, taskID string, ttlMinutes int) error {
	if agentName == "" {
		return errors.New("agent name is required")
	}
	if taskID == "" {
		return errors.New("task ID is required")
	}
	if ttlMinutes <= 0 {
		ttlMinutes = 5
	}
	if ttlMinutes > 1440 {
		ttlMinutes = 1440
	}

	result, err := tx.ExecContext(context.Background(), `
		UPDATE tasks
		SET claim_expires_at = datetime(CURRENT_TIMESTAMP, '+' || ? || ' minutes'),
		    last_heartbeat_at = CURRENT_TIMESTAMP
		WHERE id = ?
		  AND claimed_by = ?
		  AND claim_expires_at IS NOT NULL
		  AND claim_expires_at >= CURRENT_TIMESTAMP
	`, ttlMinutes, taskID, agentName)
	if err != nil {
		return fmt.Errorf("failed to heartbeat task claim: %w", err)
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to check rows affected: %w", err)
	}
	if rowsAffected == 0 {
		return ErrClaimNotOwned
	}

	return nil
}
