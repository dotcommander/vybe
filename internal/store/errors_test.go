package store

import (
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestRecoverableError_Is verifies each struct type matches its own sentinel
// via errors.Is and does not cross-match other sentinels.
func TestRecoverableError_Is(t *testing.T) {
	version := &VersionConflictError{Entity: "task", ID: "t1", Version: 3}
	inProgress := &IdempotencyInProgressError{AgentName: "agent-a", RequestID: "req-1", Command: "task create"}

	// Each struct matches its own sentinel.
	assert.ErrorIs(t, version, ErrVersionConflict)
	assert.ErrorIs(t, inProgress, ErrIdempotencyInProgress)

	// Cross-match must return false.
	assert.False(t, errors.Is(version, ErrIdempotencyInProgress), "VersionConflictError should not match ErrIdempotencyInProgress")
	assert.False(t, errors.Is(inProgress, ErrVersionConflict), "IdempotencyInProgressError should not match ErrVersionConflict")
}

// TestRecoverableError_ErrorCode verifies each struct returns the correct code string.
func TestRecoverableError_ErrorCode(t *testing.T) {
	tests := []struct {
		name     string
		err      RecoverableError
		wantCode string
	}{
		{
			name:     "VersionConflictError",
			err:      &VersionConflictError{Entity: "task", ID: "t1", Version: 3},
			wantCode: "VERSION_CONFLICT",
		},
		{
			name:     "IdempotencyInProgressError",
			err:      &IdempotencyInProgressError{AgentName: "agent-a", RequestID: "req-1", Command: "task create"},
			wantCode: "IDEMPOTENCY_IN_PROGRESS",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.wantCode, tc.err.ErrorCode())
		})
	}
}

// TestRecoverableError_Context verifies each struct returns a context map with expected keys and values.
func TestRecoverableError_Context(t *testing.T) {
	t.Run("VersionConflictError", func(t *testing.T) {
		e := &VersionConflictError{Entity: "task", ID: "t3", Version: 7}
		ctx := e.Context()
		require.Contains(t, ctx, "entity")
		require.Contains(t, ctx, "id")
		require.Contains(t, ctx, "version")
		assert.Equal(t, "task", ctx["entity"])
		assert.Equal(t, "t3", ctx["id"])
		assert.Equal(t, "7", ctx["version"])
	})

	t.Run("IdempotencyInProgressError", func(t *testing.T) {
		e := &IdempotencyInProgressError{AgentName: "agent-a", RequestID: "req-42", Command: "task begin"}
		ctx := e.Context()
		require.Contains(t, ctx, "agent_name")
		require.Contains(t, ctx, "request_id")
		require.Contains(t, ctx, "command")
		assert.Equal(t, "agent-a", ctx["agent_name"])
		assert.Equal(t, "req-42", ctx["request_id"])
		assert.Equal(t, "task begin", ctx["command"])
	})
}

// TestRecoverableError_SuggestedAction verifies each struct returns a non-empty suggested action.
func TestRecoverableError_SuggestedAction(t *testing.T) {
	tests := []struct {
		name string
		err  RecoverableError
	}{
		{
			name: "VersionConflictError",
			err:  &VersionConflictError{Entity: "task", ID: "t1", Version: 3},
		},
		{
			name: "IdempotencyInProgressError",
			err:  &IdempotencyInProgressError{AgentName: "agent-a", RequestID: "req-1", Command: "task create"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.NotEmpty(t, tc.err.SuggestedAction())
		})
	}
}

// TestRecoverableError_ErrorMessage verifies each struct's Error() matches its sentinel's message.
func TestRecoverableError_ErrorMessage(t *testing.T) {
	tests := []struct {
		name     string
		err      RecoverableError
		sentinel error
	}{
		{
			name:     "VersionConflictError",
			err:      &VersionConflictError{Entity: "task", ID: "t1", Version: 3},
			sentinel: ErrVersionConflict,
		},
		{
			name:     "IdempotencyInProgressError",
			err:      &IdempotencyInProgressError{AgentName: "agent-a", RequestID: "req-1", Command: "task create"},
			sentinel: ErrIdempotencyInProgress,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.sentinel.Error(), tc.err.Error())
		})
	}
}

// TestRecoverableError_WrappedIs verifies errors.Is works through fmt.Errorf %w wrapping chains.
func TestRecoverableError_WrappedIs(t *testing.T) {
	tests := []struct {
		name     string
		wrapped  error
		sentinel error
	}{
		{
			name:     "wrapped VersionConflictError matches ErrVersionConflict",
			wrapped:  fmt.Errorf("outer: %w", &VersionConflictError{Entity: "task", ID: "t1", Version: 3}),
			sentinel: ErrVersionConflict,
		},
		{
			name:     "wrapped IdempotencyInProgressError matches ErrIdempotencyInProgress",
			wrapped:  fmt.Errorf("outer: %w", &IdempotencyInProgressError{AgentName: "agent-a", RequestID: "req-1", Command: "task create"}),
			sentinel: ErrIdempotencyInProgress,
		},
		{
			name:     "double-wrapped VersionConflictError matches ErrVersionConflict",
			wrapped:  fmt.Errorf("level2: %w", fmt.Errorf("level1: %w", &VersionConflictError{Entity: "task", ID: "t1", Version: 3})),
			sentinel: ErrVersionConflict,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.ErrorIs(t, tc.wrapped, tc.sentinel)
		})
	}
}
