package commands

import (
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"
)

func newActorTestCmd(t *testing.T) *cobra.Command {
	t.Helper()
	cmd := &cobra.Command{Use: "test"}
	cmd.Flags().String("agent", "", "")
	cmd.Flags().String("actor", "", "")
	cmd.Flags().String("worker", "", "")
	return cmd
}

func TestResolveActorName_Precedence(t *testing.T) {
	cmd := newActorTestCmd(t)
	t.Setenv("VYBE_AGENT", "env-agent")
	require.NoError(t, cmd.Flags().Set("actor", "legacy-actor"))
	require.NoError(t, cmd.Flags().Set("agent", "global-agent"))
	require.NoError(t, cmd.Flags().Set("worker", "per-cmd-agent"))

	got := resolveActorName(cmd, "worker")
	require.Equal(t, "per-cmd-agent", got)
}

func TestResolveActorName_UsesEnvFallback(t *testing.T) {
	cmd := newActorTestCmd(t)
	t.Setenv("VYBE_AGENT", "env-agent")

	got := resolveActorName(cmd, "worker")
	require.Equal(t, "env-agent", got)
}

func TestRequireActorName_ErrorWhenMissing(t *testing.T) {
	cmd := newActorTestCmd(t)
	t.Setenv("VYBE_AGENT", "")

	got, err := requireActorName(cmd, "worker")
	require.Error(t, err)
	require.Empty(t, got)
	require.Contains(t, err.Error(), "agent is required")
}
