package actions

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/dotcommander/vybe/internal/models"
	"github.com/dotcommander/vybe/internal/store"
)

const (
	promptMemoryLimit = 5
	promptEventLimit  = 3
)

// ResumeResponse contains the complete response from a resume operation
type ResumeResponse struct {
	AgentName      string             `json:"agent_name"`
	OldCursor      int64              `json:"old_cursor"`
	NewCursor      int64              `json:"new_cursor"`
	Deltas         []*models.Event    `json:"deltas"`
	FocusTaskID    string             `json:"focus_task_id"`
	FocusProjectID string             `json:"focus_project_id,omitempty"`
	Brief          *store.BriefPacket `json:"brief"`
	Prompt         string             `json:"prompt"`
}

// ResumeOptions controls the behavior of a resume operation.
type ResumeOptions struct {
	EventLimit int
	ProjectDir string // When set, scope resume to this project and include recent prompts for it
}

// resumePacket holds pre-computed state for both resume variants.
type resumePacket struct {
	oldCursor      int64
	newCursor      int64
	oldFocusID     string
	focusProjectID string
	focusTaskID    string
	deltas         []*models.Event
	brief          *store.BriefPacket
	recentPrompts  []*models.Event
}

// computeResumePacket builds a resume response packet from current agent state.
// NOTE: This function reads agent state outside the idempotent transaction.
// A concurrent agent can change focus_project_id between this read and the
// subsequent state update. The response reflects computed state, not necessarily
// the final persisted state. This is acceptable because:
// (1) Resume is idempotent — duplicate calls converge
// (2) Brief computation is read-only — no side effects if stale
// (3) The atomic state update in RunIdempotentWithRetry ensures cursor/focus persistence is safe
func computeResumePacket(db *sql.DB, agentName string, opts ResumeOptions) (*resumePacket, error) {
	// Step 1: Load or create agent state
	state, err := store.LoadOrCreateAgentState(db, agentName)
	if err != nil {
		return nil, fmt.Errorf("failed to load agent state: %w", err)
	}

	oldCursor := state.LastSeenEventID
	oldFocusID := state.FocusTaskID
	focusProjectID := state.FocusProjectID
	// ProjectDir override: scopes brief retrieval to this project.
	// RACE NOTE: If another agent concurrently changed focus_project_id,
	// this brief may reflect a different project than what's persisted.
	// This is by design — the brief is advisory, not authoritative.
	if opts.ProjectDir != "" {
		focusProjectID = opts.ProjectDir
	}

	// Step 2: Fetch events since last cursor
	deltas, err := store.FetchEventsSince(db, oldCursor, opts.EventLimit, focusProjectID)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch deltas: %w", err)
	}

	// Calculate new cursor from deltas
	newCursor := oldCursor
	for _, event := range deltas {
		if event.ID > newCursor {
			newCursor = event.ID
		}
	}

	// Step 3: Determine focus task
	focusTaskID, err := store.DetermineFocusTask(db, agentName, oldFocusID, deltas, focusProjectID)
	if err != nil {
		return nil, fmt.Errorf("failed to determine focus task: %w", err)
	}

	// Step 4: Build brief packet
	brief, err := store.BuildBrief(db, focusTaskID, focusProjectID, agentName)
	if err != nil {
		return nil, fmt.Errorf("failed to build brief: %w", err)
	}

	// Step 5: Fetch recent user prompts for project context (best-effort, non-critical)
	recentPrompts, _ := store.FetchRecentUserPrompts(db, focusProjectID, 5) //nolint:errcheck // supplementary context; nil slice is safe

	return &resumePacket{
		oldCursor:      oldCursor,
		newCursor:      newCursor,
		oldFocusID:     oldFocusID,
		focusProjectID: focusProjectID,
		focusTaskID:    focusTaskID,
		deltas:         deltas,
		brief:          brief,
		recentPrompts:  recentPrompts,
	}, nil
}

// buildResumeResponse constructs the final ResumeResponse from a computed packet.
func buildResumeResponse(agentName string, pkt *resumePacket) *ResumeResponse {
	return &ResumeResponse{
		AgentName:      agentName,
		OldCursor:      pkt.oldCursor,
		NewCursor:      pkt.newCursor,
		Deltas:         pkt.deltas,
		FocusTaskID:    pkt.focusTaskID,
		FocusProjectID: pkt.focusProjectID,
		Brief:          pkt.brief,
		Prompt:         buildPrompt(agentName, pkt.brief, pkt.recentPrompts),
	}
}

// buildPrompt generates the context prompt injected into agent sessions.
// Vybe owns this text so hooks just pass it through.
//
// This prompt is consumed by LLM agents, including small/weak models.
// Clarity rules:
//   - Explain what vybe is (one sentence)
//   - Use exact, copy-pasteable commands with real values pre-filled
//   - Mark replaceable parts with UPPER_SNAKE_CASE and explicit instructions
//   - Number the commands so models can reference them
//   - Explain $RANDOM simply: "do not replace it, bash fills it in"
//   - Separate reading (context) from doing (commands) with clear headers
//
//nolint:gocognit,gocyclo,funlen,revive // prompt template requires many conditional sections; splitting into helpers degrades locality and readability
func buildPrompt(agentName string, brief *store.BriefPacket, recentPrompts []*models.Event) string {
	var b strings.Builder

	// Header: what vybe is
	b.WriteString("== VYBE (task tracker) ==\n")

	// --- Context section: read this ---
	if brief != nil && brief.Task != nil {
		t := brief.Task
		b.WriteString("\nYour current task:\n")
		fmt.Fprintf(&b, "  Title: %s\n", t.Title)
		fmt.Fprintf(&b, "  Status: %s\n", t.Status)
		fmt.Fprintf(&b, "  ID: %s\n", t.ID)
		if t.Description != "" {
			fmt.Fprintf(&b, "  Description: %s\n", t.Description)
		}
		// Count actionable tasks (current + pending pipeline)
		actionable := 1 // the focus task itself
		if brief.Counts != nil {
			actionable = brief.Counts.Pending + brief.Counts.InProgress
		}
		fmt.Fprintf(&b, "\n%d task(s) awaiting action in this project.\n", actionable)
	} else {
		b.WriteString("\nNo task assigned. You can work freely.\n")
	}

	if brief != nil && len(brief.RelevantMemory) > 0 {
		b.WriteString("\nSaved notes from previous sessions:\n")
		limit := min(len(brief.RelevantMemory), promptMemoryLimit)
		for i := range limit {
			m := brief.RelevantMemory[i]
			fmt.Fprintf(&b, "  %s = %s\n", m.Key, m.Value)
		}
	}

	if brief != nil && len(brief.RecentEvents) > 0 {
		b.WriteString("\nRecent activity:\n")
		limit := min(len(brief.RecentEvents), promptEventLimit)
		for i := range limit {
			e := brief.RecentEvents[i]
			fmt.Fprintf(&b, "  [%s] %s\n", e.Kind, e.Message)
		}
	}

	if len(recentPrompts) > 0 {
		b.WriteString("\nWhat the user was working on recently:\n")
		for _, e := range recentPrompts {
			msg := e.Message
			if len(msg) > 120 {
				msg = msg[:120] + "..."
			}
			fmt.Fprintf(&b, "  - %s\n", msg)
		}
	}

	if brief != nil && len(brief.PriorReasoning) > 0 {
		b.WriteString("\nPrior reasoning from previous sessions:\n")
		for _, e := range brief.PriorReasoning {
			intent, approach := extractReasoningFields(e.Metadata)
			switch {
			case intent != "" && approach != "":
				fmt.Fprintf(&b, "  - Intent: %s | Approach: %s\n", intent, approach)
			case intent != "":
				fmt.Fprintf(&b, "  - Intent: %s\n", intent)
			case approach != "":
				fmt.Fprintf(&b, "  - Approach: %s\n", approach)
			default:
				msg := e.Message
				if len(msg) > 200 {
					msg = msg[:200] + "..."
				}
				fmt.Fprintf(&b, "  - %s\n", msg)
			}
		}
	}

	// --- Discovery section: pipeline awareness ---
	if brief != nil && brief.Counts != nil {
		c := brief.Counts
		total := c.Pending + c.InProgress + c.Completed + c.Blocked
		if total > 0 {
			fmt.Fprintf(&b, "\nProgress: %d pending, %d in_progress, %d completed, %d blocked (%d total)\n",
				c.Pending, c.InProgress, c.Completed, c.Blocked, total)
		}
	}

	if brief != nil && len(brief.Pipeline) > 0 {
		b.WriteString("\nUp next:\n")
		for _, pt := range brief.Pipeline {
			fmt.Fprintf(&b, "  - %s (%s)\n", pt.Title, pt.ID)
		}
	}

	if brief != nil && len(brief.Unlocks) > 0 {
		b.WriteString("\nCompleting this task unlocks:\n")
		for _, pt := range brief.Unlocks {
			fmt.Fprintf(&b, "  - %s (%s)\n", pt.Title, pt.ID)
		}
	}

	// --- Commands section: do this ---
	if brief != nil && brief.Task != nil {
		t := brief.Task
		b.WriteString("\n== COMMANDS (run in Bash) ==\n")
		b.WriteString("Copy-paste these commands exactly. Only replace UPPER_CASE words.\n\n")

		fmt.Fprintf(&b, "1. DONE — when you finish the task:\n")
		fmt.Fprintf(&b, "   vybe task set-status --agent=%s --request-id=done_$RANDOM --id=%s --status=completed\n\n", agentName, t.ID)

		fmt.Fprintf(&b, "2. STUCK — if you cannot complete the task:\n")
		fmt.Fprintf(&b, "   vybe task set-status --agent=%s --request-id=block_$RANDOM --id=%s --status=blocked\n\n", agentName, t.ID)

		fmt.Fprintf(&b, "3. LOG — to record progress (replace YOUR_MESSAGE):\n")
		fmt.Fprintf(&b, "   vybe push --agent=%s --request-id=log_$RANDOM --json '{\"task_id\":\"%s\",\"event\":{\"kind\":\"progress\",\"message\":\"YOUR_MESSAGE\"}}'\n\n", agentName, t.ID)

		fmt.Fprintf(&b, "4. SAVE — to save a note for future sessions (replace YOUR_KEY and YOUR_VALUE):\n")
		fmt.Fprintf(&b, "   vybe memory set --agent=%s --request-id=mem_$RANDOM --key=YOUR_KEY --value=\"YOUR_VALUE\" --scope=task --scope-id=%s\n\n", agentName, t.ID)

		fmt.Fprintf(&b, "5. THINK — after interpreting what the user wants, capture your reasoning:\n")
		fmt.Fprintf(&b, "   vybe push --agent=%s --request-id=reason_$RANDOM --json '{\"task_id\":\"%s\",\"event\":{\"kind\":\"reasoning\",\"message\":\"INTENT_SUMMARY\",\"metadata\":{\"intent\":\"...\",\"approach\":\"...\",\"files\":[]}}}'\n\n", agentName, t.ID)

		b.WriteString("$RANDOM is a bash variable that generates a unique number. Do not replace it.\n")
	}

	return b.String()
}

// extractReasoningFields parses intent and approach from reasoning event metadata.
// Returns (intent, approach) as named results.
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

// ResumeWithOptionsIdempotent performs Resume once per (agentName, requestID); replays the original response on retries.
//
//nolint:gocognit,gocyclo,funlen,nestif,revive // idempotent resume mirrors non-idempotent but must handle claim contention inside tx closure
func ResumeWithOptionsIdempotent(db *sql.DB, agentName, requestID string, opts ResumeOptions) (*ResumeResponse, error) {
	if agentName == "" {
		return nil, errors.New("agent name is required")
	}
	if requestID == "" {
		return nil, errors.New("request id is required")
	}
	if opts.EventLimit <= 0 {
		opts.EventLimit = 1000
	}
	if opts.EventLimit > 1000 {
		opts.EventLimit = 1000
	}

	// Compute the packet outside the transaction to keep write locks minimal.
	// The transaction is only for the idempotency record + agent_state update.
	// ProjectDir override: scopes brief retrieval to this project.
	// RACE NOTE: If another agent concurrently changed focus_project_id,
	// this brief may reflect a different project than what's persisted.
	// This is by design — the brief is advisory, not authoritative.
	pkt, err := computeResumePacket(db, agentName, opts)
	if err != nil {
		return nil, err
	}

	// Build the response that will be persisted
	resp := buildResumeResponse(agentName, pkt)

	// Wrap only the state update in idempotent transaction
	persisted, _, err := store.RunIdempotentWithRetry(
		db,
		agentName,
		requestID,
		"resume",
		5,
		func(err error) bool {
			return errors.Is(err, store.ErrIdempotencyInProgress) ||
				errors.Is(err, store.ErrVersionConflict) ||
				store.IsVersionConflict(err)
		},
		func(tx *sql.Tx) (ResumeResponse, error) {
			applied := *resp

			// Agents-only heartbeat: always update last_active_at and reconcile head state (cursor/focus).
			if opts.ProjectDir != "" {
				if updateErr := store.UpdateAgentStateAtomicWithProjectTx(tx, agentName, applied.NewCursor, applied.FocusTaskID, applied.FocusProjectID); updateErr != nil {
					return ResumeResponse{}, updateErr
				}
			} else if updateErr := store.UpdateAgentStateAtomicTx(tx, agentName, applied.NewCursor, applied.FocusTaskID); updateErr != nil {
				return ResumeResponse{}, updateErr
			}

			// Return the persisted cursor/focus (may reflect other workers advancing the same agent).
			cursorFocus, loadErr := store.LoadAgentCursorAndFocusTx(tx, agentName)
			if loadErr != nil {
				return ResumeResponse{}, loadErr
			}
			applied.NewCursor = cursorFocus.Cursor
			applied.FocusTaskID = cursorFocus.TaskID
			applied.FocusProjectID = cursorFocus.ProjectID

			return applied, nil
		},
	)
	if err != nil {
		return nil, err
	}

	// After transaction, if focus changed due to contention, rebuild brief with authoritative focus.
	if persisted.FocusTaskID != pkt.focusTaskID {
		newBrief, briefErr := store.BuildBrief(db, persisted.FocusTaskID, persisted.FocusProjectID, agentName)
		if briefErr != nil {
			slog.Default().Warn("failed to rebuild brief after contention", "error", briefErr)
			persisted.Brief = &store.BriefPacket{} // Empty brief, not stale brief
		} else {
			persisted.Brief = newBrief
		}
	}

	return &persisted, nil
}

// Brief returns a brief packet for an agent's current focus without advancing cursor
func Brief(db *sql.DB, agentName string) (*store.BriefPacket, error) {
	if agentName == "" {
		return nil, errors.New("agent name is required")
	}

	// Load agent state
	state, err := store.LoadOrCreateAgentState(db, agentName)
	if err != nil {
		return nil, fmt.Errorf("failed to load agent state: %w", err)
	}

	// Build brief for current focus task and project
	brief, err := store.BuildBrief(db, state.FocusTaskID, state.FocusProjectID, agentName)
	if err != nil {
		return nil, fmt.Errorf("failed to build brief: %w", err)
	}

	return brief, nil
}
