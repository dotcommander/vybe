package commands

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNewArtifactCmd_HasExpectedSubcommands(t *testing.T) {
	cmd := NewArtifactCmd()
	require.Equal(t, "artifact", cmd.Use)
	require.Equal(t, "Manage artifacts linked to tasks/events", cmd.Short)

	for _, name := range []string{"add", "get", "list"} {
		sub, _, err := cmd.Find([]string{name})
		require.NoError(t, err)
		require.NotNil(t, sub)
		require.Equal(t, name, sub.Name())
	}
}

func TestArtifactAddCmd_RequiresIdentity(t *testing.T) {
	cmd := newArtifactAddCmd()
	err := cmd.RunE(cmd, nil)
	require.Error(t, err)
	require.EqualError(t, err, "error already printed")
	require.IsType(t, printedError{}, err)
}

func TestArtifactAddCmd_RequiresTaskAndPath(t *testing.T) {
	t.Run("missing task", func(t *testing.T) {
		cmd := newArtifactAddCmd()
		t.Setenv("VIBE_AGENT", "agent-1")
		t.Setenv("VIBE_REQUEST_ID", "req-1")
		require.NoError(t, cmd.Flags().Set("path", "/tmp/file"))

		err := cmd.RunE(cmd, nil)
		require.EqualError(t, err, "--task is required")
	})

	t.Run("missing path", func(t *testing.T) {
		cmd := newArtifactAddCmd()
		t.Setenv("VIBE_AGENT", "agent-1")
		t.Setenv("VIBE_REQUEST_ID", "req-1")
		require.NoError(t, cmd.Flags().Set("task", "task-1"))

		err := cmd.RunE(cmd, nil)
		require.EqualError(t, err, "--path is required")
	})
}

func TestArtifactGetAndList_ValidateInputsBeforeDB(t *testing.T) {
	getCmd := newArtifactGetCmd()
	err := getCmd.RunE(getCmd, nil)
	require.EqualError(t, err, "--id is required")

	listCmd := newArtifactListCmd()
	err = listCmd.RunE(listCmd, nil)
	require.EqualError(t, err, "--task is required")
}

func TestArtifactFlagSetup(t *testing.T) {
	add := newArtifactAddCmd()
	requireFlagExists(t, add, "task")
	requireFlagExists(t, add, "path")
	requireFlagExists(t, add, "type")

	get := newArtifactGetCmd()
	requireFlagExists(t, get, "id")

	list := newArtifactListCmd()
	requireFlagExists(t, list, "task")
	requireFlagExists(t, list, "limit")
}
