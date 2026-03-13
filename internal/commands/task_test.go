package commands

import (
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"
)

func TestNewTaskCmd_HasExpectedSubcommands(t *testing.T) {
	cmd := NewTaskCmd()
	require.Equal(t, "task", cmd.Use)
	require.Equal(t, "Manage tasks", cmd.Short)

	for _, name := range []string{"create", "begin", "set-status", "get", "list"} {
		sub, _, err := cmd.Find([]string{name})
		require.NoError(t, err)
		require.NotNil(t, sub)
		require.Equal(t, name, sub.Name())
	}
}

func TestTaskGetCmd_ValidationErrorsBeforeDB(t *testing.T) {
	cmd := newTaskGetCmd()

	err := cmd.RunE(cmd, nil)
	require.Error(t, err)
	require.IsType(t, printedError{}, err)

	require.NoError(t, cmd.Flags().Set("id", "task-1"))
	err = cmd.RunE(cmd, []string{"task-2"})
	require.Error(t, err)
	require.IsType(t, printedError{}, err)
}

func TestTaskBeginCmd_RequiresIDBeforeActorResolution(t *testing.T) {
	cmd := newTaskBeginCmd()
	err := cmd.RunE(cmd, nil)
	require.Error(t, err)
	require.IsType(t, printedError{}, err)
}

func TestTaskCreateCmd_RequiresTitleWhenIdentityPresent(t *testing.T) {
	cmd := newTaskCreateCmd()
	t.Setenv("VYBE_AGENT", "agent-1")
	t.Setenv("VYBE_REQUEST_ID", "req-1")

	err := cmd.RunE(cmd, nil)
	require.Error(t, err)
	require.IsType(t, printedError{}, err)
}

func TestTaskSetStatusCmd_RequiresIDAndStatus(t *testing.T) {
	t.Run("missing id", func(t *testing.T) {
		cmd := newTaskSetStatusCmd()
		t.Setenv("VYBE_AGENT", "agent-1")
		t.Setenv("VYBE_REQUEST_ID", "req-1")
		require.NoError(t, cmd.Flags().Set("status", "pending"))

		err := cmd.RunE(cmd, nil)
		require.Error(t, err)
		require.IsType(t, printedError{}, err)
	})

	t.Run("missing status", func(t *testing.T) {
		cmd := newTaskSetStatusCmd()
		t.Setenv("VYBE_AGENT", "agent-1")
		t.Setenv("VYBE_REQUEST_ID", "req-1")
		require.NoError(t, cmd.Flags().Set("id", "task-1"))

		err := cmd.RunE(cmd, nil)
		require.Error(t, err)
		require.IsType(t, printedError{}, err)
	})
}

func TestTaskCreateCmd_DefinesFlags(t *testing.T) {
	cmd := newTaskCreateCmd()
	requireFlagExists(t, cmd, "title")
	requireFlagExists(t, cmd, "desc")
	requireFlagExists(t, cmd, "project-id")
	requireFlagExists(t, cmd, "priority")
}

func requireFlagExists(t *testing.T, cmd *cobra.Command, name string) {
	t.Helper()
	f := cmd.Flags().Lookup(name)
	require.NotNil(t, f)
}
