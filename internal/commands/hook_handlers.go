package commands

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/dotcommander/vybe/internal/actions"
	"github.com/dotcommander/vybe/internal/app"
	"github.com/dotcommander/vybe/internal/commands/hookcmd"
	"github.com/dotcommander/vybe/internal/models"
	"github.com/dotcommander/vybe/internal/store"
	"github.com/spf13/cobra"
)

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
			ev, hctx := resolveCanonical(cmd, EventKindSessionStart)

			// On compact, the model already has session context — skip full resume.
			// Emit a lightweight focus-task reminder so the model doesn't lose
			// track of what it's working on after context compression.
			if ev.Source == "compact" {
				var reminder string
				runHookDB("session-start-compact", func(db *DB) error {
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
				}, nil)

				return renderResult(cmd, "SessionStart", ContextResult{Context: reminder})
			}

			requestID := hookRequestID("session", hctx.AgentName)

			var prompt string
			failed := false
			runHookDB("session-start-resume", func(db *DB) error {
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
			}, func(err error) {
				// Hooks must never block Claude Code — log diagnostic and exit clean.
				slog.Default().Error("session-start hook failed", "error", err, "cwd", hctx.CWD, "agent", hctx.AgentName)
				failed = true
			})
			if failed {
				return nil
			}

			prevContext := hookcmd.ReadPreviousSessionContext(hctx.CWD, ev.SessionID)
			if prevContext != "" {
				prompt += "\n" + prevContext
			}

			return renderResult(cmd, "SessionStart", ContextResult{Context: prompt})
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
			ev, hctx := resolveCanonical(cmd, EventKindPrompt)
			if ev.Prompt == "" {
				return nil
			}

			// Truncate long prompts
			msg, _ := truncateString(ev.Prompt, 500)

			requestID := hookRequestID("prompt", hctx.AgentName)

			// Hooks must never block Claude Code — errors are swallowed.
			runHookDB("prompt", func(db *DB) error {
				metadata, _ := json.Marshal(map[string]string{
					"source":        ev.EventSource,
					"session_id":    ev.SessionID,
					"hook_event":    ev.HostEventName,
					"resume_source": ev.Source,
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

				// Detect trigger words for rich summary (loaded from triggers.yaml)
				lower := strings.ToLower(strings.TrimSpace(ev.Prompt))
				_, isTrigger := app.LoadTriggerWords()[lower]

				if isTrigger {
					return emitRichBrief(db, hctx.AgentName, state.FocusTaskID, focusProjectID,
						func(r ContextResult) error { return renderResult(cmd, "UserPromptSubmit", r) })
				}

				return nil
			}, nil)

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
			ev, hctx := resolveCanonical(cmd, EventKindToolFailure)
			if ev.ToolName == "" {
				return nil
			}

			requestID := hookRequestID("tool_failure", hctx.AgentName)
			msg := fmt.Sprintf("%s failed", ev.ToolName)
			if ev.HostEventName != "" {
				msg = fmt.Sprintf("%s (%s)", msg, ev.HostEventName)
			}

			metadata := buildToolMetadata(ev)

			// Hooks must never block Claude Code — log diagnostic and exit clean.
			runHookDB("tool-failure", func(db *DB) error {
				_, err := appendEventWithFocusTask(
					db, hctx.AgentName, requestID, models.EventKindToolFailure, hctx.CWD, "", msg, metadata,
				)
				return err
			}, func(err error) {
				slog.Default().Error("tool-failure hook failed", "error", err, "tool_name", ev.ToolName)
			})

			return nil
		},
	}
}

// Single source of truth for maintenance hooks (PreCompact, SessionEnd).
// buildReqID is the only asymmetry: checkpoint uses a random per-invocation ID;
// session-end uses a stable per-session ID for idempotency across retries.
func newHookMaintenanceCmd(use, short string, buildReqID func(agentName, sessionID string) string) *cobra.Command {
	return &cobra.Command{
		Use:           use,
		Short:         short,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			_ = os.Setenv(disableExternalLLMEnv, "1")
			slog.Default().Debug("LLM subprocess execution disabled for hook", "env", disableExternalLLMEnv)

			ev, hctx := resolveCanonical(cmd, EventKindCheckpoint)
			requestIDPrefix := buildReqID(hctx.AgentName, ev.SessionID)

			runHookDB("maintenance", func(db *DB) error {
				runCheckpoint(db, ev, hctx, requestIDPrefix)
				return nil
			}, func(err error) {
				slog.Default().Error("maintenance hook failed", "error", err, "use", use)
			})

			return nil
		},
	}
}
