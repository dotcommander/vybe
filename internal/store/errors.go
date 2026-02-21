package store

import (
	"strconv"

	"github.com/dotcommander/vybe/internal/models"
)

// RecoverableError is an alias for models.RecoverableError, retained for
// backward compatibility with callers that reference store.RecoverableError.
type RecoverableError = models.RecoverableError

// VersionConflictError replaces ErrVersionConflict with structured context.
type VersionConflictError struct {
	Entity  string
	ID      string
	Version int
}

func (e *VersionConflictError) Error() string {
	return "version conflict: record was modified by another process"
}
func (e *VersionConflictError) ErrorCode() string { return "VERSION_CONFLICT" }
func (e *VersionConflictError) Context() map[string]string {
	return map[string]string{
		"entity":  e.Entity,
		"id":      e.ID,
		"version": strconv.Itoa(e.Version),
	}
}
func (e *VersionConflictError) SuggestedAction() string {
	return "retry the operation with a new --request-id"
}
func (e *VersionConflictError) Is(target error) bool { return target == ErrVersionConflict }

// IdempotencyInProgressError replaces ErrIdempotencyInProgress with structured context.
type IdempotencyInProgressError struct {
	AgentName string
	RequestID string
	Command   string
}

func (e *IdempotencyInProgressError) Error() string     { return "idempotency in progress" }
func (e *IdempotencyInProgressError) ErrorCode() string { return "IDEMPOTENCY_IN_PROGRESS" }
func (e *IdempotencyInProgressError) Context() map[string]string {
	return map[string]string{
		"agent_name": e.AgentName,
		"request_id": e.RequestID,
		"command":    e.Command,
	}
}
func (e *IdempotencyInProgressError) SuggestedAction() string {
	return "wait and retry, or use a new --request-id"
}
func (e *IdempotencyInProgressError) Is(target error) bool {
	return target == ErrIdempotencyInProgress
}
