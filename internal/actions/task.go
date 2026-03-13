package actions

import (
	"database/sql"
	"errors"
	"fmt"

	"github.com/dotcommander/vybe/internal/models"
	"github.com/dotcommander/vybe/internal/store"
)

const (
	completedStatus = "completed"
	blockedStatus   = "blocked"
)

type invalidTaskStatusError struct {
	Status string
}

func (e invalidTaskStatusError) Error() string {
	return fmt.Sprintf("invalid status '%s'", e.Status)
}

func (e invalidTaskStatusError) SlogAttrs() []any {
	return []any{
		"field", "status",
		"invalid_value", e.Status,
		"valid_options", taskStatusOptions(),
	}
}

// taskStatusOptions returns the allowed task status values.
func taskStatusOptions() []string {
	return []string{"pending", "in_progress", completedStatus, blockedStatus}
}

// isValidTaskStatus returns true if s is a known task status.
func isValidTaskStatus(s string) bool {
	switch s {
	case "pending", "in_progress", completedStatus, blockedStatus:
		return true
	}
	return false
}

func validateTaskStatus(status string) error {
	if !isValidTaskStatus(status) {
		return invalidTaskStatusError{Status: status}
	}
	return nil
}

// TaskCreateIdempotent creates a new task once per (agent_name, request_id).
// On retries with the same request id, it returns the originally created task + event id.
// If projectID is non-empty, the task is associated with that project.
func TaskCreateIdempotent(db *sql.DB, agentName, requestID, title, description, projectID string, priority int) (*models.Task, int64, error) { //nolint:revive // argument-limit: all params are required and semantically distinct; a struct would degrade test readability
	if title == "" {
		return nil, 0, errors.New("task title is required")
	}

	createdTask, eventID, err := runCreateWithEvent(db, agentName, requestID, "task.create", "create task", func(tx *sql.Tx) (models.Task, int64, error) {
		createdTask, err := store.CreateTaskTx(tx, title, description, projectID, priority)
		if err != nil {
			return models.Task{}, 0, err
		}

		eventID, err := store.InsertEventTx(tx, models.EventKindTaskCreated, agentName, createdTask.ID, fmt.Sprintf("Task created: %s", title), "")
		if err != nil {
			return models.Task{}, 0, fmt.Errorf("failed to append event: %w", err)
		}

		return *createdTask, eventID, nil
	})
	if err != nil {
		return nil, 0, err
	}

	return createdTask, eventID, nil
}

// TaskSetStatusIdempotent updates task status once per (agent_name, request_id).
// On retries with the same request_id, it replays the originally stored result
// without re-executing the status change (idempotent).
//
// blockedReason is applied atomically when status is "blocked":
//   - "dependency"       → dependency-blocked; resume Rule 1.5 keeps focus
//   - "failure:<reason>" → failure-blocked; resume Rule 1.5 skips to find new work
//   - ""                 → no blocked_reason written
//
// Uses RunIdempotentWithRetry internally to handle both idempotency replay
// and CAS version conflicts in a single retry loop.
//
//nolint:gocognit,gocyclo,revive // idempotent variant adds request deduplication around TaskSetStatus logic; all branches are required
func TaskSetStatusIdempotent(db *sql.DB, agentName, requestID, taskID, status, blockedReason string) (*models.Task, int64, error) {
	if status == "" {
		return nil, 0, errors.New("status is required")
	}

	if err := validateTaskStatus(status); err != nil {
		return nil, 0, err
	}

	updatedTask, result, err := runTaskMutationWithRetry(db, agentName, requestID, taskID, "task.set_status", "updated", func(tx *sql.Tx) (eventResult, error) {
		version, err := store.GetTaskVersionTx(tx, taskID)
		if err != nil {
			return eventResult{}, fmt.Errorf("failed to get task: %w", err)
		}

		eventID, err := store.UpdateTaskStatusWithEventTx(tx, agentName, taskID, status, version)
		if err != nil {
			return eventResult{}, err
		}

		// Set blocked_reason atomically with status change
		if status == blockedStatus && blockedReason != "" {
			if brErr := store.SetBlockedReasonTx(tx, taskID, blockedReason); brErr != nil {
				return eventResult{}, fmt.Errorf("failed to set blocked reason: %w", brErr)
			}
		}

		return eventResult{EventID: eventID}, nil
	},
	)
	if err != nil {
		return nil, 0, err
	}

	return updatedTask, result.EventID, nil
}

// TaskStartResult holds the output of a TaskStart operation.
type TaskStartResult struct {
	Task          *models.Task `json:"task"`
	StatusEventID int64        `json:"status_event_id"`
	FocusEventID  int64        `json:"focus_event_id"`
}

// TaskStartIdempotent performs TaskStart once per (agent_name, request_id).
// On retries with the same request id, it returns the originally created event ids and current task state.
func TaskStartIdempotent(db *sql.DB, agentName, requestID, taskID string) (*TaskStartResult, error) {
	if agentName == "" {
		return nil, errors.New("agent name is required")
	}
	if requestID == "" {
		return nil, errors.New("request id is required")
	}
	if taskID == "" {
		return nil, errors.New("task ID is required")
	}

	statusEventID, focusEventID, err := store.StartTaskAndFocusIdempotent(db, agentName, requestID, taskID)
	if err != nil {
		return nil, err
	}

	task, err := store.GetTask(db, taskID)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch task: %w", err)
	}

	return &TaskStartResult{Task: task, StatusEventID: statusEventID, FocusEventID: focusEventID}, nil
}

// TaskGet retrieves a task by ID
func TaskGet(db *sql.DB, taskID string) (*models.Task, error) {
	if taskID == "" {
		return nil, errors.New("task ID is required")
	}

	task, err := store.GetTask(db, taskID)
	if err != nil {
		return nil, fmt.Errorf("failed to get task: %w", err)
	}

	return task, nil
}

// TaskList retrieves all tasks, optionally filtered by status, project, and/or priority.
// priorityFilter < 0 means no filter.
func TaskList(db *sql.DB, statusFilter, projectFilter string, priorityFilter int) ([]*models.Task, error) {
	tasks, err := store.ListTasks(db, statusFilter, projectFilter, priorityFilter)
	if err != nil {
		return nil, fmt.Errorf("failed to list tasks: %w", err)
	}

	return tasks, nil
}

// TaskCloseResult captures the output of a close operation.
type TaskCloseResult struct {
	Task          *models.Task `json:"task"`
	StatusEventID int64        `json:"status_event_id"`
	CloseEventID  int64        `json:"close_event_id"`
}

// resolveCloseOutcome maps outcome alias ("done"/"blocked") to task status.
// Returns empty string if the outcome is invalid.
func resolveCloseOutcome(outcome string) string {
	switch outcome {
	case "done":
		return completedStatus
	case "blocked":
		return blockedStatus
	}
	return ""
}

// TaskCloseIdempotent atomically closes a task (status + summary event),
// once per request-id. Outcome must be "done" or "blocked".
func TaskCloseIdempotent(db *sql.DB, agentName, requestID, taskID, outcome, summary, label, blockedReason string) (*TaskCloseResult, error) { //nolint:revive // argument-limit: all params are required close-task inputs; a struct adds boilerplate without clarity
	if summary == "" {
		return nil, errors.New("summary is required")
	}

	status := resolveCloseOutcome(outcome)
	if status == "" {
		return nil, fmt.Errorf("invalid outcome '%s': must be done or blocked", outcome)
	}

	task, result, err := runTaskMutationWithRetry(db, agentName, requestID, taskID, "task.close", "closed", func(tx *sql.Tx) (store.CloseTaskResult, error) {
		result, err := store.CloseTaskTx(tx, store.CloseTaskParams{
			AgentName:     agentName,
			TaskID:        taskID,
			Status:        status,
			Summary:       summary,
			Label:         label,
			BlockedReason: blockedReason,
		})
		if err != nil {
			return store.CloseTaskResult{}, err
		}
		return *result, nil
	},
	)
	if err != nil {
		return nil, err
	}

	return &TaskCloseResult{
		Task:          task,
		StatusEventID: result.StatusEventID,
		CloseEventID:  result.CloseEventID,
	}, nil
}
