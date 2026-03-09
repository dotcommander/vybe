package demo

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
)

// CLICommandOptions controls how a vybe subprocess is invoked.
type CLICommandOptions struct {
	BinPath      string
	DBPath       string
	Agent        string
	Dir          string
	Stdin        string
	IncludeAgent bool
}

// RunCLICommand executes the vybe binary and returns trimmed stdout/stderr.
func RunCLICommand(opts CLICommandOptions, args ...string) (stdout string, stderr string, err error) {
	fullArgs := []string{"--db-path", opts.DBPath}
	if opts.IncludeAgent {
		fullArgs = append(fullArgs, "--agent", opts.Agent)
	}
	fullArgs = append(fullArgs, args...)

	cmd := exec.Command(opts.BinPath, fullArgs...)
	if opts.Dir != "" {
		cmd.Dir = opts.Dir
	}
	if opts.Stdin != "" {
		cmd.Stdin = strings.NewReader(opts.Stdin)
	}

	var outBuf, errBuf bytes.Buffer
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf
	err = cmd.Run()

	return strings.TrimSpace(outBuf.String()), strings.TrimSpace(errBuf.String()), err
}

// ParseCLIJSON parses the last valid JSON object from command output.
func ParseCLIJSON(raw string) (map[string]any, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}

	lines := strings.Split(raw, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.TrimSpace(lines[i])
		if line == "" {
			continue
		}
		var parsed map[string]any
		if err := json.Unmarshal([]byte(line), &parsed); err == nil {
			return parsed, nil
		}
	}

	var parsed map[string]any
	if err := json.Unmarshal([]byte(raw), &parsed); err != nil {
		return nil, fmt.Errorf("parse JSON: %w (output: %s)", err, raw)
	}
	return parsed, nil
}

// RunCLIJSON executes the vybe binary and parses the JSON response.
func RunCLIJSON(opts CLICommandOptions, args ...string) (map[string]any, string, error) {
	raw, stderr, err := RunCLICommand(opts, args...)
	if raw == "" {
		if err != nil {
			return nil, "", fmt.Errorf("command failed: %w (stderr: %s)", err, stderr)
		}
		return nil, raw, nil
	}

	parsed, parseErr := ParseCLIJSON(raw)
	if parseErr != nil {
		return nil, raw, parseErr
	}

	return parsed, raw, nil
}

// GetString extracts a nested string field from a parsed JSON map.
func GetString(m map[string]any, keys ...string) string {
	var cur any = m
	for _, key := range keys {
		if next, ok := cur.(map[string]any); ok {
			cur = next[key]
			continue
		}
		return ""
	}
	if s, ok := cur.(string); ok {
		return s
	}
	return ""
}

// RequestID generates a deterministic request ID for a given phase and step.
func RequestID(phase string, step int) string {
	return fmt.Sprintf("demo_%s_%d", phase, step)
}

// HookStdin builds the JSON stdin payload for hook commands.
func HookStdin(eventName, sessionID, cwd, source, prompt, toolName string) string {
	payload := map[string]any{
		"cwd":             cwd,
		"session_id":      sessionID,
		"hook_event_name": eventName,
		"prompt":          prompt,
		"tool_name":       toolName,
		"tool_input":      map[string]any{},
		"tool_response":   map[string]any{},
		"source":          source,
	}
	data, _ := json.Marshal(payload)
	return string(data)
}

// HookStdinWithToolInput builds the JSON stdin payload for hook commands with tool input.
func HookStdinWithToolInput(eventName, sessionID, cwd, toolName string, toolInput map[string]any) string {
	payload := map[string]any{
		"cwd":             cwd,
		"session_id":      sessionID,
		"hook_event_name": eventName,
		"tool_name":       toolName,
		"tool_input":      toolInput,
		"tool_response":   map[string]any{"output": "ok"},
		"source":          "",
	}
	data, _ := json.Marshal(payload)
	return string(data)
}

// Package-level aliases for backward compatibility with demo/test call sites.
var (
	getStr                = GetString
	rid                   = RequestID
	hookStdin             = HookStdin
	hookStdinWithToolInput = HookStdinWithToolInput
)
