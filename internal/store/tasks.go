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

	err := Transact(db, func(tx *sql.Tx) error {
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

// UpdateTaskStatusWithEventTx updates task status and appends task_status event atomically in-tx.
func UpdateTaskStatusWithEventTx(tx *sql.Tx, agentName, taskID, status string, version int) (int64, error) {
	result, err := tx.ExecContext(context.Background(), `
		UPDATE tasks
		SET status = ?, version = version + 1, updated_at = CURRENT_TIMESTAMP
		WHERE id = ? AND version = ?
	`, status, taskID, version)
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

	eventID, err := InsertEventTx(tx, models.EventKindTaskStatus, agentName, taskID, fmt.Sprintf("Status changed to: %s", status), "")
	if err != nil {
		return 0, fmt.Errorf("failed to append event: %w", err)
	}

	return eventID, nil
}

// UpdateTaskStatus updates a task's status using optimistic concurrency control.
// Returns ErrVersionConflict if the version has changed since read.
func UpdateTaskStatus(db *sql.DB, taskID, status string, version int) error {
	return RetryWithBackoff(func() error {
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

	// Populate dependencies
	deps, err := loadTaskDependencies(q, taskID)
	if err != nil {
		return nil, fmt.Errorf("failed to load task dependencies: %w", err)
	}
	task.DependsOn = deps

	return task, nil
}

// loadTaskDependencies loads dependencies for a task using a querier (db or tx).
func loadTaskDependencies(q Querier, taskID string) ([]string, error) {
	rows, err := q.Query(`
		SELECT depends_on_task_id
		FROM task_dependencies
		WHERE task_id = ?
		ORDER BY created_at ASC
	`, taskID)
	if err != nil {
		return nil, fmt.Errorf("failed to query task dependencies: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var deps []string
	for rows.Next() {
		var depID string
		if err := rows.Scan(&depID); err != nil {
			return nil, fmt.Errorf("failed to scan task dependency: %w", err)
		}
		deps = append(deps, depID)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating task dependencies: %w", err)
	}

	return deps, nil
}

// UpdateTaskPriorityWithEventTx updates task priority and appends task_priority_changed event atomically in-tx.
func UpdateTaskPriorityWithEventTx(tx *sql.Tx, agentName, taskID string, priority, version int) (int64, error) {
	result, err := tx.ExecContext(context.Background(), `
		UPDATE tasks
		SET priority = ?, version = version + 1, updated_at = CURRENT_TIMESTAMP
		WHERE id = ? AND version = ?
	`, priority, taskID, version)
	if err != nil {
		return 0, fmt.Errorf("failed to update task priority: %w", err)
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("failed to get rows affected: %w", err)
	}
	if rowsAffected == 0 {
		return 0, &VersionConflictError{Entity: "task", ID: taskID, Version: version}
	}

	eventID, err := InsertEventTx(tx, models.EventKindTaskPriorityChanged, agentName, taskID, fmt.Sprintf("Priority changed to: %d", priority), "")
	if err != nil {
		return 0, fmt.Errorf("failed to append event: %w", err)
	}

	return eventID, nil
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
	var taskIDs []string
	for rows.Next() {
		scanner := &taskRowScanner{}
		if scanErr := scanner.scan(rows); scanErr != nil {
			return nil, fmt.Errorf("failed to scan task row: %w", scanErr)
		}
		scanner.hydrate()
		task := scanner.getTask()
		tasks = append(tasks, task)
		taskIDs = append(taskIDs, task.ID)
	}

	if rowsErr := rows.Err(); rowsErr != nil {
		return nil, fmt.Errorf("error iterating task rows: %w", rowsErr)
	}

	// Batch-load all dependencies if we have tasks
	if len(taskIDs) > 0 {
		depsMap, depsErr := batchLoadTaskDependencies(db, taskIDs)
		if depsErr != nil {
			return nil, fmt.Errorf("failed to batch-load dependencies: %w", depsErr)
		}

		// Assign dependencies to each task
		for _, task := range tasks {
			task.DependsOn = depsMap[task.ID]
		}
	}

	return tasks, nil
}

// batchLoadTaskDependencies loads dependencies for multiple tasks in batches,
// respecting SQLite's SQLITE_MAX_VARIABLE_NUMBER limit (999).
//
//nolint:gocognit // batch loop with inline IIFE for deferred close; placeholder building adds structural branches that inflate the metric
func batchLoadTaskDependencies(db *sql.DB, taskIDs []string) (map[string][]string, error) {
	depsMap := make(map[string][]string)

	// SQLite default SQLITE_MAX_VARIABLE_NUMBER is 999
	const batchSize = 999

	for i := 0; i < len(taskIDs); i += batchSize {
		end := i + batchSize
		if end > len(taskIDs) {
			end = len(taskIDs)
		}
		batch := taskIDs[i:end]

		// Build IN clause with placeholders
		placeholders := make([]byte, 0, len(batch)*2)
		for j := range batch {
			if j > 0 {
				placeholders = append(placeholders, ',')
			}
			placeholders = append(placeholders, '?')
		}

		// placeholders contains only '?' and ',' — no user input, safe to format.
		//nolint:gosec // G201: placeholders is built from safe byte literals ('?' and ',') only — no user input
		query := fmt.Sprintf(`
			SELECT task_id, depends_on_task_id
			FROM task_dependencies
			WHERE task_id IN (%s)
			ORDER BY task_id, created_at ASC
		`, string(placeholders))

		// Convert batch to []any for query args
		queryArgs := make([]any, len(batch))
		for j, id := range batch {
			queryArgs[j] = id
		}

		if scanErr := func() error {
			rows, err := db.QueryContext(context.Background(), query, queryArgs...)
			if err != nil {
				return fmt.Errorf("failed to query task dependencies batch: %w", err)
			}
			defer func() { _ = rows.Close() }()

			for rows.Next() {
				var taskID, depID string
				if err := rows.Scan(&taskID, &depID); err != nil {
					return fmt.Errorf("failed to scan task dependency: %w", err)
				}
				depsMap[taskID] = append(depsMap[taskID], depID)
			}
			return rows.Err()
		}(); scanErr != nil {
			return nil, scanErr
		}
	}

	return depsMap, nil
}

// SetBlockedReasonTx sets the blocked_reason column for a task.
// Pass empty string to clear.
func SetBlockedReasonTx(tx *sql.Tx, taskID, reason string) error {
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
