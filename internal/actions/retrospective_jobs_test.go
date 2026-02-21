package actions

import (
	"database/sql"
	"testing"

	"github.com/dotcommander/vybe/internal/models"
	"github.com/dotcommander/vybe/internal/store"
	"github.com/stretchr/testify/require"
)

func TestEnqueueRetrospectiveJobIdempotent_ReplaysSameJob(t *testing.T) {
	db, cleanup := setupTestDBWithCleanup(t)
	defer cleanup()

	_, err := store.LoadOrCreateAgentState(db, "claude")
	require.NoError(t, err)

	job1, err := EnqueueRetrospectiveJobIdempotent(db, "claude", "req-1", "", "sess-1", 5)
	require.NoError(t, err)
	job2, err := EnqueueRetrospectiveJobIdempotent(db, "claude", "req-1", "", "sess-1", 5)
	require.NoError(t, err)

	require.Equal(t, job1.ID, job2.ID)
	require.Equal(t, models.RetrospectiveJobQueued, job1.Status)
	require.Equal(t, job1.SinceEventID, job2.SinceEventID)
	require.Equal(t, job1.UntilEventID, job2.UntilEventID)
}

func TestRunOneRetrospectiveJob_ProcessesQueuedJob(t *testing.T) {
	db, cleanup := setupTestDBWithCleanup(t)
	defer cleanup()

	_, err := store.LoadOrCreateAgentState(db, "claude")
	require.NoError(t, err)

	job, err := EnqueueRetrospectiveJobIdempotent(db, "claude", "req-enqueue", "", "sess-2", 5)
	require.NoError(t, err)
	require.NotEmpty(t, job.ID)

	result, err := RunOneRetrospectiveJob(db, "worker-1", 60)
	require.NoError(t, err)
	require.True(t, result.Processed)
	require.NotNil(t, result.Job)
	require.Equal(t, models.RetrospectiveJobSucceeded, result.Job.Status)

	var loaded *models.RetrospectiveJob
	err = store.Transact(db, func(tx *sql.Tx) error {
		j, getErr := getRetrospectiveJobByIDTx(tx, job.ID)
		if getErr != nil {
			return getErr
		}
		loaded = j
		return nil
	})
	require.NoError(t, err)
	require.Equal(t, models.RetrospectiveJobSucceeded, loaded.Status)
}

func TestRunOneRetrospectiveJob_UsesEnqueueWindow(t *testing.T) {
	db, cleanup := setupTestDBWithCleanup(t)
	defer cleanup()

	_, err := store.LoadOrCreateAgentState(db, "claude")
	require.NoError(t, err)

	for range 2 {
		require.NoError(t, store.Transact(db, func(tx *sql.Tx) error {
			_, e := store.InsertEventTx(tx, models.EventKindToolFailure, "claude", "", "bash failed", "")
			return e
		}))
	}

	job, err := EnqueueRetrospectiveJobIdempotent(db, "claude", "req-window-enqueue", "", "sess-window", 5)
	require.NoError(t, err)
	require.NotZero(t, job.UntilEventID)

	for range 2 {
		require.NoError(t, store.Transact(db, func(tx *sql.Tx) error {
			_, e := store.InsertEventTx(tx, models.EventKindToolFailure, "claude", "", "grep failed", "")
			return e
		}))
	}

	result, err := RunOneRetrospectiveJob(db, "worker-window", 60)
	require.NoError(t, err)
	require.True(t, result.Processed)
	require.Equal(t, models.RetrospectiveJobSucceeded, result.Job.Status)

	bashMem, err := store.GetMemory(db, "repeated_bash_failure", "global", "")
	require.NoError(t, err)
	require.NotNil(t, bashMem)

	grepMem, err := store.GetMemory(db, "repeated_grep_failure", "global", "")
	require.NoError(t, err)
	require.Nil(t, grepMem)
}
