package commands

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNewAgentCmd_HasExpectedSubcommands(t *testing.T) {
	cmd := NewAgentCmd()
	require.Equal(t, "agent", cmd.Use)
	require.Equal(t, "Manage agent state", cmd.Short)

	for _, name := range []string{"init", "status", "focus"} {
		sub, _, err := cmd.Find([]string{name})
		require.NoError(t, err)
		require.NotNil(t, sub)
		require.Equal(t, name, sub.Name())
	}
}

func TestAgentFocusCmd_RequiresTaskOrProject(t *testing.T) {
	cmd := newAgentFocusCmd()
	err := cmd.RunE(cmd, nil)
	require.Error(t, err)
	require.EqualError(t, err, "error already printed")
	require.IsType(t, printedError{}, err)
}

func TestAgentFocusCmd_RequiresIdentityWhenTaskProvided(t *testing.T) {
	cmd := newAgentFocusCmd()
	require.NoError(t, cmd.Flags().Set("task", "task-1"))

	err := cmd.RunE(cmd, nil)
	require.Error(t, err)
	require.EqualError(t, err, "error already printed")
	require.IsType(t, printedError{}, err)
}

func TestAgentInitAndStatus_RequireIdentityWithoutLegacyName(t *testing.T) {
	initCmd := newAgentInitCmd()
	err := initCmd.RunE(initCmd, nil)
	require.Error(t, err)
	require.EqualError(t, err, "error already printed")

	statusCmd := newAgentStatusCmd()
	err = statusCmd.RunE(statusCmd, nil)
	require.Error(t, err)
	require.EqualError(t, err, "error already printed")
}

func TestAgentFlagSetup(t *testing.T) {
	focus := newAgentFocusCmd()
	requireFlagExists(t, focus, "task")
	requireFlagExists(t, focus, "project")

	initCmd := newAgentInitCmd()
	requireFlagExists(t, initCmd, "name")

	statusCmd := newAgentStatusCmd()
	requireFlagExists(t, statusCmd, "name")
}
