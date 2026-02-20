package store

import (
	"fmt"
	"strconv"

	"github.com/dotcommander/vybe/internal/models"
)

// RecoverableError is an alias for models.RecoverableError, retained for
// backward compatibility with callers that reference store.RecoverableError.
type RecoverableError = models.RecoverableError

// ClaimContentionError replaces ErrClaimContention with structured context.
type ClaimContentionError struct {
	TaskID       string
	CurrentOwner string
	RequestedBy  string
}

func (e *ClaimContentionError) Error() string { return "task already claimed by another agent" }
func (e *ClaimContentionError) ErrorCode() string { return "CLAIM_CONTENTION" }
func (e *ClaimContentionError) Context() map[string]string {
	return map[string]string{
		"task_id":       e.TaskID,
		"current_owner": e.CurrentOwner,
		"requested_by":  e.RequestedBy,
	}
}
func (e *ClaimContentionError) SuggestedAction() string {
	return fmt.Sprintf("vybe task begin --id %s --agent %s --request-id <new>", e.TaskID, e.RequestedBy)
}
func (e *ClaimContentionError) Is(target error) bool { return target == ErrClaimContention }

// ClaimNotOwnedError replaces ErrClaimNotOwned with structured context.
type ClaimNotOwnedError struct {
	TaskID      string
	RequestedBy string
}

func (e *ClaimNotOwnedError) Error() string { return "task claim is not owned by agent" }
func (e *ClaimNotOwnedError) ErrorCode() string { return "CLAIM_NOT_OWNED" }
func (e *ClaimNotOwnedError) Context() map[string]string {
	return map[string]string{
		"task_id":      e.TaskID,
		"requested_by": e.RequestedBy,
	}
}
func (e *ClaimNotOwnedError) SuggestedAction() string {
	return fmt.Sprintf("vybe task begin --id %s --agent %s --request-id <new>", e.TaskID, e.RequestedBy)
}
func (e *ClaimNotOwnedError) Is(target error) bool { return target == ErrClaimNotOwned }

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

func (e *IdempotencyInProgressError) Error() string { return "idempotency in progress" }
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
