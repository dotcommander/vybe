package commands

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/dotcommander/vybe/internal/actions"
	"github.com/dotcommander/vybe/internal/models"
	"github.com/dotcommander/vybe/internal/store"
	"github.com/spf13/cobra"
)

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
		newHookCheckpointCmd(),
		newHookTaskCompletedCmd(),
		newHookSessionEndCmd(),
	} {
		sub.Hidden = true
		cmd.AddCommand(sub)
	}

	namespaceIndex(cmd)
	return cmd
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

			// On compact, the model already has session context — skip full resume.
			// Emit a lightweight focus-task reminder so the model doesn't lose
			// track of what it's working on after context compression.
			if hctx.Input.Source == "compact" {
				var reminder string
				withDBSilent(func(db *DB) error {
					// Ensure project focus is maintained
					if hctx.CWD != "" {
						_, _ = store.EnsureProjectByID(db, hctx.CWD, filepath.Base(hctx.CWD))
					}

					state, err := store.GetAgentState(db, hctx.AgentName)
					if err != nil || state == nil || state.FocusTaskID == "" {
						return nil
					}
					task, err := store.GetTask(db, state.FocusTaskID)
					if err != nil || task == nil {
						return nil
					}
					var b strings.Builder
					fmt.Fprintf(&b, "Focus task: [%s] %s (status: %s)\n", task.ID, task.Title, task.Status)
					if task.Description != "" {
						desc, _ := truncateString(task.Description, 500)
						fmt.Fprintf(&b, "Description: %s\n", desc)
					}
					reminder = b.String()
					return nil
				})

				out := hookOutput{
					HookSpecificOutput: &hookSpecific{
						HookEventName:     "SessionStart",
						AdditionalContext: reminder,
					},
				}
				return json.NewEncoder(os.Stdout).Encode(out)
			}

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
			withDBSilent(func(db *DB) error {
				metadata, _ := json.Marshal(map[string]string{
					"source":        defaultAgentName,
					"session_id":    hctx.Input.SessionID,
					"hook_event":    hctx.Input.HookEventName,
					"resume_source": hctx.Input.Source,
				})
				_, _ = appendEventWithFocusTask(
					db, hctx.AgentName, requestID, models.EventKindUserPrompt, hctx.CWD, "", msg, string(metadata),
				)

				// Inject task context into model. Richer output for trigger words.
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

func newHookCheckpointCmd() *cobra.Command {
	return &cobra.Command{
		Use:           "checkpoint",
		Short:         "PreCompact hook — checkpoint maintenance",
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			_ = os.Setenv(disableExternalLLMEnv, "1")
			slog.Default().Debug("LLM subprocess execution disabled for hook", "env", disableExternalLLMEnv)

			hctx := resolveHookContext(cmd)
			requestIDPrefix := hookRequestID("checkpoint", hctx.AgentName)

			if err := withDB(func(db *DB) error {
				runCheckpoint(db, hctx, requestIDPrefix)
				return nil
			}); err != nil {
				slog.Default().Error("checkpoint hook failed", "error", err, "hook_event", hctx.Input.HookEventName)
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

// newHookSessionEndCmd creates a SessionEnd hook that runs checkpoint only.
func newHookSessionEndCmd() *cobra.Command {
	return &cobra.Command{
		Use:           "session-end",
		Short:         "SessionEnd hook — best-effort checkpoint",
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			_ = os.Setenv(disableExternalLLMEnv, "1")
			slog.Default().Debug("LLM subprocess execution disabled for hook", "env", disableExternalLLMEnv)

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
