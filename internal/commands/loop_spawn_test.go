package commands

import (
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/dotcommander/vybe/internal/actions"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestIsClaudeCommand(t *testing.T) {
	tests := []struct {
		name    string
		command string
		want    bool
	}{
		{name: "simple", command: "claude", want: true},
		{name: "absolute path", command: "/usr/local/bin/claude", want: true},
		{name: "uppercase", command: "CLAUDE", want: true},
		{name: "trimmed", command: "  claude  ", want: true},
		{name: "non-claude", command: "opencode", want: false},
		{name: "empty", command: "", want: false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := isClaudeCommand(tc.command)
			if got != tc.want {
				t.Fatalf("isClaudeCommand(%q) = %v, want %v", tc.command, got, tc.want)
			}
		})
	}
}

func TestBuildAgentPrompt_NoProjectDir(t *testing.T) {
	r := &actions.ResumeResponse{
		Prompt: "== VYBE CONTEXT ==\ntest prompt\n",
	}

	got := buildAgentPrompt(r, "")

	require.Contains(t, got, "== VYBE CONTEXT ==")
	require.Contains(t, got, "== AUTONOMOUS MODE ==")
	assert.NotContains(t, got, "PROJECT MEMORY")
}

func TestBuildAgentPrompt_WithProjectDir(t *testing.T) {
	// Non-existent dir — should still work, just no auto memory section
	r := &actions.ResumeResponse{
		Prompt: "== VYBE CONTEXT ==\ntest prompt\n",
	}

	got := buildAgentPrompt(r, "/nonexistent/path/for/test")

	require.Contains(t, got, "== VYBE CONTEXT ==")
	require.Contains(t, got, "== AUTONOMOUS MODE ==")
	assert.NotContains(t, got, "PROJECT MEMORY")
}

func TestKillCommandProcessEscalatesAfterIgnoredSIGTERM(t *testing.T) {
	readyFile := filepath.Join(t.TempDir(), "ready")
	cmd := exec.Command("sh", "-c", "trap '' TERM; : > \"$READY_FILE\"; while :; do :; done")
	cmd.Env = append(os.Environ(), "READY_FILE="+readyFile)
	require.NoError(t, cmd.Start())
	t.Cleanup(func() {
		if cmd.ProcessState == nil {
			_ = cmd.Process.Kill()
		}
	})
	require.Eventually(t, func() bool {
		_, err := os.Stat(readyFile)
		return err == nil
	}, time.Second, 10*time.Millisecond)

	done := make(chan error, 1)
	go func() {
		done <- cmd.Wait()
	}()

	require.NoError(t, killCommandProcess(cmd, done))
	require.NotNil(t, cmd.ProcessState)
	status, ok := cmd.ProcessState.Sys().(syscall.WaitStatus)
	require.True(t, ok)
	require.True(t, status.Signaled())
	require.Equal(t, syscall.SIGKILL, status.Signal())
}
