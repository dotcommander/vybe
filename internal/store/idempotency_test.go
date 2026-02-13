package store

import (
	"database/sql"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestIdempotency_BeginCompleteReplay(t *testing.T) {
	db, err := InitDBWithPath(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

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
	db, err := InitDBWithPath(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

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

func TestRunIdempotent_ReplaySkipsOperation(t *testing.T) {
	db, err := InitDBWithPath(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	type result struct {
		ProjectID string `json:"project_id"`
	}

	agent := "agent1"
	requestID := "req_run_idem"
	command := "unit.run_idempotent"

	first, err := RunIdempotent(db, agent, requestID, command, func(tx *sql.Tx) (result, error) {
		projectID := GenerateProjectID()
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

	second, err := RunIdempotent(db, agent, requestID, command, func(tx *sql.Tx) (result, error) {
		t.Fatalf("operation should not run on replay")
		return result{}, nil
	})
	require.NoError(t, err)
	require.Equal(t, first.ProjectID, second.ProjectID)

	var projectCount int
	require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM projects`).Scan(&projectCount))
	require.Equal(t, 1, projectCount)
}
