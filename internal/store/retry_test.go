package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestIsVersionConflict(t *testing.T) {
	require.False(t, IsVersionConflict(nil))
	require.True(t, IsVersionConflict(ErrVersionConflict))
	require.True(t, IsVersionConflict(errors.New("wrapped: version conflict while updating")))
	require.False(t, IsVersionConflict(errors.New("database is locked")))
}

func TestIsRetryableError(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		expected bool
	}{
		{"nil", nil, false},
		{"ErrIdempotencyInProgress sentinel", ErrIdempotencyInProgress, true},
		{"IdempotencyInProgressError typed", &IdempotencyInProgressError{AgentName: "a", RequestID: "r", Command: "c"}, true},
		{"VersionConflictError typed", &VersionConflictError{Entity: "task", ID: "t1", Version: 1}, false},
		{"ErrVersionConflict sentinel", ErrVersionConflict, false},
		{"database is locked string", errors.New("database is locked"), true},
		{"SQLITE_BUSY string", errors.New("SQLITE_BUSY"), true},
		{"UNIQUE constraint string", errors.New("UNIQUE constraint failed"), false},
		{"FOREIGN KEY constraint string", errors.New("FOREIGN KEY constraint failed"), false},
		{"version conflict string", errors.New("version conflict while updating"), false},
		{"random error", errors.New("random error"), false},
		{"wrapped database is locked", fmt.Errorf("wrapped: %w", errors.New("database is locked")), true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, isRetryableError(tt.err), "isRetryableError(%v)", tt.err)
		})
	}
}

func TestRetryWithBackoff_RetriesTransientThenSucceeds(t *testing.T) {
	attempts := 0
	err := RetryWithBackoff(context.Background(), func() error {
		attempts++
		if attempts < 3 {
			return errors.New("database is locked")
		}
		return nil
	})
	require.NoError(t, err)
	assert.Equal(t, 3, attempts, "should have retried until success")
}

func TestRetryWithBackoff_StopsOnPermanent(t *testing.T) {
	attempts := 0
	err := RetryWithBackoff(context.Background(), func() error {
		attempts++
		return &VersionConflictError{Entity: "task", ID: "t1", Version: 1}
	})
	require.Error(t, err)
	assert.Equal(t, 1, attempts, "should stop on first permanent error")
	assert.ErrorIs(t, err, ErrVersionConflict)
}

func TestRetryWithBackoff_RespectsContextCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	attempts := 0
	err := RetryWithBackoff(ctx, func() error {
		attempts++
		if attempts == 2 {
			cancel()
		}
		return errors.New("database is locked")
	})
	require.Error(t, err)
	assert.LessOrEqual(t, attempts, 4, "should stop shortly after cancel")
}

func TestConcurrentWriters(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	const numWriters = 10
	const eventsPerWriter = 5

	var wg sync.WaitGroup
	errs := make(chan error, numWriters*eventsPerWriter)

	for i := 0; i < numWriters; i++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			for j := 0; j < eventsPerWriter; j++ {
				err := Transact(context.Background(), db, func(tx *sql.Tx) error {
					_, txErr := InsertEventTx(tx, "concurrent_test",
						fmt.Sprintf("worker-%d", workerID), "",
						fmt.Sprintf("event %d from worker %d", j, workerID), "")
					return txErr
				})
				if err != nil {
					errs <- err
				}
			}
		}(i)
	}

	wg.Wait()
	close(errs)

	for err := range errs {
		t.Errorf("concurrent write failed: %v", err)
	}

	var count int
	require.NoError(t, db.QueryRow("SELECT COUNT(*) FROM events WHERE kind='concurrent_test'").Scan(&count))
	assert.Equal(t, numWriters*eventsPerWriter, count, "all events should be written")
}

func TestConcurrentCASUpdates(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	task, err := CreateTask(db, "CAS Test", "", "", 0)
	require.NoError(t, err)

	const numRounds = 5
	const numContenders = 4

	for round := 0; round < numRounds; round++ {
		current, err := GetTask(db, task.ID)
		require.NoError(t, err)

		var wg sync.WaitGroup
		var successes, conflicts int32

		for c := 0; c < numContenders; c++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				newStatus := "in_progress"
				if current.Status == "in_progress" {
					newStatus = "pending"
				}
				updateErr := UpdateTaskStatus(db, task.ID, newStatus, current.Version)
				if updateErr != nil {
					if IsVersionConflict(updateErr) {
						atomic.AddInt32(&conflicts, 1)
					} else {
						t.Errorf("unexpected error: %v", updateErr)
					}
				} else {
					atomic.AddInt32(&successes, 1)
				}
			}()
		}

		wg.Wait()

		assert.Equal(t, int32(1), successes, "round %d: exactly one contender should succeed", round)
		assert.Equal(t, int32(numContenders-1), conflicts, "round %d: rest should get version conflict", round)
	}

	final, err := GetTask(db, task.ID)
	require.NoError(t, err)
	assert.Equal(t, 1+numRounds, final.Version, "version should have incremented once per round")
}

func BenchmarkConcurrentInserts(b *testing.B) {
	tempDir := b.TempDir()
	dbPath := tempDir + "/bench.db"
	db, err := InitDBWithPath(dbPath)
	if err != nil {
		b.Fatal(err)
	}
	defer func() { _ = db.Close() }()

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			i++
			err := Transact(context.Background(), db, func(tx *sql.Tx) error {
				_, txErr := InsertEventTx(tx, "bench", "bench-agent", "",
					fmt.Sprintf("bench event %d", i), "")
				return txErr
			})
			if err != nil {
				b.Errorf("insert failed: %v", err)
			}
		}
	})
}
