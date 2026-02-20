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

	for _, name := range []string{"create", "begin", "heartbeat", "set-status", "get", "list", "add-dep", "remove-dep", "delete", "gc", "claim", "complete", "set-priority", "next", "unlocks", "stats"} {
		sub, _, err := cmd.Find([]string{name})
		require.NoError(t, err)
		require.NotNil(t, sub)
		require.Equal(t, name, sub.Name())
	}
}

func TestTaskHeartbeatCmd_RequiresIDBeforeActorResolution(t *testing.T) {
	cmd := newTaskHeartbeatCmd()
	err := cmd.RunE(cmd, nil)
	require.Error(t, err)
	require.IsType(t, printedError{}, err)
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

func TestTaskDepCmd_RequiresInputsBeforeDB(t *testing.T) {
	apply := func(db *DB, agentName, requestID, taskID, dependsOn string) error {
		return nil
	}
	cmd := newTaskDepCmd("dep", "dep", "ok", apply)

	err := cmd.RunE(cmd, nil)
	require.Error(t, err)
	require.IsType(t, printedError{}, err)

	require.NoError(t, cmd.Flags().Set("id", "task-1"))
	err = cmd.RunE(cmd, nil)
	require.Error(t, err)
	require.IsType(t, printedError{}, err)
}

func TestTaskDepCmd_RequiresActorAndRequestID(t *testing.T) {
	called := false
	apply := func(db *DB, agentName, requestID, taskID, dependsOn string) error {
		called = true
		return nil
	}
	cmd := newTaskDepCmd("dep", "dep", "ok", apply)
	require.NoError(t, cmd.Flags().Set("id", "task-1"))
	require.NoError(t, cmd.Flags().Set("depends-on", "task-2"))

	err := cmd.RunE(cmd, nil)
	require.Error(t, err)
	require.EqualError(t, err, "error already printed")
	require.IsType(t, printedError{}, err)
	require.False(t, called)
}

func TestNewTaskDepVariants_Metadata(t *testing.T) {
	add := newTaskAddDepCmd()
	require.Equal(t, "add-dep", add.Name())
	require.Contains(t, add.Short, "dependency")

	remove := newTaskRemoveDepCmd()
	require.Equal(t, "remove-dep", remove.Name())
	require.Contains(t, remove.Short, "Remove")
}

func TestTaskCreateCmd_DefinesFlags(t *testing.T) {
	cmd := newTaskCreateCmd()
	requireFlagExists(t, cmd, "title")
	requireFlagExists(t, cmd, "desc")
	requireFlagExists(t, cmd, "project")
	requireFlagExists(t, cmd, "priority")
}

func TestTaskSetPriority_RequiredFlags(t *testing.T) {
	t.Run("missing id", func(t *testing.T) {
		cmd := newTaskSetPriorityCmd()
		t.Setenv("VYBE_AGENT", "agent-1")
		t.Setenv("VYBE_REQUEST_ID", "req-1")

		err := cmd.RunE(cmd, nil)
		require.Error(t, err)
		require.IsType(t, printedError{}, err)
	})

	t.Run("missing agent", func(t *testing.T) {
		cmd := newTaskSetPriorityCmd()
		require.NoError(t, cmd.Flags().Set("id", "task-1"))
		require.NoError(t, cmd.Flags().Set("priority", "5"))

		err := cmd.RunE(cmd, nil)
		require.Error(t, err)
		require.IsType(t, printedError{}, err)
	})

	t.Run("missing request-id", func(t *testing.T) {
		cmd := newTaskSetPriorityCmd()
		t.Setenv("VYBE_AGENT", "agent-1")
		require.NoError(t, cmd.Flags().Set("id", "task-1"))
		require.NoError(t, cmd.Flags().Set("priority", "5"))

		err := cmd.RunE(cmd, nil)
		require.Error(t, err)
		require.IsType(t, printedError{}, err)
	})
}

func TestTaskNext_RequiredFlags(t *testing.T) {
	cmd := newTaskNextCmd()
	err := cmd.RunE(cmd, nil)
	require.Error(t, err)
	require.IsType(t, printedError{}, err)
}

func TestTaskUnlocks_RequiredFlags(t *testing.T) {
	cmd := newTaskUnlocksCmd()
	err := cmd.RunE(cmd, nil)
	require.Error(t, err)
	require.IsType(t, printedError{}, err)
}

func TestTaskStats_NoRequiredFlags(t *testing.T) {
	cmd := newTaskStatsCmd()
	// Stats has no required flags — it needs a DB connection to succeed,
	// so just verify it defines the expected flags and doesn't panic on validation
	requireFlagExists(t, cmd, "project")
}

func TestTaskSetPriorityCmd_DefinesFlags(t *testing.T) {
	cmd := newTaskSetPriorityCmd()
	requireFlagExists(t, cmd, "id")
	requireFlagExists(t, cmd, "priority")
}

func TestTaskNextCmd_DefinesFlags(t *testing.T) {
	cmd := newTaskNextCmd()
	requireFlagExists(t, cmd, "project")
	requireFlagExists(t, cmd, "limit")
}

func TestTaskUnlocksCmd_DefinesFlags(t *testing.T) {
	cmd := newTaskUnlocksCmd()
	requireFlagExists(t, cmd, "id")
}

func requireFlagExists(t *testing.T, cmd *cobra.Command, name string) {
	t.Helper()
	f := cmd.Flags().Lookup(name)
	require.NotNil(t, f)
}
