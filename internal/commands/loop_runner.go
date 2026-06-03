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
	"os/signal"
	"syscall"
	"time"

	"github.com/dotcommander/vybe/internal/actions"
	"github.com/dotcommander/vybe/internal/models"
	"github.com/dotcommander/vybe/internal/output"
	"github.com/dotcommander/vybe/internal/store"
)

//nolint:gocognit,gocyclo,funlen,revive // run loop orchestrates per-task execution with run, status-update, and retry phases
func runLoop(opts runOptions) error {
	loopStart := time.Now()

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	var (
		completed        int
		failed           int
		totalRun         int
		consecutiveFails int
		results          []taskResult
	)

	for completed < opts.maxTasks {
		if ctx.Err() != nil {
			slog.Default().Info("shutdown signal received, exiting gracefully",
				"completed", completed, "failed", failed)
			break
		}

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
		prompt := buildAgentPrompt(response, opts.project)

		// Spawn the command
		start := time.Now()
		exitCode := spawnAgent(opts.command, prompt, opts.project, opts.taskTimeout, opts.disableHooks)
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
			consecutiveFails++
			failed++
		case finalStatus == "completed":
			result.Status = "completed"
			consecutiveFails = 0
			completed++
		case finalStatus == "in_progress" || finalStatus == "pending":
			// Agent didn't mark it done — treat as blocked
			result.Status = "blocked"
			markTaskBlocked(opts.agentName, response.FocusTaskID, "agent exited without completing")
			consecutiveFails++
			failed++
		default:
			result.Status = string(finalStatus)
			if finalStatus == "blocked" {
				consecutiveFails++
				failed++
			} else {
				consecutiveFails = 0
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
		if consecutiveFails >= opts.maxFails {
			slog.Default().Warn("circuit breaker tripped", "consecutive_fails", consecutiveFails, "max_fails", opts.maxFails)
			break
		}

		// Cooldown between tasks (interruptible by shutdown signal)
		if completed < opts.maxTasks {
			cooldownTimer := time.NewTimer(opts.cooldown)
			select {
			case <-cooldownTimer.C:
			case <-ctx.Done():
				cooldownTimer.Stop()
			}
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
//
// Security model: --post-hook is operator-supplied at agent invocation time, not
// derived from task content. Results JSON is passed only via stdin (not interpolated
// into the command string), so task data cannot influence the shell command itself.
// A 30-second timeout prevents runaway hook processes from blocking the loop.
func execPostRunHook(command string, resultsJSON []byte) error {
	if command == "" {
		return fmt.Errorf("post-run hook command must not be empty")
	}

	ctx, cancel := context.WithTimeout(context.Background(), postRunHookTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, "sh", "-c", command) //nolint:gosec // G204: operator-supplied hook, not derived from task data
	cmd.Stdin = bytes.NewReader(resultsJSON)
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("post-run hook %q start: %w", command, err)
	}

	done := make(chan error, 1)
	go func() {
		done <- cmd.Wait()
	}()

	select {
	case err := <-done:
		if err != nil {
			return fmt.Errorf("post-run hook %q: %w", command, err)
		}
		return nil
	case <-ctx.Done():
		if killErr := killCommandProcess(cmd, done); killErr != nil {
			slog.Default().Warn("failed to kill timed out post-run hook", "error", killErr, "hook", command)
		}
		return fmt.Errorf("post-run hook %q timed out after %s", command, postRunHookTimeout)
	}
}

func killCommandProcess(cmd *exec.Cmd, done <-chan error) error {
	if cmd == nil || cmd.Process == nil {
		return nil
	}
	// Send SIGTERM first and give the process a grace period to exit cleanly.
	if err := cmd.Process.Signal(syscall.SIGTERM); err != nil && !errors.Is(err, os.ErrProcessDone) {
		// SIGTERM failed for a reason other than the process already being done;
		// fall through to SIGKILL below.
		slog.Default().Warn("SIGTERM failed, escalating to SIGKILL", "error", err)
	}

	if waitForProcessExit(done) {
		return nil
	}

	// Grace period elapsed — escalate to SIGKILL.
	if err := cmd.Process.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
		return err
	}
	_ = waitForProcessExit(done)
	return nil
}

func waitForProcessExit(done <-chan error) bool {
	timer := time.NewTimer(processExitWaitTime)
	defer timer.Stop()
	select {
	case <-done:
		return true
	case <-timer.C:
		return false
	}
}
