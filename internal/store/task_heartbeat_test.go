package store

import (
	"database/sql"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestHeartbeatTaskTx_OwnerOnly(t *testing.T) {
	db, err := InitDBWithPath(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	task, err := CreateTask(db, "heartbeat", "", "", 0)
	require.NoError(t, err)

	require.NoError(t, Transact(db, func(tx *sql.Tx) error { return ClaimTaskTx(tx, "agent1", task.ID, 5) }))

	err = Transact(db, func(tx *sql.Tx) error { return HeartbeatTaskTx(tx, "agent2", task.ID, 5) })
	require.ErrorIs(t, err, ErrClaimNotOwned)

	beforeHeartbeat := fetchTaskHeartbeat(t, db, task.ID)
	time.Sleep(1100 * time.Millisecond)
	require.NoError(t, Transact(db, func(tx *sql.Tx) error { return HeartbeatTaskTx(tx, "agent1", task.ID, 5) }))
	afterHeartbeat := fetchTaskHeartbeat(t, db, task.ID)
	require.NotNil(t, afterHeartbeat)
	if beforeHeartbeat != nil {
		require.True(t, !afterHeartbeat.Before(*beforeHeartbeat))
	}
}

func TestClaimTask_AttemptIncrementsOnNewLeaseOnly(t *testing.T) {
	db, err := InitDBWithPath(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	task, err := CreateTask(db, "attempt", "", "", 0)
	require.NoError(t, err)

	require.NoError(t, Transact(db, func(tx *sql.Tx) error { return ClaimTaskTx(tx, "agent1", task.ID, 5) }))
	require.Equal(t, 1, fetchTaskAttempt(t, db, task.ID))

	require.NoError(t, Transact(db, func(tx *sql.Tx) error { return ClaimTaskTx(tx, "agent1", task.ID, 5) }))
	require.Equal(t, 1, fetchTaskAttempt(t, db, task.ID))

	_, err = db.Exec(`UPDATE tasks SET claim_expires_at = datetime('now', '-1 minute') WHERE id = ?`, task.ID)
	require.NoError(t, err)

	require.NoError(t, Transact(db, func(tx *sql.Tx) error { return ClaimTaskTx(tx, "agent2", task.ID, 5) }))
	require.Equal(t, 2, fetchTaskAttempt(t, db, task.ID))
}

func TestReleaseExpiredClaims_ClearsHeartbeat(t *testing.T) {
	db, err := InitDBWithPath(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	task, err := CreateTask(db, "gc", "", "", 0)
	require.NoError(t, err)
	require.NoError(t, Transact(db, func(tx *sql.Tx) error { return ClaimTaskTx(tx, "agent1", task.ID, 5) }))

	_, err = db.Exec(`UPDATE tasks SET claim_expires_at = datetime('now', '-1 minute') WHERE id = ?`, task.ID)
	require.NoError(t, err)

	released, err := ReleaseExpiredClaims(db)
	require.NoError(t, err)
	require.Equal(t, int64(1), released)

	var heartbeat sql.NullTime
	err = db.QueryRow(`SELECT last_heartbeat_at FROM tasks WHERE id = ?`, task.ID).Scan(&heartbeat)
	require.NoError(t, err)
	require.False(t, heartbeat.Valid)
}

func fetchTaskAttempt(t *testing.T, db *sql.DB, taskID string) int {
	t.Helper()
	var attempt int
	err := db.QueryRow(`SELECT attempt FROM tasks WHERE id = ?`, taskID).Scan(&attempt)
	require.NoError(t, err)
	return attempt
}

func fetchTaskHeartbeat(t *testing.T, db *sql.DB, taskID string) *time.Time {
	t.Helper()
	var heartbeat sql.NullTime
	err := db.QueryRow(`SELECT last_heartbeat_at FROM tasks WHERE id = ?`, taskID).Scan(&heartbeat)
	require.NoError(t, err)
	if !heartbeat.Valid {
		return nil
	}
	return &heartbeat.Time
}
