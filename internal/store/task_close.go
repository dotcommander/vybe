package store

import (
	"database/sql"
	"encoding/json"
	"fmt"

	"github.com/dotcommander/vibe/internal/models"
)

// CloseTaskResult holds the IDs produced by a close-task operation.
type CloseTaskResult struct {
	StatusEventID int64 `json:"status_event_id"`
	CloseEventID  int64 `json:"close_event_id"`
}

// CloseTaskParams groups the inputs for CloseTaskTx.
type CloseTaskParams struct {
	AgentName     string
	TaskID        string
	Status        string // "completed" or "blocked"
	Summary       string
	Label         string // optional, stored in event metadata only
	BlockedReason string // optional, only used when Status is "blocked"
}

// CloseTaskTx atomically closes a task: CAS status update, release claim,
// unblock dependents (if completed), set blocked_reason (if blocked),
// emit task_status + task_closed events.
//
// Status must be "completed" or "blocked".
func CloseTaskTx(tx *sql.Tx, p CloseTaskParams) (*CloseTaskResult, error) {
	if p.AgentName == "" {
		return nil, fmt.Errorf("agent name is required")
	}
	if p.TaskID == "" {
		return nil, fmt.Errorf("task ID is required")
	}
	if p.Status != "completed" && p.Status != "blocked" {
		return nil, fmt.Errorf("close status must be completed or blocked, got: %s", p.Status)
	}
	if p.Summary == "" {
		return nil, fmt.Errorf("summary is required")
	}

	// CAS status update.
	version, err := GetTaskVersionTx(tx, p.TaskID)
	if err != nil {
		return nil, err
	}

	statusEventID, err := UpdateTaskStatusWithEventTx(tx, p.AgentName, p.TaskID, p.Status, version)
	if err != nil {
		return nil, fmt.Errorf("failed to update task status: %w", err)
	}

	// Release claim — best-effort: if release fails (e.g., concurrent GC already
	// released it, or agent doesn't own the claim), the desired end state (task
	// closed) is still achieved. Matches existing set-status behavior.
	_ = ReleaseTaskClaimTx(tx, p.AgentName, p.TaskID)

	// Unblock dependents if completed.
	if p.Status == "completed" {
		if _, ubErr := UnblockDependentsTx(tx, p.TaskID); ubErr != nil {
			return nil, fmt.Errorf("failed to unblock dependents: %w", ubErr)
		}
	}

	// Always set/clear blocked_reason when blocked to avoid stale values.
	if p.Status == "blocked" {
		if err := SetBlockedReasonTx(tx, p.TaskID, p.BlockedReason); err != nil {
			return nil, fmt.Errorf("failed to set blocked reason: %w", err)
		}
	}

	// Build close event metadata.
	metaMap := map[string]any{
		"outcome": p.Status,
		"summary": p.Summary,
	}
	if p.Label != "" {
		metaMap["label"] = p.Label
	}
	metaBytes, err := json.Marshal(metaMap)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal close metadata: %w", err)
	}

	closeEventID, err := InsertEventTx(tx, models.EventKindTaskClosed, p.AgentName, p.TaskID,
		fmt.Sprintf("Task closed: %s", p.Status), string(metaBytes))
	if err != nil {
		return nil, fmt.Errorf("failed to append close event: %w", err)
	}

	return &CloseTaskResult{
		StatusEventID: statusEventID,
		CloseEventID:  closeEventID,
	}, nil
}
