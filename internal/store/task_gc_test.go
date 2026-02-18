package store

import (
	"database/sql"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestReleaseExpiredClaims(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	task, err := CreateTask(db, "Claimed task", "", "", 0)
	require.NoError(t, err)

	// Claim with a very short TTL, then expire it manually
	err = Transact(db, func(tx *sql.Tx) error { return ClaimTaskTx(tx, "agent1", task.ID, 1) }) // 1 minute
	require.NoError(t, err)

	// Manually set claim_expires_at to the past
	_, err = db.Exec(`UPDATE tasks SET claim_expires_at = datetime('now', '-1 minute') WHERE id = ?`, task.ID)
	require.NoError(t, err)

	// Run GC
	released, err := ReleaseExpiredClaims(db)
	require.NoError(t, err)
	assert.Equal(t, int64(1), released)

	// Verify claim is released
	updatedTask, err := GetTask(db, task.ID)
	require.NoError(t, err)
	assert.Empty(t, updatedTask.ClaimedBy)
	assert.Nil(t, updatedTask.ClaimedAt)
	assert.Nil(t, updatedTask.ClaimExpiresAt)
}

func TestReleaseExpiredClaims_NonExpiredUntouched(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	task, err := CreateTask(db, "Claimed task", "", "", 0)
	require.NoError(t, err)

	// Claim with a long TTL
	err = Transact(db, func(tx *sql.Tx) error { return ClaimTaskTx(tx, "agent1", task.ID, 60) }) // 60 minutes
	require.NoError(t, err)

	// Run GC — should not release non-expired claims
	released, err := ReleaseExpiredClaims(db)
	require.NoError(t, err)
	assert.Equal(t, int64(0), released)

	// Verify claim is still active
	updatedTask, err := GetTask(db, task.ID)
	require.NoError(t, err)
	assert.Equal(t, "agent1", updatedTask.ClaimedBy)
	assert.NotNil(t, updatedTask.ClaimedAt)
}
