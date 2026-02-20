package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/dotcommander/vybe/internal/models"
)

// setAgentFocusTx ensures agent_state exists, sets focus_task_id with CAS,
// and emits an agent_focus event. Shared by task begin and task claim flows.
func setAgentFocusTx(tx *sql.Tx, agentName, taskID string) (focusEventID int64, err error) {
	// Ensure agent_state exists.
	if _, execErr := tx.ExecContext(context.Background(), `
		INSERT OR IGNORE INTO agent_state (agent_name, last_seen_event_id, version, last_active_at)
		VALUES (?, 0, 1, CURRENT_TIMESTAMP)
	`, agentName); execErr != nil {
		return 0, fmt.Errorf("failed to ensure agent state: %w", execErr)
	}

	// Update focus with optimistic concurrency.
	var agentVersion int
	if scanErr := tx.QueryRowContext(context.Background(), `SELECT version FROM agent_state WHERE agent_name = ?`, agentName).Scan(&agentVersion); scanErr != nil {
		return 0, fmt.Errorf("failed to load agent state: %w", scanErr)
	}

	res, err := tx.ExecContext(context.Background(), `
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
		return 0, &VersionConflictError{Entity: "agent_state", ID: agentName, Version: agentVersion}
	}

	id, err := InsertEventTx(tx, models.EventKindAgentFocus, agentName, taskID, fmt.Sprintf("Focus set: %s", taskID), "")
	if err != nil {
		return 0, fmt.Errorf("failed to append focus event: %w", err)
	}

	return id, nil
}

func startTaskAndFocusTx(tx *sql.Tx, agentName, taskID string) (statusEventID int64, focusEventID int64, runErr error) {
	// NOTE: Focus is set before claim. If ClaimTaskTx fails with ErrClaimContention,
	// focus_task_id will point to an unclaimed task. Resume's DetermineFocusTask
	// re-evaluates focus on next call, so this is self-healing. The ordering is:
	// (1) update task status → (2) set agent focus → (3) claim task
	// Reordering to claim-first would require rolling back focus on claim failure.

	// Transition to in_progress (if not already), emitting a status event.
	statusEvent, err := markTaskInProgressTx(tx, agentName, taskID)
	if err != nil {
		return 0, 0, err
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

// markTaskInProgressTx transitions a task to in_progress status and appends a status event.
// Returns the status event ID (0 if the task was already in_progress).
func markTaskInProgressTx(tx *sql.Tx, agentName, taskID string) (int64, error) {
	var status string
	var version int
	queryErr := tx.QueryRowContext(context.Background(), `SELECT status, version FROM tasks WHERE id = ?`, taskID).Scan(&status, &version)
	if queryErr != nil {
		if queryErr == sql.ErrNoRows {
			return 0, fmt.Errorf("task not found: %s", taskID)
		}
		return 0, fmt.Errorf("failed to load task: %w", queryErr)
	}

	if status == "in_progress" {
		return 0, nil
	}

	res, err := tx.ExecContext(context.Background(), `
		UPDATE tasks
		SET status = 'in_progress', version = version + 1, updated_at = CURRENT_TIMESTAMP
		WHERE id = ? AND version = ?
	`, taskID, version)
	if err != nil {
		return 0, fmt.Errorf("failed to update task: %w", err)
	}
	ra, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("failed to check rows affected: %w", err)
	}
	if ra == 0 {
		return 0, &VersionConflictError{Entity: "task", ID: taskID, Version: version}
	}

	id, err := InsertEventTx(tx, models.EventKindTaskStatus, agentName, taskID, "Status changed to: in_progress", "")
	if err != nil {
		return 0, fmt.Errorf("failed to append status event: %w", err)
	}
	return id, nil
}

// StartTaskAndFocus sets a task to in_progress (if needed), sets agent focus to it,
// and appends corresponding events. Returns (statusEventID, focusEventID).
// statusEventID may be 0 if status was already in_progress.
func StartTaskAndFocus(db *sql.DB, agentName, taskID string) (statusEventID int64, focusEventID int64, runErr error) {
	if agentName == "" {
		return 0, 0, errors.New("agent name is required")
	}
	if taskID == "" {
		return 0, 0, errors.New("task ID is required")
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
		return 0, 0, errors.New("agent name is required")
	}
	if taskID == "" {
		return 0, 0, errors.New("task ID is required")
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
