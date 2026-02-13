package commands

import (
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/dotcommander/vybe/internal/actions"
	"github.com/dotcommander/vybe/internal/models"
	"github.com/dotcommander/vybe/internal/output"
	"github.com/dotcommander/vybe/internal/store"

	"github.com/spf13/cobra"
)

// NewRunCmd creates the autonomous driver command.
func NewRunCmd() *cobra.Command {
	var (
		project     string
		maxTasks    int
		maxFails    int
		taskTimeout string
		cooldown    string
		dryRun      bool
		command     string
	)

	cmd := &cobra.Command{
		Use:   "run",
		Short: "Autonomous task driver — loops resume → spawn → complete",
		Long: `Run is the autonomous driver loop. It repeatedly calls resume to get the next
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

			opts := runOptions{
				agentName:   agentName,
				project:     project,
				maxTasks:    maxTasks,
				maxFails:    maxFails,
				taskTimeout: timeout,
				cooldown:    cool,
				dryRun:      dryRun,
				command:     command,
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
}

type taskResult struct {
	TaskID    string `json:"task_id"`
	TaskTitle string `json:"task_title"`
	Status    string `json:"status"` // completed, blocked, failed, timeout
	Duration  string `json:"duration"`
}

func runLoop(opts runOptions) error {
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
			slog.Info("no pending tasks, exiting", "completed", completed, "failed", failed)
			break
		}

		taskTitle := ""
		if response.Brief != nil && response.Brief.Task != nil {
			taskTitle = response.Brief.Task.Title
		}

		slog.Info("task selected",
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
		var finalStatus string
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
			result.Status = finalStatus
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

		slog.Info("task finished",
			"task_id", response.FocusTaskID,
			"status", result.Status,
			"duration", result.Duration,
			"completed", completed,
			"failed", failed,
		)

		// Circuit breaker
		if consecuteFails >= opts.maxFails {
			slog.Warn("circuit breaker tripped", "consecutive_fails", consecuteFails, "max_fails", opts.maxFails)
			break
		}

		// Cooldown between tasks
		if completed < opts.maxTasks {
			time.Sleep(opts.cooldown)
		}
	}

	type resp struct {
		Completed int          `json:"completed"`
		Failed    int          `json:"failed"`
		Total     int          `json:"total"`
		Results   []taskResult `json:"results"`
	}
	return output.PrintSuccess(resp{
		Completed: completed,
		Failed:    failed,
		Total:     totalRun,
		Results:   results,
	})
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

	cmd := exec.Command(command, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin

	// Start with timeout
	if err := cmd.Start(); err != nil {
		slog.Error("failed to start command", "command", command, "error", err)
		return 1
	}

	done := make(chan error, 1)
	go func() {
		done <- cmd.Wait()
	}()

	select {
	case err := <-done:
		if err != nil {
			if exitErr, ok := err.(*exec.ExitError); ok {
				return exitErr.ExitCode()
			}
			return 1
		}
		return 0
	case <-time.After(timeout):
		_ = cmd.Process.Kill()
		slog.Warn("command timed out, killed", "timeout", timeout)
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
