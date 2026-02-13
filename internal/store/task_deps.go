package store

import (
	"database/sql"
	"fmt"

	"github.com/dotcommander/vibe/internal/models"
)

// AddTaskDependency adds a dependency relationship between two tasks.
// If the dependency is unresolved (not completed), sets the task to "blocked".
func AddTaskDependency(db *sql.DB, taskID, dependsOnTaskID string) error {
	return Transact(db, func(tx *sql.Tx) error {
		return AddTaskDependencyTx(tx, taskID, dependsOnTaskID)
	})
}

// detectCycleTx performs BFS from dependsOnTaskID following task_dependencies edges.
// If it reaches taskID, adding taskID→dependsOnTaskID would create a cycle.
// Max depth of 50 to prevent runaway traversals.
func detectCycleTx(tx *sql.Tx, taskID, dependsOnTaskID string) error {
	const maxDepth = 50

	visited := map[string]bool{dependsOnTaskID: true}
	queue := []string{dependsOnTaskID}

	for depth := 0; depth < maxDepth && len(queue) > 0; depth++ {
		current := queue[0]
		queue = queue[1:]

		rows, err := tx.Query(`
			SELECT depends_on_task_id
			FROM task_dependencies
			WHERE task_id = ?
		`, current)
		if err != nil {
			return fmt.Errorf("failed to query deps during cycle check: %w", err)
		}

		var neighbors []string
		for rows.Next() {
			var neighbor string
			if err := rows.Scan(&neighbor); err != nil {
				_ = rows.Close()
				return fmt.Errorf("failed to scan dep during cycle check: %w", err)
			}
			neighbors = append(neighbors, neighbor)
		}
		if err := rows.Err(); err != nil {
			_ = rows.Close()
			return fmt.Errorf("error iterating deps during cycle check: %w", err)
		}
		_ = rows.Close()

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
func AddTaskDependencyTx(tx *sql.Tx, taskID, dependsOnTaskID string) error {
	if taskID == "" {
		return fmt.Errorf("task_id is required")
	}
	if dependsOnTaskID == "" {
		return fmt.Errorf("depends_on_task_id is required")
	}
	if taskID == dependsOnTaskID {
		return fmt.Errorf("task cannot depend on itself")
	}

	// Verify both tasks exist
	var exists int
	err := tx.QueryRow(`SELECT COUNT(*) FROM tasks WHERE id = ?`, taskID).Scan(&exists)
	if err != nil {
		return fmt.Errorf("failed to verify task: %w", err)
	}
	if exists == 0 {
		return fmt.Errorf("task not found: %s", taskID)
	}

	err = tx.QueryRow(`SELECT COUNT(*) FROM tasks WHERE id = ?`, dependsOnTaskID).Scan(&exists)
	if err != nil {
		return fmt.Errorf("failed to verify dependency task: %w", err)
	}
	if exists == 0 {
		return fmt.Errorf("dependency task not found: %s", dependsOnTaskID)
	}

	// Cycle detection: BFS from dependsOnTaskID to see if we can reach taskID
	if err := detectCycleTx(tx, taskID, dependsOnTaskID); err != nil {
		return err
	}

	// Insert dependency (idempotent: ignore if already exists)
	_, err = tx.Exec(`
		INSERT OR IGNORE INTO task_dependencies (task_id, depends_on_task_id)
		VALUES (?, ?)
	`, taskID, dependsOnTaskID)
	if err != nil {
		return fmt.Errorf("failed to insert dependency: %w", err)
	}

	// Check if the dependency is unresolved
	var depStatus string
	err = tx.QueryRow(`SELECT status FROM tasks WHERE id = ?`, dependsOnTaskID).Scan(&depStatus)
	if err != nil {
		return fmt.Errorf("failed to get dependency status: %w", err)
	}

	// If dependency is not completed, set task to blocked with reason
	if depStatus != "completed" {
		version, err := GetTaskVersionTx(tx, taskID)
		if err != nil {
			return err
		}

		res, err := tx.Exec(`
			UPDATE tasks
			SET status = 'blocked', blocked_reason = ?, version = version + 1, updated_at = CURRENT_TIMESTAMP
			WHERE id = ? AND version = ?
		`, models.BlockedReasonDependency, taskID, version)
		if err != nil {
			return fmt.Errorf("failed to update task to blocked: %w", err)
		}
		ra, err := res.RowsAffected()
		if err != nil {
			return fmt.Errorf("failed to check blocked rows affected: %w", err)
		}
		if ra == 0 {
			return ErrVersionConflict
		}
	}

	return nil
}

// RemoveTaskDependency removes a dependency relationship between two tasks.
func RemoveTaskDependency(db *sql.DB, taskID, dependsOnTaskID string) error {
	return Transact(db, func(tx *sql.Tx) error {
		return RemoveTaskDependencyTx(tx, taskID, dependsOnTaskID)
	})
}

// RemoveTaskDependencyTx is the in-transaction variant of RemoveTaskDependency.
// After removing the dependency, if the task is blocked and has no remaining
// unresolved dependencies, it transitions the task to pending.
func RemoveTaskDependencyTx(tx *sql.Tx, taskID, dependsOnTaskID string) error {
	if taskID == "" {
		return fmt.Errorf("task_id is required")
	}
	if dependsOnTaskID == "" {
		return fmt.Errorf("depends_on_task_id is required")
	}

	_, err := tx.Exec(`
		DELETE FROM task_dependencies
		WHERE task_id = ? AND depends_on_task_id = ?
	`, taskID, dependsOnTaskID)
	if err != nil {
		return fmt.Errorf("failed to remove dependency: %w", err)
	}

	// Proactive unblock: if the task is blocked and has no remaining unresolved deps,
	// transition it to pending.
	var status string
	if err := tx.QueryRow(`SELECT status FROM tasks WHERE id = ?`, taskID).Scan(&status); err != nil {
		return fmt.Errorf("failed to get task status after dep removal: %w", err)
	}

	if status == "blocked" {
		hasUnresolved, err := HasUnresolvedDependenciesTx(tx, taskID)
		if err != nil {
			return err
		}

		if !hasUnresolved {
			version, err := GetTaskVersionTx(tx, taskID)
			if err != nil {
				return err
			}

			res, err := tx.Exec(`
				UPDATE tasks
				SET status = 'pending', blocked_reason = NULL, version = version + 1, updated_at = CURRENT_TIMESTAMP
				WHERE id = ? AND version = ?
			`, taskID, version)
			if err != nil {
				return fmt.Errorf("failed to unblock task after dep removal: %w", err)
			}
			ra, err := res.RowsAffected()
			if err != nil {
				return fmt.Errorf("failed to check unblock rows affected: %w", err)
			}
			if ra == 0 {
				return ErrVersionConflict
			}
		}
	}

	return nil
}

// GetTaskDependencies returns the list of task IDs that the given task depends on.
func GetTaskDependencies(db *sql.DB, taskID string) ([]string, error) {
	var deps []string

	err := RetryWithBackoff(func() error {
		rows, err := db.Query(`
			SELECT depends_on_task_id
			FROM task_dependencies
			WHERE task_id = ?
			ORDER BY created_at ASC
		`, taskID)
		if err != nil {
			return fmt.Errorf("failed to query dependencies: %w", err)
		}
		defer rows.Close()

		deps = make([]string, 0)
		for rows.Next() {
			var depID string
			if err := rows.Scan(&depID); err != nil {
				return fmt.Errorf("failed to scan dependency: %w", err)
			}
			deps = append(deps, depID)
		}

		return rows.Err()
	})

	if err != nil {
		return nil, err
	}

	return deps, nil
}

// GetBlockedByUnresolved returns the list of dependency task IDs that are not completed.
func GetBlockedByUnresolved(db *sql.DB, taskID string) ([]string, error) {
	var blockedBy []string

	err := RetryWithBackoff(func() error {
		rows, err := db.Query(`
			SELECT td.depends_on_task_id
			FROM task_dependencies td
			JOIN tasks dep ON dep.id = td.depends_on_task_id
			WHERE td.task_id = ? AND dep.status != 'completed'
			ORDER BY td.created_at ASC
		`, taskID)
		if err != nil {
			return fmt.Errorf("failed to query unresolved dependencies: %w", err)
		}
		defer rows.Close()

		blockedBy = make([]string, 0)
		for rows.Next() {
			var depID string
			if err := rows.Scan(&depID); err != nil {
				return fmt.Errorf("failed to scan blocked dependency: %w", err)
			}
			blockedBy = append(blockedBy, depID)
		}

		return rows.Err()
	})

	if err != nil {
		return nil, err
	}

	return blockedBy, nil
}

// HasUnresolvedDependencies checks if the task has any unresolved (not completed) dependencies.
func HasUnresolvedDependencies(db *sql.DB, taskID string) (bool, error) {
	var hasUnresolved bool

	err := RetryWithBackoff(func() error {
		var count int
		err := db.QueryRow(`
			SELECT COUNT(*)
			FROM task_dependencies td
			JOIN tasks dep ON dep.id = td.depends_on_task_id
			WHERE td.task_id = ? AND dep.status != 'completed'
		`, taskID).Scan(&count)
		if err != nil {
			return fmt.Errorf("failed to check unresolved dependencies: %w", err)
		}
		hasUnresolved = count > 0
		return nil
	})

	if err != nil {
		return false, err
	}

	return hasUnresolved, nil
}

// HasUnresolvedDependenciesTx is the in-transaction variant of HasUnresolvedDependencies.
func HasUnresolvedDependenciesTx(tx *sql.Tx, taskID string) (bool, error) {
	var count int
	err := tx.QueryRow(`
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
	// Batch UPDATE: unblock all tasks that:
	// 1. Depend on the completed task
	// 2. Are currently blocked with reason='dependency'
	// 3. Have no other unresolved dependencies
	result, err := tx.Exec(`
		UPDATE tasks
		SET status = 'pending', blocked_reason = NULL, version = version + 1, updated_at = CURRENT_TIMESTAMP
		WHERE id IN (
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
		)
	`, completedTaskID, completedTaskID)
	if err != nil {
		return nil, fmt.Errorf("failed to unblock dependent tasks: %w", err)
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return nil, fmt.Errorf("failed to check unblocked rows affected: %w", err)
	}

	// If no tasks were unblocked, return empty list
	if rowsAffected == 0 {
		return []string{}, nil
	}

	// Fetch the list of unblocked task IDs
	// SQLite doesn't support RETURNING clause, so query separately
	rows, err := tx.Query(`
		SELECT DISTINCT td.task_id
		FROM task_dependencies td
		JOIN tasks t ON t.id = td.task_id
		WHERE td.depends_on_task_id = ?
		  AND t.status = 'pending'
		  AND t.blocked_reason IS NULL
		  AND t.version >= (
			SELECT MAX(version) FROM tasks WHERE id = td.task_id
		  )
		  AND NOT EXISTS (
			SELECT 1 FROM task_dependencies td2
			JOIN tasks dep ON dep.id = td2.depends_on_task_id
			WHERE td2.task_id = td.task_id
			  AND td2.depends_on_task_id != ?
			  AND dep.status != 'completed'
		  )
	`, completedTaskID, completedTaskID)
	if err != nil {
		return nil, fmt.Errorf("failed to query unblocked task IDs: %w", err)
	}

	var unblockedIDs []string
	for rows.Next() {
		var taskID string
		if err := rows.Scan(&taskID); err != nil {
			_ = rows.Close()
			return nil, fmt.Errorf("failed to scan unblocked task ID: %w", err)
		}
		unblockedIDs = append(unblockedIDs, taskID)
	}

	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, fmt.Errorf("error iterating unblocked task IDs: %w", err)
	}
	if err := rows.Close(); err != nil {
		return nil, fmt.Errorf("failed to close unblocked task rows: %w", err)
	}

	return unblockedIDs, nil
}

// UnblockDependents is the standalone variant that wraps UnblockDependentsTx in a transaction.
// Returns the list of unblocked task IDs.
func UnblockDependents(db *sql.DB, completedTaskID string) ([]string, error) {
	var unblockedIDs []string
	err := Transact(db, func(tx *sql.Tx) error {
		ids, txErr := UnblockDependentsTx(tx, completedTaskID)
		if txErr != nil {
			return txErr
		}
		unblockedIDs = ids
		return nil
	})
	return unblockedIDs, err
}
