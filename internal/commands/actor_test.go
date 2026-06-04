package commands

import (
	"testing"

	"github.com/dotcommander/vybe/internal/app"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newActorTestCmd(t *testing.T) *cobra.Command {
	t.Helper()
	cmd := &cobra.Command{Use: "test"}
	cmd.Flags().String("agent", "", "")
	cmd.Flags().String("worker", "", "")
	return cmd
}

func TestResolveActorName_Precedence(t *testing.T) {
	cmd := newActorTestCmd(t)
	t.Setenv("VYBE_AGENT", "env-agent")
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
	// Isolate config so app.LoadSettings finds no config.yaml; otherwise the shipped
	// default_agent ("claude") makes resolveActorName return non-empty even with VYBE_AGENT="".
	app.ResetSettingsForTest()
	t.Cleanup(app.ResetSettingsForTest)
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)
	t.Setenv("XDG_CONFIG_HOME", "")

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
	return cmd
}

func TestResolveActorName_Normalization(t *testing.T) {
	// Isolate config so the "empty stays empty" case does not pick up the shipped
	// default_agent ("claude") from the real ~/.config/vybe/config.yaml.
	app.ResetSettingsForTest()
	t.Cleanup(app.ResetSettingsForTest)
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("VYBE_AGENT", "")

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

func TestResolveActorName_FlagOverridesConfig(t *testing.T) {
	cmd := newActorTestCmd(t)
	t.Setenv("VYBE_AGENT", "")
	require.NoError(t, cmd.Flags().Set("agent", "flag-agent"))

	got := resolveActorName(cmd, "")
	require.Equal(t, "flag-agent", got)
}

func TestResolveActorName_EnvOverridesWhenNoFlag(t *testing.T) {
	cmd := newActorTestCmd(t)
	t.Setenv("VYBE_AGENT", "env-agent")

	got := resolveActorName(cmd, "")
	require.Equal(t, "env-agent", got)
}
