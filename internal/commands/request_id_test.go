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

func TestRequireRequestID_AutoGeneratesWhenMissing(t *testing.T) {
	cmd := newRequestIDTestCmd(t)
	t.Setenv("VYBE_REQUEST_ID", "")

	rid1, err := requireRequestID(cmd)
	require.NoError(t, err)
	require.NotEmpty(t, rid1)
	require.True(t, strings.HasPrefix(rid1, "req_"), "auto-generated id must use req_ prefix, got %q", rid1)

	rid2, err := requireRequestID(cmd)
	require.NoError(t, err)
	require.NotEqual(t, rid1, rid2, "omitted request-id must yield a fresh key per call")
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
