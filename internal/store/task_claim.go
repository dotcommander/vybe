package store

import (
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

// ClaimTask atomically claims a task for an agent with a TTL.
// Uses CAS: only succeeds if task is unclaimed, self-claimed, or lease expired.
// ttlMinutes defaults to 5 if <= 0.
func ClaimTask(db *sql.DB, agentName, taskID string, ttlMinutes int) error {
	if agentName == "" {
		return fmt.Errorf("agent name is required")
	}
	if taskID == "" {
		return fmt.Errorf("task ID is required")
	}
	if ttlMinutes <= 0 {
		ttlMinutes = 5
	}
	if ttlMinutes > 1440 {
		ttlMinutes = 1440
	}

	return Transact(db, func(tx *sql.Tx) error {
		return ClaimTaskTx(tx, agentName, taskID, ttlMinutes)
	})
}

// ClaimTaskTx is the in-transaction variant.
func ClaimTaskTx(tx *sql.Tx, agentName, taskID string, ttlMinutes int) error {
	if agentName == "" {
		return fmt.Errorf("agent name is required")
	}
	if taskID == "" {
		return fmt.Errorf("task ID is required")
	}
	if ttlMinutes <= 0 {
		ttlMinutes = 5
	}
	if ttlMinutes > 1440 {
		ttlMinutes = 1440
	}

	result, err := tx.Exec(`
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

// ReleaseTaskClaim releases an agent's claim on a task.
// Only releases if the agent currently holds the claim.
func ReleaseTaskClaim(db *sql.DB, agentName, taskID string) error {
	if agentName == "" {
		return fmt.Errorf("agent name is required")
	}
	if taskID == "" {
		return fmt.Errorf("task ID is required")
	}

	return Transact(db, func(tx *sql.Tx) error {
		return ReleaseTaskClaimTx(tx, agentName, taskID)
	})
}

// ReleaseTaskClaimTx is the in-transaction variant.
func ReleaseTaskClaimTx(tx *sql.Tx, agentName, taskID string) error {
	if agentName == "" {
		return fmt.Errorf("agent name is required")
	}
	if taskID == "" {
		return fmt.Errorf("task ID is required")
	}

	_, err := tx.Exec(`
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

// HeartbeatTask refreshes lease expiry for a currently owned claim.
func HeartbeatTask(db *sql.DB, agentName, taskID string, ttlMinutes int) error {
	if agentName == "" {
		return fmt.Errorf("agent name is required")
	}
	if taskID == "" {
		return fmt.Errorf("task ID is required")
	}
	if ttlMinutes <= 0 {
		ttlMinutes = 5
	}
	if ttlMinutes > 1440 {
		ttlMinutes = 1440
	}

	return Transact(db, func(tx *sql.Tx) error {
		return HeartbeatTaskTx(tx, agentName, taskID, ttlMinutes)
	})
}

// HeartbeatTaskTx refreshes lease expiry for a currently owned claim in-tx.
func HeartbeatTaskTx(tx *sql.Tx, agentName, taskID string, ttlMinutes int) error {
	if agentName == "" {
		return fmt.Errorf("agent name is required")
	}
	if taskID == "" {
		return fmt.Errorf("task ID is required")
	}
	if ttlMinutes <= 0 {
		ttlMinutes = 5
	}
	if ttlMinutes > 1440 {
		ttlMinutes = 1440
	}

	result, err := tx.Exec(`
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
