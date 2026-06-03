package commands

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/dotcommander/vybe/internal/actions"
	"github.com/dotcommander/vybe/internal/commands/hookcmd"
	"github.com/dotcommander/vybe/internal/models"
	"github.com/dotcommander/vybe/internal/store"
)

// buildAgentPrompt constructs the prompt sent to the spawned agent.
// It wraps vybe's resume prompt with autonomous-mode rules.
// The resume prompt already contains VYBE CONTEXT and VYBE COMMANDS sections,
// so this only adds behavioral instructions — no duplicate commands.
func buildAgentPrompt(r *actions.ResumeResponse, projectDir string) string {
	var b strings.Builder

	// Vybe's resume prompt has: task details, memory, events, and commands
	b.WriteString(r.Prompt)

	// Inject Claude Code auto memory for project context
	if projectDir != "" {
		if autoMem := hookcmd.ReadAutoMemory(projectDir, maxAutoMemoryChars); autoMem != "" {
			b.WriteString("\n== PROJECT MEMORY ==\n")
			b.WriteString(autoMem)
			b.WriteString("\n")
		}
	}

	// Autonomous behavior rules — tells the agent HOW to work, not WHAT commands to run
	b.WriteString("\n== AUTONOMOUS MODE ==\n")
	b.WriteString("There is no human to ask questions. You must work independently.\n\n")
	b.WriteString("Execution contract:\n")
	b.WriteString("1. Work only on \"Your current task\" and its task_id.\n")
	b.WriteString("2. Optional: emit progress logs with LOG.\n")
	b.WriteString("3. Before stopping, run exactly one terminal command:\n")
	b.WriteString("   - DONE: vybe done <id> --note \"<summary>\"  (marks the task completed), OR\n")
	b.WriteString("   - STUCK: vybe block <id> --reason \"<why>\"  (marks the task blocked).\n")
	b.WriteString("4. Do not use 'vybe task complete' in autonomous mode.\n")

	return b.String()
}

// spawnAgent runs the external command with the prompt and returns the exit code.
// The prompt is passed via a temp file to avoid macOS's 256KB CLI argument size limit.
func spawnAgent(command, prompt, project string, timeout time.Duration, disableHooks bool) int {
	tmpFile, err := os.CreateTemp("", "vybe-prompt-*.txt")
	if err != nil {
		slog.Default().Error("failed to create temp file for prompt", "error", err)
		return 1
	}
	defer os.Remove(tmpFile.Name()) //nolint:errcheck // best-effort cleanup

	if _, err := tmpFile.WriteString(prompt); err != nil {
		slog.Default().Error("failed to write prompt to temp file", "error", err)
		_ = tmpFile.Close()
		return 1
	}
	if err := tmpFile.Close(); err != nil {
		slog.Default().Error("failed to close temp file", "error", err)
		return 1
	}

	args := []string{"-p", "@" + tmpFile.Name()}
	if project != "" {
		args = append([]string{"--project", project}, args...)
	}
	if disableHooks && isClaudeCommand(command) {
		args = append(args, "--settings", `{"hooks":{}}`)
	}

	cmd := exec.CommandContext(context.Background(), command, args...) //nolint:gosec // G204: command is user-configured and intentional for autonomous agent spawning
	if disableHooks {
		cmd.Env = append(os.Environ(), disableExternalLLMEnv+"=1")
	} else {
		cmd.Env = os.Environ()
	}
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

	timer := time.NewTimer(timeout)
	defer timer.Stop()
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
	case <-timer.C:
		if err := killCommandProcess(cmd, done); err != nil {
			slog.Default().Warn("failed to kill timed out command", "command", command, "error", err)
		}
		slog.Default().Warn("command timed out, killed", "timeout", timeout)
		return 124 // standard timeout exit code
	}
}

func isClaudeCommand(command string) bool {
	base := strings.ToLower(filepath.Base(strings.TrimSpace(command)))
	return base == "claude"
}

// markTaskBlocked sets a task to blocked status via vybe and records the failure reason.
// Best-effort: called from error recovery path; DB errors are logged but not propagated.
func markTaskBlocked(agentName, taskID, reason string) {
	//nolint:errcheck // best-effort recovery — if DB is also down, nothing to do
	_ = withDB(func(db *DB) error {
		requestID := fmt.Sprintf("block_%s_%d", taskID, time.Now().UnixMilli())

		// Log why it's blocked
		_, _ = store.AppendEventIdempotent(db, agentName, requestID+"_log", "task_blocked", taskID, reason)

		// Set status + blocked_reason atomically
		_, _, err := actions.TaskSetStatusIdempotent(db, agentName, requestID, taskID, "blocked", string(models.NewFailureBlockedReason(reason)))
		return err
	})
}
