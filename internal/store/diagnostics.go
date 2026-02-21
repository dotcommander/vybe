package store

import (
	"context"
	"database/sql"
	"fmt"
)

// Diagnostic represents a single consistency check finding.
type Diagnostic struct {
	Level           string `json:"level"` // "warning" or "error"
	Code            string `json:"code"`
	Message         string `json:"message"`
	SuggestedAction string `json:"suggested_action,omitempty"`
}

// RunDiagnostics performs consistency checks and returns findings.
func RunDiagnostics(db *sql.DB) ([]Diagnostic, error) {
	var diags []Diagnostic

	staleFocus, err := findStaleFocus(db)
	if err != nil {
		return nil, fmt.Errorf("stale focus check: %w", err)
	}
	diags = append(diags, staleFocus...)

	staleInProgress, err := findStaleInProgress(db)
	if err != nil {
		return nil, fmt.Errorf("stale in_progress check: %w", err)
	}
	diags = append(diags, staleInProgress...)

	return diags, nil
}

// findStaleFocus finds agent_state rows pointing to completed or non-existent tasks.
func findStaleFocus(db *sql.DB) ([]Diagnostic, error) {
	rows, err := db.QueryContext(context.Background(), `
		SELECT a.agent_name, a.focus_task_id, COALESCE(t.status, 'not_found') AS task_status
		FROM agent_state a
		LEFT JOIN tasks t ON a.focus_task_id = t.id
		WHERE a.focus_task_id IS NOT NULL
		  AND (t.id IS NULL OR t.status IN ('completed'))
	`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var diags []Diagnostic
	for rows.Next() {
		var agentName, focusTaskID, taskStatus string
		if err := rows.Scan(&agentName, &focusTaskID, &taskStatus); err != nil {
			return nil, err
		}
		msg := fmt.Sprintf("agent %s has focus on %s task %s", agentName, taskStatus, focusTaskID)
		diags = append(diags, Diagnostic{
			Level:           "warning",
			Code:            "STALE_FOCUS",
			Message:         msg,
			SuggestedAction: "next vybe resume will auto-advance focus",
		})
	}
	return diags, rows.Err()
}

// findStaleInProgress finds tasks stuck in in_progress for more than 30 minutes.
func findStaleInProgress(db *sql.DB) ([]Diagnostic, error) {
	rows, err := db.QueryContext(context.Background(), `
		SELECT id, updated_at
		FROM tasks
		WHERE status = 'in_progress'
		  AND updated_at < datetime('now', '-30 minutes')
	`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var diags []Diagnostic
	for rows.Next() {
		var taskID, updatedAt string
		if err := rows.Scan(&taskID, &updatedAt); err != nil {
			return nil, err
		}
		diags = append(diags, Diagnostic{
			Level:           "warning",
			Code:            "STALE_IN_PROGRESS",
			Message:         fmt.Sprintf("task %s has been in_progress since %s", taskID, updatedAt),
			SuggestedAction: fmt.Sprintf("vybe task set-status --id %s --status=pending --request-id <new>", taskID),
		})
	}
	return diags, rows.Err()
}
