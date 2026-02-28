package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/dotcommander/vybe/internal/models"
)

// AgentCursorFocus holds the persisted cursor position and focus pointers for an agent.
type AgentCursorFocus struct {
	Cursor    int64
	TaskID    string
	ProjectID string
}

func ensureAgentStateTx(tx *sql.Tx, agentName string) error {
	if _, err := tx.ExecContext(context.Background(), `
		INSERT OR IGNORE INTO agent_state (agent_name, last_seen_event_id, version, last_active_at)
		VALUES (?, 0, 1, ?)
	`, agentName, time.Now()); err != nil {
		return fmt.Errorf("failed to ensure agent state: %w", err)
	}
	return nil
}

func validateProjectExistsTx(tx *sql.Tx, projectID string) error {
	if projectID == "" {
		return nil
	}

	var exists int
	err := tx.QueryRowContext(context.Background(), `SELECT COUNT(*) FROM projects WHERE id = ?`, projectID).Scan(&exists)
	if err != nil {
		return fmt.Errorf("failed to verify project: %w", err)
	}
	if exists == 0 {
		return fmt.Errorf("project not found: %s", projectID)
	}

	return nil
}

func loadAgentStateTx(tx *sql.Tx, agentName string) (models.AgentState, error) {
	var s models.AgentState
	var focusTaskID, focusProjectID sql.NullString
	if err := tx.QueryRowContext(context.Background(), `
		SELECT agent_name, last_seen_event_id, focus_task_id, focus_project_id, version, last_active_at
		FROM agent_state
		WHERE agent_name = ?
	`, agentName).Scan(&s.AgentName, &s.LastSeenEventID, &focusTaskID, &focusProjectID, &s.Version, &s.LastActiveAt); err != nil {
		return models.AgentState{}, fmt.Errorf("failed to load agent state: %w", err)
	}
	if focusTaskID.Valid {
		s.FocusTaskID = focusTaskID.String
	}
	if focusProjectID.Valid {
		s.FocusProjectID = focusProjectID.String
	}

	return s, nil
}

// LoadAgentCursorAndFocusTx returns the persisted cursor and focus pointers for an agent.
func LoadAgentCursorAndFocusTx(tx *sql.Tx, agentName string) (AgentCursorFocus, error) {
	var out AgentCursorFocus
	var focusTaskID, focusProjectID sql.NullString
	err := tx.QueryRowContext(context.Background(), `
		SELECT last_seen_event_id, focus_task_id, focus_project_id
		FROM agent_state
		WHERE agent_name = ?
	`, agentName).Scan(&out.Cursor, &focusTaskID, &focusProjectID)
	if err != nil {
		return AgentCursorFocus{}, fmt.Errorf("failed to reload agent state: %w", err)
	}

	if focusTaskID.Valid {
		out.TaskID = focusTaskID.String
	}

	if focusProjectID.Valid {
		out.ProjectID = focusProjectID.String
	}

	return out, nil
}

// LoadOrCreateAgentCursorAndFocusTx ensures an agent_state row exists and returns
// the current cursor + focus pointers.
func LoadOrCreateAgentCursorAndFocusTx(tx *sql.Tx, agentName string) (AgentCursorFocus, error) {
	if agentName == "" {
		return AgentCursorFocus{}, errors.New("agent name is required")
	}

	if err := ensureAgentStateTx(tx, agentName); err != nil {
		return AgentCursorFocus{}, err
	}

	return LoadAgentCursorAndFocusTx(tx, agentName)
}

func runFocusEventIdempotent(db *sql.DB, agentName, requestID, command string, setFocus func(tx *sql.Tx) (int64, error)) (int64, error) {
	if agentName == "" {
		return 0, errors.New("agent name is required")
	}

	type idemResult struct {
		EventID int64 `json:"event_id"`
	}

	r, err := RunIdempotent(context.Background(), db, agentName, requestID, command, func(tx *sql.Tx) (idemResult, error) {
		eventID, setErr := setFocus(tx)
		if setErr != nil {
			return idemResult{}, setErr
		}
		return idemResult{EventID: eventID}, nil
	})
	if err != nil {
		return 0, err
	}

	return r.EventID, nil
}

// LoadOrCreateAgentState loads the agent state or creates it if it doesn't exist
func LoadOrCreateAgentState(db *sql.DB, agentName string) (*models.AgentState, error) {
	var state models.AgentState

	err := RetryWithBackoff(context.Background(), func() error {
		return Transact(context.Background(), db, func(tx *sql.Tx) error {
			// Concurrency-safe create: two workers may race to create the same agent.
			// INSERT OR IGNORE ensures only one wins and others fall through to the SELECT.
			if _, err := tx.ExecContext(context.Background(), `
				INSERT OR IGNORE INTO agent_state (agent_name, last_seen_event_id, version, last_active_at)
				VALUES (?, 0, 1, ?)
			`, agentName, time.Now()); err != nil {
				return fmt.Errorf("failed to ensure agent state: %w", err)
			}

			row := tx.QueryRowContext(context.Background(), `
				SELECT agent_name, last_seen_event_id, focus_task_id, focus_project_id, version, last_active_at
				FROM agent_state
				WHERE agent_name = ?
			`, agentName)

			var focusTaskID, focusProjectID sql.NullString
			if err := row.Scan(
				&state.AgentName,
				&state.LastSeenEventID,
				&focusTaskID,
				&focusProjectID,
				&state.Version,
				&state.LastActiveAt,
			); err != nil {
				return fmt.Errorf("failed to load agent state: %w", err)
			}

			if focusTaskID.Valid {
				state.FocusTaskID = focusTaskID.String
			}
			if focusProjectID.Valid {
				state.FocusProjectID = focusProjectID.String
			}

			return nil
		})
	})

	if err != nil {
		return nil, err
	}

	return &state, nil
}

// LoadOrCreateAgentStateIdempotent performs LoadOrCreateAgentState once per (agent_name, request_id).
// On retries with the same request id, it returns the originally stored state.
func LoadOrCreateAgentStateIdempotent(db *sql.DB, agentName, requestID string) (*models.AgentState, error) {
	if agentName == "" {
		return nil, errors.New("agent name is required")
	}
	if requestID == "" {
		return nil, errors.New("request id is required")
	}

	s, err := RunIdempotent(context.Background(), db, agentName, requestID, "agent.init", func(tx *sql.Tx) (models.AgentState, error) {
		if _, err := tx.ExecContext(context.Background(), `
			INSERT OR IGNORE INTO agent_state (agent_name, last_seen_event_id, version, last_active_at)
			VALUES (?, 0, 1, ?)
		`, agentName, time.Now()); err != nil {
			return models.AgentState{}, fmt.Errorf("failed to ensure agent state: %w", err)
		}

		return loadAgentStateTx(tx, agentName)
	})
	if err != nil {
		return nil, err
	}

	return &s, nil
}

// AdvanceAgentCursor advances the agent's cursor position using monotonic advance.
// The cursor will only move forward, never backward.
// Updates last_active_at timestamp and uses optimistic concurrency via version column.
func AdvanceAgentCursor(db *sql.DB, agentName string, newCursor int64) error {
	return Transact(context.Background(), db, func(tx *sql.Tx) error {
		// Load current state for version check
		var currentVersion int
		var currentCursor int64
		err := tx.QueryRowContext(context.Background(), `
			SELECT version, last_seen_event_id
			FROM agent_state
			WHERE agent_name = ?
		`, agentName).Scan(&currentVersion, &currentCursor)

		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("agent state not found: %s", agentName)
		}
		if err != nil {
			return fmt.Errorf("failed to load agent state: %w", err)
		}

		// Monotonic advance: use MAX to ensure cursor never goes backward
		result, err := tx.ExecContext(context.Background(), `
			UPDATE agent_state
			SET last_seen_event_id = MAX(last_seen_event_id, ?),
			    last_active_at = ?,
			    version = version + 1
			WHERE agent_name = ? AND version = ?
		`, newCursor, time.Now(), agentName, currentVersion)
		if err != nil {
			return fmt.Errorf("failed to update agent cursor: %w", err)
		}

		rowsAffected, err := result.RowsAffected()
		if err != nil {
			return fmt.Errorf("failed to check rows affected: %w", err)
		}

		if rowsAffected == 0 {
			return ErrVersionConflict
		}

		return nil
	})
}

// SetAgentFocusTaskWithEventIdempotent performs SetAgentFocusTaskWithEvent once per (agent_name, request_id).
// On retries with the same request id, returns the originally created event id.
func SetAgentFocusTaskWithEventIdempotent(db *sql.DB, agentName, requestID, taskID string) (int64, error) {
	return runFocusEventIdempotent(db, agentName, requestID, "agent.focus", func(tx *sql.Tx) (int64, error) {
		return setAgentFocusTaskWithEventTx(tx, agentName, taskID)
	})
}

func setAgentFocusTaskWithEventTx(tx *sql.Tx, agentName, taskID string) (int64, error) {
	var currentVersion int
	err := tx.QueryRowContext(context.Background(), `
		SELECT version
		FROM agent_state
		WHERE agent_name = ?
	`, agentName).Scan(&currentVersion)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, fmt.Errorf("agent state not found: %s", agentName)
	}
	if err != nil {
		return 0, fmt.Errorf("failed to load agent state: %w", err)
	}

	result, err := tx.ExecContext(context.Background(), `
		UPDATE agent_state
		SET focus_task_id = ?,
		    last_active_at = ?,
		    version = version + 1
		WHERE agent_name = ? AND version = ?
	`, taskID, time.Now(), agentName, currentVersion)
	if err != nil {
		return 0, fmt.Errorf("failed to update focus task: %w", err)
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("failed to check rows affected: %w", err)
	}
	if rowsAffected == 0 {
		return 0, ErrVersionConflict
	}

	eventID, err := InsertEventTx(tx, models.EventKindAgentFocus, agentName, taskID, fmt.Sprintf("Focus set: %s", taskID), "")
	if err != nil {
		return 0, fmt.Errorf("failed to append event: %w", err)
	}

	return eventID, nil
}

// SetAgentFocusProjectWithEventIdempotent performs SetAgentFocusProjectWithEvent once per (agent_name, request_id).
// On retries with the same request id, returns the originally created event id.
func SetAgentFocusProjectWithEventIdempotent(db *sql.DB, agentName, requestID, projectID string) (int64, error) {
	return runFocusEventIdempotent(db, agentName, requestID, "agent.project_focus", func(tx *sql.Tx) (int64, error) {
		return setAgentFocusProjectWithEventTx(tx, agentName, projectID)
	})
}

func setAgentFocusProjectWithEventTx(tx *sql.Tx, agentName, projectID string) (int64, error) {
	if err := ensureAgentStateTx(tx, agentName); err != nil {
		return 0, err
	}
	if err := validateProjectExistsTx(tx, projectID); err != nil {
		return 0, err
	}

	var currentVersion int
	err := tx.QueryRowContext(context.Background(), `
		SELECT version
		FROM agent_state
		WHERE agent_name = ?
	`, agentName).Scan(&currentVersion)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, fmt.Errorf("agent state not found: %s", agentName)
	}
	if err != nil {
		return 0, fmt.Errorf("failed to load agent state: %w", err)
	}

	var result sql.Result
	if projectID != "" {
		result, err = tx.ExecContext(context.Background(), `
			UPDATE agent_state
			SET focus_project_id = ?,
			    last_active_at = ?,
			    version = version + 1
			WHERE agent_name = ? AND version = ?
		`, projectID, time.Now(), agentName, currentVersion)
	} else {
		result, err = tx.ExecContext(context.Background(), `
			UPDATE agent_state
			SET focus_project_id = NULL,
			    last_active_at = ?,
			    version = version + 1
			WHERE agent_name = ? AND version = ?
		`, time.Now(), agentName, currentVersion)
	}
	if err != nil {
		return 0, fmt.Errorf("failed to update focus project: %w", err)
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("failed to check rows affected: %w", err)
	}
	if rowsAffected == 0 {
		return 0, ErrVersionConflict
	}

	msg := "Project focus cleared"
	if projectID != "" {
		msg = fmt.Sprintf("Project focus set: %s", projectID)
	}

	eventID, err := InsertEventTx(tx, models.EventKindAgentProjectFocus, agentName, "", msg, "")
	if err != nil {
		return 0, fmt.Errorf("failed to append event: %w", err)
	}

	return eventID, nil
}
