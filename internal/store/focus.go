package store

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/dotcommander/vybe/internal/models"
)

const (
	statusInProgress = "in_progress"
	statusBlocked    = "blocked"
)

// FocusResult holds the outcome of DetermineFocusTask, including which rule fired.
type FocusResult struct {
	TaskID string
	Rule   string
}

func keepCurrentFocus(db *sql.DB, currentFocusID string) (bool, string) {
	if currentFocusID == "" {
		return false, ""
	}

	task, err := GetTask(db, currentFocusID)
	if err != nil {
		return false, ""
	}
	if task.Status == statusInProgress {
		return true, fmt.Sprintf("rule1: kept in_progress focus on %s", currentFocusID)
	}
	if task.Status == statusBlocked && !task.BlockedReason.IsFailure() {
		var hasUnresolved bool
		depErr := Transact(context.Background(), db, func(tx *sql.Tx) error {
			var txErr error
			hasUnresolved, txErr = HasUnresolvedDependenciesTx(tx, currentFocusID)
			return txErr
		})
		if depErr == nil && hasUnresolved {
			return true, fmt.Sprintf("rule1.5: kept dependency-blocked focus on %s", currentFocusID)
		}
	}

	return false, ""
}

func pickAssignedTask(db *sql.DB, taskID, projectID string) string {
	task, err := GetTask(db, taskID)
	if err != nil {
		return ""
	}
	if task.Status != "pending" {
		return ""
	}

	var hasUnresolved bool
	if depErr := Transact(context.Background(), db, func(tx *sql.Tx) error {
		var txErr error
		hasUnresolved, txErr = HasUnresolvedDependenciesTx(tx, taskID)
		return txErr
	}); depErr != nil || hasUnresolved {
		return ""
	}
	if projectID != "" && task.ProjectID != projectID {
		return ""
	}

	return taskID
}

// DetermineFocusTask selects a task to focus on using deterministic rules.
func DetermineFocusTask(db *sql.DB, agentName, currentFocusID string, deltas []*models.Event, projectID string) (FocusResult, error) {
	_ = agentName

	if keep, rule := keepCurrentFocus(db, currentFocusID); keep {
		return FocusResult{TaskID: currentFocusID, Rule: rule}, nil
	}

	for _, event := range deltas {
		if event.Kind != "task_assigned" || event.TaskID == "" {
			continue
		}
		if taskID := pickAssignedTask(db, event.TaskID, projectID); taskID != "" {
			return FocusResult{
				TaskID: taskID,
				Rule:   fmt.Sprintf("rule2: assigned via task_assigned event for %s", taskID),
			}, nil
		}
	}

	if currentFocusID != "" {
		task, err := GetTask(db, currentFocusID)
		if err == nil && task.Status == "pending" {
			return FocusResult{
				TaskID: currentFocusID,
				Rule:   fmt.Sprintf("rule3: resumed previously-blocked focus on %s", currentFocusID),
			}, nil
		}
	}

	var taskID string
	err := RetryWithBackoff(context.Background(), func() error {
		if projectID != "" {
			err := db.QueryRowContext(context.Background(), `
				SELECT id FROM tasks
				WHERE status = 'pending' AND project_id = ?
				  AND NOT EXISTS (
					SELECT 1 FROM task_dependencies td
					JOIN tasks dep ON dep.id = td.depends_on_task_id
					WHERE td.task_id = tasks.id AND dep.status != 'completed'
				  )
				ORDER BY priority DESC, created_at ASC LIMIT 1
			`, projectID).Scan(&taskID)
			if err == sql.ErrNoRows {
				taskID = ""
				return nil
			}
			return err
		}

		err := db.QueryRowContext(context.Background(), `
			SELECT id FROM tasks
			WHERE status = 'pending'
			  AND NOT EXISTS (
				SELECT 1 FROM task_dependencies td
				JOIN tasks dep ON dep.id = td.depends_on_task_id
				WHERE td.task_id = tasks.id AND dep.status != 'completed'
			  )
			ORDER BY priority DESC, created_at ASC LIMIT 1
		`).Scan(&taskID)
		if err == sql.ErrNoRows {
			taskID = ""
			return nil
		}
		return err
	})
	if err != nil {
		return FocusResult{}, fmt.Errorf("failed to select focus task: %w", err)
	}

	if taskID != "" {
		return FocusResult{TaskID: taskID, Rule: fmt.Sprintf("rule4: selected highest-priority pending task %s", taskID)}, nil
	}

	return FocusResult{TaskID: "", Rule: "rule5: no pending tasks available"}, nil
}
