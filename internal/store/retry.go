package store

import (
	"errors"
	"strings"
	"time"

	"github.com/cenkalti/backoff/v4"
)

// RetryWithBackoff wraps an operation with exponential backoff retry logic.
// Retries on transient SQLite errors (SQLITE_BUSY, "database is locked").
// Does not retry on version conflicts or constraint violations.
func RetryWithBackoff(operation func() error) error {
	b := backoff.NewExponentialBackOff()
	b.InitialInterval = 50 * time.Millisecond
	b.MaxInterval = 2 * time.Second
	b.MaxElapsedTime = 10 * time.Second
	b.RandomizationFactor = 0.1

	return backoff.Retry(func() error {
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
	}, b)
}

// isRetryableError determines if an error should be retried.
//
// Error detection relies on modernc.org/sqlite error message strings.
// If modernc changes its error format in a major version bump, update
// the string matchers below. Current baseline: modernc.org/sqlite v1.45+.
func isRetryableError(err error) bool {
	// Idempotency in-progress rows should be treated as transient contention.
	if errors.Is(err, ErrIdempotencyInProgress) {
		return true
	}

	errStr := err.Error()

	// SQLite busy/locked errors are retryable
	if strings.Contains(errStr, "database is locked") ||
		strings.Contains(errStr, "SQLITE_BUSY") {
		return true
	}

	// Version conflicts and constraint violations are NOT retryable
	if strings.Contains(errStr, "UNIQUE constraint") ||
		strings.Contains(errStr, "FOREIGN KEY constraint") ||
		strings.Contains(errStr, "version conflict") {
		return false
	}

	return false
}

// IsVersionConflict checks if an error is a version conflict
func IsVersionConflict(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(err.Error(), "version conflict")
}

// ErrVersionConflict is returned when optimistic concurrency fails
var ErrVersionConflict = errors.New("version conflict: record was modified by another process")
