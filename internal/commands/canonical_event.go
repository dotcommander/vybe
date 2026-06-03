package commands

import "encoding/json"

// EventKind is vybe's host-agnostic event taxonomy. NOT Claude's PascalCase
// hook names — those map in via the host input parser (claude_input.go).
type EventKind string

const (
	EventKindSessionStart  EventKind = "session_start"
	EventKindPrompt        EventKind = "prompt"
	EventKindToolFailure   EventKind = "tool_failure"
	EventKindCheckpoint    EventKind = "checkpoint"
	EventKindSessionEnd    EventKind = "session_end"
	EventKindTaskCompleted EventKind = "task_completed"
)

// CanonicalEvent is the host-agnostic event handlers consume. Every host input
// parser produces one of these; the core never sees host JSON.
type CanonicalEvent struct {
	Kind         EventKind
	CWD          string
	SessionID    string
	Prompt       string
	ToolName     string
	ToolInput    json.RawMessage
	ToolResponse json.RawMessage
	Source       string // host-provided sub-source, e.g. "compact"
	TaskID       string
	Raw          map[string]any // passthrough for diagnostics / unknown fields
}

// ContextResult is the host-agnostic result handlers produce. The host output
// renderer turns it into host stdout (Claude -> hookSpecificOutput JSON).
type ContextResult struct {
	Context string // additionalContext to inject; empty = emit nothing
}
