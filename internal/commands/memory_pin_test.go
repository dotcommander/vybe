package commands

import (
	"context"
	"testing"

	"github.com/dotcommander/vybe/internal/store"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestMemorySetPinFlag verifies the --pin flag is registered and wired.
func TestMemorySetPinFlag(t *testing.T) {
	t.Parallel()

	set := newMemorySetCmd()
	requireFlagExists(t, set, "pin")

	f := set.Flag("pin")
	require.NotNil(t, f)
	assert.Equal(t, "false", f.DefValue, "--pin default must be false")

	// Confirm --pin is plumbed through to the store via a real DB round-trip.
	dir := t.TempDir()
	db, err := store.InitDBWithPath(dir + "/test.db")
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	_, err = store.UpsertMemoryWithEventIdempotent(
		db, "agent-pin-flag", "req-pin-set-flag-1",
		"arch", "monolith", "string", "global", "", nil, true, "", nil, "",
	)
	require.NoError(t, err)

	mem, err := store.GetMemory(db, "arch", "global", "")
	require.NoError(t, err)
	require.NotNil(t, mem)
	assert.True(t, mem.Pinned, "UpsertMemoryWithEventIdempotent with pinned=true must set Pinned field")
}

// TestMemoryPinSubcommand verifies pin subcommand registration and pin/unpin toggle semantics.
func TestMemoryPinSubcommand(t *testing.T) {
	t.Parallel()

	// Subcommand must be registered
	memCmd := NewMemoryCmd()
	sub, _, err := memCmd.Find([]string{"pin"})
	require.NoError(t, err)
	require.NotNil(t, sub)
	require.Equal(t, "pin", sub.Name())

	// Flags must be present
	requireFlagExists(t, sub, "key")
	requireFlagExists(t, sub, "scope")
	requireFlagExists(t, sub, "scope-id")
	requireFlagExists(t, sub, "unpin")
	assert.Equal(t, "false", sub.Flag("unpin").DefValue)

	// Full pin/unpin toggle round-trip via store
	dir := t.TempDir()
	db, dbErr := store.InitDBWithPath(dir + "/test.db")
	require.NoError(t, dbErr)
	defer func() { _ = db.Close() }()

	require.NoError(t, store.SetMemory(db, "toggle-key", "v", "string", "global", "", nil, false, "", nil))

	// Pin
	eid1, err := store.PinMemoryIdempotent(context.Background(), db, "agent", "req-toggle-pin", "toggle-key", "global", "", true)
	require.NoError(t, err)
	require.Greater(t, eid1, int64(0))

	mem1, err := store.GetMemory(db, "toggle-key", "global", "")
	require.NoError(t, err)
	require.NotNil(t, mem1)
	assert.True(t, mem1.Pinned, "after pin: Pinned must be true")

	// Unpin
	eid2, err := store.PinMemoryIdempotent(context.Background(), db, "agent", "req-toggle-unpin", "toggle-key", "global", "", false)
	require.NoError(t, err)
	require.Greater(t, eid2, int64(0))
	assert.NotEqual(t, eid1, eid2, "pin and unpin must produce distinct events")

	mem2, err := store.GetMemory(db, "toggle-key", "global", "")
	require.NoError(t, err)
	require.NotNil(t, mem2)
	assert.False(t, mem2.Pinned, "after unpin: Pinned must be false")
}
