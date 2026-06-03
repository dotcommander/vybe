package commands

import (
	"encoding/json"
	"io"
	"log/slog"
	"os"
)

// genericInput is vybe's host-native hook stdin schema. See docs/bridge-protocol.md.
// Field names mirror Claude's hookInput tags so a bridge emitting Claude-shaped JSON
// works unchanged, plus vybe-native extensions (kind, event_name, host_source, agent).
type genericInput struct {
	Kind         string          `json:"kind"` // vybe EventKind; overrides the subcommand's kind if set
	CWD          string          `json:"cwd"`
	SessionID    string          `json:"session_id"`
	Prompt       string          `json:"prompt"`
	ToolName     string          `json:"tool_name"`
	ToolInput    json.RawMessage `json:"tool_input"`
	ToolResponse json.RawMessage `json:"tool_response"`
	Source       string          `json:"source"`      // host sub-source, e.g. "compact" -> metadata resume_source
	EventName    string          `json:"event_name"`  // native event label -> metadata hook_event
	HostSource   string          `json:"host_source"` // metadata "source" attribution (e.g. "cursor"); default "generic"
	TaskID       string          `json:"task_id"`
	Agent        string          `json:"agent"` // identity fallback when no --agent/VYBE_AGENT/config
}

// readGenericCanonical reads vybe-native JSON from stdin into a CanonicalEvent.
// kind is the handler's own EventKind; an explicit "kind" field in the payload
// overrides it (forward-compat with a future single-endpoint daemon). The second
// return is the optional agent-identity fallback from the payload.
func readGenericCanonical(kind EventKind) (CanonicalEvent, string) {
	data, err := io.ReadAll(io.LimitReader(os.Stdin, maxHookStdinBytes))
	if err != nil {
		return CanonicalEvent{Kind: kind, EventSource: genericEventSource}, ""
	}
	var in genericInput
	if len(data) > 0 {
		if err := json.Unmarshal(data, &in); err != nil {
			slog.Default().Warn("generic hook stdin unmarshal failed", "error", err, "bytes", len(data))
		}
	}
	var raw map[string]any
	_ = json.Unmarshal(data, &raw)

	resolvedKind := kind
	if in.Kind != "" {
		resolvedKind = EventKind(in.Kind)
	}
	eventSource := genericEventSource
	if in.HostSource != "" {
		eventSource = in.HostSource
	}
	ev := CanonicalEvent{
		Kind:          resolvedKind,
		CWD:           in.CWD,
		SessionID:     in.SessionID,
		Prompt:        in.Prompt,
		ToolName:      in.ToolName,
		ToolInput:     in.ToolInput,
		ToolResponse:  in.ToolResponse,
		Source:        in.Source,
		HostEventName: in.EventName,
		EventSource:   eventSource,
		TaskID:        in.TaskID,
		Raw:           raw,
	}
	return ev, in.Agent
}
