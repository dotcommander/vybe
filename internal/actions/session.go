package actions

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/dotcommander/vybe/internal/llm"
	"github.com/dotcommander/vybe/internal/models"
	"github.com/dotcommander/vybe/internal/store"
)

// SessionDigestResult contains a read-only summary of session activity.
// CursorEventID is the agent's last-seen cursor at the time of the digest;
// events returned are those created after this cursor (i.e. during the current session).
type SessionDigestResult struct {
	AgentName     string          `json:"agent_name"`
	ProjectID     string          `json:"project_id,omitempty"`
	CursorEventID int64           `json:"cursor_event_id"`
	EventCount    int             `json:"event_count"`
	EventsByKind  map[string]int  `json:"events_by_kind"`
	Events        []*models.Event `json:"-"` // internal; used by SessionRetrospective
}

// SessionDigest produces a read-only digest of the current session's events.
func SessionDigest(db *sql.DB, agentName string) (*SessionDigestResult, error) {
	if agentName == "" {
		return nil, errors.New("agent name is required")
	}

	state, err := store.LoadOrCreateAgentState(db, agentName)
	if err != nil {
		return nil, fmt.Errorf("load agent state: %w", err)
	}

	events, err := store.FetchSessionEvents(db, state.LastSeenEventID, state.FocusProjectID, 200)
	if err != nil {
		return nil, fmt.Errorf("fetch session events: %w", err)
	}

	byKind := make(map[string]int)
	for _, e := range events {
		byKind[e.Kind]++
	}

	return &SessionDigestResult{
		AgentName:     agentName,
		ProjectID:     state.FocusProjectID,
		CursorEventID: state.LastSeenEventID,
		EventCount:    len(events),
		EventsByKind:  byKind,
		Events:        events,
	}, nil
}

// Lesson represents a single extracted insight from session retrospective.
type Lesson struct {
	Type  string `json:"type"`
	Key   string `json:"key"`
	Value string `json:"value"`
	Scope string `json:"scope"`
}

// sessionRetrospectiveResult captures the outcome of a retrospective operation.
type sessionRetrospectiveResult struct {
	AgentName    string  `json:"agent_name"`
	ProjectID    string  `json:"project_id,omitempty"`
	EventCount   int     `json:"event_count"`
	LessonsCount int     `json:"lessons_count"`
	EventIDs     []int64 `json:"event_ids,omitempty"`
	Skipped      bool    `json:"skipped"`
	SkipReason   string  `json:"skip_reason,omitempty"`
}

const extractionSystemPrompt = `You are analyzing an AI agent's session activity to extract durable knowledge.
Output a JSON array of lessons. Each lesson:
- "type": "correction" | "preference" | "pattern" | "knowledge"
- "key": short snake_case identifier (max 64 chars)
- "value": concise lesson description (max 256 chars)
- "scope": "project" (default) or "global" (ONLY for universal agent behavior patterns, NOT domain knowledge)
Rules: Only useful across sessions. Fewer high-quality > many low-quality. Max 10. JSON array only, no markdown fencing.
Scope guidance: domain knowledge, codebase patterns, API details = "project". Universal agent corrections = "global".`

// extractRuleBasedLessons applies deterministic pattern matching to extract lessons
// without requiring LLM availability.
func extractRuleBasedLessons(events []*models.Event) []Lesson {
	var lessons []Lesson

	// Pattern: repeated tool failures (same tool >= 2x) → correction lesson
	toolFailures := make(map[string]int)
	for _, e := range events {
		if e.Kind == models.EventKindToolFailure {
			// Extract tool name from message (format: "toolname failed" or "toolname (event)")
			parts := strings.Fields(e.Message)
			if len(parts) > 0 {
				toolFailures[parts[0]]++
			}
		}
	}
	for tool, count := range toolFailures {
		if count >= 2 {
			lessons = append(lessons, Lesson{
				Type:  "correction",
				Key:   fmt.Sprintf("repeated_%s_failure", sanitizeKey(tool)),
				Value: fmt.Sprintf("%s failed %dx in session — investigate root cause", tool, count),
				Scope: "project",
			})
		}
	}

	// Pattern: task completed → knowledge lesson
	for _, e := range events {
		if e.Kind == "task_completed_signal" || e.Kind == models.EventKindTaskStatus {
			if strings.Contains(e.Message, "completed") {
				lessons = append(lessons, Lesson{
					Type:  "knowledge",
					Key:   "task_completion_observed",
					Value: fmt.Sprintf("Task completed: %s", truncate(e.Message, 200)),
					Scope: "project",
				})
				break // Only record one completion lesson per session
			}
		}
	}

	if len(lessons) > 10 {
		lessons = lessons[:10]
	}

	return lessons
}

// sanitizeKey converts a string to a safe snake_case key fragment.
func sanitizeKey(s string) string {
	s = strings.ToLower(s)
	s = strings.Map(func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '_' {
			return r
		}
		return '_'
	}, s)
	if len(s) > 40 {
		s = s[:40]
	}
	return s
}

// persistLessons stores extracted lessons as memory entries via idempotent batch upsert.
// All lessons are persisted in a single transaction. Returns (eventIDs, error).
// On failure, all upserts are rolled back; no partial writes.
func persistLessons(db *sql.DB, agentName, requestIDPrefix, projectID string, lessons []Lesson) ([]int64, error) {
	if len(lessons) == 0 {
		return nil, nil
	}

	// Validate and prepare lessons upfront (fail fast before transaction)
	type preparedLesson struct {
		key     string
		value   string
		scope   string
		scopeID string
	}
	prepared := make([]preparedLesson, 0, len(lessons))

	for _, lesson := range lessons {
		key := truncate(lesson.Key, 64)
		value := truncate(lesson.Value, 256)
		if key == "" {
			continue // skip empty keys
		}

		scope := lesson.Scope
		scopeID := ""
		if scope == "project" && projectID != "" {
			scopeID = projectID
		} else if scope == "project" {
			continue // drop: no projectID available, don't promote to global
		}
		// scope == "global" passes through unchanged

		prepared = append(prepared, preparedLesson{key: key, value: value, scope: scope, scopeID: scopeID})
	}

	if len(prepared) == 0 {
		return nil, nil
	}

	// Batch upsert in single idempotent transaction
	type batchResult struct {
		EventIDs []int64 `json:"event_ids"`
	}

	r, err := store.RunIdempotent(db, agentName, requestIDPrefix, "lessons.batch_upsert", func(tx *sql.Tx) (batchResult, error) {
		eventIDs := make([]int64, 0, len(prepared))
		for _, pl := range prepared {
			eventID, upsertErr := store.UpsertMemoryTx(tx, agentName, pl.key, pl.value, "string", pl.scope, pl.scopeID, nil)
			if upsertErr != nil {
				return batchResult{}, fmt.Errorf("failed to upsert lesson %q: %w", pl.key, upsertErr)
			}
			eventIDs = append(eventIDs, eventID)
		}
		return batchResult{EventIDs: eventIDs}, nil
	})

	if err != nil {
		return nil, err
	}

	return r.EventIDs, nil
}

// SessionRetrospective analyzes session events and extracts durable lessons as memory.
//
//nolint:funlen,nestif // retrospective orchestrates LLM extraction with fallback to rule-based extraction; splitting degrades the fallback flow
func SessionRetrospective(db *sql.DB, agentName, requestIDPrefix, projectIDHint string) (*sessionRetrospectiveResult, error) {
	return sessionRetrospective(db, agentName, requestIDPrefix, projectIDHint, true)
}

// SessionRetrospectiveRuleOnly performs retrospective extraction without invoking
// external LLM CLIs. This avoids hook recursion in background job workers.
func SessionRetrospectiveRuleOnly(db *sql.DB, agentName, requestIDPrefix, projectIDHint string) (*sessionRetrospectiveResult, error) {
	return sessionRetrospective(db, agentName, requestIDPrefix, projectIDHint, false)
}

//nolint:funlen,nestif // same complexity as SessionRetrospective; split would duplicate flow.
func sessionRetrospective(db *sql.DB, agentName, requestIDPrefix, projectIDHint string, allowLLM bool) (*sessionRetrospectiveResult, error) {
	if agentName == "" {
		return nil, errors.New("agent name is required")
	}

	digest, err := SessionDigest(db, agentName)
	if err != nil {
		return nil, fmt.Errorf("session digest: %w", err)
	}

	// Use CWD hint as fallback when agent_state has no focus project
	if digest.ProjectID == "" && projectIDHint != "" {
		digest.ProjectID = projectIDHint
	}
	return sessionRetrospectiveFromDigest(db, requestIDPrefix, digest, allowLLM)
}

//nolint:funlen,nestif // same complexity as sessionRetrospective; split would duplicate flow.
func sessionRetrospectiveFromDigest(db *sql.DB, requestIDPrefix string, digest *SessionDigestResult, allowLLM bool) (*sessionRetrospectiveResult, error) {
	if digest == nil {
		return nil, errors.New("session digest is required")
	}

	result := &sessionRetrospectiveResult{
		AgentName:  digest.AgentName,
		ProjectID:  digest.ProjectID,
		EventCount: digest.EventCount,
	}

	if digest.EventCount < 2 {
		result.Skipped = true
		result.SkipReason = "insufficient events (< 2)"
		return result, nil
	}

	var lessons []Lesson

	if allowLLM {
		runner, runnerErr := llm.NewRunner(digest.AgentName)
		if runnerErr != nil {
			slog.Default().Debug("LLM runner not available, falling back to rules", "error", runnerErr)
		}
		if runner != nil {
			// Build extraction prompt from events (cap at ~8000 chars)
			var b strings.Builder
			b.WriteString(extractionSystemPrompt)
			b.WriteString("\n\nSession events:\n")
			totalChars := 0
			for i, e := range digest.Events {
				line := fmt.Sprintf("[%d] [%s] %s\n", i+1, e.Kind, e.Message)
				if totalChars+len(line) > 8000 {
					break
				}
				b.WriteString(line)
				totalChars += len(line)
			}

			ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
			defer cancel()

			raw, llmErr := runner.Extract(ctx, b.String())
			if llmErr != nil {
				slog.Default().Warn("retrospective LLM extraction failed, falling back to rules", "error", llmErr, "cli", runner.Command())
			} else {
				raw = strings.TrimSpace(raw)
				raw = strings.TrimPrefix(raw, "```json")
				raw = strings.TrimPrefix(raw, "```")
				raw = strings.TrimSuffix(raw, "```")
				raw = strings.TrimSpace(raw)

				if parseErr := json.Unmarshal([]byte(raw), &lessons); parseErr != nil {
					slog.Default().Warn("retrospective parse failed, falling back to rules", "error", parseErr, "raw", raw)
					lessons = nil
				}
			}
		}
	}

	// Fallback to rule-based extraction if LLM produced nothing
	if len(lessons) == 0 {
		lessons = extractRuleBasedLessons(digest.Events)
	}

	if len(lessons) == 0 {
		result.Skipped = true
		result.SkipReason = "no lessons extracted"
		return result, nil
	}

	if len(lessons) > 10 {
		lessons = lessons[:10]
	}

	eventIDs, persistErr := persistLessons(db, digest.AgentName, requestIDPrefix, digest.ProjectID, lessons)
	if persistErr != nil {
		slog.Default().Warn("retrospective persist failed", "error", persistErr, "lessons", len(lessons))
	}

	result.LessonsCount = len(eventIDs)
	result.EventIDs = eventIDs
	return result, nil
}

// truncate returns s capped at maxLen characters.
func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen]
}

// AutoSummarizeEventsIdempotent archives old events when active count exceeds threshold,
// keeping the most recent keepRecent events active.
// Returns (summaryEventID, archivedCount) or (0, 0) if below threshold.
//
//nolint:revive // argument-limit: all params (agent, req, project, threshold, keepRecent) required together
func AutoSummarizeEventsIdempotent(db *sql.DB, agentName, requestID, projectID string, threshold, keepRecent int) (summaryEventID int64, archivedCount int64, err error) {
	if agentName == "" {
		return 0, 0, errors.New("agent name is required")
	}
	if requestID == "" {
		return 0, 0, errors.New("request id is required")
	}

	count, err := store.CountActiveEvents(db, projectID)
	if err != nil {
		return 0, 0, fmt.Errorf("count active events: %w", err)
	}
	if count < int64(threshold) {
		return 0, 0, nil
	}

	fromID, toID, err := store.FindArchiveWindow(db, projectID, keepRecent)
	if err != nil {
		return 0, 0, fmt.Errorf("find archive window: %w", err)
	}
	if fromID == 0 && toID == 0 {
		return 0, 0, nil
	}

	summary := fmt.Sprintf("Auto-compressed events %d–%d (%d active exceeded threshold %d)", fromID, toID, count, threshold)

	var autoSumErr error
	summaryEventID, archivedCount, autoSumErr = store.ArchiveEventsRangeWithSummaryIdempotent(
		db, agentName, requestID, projectID, "", fromID, toID, summary,
	)
	if autoSumErr != nil {
		return 0, 0, fmt.Errorf("archive events: %w", autoSumErr)
	}

	return summaryEventID, archivedCount, nil
}

// AutoPruneArchivedEventsIdempotent permanently deletes archived events older
// than olderThanDays. Deletion is bounded by limit per call.
func AutoPruneArchivedEventsIdempotent(db *sql.DB, agentName, requestID, projectID string, olderThanDays, limit int) (deletedCount int64, err error) {
	if agentName == "" {
		return 0, errors.New("agent name is required")
	}
	if requestID == "" {
		return 0, errors.New("request id is required")
	}

	deleted, err := store.PruneArchivedEventsIdempotent(db, agentName, requestID, projectID, olderThanDays, limit)
	if err != nil {
		return 0, fmt.Errorf("prune archived events: %w", err)
	}

	return deleted, nil
}
