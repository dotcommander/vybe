package llm

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestResolveRunner_Claude(t *testing.T) {
	r := resolveRunner("claude")
	assert.Equal(t, "claude", r.command)
	assert.Equal(t, []string{"-p", "hello", "--output-format", "text"}, r.args("hello"))
}

func TestResolveRunner_OpenCode(t *testing.T) {
	r := resolveRunner("opencode")
	assert.Equal(t, "opencode", r.command)
	assert.Equal(t, []string{"run", "hello"}, r.args("hello"))
}

func TestResolveRunner_OpenCodePrefixed(t *testing.T) {
	r := resolveRunner("opencode-worker-1")
	assert.Equal(t, "opencode", r.command)
}

func TestResolveRunner_DefaultToClaude(t *testing.T) {
	r := resolveRunner("some-agent")
	assert.Equal(t, "claude", r.command)
}

func TestResolveRunner_CaseInsensitive(t *testing.T) {
	r := resolveRunner("OpenCode")
	assert.Equal(t, "opencode", r.command)

	r = resolveRunner("CLAUDE")
	assert.Equal(t, "claude", r.command)
}

func TestNewRunner_NilWhenCommandMissing(t *testing.T) {
	// Use a command that won't exist
	r := resolveRunner("opencode")
	if _, err := exec.LookPath(r.command); err != nil {
		runner := NewRunner("opencode")
		assert.Nil(t, runner)
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
	r := resolveRunner("claude")
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
echo '[{"type":"pattern","key":"claude_test","value":"extracted via claude","scope":"global"}]'
`), 0o755)
	require.NoError(t, err)

	t.Setenv("PATH", dir)

	runner := NewRunner("claude")
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

	runner := NewRunner("opencode-agent")
	require.NotNil(t, runner)
	assert.Equal(t, "opencode", runner.Command())

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	result, err := runner.Extract(ctx, "test prompt")
	require.NoError(t, err)
	assert.Contains(t, result, "opencode_test")
	assert.Contains(t, result, "extracted via opencode")
}
