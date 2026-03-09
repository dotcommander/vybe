package actions

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/dotcommander/vybe/internal/store"
)

// deleteParams groups the parameters for an idempotent delete operation.
type deleteParams struct {
	AgentName    string
	RequestID    string
	ResourceID   string
	ResourceName string // e.g. "task", "project" — used in error messages
	Command      string // idempotency command key
	EventKind    string
	EventTaskID  string
	EventMessage string
	DeleteFn     func(tx *sql.Tx) error
}

func runDeleteIdempotent(db *sql.DB, p deleteParams) (int64, error) {
	if err := validateAgentRequest(p.AgentName, p.RequestID); err != nil {
		return 0, err
	}
	if p.ResourceID == "" {
		return 0, fmt.Errorf("%s ID is required", p.ResourceName)
	}

	r, err := store.RunIdempotent(context.Background(), db, p.AgentName, p.RequestID, p.Command, func(tx *sql.Tx) (eventResult, error) {
		if err := p.DeleteFn(tx); err != nil {
			return eventResult{}, err
		}

		eventID, err := store.InsertEventTx(tx, p.EventKind, p.AgentName, p.EventTaskID, p.EventMessage, "")
		if err != nil {
			return eventResult{}, fmt.Errorf("failed to append event: %w", err)
		}

		return eventResult{EventID: eventID}, nil
	})
	if err != nil {
		return 0, fmt.Errorf("failed to delete %s: %w", p.ResourceName, err)
	}

	return r.EventID, nil
}
