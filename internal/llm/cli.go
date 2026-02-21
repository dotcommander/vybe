package llm

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

const disableExternalLLMEnv = "VYBE_DISABLE_EXTERNAL_LLM"

const claudeHooklessSettingsJSON = `{"hooks":{}}`

// validatePrompt checks for unsafe characters in prompts.
// While Go's exec avoids shell injection (no shell involved),
// this is defense-in-depth: external CLIs may be shell scripts.
func validatePrompt(s string) error {
	if len(s) == 0 {
		return errors.New("empty prompt")
	}
	if len(s) > 16000 {
		return fmt.Errorf("prompt exceeds 16000 byte limit (%d bytes)", len(s))
	}
	if strings.ContainsRune(s, 0) {
		return errors.New("prompt contains null byte")
	}
	return nil
}

// Runner dispatches extraction prompts to a CLI tool based on agent identity.
// "claude" agents use `claude -p`, "opencode" agents use `opencode run`.
// No API keys required — the CLIs handle their own auth.
type Runner struct {
	command string
	args    func(prompt string) []string
}

// NewRunner returns a Runner for the given agent name.
// Returns error if agent type is unknown or CLI binary is not found in PATH.
func NewRunner(agentName string) (*Runner, error) {
	if strings.TrimSpace(os.Getenv(disableExternalLLMEnv)) != "" {
		return nil, fmt.Errorf("external LLM CLI execution disabled by %s", disableExternalLLMEnv)
	}

	r, err := resolveRunner(agentName)
	if err != nil {
		return nil, err
	}
	if _, err := exec.LookPath(r.command); err != nil {
		return nil, fmt.Errorf("cli tool %q not found in PATH: %w", r.command, err)
	}
	return r, nil
}

// resolveRunner maps agent name to CLI command + arg builder.
// Returns error for unknown agent types. Empty string defaults to claude.
func resolveRunner(agentName string) (*Runner, error) {
	name := strings.ToLower(agentName)
	switch {
	case strings.HasPrefix(name, "opencode"):
		return &Runner{
			command: "opencode",
			args:    func(p string) []string { return []string{"run", p} },
		}, nil
	case strings.HasPrefix(name, "claude"), name == "":
		return &Runner{
			command: "claude",
			args: func(p string) []string {
				return []string{"-p", p, "--output-format", "text", "--settings", claudeHooklessSettingsJSON}
			},
		}, nil
	default:
		return nil, fmt.Errorf("unknown agent type %q (supported: claude, opencode)", agentName)
	}
}

// limitedWriter caps writes at maxBytes, silently discarding overflow.
// This prevents OOM attacks from malicious or buggy CLIs emitting unbounded stderr.
type limitedWriter struct {
	buf      bytes.Buffer
	maxBytes int
}

func (w *limitedWriter) Write(p []byte) (int, error) {
	originalLen := len(p)
	remaining := w.maxBytes - w.buf.Len()
	if remaining <= 0 {
		return originalLen, nil // discard but report success
	}
	if len(p) > remaining {
		p = p[:remaining]
	}
	w.buf.Write(p)
	return originalLen, nil // always report original len to avoid short write errors
}

// Extract runs the CLI with a combined prompt and returns the text response.
func (r *Runner) Extract(ctx context.Context, prompt string) (string, error) {
	if err := validatePrompt(prompt); err != nil {
		return "", fmt.Errorf("invalid prompt: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return "", fmt.Errorf("context expired before exec: %w", err)
	}
	args := r.args(prompt)
	cmd := exec.CommandContext(ctx, r.command, args...) //nolint:gosec // G204: command is caller-provided LLM CLI binary, validated at construction
	cmd.Env = os.Environ()

	var stdout bytes.Buffer
	stderrW := &limitedWriter{maxBytes: 4096}
	cmd.Stdout = &stdout
	cmd.Stderr = stderrW

	if err := cmd.Run(); err != nil {
		stderrMsg := stderrW.buf.String()
		if stderrW.buf.Len() >= stderrW.maxBytes {
			stderrMsg += " (truncated)"
		}
		return "", fmt.Errorf("cli %s failed: %w (stderr: %s)", r.command, err, stderrMsg)
	}

	return strings.TrimSpace(stdout.String()), nil
}

// Command returns the CLI command name for this runner.
func (r *Runner) Command() string {
	return r.command
}
