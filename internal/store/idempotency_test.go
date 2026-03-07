package store

import (
	"context"
	"database/sql"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestIdempotency_BeginCompleteReplay(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()
	var err error

	agent := "agent1"
	requestID := "req_1"
	command := "unit.test"
	result := `{"ok":true}`

	tx, err := db.Begin()
	require.NoError(t, err)
	_, done, err := beginIdempotencyTx(tx, agent, requestID, command)
	require.NoError(t, err)
	require.False(t, done)
	require.NoError(t, completeIdempotencyTx(tx, agent, requestID, result))
	require.NoError(t, tx.Commit())

	tx2, err := db.Begin()
	require.NoError(t, err)
	existing, done, err := beginIdempotencyTx(tx2, agent, requestID, command)
	require.NoError(t, err)
	require.True(t, done)
	require.Equal(t, result, existing)
	require.NoError(t, tx2.Rollback())
}

func TestIdempotency_InProgressIsRetryable(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()
	var err error

	agent := "agent1"
	requestID := "req_inflight"
	command := "unit.inflight"

	// Simulate a broken writer that committed an empty result_json row.
	_, err = db.Exec(`INSERT INTO idempotency (agent_name, request_id, command, result_json) VALUES (?, ?, ?, '')`, agent, requestID, command)
	require.NoError(t, err)

	tx, err := db.Begin()
	require.NoError(t, err)
	_, done, err := beginIdempotencyTx(tx, agent, requestID, command)
	require.Error(t, err)
	require.False(t, done)
	require.ErrorIs(t, err, ErrIdempotencyInProgress)
	require.NoError(t, tx.Rollback())

	require.True(t, isRetryableError(err))
}

func TestRunIdempotentWithRetry_ExhaustsAttempts(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	attempts := 0
	_, _, err := RunIdempotentWithRetry(context.Background(), db,
		"agent1", "req_exhaust", "unit.exhaust",
		3, defaultRetryPredicate,
		func(tx *sql.Tx) (string, error) {
			attempts++
			return "", ErrIdempotencyInProgress
		},
	)
	require.Error(t, err)
	require.Equal(t, 3, attempts, "should exhaust all attempts")
}

func TestRunIdempotentWithRetry_SucceedsOnRetry(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	attempts := 0
	result, replayed, err := RunIdempotentWithRetry(context.Background(), db,
		"agent1", "req_retry_ok", "unit.retry_ok",
		5, defaultRetryPredicate,
		func(tx *sql.Tx) (string, error) {
			attempts++
			if attempts < 3 {
				return "", ErrIdempotencyInProgress
			}
			return "success", nil
		},
	)
	require.NoError(t, err)
	require.Equal(t, "success", result)
	require.False(t, replayed)
	require.Equal(t, 3, attempts)
}

func TestRunIdempotentWithRetry_CustomPredicate(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	attempts := 0
	// Custom predicate that rejects ErrIdempotencyInProgress
	neverRetry := func(error) bool { return false }

	_, _, err := RunIdempotentWithRetry(context.Background(), db,
		"agent1", "req_custom_pred", "unit.custom_pred",
		5, neverRetry,
		func(tx *sql.Tx) (string, error) {
			attempts++
			return "", ErrIdempotencyInProgress
		},
	)
	require.Error(t, err)
	require.Equal(t, 1, attempts, "custom predicate should stop after first attempt")
}

func TestRunIdempotentWithRetry_NilPredicateStopsOnFirst(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	attempts := 0
	_, _, err := RunIdempotentWithRetry(context.Background(), db,
		"agent1", "req_nil_pred", "unit.nil_pred",
		5, nil,
		func(tx *sql.Tx) (string, error) {
			attempts++
			return "", ErrIdempotencyInProgress
		},
	)
	require.Error(t, err)
	require.Equal(t, 1, attempts, "nil shouldRetry should stop after first attempt")
}

func TestRunIdempotentWithRetry_ZeroMaxAttemptsDefaultsToOne(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	attempts := 0
	result, _, err := RunIdempotentWithRetry(context.Background(), db,
		"agent1", "req_zero_max", "unit.zero_max",
		0, defaultRetryPredicate,
		func(tx *sql.Tx) (string, error) {
			attempts++
			return "ok", nil
		},
	)
	require.NoError(t, err)
	require.Equal(t, "ok", result)
	require.Equal(t, 1, attempts, "maxAttempts=0 should default to 1")
}

func TestRunIdempotentWithRetry_ReplayedFlag(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	// First call: fresh execution
	result1, replayed1, err := RunIdempotentWithRetry(context.Background(), db,
		"agent1", "req_replay_flag", "unit.replay_flag",
		3, defaultRetryPredicate,
		func(tx *sql.Tx) (string, error) {
			return "first", nil
		},
	)
	require.NoError(t, err)
	require.Equal(t, "first", result1)
	require.False(t, replayed1, "first execution should not be replayed")

	// Second call: same request_id → replay
	result2, replayed2, err := RunIdempotentWithRetry(context.Background(), db,
		"agent1", "req_replay_flag", "unit.replay_flag",
		3, defaultRetryPredicate,
		func(tx *sql.Tx) (string, error) {
			t.Fatal("should not execute on replay")
			return "", nil
		},
	)
	require.NoError(t, err)
	require.Equal(t, "first", result2)
	require.True(t, replayed2, "second execution should be replayed")
}

func TestRunIdempotent_ReplaySkipsOperation(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()
	var err error

	type result struct {
		ProjectID string `json:"project_id"`
	}

	agent := "agent1"
	requestID := "req_run_idem"
	command := "unit.run_idempotent"

	first, err := RunIdempotent(context.Background(), db, agent, requestID, command, func(tx *sql.Tx) (result, error) {
		projectID := generateProjectID()
		_, execErr := tx.Exec(`
			INSERT INTO projects (id, name, metadata, created_at)
			VALUES (?, ?, NULL, CURRENT_TIMESTAMP)
		`, projectID, "project-a")
		if execErr != nil {
			return result{}, execErr
		}
		return result{ProjectID: projectID}, nil
	})
	require.NoError(t, err)
	require.NotEmpty(t, first.ProjectID)

	second, err := RunIdempotent(context.Background(), db, agent, requestID, command, func(tx *sql.Tx) (result, error) {
		t.Fatalf("operation should not run on replay")
		return result{}, nil
	})
	require.NoError(t, err)
	require.Equal(t, first.ProjectID, second.ProjectID)

	var projectCount int
	require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM projects`).Scan(&projectCount))
	require.Equal(t, 1, projectCount)
}
