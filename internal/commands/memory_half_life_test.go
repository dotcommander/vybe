package commands

import (
	"encoding/json"
	"testing"

	"github.com/dotcommander/vybe/internal/models"
	"github.com/dotcommander/vybe/internal/store"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestMemorySetCmd_HalfLifeDaysFlag verifies the --half-life-days flag is registered,
// stored, and returned correctly in the memory list JSON response.
func TestMemorySetCmd_HalfLifeDaysFlag(t *testing.T) {
	t.Parallel()

	set := newMemorySetCmd()
	requireFlagExists(t, set, "half-life-days")
	assert.Equal(t, "-1", set.Flag("half-life-days").DefValue, "--half-life-days default must be -1")

	// Round-trip via store: set with half_life_days=7, list, verify field is present.
	dir := t.TempDir()
	db, err := store.InitDBWithPath(dir + "/test.db")
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	halfLife := 7.0
	_, err = store.UpsertMemoryWithEventIdempotent(
		db, "agent-hl", "req-hl-flag-1",
		"lesson-key", "some insight", "string",
		"global", "", nil, false, "lesson", &halfLife,
	)
	require.NoError(t, err)

	mems, err := store.ListMemory(db, "global", "")
	require.NoError(t, err)
	require.Len(t, mems, 1)

	// Serialize to JSON and parse back — confirms the field round-trips via encoding/json.
	raw, err := json.Marshal(mems[0])
	require.NoError(t, err)

	var got models.Memory
	require.NoError(t, json.Unmarshal(raw, &got))
	require.NotNil(t, got.HalfLifeDays, "half_life_days must be present in JSON output")
	assert.InDelta(t, 7.0, *got.HalfLifeDays, 0.001, "half_life_days must round-trip as 7.0")
}
