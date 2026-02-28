package store

import (
	"context"
	"database/sql"
	"testing"

	"github.com/dotcommander/vybe/internal/models"
)

// newBlockedReasonFailure is a test helper that constructs a failure-type
// blocked reason. It mirrors the logic from models.BlockedReasonFailurePrefix
// and is kept here because it is only used in tests.
func newBlockedReasonFailure(reason string) models.BlockedReason {
	return models.BlockedReason(models.BlockedReasonFailurePrefix + reason)
}

// appendEvent is a test helper that inserts an event without metadata.
// It wraps InsertEventTx in a transaction, matching the behaviour of the
// former exported AppendEvent function.
func appendEvent(t *testing.T, db *sql.DB, kind, agentName, taskID, message string) int64 {
	t.Helper()
	var id int64
	if err := Transact(context.Background(), db, func(tx *sql.Tx) error {
		var txErr error
		id, txErr = InsertEventTx(tx, kind, agentName, taskID, message, "")
		return txErr
	}); err != nil {
		t.Fatalf("appendEvent(%q, %q, %q, %q): %v", kind, agentName, taskID, message, err)
	}
	return id
}

// appendEventWithMetadata is a test helper that inserts an event with metadata.
func appendEventWithMetadata(t *testing.T, db *sql.DB, kind, agentName, taskID, message, metadata string) int64 {
	t.Helper()
	var id int64
	if err := Transact(context.Background(), db, func(tx *sql.Tx) error {
		var txErr error
		id, txErr = InsertEventTx(tx, kind, agentName, taskID, message, metadata)
		return txErr
	}); err != nil {
		t.Fatalf("appendEventWithMetadata(%q, %q, %q, %q): %v", kind, agentName, taskID, message, err)
	}
	return id
}
