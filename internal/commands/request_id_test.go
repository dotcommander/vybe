package commands

import (
	"testing"

	"github.com/spf13/cobra"
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

func TestRequireRequestID_ReturnsErrorWhenMissing(t *testing.T) {
	cmd := newRequestIDTestCmd(t)
	t.Setenv("VYBE_REQUEST_ID", "")

	rid, err := requireRequestID(cmd)
	require.Error(t, err)
	require.Empty(t, rid)
	require.Contains(t, err.Error(), "request id is required")
}

func TestRequireRequestID_ReturnsValue(t *testing.T) {
	cmd := newRequestIDTestCmd(t)
	require.NoError(t, cmd.Flags().Set("request-id", "req-123"))

	rid, err := requireRequestID(cmd)
	require.NoError(t, err)
	require.Equal(t, "req-123", rid)
}
