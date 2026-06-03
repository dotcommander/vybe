package actions

import (
	"fmt"
	"time"

	"github.com/dotcommander/vybe/internal/actions/promptbuilder"
	"github.com/dotcommander/vybe/internal/models"
	"github.com/dotcommander/vybe/internal/store"
)

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

// promptContext carries all inputs a section needs to render itself.
type promptContext struct {
	agentName     string
	brief         *store.BriefPacket
	recentPrompts []*models.Event
	task          *models.Task
}

// promptSection is a single renderable unit in the resume prompt.
type promptSection struct {
	render func(b *promptbuilder.Builder, ctx promptContext)
}

// buildPrompt generates the context prompt injected into agent sessions.
func buildPrompt(agentName string, brief *store.BriefPacket, recentPrompts []*models.Event) string {
	b := promptbuilder.New(defaultContextBudget)
	ctx := promptContext{
		agentName:     agentName,
		brief:         brief,
		recentPrompts: recentPrompts,
		task:          getBriefTask(brief),
	}

	b.WriteFixed("== VYBE (task tracker) ==\n")

	// Sections rendered in order. Fixed sections ignore budget; budgeted sections
	// decrement the builder's shared budget and stop when exhausted.
	sections := []promptSection{
		{func(b *promptbuilder.Builder, ctx promptContext) { appendTaskContext(b, ctx.brief, ctx.task) }},
		{func(b *promptbuilder.Builder, ctx promptContext) { appendDecisionProtocol(b, ctx.task) }},
		{func(b *promptbuilder.Builder, ctx promptContext) { appendMemoryContext(b, ctx.brief) }},
		{func(b *promptbuilder.Builder, ctx promptContext) {
			appendRecentPromptsContext(b, ctx.recentPrompts)
		}},
		{func(b *promptbuilder.Builder, ctx promptContext) { appendEventContext(b, ctx.brief) }},
		{func(b *promptbuilder.Builder, ctx promptContext) { appendReasoningContext(b, ctx.brief) }},
		{func(b *promptbuilder.Builder, ctx promptContext) { appendPipelineContext(b, ctx.brief) }},
		{func(b *promptbuilder.Builder, ctx promptContext) { appendTaskCommands(b, ctx.agentName, ctx.task) }},
	}
	for _, s := range sections {
		s.render(b, ctx)
	}

	return b.String()
}

func getBriefTask(brief *store.BriefPacket) *models.Task {
	if brief == nil {
		return nil
	}
	return brief.Task
}

func appendTaskContext(b *promptbuilder.Builder, brief *store.BriefPacket, task *models.Task) {
	if task == nil {
		b.WriteFixed("\nNo task assigned. You can work freely.\n")
		return
	}

	b.WriteFixed("\nYour current task:\n")
	b.WriteFixed(fmt.Sprintf("  Title: %s\n", task.Title))
	b.WriteFixed(fmt.Sprintf("  Status: %s\n", task.Status))
	b.WriteFixed(fmt.Sprintf("  ID: %s\n", task.ID))
	if task.Description != "" {
		b.WriteFixed(fmt.Sprintf("  Description: %s\n", task.Description))
	}

	actionable := 1
	if brief != nil && brief.Counts != nil {
		actionable = brief.Counts.Pending + brief.Counts.InProgress
	}
	b.WriteFixed(fmt.Sprintf("\n%d task(s) awaiting action in this project.\n", actionable))
}

func appendDecisionProtocol(b *promptbuilder.Builder, task *models.Task) {
	if task == nil {
		return
	}
	b.WriteFixed("\nDecision protocol (strict):\n")
	b.WriteFixed(fmt.Sprintf("  - Work only on task_id=%s\n", task.ID))
	b.WriteFixed("  - Before stopping, set terminal status exactly once: completed OR blocked\n")
	b.WriteFixed("  - Use the done/block commands below\n")
}

// appendMemoryContext renders memory in two sections: directives (imperative rules,
// bulleted) first, then facts (key=value, less salient). Staleness tags applied per entry.
// The caveat block appears once after the memory section if ANY entry rendered.
//
// Sort order: kind primary (directives before facts), scope secondary
// (global → project → task → agent) for determinism. The store returns entries
// sorted by pinned DESC, relevance DESC; we re-sort by (kind, scope) here to
// produce the shape the renderer expects.
func appendMemoryContext(b *promptbuilder.Builder, brief *store.BriefPacket) {
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
		b.WriteBudgetedSection("\n=== Directives ===\n", lines)
	}

	if len(facts) > 0 {
		lines := make([]string, len(facts))
		for i, m := range facts {
			tag := staleTag(m.UpdatedAt, m.Pinned, m.ExpiresAt, now)
			lines[i] = fmt.Sprintf("  %s = %s%s\n", m.Key, m.Value, tag)
		}
		b.WriteBudgetedSection("\n=== Facts ===\n", lines)
	}

	if b.Len() > beforeLen {
		// Caveat appears once when ANY memory line rendered (either section).
		// Budget-gated; silently skipped if exhausted.
		_ = b.WriteBudgetedLine(memoryCaveat)
	}
}

func appendEventContext(b *promptbuilder.Builder, brief *store.BriefPacket) {
	if brief == nil || len(brief.RecentEvents) == 0 {
		return
	}
	lines := make([]string, len(brief.RecentEvents))
	for i, event := range brief.RecentEvents {
		lines[i] = fmt.Sprintf("  [%s] %s\n", event.Kind, event.Message)
	}
	b.WriteBudgetedSection("\nRecent activity:\n", lines)
}

func appendRecentPromptsContext(b *promptbuilder.Builder, recentPrompts []*models.Event) {
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
	b.WriteBudgetedSection("\nWhat the user was working on recently:\n", lines)
}

func appendReasoningContext(b *promptbuilder.Builder, brief *store.BriefPacket) {
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
	b.WriteBudgetedSection("\nPrior reasoning from previous sessions:\n", lines)
}

func appendPipelineContext(b *promptbuilder.Builder, brief *store.BriefPacket) {
	if brief == nil {
		return
	}
	if brief.Counts != nil {
		counts := brief.Counts
		total := counts.Pending + counts.InProgress + counts.Completed + counts.Blocked
		if total > 0 {
			b.WriteFixed(fmt.Sprintf("\nProgress: %d pending, %d in_progress, %d completed, %d blocked (%d total)\n",
				counts.Pending, counts.InProgress, counts.Completed, counts.Blocked, total))
		}
	}
	if len(brief.Pipeline) > 0 {
		b.WriteFixed("\nUp next:\n")
		for _, task := range brief.Pipeline {
			b.WriteFixed(fmt.Sprintf("  - %s (%s)\n", task.Title, task.ID))
		}
	}
}

func appendTaskCommands(b *promptbuilder.Builder, agentName string, task *models.Task) {
	if task == nil {
		return
	}
	b.WriteFixed("\n== COMMANDS (canonical agent path) ==\n")
	b.WriteFixed("Run in Bash. Copy-paste exactly. Only replace UPPER_CASE words.\n")
	b.WriteFixed("Required terminal action: run command 1 OR 2 exactly once before stopping.\n\n")

	b.WriteFixed(fmt.Sprintf("1. DONE (required on success):\n"))
	b.WriteFixed(fmt.Sprintf("   vybe done %s --note \"<summary>\"\n\n", task.ID))

	b.WriteFixed(fmt.Sprintf("2. STUCK (required when blocked):\n"))
	b.WriteFixed(fmt.Sprintf("   vybe block %s --reason \"<why>\"  (add --failure so resume skips it)\n\n", task.ID))

	b.WriteFixed(fmt.Sprintf("3. LOG (optional progress):\n"))
	b.WriteFixed(fmt.Sprintf("   vybe note %s \"YOUR_MESSAGE\"\n\n", task.ID))

	b.WriteFixed(fmt.Sprintf("4. SAVE (optional memory):\n"))
	b.WriteFixed(fmt.Sprintf("   vybe remember \"YOUR_KEY=YOUR_VALUE\" --scope task --scope-id %s\n\n", task.ID))

	b.WriteFixed(fmt.Sprintf("5. THINK (optional reasoning checkpoint):\n"))
	b.WriteFixed(fmt.Sprintf("   vybe push --json '{\"task_id\":\"%s\",\"event\":{\"kind\":\"reasoning\",\"message\":\"INTENT_SUMMARY\",\"metadata\":{\"intent\":\"...\",\"approach\":\"...\",\"files\":[]}}}'\n\n", task.ID))

	b.WriteFixed("Omit --request-id (auto-generated). With default_agent set in config, omit --agent too.\n")
}
