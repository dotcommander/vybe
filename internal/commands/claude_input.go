package commands

import (
	"encoding/json"
	"os"
)

// toCanonical converts Claude's hookInput into a host-agnostic CanonicalEvent.
// kind is supplied by the caller (each hook handler knows its own event kind)
// rather than inferred from HookEventName, so handlers stay explicit and Claude's
// PascalCase event-name strings remain confined to manifest.go.
func (in hookInput) toCanonical(kind EventKind) CanonicalEvent {
	return CanonicalEvent{
		Kind:         kind,
		CWD:          in.CWD,
		SessionID:    in.SessionID,
		Prompt:       in.Prompt,
		ToolName:     in.ToolName,
		ToolInput:    in.ToolInput,
		ToolResponse: in.ToolResponse,
		Source:       in.Source,
		TaskID:       in.TaskID,
		Raw:          in.Raw,
	}
}

// renderClaudeResult writes a ContextResult as Claude's hookSpecificOutput JSON.
// eventName is Claude's PascalCase hook event (e.g. "SessionStart"). The emitted
// JSON is byte-identical to the pre-Phase-0 emitHookJSON path.
func renderClaudeResult(eventName string, res ContextResult) error {
	out := hookOutput{
		HookSpecificOutput: &hookSpecific{
			HookEventName:     eventName,
			AdditionalContext: res.Context,
		},
	}
	return json.NewEncoder(os.Stdout).Encode(out)
}
