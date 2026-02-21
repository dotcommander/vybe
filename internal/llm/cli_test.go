package llm

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestResolveRunner_Claude(t *testing.T) {
	r, err := resolveRunner("claude")
	require.NoError(t, err)
	assert.Equal(t, "claude", r.command)
	assert.Equal(t, []string{"-p", "hello", "--output-format", "text", "--settings", `{"hooks":{}}`}, r.args("hello"))
}

func TestResolveRunner_OpenCode(t *testing.T) {
	r, err := resolveRunner("opencode")
	require.NoError(t, err)
	assert.Equal(t, "opencode", r.command)
	assert.Equal(t, []string{"run", "hello"}, r.args("hello"))
}

func TestResolveRunner_OpenCodePrefixed(t *testing.T) {
	r, err := resolveRunner("opencode-worker-1")
	require.NoError(t, err)
	assert.Equal(t, "opencode", r.command)
}

func TestResolveRunner_UnknownAgent(t *testing.T) {
	_, err := resolveRunner("some-agent")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unknown agent type")
}

func TestResolveRunner_EmptyDefaultsClaude(t *testing.T) {
	r, err := resolveRunner("")
	require.NoError(t, err)
	assert.Equal(t, "claude", r.command)
}

func TestResolveRunner_CaseInsensitive(t *testing.T) {
	r, err := resolveRunner("OpenCode")
	require.NoError(t, err)
	assert.Equal(t, "opencode", r.command)

	r, err = resolveRunner("CLAUDE")
	require.NoError(t, err)
	assert.Equal(t, "claude", r.command)
}

func TestNewRunner_ErrorOnUnknownAgent(t *testing.T) {
	_, err := NewRunner("unknown-agent")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unknown agent type")
}

func TestNewRunner_ErrorOnMissingBinary(t *testing.T) {
	// Only run if opencode is NOT in PATH
	if _, err := exec.LookPath("opencode"); err != nil {
		_, runnerErr := NewRunner("opencode")
		require.Error(t, runnerErr)
		assert.Contains(t, runnerErr.Error(), "not found in PATH")
	}
}

func TestExtract_WithMockScript(t *testing.T) {
	// Create a temporary script that echoes its input
	dir := t.TempDir()
	script := filepath.Join(dir, "mock-claude")
	err := os.WriteFile(script, []byte("#!/bin/sh\necho '[{\"type\":\"knowledge\",\"key\":\"test_key\",\"value\":\"test value\",\"scope\":\"global\"}]'\n"), 0o755)
	require.NoError(t, err)

	r := &Runner{
		command: script,
		args:    func(p string) []string { return []string{p} },
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	result, err := r.Extract(ctx, "test prompt")
	require.NoError(t, err)
	assert.Contains(t, result, "test_key")
}

func TestExtract_FailsOnBadCommand(t *testing.T) {
	r := &Runner{
		command: "/nonexistent/command",
		args:    func(p string) []string { return []string{p} },
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, err := r.Extract(ctx, "test")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed")
}

func TestCommand(t *testing.T) {
	r, err := resolveRunner("claude")
	require.NoError(t, err)
	assert.Equal(t, "claude", r.Command())
}

// TestExtract_ClaudeDispatch verifies the claude agent path builds correct args
// and processes output through a mock script named "claude".
func TestExtract_ClaudeDispatch(t *testing.T) {
	dir := t.TempDir()
	// Mock "claude" script: verify -p and --output-format flags, emit JSON
	script := filepath.Join(dir, "claude")
	err := os.WriteFile(script, []byte(`#!/bin/sh
# Verify we received -p flag
if [ "$1" != "-p" ]; then
  echo "expected -p as first arg, got $1" >&2
  exit 1
fi
if [ "$3" != "--output-format" ] || [ "$4" != "text" ]; then
  echo "expected --output-format text" >&2
  exit 1
fi
if [ "$5" != "--settings" ] || [ "$6" != '{"hooks":{}}' ]; then
  echo "expected --settings hookless json" >&2
  exit 1
fi
echo '[{"type":"pattern","key":"claude_test","value":"extracted via claude","scope":"global"}]'
`), 0o755)
	require.NoError(t, err)

	t.Setenv("PATH", dir)

	runner, err := NewRunner("claude")
	require.NoError(t, err)
	require.NotNil(t, runner)
	assert.Equal(t, "claude", runner.Command())

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	result, err := runner.Extract(ctx, "test prompt")
	require.NoError(t, err)
	assert.Contains(t, result, "claude_test")
	assert.Contains(t, result, "extracted via claude")
}

// TestExtract_OpenCodeDispatch verifies the opencode agent path builds correct args
// and processes output through a mock script named "opencode".
func TestExtract_OpenCodeDispatch(t *testing.T) {
	dir := t.TempDir()
	// Mock "opencode" script: verify "run" subcommand, emit JSON
	script := filepath.Join(dir, "opencode")
	err := os.WriteFile(script, []byte(`#!/bin/sh
# Verify we received "run" as first arg
if [ "$1" != "run" ]; then
  echo "expected run as first arg, got $1" >&2
  exit 1
fi
echo '[{"type":"knowledge","key":"opencode_test","value":"extracted via opencode","scope":"global"}]'
`), 0o755)
	require.NoError(t, err)

	t.Setenv("PATH", dir)

	runner, err := NewRunner("opencode-agent")
	require.NoError(t, err)
	require.NotNil(t, runner)
	assert.Equal(t, "opencode", runner.Command())

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	result, err := runner.Extract(ctx, "test prompt")
	require.NoError(t, err)
	assert.Contains(t, result, "opencode_test")
	assert.Contains(t, result, "extracted via opencode")
}

func TestValidatePrompt(t *testing.T) {
	tests := []struct {
		name    string
		prompt  string
		wantErr bool
	}{
		{"valid", "analyze these events", false},
		{"empty", "", true},
		{"null_byte", "test\x00injected", true},
		{"max_length", strings.Repeat("a", 16000), false},
		{"over_max_length", strings.Repeat("a", 16001), true},
		{"shell_metachar_allowed", "test; echo hello | cat", false}, // safe in Go exec
		{"newlines_allowed", "line1\nline2\nline3", false},
		{"unicode_allowed", "日本語テスト", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validatePrompt(tt.prompt)
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestExtract_RejectsInvalidPrompt(t *testing.T) {
	r := &Runner{
		command: "echo",
		args:    func(p string) []string { return []string{p} },
	}

	ctx := context.Background()

	_, err := r.Extract(ctx, "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid prompt")

	_, err = r.Extract(ctx, "test\x00injected")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "null byte")
}

func TestExtract_CancelledContext(t *testing.T) {
	r := &Runner{
		command: "echo",
		args:    func(p string) []string { return []string{p} },
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	_, err := r.Extract(ctx, "test prompt")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "context expired")
}

func TestLimitedWriter(t *testing.T) {
	w := &limitedWriter{maxBytes: 10}
	n, err := w.Write([]byte("hello"))
	require.NoError(t, err)
	assert.Equal(t, 5, n)
	assert.Equal(t, "hello", w.buf.String())

	// Overflow: write 20 bytes, only first 5 more fit
	n, err = w.Write([]byte("world and then some!"))
	require.NoError(t, err)
	assert.Equal(t, 20, n)                        // reports full len
	assert.Equal(t, "helloworld", w.buf.String()) // capped at 10
}

func TestExtract_StderrCapped(t *testing.T) {
	dir := t.TempDir()
	// Script that emits 10KB to stderr then fails
	script := filepath.Join(dir, "noisy-cli")
	err := os.WriteFile(script, []byte("#!/bin/sh\ndd if=/dev/zero bs=1024 count=10 2>/dev/null | tr '\\0' 'x' >&2\nexit 1\n"), 0o755)
	require.NoError(t, err)

	r := &Runner{
		command: script,
		args:    func(p string) []string { return []string{} },
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, err = r.Extract(ctx, "test")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "truncated")
	// Verify stderr portion of error message is bounded
	assert.Less(t, len(err.Error()), 5000)
}
