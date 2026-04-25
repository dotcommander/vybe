package actions

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/dotcommander/vybe/internal/models"
	"github.com/dotcommander/vybe/internal/store"
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

const (
	// defaultContextBudget is the token budget for variable prompt sections
	// (memory, recent prompts, events, reasoning). Fixed sections always included.
	defaultContextBudget = 1500

	// staleSoftDays: memory older than this gets a soft age marker; agent
	// should treat it as possibly drifted.
	staleSoftDays = 30
	// staleHardDays: memory older than this gets a hard "verify" marker.
	staleHardDays = 90
)

// memoryCaveat is appended once after the memory section when any entries were
// rendered. Reminds the agent to trust observed code over recalled facts.
const memoryCaveat = "\nNote: recalled memory may be out of date. If a fact contradicts current code, trust what you observe and update the memory.\n"

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

// buildPrompt generates the context prompt injected into agent sessions.
func buildPrompt(agentName string, brief *store.BriefPacket, recentPrompts []*models.Event) string {
	var b strings.Builder

	b.WriteString("== VYBE (task tracker) ==\n")
	task := getBriefTask(brief)

	// Fixed sections — always included, not counted against budget.
	appendTaskContext(&b, brief, task)
	appendDecisionProtocol(&b, task)

	// Variable sections — ranked by priority, filled until budget exhausted.
	budget := defaultContextBudget
	appendMemoryContext(&b, brief, &budget)
	appendRecentPromptsContext(&b, recentPrompts, &budget)
	appendEventContext(&b, brief, &budget)
	appendReasoningContext(&b, brief, &budget)

	// Fixed sections — always included.
	appendPipelineContext(&b, brief)
	appendTaskCommands(&b, agentName, task)

	return b.String()
}

func getBriefTask(brief *store.BriefPacket) *models.Task {
	if brief == nil {
		return nil
	}
	return brief.Task
}

func appendTaskContext(b *strings.Builder, brief *store.BriefPacket, task *models.Task) {
	if task == nil {
		b.WriteString("\nNo task assigned. You can work freely.\n")
		return
	}

	b.WriteString("\nYour current task:\n")
	fmt.Fprintf(b, "  Title: %s\n", task.Title)
	fmt.Fprintf(b, "  Status: %s\n", task.Status)
	fmt.Fprintf(b, "  ID: %s\n", task.ID)
	if task.Description != "" {
		fmt.Fprintf(b, "  Description: %s\n", task.Description)
	}

	actionable := 1
	if brief != nil && brief.Counts != nil {
		actionable = brief.Counts.Pending + brief.Counts.InProgress
	}
	fmt.Fprintf(b, "\n%d task(s) awaiting action in this project.\n", actionable)
}

func appendDecisionProtocol(b *strings.Builder, task *models.Task) {
	if task == nil {
		return
	}

	b.WriteString("\nDecision protocol (strict):\n")
	fmt.Fprintf(b, "  - Work only on task_id=%s\n", task.ID)
	b.WriteString("  - Before stopping, set terminal status exactly once: completed OR blocked\n")
	b.WriteString("  - Use DONE/STUCK commands below (set-status path)\n")
}

// appendBudgetedSection writes header + lines to b, one line at a time,
// charging each against remainingBudget. Stops at the first line that exceeds budget.
// The header is prepended onto the first line only.
func appendBudgetedSection(b *strings.Builder, header string, lines []string, remainingBudget *int) {
	if len(lines) == 0 || *remainingBudget <= 0 {
		return
	}
	for i, line := range lines {
		if i == 0 {
			line = header + line
		}
		if !appendBudgetedLine(b, line, remainingBudget) {
			return
		}
	}
}

// appendMemoryContext renders memory in two sections: directives (imperative rules,
// bulleted) first, then facts (key=value, less salient). Staleness tags applied per entry.
// The caveat block appears once after the memory section if ANY entry rendered.
//
// Sort order: kind primary (directives before facts), scope secondary
// (global → project → task → agent) for determinism. The store returns entries
// sorted by pinned DESC, relevance DESC; we re-sort by (kind, scope) here to
// produce the shape the renderer expects.
func appendMemoryContext(b *strings.Builder, brief *store.BriefPacket, remainingBudget *int) {
	if brief == nil || len(brief.RelevantMemory) == 0 {
		return
	}
	now := time.Now()

	var directives, facts []*models.Memory
	for _, m := range brief.RelevantMemory {
		if m.Kind == string(models.MemoryKindDirective) {
			directives = append(directives, m)
		} else {
			// Includes "fact" and (defensively) any unrecognized value;
			// store CHECK constraint prevents unknown kinds from ever persisting.
			facts = append(facts, m)
		}
	}
	sortMemoryByScope(directives)
	sortMemoryByScope(facts)

	beforeLen := b.Len()

	if len(directives) > 0 {
		lines := make([]string, len(directives))
		for i, m := range directives {
			tag := staleTag(m.UpdatedAt, m.Pinned, m.ExpiresAt, now)
			lines[i] = fmt.Sprintf("  - %s%s\n", m.Value, tag)
		}
		appendBudgetedSection(b, "\n=== Directives ===\n", lines, remainingBudget)
	}

	if len(facts) > 0 {
		lines := make([]string, len(facts))
		for i, m := range facts {
			tag := staleTag(m.UpdatedAt, m.Pinned, m.ExpiresAt, now)
			lines[i] = fmt.Sprintf("  %s = %s%s\n", m.Key, m.Value, tag)
		}
		appendBudgetedSection(b, "\n=== Facts ===\n", lines, remainingBudget)
	}

	if b.Len() > beforeLen {
		// Caveat appears once when ANY memory line rendered (either section).
		// Budget-gated; silently skipped if exhausted.
		_ = appendBudgetedLine(b, memoryCaveat, remainingBudget)
	}
}

func appendEventContext(b *strings.Builder, brief *store.BriefPacket, remainingBudget *int) {
	if brief == nil || len(brief.RecentEvents) == 0 {
		return
	}
	lines := make([]string, len(brief.RecentEvents))
	for i, event := range brief.RecentEvents {
		lines[i] = fmt.Sprintf("  [%s] %s\n", event.Kind, event.Message)
	}
	appendBudgetedSection(b, "\nRecent activity:\n", lines, remainingBudget)
}

func appendRecentPromptsContext(b *strings.Builder, recentPrompts []*models.Event, remainingBudget *int) {
	if len(recentPrompts) == 0 {
		return
	}
	lines := make([]string, len(recentPrompts))
	for i, event := range recentPrompts {
		msg := event.Message
		if r := []rune(msg); len(r) > 120 {
			msg = string(r[:120]) + "..."
		}
		lines[i] = fmt.Sprintf("  - %s\n", msg)
	}
	appendBudgetedSection(b, "\nWhat the user was working on recently:\n", lines, remainingBudget)
}

func appendReasoningContext(b *strings.Builder, brief *store.BriefPacket, remainingBudget *int) {
	if brief == nil || len(brief.PriorReasoning) == 0 {
		return
	}
	lines := make([]string, len(brief.PriorReasoning))
	for i, event := range brief.PriorReasoning {
		intent, approach := extractReasoningFields(event.Metadata)
		switch {
		case intent != "" && approach != "":
			lines[i] = fmt.Sprintf("  - Intent: %s | Approach: %s\n", intent, approach)
		case intent != "":
			lines[i] = fmt.Sprintf("  - Intent: %s\n", intent)
		case approach != "":
			lines[i] = fmt.Sprintf("  - Approach: %s\n", approach)
		default:
			msg := event.Message
			if r := []rune(msg); len(r) > 200 {
				msg = string(r[:200]) + "..."
			}
			lines[i] = fmt.Sprintf("  - %s\n", msg)
		}
	}
	appendBudgetedSection(b, "\nPrior reasoning from previous sessions:\n", lines, remainingBudget)
}

func appendPipelineContext(b *strings.Builder, brief *store.BriefPacket) {
	if brief == nil {
		return
	}

	appendProgressCountsContext(b, brief)
	appendPipelineTasksContext(b, brief.Pipeline)
}

func appendProgressCountsContext(b *strings.Builder, brief *store.BriefPacket) {
	if brief.Counts == nil {
		return
	}

	counts := brief.Counts
	total := counts.Pending + counts.InProgress + counts.Completed + counts.Blocked
	if total == 0 {
		return
	}

	fmt.Fprintf(b, "\nProgress: %d pending, %d in_progress, %d completed, %d blocked (%d total)\n",
		counts.Pending, counts.InProgress, counts.Completed, counts.Blocked, total)
}

func appendPipelineTasksContext(b *strings.Builder, pipeline []store.PipelineTask) {
	if len(pipeline) == 0 {
		return
	}

	b.WriteString("\nUp next:\n")
	for _, task := range pipeline {
		fmt.Fprintf(b, "  - %s (%s)\n", task.Title, task.ID)
	}
}

func appendTaskCommands(b *strings.Builder, agentName string, task *models.Task) {
	if task == nil {
		return
	}

	b.WriteString("\n== COMMANDS (canonical agent path) ==\n")
	b.WriteString("Run in Bash. Copy-paste exactly. Only replace UPPER_CASE words.\n")
	b.WriteString("Required terminal action: run command 1 OR 2 exactly once before stopping.\n\n")

	fmt.Fprintf(b, "1. DONE (required on success):\n")
	fmt.Fprintf(b, "   vybe task set-status --agent=%s --request-id=done_$RANDOM --id=%s --status=completed\n\n", agentName, task.ID)

	fmt.Fprintf(b, "2. STUCK (required when blocked):\n")
	fmt.Fprintf(b, "   vybe task set-status --agent=%s --request-id=block_$RANDOM --id=%s --status=blocked\n\n", agentName, task.ID)

	fmt.Fprintf(b, "3. LOG (optional progress):\n")
	fmt.Fprintf(b, "   vybe push --agent=%s --request-id=log_$RANDOM --json '{\"task_id\":\"%s\",\"event\":{\"kind\":\"progress\",\"message\":\"YOUR_MESSAGE\"}}'\n\n", agentName, task.ID)

	fmt.Fprintf(b, "4. SAVE (optional memory):\n")
	fmt.Fprintf(b, "   vybe memory set --agent=%s --request-id=mem_$RANDOM --key=YOUR_KEY --value=\"YOUR_VALUE\" --scope=task --scope-id=%s\n\n", agentName, task.ID)

	fmt.Fprintf(b, "5. THINK (optional reasoning checkpoint):\n")
	fmt.Fprintf(b, "   vybe push --agent=%s --request-id=reason_$RANDOM --json '{\"task_id\":\"%s\",\"event\":{\"kind\":\"reasoning\",\"message\":\"INTENT_SUMMARY\",\"metadata\":{\"intent\":\"...\",\"approach\":\"...\",\"files\":[]}}}'\n\n", agentName, task.ID)

	b.WriteString("$RANDOM is a bash variable that generates a unique number. Do not replace it.\n")
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

// estimateTokens estimates the token count for a string using the chars/4 heuristic.
func estimateTokens(s string) int {
	return (utf8.RuneCountInString(s) + 3) / 4
}

// appendBudgetedLine writes line to b if it fits within remainingBudget.
// Returns true if the line was written, false if budget exhausted.
func appendBudgetedLine(b *strings.Builder, line string, remainingBudget *int) bool {
	cost := estimateTokens(line)
	if cost > *remainingBudget {
		return false
	}
	b.WriteString(line)
	*remainingBudget -= cost
	return true
}
