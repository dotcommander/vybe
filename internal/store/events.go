package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// Event payload size constraints enforced by ValidateEventPayload.
const (
	MaxEventKindLength      = 128
	MaxEventAgentNameLength = 128
	MaxEventMessageLength   = 4096
	MaxEventMetadataLength  = 16384

	// ProjectScopeClause filters events to a specific project ONLY (strict).
	// Use with a single ? arg. Apply for queries/archives where only that project's
	// events are wanted (listing, archiving, run summaries).
	ProjectScopeClause = "(project_id = ?)"

	// ProjectOrGlobalScopeClause filters to a specific project plus global (NULL project_id) events.
	// Use with a single ? arg. Apply for resume/brief building and checkpoint lookup where
	// agents need global context alongside project-scoped context.
	ProjectOrGlobalScopeClause = "(project_id = ? OR project_id IS NULL)"
)

type eventIDResult struct {
	EventID int64 `json:"event_id"`
}

// ValidateEventPayload enforces event payload constraints for durability and safety.
func ValidateEventPayload(kind, agentName, message, metadata string) error {
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
	kind = strings.TrimSpace(kind)
	agentName = strings.TrimSpace(agentName)
	message = strings.TrimSpace(message)

	if err := ValidateEventPayload(kind, agentName, message, metadata); err != nil {
		return 0, err
	}

	meta := any(nil)
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

	meta := any(nil)
	if metadata != "" {
		meta = metadata
	}

	var projVal any
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

func appendEventIdempotentResult(
	db *sql.DB,
	agentName, requestID, command, kind, message, metadata string,
	insert func(tx *sql.Tx) (int64, error),
) (int64, error) {
	if err := ValidateEventPayload(kind, agentName, message, metadata); err != nil {
		return 0, err
	}

	r, err := RunIdempotent(context.Background(), db, agentName, requestID, command, func(tx *sql.Tx) (eventIDResult, error) {
		eventID, err := insert(tx)
		if err != nil {
			return eventIDResult{}, err
		}
		return eventIDResult{EventID: eventID}, nil
	})
	if err != nil {
		return 0, err
	}
	return r.EventID, nil
}

// AppendEventWithProjectAndMetadataIdempotent inserts an event with explicit project_id,
// idempotent on (agent_name, request_id).
//
//nolint:revive // argument-limit: all 8 event params (agent, req, kind, project, task, msg, metadata) required for idempotent path
func AppendEventWithProjectAndMetadataIdempotent(db *sql.DB, agentName, requestID, kind, projectID, taskID, message, metadata string) (int64, error) {
	return appendEventIdempotentResult(db, agentName, requestID, "events.append_with_project", kind, message, metadata, func(tx *sql.Tx) (int64, error) {
		return InsertEventWithProjectTx(tx, kind, agentName, projectID, taskID, message, metadata)
	})
}

// AppendEventIdempotent appends a new event once per (agent_name, request_id).
// On retries with the same request id, it returns the previously-created event id.
//
//nolint:revive // argument-limit: event params (agent, req, kind, task, msg) are all required
func AppendEventIdempotent(db *sql.DB, agentName, requestID, kind, taskID, message string) (int64, error) {
	return appendEventIdempotentResult(db, agentName, requestID, "events.append", kind, message, "", func(tx *sql.Tx) (int64, error) {
		return insertEventRowTx(tx, kind, agentName, taskID, message, "")
	})
}

// AppendEventWithMetadataIdempotent is the metadata variant of AppendEventIdempotent.
//
//nolint:revive // argument-limit: event params (agent, req, kind, task, msg, metadata) are all required
func AppendEventWithMetadataIdempotent(db *sql.DB, agentName, requestID, kind, taskID, message, metadata string) (int64, error) {
	return appendEventIdempotentResult(db, agentName, requestID, "events.append_with_metadata", kind, message, metadata, func(tx *sql.Tx) (int64, error) {
		return insertEventRowTx(tx, kind, agentName, taskID, message, metadata)
	})
}
