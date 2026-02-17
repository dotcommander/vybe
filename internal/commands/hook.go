package commands

import (
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sync/atomic"
	"time"

	"github.com/dotcommander/vybe/internal/actions"
	"github.com/dotcommander/vybe/internal/models"
	"github.com/dotcommander/vybe/internal/store"
	"github.com/spf13/cobra"
)

// maxHookStdinBytes caps stdin reads. Hook payloads are small JSON objects;
// 1 MB is generous headroom that prevents unbounded allocation.
const maxHookStdinBytes = 1 << 20

// hookSeqCounter provides monotonic fallback entropy when crypto/rand fails.
var hookSeqCounter uint64

// NewHookCmd creates the hook parent command.
func NewHookCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "hook",
		Short: "Hook handlers and installers for Claude/OpenCode",
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
		newHookSubagentStopCmd(),
		newHookSubagentStartCmd(),
		newHookStopCmd(),
	} {
		sub.Hidden = true
		cmd.AddCommand(sub)
	}

	return cmd
}

// hookInput is the JSON Claude Code sends on stdin to hooks.
type hookInput struct {
	CWD           string          `json:"cwd"`
	SessionID     string          `json:"session_id"`
	SessionIDOld  string          `json:"sessionId"` // backward compat: Claude Code migrated from camelCase to snake_case
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
		agentName = "claude"
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
	return fmt.Sprintf("%x", b)
}

func hookRequestID(prefix, agentName string) string {
	return fmt.Sprintf("hook_%s_%s_%d_%s", prefix, agentName, time.Now().UnixMilli(), randomHex(2))
}

func truncateString(raw string, max int) (string, bool) {
	if max <= 0 || len(raw) <= max {
		return raw, false
	}
	return raw[:max], true
}

func buildToolMetadata(input hookInput) string {
	inputPreview, inputTruncated := truncateString(string(input.ToolInput), 2048)
	outputPreview, outputTruncated := truncateString(string(input.ToolResponse), 4096)

	metaObj := map[string]any{
		"source":                  "claude",
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
		"source":                  "claude",
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
		slog.Warn("hook stdin unmarshal failed", "error", err, "bytes", len(data))
	}
	// Intentional double-unmarshal: struct tags handle known fields while
	// the Raw map preserves unknown fields for forward compatibility.
	// Hook payloads are <1 KB so the cost is negligible.
	var raw map[string]any
	_ = json.Unmarshal(data, &raw)
	input.Raw = raw
	if input.SessionID == "" {
		input.SessionID = input.SessionIDOld
	}
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
						slog.Warn("project ensure failed", "error", err, "cwd", hctx.CWD)
					} else {
						_ = store.SetAgentFocusProject(db, hctx.AgentName, hctx.CWD)
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
				slog.Error("session-start hook failed", "error", err, "cwd", hctx.CWD, "agent", hctx.AgentName)
				return nil
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
			msg := hctx.Input.Prompt
			if len(msg) > 500 {
				msg = msg[:500]
			}

			requestID := hookRequestID("prompt", hctx.AgentName)

			// Hooks must never block Claude Code — errors are swallowed.
			_ = withDB(func(db *DB) error {
				metadata, _ := json.Marshal(map[string]string{
					"source":        "claude",
					"session_id":    hctx.Input.SessionID,
					"hook_event":    hctx.Input.HookEventName,
					"resume_source": hctx.Input.Source,
				})
				_, _ = appendEventWithFocusTask(
					db, hctx.AgentName, requestID, models.EventKindUserPrompt, hctx.CWD, "", msg, string(metadata),
				)
				return nil
			})

			return nil
		},
	}
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
				slog.Error("tool-failure hook failed", "error", err, "tool_name", hctx.Input.ToolName)
			}

			return nil
		},
	}
}

// mutatingTools is the set of tools that modify state. Read-only tools are skipped
// by the tool-success hook to reduce event noise.
var mutatingTools = map[string]bool{
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
			msg := toolInputSummary(hctx.Input.ToolName, hctx.Input.ToolInput)
			if len(msg) > 500 {
				msg = msg[:500]
			}

			metadata := buildToolMetadata(hctx.Input)

			// Hooks must never block Claude Code — log diagnostic and exit clean.
			if err := withDB(func(db *DB) error {
				_, err := appendEventWithFocusTask(
					db, hctx.AgentName, requestID, models.EventKindToolSuccess, hctx.CWD, "", msg, metadata,
				)
				return err
			}); err != nil {
				slog.Error("tool-success hook failed", "error", err, "tool_name", hctx.Input.ToolName)
			}

			return nil
		},
	}
}

func newHookCheckpointCmd() *cobra.Command {
	return &cobra.Command{
		Use:           "checkpoint",
		Short:         "SessionEnd/PreCompact hook — best-effort memory checkpoint",
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			hctx := resolveHookContext(cmd)
			requestIDPrefix := hookRequestID("checkpoint", hctx.AgentName)

			// Hooks must never block Claude Code — log diagnostic and exit clean.
			if err := withDB(func(db *DB) error {
				scope := "global"
				scopeID := ""
				if hctx.Input.CWD != "" {
					scope = "project"
					scopeID = hctx.Input.CWD
				}

				_, compactErr := actions.MemoryCompactIdempotent(db, hctx.AgentName, requestIDPrefix+"_compact", scope, scopeID, 14*24*time.Hour, 10)
				if compactErr != nil {
					slog.Warn("checkpoint compact failed", "error", compactErr, "hook_event", hctx.Input.HookEventName, "scope", scope)
				}

				_, gcErr := actions.MemoryGCIdempotent(db, hctx.AgentName, requestIDPrefix+"_gc", 500)
				if gcErr != nil {
					slog.Warn("checkpoint gc failed", "error", gcErr, "hook_event", hctx.Input.HookEventName)
				}

				// Auto-compress old events when active count exceeds threshold
				summarizeReqID := requestIDPrefix + "_summarize"
				projectID := hctx.CWD
				_, _, summarizeErr := actions.AutoSummarizeEventsIdempotent(db, hctx.AgentName, summarizeReqID, projectID, 200, 50)
				if summarizeErr != nil {
					slog.Warn("checkpoint auto-summarize failed", "error", summarizeErr, "hook_event", hctx.Input.HookEventName)
				}

				return nil
			}); err != nil {
				slog.Error("checkpoint hook failed", "error", err, "hook_event", hctx.Input.HookEventName)
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
				"source":                    "claude",
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
						slog.Warn("task-completed status promotion failed",
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
				slog.Error("task-completed hook failed", "error", err)
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

			msg := fmt.Sprintf("SubagentStop: %s", desc)
			if len(msg) > 500 {
				msg = msg[:500]
			}

			metadata := buildToolMetadata(hctx.Input)

			if err := withDB(func(db *DB) error {
				_, err := appendEventWithFocusTask(
					db, hctx.AgentName, requestID, models.EventKindAgentCompleted, hctx.CWD, "", msg, metadata,
				)
				return err
			}); err != nil {
				slog.Error("subagent-stop hook failed", "error", err)
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

			msg := fmt.Sprintf("SubagentStart: %s", desc)
			if len(msg) > 500 {
				msg = msg[:500]
			}

			metadata, _ := json.Marshal(map[string]string{
				"source":     "claude",
				"session_id": hctx.Input.SessionID,
				"hook_event": hctx.Input.HookEventName,
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
				slog.Error("subagent-start hook failed", "error", err)
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
				"source":     "claude",
				"session_id": hctx.Input.SessionID,
				"hook_event": hctx.Input.HookEventName,
			})

			if err := withDB(func(db *DB) error {
				_, err := appendEventWithFocusTask(
					db, hctx.AgentName, requestID, models.EventKindHeartbeat, hctx.CWD, "", "turn_complete", string(metadata),
				)
				return err
			}); err != nil {
				slog.Error("stop hook failed", "error", err)
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
			hctx := resolveHookContext(cmd)
			requestIDPrefix := hookRequestID("retro", hctx.AgentName)

			// Hooks must never block Claude Code — log diagnostic and exit clean.
			if err := withDB(func(db *DB) error {
				result, err := actions.SessionRetrospective(db, hctx.AgentName, requestIDPrefix)
				if err != nil {
					slog.Warn("retrospective failed", "error", err)
					return nil
				}
				if result.Skipped {
					slog.Info("retrospective skipped", "reason", result.SkipReason)
				} else {
					slog.Info("retrospective complete", "lessons", result.LessonsCount)
				}
				return nil
			}); err != nil {
				slog.Error("retrospective hook failed", "error", err)
			}

			return nil
		},
	}
}
