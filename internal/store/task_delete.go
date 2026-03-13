package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// DeleteTaskTx deletes a task by ID inside an existing transaction.
// Foreign key CASCADE handles cleanup of task_dependencies rows.
// Returns error if the task does not exist.
func DeleteTaskTx(tx *sql.Tx, agentName, taskID string) error {
	if taskID == "" {
		return errors.New("task ID is required")
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

	return nil
}
