package commands

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/dotcommander/vybe/internal/actions"
	"github.com/dotcommander/vybe/internal/app"
	"github.com/dotcommander/vybe/internal/models"
	"github.com/dotcommander/vybe/internal/output"
	"github.com/dotcommander/vybe/internal/store"

	"github.com/spf13/cobra"
)

// NewLoopCmd creates the autonomous driver command.
func NewLoopCmd() *cobra.Command {
	var (
		project     string
		maxTasks    int
		maxFails    int
		taskTimeout string
		cooldown    string
		dryRun      bool
		command     string
		postHook    string
	)

	cmd := &cobra.Command{
		Use:   "loop",
		Short: "Autonomous task driver — loops resume → spawn → complete",
		Long: `Loop is the autonomous driver loop. It repeatedly calls resume to get the next
focus task, spawns an external command (default: claude -p) with the task prompt,
waits for completion, and moves to the next task.

Safety rails:
  --max-tasks     Stop after N tasks completed (default: 10)
  --max-fails     Circuit breaker: stop after N consecutive failures (default: 3)
  --task-timeout  Kill spawned command after duration (default: 10m)
  --cooldown      Wait between tasks (default: 5s)
  --dry-run       Show what would run without spawning`,
		RunE: func(cmd *cobra.Command, args []string) error {
			agentName, err := requireActorName(cmd, "")
			if err != nil {
				return cmdErr(err)
			}

			timeout, err := time.ParseDuration(taskTimeout)
			if err != nil {
				return cmdErr(fmt.Errorf("invalid --task-timeout: %w", err))
			}
			cool, err := time.ParseDuration(cooldown)
			if err != nil {
				return cmdErr(fmt.Errorf("invalid --cooldown: %w", err))
			}

			// Resolve post-hook: CLI flag > config file
			hook := postHook
			if hook == "" {
				if s, err := app.LoadSettings(); err == nil && s.PostRunHook != "" {
					hook = s.PostRunHook
				}
			}

			opts := runOptions{
				agentName:   agentName,
				project:     project,
				maxTasks:    maxTasks,
				maxFails:    maxFails,
				taskTimeout: timeout,
				cooldown:    cool,
				dryRun:      dryRun,
				command:     command,
				postHook:    hook,
			}

			return runLoop(opts)
		},
	}

	cmd.Flags().StringVar(&project, "project", "", "Project directory to scope tasks and resume")
	cmd.Flags().IntVar(&maxTasks, "max-tasks", 10, "Stop after N tasks completed")
	cmd.Flags().IntVar(&maxFails, "max-fails", 3, "Circuit breaker: stop after N consecutive failures")
	cmd.Flags().StringVar(&taskTimeout, "task-timeout", "10m", "Kill spawned command after this duration")
	cmd.Flags().StringVar(&cooldown, "cooldown", "5s", "Wait between tasks")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Show what would run without spawning")
	cmd.Flags().StringVar(&command, "command", "claude", "Command to spawn (receives prompt via -p flag)")
	cmd.Flags().StringVar(&postHook, "post-hook", "", "Command to pipe run results JSON to on completion (fallback: config post_run_hook)")

	cmd.AddCommand(newLoopStatsCmd())

	return cmd
}

type runOptions struct {
	agentName   string
	project     string
	maxTasks    int
	maxFails    int
	taskTimeout time.Duration
	cooldown    time.Duration
	dryRun      bool
	command     string
	postHook    string
}

type taskResult struct {
	TaskID    string `json:"task_id"`
	TaskTitle string `json:"task_title"`
	Status    string `json:"status"` // completed, blocked, failed, timeout
	Duration  string `json:"duration"`
}

//nolint:gocognit,gocyclo,funlen,revive // run loop orchestrates per-task execution with claim, run, status-update, and retry phases
func runLoop(opts runOptions) error {
	loopStart := time.Now()

	var (
		completed      int
		failed         int
		totalRun       int
		consecuteFails int
		results        []taskResult
	)

	for completed < opts.maxTasks {
		// Resume to get focus task
		requestID := fmt.Sprintf("run_%d_%d", time.Now().UnixMilli(), totalRun)

		var response *actions.ResumeResponse
		if err := withDB(func(db *DB) error {
			r, err := actions.ResumeWithOptionsIdempotent(db, opts.agentName, requestID, actions.ResumeOptions{
				EventLimit: 100,
				ProjectDir: opts.project,
			})
			if err != nil {
				return err
			}
			response = r
			return nil
		}); err != nil {
			return cmdErr(err)
		}

		// No focus task = no more work
		if response.FocusTaskID == "" {
			slog.Default().Info("no pending tasks, exiting", "completed", completed, "failed", failed)
			break
		}

		taskTitle := ""
		if response.Brief != nil && response.Brief.Task != nil {
			taskTitle = response.Brief.Task.Title
		}

		slog.Default().Info("task selected",
			"task_id", response.FocusTaskID,
			"title", taskTitle,
			"iteration", totalRun+1,
		)

		if opts.dryRun {
			results = append(results, taskResult{
				TaskID:    response.FocusTaskID,
				TaskTitle: taskTitle,
				Status:    "dry_run",
			})
			totalRun++
			completed++
			continue
		}

		// Build the prompt for the agent
		prompt := buildAgentPrompt(response)

		// Spawn the command
		start := time.Now()
		exitCode := spawnAgent(opts.command, prompt, opts.project, opts.taskTimeout)
		duration := time.Since(start)

		// Check task status after agent finishes
		var finalStatus models.TaskStatus
		if err := withDB(func(db *DB) error {
			task, err := store.GetTask(db, response.FocusTaskID)
			if err != nil {
				return err
			}
			finalStatus = task.Status
			return nil
		}); err != nil {
			finalStatus = "unknown"
		}

		// Determine result
		result := taskResult{
			TaskID:    response.FocusTaskID,
			TaskTitle: taskTitle,
			Duration:  duration.Round(time.Second).String(),
		}

		switch {
		case exitCode != 0 && duration >= opts.taskTimeout:
			result.Status = "timeout"
			markTaskBlocked(opts.agentName, response.FocusTaskID, "timed out")
			consecuteFails++
			failed++
		case finalStatus == "completed":
			result.Status = "completed"
			consecuteFails = 0
			completed++
		case finalStatus == "in_progress" || finalStatus == "pending":
			// Agent didn't mark it done — treat as blocked
			result.Status = "blocked"
			markTaskBlocked(opts.agentName, response.FocusTaskID, "agent exited without completing")
			consecuteFails++
			failed++
		default:
			result.Status = string(finalStatus)
			if finalStatus == "blocked" {
				consecuteFails++
				failed++
			} else {
				consecuteFails = 0
				completed++
			}
		}

		results = append(results, result)
		totalRun++

		slog.Default().Info("task finished",
			"task_id", response.FocusTaskID,
			"status", result.Status,
			"duration", result.Duration,
			"completed", completed,
			"failed", failed,
		)

		// Circuit breaker
		if consecuteFails >= opts.maxFails {
			slog.Default().Warn("circuit breaker tripped", "consecutive_fails", consecuteFails, "max_fails", opts.maxFails)
			break
		}

		// Cooldown between tasks
		if completed < opts.maxTasks {
			time.Sleep(opts.cooldown)
		}
	}

	duration := time.Since(loopStart)

	// Persist run results as event (non-fatal)
	runResult := actions.RunResult{
		Completed: completed,
		Failed:    failed,
		Total:     totalRun,
		Duration:  duration.Seconds(),
	}
	persistRequestID := fmt.Sprintf("run_result_%d", time.Now().UnixMilli())
	if err := withDB(func(db *DB) error {
		_, err := actions.PersistRunResultIdempotent(db, opts.agentName, persistRequestID, opts.project, runResult)
		return err
	}); err != nil {
		slog.Default().Warn("failed to persist run results", "error", err)
	}

	type resp struct {
		Completed   int          `json:"completed"`
		Failed      int          `json:"failed"`
		Total       int          `json:"total"`
		DurationSec float64      `json:"duration_sec"`
		Results     []taskResult `json:"results"`
	}
	r := resp{
		Completed:   completed,
		Failed:      failed,
		Total:       totalRun,
		DurationSec: duration.Seconds(),
		Results:     results,
	}

	// Execute post-run hook if configured (non-fatal)
	if opts.postHook != "" {
		resultsJSON, marshalErr := json.Marshal(r)
		if marshalErr != nil {
			slog.Default().Warn("failed to marshal results for post-hook", "error", marshalErr)
		} else if hookErr := execPostRunHook(opts.postHook, resultsJSON); hookErr != nil {
			slog.Default().Warn("post-run hook failed", "error", hookErr, "hook", opts.postHook)
		}
	}

	return output.PrintSuccess(r)
}

// execPostRunHook pipes run results JSON to an external command via stdin.
func execPostRunHook(command string, resultsJSON []byte) error {
	cmd := exec.CommandContext(context.Background(), "sh", "-c", command) //nolint:gosec // G204: sh is a known system tool
	cmd.Stdin = bytes.NewReader(resultsJSON)
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("post-run hook %q: %w", command, err)
	}
	return nil
}

// newLoopStatsCmd creates the "loop stats" subcommand.
func newLoopStatsCmd() *cobra.Command {
	var (
		lastN   int
		project string
	)

	cmd := &cobra.Command{
		Use:   "stats",
		Short: "Show run dashboard with aggregate statistics",
		RunE: func(cmd *cobra.Command, args []string) error {
			agentName := resolveActorName(cmd, "")

			return withDB(func(db *DB) error {
				dash, err := actions.RunStats(db, agentName, project, lastN)
				if err != nil {
					return err
				}
				return output.PrintSuccess(dash)
			})
		},
	}

	cmd.Flags().IntVar(&lastN, "last", 7, "Number of recent runs to aggregate")
	cmd.Flags().StringVar(&project, "project", "", "Project ID to filter runs")

	return cmd
}

// buildAgentPrompt constructs the prompt sent to the spawned agent.
// It wraps vybe's resume prompt with autonomous-mode rules.
// The resume prompt already contains VYBE CONTEXT and VYBE COMMANDS sections,
// so this only adds behavioral instructions — no duplicate commands.
func buildAgentPrompt(r *actions.ResumeResponse) string {
	var b strings.Builder

	// Vybe's resume prompt has: task details, memory, events, and commands
	b.WriteString(r.Prompt)

	// Autonomous behavior rules — tells the agent HOW to work, not WHAT commands to run
	b.WriteString("\n== AUTONOMOUS MODE ==\n")
	b.WriteString("There is no human to ask questions. You must work independently.\n\n")
	b.WriteString("Steps:\n")
	b.WriteString("1. Read \"Your current task\" above. Do exactly what the description says.\n")
	b.WriteString("2. Work on the task. Log progress using command 3 from COMMANDS above.\n")
	b.WriteString("3. When finished, run command 1 (DONE) from COMMANDS above.\n")
	b.WriteString("4. If you get stuck, run command 2 (STUCK) from COMMANDS above.\n")
	b.WriteString("5. You MUST run either DONE or STUCK before you stop.\n")

	return b.String()
}

// spawnAgent runs the external command with the prompt and returns the exit code.
func spawnAgent(command, prompt, project string, timeout time.Duration) int {
	args := []string{"-p", prompt}
	if project != "" {
		args = append([]string{"--project", project}, args...)
	}

	cmd := exec.CommandContext(context.Background(), command, args...) //nolint:gosec // G204: command is user-configured and intentional for autonomous agent spawning
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin

	// Start with timeout
	if err := cmd.Start(); err != nil {
		slog.Default().Error("failed to start command", "command", command, "error", err)
		return 1
	}

	done := make(chan error, 1)
	go func() {
		done <- cmd.Wait()
	}()

	select {
	case err := <-done:
		if err != nil {
			var exitErr *exec.ExitError
			if errors.As(err, &exitErr) {
				return exitErr.ExitCode()
			}
			return 1
		}
		return 0
	case <-time.After(timeout):
		_ = cmd.Process.Kill()
		slog.Default().Warn("command timed out, killed", "timeout", timeout)
		return 124 // standard timeout exit code
	}
}

// markTaskBlocked sets a task to blocked status via vybe and records the failure reason.
func markTaskBlocked(agentName, taskID, reason string) {
	_ = withDB(func(db *DB) error {
		requestID := fmt.Sprintf("block_%s_%d", taskID, time.Now().UnixMilli())

		// Log why it's blocked
		_, _ = store.AppendEventIdempotent(db, agentName, requestID+"_log", "task_blocked", taskID, reason)

		// Set status + blocked_reason atomically
		_, _, err := actions.TaskSetStatusIdempotent(db, agentName, requestID, taskID, "blocked", models.BlockedReasonFailurePrefix+reason)
		return err
	})
}
