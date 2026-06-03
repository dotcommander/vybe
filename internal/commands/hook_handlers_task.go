package commands

import (
	"encoding/json"
	"log/slog"

	"github.com/dotcommander/vybe/internal/actions"
	"github.com/dotcommander/vybe/internal/store"
	"github.com/spf13/cobra"
)

func newHookTaskCompletedCmd() *cobra.Command {
	return &cobra.Command{
		Use:           "task-completed",
		Short:         "TaskCompleted hook — logs completion signals to vybe",
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			hctx := resolveHookContext(cmd)
			ev := hctx.Input.toCanonical(EventKindTaskCompleted)
			requestID := hookRequestID("task_completed", hctx.AgentName)

			if rawTaskID, ok := ev.Raw["task_id"].(string); ok && rawTaskID != "" {
				ev.TaskID = rawTaskID
			}

			rawPayload, _ := json.Marshal(hctx.Input.Raw)
			payloadPreview, payloadTruncated := truncateString(string(rawPayload), 6000)

			// metadata stays host-shaped until Phase 1 metadata renderer;
			// task_id uses the canonical-resolved value to stay byte-identical.
			metadataObj := map[string]any{
				"source":                    defaultEventSource,
				"session_id":                hctx.Input.SessionID,
				"hook_event":                hctx.Input.HookEventName,
				"task_id":                   ev.TaskID,
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
			runHookDB("task-completed", func(db *DB) error {
				// Best-effort: promote task to completed status
				taskID := ev.TaskID
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
					hctx.CWD, ev.TaskID, "TaskCompleted hook fired", string(metadata),
				)
				return err
			}, func(err error) {
				slog.Default().Error("task-completed hook failed", "error", err)
			})

			return nil
		},
	}
}
