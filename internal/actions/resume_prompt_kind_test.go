package actions

import (
	"strings"
	"testing"
	"time"

	"github.com/dotcommander/vybe/internal/models"
	"github.com/dotcommander/vybe/internal/store"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// largeBudget is a sentinel that won't constrain any test output.
const largeBudget = 10000

func briefWithMemory(mems ...*models.Memory) *store.BriefPacket {
	return &store.BriefPacket{RelevantMemory: mems}
}

func mem(key, value, kind string) *models.Memory {
	return &models.Memory{Key: key, Value: value, Kind: kind}
}

func memWithScope(key, value, kind string, scope models.MemoryScope) *models.Memory {
	return &models.Memory{Key: key, Value: value, Kind: kind, Scope: scope}
}

func TestAppendMemoryContext_DirectivesFirst(t *testing.T) {
	t.Parallel()
	brief := briefWithMemory(
		mem("url", "https://api.example.com", "fact"),
		mem("", "always respond in JSON", "directive"),
	)

	var b strings.Builder
	budget := largeBudget
	appendMemoryContext(&b, brief, &budget)

	out := b.String()
	require.Contains(t, out, "=== Directives ===")
	require.Contains(t, out, "=== Facts ===")

	directivesPos := strings.Index(out, "=== Directives ===")
	factsPos := strings.Index(out, "=== Facts ===")
	assert.Less(t, directivesPos, factsPos, "Directives section must appear before Facts section")
}

func TestAppendMemoryContext_OnlyFacts(t *testing.T) {
	t.Parallel()
	brief := briefWithMemory(
		mem("key1", "val1", "fact"),
		mem("key2", "val2", "fact"),
	)

	var b strings.Builder
	budget := largeBudget
	appendMemoryContext(&b, brief, &budget)

	out := b.String()
	assert.NotContains(t, out, "=== Directives ===", "no Directives header when no directives present")
	assert.Contains(t, out, "=== Facts ===")
	assert.Contains(t, out, "key1 = val1")
	assert.Contains(t, out, "key2 = val2")
}

func TestAppendMemoryContext_OnlyDirectives(t *testing.T) {
	t.Parallel()
	brief := briefWithMemory(
		mem("", "be concise", "directive"),
		mem("", "prefer JSON output", "directive"),
	)

	var b strings.Builder
	budget := largeBudget
	appendMemoryContext(&b, brief, &budget)

	out := b.String()
	assert.Contains(t, out, "=== Directives ===")
	assert.NotContains(t, out, "=== Facts ===", "no Facts header when no facts present")
	assert.Contains(t, out, "  - be concise")
	assert.Contains(t, out, "  - prefer JSON output")
}

func TestAppendMemoryContext_CaveatRenderedOnce(t *testing.T) {
	t.Parallel()
	brief := briefWithMemory(
		mem("k", "v", "fact"),
		mem("", "be terse", "directive"),
	)

	var b strings.Builder
	budget := largeBudget
	appendMemoryContext(&b, brief, &budget)

	out := b.String()
	count := strings.Count(out, "recalled memory may be out of date")
	assert.Equal(t, 1, count, "caveat must appear exactly once regardless of how many sections render")
}

func TestAppendMemoryContext_StalenessTagOnDirective(t *testing.T) {
	t.Parallel()
	// 100 days ago — past staleHardDays (90), so we get "[stale: Xd — verify]"
	old := time.Now().Add(-100 * 24 * time.Hour)
	m := &models.Memory{
		Key:       "",
		Value:     "always log errors",
		Kind:      "directive",
		UpdatedAt: old,
	}
	brief := briefWithMemory(m)

	var b strings.Builder
	budget := largeBudget
	appendMemoryContext(&b, brief, &budget)

	out := b.String()
	assert.Contains(t, out, "[stale:", "hard-stale directive must carry [stale:] tag")
}

func TestAppendMemoryContext_PinnedDirectiveNoStaleness(t *testing.T) {
	t.Parallel()
	old := time.Now().Add(-60 * 24 * time.Hour) // well past stale threshold
	m := &models.Memory{
		Key:       "",
		Value:     "always log errors",
		Kind:      "directive",
		UpdatedAt: old,
		Pinned:    true,
	}
	brief := briefWithMemory(m)

	var b strings.Builder
	budget := largeBudget
	appendMemoryContext(&b, brief, &budget)

	out := b.String()
	assert.NotContains(t, out, "[stale:", "pinned directive must not carry staleness tag")
}

func TestAppendMemoryContext_SortByScopeWithinKind(t *testing.T) {
	t.Parallel()
	brief := briefWithMemory(
		memWithScope("task-fact", "tv", "fact", models.MemoryScopeTask),
		memWithScope("global-fact", "gv", "fact", models.MemoryScopeGlobal),
		memWithScope("task-dir", "td", "directive", models.MemoryScopeTask),
		memWithScope("global-dir", "gd", "directive", models.MemoryScopeGlobal),
	)

	var b strings.Builder
	budget := largeBudget
	appendMemoryContext(&b, brief, &budget)

	out := b.String()

	// Within Directives: global before task
	globalDirPos := strings.Index(out, "gd")
	taskDirPos := strings.Index(out, "td")
	assert.Less(t, globalDirPos, taskDirPos, "global directive must appear before task directive")

	// Within Facts: global before task
	globalFactPos := strings.Index(out, "global-fact")
	taskFactPos := strings.Index(out, "task-fact")
	assert.Less(t, globalFactPos, taskFactPos, "global fact must appear before task fact")
}
