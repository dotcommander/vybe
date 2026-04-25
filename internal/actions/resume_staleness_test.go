package actions

import (
	"strings"
	"testing"
	"time"

	"github.com/dotcommander/vybe/internal/models"
	"github.com/dotcommander/vybe/internal/store"
	"github.com/stretchr/testify/assert"
)

func TestStaleTag(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 4, 24, 12, 0, 0, 0, time.UTC)
	future := now.Add(7 * 24 * time.Hour)

	tests := []struct {
		name      string
		updatedAt time.Time
		pinned    bool
		expiresAt *time.Time
		want      string
	}{
		{
			name:      "fresh 5 days",
			updatedAt: now.Add(-5 * 24 * time.Hour),
			want:      "",
		},
		{
			name:      "boundary 29 days",
			updatedAt: now.Add(-29 * 24 * time.Hour),
			want:      "",
		},
		{
			name:      "soft boundary 30 days",
			updatedAt: now.Add(-30 * 24 * time.Hour),
			want:      " [30d old]",
		},
		{
			name:      "soft 89 days",
			updatedAt: now.Add(-89 * 24 * time.Hour),
			want:      " [89d old]",
		},
		{
			name:      "hard boundary 90 days",
			updatedAt: now.Add(-90 * 24 * time.Hour),
			want:      " [stale: 90d — verify]",
		},
		{
			name:      "pinned ancient bypasses",
			updatedAt: now.Add(-365 * 24 * time.Hour),
			pinned:    true,
			want:      "",
		},
		{
			name:      "ttl ancient bypasses",
			updatedAt: now.Add(-365 * 24 * time.Hour),
			expiresAt: &future,
			want:      "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := staleTag(tt.updatedAt, tt.pinned, tt.expiresAt, now)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestBuildPrompt_MemoryStalenessTagsAndCaveat(t *testing.T) {
	t.Parallel()

	// appendMemoryContext reads time.Now() internally, so anchor ages to real
	// now. Staleness thresholds are days-grained, so sub-second drift is fine.
	now := time.Now()

	brief := &store.BriefPacket{
		Task: &models.Task{ID: "task_1", Title: "T", Status: "in_progress"},
		RelevantMemory: []*models.Memory{
			{Key: "fresh_key", Value: "fresh_val", UpdatedAt: now.Add(-5 * 24 * time.Hour)},
			{Key: "soft_key", Value: "soft_val", UpdatedAt: now.Add(-45 * 24 * time.Hour)},
			{Key: "hard_key", Value: "hard_val", UpdatedAt: now.Add(-142 * 24 * time.Hour)},
		},
		RecentEvents: []*models.Event{},
		Artifacts:    []*models.Artifact{},
	}

	prompt := buildPrompt("agent1", brief, nil)

	assert.Contains(t, prompt, "fresh_key = fresh_val\n", "fresh entry should have no tag")
	assert.NotContains(t, prompt, "fresh_val [")

	assert.Contains(t, prompt, "soft_val [", "soft entry should have tag")
	assert.Regexp(t, `soft_val \[\d+d old\]`, prompt)

	assert.Contains(t, prompt, "hard_val [stale:", "hard entry should have verify tag")
	assert.Regexp(t, `hard_val \[stale: \d+d — verify\]`, prompt)

	assert.Equal(t, 1, strings.Count(prompt, "recalled memory may be out of date"),
		"caveat should appear exactly once")
}

func TestBuildPrompt_EmptyMemoryNoCaveat(t *testing.T) {
	t.Parallel()

	brief := &store.BriefPacket{
		Task:           &models.Task{ID: "task_1", Title: "T", Status: "in_progress"},
		RelevantMemory: []*models.Memory{},
		RecentEvents:   []*models.Event{},
		Artifacts:      []*models.Artifact{},
	}

	prompt := buildPrompt("agent1", brief, nil)
	assert.NotContains(t, prompt, "recalled memory may be out of date",
		"caveat must not render when memory section is empty")
}

func TestBuildPrompt_OnlyPinnedMemoryStillCaveat(t *testing.T) {
	t.Parallel()

	now := time.Now()
	brief := &store.BriefPacket{
		Task: &models.Task{ID: "task_1", Title: "T", Status: "in_progress"},
		RelevantMemory: []*models.Memory{
			{Key: "pinned_key", Value: "pinned_val", UpdatedAt: now.Add(-365 * 24 * time.Hour), Pinned: true},
		},
		RecentEvents: []*models.Event{},
		Artifacts:    []*models.Artifact{},
	}

	prompt := buildPrompt("agent1", brief, nil)
	assert.Contains(t, prompt, "pinned_key = pinned_val\n")
	assert.NotContains(t, prompt, "pinned_val [", "pinned entry bypasses staleness")
	assert.Contains(t, prompt, "recalled memory may be out of date",
		"caveat should render when any memory was emitted, even pinned-only")
}
