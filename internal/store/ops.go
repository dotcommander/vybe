package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
)

// attemptResult holds the outcome of a single idempotent operation attempt.
type attemptResult[T any] struct {
	result   T
	replayed bool
	err      error
	done     bool // true = return this result; false = retryable error, try again
}

// attemptIdempotent executes a single idempotent operation attempt.
// Returns done=true for non-retryable errors or success, done=false for retryable errors.
func attemptIdempotent[T any](
	db *sql.DB,
	agentName, requestID, command string,
	operation func(tx *sql.Tx) (T, error),
) attemptResult[T] {
	tx, err := db.BeginTx(context.Background(), nil)
	if err != nil {
		return attemptResult[T]{err: fmt.Errorf("failed to begin transaction: %w", err), done: true}
	}

	existing, done, err := beginIdempotencyTx(tx, agentName, requestID, command)
	if err != nil {
		_ = tx.Rollback()
		return attemptResult[T]{err: err} // done=false, retryable
	}

	if done {
		var result T
		if unmarshalErr := json.Unmarshal([]byte(existing), &result); unmarshalErr != nil {
			_ = tx.Rollback()
			return attemptResult[T]{err: fmt.Errorf("failed to decode idempotency result: %w", unmarshalErr), done: true}
		}
		if commitErr := tx.Commit(); commitErr != nil {
			return attemptResult[T]{err: fmt.Errorf("failed to commit transaction: %w", commitErr), done: true}
		}
		return attemptResult[T]{result: result, replayed: true, done: true}
	}

	opResult, err := operation(tx)
	if err != nil {
		_ = tx.Rollback()
		return attemptResult[T]{err: err} // retryable
	}

	b, err := json.Marshal(opResult)
	if err != nil {
		_ = tx.Rollback()
		return attemptResult[T]{err: fmt.Errorf("failed to encode idempotency result: %w", err), done: true}
	}

	if err := completeIdempotencyTx(tx, agentName, requestID, string(b)); err != nil {
		_ = tx.Rollback()
		return attemptResult[T]{err: err} // retryable
	}

	if err := tx.Commit(); err != nil {
		return attemptResult[T]{err: fmt.Errorf("failed to commit transaction: %w", err), done: true}
	}

	return attemptResult[T]{result: opResult, done: true}
}

// RunIdempotent executes an idempotent operation once per (agent_name, request_id, command).
// It wraps retry, transaction lifecycle, idempotency begin/replay, completion, and commit.
// The operation callback must perform only transactional work (DB reads/writes via tx).
// Non-transactional side effects (HTTP, file I/O) will re-execute on retry.
func RunIdempotent[T any](db *sql.DB, agentName, requestID, command string, operation func(tx *sql.Tx) (T, error)) (T, error) {
	out, _, err := RunIdempotentWithRetry(db, agentName, requestID, command, 1, nil, operation)
	return out, err
}

// RunIdempotentWithRetry executes an idempotent operation with bounded retry on caller-defined retryable errors.
// Returns replayed=true when the result is loaded from an existing idempotency record.
//
//nolint:revive // argument-limit: generics + idempotency params + retry config all required together
func RunIdempotentWithRetry[T any](
	db *sql.DB,
	agentName, requestID, command string,
	maxAttempts int,
	shouldRetry func(error) bool,
	operation func(tx *sql.Tx) (T, error),
) (result T, replayed bool, err error) {
	if maxAttempts <= 0 {
		maxAttempts = 1
	}

	for attempt := 0; attempt < maxAttempts; attempt++ {
		r := attemptIdempotent(db, agentName, requestID, command, operation)
		if r.done {
			return r.result, r.replayed, r.err
		}
		// Not done = retryable error
		if shouldRetry == nil || !shouldRetry(r.err) || attempt >= maxAttempts-1 {
			return r.result, false, r.err
		}
		// else: continue to next attempt
	}

	return result, false, errors.New("idempotent operation exhausted retry attempts")
}
