package commands

import (
	"fmt"
	"time"

	"github.com/spf13/cobra"
)

const (
	postRunHookTimeout  = 30 * time.Second
	processExitWaitTime = 2 * time.Second
	maxAutoMemoryChars  = 2000
)

// NewLoopCmd creates the autonomous driver command.
func NewLoopCmd() *cobra.Command {
	var (
		projectDir   string
		maxTasks     int
		maxFails     int
		taskTimeout  string
		cooldown     string
		dryRun       bool
		command      string
		postHook     string
		disableHooks bool
	)

	cmd := &cobra.Command{
		Use:   "loop",
		Short: "Autonomous task driver — loops resume → spawn → complete",
		Args:  cobra.NoArgs,
		Long: `Loop is the autonomous driver loop. It repeatedly calls resume to get the next
focus task, spawns an external command (specified via --command) with the task prompt,
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

			if !dryRun && command == "" {
				return cmdErr(fmt.Errorf("required flag(s) \"command\" not set"))
			}

			opts := runOptions{
				agentName:    agentName,
				project:      projectDir,
				maxTasks:     maxTasks,
				maxFails:     maxFails,
				taskTimeout:  timeout,
				cooldown:     cool,
				dryRun:       dryRun,
				command:      command,
				postHook:     postHook,
				disableHooks: disableHooks,
			}

			return runLoop(opts)
		},
	}

	cmd.Flags().StringVar(&projectDir, "project-dir", "", "Project directory to scope tasks and resume")
	cmd.Flags().IntVar(&maxTasks, "max-tasks", 10, "Stop after N tasks completed")
	cmd.Flags().IntVar(&maxFails, "max-fails", 3, "Circuit breaker: stop after N consecutive failures")
	cmd.Flags().StringVar(&taskTimeout, "task-timeout", "10m", "Kill spawned command after this duration")
	cmd.Flags().StringVar(&cooldown, "cooldown", "5s", "Wait between tasks")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Show what would run without spawning")
	cmd.Flags().StringVar(&command, "command", "", "Command to spawn (receives prompt via -p flag)")
	cmd.Flags().StringVar(&postHook, "post-hook", "", "Command to pipe run results JSON to on completion (must be explicitly set per run)")
	cmd.Flags().BoolVar(&disableHooks, "spawn-disable-hooks", false, "Disable hooks for spawned agents (sets hookless mode and isolation env vars)")

	cmd.Annotations = map[string]string{"mutates": "true"}
	return cmd
}

type runOptions struct {
	agentName    string
	project      string
	maxTasks     int
	maxFails     int
	taskTimeout  time.Duration
	cooldown     time.Duration
	dryRun       bool
	command      string
	postHook     string
	disableHooks bool
}

type taskResult struct {
	TaskID    string `json:"task_id"`
	TaskTitle string `json:"task_title"`
	Status    string `json:"status"` // completed, blocked, failed, timeout
	Duration  string `json:"duration"`
}
