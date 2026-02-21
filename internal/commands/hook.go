package commands

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"

	"github.com/dotcommander/vybe/internal/actions"
	"github.com/dotcommander/vybe/internal/app"
	"github.com/dotcommander/vybe/internal/models"
	"github.com/dotcommander/vybe/internal/store"
	"github.com/spf13/cobra"
)

const (
	// maxHookStdinBytes caps stdin reads. Hook payloads are small JSON objects;
	// 1 MB is generous headroom that prevents unbounded allocation.
	maxHookStdinBytes = 1 << 20

	// defaultAgentName is the default agent identity used by hooks when no --agent flag is provided.
	defaultAgentName = "claude"

	// retroChildEnv marks processes spawned for retrospective extraction to prevent hook recursion.
	retroChildEnv = "VYBE_RETRO_CHILD"

	// disableExternalLLMEnv blocks claude/opencode subprocess execution in guarded flows.
	disableExternalLLMEnv = "VYBE_DISABLE_EXTERNAL_LLM"
)

// hookSeqCounter provides monotonic fallback entropy when crypto/rand fails.
var hookSeqCounter uint64 //nolint:gochecknoglobals // atomic counter shared across hook invocations; required for fallback entropy

// NewHookCmd creates the hook parent command.
func NewHookCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "hook",
		Short: "Hook handlers and installers for Claude/OpenCode",
		Args:  cobra.NoArgs,
	}

	cmd.AddCommand(newHookInstallCmd())
	cmd.AddCommand(newHookUninstallCmd())

	// Hook handler subcommands — called by the hook system, not agents directly.
	// Hidden from help output to reduce command surface noise.
	for _, sub := range []*cobra.Command{
		newHookSessionStartCmd(),
		newHookPromptCmd(),
		newHookToolFailureCmd(),
		newHookToolSuccessCmd(),
		newHookCheckpointCmd(),
		newHookTaskCompletedCmd(),
		newHookRetrospectiveCmd(),
		newHookRetrospectiveBgCmd(),
		newHookSessionEndCmd(),
		newHookSubagentStopCmd(),
		newHookSubagentStartCmd(),
		newHookStopCmd(),
	} {
		sub.Hidden = true
		cmd.AddCommand(sub)
	}

	namespaceIndex(cmd)
	return cmd
}

func isRetrospectiveChildProcess() bool {
	return strings.TrimSpace(os.Getenv(retroChildEnv)) != ""
}

// hookInput is the JSON Claude Code sends on stdin to hooks.
type hookInput struct {
	CWD           string          `json:"cwd"`
	SessionID     string          `json:"session_id"`
	HookEventName string          `json:"hook_event_name"`
	Prompt        string          `json:"prompt"`
	ToolName      string          `json:"tool_name"`
	ToolInput     json.RawMessage `json:"tool_input"`
	ToolResponse  json.RawMessage `json:"tool_response"`
	Source        string          `json:"source"`
	TaskID        string          `json:"task_id"`
	Raw           map[string]any  `json:"-"`
}

// hookOutput is the JSON Claude Code expects on stdout from SessionStart hooks.
type hookOutput struct {
	HookSpecificOutput *hookSpecific `json:"hookSpecificOutput,omitempty"`
}

type hookSpecific struct {
	HookEventName     string `json:"hookEventName"`
	AdditionalContext string `json:"additionalContext,omitempty"`
}

// hookContext holds resolved common state shared by all hook commands.
type hookContext struct {
	Input     hookInput
	AgentName string
	CWD       string
}

// resolveHookContext reads stdin and resolves agent name and working directory.
func resolveHookContext(cmd *cobra.Command) hookContext {
	input := readHookStdin()
	agentName := resolveActorName(cmd, "")
	if agentName == "" {
		agentName = defaultAgentName
	}
	cwd := input.CWD
	if cwd == "" {
		cwd, _ = os.Getwd()
	}
	return hookContext{Input: input, AgentName: agentName, CWD: cwd}
}

func randomHex(bytesLen int) string {
	if bytesLen <= 0 {
		return "00"
	}
	b := make([]byte, bytesLen)
	if _, err := rand.Read(b); err != nil {
		// Fallback: PID + atomic counter avoids collisions across rapid invocations.
		seq := atomic.AddUint64(&hookSeqCounter, 1)
		return fmt.Sprintf("%x%x", os.Getpid(), seq)
	}
	return hex.EncodeToString(b)
}

func hookRequestID(prefix, agentName string) string {
	return fmt.Sprintf("hook_%s_%s_%d_%s", prefix, agentName, time.Now().UnixMilli(), randomHex(2))
}

func stableHookRequestID(prefix, agentName, sessionID string) string {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return hookRequestID(prefix, agentName)
	}
	return fmt.Sprintf("hook_%s_%s_%s", prefix, agentName, sanitizeRequestToken(sessionID, 64))
}

func sanitizeRequestToken(raw string, maxLen int) string {
	if maxLen <= 0 {
		maxLen = 64
	}
	var b strings.Builder
	b.Grow(len(raw))
	for _, r := range raw {
		switch {
		case r >= 'a' && r <= 'z':
			b.WriteRune(r)
		case r >= 'A' && r <= 'Z':
			b.WriteRune(r)
		case r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == '-' || r == '_':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	out := b.String()
	if len(out) > maxLen {
		out = out[:maxLen]
	}
	if out == "" {
		return "session"
	}
	return out
}

func truncateString(raw string, max int) (string, bool) {
	if max <= 0 || len(raw) <= max {
		return raw, false
	}
	return raw[:max], true
}

// runCheckpoint performs best-effort memory GC and event summarization.
// Used by both the checkpoint and session-end hook handlers.
func runCheckpoint(db *DB, hctx hookContext, requestIDPrefix string) {
	maint := app.EffectiveEventMaintenanceSettings()

	_, gcErr := actions.MemoryGCIdempotent(db, hctx.AgentName, requestIDPrefix+"_gc", 500)
	if gcErr != nil {
		slog.Default().Warn("checkpoint gc failed", "error", gcErr, "hook_event", hctx.Input.HookEventName)
	}

	// Auto-compress old events when active count exceeds threshold
	summarizeReqID := requestIDPrefix + "_summarize"
	projectID := hctx.CWD
	_, _, summarizeErr := actions.AutoSummarizeEventsIdempotent(
		db, hctx.AgentName, summarizeReqID, projectID,
		maint.SummarizeThreshold, maint.SummarizeKeepRecent,
	)
	if summarizeErr != nil {
		slog.Default().Warn("checkpoint auto-summarize failed", "error", summarizeErr, "hook_event", hctx.Input.HookEventName)
	}

	deleted, pruneErr := actions.AutoPruneArchivedEventsIdempotent(
		db, hctx.AgentName, requestIDPrefix+"_prune", projectID,
		maint.RetentionDays, maint.PruneBatch,
	)
	if pruneErr != nil {
		slog.Default().Warn("checkpoint archived-event prune failed", "error", pruneErr, "hook_event", hctx.Input.HookEventName)
		return
	}
	if deleted > 0 {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if err := store.CheckpointWAL(ctx, db, "TRUNCATE"); err != nil {
			slog.Default().Warn("checkpoint wal truncate failed", "error", err, "hook_event", hctx.Input.HookEventName)
		}
	}
}

func buildToolMetadata(input hookInput) string {
	inputPreview, inputTruncated := truncateString(string(input.ToolInput), 2048)
	outputPreview, outputTruncated := truncateString(string(input.ToolResponse), 4096)

	metaObj := map[string]any{
		"source":                  defaultAgentName,
		"session_id":              input.SessionID,
		"hook_event":              input.HookEventName,
		"tool_name":               input.ToolName,
		"tool_input_bytes":        len(input.ToolInput),
		"tool_output_bytes":       len(input.ToolResponse),
		"tool_input_preview":      inputPreview,
		"tool_output_preview":     outputPreview,
		"tool_input_truncated":    inputTruncated,
		"tool_output_truncated":   outputTruncated,
		"metadata_schema_version": "v1",
	}

	metadata, _ := json.Marshal(metaObj)
	if len(metadata) <= store.MaxEventMetadataLength {
		return string(metadata)
	}

	delete(metaObj, "tool_output_preview")
	delete(metaObj, "tool_output_truncated")
	metadata, _ = json.Marshal(metaObj)
	if len(metadata) <= store.MaxEventMetadataLength {
		return string(metadata)
	}

	delete(metaObj, "tool_input_preview")
	delete(metaObj, "tool_input_truncated")
	metadata, _ = json.Marshal(metaObj)
	if len(metadata) <= store.MaxEventMetadataLength {
		return string(metadata)
	}

	fallback := map[string]any{
		"source":                  defaultAgentName,
		"session_id":              input.SessionID,
		"hook_event":              input.HookEventName,
		"tool_name":               input.ToolName,
		"tool_input_bytes":        len(input.ToolInput),
		"tool_output_bytes":       len(input.ToolResponse),
		"metadata_schema_version": "v1",
	}
	minimal, _ := json.Marshal(fallback)
	return string(minimal)
}

func readHookStdin() hookInput {
	data, err := io.ReadAll(io.LimitReader(os.Stdin, maxHookStdinBytes))
	if err != nil {
		return hookInput{}
	}
	var input hookInput
	if err := json.Unmarshal(data, &input); err != nil {
		slog.Default().Warn("hook stdin unmarshal failed", "error", err, "bytes", len(data))
	}
	// Intentional double-unmarshal: struct tags handle known fields while
	// the Raw map preserves unknown fields for diagnostics/debugging.
	// Hook payloads are <1 KB so the cost is negligible.
	var raw map[string]any
	_ = json.Unmarshal(data, &raw)
	input.Raw = raw
	return input
}

// resolveAgentFocusTaskID loads the agent's current focus task ID.
// Returns empty string if no focus task is set or on any error.
func resolveAgentFocusTaskID(db *DB, agentName string) string {
	state, err := store.GetAgentState(db, agentName)
	if err != nil || state == nil {
		return ""
	}
	return state.FocusTaskID
}

// appendEventWithFocusTask resolves the agent's focus task (unless overridden)
// and appends an event with project and metadata. Consolidates the repeated
// resolve-then-append pattern used by prompt, tool-failure, and task-completed hooks.
//
//nolint:revive // argument-limit: all 8 params are required for the unified hook event path
func appendEventWithFocusTask(db *DB, agentName, requestID, kind, projectID, taskIDOverride, msg, metadata string) (int64, error) {
	taskID := taskIDOverride
	if taskID == "" {
		taskID = resolveAgentFocusTaskID(db, agentName)
	}
	return store.AppendEventWithProjectAndMetadataIdempotent(
		db, agentName, requestID, kind, projectID, taskID, msg, metadata,
	)
}

// newHookSessionStartCmd creates the session-start hook handler.
//
// Usage:
//
//	vybe hook install
func newHookSessionStartCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "session-start",
		Short: "SessionStart hook — injects vybe context into Claude Code",
		Long: `Reads hook input from stdin (Claude Code provides cwd), calls vybe resume
internally, and outputs additionalContext for the model.

Register via 'vybe hook install'.
This runs alongside any existing SessionStart hooks — no conflicts.`,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			hctx := resolveHookContext(cmd)
			requestID := hookRequestID("session", hctx.AgentName)

			var prompt string
			if err := withDB(func(db *DB) error {
				// Ensure project exists before setting focus scope
				if hctx.CWD != "" {
					if _, err := store.EnsureProjectByID(db, hctx.CWD, filepath.Base(hctx.CWD)); err != nil {
						slog.Default().Warn("project ensure failed", "error", err, "cwd", hctx.CWD)
					} else {
						if _, err := store.SetAgentFocusProjectWithEventIdempotent(db, hctx.AgentName, requestID+"_projfocus", hctx.CWD); err != nil {
							slog.Default().Warn("project focus failed", "error", err, "cwd", hctx.CWD)
						}
					}
				}

				r, err := actions.ResumeWithOptionsIdempotent(db, hctx.AgentName, requestID, actions.ResumeOptions{
					EventLimit: 100,
					ProjectDir: hctx.CWD,
				})
				if err != nil {
					return err
				}
				prompt = r.Prompt
				return nil
			}); err != nil {
				// Hooks must never block Claude Code — log diagnostic and exit clean.
				slog.Default().Error("session-start hook failed", "error", err, "cwd", hctx.CWD, "agent", hctx.AgentName)
				return nil
			}

			prevContext := readPreviousSessionContext(hctx.CWD, hctx.Input.SessionID)
			if prevContext != "" {
				prompt += "\n" + prevContext
			}

			out := hookOutput{
				HookSpecificOutput: &hookSpecific{
					HookEventName:     "SessionStart",
					AdditionalContext: prompt,
				},
			}

			enc := json.NewEncoder(os.Stdout)
			return enc.Encode(out)
		},
	}
}

// newHookPromptCmd creates the user-prompt-submit hook handler.
//
// Usage:
//
//	vybe hook install
func newHookPromptCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "prompt",
		Short: "UserPromptSubmit hook — logs user prompts to vybe",
		Long: `Reads hook input from stdin (Claude Code provides cwd and prompt),
logs the prompt as a user_prompt event in vybe.

Register via 'vybe hook install'.`,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			hctx := resolveHookContext(cmd)
			if hctx.Input.Prompt == "" {
				return nil
			}

			// Truncate long prompts
			msg, _ := truncateString(hctx.Input.Prompt, 500)

			requestID := hookRequestID("prompt", hctx.AgentName)

			// Hooks must never block Claude Code — errors are swallowed.
			_ = withDB(func(db *DB) error {
				metadata, _ := json.Marshal(map[string]string{
					"source":        defaultAgentName,
					"session_id":    hctx.Input.SessionID,
					"hook_event":    hctx.Input.HookEventName,
					"resume_source": hctx.Input.Source,
				})
				_, _ = appendEventWithFocusTask(
					db, hctx.AgentName, requestID, models.EventKindUserPrompt, hctx.CWD, "", msg, string(metadata),
				)
				return nil
			})

			// Inject task context into model. Richer output for trigger words.
			_ = withDB(func(db *DB) error {
				state, err := store.LoadOrCreateAgentState(db, hctx.AgentName)
				if err != nil {
					return err
				}

				focusProjectID := state.FocusProjectID
				if hctx.CWD != "" {
					focusProjectID = hctx.CWD
				}

				// Detect trigger words for rich summary
				lower := strings.ToLower(strings.TrimSpace(hctx.Input.Prompt))
				isTrigger := lower == "brief me" || lower == "remember" ||
					lower == "remember?" || lower == "brief" ||
					lower == "what's pending" || lower == "status"

				if isTrigger {
					return emitRichBrief(db, hctx.AgentName, state.FocusTaskID, focusProjectID)
				}

				// Non-trigger: lightweight reminder if focus task exists
				if state.FocusTaskID == "" {
					return nil
				}

				brief, err := store.BuildBrief(db, state.FocusTaskID, focusProjectID, hctx.AgentName)
				if err != nil {
					return err
				}
				if brief == nil || brief.Task == nil {
					return nil
				}

				actionable := 1
				if brief.Counts != nil {
					actionable = brief.Counts.Pending + brief.Counts.InProgress
				}

				var reminder strings.Builder
				fmt.Fprintf(&reminder, "TASK REMINDER: You have %d task(s) awaiting action.\n", actionable)
				fmt.Fprintf(&reminder, "Current: %s — %s\n", brief.Task.ID, brief.Task.Title)
				if brief.Task.Description != "" {
					fmt.Fprintf(&reminder, "Description: %s\n", brief.Task.Description)
				}
				reminder.WriteString("Ask the user if they'd like to address pending tasks before proceeding.\n")

				return emitHookJSON("UserPromptSubmit", reminder.String())
			})

			return nil
		},
	}
}

// emitRichBrief builds a comprehensive vybe summary for trigger words like "brief me" and "remember".
func emitRichBrief(db *DB, agentName, focusTaskID, projectID string) error {
	var b strings.Builder

	b.WriteString("== VYBE PROJECT SUMMARY ==\n")

	// List all non-completed tasks for this project
	tasks, err := store.ListTasks(db, "", projectID, 0)
	if err != nil {
		return err
	}

	var pending, inProgress, blocked []*models.Task
	for _, t := range tasks {
		switch t.Status {
		case "pending":
			pending = append(pending, t)
		case "in_progress":
			inProgress = append(inProgress, t)
		case "blocked":
			blocked = append(blocked, t)
		}
	}

	if len(inProgress) > 0 {
		b.WriteString("\nIn Progress:\n")
		for _, t := range inProgress {
			fmt.Fprintf(&b, "  [%s] %s", t.ID, t.Title)
			if t.ID == focusTaskID {
				b.WriteString(" <- current focus")
			}
			b.WriteString("\n")
			if t.Description != "" {
				fmt.Fprintf(&b, "    %s\n", t.Description)
			}
		}
	}

	if len(pending) > 0 {
		b.WriteString("\nPending:\n")
		for _, t := range pending {
			fmt.Fprintf(&b, "  [%s] %s\n", t.ID, t.Title)
			if t.Description != "" {
				fmt.Fprintf(&b, "    %s\n", t.Description)
			}
		}
	}

	if len(blocked) > 0 {
		b.WriteString("\nBlocked:\n")
		for _, t := range blocked {
			fmt.Fprintf(&b, "  [%s] %s", t.ID, t.Title)
			if t.BlockedReason != "" {
				fmt.Fprintf(&b, " (%s)", t.BlockedReason)
			}
			b.WriteString("\n")
		}
	}

	total := len(pending) + len(inProgress) + len(blocked)
	if total == 0 {
		b.WriteString("\nNo actionable tasks in this project.\n")
	} else {
		fmt.Fprintf(&b, "\nTotal: %d actionable (%d pending, %d in progress, %d blocked)\n",
			total, len(pending), len(inProgress), len(blocked))
	}

	// Add memory if available
	if focusTaskID != "" {
		brief, err := store.BuildBrief(db, focusTaskID, projectID, agentName)
		if err == nil && brief != nil && len(brief.RelevantMemory) > 0 {
			b.WriteString("\nSaved notes:\n")
			for _, m := range brief.RelevantMemory {
				fmt.Fprintf(&b, "  %s = %s\n", m.Key, m.Value)
			}
		}
	}

	b.WriteString("\nPresent this summary to the user and ask which task(s) they'd like to work on.\n")

	return emitHookJSON("UserPromptSubmit", b.String())
}

// emitHookJSON writes a hookOutput JSON to stdout.
func emitHookJSON(eventName, context string) error {
	out := hookOutput{
		HookSpecificOutput: &hookSpecific{
			HookEventName:     eventName,
			AdditionalContext: context,
		},
	}
	return json.NewEncoder(os.Stdout).Encode(out)
}

func newHookToolFailureCmd() *cobra.Command {
	return &cobra.Command{
		Use:           "tool-failure",
		Short:         "PostToolUseFailure hook — logs failed tool calls to vybe",
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			hctx := resolveHookContext(cmd)
			if hctx.Input.ToolName == "" {
				return nil
			}

			requestID := hookRequestID("tool_failure", hctx.AgentName)
			msg := fmt.Sprintf("%s failed", hctx.Input.ToolName)
			if hctx.Input.HookEventName != "" {
				msg = fmt.Sprintf("%s (%s)", msg, hctx.Input.HookEventName)
			}

			metadata := buildToolMetadata(hctx.Input)

			// Hooks must never block Claude Code — log diagnostic and exit clean.
			if err := withDB(func(db *DB) error {
				_, err := appendEventWithFocusTask(
					db, hctx.AgentName, requestID, models.EventKindToolFailure, hctx.CWD, "", msg, metadata,
				)
				return err
			}); err != nil {
				slog.Default().Error("tool-failure hook failed", "error", err, "tool_name", hctx.Input.ToolName)
			}

			return nil
		},
	}
}

// mutatingTools is the set of tools that modify state. Read-only tools are skipped
// by the tool-success hook to reduce event noise.
var mutatingTools = map[string]bool{ //nolint:gochecknoglobals // read-only lookup table initialized once at startup
	"Write":        true,
	"Edit":         true,
	"MultiEdit":    true,
	"Bash":         true,
	"NotebookEdit": true,
}

// toolInputSummary extracts a short human-readable summary from the tool input JSON.
// Returns the tool name prefix + meaningful identifier (file path or command).
func toolInputSummary(toolName string, raw json.RawMessage) string {
	var obj map[string]any
	if err := json.Unmarshal(raw, &obj); err != nil {
		return toolName
	}

	switch toolName {
	case "Write", "Edit", "MultiEdit":
		if fp, ok := obj["file_path"].(string); ok {
			return fmt.Sprintf("%s: %s", toolName, fp)
		}
	case "NotebookEdit":
		if np, ok := obj["notebook_path"].(string); ok {
			return fmt.Sprintf("%s: %s", toolName, np)
		}
	case "Bash":
		if cmd, ok := obj["command"].(string); ok {
			if len(cmd) > 120 {
				cmd = cmd[:120]
			}
			return fmt.Sprintf("Bash: %s", cmd)
		}
	}
	return toolName
}

func newHookToolSuccessCmd() *cobra.Command {
	return &cobra.Command{
		Use:           "tool-success",
		Short:         "PostToolUse hook — logs successful mutating tool calls to vybe",
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			hctx := resolveHookContext(cmd)
			if hctx.Input.ToolName == "" {
				return nil
			}

			// Skip read-only tools
			if !mutatingTools[hctx.Input.ToolName] {
				return nil
			}

			requestID := hookRequestID("tool_success", hctx.AgentName)
			msg, _ := truncateString(toolInputSummary(hctx.Input.ToolName, hctx.Input.ToolInput), 500)

			metadata := buildToolMetadata(hctx.Input)

			// Hooks must never block Claude Code — log diagnostic and exit clean.
			if err := withDB(func(db *DB) error {
				_, err := appendEventWithFocusTask(
					db, hctx.AgentName, requestID, models.EventKindToolSuccess, hctx.CWD, "", msg, metadata,
				)
				return err
			}); err != nil {
				slog.Default().Error("tool-success hook failed", "error", err, "tool_name", hctx.Input.ToolName)
			}

			return nil
		},
	}
}

func newHookCheckpointCmd() *cobra.Command {
	return &cobra.Command{
		Use:           "checkpoint",
		Short:         "PreCompact hook — checkpoint + retrospective",
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if isRetrospectiveChildProcess() {
				return nil
			}
			_ = os.Setenv(disableExternalLLMEnv, "1")

			hctx := resolveHookContext(cmd)
			requestIDPrefix := hookRequestID("checkpoint", hctx.AgentName)

			// --- Phase 1: synchronous checkpoint (fast, ~500ms) ---
			if err := withDB(func(db *DB) error {
				runCheckpoint(db, hctx, requestIDPrefix)
				return nil
			}); err != nil {
				slog.Default().Error("checkpoint hook failed", "error", err, "hook_event", hctx.Input.HookEventName)
			}

			// --- Phase 2: best-effort retrospective (synchronous, rule-based) ---
			retroReq := hookRequestID("retro", hctx.AgentName)
			if err := withDB(func(db *DB) error {
				result, retroErr := actions.SessionRetrospectiveRuleOnly(db, hctx.AgentName, retroReq, hctx.CWD)
				if retroErr != nil {
					slog.Default().Warn("checkpoint retrospective failed", "error", retroErr)
					return nil
				}
				if result.Skipped {
					slog.Default().Info("checkpoint retrospective skipped", "reason", result.SkipReason)
				} else {
					slog.Default().Info("checkpoint retrospective complete", "lessons", result.LessonsCount)
				}
				return nil
			}); err != nil {
				slog.Default().Error("checkpoint retrospective hook failed", "error", err)
			}

			return nil
		},
	}
}

func newHookTaskCompletedCmd() *cobra.Command {
	return &cobra.Command{
		Use:           "task-completed",
		Short:         "TaskCompleted hook — logs completion signals to vybe",
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			hctx := resolveHookContext(cmd)
			requestID := hookRequestID("task_completed", hctx.AgentName)

			if rawTaskID, ok := hctx.Input.Raw["task_id"].(string); ok && rawTaskID != "" {
				hctx.Input.TaskID = rawTaskID
			}

			rawPayload, _ := json.Marshal(hctx.Input.Raw)
			payloadPreview, payloadTruncated := truncateString(string(rawPayload), 6000)

			metadataObj := map[string]any{
				"source":                    defaultAgentName,
				"session_id":                hctx.Input.SessionID,
				"hook_event":                hctx.Input.HookEventName,
				"task_id":                   hctx.Input.TaskID,
				"payload_preview":           payloadPreview,
				"payload_preview_truncated": payloadTruncated,
				"metadata_schema_version":   "v1",
			}
			metadata, _ := json.Marshal(metadataObj)
			if len(metadata) > store.MaxEventMetadataLength {
				delete(metadataObj, "payload_preview")
				delete(metadataObj, "payload_preview_truncated")
				metadata, _ = json.Marshal(metadataObj)
			}

			// Hooks must never block Claude Code — log diagnostic and exit clean.
			if err := withDB(func(db *DB) error {
				// Best-effort: promote task to completed status
				taskID := hctx.Input.TaskID
				if taskID == "" {
					taskID = resolveAgentFocusTaskID(db, hctx.AgentName)
				}
				if taskID != "" {
					statusReqID := hookRequestID("task_done", hctx.AgentName)
					_, _, statusErr := actions.TaskSetStatusIdempotent(
						db, hctx.AgentName, statusReqID, taskID, "completed", "",
					)
					if statusErr != nil {
						slog.Default().Warn("task-completed status promotion failed",
							"error", statusErr, "task_id", taskID)
					}
				}

				// Prefer explicit task_id from hook payload, fall back to agent's current focus
				_, err := appendEventWithFocusTask(
					db, hctx.AgentName, requestID, "task_completed_signal",
					hctx.CWD, hctx.Input.TaskID, "TaskCompleted hook fired", string(metadata),
				)
				return err
			}); err != nil {
				slog.Default().Error("task-completed hook failed", "error", err)
			}

			return nil
		},
	}
}

func newHookSubagentStopCmd() *cobra.Command {
	return &cobra.Command{
		Use:           "subagent-stop",
		Short:         "SubagentStop hook — logs sub-agent completion to vybe",
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			hctx := resolveHookContext(cmd)
			requestID := hookRequestID("subagent_stop", hctx.AgentName)

			// Extract description from raw payload fields
			desc := hctx.Input.ToolName
			if d, ok := hctx.Input.Raw["description"].(string); ok && d != "" {
				desc = d
			}
			if desc == "" {
				desc = "subagent"
			}

			msg, _ := truncateString(fmt.Sprintf("SubagentStop: %s", desc), 500)

			metadata := buildToolMetadata(hctx.Input)

			if err := withDB(func(db *DB) error {
				_, err := appendEventWithFocusTask(
					db, hctx.AgentName, requestID, models.EventKindAgentCompleted, hctx.CWD, "", msg, metadata,
				)
				return err
			}); err != nil {
				slog.Default().Error("subagent-stop hook failed", "error", err)
			}

			return nil
		},
	}
}

func newHookSubagentStartCmd() *cobra.Command {
	return &cobra.Command{
		Use:           "subagent-start",
		Short:         "SubagentStart hook — logs sub-agent spawn to vybe",
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			hctx := resolveHookContext(cmd)
			requestID := hookRequestID("subagent_start", hctx.AgentName)

			desc := "subagent"
			if d, ok := hctx.Input.Raw["description"].(string); ok && d != "" {
				desc = d
			}

			msg, _ := truncateString(fmt.Sprintf("SubagentStart: %s", desc), 500)

			metadata, _ := json.Marshal(map[string]string{
				"source":      defaultAgentName,
				"session_id":  hctx.Input.SessionID,
				"hook_event":  hctx.Input.HookEventName,
				"description": desc,
			})
			metaStr := string(metadata)
			if len(metaStr) > store.MaxEventMetadataLength {
				metaStr = "{}"
			}

			if err := withDB(func(db *DB) error {
				_, err := appendEventWithFocusTask(
					db, hctx.AgentName, requestID, models.EventKindAgentSpawned, hctx.CWD, "", msg, metaStr,
				)
				return err
			}); err != nil {
				slog.Default().Error("subagent-start hook failed", "error", err)
			}

			return nil
		},
	}
}

func newHookStopCmd() *cobra.Command {
	return &cobra.Command{
		Use:           "stop",
		Short:         "Stop hook — logs turn completion heartbeat to vybe",
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			hctx := resolveHookContext(cmd)
			requestID := hookRequestID("stop", hctx.AgentName)

			metadata, _ := json.Marshal(map[string]string{
				"source":     defaultAgentName,
				"session_id": hctx.Input.SessionID,
				"hook_event": hctx.Input.HookEventName,
			})

			if err := withDB(func(db *DB) error {
				_, err := appendEventWithFocusTask(
					db, hctx.AgentName, requestID, models.EventKindHeartbeat, hctx.CWD, "", "turn_complete", string(metadata),
				)
				return err
			}); err != nil {
				slog.Default().Error("stop hook failed", "error", err)
			}

			return nil
		},
	}
}

func newHookRetrospectiveCmd() *cobra.Command {
	return &cobra.Command{
		Use:           "retrospective",
		Short:         "SessionEnd hook — best-effort session retrospective extraction",
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if isRetrospectiveChildProcess() {
				return nil
			}

			hctx := resolveHookContext(cmd)
			requestIDPrefix := hookRequestID("retro", hctx.AgentName)

			// Hooks must never block Claude Code — log diagnostic and exit clean.
			if err := withDB(func(db *DB) error {
				result, err := actions.SessionRetrospectiveRuleOnly(db, hctx.AgentName, requestIDPrefix, hctx.CWD)
				if err != nil {
					slog.Default().Warn("retrospective failed", "error", err)
					return nil
				}
				if result.Skipped {
					slog.Default().Info("retrospective skipped", "reason", result.SkipReason)
				} else {
					slog.Default().Info("retrospective complete", "lessons", result.LessonsCount)
				}
				return nil
			}); err != nil {
				slog.Default().Error("retrospective hook failed", "error", err)
			}

			return nil
		},
	}
}

// newHookRetrospectiveBgCmd runs the retrospective as a detached background worker.
// Args: <agent-name> <temp-file-path>
// The temp file is cleaned up after use. Spawned by session-end and the OpenCode bridge.
func newHookRetrospectiveBgCmd() *cobra.Command {
	return &cobra.Command{
		Use:           "retrospective-bg <agent> <payload-path>",
		Short:         "Background retrospective worker (spawned by session-end)",
		SilenceUsage:  true,
		SilenceErrors: true,
		Args:          cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			if isRetrospectiveChildProcess() {
				return nil
			}

			agentName := args[0]
			payloadPath := args[1]
			defer func() { _ = os.Remove(payloadPath) }()

			if agentName == "" {
				agentName = defaultAgentName
			}
			requestIDPrefix := hookRequestID("retro", agentName)

			// Extract CWD from payload file for project-scoped lesson storage.
			var cwdHint string
			if data, readErr := os.ReadFile(payloadPath); readErr == nil { //nolint:gosec // G304: payloadPath is generated by this process
				var payload hookInput
				if jsonErr := json.Unmarshal(data, &payload); jsonErr == nil {
					cwdHint = payload.CWD
				}
			}

			if err := withDB(func(db *DB) error {
				result, err := actions.SessionRetrospectiveRuleOnly(db, agentName, requestIDPrefix, cwdHint)
				if err != nil {
					slog.Default().Warn("retrospective-bg failed", "error", err)
					return nil
				}
				if result.Skipped {
					slog.Default().Info("retrospective-bg skipped", "reason", result.SkipReason)
				} else {
					slog.Default().Info("retrospective-bg complete", "lessons", result.LessonsCount)
				}
				return nil
			}); err != nil {
				slog.Default().Error("retrospective-bg hook failed", "error", err)
			}

			return nil
		},
	}
}

// newHookSessionEndCmd creates a SessionEnd hook that runs checkpoint only.
// Retrospective enqueue is handled by PreCompact (checkpoint hook) instead.
func newHookSessionEndCmd() *cobra.Command {
	return &cobra.Command{
		Use:           "session-end",
		Short:         "SessionEnd hook — best-effort checkpoint",
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if isRetrospectiveChildProcess() {
				return nil
			}
			_ = os.Setenv(disableExternalLLMEnv, "1")

			hctx := resolveHookContext(cmd)
			sessionID := hctx.Input.SessionID
			requestIDPrefix := stableHookRequestID("session_end", hctx.AgentName, sessionID)

			if err := withDB(func(db *DB) error {
				runCheckpoint(db, hctx, requestIDPrefix)
				return nil
			}); err != nil {
				slog.Default().Error("session-end checkpoint failed", "error", err)
			}

			return nil
		},
	}
}

// encodeProjectPath converts a filesystem path to the Claude Code project directory
// name format, where each "/" is replaced with "-".
// Example: "/Users/vampire/go/src/vybe" → "-Users-vampire-go-src-vybe"
func encodeProjectPath(cwd string) string {
	return strings.ReplaceAll(cwd, "/", "-")
}

// readPreviousSessionContext finds the most recent Claude Code session transcript
// for the given working directory (excluding the current session) and returns a
// formatted string of the last few user/assistant exchanges.
//
// All errors are silently swallowed — hooks must never block Claude Code.
func readPreviousSessionContext(cwd, currentSessionID string) string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}

	projectDir := filepath.Join(home, ".claude", "projects", encodeProjectPath(cwd))
	entries, err := os.ReadDir(projectDir)
	if err != nil {
		return ""
	}

	// Collect .jsonl files excluding the current session.
	type fileInfo struct {
		path    string
		modTime time.Time
	}
	var candidates []fileInfo
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.HasSuffix(name, ".jsonl") {
			continue
		}
		sessionID := strings.TrimSuffix(name, ".jsonl")
		if sessionID == currentSessionID {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		candidates = append(candidates, fileInfo{
			path:    filepath.Join(projectDir, name),
			modTime: info.ModTime(),
		})
	}
	if len(candidates) == 0 {
		return ""
	}

	// Pick most recent by ModTime.
	best := candidates[0]
	for _, c := range candidates[1:] {
		if c.modTime.After(best.modTime) {
			best = c
		}
	}

	data, err := os.ReadFile(best.path) //nolint:gosec // G304: path is constructed from known home dir + encoded cwd
	if err != nil {
		return ""
	}

	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	// Take last 50 lines.
	if len(lines) > 50 {
		lines = lines[len(lines)-50:]
	}

	type contentItem struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	type message struct {
		Role    string        `json:"role"`
		Content []contentItem `json:"content"`
	}
	type record struct {
		Type    string  `json:"type"`
		Message message `json:"message"`
	}

	const maxMsgLen = 200
	const maxTotalLen = 2000

	var sb strings.Builder
	sb.WriteString("Previous session context (last session before this one):\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var rec record
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			continue
		}
		if rec.Type != "user" && rec.Type != "assistant" {
			continue
		}
		role := rec.Message.Role
		if role == "" {
			role = rec.Type
		}
		var textParts []string
		for _, item := range rec.Message.Content {
			if item.Type != "text" || item.Text == "" {
				continue
			}
			t := item.Text
			if len(t) > maxMsgLen {
				t = t[:maxMsgLen]
			}
			textParts = append(textParts, t)
		}
		if len(textParts) == 0 {
			continue
		}
		line := fmt.Sprintf("  [%s] %s\n", role, strings.Join(textParts, " "))
		if sb.Len()+len(line) > maxTotalLen {
			break
		}
		sb.WriteString(line)
	}

	result := sb.String()
	// If nothing was appended beyond the header, return empty.
	if result == "Previous session context (last session before this one):\n" {
		return ""
	}
	return result
}
