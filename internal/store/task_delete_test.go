package store

import (
	"context"
	"database/sql"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDeleteTask(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	task, err := CreateTask(db, "Delete me", "", "", 0)
	require.NoError(t, err)

	err = Transact(context.Background(), db, func(tx *sql.Tx) error {
		return DeleteTaskTx(tx, "agent1", task.ID)
	})
	require.NoError(t, err)

	// Verify task is gone
	_, err = GetTask(db, task.ID)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}

func TestDeleteTask_NotFound(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	err := Transact(context.Background(), db, func(tx *sql.Tx) error {
		return DeleteTaskTx(tx, "agent1", "nonexistent")
	})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}
