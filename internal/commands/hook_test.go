package commands

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/dotcommander/vybe/internal/store"
	"github.com/stretchr/testify/require"
)

func TestToolInputSummary(t *testing.T) {
	tests := []struct {
		name     string
		toolName string
		input    string
		want     string
	}{
		{
			name:     "Write extracts file_path",
			toolName: "Write",
			input:    `{"file_path":"/tmp/foo.go","content":"package main"}`,
			want:     "Write: /tmp/foo.go",
		},
		{
			name:     "Edit extracts file_path",
			toolName: "Edit",
			input:    `{"file_path":"/tmp/bar.go","old_string":"a","new_string":"b"}`,
			want:     "Edit: /tmp/bar.go",
		},
		{
			name:     "Bash extracts command",
			toolName: "Bash",
			input:    `{"command":"go build ./..."}`,
			want:     "Bash: go build ./...",
		},
		{
			name:     "Bash truncates long commands",
			toolName: "Bash",
			input:    `{"command":"` + strings.Repeat("x", 200) + `"}`,
			want:     "Bash: " + strings.Repeat("x", 120),
		},
		{
			name:     "NotebookEdit extracts notebook_path",
			toolName: "NotebookEdit",
			input:    `{"notebook_path":"/tmp/nb.ipynb"}`,
			want:     "NotebookEdit: /tmp/nb.ipynb",
		},
		{
			name:     "Unknown tool returns tool name",
			toolName: "Read",
			input:    `{"file_path":"/tmp/foo.go"}`,
			want:     "Read",
		},
		{
			name:     "Invalid JSON returns tool name",
			toolName: "Write",
			input:    `{invalid`,
			want:     "Write",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := toolInputSummary(tt.toolName, json.RawMessage(tt.input))
			require.Equal(t, tt.want, got)
		})
	}
}

func TestMutatingTools(t *testing.T) {
	// Expected mutating tools
	for _, tool := range []string{"Write", "Edit", "MultiEdit", "Bash", "NotebookEdit"} {
		require.True(t, mutatingTools[tool], "%s should be mutating", tool)
	}

	// Read-only tools should not be in set
	for _, tool := range []string{"Read", "Glob", "Grep", "LSP", "WebFetch", "WebSearch"} {
		require.False(t, mutatingTools[tool], "%s should not be mutating", tool)
	}
}

func TestTruncateString(t *testing.T) {
	// Within limit — passthrough
	s, truncated := truncateString("hello", 10)
	require.Equal(t, "hello", s)
	require.False(t, truncated)

	// Exact limit — passthrough
	s, truncated = truncateString("hello", 5)
	require.Equal(t, "hello", s)
	require.False(t, truncated)

	// Over limit — truncated
	s, truncated = truncateString("hello world", 5)
	require.Equal(t, "hello", s)
	require.True(t, truncated)

	// Zero max — passthrough (edge case)
	s, truncated = truncateString("hello", 0)
	require.Equal(t, "hello", s)
	require.False(t, truncated)

	// Empty string
	s, truncated = truncateString("", 10)
	require.Equal(t, "", s)
	require.False(t, truncated)
}

func TestBuildToolFailureMetadata(t *testing.T) {
	input := hookInput{
		SessionID:     "test-session",
		HookEventName: "PostToolUseFailure",
		ToolName:      "Bash",
		ToolInput:     json.RawMessage(`{"command":"go build"}`),
		ToolResponse:  json.RawMessage(`{"error":"exit 1"}`),
	}

	meta := buildToolMetadata(input)

	// Should be valid JSON
	var parsed map[string]any
	require.NoError(t, json.Unmarshal([]byte(meta), &parsed))

	// Should contain expected fields
	require.Equal(t, "claude", parsed["source"])
	require.Equal(t, "test-session", parsed["session_id"])
	require.Equal(t, "Bash", parsed["tool_name"])
	require.Equal(t, "PostToolUseFailure", parsed["hook_event"])

	// Should respect MaxEventMetadataLength
	require.LessOrEqual(t, len(meta), store.MaxEventMetadataLength)
}

func TestBuildToolFailureMetadata_LargePayload(t *testing.T) {
	// Create input that exceeds metadata limit to trigger fallback cascade
	largePayload := make([]byte, store.MaxEventMetadataLength)
	for i := range largePayload {
		largePayload[i] = 'x'
	}

	input := hookInput{
		SessionID:     "test-session",
		HookEventName: "PostToolUseFailure",
		ToolName:      "Bash",
		ToolInput:     json.RawMessage(`"` + string(largePayload) + `"`),
		ToolResponse:  json.RawMessage(`"` + string(largePayload) + `"`),
	}

	meta := buildToolMetadata(input)
	require.LessOrEqual(t, len(meta), store.MaxEventMetadataLength)

	var parsed map[string]any
	require.NoError(t, json.Unmarshal([]byte(meta), &parsed))
	require.Equal(t, "claude", parsed["source"])
}

func TestReadHookStdin_InvalidJSON(t *testing.T) {
	// readHookStdin reads from os.Stdin which we can't easily mock in unit tests.
	// Instead we test the hookInput struct behavior directly.
	var input hookInput
	err := json.Unmarshal([]byte(`{"session_id":"abc","tool_name":"Bash"}`), &input)
	require.NoError(t, err)
	require.Equal(t, "abc", input.SessionID)
	require.Equal(t, "Bash", input.ToolName)
}

func TestReadHookStdin_UnknownFieldsIgnored(t *testing.T) {
	var input hookInput
	err := json.Unmarshal([]byte(`{"unknown_field":"value"}`), &input)
	require.NoError(t, err)
	require.Empty(t, input.SessionID)
}

func TestHookRequestID(t *testing.T) {
	id := hookRequestID("test", "claude")
	require.Contains(t, id, "hook_test_claude_")
	require.NotEmpty(t, id)

	// Two calls should produce different IDs
	id2 := hookRequestID("test", "claude")
	require.NotEqual(t, id, id2)
}

func TestStableHookRequestID(t *testing.T) {
	id1 := stableHookRequestID("session_end", "agent-a", "sess_123")
	id2 := stableHookRequestID("session_end", "agent-a", "sess_123")
	require.Equal(t, id1, id2)
	require.Contains(t, id1, "hook_session_end_agent-a_sess_123")

	id3 := stableHookRequestID("session_end", "agent-a", "")
	id4 := stableHookRequestID("session_end", "agent-a", "")
	require.NotEqual(t, id3, id4)
}

func TestSanitizeRequestToken(t *testing.T) {
	got := sanitizeRequestToken("abc:/def?ghi", 64)
	require.Equal(t, "abc__def_ghi", got)
	require.Equal(t, "session", sanitizeRequestToken("", 64))
}

func TestIsRetrospectiveChildProcess(t *testing.T) {
	t.Setenv(retroChildEnv, "")
	require.False(t, isRetrospectiveChildProcess())

	t.Setenv(retroChildEnv, "1")
	require.True(t, isRetrospectiveChildProcess())
}
