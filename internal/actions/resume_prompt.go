package actions

import (
	"encoding/json"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/dotcommander/vybe/internal/models"
	"github.com/dotcommander/vybe/internal/store"
)

const (
	// defaultContextBudget is the token budget for variable prompt sections
	// (memory, recent prompts, events, reasoning). Fixed sections always included.
	defaultContextBudget = 1500
)

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

func appendMemoryContext(b *strings.Builder, brief *store.BriefPacket, remainingBudget *int) {
	if brief == nil || len(brief.RelevantMemory) == 0 || *remainingBudget <= 0 {
		return
	}

	header := "\nSaved notes from previous sessions:\n"
	for _, memory := range brief.RelevantMemory {
		line := fmt.Sprintf("  %s = %s\n", memory.Key, memory.Value)
		if header != "" {
			line = header + line
			header = ""
		}
		if !appendBudgetedLine(b, line, remainingBudget) {
			break
		}
	}
}

func appendEventContext(b *strings.Builder, brief *store.BriefPacket, remainingBudget *int) {
	if brief == nil || len(brief.RecentEvents) == 0 || *remainingBudget <= 0 {
		return
	}

	header := "\nRecent activity:\n"
	for _, event := range brief.RecentEvents {
		line := fmt.Sprintf("  [%s] %s\n", event.Kind, event.Message)
		if header != "" {
			line = header + line
			header = ""
		}
		if !appendBudgetedLine(b, line, remainingBudget) {
			break
		}
	}
}

func appendRecentPromptsContext(b *strings.Builder, recentPrompts []*models.Event, remainingBudget *int) {
	if len(recentPrompts) == 0 || *remainingBudget <= 0 {
		return
	}

	header := "\nWhat the user was working on recently:\n"
	for _, event := range recentPrompts {
		msg := event.Message
		if r := []rune(msg); len(r) > 120 {
			msg = string(r[:120]) + "..."
		}
		line := fmt.Sprintf("  - %s\n", msg)
		if header != "" {
			line = header + line
			header = ""
		}
		if !appendBudgetedLine(b, line, remainingBudget) {
			break
		}
	}
}

func appendReasoningContext(b *strings.Builder, brief *store.BriefPacket, remainingBudget *int) {
	if brief == nil || len(brief.PriorReasoning) == 0 || *remainingBudget <= 0 {
		return
	}

	header := "\nPrior reasoning from previous sessions:\n"
	for _, event := range brief.PriorReasoning {
		intent, approach := extractReasoningFields(event.Metadata)
		var line string
		switch {
		case intent != "" && approach != "":
			line = fmt.Sprintf("  - Intent: %s | Approach: %s\n", intent, approach)
		case intent != "":
			line = fmt.Sprintf("  - Intent: %s\n", intent)
		case approach != "":
			line = fmt.Sprintf("  - Approach: %s\n", approach)
		default:
			msg := event.Message
			if r := []rune(msg); len(r) > 200 {
				msg = string(r[:200]) + "..."
			}
			line = fmt.Sprintf("  - %s\n", msg)
		}
		if header != "" {
			line = header + line
			header = ""
		}
		if !appendBudgetedLine(b, line, remainingBudget) {
			break
		}
	}
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
