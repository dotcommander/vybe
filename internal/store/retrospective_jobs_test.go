package store

import (
	"database/sql"
	"testing"

	"github.com/dotcommander/vybe/internal/models"
	"github.com/stretchr/testify/require"
)

func TestEnqueueRetrospectiveJobTx_DedupByAgentSession(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	var firstID string
	err := Transact(db, func(tx *sql.Tx) error {
		job, enqueueErr := EnqueueRetrospectiveJobTx(tx, "claude", "", "session-1", 10, 20, 5)
		if enqueueErr != nil {
			return enqueueErr
		}
		firstID = job.ID
		require.Equal(t, int64(10), job.SinceEventID)
		require.Equal(t, int64(20), job.UntilEventID)
		return nil
	})
	require.NoError(t, err)

	err = Transact(db, func(tx *sql.Tx) error {
		job, enqueueErr := EnqueueRetrospectiveJobTx(tx, "claude", "", "session-1", 99, 100, 5)
		if enqueueErr != nil {
			return enqueueErr
		}
		require.Equal(t, firstID, job.ID)
		require.Equal(t, int64(10), job.SinceEventID)
		require.Equal(t, int64(20), job.UntilEventID)
		return nil
	})
	require.NoError(t, err)
}

func TestClaimAndCompleteRetrospectiveJobTx(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	var claimedID string
	err := Transact(db, func(tx *sql.Tx) error {
		if _, enqueueErr := EnqueueRetrospectiveJobTx(tx, "claude", "", "session-claim", 0, 42, 5); enqueueErr != nil {
			return enqueueErr
		}
		job, claimErr := ClaimNextRetrospectiveJobTx(tx, "worker-a", 60)
		if claimErr != nil {
			return claimErr
		}
		require.NotNil(t, job)
		require.Equal(t, models.RetrospectiveJobRunning, job.Status)
		claimedID = job.ID
		return nil
	})
	require.NoError(t, err)

	err = Transact(db, func(tx *sql.Tx) error {
		return MarkRetrospectiveJobSucceededTx(tx, claimedID)
	})
	require.NoError(t, err)

	err = Transact(db, func(tx *sql.Tx) error {
		job, getErr := getRetrospectiveJobByIDTx(tx, claimedID)
		if getErr != nil {
			return getErr
		}
		require.Equal(t, models.RetrospectiveJobSucceeded, job.Status)
		require.NotNil(t, job.CompletedAt)
		return nil
	})
	require.NoError(t, err)
}
