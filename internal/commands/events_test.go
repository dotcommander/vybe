package commands

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNewEventsCmd_HasExpectedSubcommands(t *testing.T) {
	cmd := NewEventsCmd()
	require.Equal(t, "events", cmd.Use)
	require.Equal(t, "Inspect the continuity event log", cmd.Short)

	for _, name := range []string{"list", "tail", "summarize"} {
		sub, _, err := cmd.Find([]string{name})
		require.NoError(t, err)
		require.NotNil(t, sub)
		require.Equal(t, name, sub.Name())
	}
}

func TestEventsListCmd_RequiresAgentUnlessAll(t *testing.T) {
	cmd := newEventsListCmd()
	err := cmd.RunE(cmd, nil)
	require.Error(t, err)
	require.EqualError(t, err, "error already printed")
	require.IsType(t, printedError{}, err)
}

func TestEventsTailCmd_RequiresAgentUnlessAll(t *testing.T) {
	cmd := newEventsTailCmd()
	require.NoError(t, cmd.Flags().Set("once", "true"))

	err := cmd.RunE(cmd, nil)
	require.Error(t, err)
	require.EqualError(t, err, "error already printed")
	require.IsType(t, printedError{}, err)
}

func TestEventsSummarizeCmd_ValidationErrors(t *testing.T) {
	t.Run("missing from-id", func(t *testing.T) {
		cmd := newEventsSummarizeCmd()
		t.Setenv("VIBE_AGENT", "agent-1")
		t.Setenv("VIBE_REQUEST_ID", "req-1")

		err := cmd.RunE(cmd, nil)
		require.Error(t, err)
		require.EqualError(t, err, "error already printed")
	})

	t.Run("missing to-id", func(t *testing.T) {
		cmd := newEventsSummarizeCmd()
		t.Setenv("VIBE_AGENT", "agent-1")
		t.Setenv("VIBE_REQUEST_ID", "req-1")
		require.NoError(t, cmd.Flags().Set("from-id", "1"))

		err := cmd.RunE(cmd, nil)
		require.Error(t, err)
		require.EqualError(t, err, "error already printed")
	})

	t.Run("missing summary", func(t *testing.T) {
		cmd := newEventsSummarizeCmd()
		t.Setenv("VIBE_AGENT", "agent-1")
		t.Setenv("VIBE_REQUEST_ID", "req-1")
		require.NoError(t, cmd.Flags().Set("from-id", "1"))
		require.NoError(t, cmd.Flags().Set("to-id", "2"))

		err := cmd.RunE(cmd, nil)
		require.Error(t, err)
		require.EqualError(t, err, "error already printed")
	})
}

func TestEventsFlagSetup(t *testing.T) {
	list := newEventsListCmd()
	requireFlagExists(t, list, "all")
	requireFlagExists(t, list, "task")
	requireFlagExists(t, list, "kind")
	requireFlagExists(t, list, "limit")
	requireFlagExists(t, list, "since-id")
	requireFlagExists(t, list, "asc")
	requireFlagExists(t, list, "include-archived")

	tail := newEventsTailCmd()
	requireFlagExists(t, tail, "all")
	requireFlagExists(t, tail, "task")
	requireFlagExists(t, tail, "kind")
	requireFlagExists(t, tail, "limit")
	requireFlagExists(t, tail, "since-id")
	requireFlagExists(t, tail, "interval")
	requireFlagExists(t, tail, "once")
	requireFlagExists(t, tail, "jsonl")
	requireFlagExists(t, tail, "from-cursor")
	requireFlagExists(t, tail, "include-archived")

	summarize := newEventsSummarizeCmd()
	requireFlagExists(t, summarize, "from-id")
	requireFlagExists(t, summarize, "to-id")
	requireFlagExists(t, summarize, "task")
	requireFlagExists(t, summarize, "summary")
}
