package commands

import (
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
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

func newTestCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "test"}
	cmd.Flags().String("agent", "", "")
	cmd.Flags().String("actor", "", "")
	return cmd
}

func TestResolveActorName_Normalization(t *testing.T) {
	tests := []struct {
		name     string
		flagVal  string
		expected string
	}{
		{"lowercase passthrough", "claude", "claude"},
		{"uppercase normalized", "Claude", "claude"},
		{"mixed case", "Poet-Agent", "poet-agent"},
		{"whitespace trimmed", "  claude  ", "claude"},
		{"upper + whitespace", " CLAUDE ", "claude"},
		{"empty stays empty", "", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd := newTestCmd()
			if tt.flagVal != "" {
				_ = cmd.Flags().Set("agent", tt.flagVal)
			}
			got := resolveActorName(cmd, "")
			assert.Equal(t, tt.expected, got)
		})
	}
}

func TestResolveActorName_EnvNormalized(t *testing.T) {
	cmd := newTestCmd()
	t.Setenv("VYBE_AGENT", "Claude")
	got := resolveActorName(cmd, "")
	assert.Equal(t, "claude", got)
}
