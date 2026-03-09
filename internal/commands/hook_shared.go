package commands

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
	"sync/atomic"
	"time"

	"github.com/dotcommander/vybe/internal/store"
	"github.com/spf13/cobra"
)

const (
	// maxHookStdinBytes caps stdin reads. Hook payloads are small JSON objects;
	// 1 MB is generous headroom that prevents unbounded allocation.
	maxHookStdinBytes = 1 << 20

	// defaultAgentName is the default agent identity used by hooks when no --agent flag is provided.
	defaultAgentName = "claude"

	// disableExternalLLMEnv blocks claude/opencode subprocess execution in guarded flows.
	disableExternalLLMEnv = "VYBE_DISABLE_EXTERNAL_LLM"
)

// hookSeqCounter provides monotonic fallback entropy when crypto/rand fails.
var hookSeqCounter uint64 //nolint:gochecknoglobals // atomic counter shared across hook invocations; required for fallback entropy

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
		slog.Default().Warn("hook using default agent identity",
			"agent", agentName,
			"hint", "set VYBE_AGENT or --agent to avoid cross-session contamination")
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
	if max <= 0 {
		return raw, false
	}
	runes := []rune(raw)
	if len(runes) <= max {
		return raw, false
	}
	return string(runes[:max]), true
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
