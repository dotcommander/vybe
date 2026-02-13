package store

import (
	"database/sql"
	"encoding/json"
	"fmt"

	"github.com/dotcommander/vybe/internal/models"
)

// ClaimNextTaskResult holds the IDs produced by a successful claim-next operation.
type ClaimNextTaskResult struct {
	TaskID        string `json:"task_id"`
	StatusEventID int64  `json:"status_event_id"`
	FocusEventID  int64  `json:"focus_event_id"`
	ClaimEventID  int64  `json:"claim_event_id"`
}

// ClaimNextTaskTx selects the first eligible pending task, claims it, sets it
// in_progress, sets agent focus, and emits events — all in one transaction.
//
// Selection: pending, unclaimed or expired claim, no unresolved dependencies,
// project-scoped when projectID is non-empty.
// ORDER BY priority DESC, created_at ASC.
//
// Returns empty TaskID + nil error when no work is available.
func ClaimNextTaskTx(tx *sql.Tx, agentName, projectID string, ttlMinutes int) (*ClaimNextTaskResult, error) {
	if agentName == "" {
		return nil, fmt.Errorf("agent name is required")
	}
	if ttlMinutes <= 0 {
		ttlMinutes = 5
	}
	if ttlMinutes > 1440 {
		ttlMinutes = 1440
	}

	// Build selection query: pending tasks with no unresolved deps,
	// unclaimed or expired claim.
	query := `
		SELECT id, version FROM tasks
		WHERE status = 'pending'
		  AND (claimed_by IS NULL OR claim_expires_at < CURRENT_TIMESTAMP)
		  AND NOT EXISTS (
			SELECT 1 FROM task_dependencies td
			JOIN tasks dep ON dep.id = td.depends_on_task_id
			WHERE td.task_id = tasks.id AND dep.status != 'completed'
		  )
	`
	var args []any
	if projectID != "" {
		query += ` AND project_id = ?`
		args = append(args, projectID)
	}
	query += ` ORDER BY priority DESC, created_at ASC LIMIT 1`

	var taskID string
	var version int
	err := tx.QueryRow(query, args...).Scan(&taskID, &version)
	if err == sql.ErrNoRows {
		return &ClaimNextTaskResult{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to select next task: %w", err)
	}

	// Set status to in_progress with CAS.
	res, err := tx.Exec(`
		UPDATE tasks
		SET status = 'in_progress', version = version + 1, updated_at = CURRENT_TIMESTAMP
		WHERE id = ? AND version = ?
	`, taskID, version)
	if err != nil {
		return nil, fmt.Errorf("failed to update task status: %w", err)
	}
	ra, err := res.RowsAffected()
	if err != nil {
		return nil, fmt.Errorf("failed to check rows affected: %w", err)
	}
	if ra == 0 {
		return nil, ErrVersionConflict
	}

	statusEventID, err := InsertEventTx(tx, models.EventKindTaskStatus, agentName, taskID, "Status changed to: in_progress", "")
	if err != nil {
		return nil, fmt.Errorf("failed to append status event: %w", err)
	}

	// Set agent focus.
	focusEventID, err := setAgentFocusTx(tx, agentName, taskID)
	if err != nil {
		return nil, fmt.Errorf("failed to set agent focus: %w", err)
	}

	// Claim the task.
	if err := ClaimTaskTx(tx, agentName, taskID, ttlMinutes); err != nil {
		return nil, fmt.Errorf("failed to claim task: %w", err)
	}

	// Emit task_claimed event with metadata.
	meta, err := json.Marshal(map[string]any{
		"ttl_minutes": ttlMinutes,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to marshal claim metadata: %w", err)
	}
	claimEventID, err := InsertEventTx(tx, models.EventKindTaskClaimed, agentName, taskID, fmt.Sprintf("Task claimed by %s", agentName), string(meta))
	if err != nil {
		return nil, fmt.Errorf("failed to append claim event: %w", err)
	}

	return &ClaimNextTaskResult{
		TaskID:        taskID,
		StatusEventID: statusEventID,
		FocusEventID:  focusEventID,
		ClaimEventID:  claimEventID,
	}, nil
}
