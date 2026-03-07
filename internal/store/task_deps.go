package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/dotcommander/vybe/internal/models"
)

// AddTaskDependency adds a dependency relationship between two tasks.
// If the dependency is unresolved (not completed), sets the task to "blocked".
func AddTaskDependency(db *sql.DB, taskID, dependsOnTaskID string) error {
	return Transact(context.Background(), db, func(tx *sql.Tx) error {
		return AddTaskDependencyTx(tx, taskID, dependsOnTaskID)
	})
}

// detectCycleTx performs BFS from dependsOnTaskID following task_dependencies edges.
// If it reaches taskID, adding taskID→dependsOnTaskID would create a cycle.
// Max 1000 nodes to prevent runaway traversals.
func detectCycleTx(tx *sql.Tx, taskID, dependsOnTaskID string) error {
	const maxNodes = 1000

	visited := map[string]bool{dependsOnTaskID: true}
	queue := []string{dependsOnTaskID}
	examined := 0

	for len(queue) > 0 && examined < maxNodes {
		current := queue[0]
		queue = queue[1:]
		examined++

		neighbors, err := queryStringColumn(tx, `
			SELECT depends_on_task_id
			FROM task_dependencies
			WHERE task_id = ?
		`, current)
		if err != nil {
			return fmt.Errorf("failed to query deps during cycle check: %w", err)
		}

		for _, neighbor := range neighbors {
			if neighbor == taskID {
				return fmt.Errorf("dependency cycle detected: adding %s → %s would create a cycle", taskID, dependsOnTaskID)
			}
			if !visited[neighbor] {
				visited[neighbor] = true
				queue = append(queue, neighbor)
			}
		}
	}

	return nil
}

// AddTaskDependencyTx is the in-transaction variant of AddTaskDependency.
//
//nolint:gocyclo // dependency add requires: param validation, both-tasks exist, cycle detection, insert, dep-status check, block-if-unresolved
func AddTaskDependencyTx(tx *sql.Tx, taskID, dependsOnTaskID string) error {
	if taskID == "" {
		return errors.New("task_id is required")
	}
	if dependsOnTaskID == "" {
		return errors.New("depends_on_task_id is required")
	}
	if taskID == dependsOnTaskID {
		return errors.New("task cannot depend on itself")
	}

	// Verify both tasks exist
	var exists int
	err := tx.QueryRowContext(context.Background(), `SELECT COUNT(*) FROM tasks WHERE id = ?`, taskID).Scan(&exists)
	if err != nil {
		return fmt.Errorf("failed to verify task: %w", err)
	}
	if exists == 0 {
		return fmt.Errorf("task not found: %s", taskID)
	}

	err = tx.QueryRowContext(context.Background(), `SELECT COUNT(*) FROM tasks WHERE id = ?`, dependsOnTaskID).Scan(&exists)
	if err != nil {
		return fmt.Errorf("failed to verify dependency task: %w", err)
	}
	if exists == 0 {
		return fmt.Errorf("dependency task not found: %s", dependsOnTaskID)
	}

	// Cycle detection: BFS from dependsOnTaskID to see if we can reach taskID
	if cycleErr := detectCycleTx(tx, taskID, dependsOnTaskID); cycleErr != nil {
		return cycleErr
	}

	// Insert dependency (idempotent: ignore if already exists)
	_, err = tx.ExecContext(context.Background(), `
		INSERT OR IGNORE INTO task_dependencies (task_id, depends_on_task_id)
		VALUES (?, ?)
	`, taskID, dependsOnTaskID)
	if err != nil {
		return fmt.Errorf("failed to insert dependency: %w", err)
	}

	// Check if the dependency is unresolved
	var depStatus string
	err = tx.QueryRowContext(context.Background(), `SELECT status FROM tasks WHERE id = ?`, dependsOnTaskID).Scan(&depStatus)
	if err != nil {
		return fmt.Errorf("failed to get dependency status: %w", err)
	}

	// If dependency is not completed, set task to blocked with reason
	if models.TaskStatus(depStatus) != models.TaskStatusCompleted {
		return blockTaskForDependencyTx(tx, taskID)
	}

	return nil
}

// blockTaskForDependencyTx sets a task to blocked status due to an unresolved dependency.
func blockTaskForDependencyTx(tx *sql.Tx, taskID string) error {
	version, err := GetTaskVersionTx(tx, taskID)
	if err != nil {
		return err
	}
	return casUpdateTaskStatusTx(tx, taskID, string(models.TaskStatusBlocked), string(models.BlockedReasonDependency), version)
}

// RemoveTaskDependency removes a dependency relationship between two tasks.
func RemoveTaskDependency(db *sql.DB, taskID, dependsOnTaskID string) error {
	return Transact(context.Background(), db, func(tx *sql.Tx) error {
		return RemoveTaskDependencyTx(tx, taskID, dependsOnTaskID)
	})
}

// RemoveTaskDependencyTx is the in-transaction variant of RemoveTaskDependency.
// After removing the dependency, if the task is blocked and has no remaining
// unresolved dependencies, it transitions the task to pending.
//
//nolint:gocognit // remove dep + proactive unblock requires status lookup and CAS update — coupled by the same transaction
func RemoveTaskDependencyTx(tx *sql.Tx, taskID, dependsOnTaskID string) error {
	if taskID == "" {
		return errors.New("task_id is required")
	}
	if dependsOnTaskID == "" {
		return errors.New("depends_on_task_id is required")
	}

	_, err := tx.ExecContext(context.Background(), `
		DELETE FROM task_dependencies
		WHERE task_id = ? AND depends_on_task_id = ?
	`, taskID, dependsOnTaskID)
	if err != nil {
		return fmt.Errorf("failed to remove dependency: %w", err)
	}

	// Proactive unblock: if the task is blocked and has no remaining unresolved deps,
	// transition it to pending.
	var status string
	if err := tx.QueryRowContext(context.Background(), `SELECT status FROM tasks WHERE id = ?`, taskID).Scan(&status); err != nil {
		return fmt.Errorf("failed to get task status after dep removal: %w", err)
	}

	if models.TaskStatus(status) == models.TaskStatusBlocked {
		return unblockTaskIfResolvedTx(tx, taskID)
	}

	return nil
}

// unblockTaskIfResolvedTx transitions a blocked task to pending if it has no remaining unresolved dependencies.
func unblockTaskIfResolvedTx(tx *sql.Tx, taskID string) error {
	hasUnresolved, err := HasUnresolvedDependenciesTx(tx, taskID)
	if err != nil {
		return err
	}
	if hasUnresolved {
		return nil
	}

	version, err := GetTaskVersionTx(tx, taskID)
	if err != nil {
		return err
	}
	return casUpdateTaskStatusTx(tx, taskID, string(models.TaskStatusPending), "", version)
}

// queryDependencyIDs runs a query that returns a single string column and collects the results.
// Used by GetTaskDependencies to avoid duplicating the scan loop.
func queryDependencyIDs(db *sql.DB, query, errMsg string, args ...any) ([]string, error) {
	var ids []string

	err := RetryWithBackoff(context.Background(), func() error {
		rows, err := db.QueryContext(context.Background(), query, args...)
		if err != nil {
			return fmt.Errorf("%s: %w", errMsg, err)
		}
		defer func() { _ = rows.Close() }()

		ids = make([]string, 0)
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				return fmt.Errorf("failed to scan dependency id: %w", err)
			}
			ids = append(ids, id)
		}

		return rows.Err()
	})

	if err != nil {
		return nil, err
	}

	return ids, nil
}

// GetTaskDependencies returns the list of task IDs that the given task depends on.
func GetTaskDependencies(db *sql.DB, taskID string) ([]string, error) {
	return queryDependencyIDs(db, `
		SELECT depends_on_task_id
		FROM task_dependencies
		WHERE task_id = ?
		ORDER BY created_at ASC
	`, "failed to query dependencies", taskID)
}

// HasUnresolvedDependenciesTx checks if the task has any unresolved (not completed) dependencies.
func HasUnresolvedDependenciesTx(tx *sql.Tx, taskID string) (bool, error) {
	var count int
	err := tx.QueryRowContext(context.Background(), `
		SELECT COUNT(*)
		FROM task_dependencies td
		JOIN tasks dep ON dep.id = td.depends_on_task_id
		WHERE td.task_id = ? AND dep.status != 'completed'
	`, taskID).Scan(&count)
	if err != nil {
		return false, fmt.Errorf("failed to check unresolved dependencies: %w", err)
	}
	return count > 0, nil
}

// UnblockDependentsTx finds all tasks that depend on the completed task,
// and transitions them from "blocked" to "pending" if all their dependencies are now complete.
// Must be called inside an existing transaction.
// Returns the list of unblocked task IDs (useful for event logging).
func UnblockDependentsTx(tx *sql.Tx, completedTaskID string) ([]string, error) {
	// Find eligible tasks: blocked by dependency, all deps now resolved.
	eligible, err := queryStringColumn(tx, `
		SELECT DISTINCT td.task_id
		FROM task_dependencies td
		JOIN tasks t ON t.id = td.task_id
		WHERE td.depends_on_task_id = ?
		  AND t.status = 'blocked'
		  AND t.blocked_reason = 'dependency'
		  AND NOT EXISTS (
			SELECT 1 FROM task_dependencies td2
			JOIN tasks dep ON dep.id = td2.depends_on_task_id
			WHERE td2.task_id = td.task_id
			  AND td2.depends_on_task_id != ?
			  AND dep.status != 'completed'
		  )
	`, completedTaskID, completedTaskID)
	if err != nil {
		return nil, fmt.Errorf("failed to find unblockable dependents: %w", err)
	}

	if len(eligible) == 0 {
		return []string{}, nil
	}

	// Unblock each via CAS primitive.
	var unblockedIDs []string
	for _, taskID := range eligible {
		version, vErr := GetTaskVersionTx(tx, taskID)
		if vErr != nil {
			return nil, fmt.Errorf("failed to get version for task %s: %w", taskID, vErr)
		}
		if uErr := casUpdateTaskStatusTx(tx, taskID, string(models.TaskStatusPending), "", version); uErr != nil {
			return nil, fmt.Errorf("failed to unblock task %s: %w", taskID, uErr)
		}
		unblockedIDs = append(unblockedIDs, taskID)
	}

	return unblockedIDs, nil
}
