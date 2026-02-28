package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
)

// DeleteTaskTx deletes a task by ID inside an existing transaction.
// Foreign key CASCADE handles cleanup of task_dependencies rows.
// Before deleting, collects blocked dependents and unblocks any that
// would have no remaining unresolved dependencies after the delete.
// Returns error if the task does not exist.
func DeleteTaskTx(tx *sql.Tx, agentName, taskID string) error {
	if taskID == "" {
		return errors.New("task ID is required")
	}

	// Guard: verify the task exists.
	var status string
	err := tx.QueryRowContext(context.Background(), `
		SELECT status FROM tasks WHERE id = ?`, taskID).
		Scan(&status)
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("task not found: %s", taskID)
	}
	if err != nil {
		return fmt.Errorf("failed to check task state: %w", err)
	}

	// Collect blocked tasks that depend on this task BEFORE cascade delete
	// removes the dependency rows.
	blockedDeps, err := collectBlockedDependentsTx(tx, taskID)
	if err != nil {
		return fmt.Errorf("collect blocked dependents: %w", err)
	}

	result, err := tx.ExecContext(context.Background(), `DELETE FROM tasks WHERE id = ?`, taskID)
	if err != nil {
		return fmt.Errorf("failed to delete task: %w", err)
	}

	ra, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to check rows affected: %w", err)
	}
	if ra == 0 {
		return fmt.Errorf("task not found: %s", taskID)
	}

	// After CASCADE removed the dep rows for the deleted task, unblock
	// dependents that have no remaining unresolved deps. Set-based to avoid N+1.
	if len(blockedDeps) > 0 {
		// Build placeholders for the IN clause
		placeholders := make([]string, len(blockedDeps))
		args := make([]any, len(blockedDeps))
		for i, id := range blockedDeps {
			placeholders[i] = "?"
			args[i] = id
		}

		query := fmt.Sprintf(`
			UPDATE tasks
			SET status = 'pending', blocked_reason = NULL, version = version + 1
			WHERE id IN (%s)
			  AND status = 'blocked'
			  AND NOT EXISTS (
				  SELECT 1 FROM task_dependencies td2
				  JOIN tasks dep ON dep.id = td2.depends_on_task_id
				  WHERE td2.task_id = tasks.id
					AND dep.status != 'completed'
			  )`, strings.Join(placeholders, ","))

		_, err = tx.ExecContext(context.Background(), query, args...)
		if err != nil {
			return fmt.Errorf("unblock dependents: %w", err)
		}
	}

	return nil
}

// collectBlockedDependentsTx returns IDs of tasks that depend on taskID
// and are currently dependency-blocked (not failure-blocked).
// Only these are eligible for auto-unblock when the dependency is removed.
func collectBlockedDependentsTx(tx *sql.Tx, taskID string) ([]string, error) {
	rows, err := tx.QueryContext(context.Background(), `
		SELECT td.task_id
		FROM task_dependencies td
		JOIN tasks t ON t.id = td.task_id
		WHERE td.depends_on_task_id = ?
		  AND t.status = 'blocked'
		  AND (t.blocked_reason = 'dependency' OR t.blocked_reason IS NULL OR t.blocked_reason = '')`, taskID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}
