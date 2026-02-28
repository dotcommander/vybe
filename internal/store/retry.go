package store

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/cenkalti/backoff/v4"
	sqlite "modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"
)

// RetryWithBackoff wraps an operation with exponential backoff retry logic.
// Retries on transient SQLite errors (SQLITE_BUSY, "database is locked").
// Does not retry on version conflicts or constraint violations.
func RetryWithBackoff(ctx context.Context, operation func() error) error {
	b := backoff.NewExponentialBackOff()
	b.InitialInterval = 50 * time.Millisecond
	b.MaxInterval = 2 * time.Second
	b.MaxElapsedTime = 10 * time.Second
	b.RandomizationFactor = 0.1

	return backoff.Retry(func() error {
		if err := ctx.Err(); err != nil {
			return backoff.Permanent(err)
		}

		err := operation()
		if err == nil {
			return nil
		}

		// Check if error is retryable
		if isRetryableError(err) {
			return err // Will be retried
		}

		// Non-retryable error: stop immediately
		return backoff.Permanent(err)
	}, backoff.WithContext(b, ctx))
}

// isRetryableError determines if an error should be retried.
//
// Uses typed sqlite.Error code matching first (belt), then string matching
// as a fallback for wrapped errors that may lose the concrete type (suspenders).
func isRetryableError(err error) bool {
	// Idempotency in-progress rows should be treated as transient contention.
	if errors.Is(err, ErrIdempotencyInProgress) {
		return true
	}

	// Version conflicts are NOT retryable — they signal a real concurrency conflict
	// that the caller must handle by reloading and retrying the business logic.
	var vce *VersionConflictError
	if errors.As(err, &vce) {
		return false
	}

	// Typed sqlite error code matching (preferred — immune to string format changes).
	var sqliteErr *sqlite.Error
	if errors.As(err, &sqliteErr) {
		// Primary code is lower 8 bits; extended codes carry subtype in upper bits.
		primaryCode := sqliteErr.Code() & 0xFF
		switch primaryCode {
		case sqlite3.SQLITE_BUSY, sqlite3.SQLITE_LOCKED:
			return true
		case sqlite3.SQLITE_CONSTRAINT:
			return false
		}
	}

	// Fallback: string matching for wrapped errors that lose the concrete type.
	// Baseline: modernc.org/sqlite v1.45+. Update if error format changes.
	errStr := err.Error()
	if strings.Contains(errStr, "database is locked") ||
		strings.Contains(errStr, "SQLITE_BUSY") {
		return true
	}
	if strings.Contains(errStr, "UNIQUE constraint") ||
		strings.Contains(errStr, "FOREIGN KEY constraint") ||
		strings.Contains(errStr, "version conflict") {
		return false
	}

	return false
}

// IsVersionConflict checks if an error is a version conflict.
// Uses typed error matching first, sentinel second, string fallback third.
func IsVersionConflict(err error) bool {
	if err == nil {
		return false
	}
	var vce *VersionConflictError
	if errors.As(err, &vce) {
		return true
	}
	if errors.Is(err, ErrVersionConflict) {
		return true
	}
	return strings.Contains(err.Error(), "version conflict")
}

// ErrVersionConflict is returned when optimistic concurrency fails
var ErrVersionConflict = errors.New("version conflict: record was modified by another process")
