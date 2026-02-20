package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/dotcommander/vybe/internal/models"
)

// Event payload size constraints enforced by ValidateEventPayload.
const (
	MaxEventKindLength      = 128
	MaxEventAgentNameLength = 128
	MaxEventMessageLength   = 4096
	MaxEventMetadataLength  = 16384

	// ProjectScopeClause filters events to a specific project ONLY (strict).
	// Use with a single ? arg. Apply for queries/archives where only that project's
	// events are wanted (listing, archiving, run summaries, session retrospective).
	ProjectScopeClause = "(project_id = ?)"

	// ProjectOrGlobalScopeClause filters to a specific project plus global (NULL project_id) events.
	// Use with a single ? arg. Apply for resume/brief building and checkpoint lookup where
	// agents need global context alongside project-scoped context.
	ProjectOrGlobalScopeClause = "(project_id = ? OR project_id IS NULL)"
)

// ValidateEventPayload enforces event payload constraints for durability and safety.
func ValidateEventPayload(kind, agentName, message, metadata string) error {
	kind = strings.TrimSpace(kind)
	agentName = strings.TrimSpace(agentName)
	message = strings.TrimSpace(message)

	if kind == "" {
		return errors.New("event kind is required")
	}
	if len(kind) > MaxEventKindLength {
		return fmt.Errorf("event kind exceeds max length (%d)", MaxEventKindLength)
	}
	if agentName == "" {
		return errors.New("agent name is required")
	}
	if len(agentName) > MaxEventAgentNameLength {
		return fmt.Errorf("agent name exceeds max length (%d)", MaxEventAgentNameLength)
	}
	if message == "" {
		return errors.New("event message is required")
	}
	if len(message) > MaxEventMessageLength {
		return fmt.Errorf("event message exceeds max length (%d)", MaxEventMessageLength)
	}
	if metadata != "" {
		if len(metadata) > MaxEventMetadataLength {
			return fmt.Errorf("event metadata exceeds max length (%d)", MaxEventMetadataLength)
		}
		if !json.Valid([]byte(metadata)) {
			return errors.New("event metadata must be valid JSON")
		}
	}

	return nil
}

//nolint:revive // argument-limit: event params (kind, agent, task, msg, metadata) are all required
func insertEventRowTx(tx *sql.Tx, kind, agentName, taskID, message, metadata string) (int64, error) {
	if err := ValidateEventPayload(kind, agentName, message, metadata); err != nil {
		return 0, err
	}

	meta := interface{}(nil)
	if metadata != "" {
		meta = metadata
	}

	projectID, err := resolveEventProjectIDTx(tx, agentName, taskID)
	if err != nil {
		return 0, err
	}

	result, err := tx.ExecContext(context.Background(), `
		INSERT INTO events (kind, agent_name, project_id, task_id, message, metadata)
		VALUES (?, ?, ?, ?, ?, ?)
	`, kind, agentName, projectID, taskID, message, meta)
	if err != nil {
		return 0, fmt.Errorf("failed to insert event: %w", err)
	}

	eventID, err := result.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("failed to get last insert id: %w", err)
	}

	return eventID, nil
}

func resolveEventProjectIDTx(tx *sql.Tx, agentName, taskID string) (any, error) {
	if taskID != "" {
		return resolveProjectFromTaskTx(tx, taskID)
	}
	return resolveProjectFromAgentFocusTx(tx, agentName)
}

func resolveProjectFromTaskTx(tx *sql.Tx, taskID string) (any, error) {
	var taskProjectID sql.NullString
	err := tx.QueryRowContext(context.Background(), `SELECT project_id FROM tasks WHERE id = ?`, taskID).Scan(&taskProjectID)
	if err == nil {
		if taskProjectID.Valid {
			return taskProjectID.String, nil
		}
		return nil, nil
	}
	if err != sql.ErrNoRows {
		return nil, fmt.Errorf("failed to resolve event project from task: %w", err)
	}
	return nil, nil
}

func resolveProjectFromAgentFocusTx(tx *sql.Tx, agentName string) (any, error) {
	if agentName == "" {
		return nil, nil
	}

	var focusProjectID sql.NullString
	err := tx.QueryRowContext(context.Background(), `SELECT focus_project_id FROM agent_state WHERE agent_name = ?`, agentName).Scan(&focusProjectID)
	if err == nil {
		if focusProjectID.Valid {
			return focusProjectID.String, nil
		}
		return nil, nil
	}
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return nil, fmt.Errorf("failed to resolve event project from agent focus: %w", err)
}

// InsertEventTx validates and inserts an event inside an existing transaction.
//
//nolint:revive // argument-limit: event params (kind, agent, task, msg, metadata) are all required
func InsertEventTx(tx *sql.Tx, kind, agentName, taskID, message, metadata string) (int64, error) {
	return insertEventRowTx(tx, kind, agentName, taskID, message, metadata)
}

// InsertEventWithProjectTx inserts an event with an explicit project_id, bypassing
// the automatic project resolution. Used by ingest commands where the source data
// knows the project but the agent_state may not reflect it.
//
//nolint:revive // argument-limit: event params (kind, agent, project, task, msg, metadata) are all required
func InsertEventWithProjectTx(tx *sql.Tx, kind, agentName, projectID, taskID, message, metadata string) (int64, error) {
	if err := ValidateEventPayload(kind, agentName, message, metadata); err != nil {
		return 0, err
	}

	meta := interface{}(nil)
	if metadata != "" {
		meta = metadata
	}

	var projVal interface{}
	if projectID != "" {
		projVal = projectID
	}

	result, err := tx.ExecContext(context.Background(), `
		INSERT INTO events (kind, agent_name, project_id, task_id, message, metadata)
		VALUES (?, ?, ?, ?, ?, ?)
	`, kind, agentName, projVal, taskID, message, meta)
	if err != nil {
		return 0, fmt.Errorf("failed to insert event: %w", err)
	}

	return result.LastInsertId()
}

// AppendEventWithProjectAndMetadataIdempotent inserts an event with explicit project_id,
// idempotent on (agent_name, request_id).
//
//nolint:revive // argument-limit: all 8 event params (agent, req, kind, project, task, msg, metadata) required for idempotent path
func AppendEventWithProjectAndMetadataIdempotent(db *sql.DB, agentName, requestID, kind, projectID, taskID, message, metadata string) (int64, error) {
	if err := ValidateEventPayload(kind, agentName, message, metadata); err != nil {
		return 0, err
	}
	type idemResult struct {
		EventID int64 `json:"event_id"`
	}
	r, err := RunIdempotent(db, agentName, requestID, "events.append_with_project", func(tx *sql.Tx) (idemResult, error) {
		eventID, err := InsertEventWithProjectTx(tx, kind, agentName, projectID, taskID, message, metadata)
		if err != nil {
			return idemResult{}, err
		}
		return idemResult{EventID: eventID}, nil
	})
	if err != nil {
		return 0, err
	}
	return r.EventID, nil
}

// AppendEventIdempotent appends a new event once per (agent_name, request_id).
// On retries with the same request id, it returns the previously-created event id.
//
//nolint:revive // argument-limit: event params (agent, req, kind, task, msg) are all required
func AppendEventIdempotent(db *sql.DB, agentName, requestID, kind, taskID, message string) (int64, error) {
	if err := ValidateEventPayload(kind, agentName, message, ""); err != nil {
		return 0, err
	}
	type idemResult struct {
		EventID int64 `json:"event_id"`
	}
	r, err := RunIdempotent(db, agentName, requestID, "events.append", func(tx *sql.Tx) (idemResult, error) {
		eventID, err := insertEventRowTx(tx, kind, agentName, taskID, message, "")
		if err != nil {
			return idemResult{}, err
		}
		return idemResult{EventID: eventID}, nil
	})
	if err != nil {
		return 0, err
	}
	return r.EventID, nil
}

// AppendEventWithMetadataIdempotent is the metadata variant of AppendEventIdempotent.
//
//nolint:revive // argument-limit: event params (agent, req, kind, task, msg, metadata) are all required
func AppendEventWithMetadataIdempotent(db *sql.DB, agentName, requestID, kind, taskID, message, metadata string) (int64, error) {
	if err := ValidateEventPayload(kind, agentName, message, metadata); err != nil {
		return 0, err
	}
	type idemResult struct {
		EventID int64 `json:"event_id"`
	}
	r, err := RunIdempotent(db, agentName, requestID, "events.append_with_metadata", func(tx *sql.Tx) (idemResult, error) {
		eventID, err := insertEventRowTx(tx, kind, agentName, taskID, message, metadata)
		if err != nil {
			return idemResult{}, err
		}
		return idemResult{EventID: eventID}, nil
	})
	if err != nil {
		return 0, err
	}
	return r.EventID, nil
}

// ArchiveEventsRangeWithSummaryIdempotent marks events in an ID range as archived and appends
// a single summary event for continuity compression.
// When projectID is non-empty, only events matching that project (or with NULL project) are archived.
//
//nolint:revive // argument-limit: all 8 params (agent, req, project, task, from, to, summary) required together
func ArchiveEventsRangeWithSummaryIdempotent(db *sql.DB, agentName, requestID, projectID, taskID string, fromID, toID int64, summary string) (summaryEventID int64, archivedCount int64, err error) {
	if agentName == "" {
		return 0, 0, errors.New("agent name is required")
	}
	if requestID == "" {
		return 0, 0, errors.New("request id is required")
	}
	if fromID <= 0 || toID <= 0 {
		return 0, 0, errors.New("from-id and to-id must be > 0")
	}
	if fromID > toID {
		return 0, 0, errors.New("from-id must be <= to-id")
	}
	summary = strings.TrimSpace(summary)
	if summary == "" {
		return 0, 0, errors.New("summary is required")
	}

	type idemResult struct {
		SummaryEventID int64 `json:"summary_event_id"`
		ArchivedCount  int64 `json:"archived_count"`
	}

	r, err := RunIdempotent(db, agentName, requestID, "events.summarize", func(tx *sql.Tx) (idemResult, error) {
		updateSQL := `
			UPDATE events
			SET archived_at = CURRENT_TIMESTAMP
			WHERE id >= ? AND id <= ? AND archived_at IS NULL
		`
		args := []any{fromID, toID}
		if projectID != "" {
			updateSQL += " AND " + ProjectScopeClause
			args = append(args, projectID)
		}
		if taskID != "" {
			updateSQL += " AND task_id = ?"
			args = append(args, taskID)
		}

		res, txErr := tx.ExecContext(context.Background(), updateSQL, args...)
		if txErr != nil {
			return idemResult{}, fmt.Errorf("failed to archive events: %w", txErr)
		}
		archivedCount, txErr := res.RowsAffected()
		if txErr != nil {
			return idemResult{}, fmt.Errorf("failed to count archived events: %w", txErr)
		}

		metaBytes, txErr := json.Marshal(map[string]any{
			"archived_from_id": fromID,
			"archived_to_id":   toID,
			"archived_count":   archivedCount,
		})
		if txErr != nil {
			return idemResult{}, fmt.Errorf("failed to marshal summary metadata: %w", txErr)
		}

		summaryEventID, txErr := insertEventRowTx(tx, models.EventKindEventsSummary, agentName, taskID, summary, string(metaBytes))
		if txErr != nil {
			return idemResult{}, txErr
		}

		return idemResult{SummaryEventID: summaryEventID, ArchivedCount: archivedCount}, nil
	})
	if err != nil {
		return 0, 0, err
	}

	return r.SummaryEventID, r.ArchivedCount, nil
}

// CountActiveEvents returns the number of non-archived events,
// optionally filtered by project.
func CountActiveEvents(db *sql.DB, projectID string) (int64, error) {
	var count int64
	err := RetryWithBackoff(func() error {
		query := `SELECT COUNT(*) FROM events WHERE archived_at IS NULL`
		args := []any{}
		if projectID != "" {
			query += ` AND ` + ProjectScopeClause
			args = append(args, projectID)
		}
		return db.QueryRowContext(context.Background(), query, args...).Scan(&count)
	})
	if err != nil {
		return 0, fmt.Errorf("count active events: %w", err)
	}
	return count, nil
}

// FindArchiveWindow computes the ID range of active events to archive,
// keeping the most recent keepRecent events active.
// Returns (0, 0, nil) when there are not enough events to archive.
func FindArchiveWindow(db *sql.DB, projectID string, keepRecent int) (fromID, toID int64, err error) {
	if keepRecent < 0 {
		keepRecent = 0
	}
	err = RetryWithBackoff(func() error {
		query := `
			SELECT MIN(id), MAX(id) FROM (
				SELECT id FROM events
				WHERE archived_at IS NULL
		`
		args := []any{}
		if projectID != "" {
			query += ` AND ` + ProjectScopeClause
			args = append(args, projectID)
		}
		query += `
				ORDER BY id ASC
				LIMIT (
					SELECT MAX(COUNT(*) - ?, 0) FROM events
					WHERE archived_at IS NULL
		`
		if projectID != "" {
			query += ` AND ` + ProjectScopeClause
			args = append(args, keepRecent, projectID)
		} else {
			args = append(args, keepRecent)
		}
		query += `
				)
			)
		`
		var minID, maxID sql.NullInt64
		if scanErr := db.QueryRowContext(context.Background(), query, args...).Scan(&minID, &maxID); scanErr != nil {
			return scanErr
		}
		if !minID.Valid || !maxID.Valid {
			fromID, toID = 0, 0
			return nil
		}
		fromID = minID.Int64
		toID = maxID.Int64
		return nil
	})
	if err != nil {
		return 0, 0, fmt.Errorf("find archive window: %w", err)
	}
	return fromID, toID, nil
}
