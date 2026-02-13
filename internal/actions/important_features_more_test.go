package actions

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestResumeIdempotent_WrapperPath(t *testing.T) {
	db, cleanup := setupTestDBWithCleanup(t)
	defer cleanup()

	resp, err := ResumeIdempotent(db, "agent-a", "req-resume-1")
	require.NoError(t, err)
	require.NotNil(t, resp)
}

func TestInvalidTaskStatusError_SlogAttrs(t *testing.T) {
	err := invalidTaskStatusError{Status: "wat"}
	attrs := err.SlogAttrs()
	require.Len(t, attrs, 6)
	require.Equal(t, "field", attrs[0])
	require.Equal(t, "status", attrs[1])
	require.Equal(t, "invalid_value", attrs[2])
	require.Equal(t, "wat", attrs[3])
	require.Equal(t, "valid_options", attrs[4])
}
