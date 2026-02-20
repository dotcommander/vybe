package store

import (
	"context"
	"database/sql"
	"fmt"
)

// Diagnostic represents a single consistency check finding.
type Diagnostic struct {
	Level           string `json:"level"`                      // "warning" or "error"
	Code            string `json:"code"`
	Message         string `json:"message"`
	SuggestedAction string `json:"suggested_action,omitempty"`
}

// RunDiagnostics performs consistency checks and returns findings.
func RunDiagnostics(db *sql.DB) ([]Diagnostic, error) {
	var diags []Diagnostic

	staleClaims, err := findStaleClaims(db)
	if err != nil {
		return nil, fmt.Errorf("stale claims check: %w", err)
	}
	diags = append(diags, staleClaims...)

	staleFocus, err := findStaleFocus(db)
	if err != nil {
		return nil, fmt.Errorf("stale focus check: %w", err)
	}
	diags = append(diags, staleFocus...)

	zombieClaims, err := findZombieClaims(db)
	if err != nil {
		return nil, fmt.Errorf("zombie claims check: %w", err)
	}
	diags = append(diags, zombieClaims...)

	return diags, nil
}

// findStaleClaims finds tasks that are in_progress with expired claims.
func findStaleClaims(db *sql.DB) ([]Diagnostic, error) {
	rows, err := db.QueryContext(context.Background(), `
		SELECT id, claimed_by
		FROM tasks
		WHERE status = 'in_progress'
		  AND claimed_by IS NOT NULL
		  AND claim_expires_at IS NOT NULL
		  AND claim_expires_at < datetime('now')
	`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var diags []Diagnostic
	for rows.Next() {
		var taskID string
		var claimedBy sql.NullString
		if err := rows.Scan(&taskID, &claimedBy); err != nil {
			return nil, err
		}
		agent := claimedBy.String
		diags = append(diags, Diagnostic{
			Level:           "warning",
			Code:            "STALE_CLAIM",
			Message:         fmt.Sprintf("task %s has expired claim by agent %s", taskID, agent),
			SuggestedAction: fmt.Sprintf("vybe task begin --id %s --agent %s --request-id <new>", taskID, agent),
		})
	}
	return diags, rows.Err()
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

// findZombieClaims finds tasks with claimed_by set but expired claim_expires_at that are not in_progress.
func findZombieClaims(db *sql.DB) ([]Diagnostic, error) {
	rows, err := db.QueryContext(context.Background(), `
		SELECT id, claimed_by, status
		FROM tasks
		WHERE claimed_by IS NOT NULL
		  AND claim_expires_at IS NOT NULL
		  AND claim_expires_at < datetime('now')
		  AND status != 'in_progress'
	`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var diags []Diagnostic
	for rows.Next() {
		var taskID string
		var claimedBy sql.NullString
		var status string
		if err := rows.Scan(&taskID, &claimedBy, &status); err != nil {
			return nil, err
		}
		diags = append(diags, Diagnostic{
			Level:           "warning",
			Code:            "ZOMBIE_CLAIM",
			Message:         fmt.Sprintf("task %s (status: %s) has expired claim by %s", taskID, status, claimedBy.String),
			SuggestedAction: fmt.Sprintf("vybe task begin --id %s --agent %s --request-id <new>", taskID, claimedBy.String),
		})
	}
	return diags, rows.Err()
}
