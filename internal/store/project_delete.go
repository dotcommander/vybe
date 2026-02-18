package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// DeleteProjectTx deletes a project by ID inside an existing transaction.
// Before deleting, it clears project_id references in tasks, events, and agent_state
// to maintain referential integrity (no FK constraints on these columns).
func DeleteProjectTx(tx *sql.Tx, projectID string) error {
	if projectID == "" {
		return errors.New("project ID is required")
	}

	// Clear tasks.project_id
	if _, err := tx.ExecContext(context.Background(), `UPDATE tasks SET project_id = NULL WHERE project_id = ?`, projectID); err != nil {
		return fmt.Errorf("failed to clear tasks project_id: %w", err)
	}

	// Clear events.project_id
	if _, err := tx.ExecContext(context.Background(), `UPDATE events SET project_id = NULL WHERE project_id = ?`, projectID); err != nil {
		return fmt.Errorf("failed to clear events project_id: %w", err)
	}

	// Clear agent_state.focus_project_id
	if _, err := tx.ExecContext(context.Background(), `UPDATE agent_state SET focus_project_id = NULL WHERE focus_project_id = ?`, projectID); err != nil {
		return fmt.Errorf("failed to clear agent_state focus_project_id: %w", err)
	}

	// Delete the project
	result, err := tx.ExecContext(context.Background(), `DELETE FROM projects WHERE id = ?`, projectID)
	if err != nil {
		return fmt.Errorf("failed to delete project: %w", err)
	}

	ra, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to check rows affected: %w", err)
	}
	if ra == 0 {
		return fmt.Errorf("project not found: %s", projectID)
	}

	return nil
}
