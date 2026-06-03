package commands

import (
	"encoding/json"
	"log/slog"

	"github.com/dotcommander/vybe/internal/actions"
	"github.com/dotcommander/vybe/internal/store"
)

// runCheckpoint performs best-effort memory GC.
// Used by both the checkpoint and session-end hook handlers.
func runCheckpoint(db *DB, ev CanonicalEvent, hctx hookContext, requestIDPrefix string) {
	_, gcErr := actions.MemoryGCIdempotent(db, hctx.AgentName, requestIDPrefix+"_gc", 500)
	if gcErr != nil {
		slog.Default().Warn("checkpoint gc failed", "error", gcErr, "hook_event", ev.HostEventName)
	}
}

func buildToolMetadata(ev CanonicalEvent) string {
	inputPreview, inputTruncated := truncateString(string(ev.ToolInput), 2048)
	outputPreview, outputTruncated := truncateString(string(ev.ToolResponse), 4096)

	metaObj := map[string]any{
		"source":                  ev.EventSource,
		"session_id":              ev.SessionID,
		"hook_event":              ev.HostEventName,
		"tool_name":               ev.ToolName,
		"tool_input_bytes":        len(ev.ToolInput),
		"tool_output_bytes":       len(ev.ToolResponse),
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
		"source":                  ev.EventSource,
		"session_id":              ev.SessionID,
		"hook_event":              ev.HostEventName,
		"tool_name":               ev.ToolName,
		"tool_input_bytes":        len(ev.ToolInput),
		"tool_output_bytes":       len(ev.ToolResponse),
		"metadata_schema_version": "v1",
	}
	minimal, _ := json.Marshal(fallback)
	return string(minimal)
}
