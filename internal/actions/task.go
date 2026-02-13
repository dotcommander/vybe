package actions

import (
	"database/sql"
	"errors"
	"fmt"
	"log/slog"

	"github.com/dotcommander/vybe/internal/models"
	"github.com/dotcommander/vybe/internal/store"
)

const completedStatus = "completed"

var validTaskStatusOptions = []string{"pending", "in_progress", completedStatus, "blocked"}

var validTaskStatuses = map[string]bool{
	"pending":       true,
	"in_progress":   true,
	completedStatus: true,
	"blocked":       true,
}

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
		"valid_options", validTaskStatusOptions,
	}
}

func validateTaskStatus(status string) error {
	if !validTaskStatuses[status] {
		return invalidTaskStatusError{Status: status}
	}
	return nil
}

// TaskCreate creates a new task with the given title and description.
// Also appends a task_created event in the same transaction for continuity.
// If projectID is non-empty, the task is associated with that project.
func TaskCreate(db *sql.DB, agentName, title, description, projectID string, priority int) (*models.Task, int64, error) {
	if agentName == "" {
		return nil, 0, fmt.Errorf("agent name is required")
	}
	if title == "" {
		return nil, 0, fmt.Errorf("task title is required")
	}

	var (
		task    *models.Task
		eventID int64
	)

	// Atomic: task row + creation event.
	if err := store.Transact(db, func(tx *sql.Tx) error {
		var err error
		task, err = store.CreateTaskTx(tx, title, description, projectID, priority)
		if err != nil {
			return err
		}

		lastEventID, err := store.InsertEventTx(tx, models.EventKindTaskCreated, agentName, task.ID, fmt.Sprintf("Task created: %s", title), "")
		if err != nil {
			return fmt.Errorf("failed to append event: %w", err)
		}
		eventID = lastEventID
		return nil
	}); err != nil {
		return nil, 0, fmt.Errorf("failed to create task: %w", err)
	}

	return task, eventID, nil
}

// TaskCreateIdempotent creates a new task once per (agent_name, request_id).
// On retries with the same request id, it returns the originally created task + event id.
// If projectID is non-empty, the task is associated with that project.
func TaskCreateIdempotent(db *sql.DB, agentName, requestID, title, description, projectID string, priority int) (*models.Task, int64, error) {
	if agentName == "" {
		return nil, 0, fmt.Errorf("agent name is required")
	}
	if requestID == "" {
		return nil, 0, fmt.Errorf("request id is required")
	}
	if title == "" {
		return nil, 0, fmt.Errorf("task title is required")
	}

	type idemResult struct {
		TaskID  string `json:"task_id"`
		EventID int64  `json:"event_id"`
	}

	r, err := store.RunIdempotent(db, agentName, requestID, "task.create", func(tx *sql.Tx) (idemResult, error) {
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

// TaskSetStatus atomically updates task status and logs an event.
// Returns the updated task and the event ID.
//
// Status transitions are intentionally unrestricted — any status may move to
// any other status (pending, in_progress, completed, blocked). This gives
// agents maximum flexibility for self-correction and recovery.
//
// Side effects on transition:
//   - completed → releases claim, unblocks dependent tasks
//   - blocked   → releases claim
//
// Uses optimistic concurrency control with CAS retry (up to 3 attempts).
func TaskSetStatus(db *sql.DB, agentName, taskID, status string) (*models.Task, int64, error) {
	if agentName == "" {
		return nil, 0, fmt.Errorf("agent name is required")
	}
	if taskID == "" {
		return nil, 0, fmt.Errorf("task ID is required")
	}

	if status == "" {
		return nil, 0, fmt.Errorf("status is required")
	}

	if err := validateTaskStatus(status); err != nil {
		return nil, 0, err
	}

	var eventID int64

	// CAS retry loop for optimistic concurrency
	maxRetries := 3
	for attempt := 0; attempt < maxRetries; attempt++ {
		err := store.Transact(db, func(tx *sql.Tx) error {
			version, vErr := store.GetTaskVersionTx(tx, taskID)
			if vErr != nil {
				return fmt.Errorf("failed to get task: %w", vErr)
			}

			lastEventID, uErr := store.UpdateTaskStatusWithEventTx(tx, agentName, taskID, status, version)
			if uErr != nil {
				return uErr
			}
			eventID = lastEventID

			// Release claim if task is completed or blocked (best-effort, log on error)
			if status == completedStatus || status == "blocked" {
				if releaseErr := store.ReleaseTaskClaimTx(tx, agentName, taskID); releaseErr != nil {
					slog.Warn("failed to release task claim",
						"task", taskID, "agent", agentName, "error", releaseErr)
				}
			}

			// Unblock dependent tasks atomically if this task is now completed
			if status == completedStatus {
				_, err := store.UnblockDependentsTx(tx, taskID)
				if err != nil {
					return fmt.Errorf("failed to unblock dependents: %w", err)
				}
			}

			return nil
		})
		if err != nil {
			if errors.Is(err, store.ErrVersionConflict) && attempt < maxRetries-1 {
				continue
			}
			return nil, 0, err
		}
		break
	}

	updatedTask, err := store.GetTask(db, taskID)
	if err != nil {
		return nil, 0, fmt.Errorf("failed to fetch updated task: %w", err)
	}

	return updatedTask, eventID, nil
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
func TaskSetStatusIdempotent(db *sql.DB, agentName, requestID, taskID, status, blockedReason string) (*models.Task, int64, error) {
	if agentName == "" {
		return nil, 0, fmt.Errorf("agent name is required")
	}
	if requestID == "" {
		return nil, 0, fmt.Errorf("request id is required")
	}
	if taskID == "" {
		return nil, 0, fmt.Errorf("task ID is required")
	}
	if status == "" {
		return nil, 0, fmt.Errorf("status is required")
	}

	type idemResult struct {
		EventID int64 `json:"event_id"`
	}

	if err := validateTaskStatus(status); err != nil {
		return nil, 0, err
	}

	r, _, err := store.RunIdempotentWithRetry(
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

			// Release claim if task is completed or blocked (best-effort, log on error)
			if status == completedStatus || status == "blocked" {
				if releaseErr := store.ReleaseTaskClaimTx(tx, agentName, taskID); releaseErr != nil {
					slog.Warn("failed to release task claim",
						"task", taskID, "agent", agentName, "error", releaseErr)
				}
			}

			// Unblock dependent tasks atomically if this task is now completed
			if status == completedStatus {
				_, ubErr := store.UnblockDependentsTx(tx, taskID)
				if ubErr != nil {
					return idemResult{}, fmt.Errorf("failed to unblock dependents: %w", ubErr)
				}
			}

			// Set blocked_reason atomically with status change
			if status == "blocked" && blockedReason != "" {
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

// TaskStart is a convenience: set status to in_progress and focus the task for the agent.
// Returns the updated task, the task_status event id (0 if unchanged), and the agent_focus event id.
func TaskStart(db *sql.DB, agentName, taskID string) (task *models.Task, statusEventID int64, focusEventID int64, err error) {
	if agentName == "" {
		return nil, 0, 0, fmt.Errorf("agent name is required")
	}
	if taskID == "" {
		return nil, 0, 0, fmt.Errorf("task ID is required")
	}

	statusEventID, focusEventID, err = store.StartTaskAndFocus(db, agentName, taskID)
	if err != nil {
		return nil, 0, 0, err
	}

	task, err = store.GetTask(db, taskID)
	if err != nil {
		return nil, 0, 0, fmt.Errorf("failed to fetch task: %w", err)
	}

	return task, statusEventID, focusEventID, nil
}

// TaskStartIdempotent performs TaskStart once per (agent_name, request_id).
// On retries with the same request id, it returns the originally created event ids and current task state.
func TaskStartIdempotent(db *sql.DB, agentName, requestID, taskID string) (task *models.Task, statusEventID int64, focusEventID int64, err error) {
	if agentName == "" {
		return nil, 0, 0, fmt.Errorf("agent name is required")
	}
	if requestID == "" {
		return nil, 0, 0, fmt.Errorf("request id is required")
	}
	if taskID == "" {
		return nil, 0, 0, fmt.Errorf("task ID is required")
	}

	statusEventID, focusEventID, err = store.StartTaskAndFocusIdempotent(db, agentName, requestID, taskID)
	if err != nil {
		return nil, 0, 0, err
	}

	task, err = store.GetTask(db, taskID)
	if err != nil {
		return nil, 0, 0, fmt.Errorf("failed to fetch task: %w", err)
	}

	return task, statusEventID, focusEventID, nil
}

// TaskHeartbeatResult captures heartbeat mutation output.
type TaskHeartbeatResult struct {
	Task    *models.Task `json:"task"`
	EventID int64        `json:"event_id"`
}

// TaskHeartbeatIdempotent refreshes lease expiry for a task claim owner.
func TaskHeartbeatIdempotent(db *sql.DB, agentName, requestID, taskID string, ttlMinutes int) (*TaskHeartbeatResult, error) {
	if agentName == "" {
		return nil, fmt.Errorf("agent name is required")
	}
	if requestID == "" {
		return nil, fmt.Errorf("request id is required")
	}
	if taskID == "" {
		return nil, fmt.Errorf("task ID is required")
	}
	if ttlMinutes <= 0 {
		ttlMinutes = 5
	}

	type idemResult struct {
		EventID int64 `json:"event_id"`
	}

	r, err := store.RunIdempotent(db, agentName, requestID, "task.heartbeat", func(tx *sql.Tx) (idemResult, error) {
		if err := store.HeartbeatTaskTx(tx, agentName, taskID, ttlMinutes); err != nil {
			return idemResult{}, err
		}

		eventID, err := store.InsertEventTx(tx, models.EventKindTaskHeartbeat, agentName, taskID, "Lease heartbeat refreshed", "")
		if err != nil {
			return idemResult{}, fmt.Errorf("failed to append event: %w", err)
		}

		return idemResult{EventID: eventID}, nil
	})
	if err != nil {
		return nil, err
	}

	task, err := store.GetTask(db, taskID)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch task: %w", err)
	}

	return &TaskHeartbeatResult{Task: task, EventID: r.EventID}, nil
}

// TaskGet retrieves a task by ID
func TaskGet(db *sql.DB, taskID string) (*models.Task, error) {
	if taskID == "" {
		return nil, fmt.Errorf("task ID is required")
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
		return nil, 0, fmt.Errorf("agent name is required")
	}
	if requestID == "" {
		return nil, 0, fmt.Errorf("request id is required")
	}
	if taskID == "" {
		return nil, 0, fmt.Errorf("task ID is required")
	}

	type idemResult struct {
		EventID int64 `json:"event_id"`
	}

	r, _, err := store.RunIdempotentWithRetry(
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

// TaskNext returns the next pending tasks in queue order for the given agent.
// Read-only, no idempotency needed.
func TaskNext(db *sql.DB, agentName, projectFilter string, limit int) ([]store.PipelineTask, error) {
	if agentName == "" {
		return nil, fmt.Errorf("agent name is required")
	}

	state, err := store.LoadOrCreateAgentState(db, agentName)
	if err != nil {
		return nil, fmt.Errorf("failed to load agent state: %w", err)
	}

	tasks, err := store.FetchPipelineTasks(db, state.FocusTaskID, agentName, projectFilter, limit)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch pipeline tasks: %w", err)
	}

	return tasks, nil
}

// TaskUnlocks returns tasks that would become unblocked if the given task were completed.
// Read-only, no idempotency needed.
func TaskUnlocks(db *sql.DB, taskID string) ([]store.PipelineTask, error) {
	if taskID == "" {
		return nil, fmt.Errorf("task ID is required")
	}

	tasks, err := store.FetchUnlockedByCompletion(db, taskID)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch unlocked tasks: %w", err)
	}

	return tasks, nil
}

// TaskStats returns task status counts, optionally scoped to a project.
// Read-only, no idempotency needed.
func TaskStats(db *sql.DB, projectFilter string) (*store.TaskStatusCounts, error) {
	counts, err := store.GetTaskStatusCounts(db, projectFilter)
	if err != nil {
		return nil, fmt.Errorf("failed to get task stats: %w", err)
	}

	return counts, nil
}

func validateDependencyArgs(agentName, taskID, dependsOnTaskID string) error {
	if agentName == "" {
		return fmt.Errorf("agent name is required")
	}
	if taskID == "" {
		return fmt.Errorf("task ID is required")
	}
	if dependsOnTaskID == "" {
		return fmt.Errorf("depends_on_task_id is required")
	}
	return nil
}

func taskDependencyMessage(taskID, dependsOnTaskID string, adding bool) string {
	if adding {
		return fmt.Sprintf("Dependency added: %s depends on %s", taskID, dependsOnTaskID)
	}
	return fmt.Sprintf("Dependency removed: %s no longer depends on %s", taskID, dependsOnTaskID)
}

func mutateTaskDependency(
	db *sql.DB,
	agentName,
	taskID,
	dependsOnTaskID,
	eventKind string,
	idempotencyCommand string,
	requestID string,
	mutate func(tx *sql.Tx, taskID, dependsOnTaskID string) error,
	adding bool,
) error {
	if err := validateDependencyArgs(agentName, taskID, dependsOnTaskID); err != nil {
		return err
	}

	message := taskDependencyMessage(taskID, dependsOnTaskID, adding)

	if requestID == "" {
		return store.Transact(db, func(tx *sql.Tx) error {
			if err := mutate(tx, taskID, dependsOnTaskID); err != nil {
				return err
			}

			if _, err := store.InsertEventTx(tx, eventKind, agentName, taskID, message, ""); err != nil {
				return fmt.Errorf("failed to append event: %w", err)
			}

			return nil
		})
	}

	type idemResult struct {
		EventID int64 `json:"event_id"`
	}

	_, err := store.RunIdempotent(db, agentName, requestID, idempotencyCommand, func(tx *sql.Tx) (idemResult, error) {
		if err := mutate(tx, taskID, dependsOnTaskID); err != nil {
			return idemResult{}, err
		}

		eventID, err := store.InsertEventTx(tx, eventKind, agentName, taskID, message, "")
		if err != nil {
			return idemResult{}, fmt.Errorf("failed to append event: %w", err)
		}

		return idemResult{EventID: eventID}, nil
	})

	return err
}

// TaskAddDependency adds a dependency relationship between two tasks.
func TaskAddDependency(db *sql.DB, agentName, taskID, dependsOnTaskID string) error {
	err := mutateTaskDependency(db, agentName, taskID, dependsOnTaskID, models.EventKindTaskDependencyAdded, "task.add_dependency", "", store.AddTaskDependencyTx, true)
	if err != nil {
		return fmt.Errorf("failed to add dependency: %w", err)
	}
	return nil
}

// TaskAddDependencyIdempotent adds a dependency once per (agent_name, request_id).
func TaskAddDependencyIdempotent(db *sql.DB, agentName, requestID, taskID, dependsOnTaskID string) error {
	if requestID == "" {
		return fmt.Errorf("request id is required")
	}

	return mutateTaskDependency(db, agentName, taskID, dependsOnTaskID, models.EventKindTaskDependencyAdded, "task.add_dependency", requestID, store.AddTaskDependencyTx, true)
}

// TaskRemoveDependency removes a dependency relationship between two tasks.
func TaskRemoveDependency(db *sql.DB, agentName, taskID, dependsOnTaskID string) error {
	err := mutateTaskDependency(db, agentName, taskID, dependsOnTaskID, models.EventKindTaskDependencyRemoved, "task.remove_dependency", "", store.RemoveTaskDependencyTx, false)
	if err != nil {
		return fmt.Errorf("failed to remove dependency: %w", err)
	}
	return nil
}

// TaskRemoveDependencyIdempotent removes a dependency once per (agent_name, request_id).
func TaskRemoveDependencyIdempotent(db *sql.DB, agentName, requestID, taskID, dependsOnTaskID string) error {
	if requestID == "" {
		return fmt.Errorf("request id is required")
	}

	return mutateTaskDependency(db, agentName, taskID, dependsOnTaskID, models.EventKindTaskDependencyRemoved, "task.remove_dependency", requestID, store.RemoveTaskDependencyTx, false)
}

// TaskClaimResult captures the output of a claim-next operation.
type TaskClaimResult struct {
	Task          *models.Task `json:"task"`
	StatusEventID int64        `json:"status_event_id"`
	FocusEventID  int64        `json:"focus_event_id"`
	ClaimEventID  int64        `json:"claim_event_id"`
}

// TaskClaimIdempotent atomically selects the next eligible pending task,
// claims it, sets it in_progress, and focuses the agent — once per request-id.
// Returns nil Task when the queue is empty (not an error).
func TaskClaimIdempotent(db *sql.DB, agentName, requestID, projectID string, ttlMinutes int) (*TaskClaimResult, error) {
	if agentName == "" {
		return nil, fmt.Errorf("agent name is required")
	}
	if requestID == "" {
		return nil, fmt.Errorf("request id is required")
	}
	if ttlMinutes <= 0 {
		ttlMinutes = 5
	}

	r, err := store.RunIdempotent(db, agentName, requestID, "task.claim", func(tx *sql.Tx) (store.ClaimNextTaskResult, error) {
		result, err := store.ClaimNextTaskTx(tx, agentName, projectID, ttlMinutes)
		if err != nil {
			return store.ClaimNextTaskResult{}, err
		}
		return *result, nil
	})
	if err != nil {
		return nil, err
	}

	// Empty queue — success with nil task.
	if r.TaskID == "" {
		return &TaskClaimResult{}, nil
	}

	task, err := store.GetTask(db, r.TaskID)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch claimed task: %w", err)
	}

	return &TaskClaimResult{
		Task:          task,
		StatusEventID: r.StatusEventID,
		FocusEventID:  r.FocusEventID,
		ClaimEventID:  r.ClaimEventID,
	}, nil
}

// TaskCloseResult captures the output of a close operation.
type TaskCloseResult struct {
	Task          *models.Task `json:"task"`
	StatusEventID int64        `json:"status_event_id"`
	CloseEventID  int64        `json:"close_event_id"`
}

var validCloseOutcomes = map[string]string{
	"done":    completedStatus,
	"blocked": "blocked",
}

// TaskCloseIdempotent atomically closes a task (status + summary event),
// once per request-id. Outcome must be "done" or "blocked".
func TaskCloseIdempotent(db *sql.DB, agentName, requestID, taskID, outcome, summary, label, blockedReason string) (*TaskCloseResult, error) {
	if agentName == "" {
		return nil, fmt.Errorf("agent name is required")
	}
	if requestID == "" {
		return nil, fmt.Errorf("request id is required")
	}
	if taskID == "" {
		return nil, fmt.Errorf("task ID is required")
	}
	if summary == "" {
		return nil, fmt.Errorf("summary is required")
	}

	status, ok := validCloseOutcomes[outcome]
	if !ok {
		return nil, fmt.Errorf("invalid outcome '%s': must be done or blocked", outcome)
	}

	r, _, err := store.RunIdempotentWithRetry(
		db, agentName, requestID, "task.close",
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
