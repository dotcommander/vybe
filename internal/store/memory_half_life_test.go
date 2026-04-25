package store

import (
	"testing"

	"github.com/dotcommander/vybe/internal/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func ptr(f float64) *float64 { return &f }

func TestMemoryHalfLife_KindDefaults(t *testing.T) {
	t.Parallel()
	// A lesson inserted now with access_count=0 and last_accessed_at set to 14 days ago
	// should have relevance ≈ 0.5 ± 0.1:
	//   days_elapsed / half_life = 14 / 14 = 1
	//   relevance = (1+0) / (1+1) = 0.5
	db, cleanup := setupMemoryTestDB(t)
	t.Cleanup(cleanup)

	require.NoError(t, SetMemory(db, "lesson-key", "insight", "string", "global", "", nil, false, "lesson", nil))
	_, err := db.Exec(`UPDATE memory SET last_accessed_at = datetime('now', '-14 days') WHERE key = 'lesson-key'`)
	require.NoError(t, err)

	mems, err := fetchRelevantMemory(db, "", "")
	require.NoError(t, err)

	var found *models.Memory
	for _, m := range mems {
		if m.Key == "lesson-key" {
			found = m
			break
		}
	}
	require.NotNil(t, found, "lesson-key must appear in relevant memory")
	assert.InDelta(t, 0.5, found.Relevance, 0.1, "lesson at exactly one half-life should have relevance ≈ 0.5")
}

func TestMemoryHalfLife_DirectiveNoDecay(t *testing.T) {
	t.Parallel()
	// A directive with last_accessed_at = 365 days ago should have relevance ≈ 1.0:
	//   days_elapsed / half_life = 365 / 1e9 ≈ 0
	//   relevance = (1+0) / (1+0) = 1.0
	db, cleanup := setupMemoryTestDB(t)
	t.Cleanup(cleanup)

	require.NoError(t, SetMemory(db, "dir-key", "never do X", "string", "global", "", nil, false, "directive", nil))
	_, err := db.Exec(`UPDATE memory SET last_accessed_at = datetime('now', '-365 days') WHERE key = 'dir-key'`)
	require.NoError(t, err)

	mems, err := fetchRelevantMemory(db, "", "")
	require.NoError(t, err)

	var found *models.Memory
	for _, m := range mems {
		if m.Key == "dir-key" {
			found = m
			break
		}
	}
	require.NotNil(t, found, "dir-key must appear in relevant memory")
	assert.InDelta(t, 1.0, found.Relevance, 0.01, "directive should have near-zero decay after 365 days")
}

func TestMemoryHalfLife_ExplicitOverride(t *testing.T) {
	t.Parallel()
	// A fact with half_life_days=1.0 and last_accessed_at = 1 day ago:
	//   days_elapsed / half_life = 1 / 1 = 1
	//   relevance = (1+0) / (1+1) = 0.5
	db, cleanup := setupMemoryTestDB(t)
	t.Cleanup(cleanup)

	require.NoError(t, SetMemory(db, "fast-decay", "value", "string", "global", "", nil, false, "fact", ptr(1.0)))
	_, err := db.Exec(`UPDATE memory SET last_accessed_at = datetime('now', '-1 days') WHERE key = 'fast-decay'`)
	require.NoError(t, err)

	mems, err := fetchRelevantMemory(db, "", "")
	require.NoError(t, err)

	var found *models.Memory
	for _, m := range mems {
		if m.Key == "fast-decay" {
			found = m
			break
		}
	}
	require.NotNil(t, found, "fast-decay must appear in relevant memory")
	assert.InDelta(t, 0.5, found.Relevance, 0.1, "fact with 1-day half-life at 1 day old should have relevance ≈ 0.5")
}

func TestMemoryHalfLife_StickyPreserve(t *testing.T) {
	t.Parallel()
	// Upsert a lesson with half_life=7, then upsert the same key WITHOUT half_life.
	// The stored half_life_days must still be 7.
	db, cleanup := setupMemoryTestDB(t)
	t.Cleanup(cleanup)

	_, err := UpsertMemoryWithEventIdempotent(db, "agent", "req-hl-sticky-1", "sticky-key", "v1", "string", "global", "", nil, false, "lesson", ptr(7.0))
	require.NoError(t, err)

	// Second upsert without half_life — nil must not clobber stored value
	_, err = UpsertMemoryWithEventIdempotent(db, "agent", "req-hl-sticky-2", "sticky-key", "v2", "string", "global", "", nil, false, "lesson", nil)
	require.NoError(t, err)

	var storedHL *float64
	row := db.QueryRow(`SELECT half_life_days FROM memory WHERE key = 'sticky-key' AND scope = 'global' AND scope_id = ''`)
	require.NoError(t, row.Scan(&storedHL))
	require.NotNil(t, storedHL, "half_life_days must not be cleared by a nil upsert")
	assert.InDelta(t, 7.0, *storedHL, 0.001, "half_life_days must remain 7.0 after nil upsert")
}
