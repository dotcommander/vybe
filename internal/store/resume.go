package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/dotcommander/vybe/internal/models"
)

// scanEventRows extracts the repeated 8-column event scan loop used by all event
// fetch functions. Handles NullString decoding for project_id and metadata.
func scanEventRows(rows *sql.Rows) ([]*models.Event, error) {
	var events []*models.Event
	for rows.Next() {
		var event models.Event
		var eventProjectID sql.NullString
		var metadata sql.NullString
		if err := rows.Scan(
			&event.ID, &event.Kind, &event.AgentName, &eventProjectID,
			&event.TaskID, &event.Message, &metadata, &event.CreatedAt,
		); err != nil {
			return nil, fmt.Errorf("failed to scan event: %w", err)
		}
		if eventProjectID.Valid {
			event.ProjectID = eventProjectID.String
		}
		event.Metadata = decodeEventMetadata(metadata)
		events = append(events, &event)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return events, nil
}

// FetchEventsSince retrieves events after a cursor position.
func FetchEventsSince(db *sql.DB, cursorID int64, limit int, projectID string) ([]*models.Event, error) {
	if limit <= 0 {
		limit = 1000
	}

	var events []*models.Event
	err := RetryWithBackoff(context.Background(), func() error {
		query := `
			SELECT id, kind, agent_name, project_id, task_id, message, metadata, created_at
			FROM events
			WHERE id > ? AND archived_at IS NULL
		`
		args := []any{cursorID}
		if projectID != "" {
			query += " AND " + ProjectOrGlobalScopeClause
			args = append(args, projectID)
		}
		query += " ORDER BY id ASC LIMIT ?"
		args = append(args, limit)

		rows, err := db.QueryContext(context.Background(), query, args...)
		if err != nil {
			return fmt.Errorf("failed to fetch events: %w", err)
		}
		defer func() { _ = rows.Close() }()

		events, err = scanEventRows(rows)
		return err
	})
	if err != nil {
		return nil, err
	}

	return events, nil
}

// FetchSessionEvents retrieves events useful for session retrospective extraction.
func FetchSessionEvents(db *sql.DB, sinceID int64, projectID string, limit int) ([]*models.Event, error) {
	if limit <= 0 {
		limit = 200
	}

	var events []*models.Event
	err := RetryWithBackoff(context.Background(), func() error {
		query := `
			SELECT id, kind, agent_name, project_id, task_id, message, metadata, created_at
			FROM events
			WHERE id > ? AND archived_at IS NULL
			  AND kind IN ('user_prompt', 'reasoning', 'tool_failure', 'task_status', 'progress')
		`
		args := []any{sinceID}
		if projectID != "" {
			query += " AND (project_id = ? OR project_id = '' OR project_id IS NULL)"
			args = append(args, projectID)
		}
		query += " ORDER BY id ASC LIMIT ?"
		args = append(args, limit)

		rows, err := db.QueryContext(context.Background(), query, args...)
		if err != nil {
			return fmt.Errorf("failed to fetch session events: %w", err)
		}
		defer func() { _ = rows.Close() }()

		events, err = scanEventRows(rows)
		return err
	})
	if err != nil {
		return nil, err
	}

	return events, nil
}

type projectFocusUpdate int

const (
	projectFocusPreserve projectFocusUpdate = 0 // do not change focus_project_id
	projectFocusSet      projectFocusUpdate = 1 // set to projectValue
	projectFocusClear    projectFocusUpdate = 2 // set to NULL
)

func applyAgentStateAtomicTx(tx *sql.Tx, agentName string, newCursor int64, focusTaskID string, focusProjectID *string) error {
	var currentVersion int
	err := tx.QueryRowContext(context.Background(), `
		SELECT version FROM agent_state WHERE agent_name = ?
	`, agentName).Scan(&currentVersion)
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("agent state not found: %s", agentName)
	}
	if err != nil {
		return fmt.Errorf("failed to load agent state: %w", err)
	}

	taskSetFlag := 0
	if focusTaskID != "" {
		taskSetFlag = 1
	}

	projectMode := projectFocusPreserve
	projectValue := ""
	if focusProjectID != nil {
		if *focusProjectID == "" {
			projectMode = projectFocusClear
		} else {
			projectMode = projectFocusSet
			projectValue = *focusProjectID
		}
	}

	result, err := tx.ExecContext(context.Background(), `
		UPDATE agent_state
		SET
			last_seen_event_id = MAX(last_seen_event_id, ?),
			last_active_at = CURRENT_TIMESTAMP,
			version = version + 1,
			focus_task_id = CASE WHEN ? = 1 THEN ? ELSE NULL END,
			focus_project_id = CASE
				WHEN ? = 0 THEN focus_project_id
				WHEN ? = 1 THEN ?
				ELSE NULL
			END
		WHERE agent_name = ? AND version = ?
	`, newCursor, taskSetFlag, focusTaskID, projectMode, projectMode, projectValue, agentName, currentVersion)
	if err != nil {
		return fmt.Errorf("failed to update agent state: %w", err)
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to check rows affected: %w", err)
	}
	if rowsAffected == 0 {
		return ErrVersionConflict
	}

	return nil
}

// UpdateAgentStateAtomicTx is the in-transaction variant of UpdateAgentStateAtomic.
func UpdateAgentStateAtomicTx(tx *sql.Tx, agentName string, newCursor int64, focusTaskID string) error {
	return applyAgentStateAtomicTx(tx, agentName, newCursor, focusTaskID, nil)
}

// UpdateAgentStateAtomicWithProjectTx atomically updates cursor, focus task, and project focus.
func UpdateAgentStateAtomicWithProjectTx(tx *sql.Tx, agentName string, newCursor int64, focusTaskID, projectID string) error {
	return applyAgentStateAtomicTx(tx, agentName, newCursor, focusTaskID, &projectID)
}

// UpdateAgentStateAtomic atomically updates cursor and focus task.
func UpdateAgentStateAtomic(db *sql.DB, agentName string, newCursor int64, focusTaskID string) error {
	return Transact(context.Background(), db, func(tx *sql.Tx) error {
		return applyAgentStateAtomicTx(tx, agentName, newCursor, focusTaskID, nil)
	})
}

// UpdateAgentStateAtomicWithProject atomically updates cursor, focus task, and project focus.
func UpdateAgentStateAtomicWithProject(db *sql.DB, agentName string, newCursor int64, focusTaskID, projectID string) error {
	return Transact(context.Background(), db, func(tx *sql.Tx) error {
		return applyAgentStateAtomicTx(tx, agentName, newCursor, focusTaskID, &projectID)
	})
}
