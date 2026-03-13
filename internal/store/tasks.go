package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/dotcommander/vybe/internal/models"
)

// CreateTask creates a new task with the given title and description.
// Task ID is generated using pattern: task_<unix_timestamp>_<random_suffix>
// Initial status is "pending", version starts at 1.
// If projectID is non-empty, the task is associated with that project.
func CreateTask(db *sql.DB, title, description, projectID string, priority int) (*models.Task, error) {
	var task *models.Task

	err := Transact(context.Background(), db, func(tx *sql.Tx) error {
		createdTask, err := CreateTaskTx(tx, title, description, projectID, priority)
		if err != nil {
			return err
		}
		task = createdTask
		return nil
	})

	if err != nil {
		return nil, err
	}

	return task, nil
}

// CreateTaskTx inserts and returns a task inside an existing transaction.
func CreateTaskTx(tx *sql.Tx, title, description, projectID string, priority int) (*models.Task, error) {
	taskID := generateTaskID()
	var projVal any
	if projectID != "" {
		projVal = projectID
	}

	result, err := tx.ExecContext(context.Background(), `
		INSERT INTO tasks (id, title, description, status, priority, project_id, version, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, 1, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
	`, taskID, title, description, "pending", priority, projVal)
	if err != nil {
		return nil, fmt.Errorf("failed to insert task: %w", err)
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return nil, fmt.Errorf("failed to get rows affected: %w", err)
	}
	if rowsAffected == 0 {
		return nil, errors.New("failed to insert task: no rows affected")
	}

	row := tx.QueryRowContext(context.Background(), `
		SELECT id, title, description, status, priority, project_id, blocked_reason, version, created_at, updated_at
		FROM tasks WHERE id = ?
	`, taskID)

	task, err := scanTaskRow(row)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch created task: %w", err)
	}

	return task, nil
}

// GetTaskVersionTx loads only the version for optimistic concurrency updates.
func GetTaskVersionTx(tx *sql.Tx, taskID string) (int, error) {
	var version int
	err := tx.QueryRowContext(context.Background(), `SELECT version FROM tasks WHERE id = ?`, taskID).Scan(&version)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, fmt.Errorf("task not found: %s", taskID)
	}
	if err != nil {
		return 0, fmt.Errorf("failed to get task version: %w", err)
	}

	return version, nil
}

// casUpdateTaskWithEvent executes a CAS UPDATE on the tasks table and appends an event in the
// same transaction. The caller provides the full SQL and its args (the final two args must be
// the task ID and the expected version for the WHERE clause). On zero rows affected the helper
// returns VersionConflictError so callers can retry via RetryWithBackoff.
func casUpdateTaskWithEvent(tx *sql.Tx, agentName, taskID string, version int, updateSQL string, updateArgs []any, eventKind, message string) (int64, error) {
	result, err := tx.ExecContext(context.Background(), updateSQL, updateArgs...)
	if err != nil {
		return 0, fmt.Errorf("failed to update task: %w", err)
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("failed to get rows affected: %w", err)
	}
	if rowsAffected == 0 {
		return 0, &VersionConflictError{Entity: "task", ID: taskID, Version: version}
	}

	eventID, err := InsertEventTx(tx, eventKind, agentName, taskID, message, "")
	if err != nil {
		return 0, fmt.Errorf("failed to append event: %w", err)
	}

	return eventID, nil
}

// UpdateTaskStatusWithEventTx updates task status and appends task_status event atomically in-tx.
//
// blocked_reason behavior (via SQL CASE):
//   - status != "blocked": blocked_reason is cleared to NULL
//   - status == "blocked": blocked_reason is PRESERVED (not set)
//
// Callers that need to SET blocked_reason must follow with SetBlockedReasonTx
// within the same transaction. See CloseTaskTx and TaskSetStatusIdempotent.
func UpdateTaskStatusWithEventTx(tx *sql.Tx, agentName, taskID, status string, version int) (int64, error) {
	return casUpdateTaskWithEvent(tx, agentName, taskID, version,
		`UPDATE tasks
		SET status = ?,
		    blocked_reason = CASE WHEN ? = 'blocked' THEN blocked_reason ELSE NULL END,
		    version = version + 1, updated_at = CURRENT_TIMESTAMP
		WHERE id = ? AND version = ?`,
		[]any{status, status, taskID, version},
		models.EventKindTaskStatus,
		fmt.Sprintf("Status changed to: %s", status),
	)
}

// UpdateTaskStatus updates a task's status using optimistic concurrency control.
// Returns ErrVersionConflict if the version has changed since read.
func UpdateTaskStatus(db *sql.DB, taskID, status string, version int) error {
	return RetryWithBackoff(context.Background(), func() error {
		result, err := db.ExecContext(context.Background(), `
			UPDATE tasks
			SET status = ?, version = version + 1, updated_at = CURRENT_TIMESTAMP
			WHERE id = ? AND version = ?
		`, status, taskID, version)
		if err != nil {
			return fmt.Errorf("failed to update task status: %w", err)
		}

		rowsAffected, err := result.RowsAffected()
		if err != nil {
			return fmt.Errorf("failed to get rows affected: %w", err)
		}

		if rowsAffected == 0 {
			return &VersionConflictError{Entity: "task", ID: taskID, Version: version}
		}

		return nil
	})
}

// GetTask retrieves a task by ID
func GetTask(db *sql.DB, taskID string) (*models.Task, error) {
	return getTaskByQuerier(db, taskID)
}

// getTaskTx retrieves a task by ID in an existing transaction.
func getTaskTx(tx *sql.Tx, taskID string) (*models.Task, error) {
	return getTaskByQuerier(tx, taskID)
}

func getTaskByQuerier(q Querier, taskID string) (*models.Task, error) {
	row := q.QueryRow(`
		SELECT id, title, description, status, priority, project_id, blocked_reason, version, created_at, updated_at
		FROM tasks WHERE id = ?
	`, taskID)

	task, err := scanTaskRow(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("task not found: %s", taskID)
	}
	if err != nil {
		return nil, fmt.Errorf("failed to query task: %w", err)
	}

	return task, nil
}

// ListTasks retrieves all tasks, optionally filtered by status, project, and/or priority.
// Empty/negative filters are ignored.
func ListTasks(db *sql.DB, statusFilter, projectFilter string, priorityFilter int) ([]*models.Task, error) {
	query := `SELECT id, title, description, status, priority, project_id, blocked_reason, version, created_at, updated_at FROM tasks WHERE 1=1`
	var args []any

	if statusFilter != "" {
		query += ` AND status = ?`
		args = append(args, statusFilter)
	}
	if projectFilter != "" {
		query += ` AND project_id = ?`
		args = append(args, projectFilter)
	}
	if priorityFilter >= 0 {
		query += ` AND priority = ?`
		args = append(args, priorityFilter)
	}

	query += ` ORDER BY priority DESC, created_at DESC`

	rows, err := db.QueryContext(context.Background(), query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to query tasks: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var tasks []*models.Task
	for rows.Next() {
		scanner := &taskRowScanner{}
		if scanErr := scanner.scan(rows); scanErr != nil {
			return nil, fmt.Errorf("failed to scan task row: %w", scanErr)
		}
		scanner.hydrate()
		tasks = append(tasks, scanner.getTask())
	}

	if rowsErr := rows.Err(); rowsErr != nil {
		return nil, fmt.Errorf("error iterating task rows: %w", rowsErr)
	}

	return tasks, nil
}

// SetBlockedReasonTx sets the blocked_reason column for a task.
// Pass empty string to clear.
func SetBlockedReasonTx(tx *sql.Tx, taskID, reason string) error {
	const maxBlockedReasonLen = 256
	runes := []rune(reason)
	if len(runes) > maxBlockedReasonLen {
		reason = string(runes[:maxBlockedReasonLen])
	}

	var val any
	if reason != "" {
		val = reason
	}
	_, err := tx.ExecContext(context.Background(), `UPDATE tasks SET blocked_reason = ? WHERE id = ?`, val, taskID)
	if err != nil {
		return fmt.Errorf("failed to set blocked_reason: %w", err)
	}
	return nil
}

// generateTaskID generates a task ID using pattern: task_<unix_nano>_<random_hex>.
func generateTaskID() string {
	return generatePrefixedID("task")
}
