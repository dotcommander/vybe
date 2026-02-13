package commands

import (
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"
)

func TestNewMemoryCmd_HasExpectedSubcommands(t *testing.T) {
	cmd := NewMemoryCmd()
	require.Equal(t, "memory", cmd.Use)
	require.Equal(t, "Manage memory key-value storage with scoping", cmd.Short)

	for _, name := range []string{"set", "compact", "gc", "get", "list", "delete"} {
		sub, _, err := cmd.Find([]string{name})
		require.NoError(t, err)
		require.NotNil(t, sub)
		require.Equal(t, name, sub.Name())
	}
}

func TestMemorySetCmd_InvalidExpiresInReturnsPrintedError(t *testing.T) {
	cmd := newMemorySetCmd()
	t.Setenv("VIBE_AGENT", "agent-1")
	t.Setenv("VIBE_REQUEST_ID", "req-1")
	require.NoError(t, cmd.Flags().Set("key", "k"))
	require.NoError(t, cmd.Flags().Set("value", "v"))
	require.NoError(t, cmd.Flags().Set("expires-in", "bad-duration"))

	err := cmd.RunE(cmd, nil)
	require.Error(t, err)
	require.EqualError(t, err, "error already printed")
	require.IsType(t, printedError{}, err)
}

func TestMemoryCompactCmd_InvalidMaxAgeReturnsPrintedError(t *testing.T) {
	cmd := newMemoryCompactCmd()
	t.Setenv("VIBE_AGENT", "agent-1")
	t.Setenv("VIBE_REQUEST_ID", "req-1")
	require.NoError(t, cmd.Flags().Set("max-age", "???"))

	err := cmd.RunE(cmd, nil)
	require.Error(t, err)
	require.EqualError(t, err, "error already printed")
	require.IsType(t, printedError{}, err)
}

func TestMemoryMutatingCommands_RequireIdentity(t *testing.T) {
	t.Run("gc without agent/request-id", func(t *testing.T) {
		cmd := newMemoryGCCmd()
		err := cmd.RunE(cmd, nil)
		require.Error(t, err)
		require.EqualError(t, err, "error already printed")
	})

	t.Run("delete without agent/request-id", func(t *testing.T) {
		cmd := newMemoryDeleteCmd()
		require.NoError(t, cmd.Flags().Set("key", "k"))
		err := cmd.RunE(cmd, nil)
		require.Error(t, err)
		require.EqualError(t, err, "error already printed")
	})
}

func TestMemoryFlagSetup(t *testing.T) {
	set := newMemorySetCmd()
	requireFlagExists(t, set, "key")
	requireFlagExists(t, set, "value")
	requireFlagExists(t, set, "type")
	requireFlagExists(t, set, "scope")
	requireFlagExists(t, set, "scope-id")
	requireFlagExists(t, set, "expires-in")
	require.Equal(t, "true", set.Flag("key").Annotations[cobra.BashCompOneRequiredFlag][0])
	require.Equal(t, "true", set.Flag("value").Annotations[cobra.BashCompOneRequiredFlag][0])

	get := newMemoryGetCmd()
	requireFlagExists(t, get, "key")
	requireFlagExists(t, get, "scope")
	requireFlagExists(t, get, "scope-id")
	require.Equal(t, "true", get.Flag("key").Annotations[cobra.BashCompOneRequiredFlag][0])

	compact := newMemoryCompactCmd()
	requireFlagExists(t, compact, "max-age")
	requireFlagExists(t, compact, "keep-top")

	gc := newMemoryGCCmd()
	requireFlagExists(t, gc, "limit")

	list := newMemoryListCmd()
	requireFlagExists(t, list, "scope")
	requireFlagExists(t, list, "scope-id")

	deleteCmd := newMemoryDeleteCmd()
	requireFlagExists(t, deleteCmd, "key")
	requireFlagExists(t, deleteCmd, "scope")
	requireFlagExists(t, deleteCmd, "scope-id")
	require.Equal(t, "true", deleteCmd.Flag("key").Annotations[cobra.BashCompOneRequiredFlag][0])
}
