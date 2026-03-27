package actions

import (
	"context"
	"database/sql"
	"testing"

	"github.com/dotcommander/vybe/internal/store"
	"github.com/stretchr/testify/require"
)

func TestAutoSummarizeEventsIdempotent(t *testing.T) {
	db, cleanup := setupTestDBWithCleanup(t)
	defer cleanup()

	_, err := store.LoadOrCreateAgentState(db, "test-agent")
	require.NoError(t, err)

	// Below threshold — no-op
	for range 5 {
		require.NoError(t, store.Transact(context.Background(), db, func(tx *sql.Tx) error {
			_, e := store.InsertEventTx(tx, "note", "test-agent", "", "event", "")
			return e
		}))
	}

	summaryID, archived, err := AutoSummarizeEventsIdempotent(db, "test-agent", "req-sum-1", "", 200, 50)
	require.NoError(t, err)
	require.Equal(t, int64(0), summaryID)
	require.Equal(t, int64(0), archived)
}

func TestAutoPruneArchivedEventsIdempotent(t *testing.T) {
	db, cleanup := setupTestDBWithCleanup(t)
	defer cleanup()

	_, err := store.LoadOrCreateAgentState(db, "test-agent")
	require.NoError(t, err)

	for range 3 {
		require.NoError(t, store.Transact(context.Background(), db, func(tx *sql.Tx) error {
			_, e := store.InsertEventTx(tx, "note", "test-agent", "", "event", "")
			return e
		}))
	}

	_, _, err = store.ArchiveEventsRangeWithSummaryIdempotent(db, "test-agent", "req-archive-prune", "", "", 1, 2, "compressed")
	require.NoError(t, err)

	_, err = db.Exec(`UPDATE events SET archived_at = datetime('now', '-40 days') WHERE id IN (1,2)`)
	require.NoError(t, err)

	deleted, err := AutoPruneArchivedEventsIdempotent(db, "test-agent", "req-prune-action", "", 30, 10)
	require.NoError(t, err)
	require.Equal(t, int64(2), deleted)
}
