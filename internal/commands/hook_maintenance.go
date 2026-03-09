package commands

import (
	"context"
	"encoding/json"
	"log/slog"
	"time"

	"github.com/dotcommander/vybe/internal/actions"
	"github.com/dotcommander/vybe/internal/app"
	"github.com/dotcommander/vybe/internal/store"
)

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
