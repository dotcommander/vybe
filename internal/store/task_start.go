package store

import (
	"database/sql"
	"fmt"

	"github.com/dotcommander/vybe/internal/models"
)

// setAgentFocusTx ensures agent_state exists, sets focus_task_id with CAS,
// and emits an agent_focus event. Shared by task start and task claim flows.
func setAgentFocusTx(tx *sql.Tx, agentName, taskID string) (focusEventID int64, err error) {
	// Ensure agent_state exists.
	if _, execErr := tx.Exec(`
		INSERT OR IGNORE INTO agent_state (agent_name, last_seen_event_id, version, last_active_at)
		VALUES (?, 0, 1, CURRENT_TIMESTAMP)
	`, agentName); execErr != nil {
		return 0, fmt.Errorf("failed to ensure agent state: %w", execErr)
	}

	// Update focus with optimistic concurrency.
	var agentVersion int
	if err := tx.QueryRow(`SELECT version FROM agent_state WHERE agent_name = ?`, agentName).Scan(&agentVersion); err != nil {
		return 0, fmt.Errorf("failed to load agent state: %w", err)
	}

	res, err := tx.Exec(`
		UPDATE agent_state
		SET focus_task_id = ?,
		    last_active_at = CURRENT_TIMESTAMP,
		    version = version + 1
		WHERE agent_name = ? AND version = ?
	`, taskID, agentName, agentVersion)
	if err != nil {
		return 0, fmt.Errorf("failed to set focus: %w", err)
	}
	ra, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("failed to check rows affected: %w", err)
	}
	if ra == 0 {
		return 0, ErrVersionConflict
	}

	id, err := InsertEventTx(tx, models.EventKindAgentFocus, agentName, taskID, fmt.Sprintf("Focus set: %s", taskID), "")
	if err != nil {
		return 0, fmt.Errorf("failed to append focus event: %w", err)
	}

	return id, nil
}

func startTaskAndFocusTx(tx *sql.Tx, agentName, taskID string) (statusEventID int64, focusEventID int64, runErr error) {
	var statusEvent int64

	// NOTE: Focus is set before claim. If ClaimTaskTx fails with ErrClaimContention,
	// focus_task_id will point to an unclaimed task. Resume's DetermineFocusTask
	// re-evaluates focus on next call, so this is self-healing. The ordering is:
	// (1) update task status → (2) set agent focus → (3) claim task
	// Reordering to claim-first would require rolling back focus on claim failure.

	// Load current task status/version.
	var status string
	var version int
	queryErr := tx.QueryRow(`SELECT status, version FROM tasks WHERE id = ?`, taskID).Scan(&status, &version)
	if queryErr != nil {
		if queryErr == sql.ErrNoRows {
			return 0, 0, fmt.Errorf("task not found: %s", taskID)
		}
		return 0, 0, fmt.Errorf("failed to load task: %w", queryErr)
	}

	// Update status only if needed.
	if status != "in_progress" {
		res, err := tx.Exec(`
			UPDATE tasks
			SET status = 'in_progress', version = version + 1, updated_at = CURRENT_TIMESTAMP
			WHERE id = ? AND version = ?
		`, taskID, version)
		if err != nil {
			return 0, 0, fmt.Errorf("failed to update task: %w", err)
		}
		ra, err := res.RowsAffected()
		if err != nil {
			return 0, 0, fmt.Errorf("failed to check rows affected: %w", err)
		}
		if ra == 0 {
			return 0, 0, ErrVersionConflict
		}

		id, err := InsertEventTx(tx, models.EventKindTaskStatus, agentName, taskID, "Status changed to: in_progress", "")
		if err != nil {
			return 0, 0, fmt.Errorf("failed to append status event: %w", err)
		}
		statusEvent = id
	}

	// Set agent focus.
	focusEvent, err := setAgentFocusTx(tx, agentName, taskID)
	if err != nil {
		return 0, 0, err
	}

	// Claim the task — failure means another agent holds the claim.
	if err := ClaimTaskTx(tx, agentName, taskID, 5); err != nil {
		return 0, 0, fmt.Errorf("failed to claim task: %w", err)
	}

	return statusEvent, focusEvent, nil
}

// StartTaskAndFocus sets a task to in_progress (if needed), sets agent focus to it,
// and appends corresponding events. Returns (statusEventID, focusEventID).
// statusEventID may be 0 if status was already in_progress.
func StartTaskAndFocus(db *sql.DB, agentName, taskID string) (statusEventID int64, focusEventID int64, runErr error) {
	if agentName == "" {
		return 0, 0, fmt.Errorf("agent name is required")
	}
	if taskID == "" {
		return 0, 0, fmt.Errorf("task ID is required")
	}

	var statusEvent int64
	var focusEvent int64

	runErr = Transact(db, func(tx *sql.Tx) error {
		se, fe, txErr := startTaskAndFocusTx(tx, agentName, taskID)
		if txErr != nil {
			return txErr
		}
		statusEvent = se
		focusEvent = fe
		return nil
	})
	if runErr != nil {
		return 0, 0, runErr
	}

	return statusEvent, focusEvent, nil
}

// StartTaskAndFocusIdempotent performs StartTaskAndFocus once per (agent_name, request_id).
// On retries with the same request id, returns the originally created event ids.
func StartTaskAndFocusIdempotent(db *sql.DB, agentName, requestID, taskID string) (statusEventID int64, focusEventID int64, runErr error) {
	if agentName == "" {
		return 0, 0, fmt.Errorf("agent name is required")
	}
	if taskID == "" {
		return 0, 0, fmt.Errorf("task ID is required")
	}

	type idemResult struct {
		StatusEventID int64 `json:"status_event_id"`
		FocusEventID  int64 `json:"focus_event_id"`
	}

	r, err := RunIdempotent(db, agentName, requestID, "task.start", func(tx *sql.Tx) (idemResult, error) {
		statusEventID, focusEventID, txErr := startTaskAndFocusTx(tx, agentName, taskID)
		if txErr != nil {
			return idemResult{}, txErr
		}
		return idemResult{StatusEventID: statusEventID, FocusEventID: focusEventID}, nil
	})
	if err != nil {
		return 0, 0, err
	}

	return r.StatusEventID, r.FocusEventID, nil
}
