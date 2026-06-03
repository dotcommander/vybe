package actions

import (
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"github.com/dotcommander/vybe/internal/models"
)

// scopePriority returns a sort key for scope: global(0) → project(1) → task(2) → agent(3).
// Lower value = higher priority in the brief.
func scopePriority(s models.MemoryScope) int {
	switch s {
	case models.MemoryScopeGlobal:
		return 0
	case models.MemoryScopeProject:
		return 1
	case models.MemoryScopeTask:
		return 2
	case models.MemoryScopeAgent:
		return 3
	}
	return 4
}

// sortMemoryByScope sorts memory entries by scope priority: global → project → task → agent.
// Stable — preserves the store-level ordering (pinned/relevance) within each scope bucket.
func sortMemoryByScope(ms []*models.Memory) {
	sort.SliceStable(ms, func(i, j int) bool {
		return scopePriority(ms[i].Scope) < scopePriority(ms[j].Scope)
	})
}

// staleTag returns an age marker for memory entries that warrant verification.
// Pinned and TTL'd entries return "" — they self-manage freshness.
// now is injected for testability; never call time.Now() inside.
func staleTag(updatedAt time.Time, pinned bool, expiresAt *time.Time, now time.Time) string {
	if pinned || expiresAt != nil {
		return ""
	}
	days := int(now.Sub(updatedAt).Hours() / 24)
	switch {
	case days >= staleHardDays:
		return fmt.Sprintf(" [stale: %dd — verify]", days)
	case days >= staleSoftDays:
		return fmt.Sprintf(" [%dd old]", days)
	default:
		return ""
	}
}

// extractReasoningFields parses intent and approach from reasoning event metadata.
func extractReasoningFields(metadata json.RawMessage) (intent string, approach string) {
	if len(metadata) == 0 {
		return "", ""
	}

	var fields struct {
		Intent   string `json:"intent"`
		Approach string `json:"approach"`
	}
	if err := json.Unmarshal(metadata, &fields); err != nil {
		return "", ""
	}

	return fields.Intent, fields.Approach
}
