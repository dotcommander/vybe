package actions

import (
	"context"
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
	if agentName == "" {
		return nil, 0, errors.New("agent name is required")
	}
	if requestID == "" {
		return nil, 0, errors.New("request id is required")
	}
	if title == "" {
		return nil, 0, errors.New("task title is required")
	}

	type idemResult struct {
		TaskID  string `json:"task_id"`
		EventID int64  `json:"event_id"`
	}

	r, err := store.RunIdempotent(context.Background(), db, agentName, requestID, "task.create", func(tx *sql.Tx) (idemResult, error) {
		createdTask, err := store.CreateTaskTx(tx, title, description, projectID, priority)
		if err != nil {
			return idemResult{}, err
		}

		eventID, err := store.InsertEventTx(tx, models.EventKindTaskCreated, agentName, createdTask.ID, fmt.Sprintf("Task created: %s", title), "")
		if err != nil {
			return idemResult{}, fmt.Errorf("failed to append event: %w", err)
		}

		return idemResult{TaskID: createdTask.ID, EventID: eventID}, nil
	})
	if err != nil {
		return nil, 0, fmt.Errorf("failed to create task: %w", err)
	}

	task, err := store.GetTask(db, r.TaskID)
	if err != nil {
		return nil, 0, fmt.Errorf("failed to fetch created task: %w", err)
	}

	return task, r.EventID, nil
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
	if agentName == "" {
		return nil, 0, errors.New("agent name is required")
	}
	if requestID == "" {
		return nil, 0, errors.New("request id is required")
	}
	if taskID == "" {
		return nil, 0, errors.New("task ID is required")
	}
	if status == "" {
		return nil, 0, errors.New("status is required")
	}

	type idemResult struct {
		EventID int64 `json:"event_id"`
	}

	if err := validateTaskStatus(status); err != nil {
		return nil, 0, err
	}

	r, _, err := store.RunIdempotentWithRetry(
		context.Background(),
		db,
		agentName,
		requestID,
		"task.set_status",
		3,
		func(err error) bool { return errors.Is(err, store.ErrVersionConflict) },
		func(tx *sql.Tx) (idemResult, error) {
			version, err := store.GetTaskVersionTx(tx, taskID)
			if err != nil {
				return idemResult{}, fmt.Errorf("failed to get task: %w", err)
			}

			eventID, err := store.UpdateTaskStatusWithEventTx(tx, agentName, taskID, status, version)
			if err != nil {
				return idemResult{}, err
			}

			// Unblock dependent tasks atomically if this task is now completed
			if status == completedStatus {
				_, ubErr := store.UnblockDependentsTx(tx, taskID)
				if ubErr != nil {
					return idemResult{}, fmt.Errorf("failed to unblock dependents: %w", ubErr)
				}
			}

			// Set blocked_reason atomically with status change
			if status == blockedStatus && blockedReason != "" {
				if brErr := store.SetBlockedReasonTx(tx, taskID, blockedReason); brErr != nil {
					return idemResult{}, fmt.Errorf("failed to set blocked reason: %w", brErr)
				}
			}

			return idemResult{EventID: eventID}, nil
		},
	)
	if err != nil {
		return nil, 0, err
	}

	updatedTask, err := store.GetTask(db, taskID)
	if err != nil {
		return nil, 0, fmt.Errorf("failed to fetch updated task: %w", err)
	}

	return updatedTask, r.EventID, nil
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

// TaskSetPriorityIdempotent updates task priority once per (agent_name, request_id).
// Uses RunIdempotentWithRetry internally to handle both idempotency replay
// and CAS version conflicts in a single retry loop.
func TaskSetPriorityIdempotent(db *sql.DB, agentName, requestID, taskID string, priority int) (*models.Task, int64, error) {
	if agentName == "" {
		return nil, 0, errors.New("agent name is required")
	}
	if requestID == "" {
		return nil, 0, errors.New("request id is required")
	}
	if taskID == "" {
		return nil, 0, errors.New("task ID is required")
	}

	type idemResult struct {
		EventID int64 `json:"event_id"`
	}

	r, _, err := store.RunIdempotentWithRetry(
		context.Background(),
		db,
		agentName,
		requestID,
		"task.set_priority",
		3,
		func(err error) bool { return errors.Is(err, store.ErrVersionConflict) },
		func(tx *sql.Tx) (idemResult, error) {
			version, err := store.GetTaskVersionTx(tx, taskID)
			if err != nil {
				return idemResult{}, fmt.Errorf("failed to get task: %w", err)
			}

			eventID, err := store.UpdateTaskPriorityWithEventTx(tx, agentName, taskID, priority, version)
			if err != nil {
				return idemResult{}, err
			}

			return idemResult{EventID: eventID}, nil
		},
	)
	if err != nil {
		return nil, 0, err
	}

	updatedTask, err := store.GetTask(db, taskID)
	if err != nil {
		return nil, 0, fmt.Errorf("failed to fetch updated task: %w", err)
	}

	return updatedTask, r.EventID, nil
}

func validateDependencyArgs(agentName, taskID, dependsOnTaskID string) error {
	if agentName == "" {
		return errors.New("agent name is required")
	}
	if taskID == "" {
		return errors.New("task ID is required")
	}
	if dependsOnTaskID == "" {
		return errors.New("depends_on_task_id is required")
	}
	return nil
}

func taskDependencyMessage(taskID, dependsOnTaskID string, adding bool) string {
	if adding {
		return fmt.Sprintf("Dependency added: %s depends on %s", taskID, dependsOnTaskID)
	}
	return fmt.Sprintf("Dependency removed: %s no longer depends on %s", taskID, dependsOnTaskID)
}

type taskDependencyParams struct {
	db              *sql.DB
	agentName       string
	taskID          string
	dependsOnTaskID string
	eventKind       string
	idempotencyCmd  string
	requestID       string
	mutate          func(tx *sql.Tx, taskID, dependsOnTaskID string) error
	adding          bool
}

func mutateTaskDependency(p taskDependencyParams) error {
	if err := validateDependencyArgs(p.agentName, p.taskID, p.dependsOnTaskID); err != nil {
		return err
	}

	message := taskDependencyMessage(p.taskID, p.dependsOnTaskID, p.adding)

	type idemResult struct {
		EventID int64 `json:"event_id"`
	}

	_, err := store.RunIdempotent(context.Background(), p.db, p.agentName, p.requestID, p.idempotencyCmd, func(tx *sql.Tx) (idemResult, error) {
		if err := p.mutate(tx, p.taskID, p.dependsOnTaskID); err != nil {
			return idemResult{}, err
		}

		eventID, err := store.InsertEventTx(tx, p.eventKind, p.agentName, p.taskID, message, "")
		if err != nil {
			return idemResult{}, fmt.Errorf("failed to append event: %w", err)
		}

		return idemResult{EventID: eventID}, nil
	})

	return err
}

// TaskAddDependencyIdempotent adds a dependency once per (agent_name, request_id).
func TaskAddDependencyIdempotent(db *sql.DB, agentName, requestID, taskID, dependsOnTaskID string) error {
	if requestID == "" {
		return errors.New("request id is required")
	}

	return mutateTaskDependency(taskDependencyParams{
		db: db, agentName: agentName, taskID: taskID, dependsOnTaskID: dependsOnTaskID,
		eventKind: models.EventKindTaskDependencyAdded, idempotencyCmd: "task.add_dependency",
		requestID: requestID, mutate: store.AddTaskDependencyTx, adding: true,
	})
}

// TaskRemoveDependencyIdempotent removes a dependency once per (agent_name, request_id).
func TaskRemoveDependencyIdempotent(db *sql.DB, agentName, requestID, taskID, dependsOnTaskID string) error {
	if requestID == "" {
		return errors.New("request id is required")
	}

	return mutateTaskDependency(taskDependencyParams{
		db: db, agentName: agentName, taskID: taskID, dependsOnTaskID: dependsOnTaskID,
		eventKind: models.EventKindTaskDependencyRemoved, idempotencyCmd: "task.remove_dependency",
		requestID: requestID, mutate: store.RemoveTaskDependencyTx, adding: false,
	})
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
	if agentName == "" {
		return nil, errors.New("agent name is required")
	}
	if requestID == "" {
		return nil, errors.New("request id is required")
	}
	if taskID == "" {
		return nil, errors.New("task ID is required")
	}
	if summary == "" {
		return nil, errors.New("summary is required")
	}

	status := resolveCloseOutcome(outcome)
	if status == "" {
		return nil, fmt.Errorf("invalid outcome '%s': must be done or blocked", outcome)
	}

	r, _, err := store.RunIdempotentWithRetry(
		context.Background(), db, agentName, requestID, "task.close",
		3,
		func(err error) bool { return errors.Is(err, store.ErrVersionConflict) },
		func(tx *sql.Tx) (store.CloseTaskResult, error) {
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

	task, err := store.GetTask(db, taskID)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch closed task: %w", err)
	}

	return &TaskCloseResult{
		Task:          task,
		StatusEventID: r.StatusEventID,
		CloseEventID:  r.CloseEventID,
	}, nil
}
