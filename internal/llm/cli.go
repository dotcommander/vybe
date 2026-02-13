package llm

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
)

// Runner dispatches extraction prompts to a CLI tool based on agent identity.
// "claude" agents use `claude -p`, "opencode" agents use `opencode run`.
// No API keys required — the CLIs handle their own auth.
type Runner struct {
	command string
	args    func(prompt string) []string
}

// NewRunner returns a Runner for the given agent name.
// Returns nil if the CLI binary is not found in PATH. Callers must
// handle nil gracefully — session context extraction is best-effort
// and degrades silently when no CLI tool is available.
func NewRunner(agentName string) *Runner {
	r := resolveRunner(agentName)
	if _, err := exec.LookPath(r.command); err != nil {
		return nil
	}
	return r
}

// resolveRunner maps agent name to CLI command + arg builder.
func resolveRunner(agentName string) *Runner {
	name := strings.ToLower(agentName)
	switch {
	case strings.HasPrefix(name, "opencode"):
		return &Runner{
			command: "opencode",
			args:    func(p string) []string { return []string{"run", p} },
		}
	default:
		// Default to claude for all other agents.
		return &Runner{
			command: "claude",
			args:    func(p string) []string { return []string{"-p", p, "--output-format", "text"} },
		}
	}
}

// Extract runs the CLI with a combined prompt and returns the text response.
func (r *Runner) Extract(ctx context.Context, prompt string) (string, error) {
	args := r.args(prompt)
	cmd := exec.CommandContext(ctx, r.command, args...)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("cli %s failed: %w (stderr: %s)", r.command, err, stderr.String())
	}

	return strings.TrimSpace(stdout.String()), nil
}

// Command returns the CLI command name for this runner.
func (r *Runner) Command() string {
	return r.command
}
