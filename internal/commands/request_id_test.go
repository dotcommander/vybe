package commands

import (
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newRequestIDTestCmd(t *testing.T) *cobra.Command {
	t.Helper()
	cmd := &cobra.Command{Use: "test"}
	cmd.Flags().String("request-id", "", "")
	return cmd
}

func TestResolveRequestID_FlagWinsOverEnv(t *testing.T) {
	cmd := newRequestIDTestCmd(t)
	t.Setenv("VYBE_REQUEST_ID", "env-req")
	require.NoError(t, cmd.Flags().Set("request-id", "flag-req"))

	rid := resolveRequestID(cmd)
	require.Equal(t, "flag-req", rid)
}

func TestResolveRequestID_UsesEnvWhenFlagEmpty(t *testing.T) {
	cmd := newRequestIDTestCmd(t)
	t.Setenv("VYBE_REQUEST_ID", "env-req")

	rid := resolveRequestID(cmd)
	require.Equal(t, "env-req", rid)
}

func TestRequireRequestID_ErrorsWhenMissing(t *testing.T) {
	cmd := newRequestIDTestCmd(t)
	t.Setenv("VYBE_REQUEST_ID", "")

	_, err := requireRequestID(cmd)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "--request-id")
}

func TestRequireRequestID_ReturnsValue(t *testing.T) {
	cmd := newRequestIDTestCmd(t)
	require.NoError(t, cmd.Flags().Set("request-id", "req-123"))

	rid, err := requireRequestID(cmd)
	require.NoError(t, err)
	require.Equal(t, "req-123", rid)
}

func TestRequireRequestID_ExplicitFlagUsed(t *testing.T) {
	cmd := newRequestIDTestCmd(t)
	require.NoError(t, cmd.Flags().Set("request-id", "my-explicit-id"))

	rid, err := requireRequestID(cmd)
	require.NoError(t, err)
	assert.Equal(t, "my-explicit-id", rid)
}

func TestRequireRequestID_EnvOverride(t *testing.T) {
	cmd := newRequestIDTestCmd(t)
	t.Setenv("VYBE_REQUEST_ID", "env-id-123")

	rid, err := requireRequestID(cmd)
	require.NoError(t, err)
	assert.Equal(t, "env-id-123", rid)
}

func TestGenerateRequestID_Format(t *testing.T) {
	id := generateRequestID()
	assert.True(t, strings.HasPrefix(id, "req_"))
	// Should have format: req_{digits}_{hex}
	parts := strings.SplitN(id, "_", 3)
	assert.Len(t, parts, 3)
}
