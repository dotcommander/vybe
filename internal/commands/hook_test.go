package commands

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/dotcommander/vybe/internal/store"
	"github.com/stretchr/testify/require"
)

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

func TestBuildToolSuccessMetadata(t *testing.T) {
	input := hookInput{
		SessionID:     "test-session",
		HookEventName: "PostToolUse",
		ToolName:      "Write",
		ToolInput:     json.RawMessage(`{"file_path":"/tmp/test.go","content":"package main"}`),
		ToolResponse:  json.RawMessage(`{}`),
	}

	meta := buildToolMetadata(input)

	var parsed map[string]any
	require.NoError(t, json.Unmarshal([]byte(meta), &parsed))
	require.Equal(t, "claude", parsed["source"])
	require.Equal(t, "Write", parsed["tool_name"])
	require.Equal(t, "PostToolUse", parsed["hook_event"])
	require.LessOrEqual(t, len(meta), store.MaxEventMetadataLength)
}

func TestReadAutoMemory_EmptyCWD(t *testing.T) {
	got := readAutoMemory("", maxAutoMemoryChars)
	require.Empty(t, got)
}

func TestReadAutoMemory_NonexistentPath(t *testing.T) {
	got := readAutoMemory("/nonexistent/path/for/test", maxAutoMemoryChars)
	require.Empty(t, got)
}

func TestSessionStartCompactSourceField(t *testing.T) {
	// Verify the Source field is correctly parsed from hook input
	var input hookInput
	err := json.Unmarshal([]byte(`{"source":"compact","cwd":"/tmp/test","session_id":"sess_123"}`), &input)
	require.NoError(t, err)
	require.Equal(t, "compact", input.Source)
	require.Equal(t, "/tmp/test", input.CWD)
}

func TestPrevSessionCacheVariables(t *testing.T) {
	// Save and restore cache state so this test is order-independent.
	savedPath := prevSessionCachePath
	savedMod := prevSessionCacheModTime
	savedResult := prevSessionCacheResult
	t.Cleanup(func() {
		prevSessionCachePath = savedPath
		prevSessionCacheModTime = savedMod
		prevSessionCacheResult = savedResult
	})

	// Reset to known-empty state for this test.
	prevSessionCachePath = ""
	prevSessionCacheModTime = time.Time{}
	prevSessionCacheResult = ""

	// Verify cache variables are accessible and start empty after reset.
	require.Empty(t, prevSessionCachePath)
	require.True(t, prevSessionCacheModTime.IsZero())
	require.Empty(t, prevSessionCacheResult)

	// Verify readPreviousSessionContext returns empty for nonexistent path
	// (doesn't panic on cache operations).
	result := readPreviousSessionContext("/nonexistent/path/for/cache/test", "sess_test")
	require.Empty(t, result)
}
