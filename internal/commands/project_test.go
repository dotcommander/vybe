package commands

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNewProjectCmd_HasExpectedSubcommands(t *testing.T) {
	cmd := NewProjectCmd()
	require.Equal(t, "project", cmd.Use)
	require.Equal(t, "Manage projects", cmd.Short)

	for _, name := range []string{"create", "get", "list"} {
		sub, _, err := cmd.Find([]string{name})
		require.NoError(t, err)
		require.NotNil(t, sub)
		require.Equal(t, name, sub.Name())
	}
}

func TestProjectCreateCmd_RequiresIdentity(t *testing.T) {
	cmd := newProjectCreateCmd()
	err := cmd.RunE(cmd, nil)
	require.Error(t, err)
	require.EqualError(t, err, "error already printed")
	require.IsType(t, printedError{}, err)
}

func TestProjectCreateCmd_RequiresName(t *testing.T) {
	cmd := newProjectCreateCmd()
	t.Setenv("VIBE_AGENT", "agent-1")
	t.Setenv("VIBE_REQUEST_ID", "req-1")

	err := cmd.RunE(cmd, nil)
	require.Error(t, err)
	require.EqualError(t, err, "error already printed")
	require.IsType(t, printedError{}, err)
}

func TestProjectGetCmd_ValidationErrorsBeforeDB(t *testing.T) {
	cmd := newProjectGetCmd()

	err := cmd.RunE(cmd, nil)
	require.Error(t, err)
	require.EqualError(t, err, "error already printed")

	require.NoError(t, cmd.Flags().Set("id", "project-1"))
	err = cmd.RunE(cmd, []string{"project-2"})
	require.Error(t, err)
	require.EqualError(t, err, "error already printed")
}

func TestProjectFlagSetup(t *testing.T) {
	create := newProjectCreateCmd()
	requireFlagExists(t, create, "name")
	requireFlagExists(t, create, "metadata")

	get := newProjectGetCmd()
	requireFlagExists(t, get, "id")
}
