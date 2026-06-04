package commands

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/dotcommander/vybe/internal/commands/hookcmd"
	"github.com/dotcommander/vybe/internal/store"
	"github.com/stretchr/testify/require"
)

// hookTestStdinMu serializes tests that replace os.Stdin. Parallel tests may
// otherwise race when two swap stdin concurrently on the same process.
var hookTestStdinMu sync.Mutex //nolint:gochecknoglobals // test-only mutex; cannot be avoided with global os.Stdin

// withHookStdin replaces os.Stdin with a pipe that provides payload, runs fn,
// then restores the original stdin. The caller must hold hookTestStdinMu.
func withHookStdin(t *testing.T, payload string, fn func()) {
	t.Helper()
	r, w, err := os.Pipe()
	require.NoError(t, err)
	_, writeErr := io.WriteString(w, payload)
	require.NoError(t, writeErr)
	require.NoError(t, w.Close())

	orig := os.Stdin
	os.Stdin = r
	t.Cleanup(func() {
		os.Stdin = orig
		_ = r.Close()
	})

	fn()
}

// hookStdinPayload builds the JSON stdin payload for a prompt hook invocation.
func hookStdinPayload(sessionID, cwd, prompt string) string {
	p := map[string]any{
		"cwd":             cwd,
		"session_id":      sessionID,
		"hook_event_name": "UserPromptSubmit",
		"prompt":          prompt,
	}
	b, _ := json.Marshal(p)
	return string(b)
}

// initTestDB creates a temp SQLite database and returns it along with its path.
// DB is the type alias defined in dbutil.go (= sql.DB).
func initTestDB(t *testing.T) (*DB, string) {
	t.Helper()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")
	db, err := store.InitDBWithPath(dbPath)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	return db, dbPath
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
	ev := CanonicalEvent{
		SessionID:     "test-session",
		HostEventName: "PostToolUseFailure",
		ToolName:      "Bash",
		ToolInput:     json.RawMessage(`{"command":"go build"}`),
		ToolResponse:  json.RawMessage(`{"error":"exit 1"}`),
		EventSource:   "claude",
	}

	meta := buildToolMetadata(ev)

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

	ev := CanonicalEvent{
		SessionID:     "test-session",
		HostEventName: "PostToolUseFailure",
		ToolName:      "Bash",
		ToolInput:     json.RawMessage(`"` + string(largePayload) + `"`),
		ToolResponse:  json.RawMessage(`"` + string(largePayload) + `"`),
		EventSource:   "claude",
	}

	meta := buildToolMetadata(ev)
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
	ev := CanonicalEvent{
		SessionID:     "test-session",
		HostEventName: "PostToolUse",
		ToolName:      "Write",
		ToolInput:     json.RawMessage(`{"file_path":"/tmp/test.go","content":"package main"}`),
		ToolResponse:  json.RawMessage(`{}`),
		EventSource:   "claude",
	}

	meta := buildToolMetadata(ev)

	var parsed map[string]any
	require.NoError(t, json.Unmarshal([]byte(meta), &parsed))
	require.Equal(t, "claude", parsed["source"])
	require.Equal(t, "Write", parsed["tool_name"])
	require.Equal(t, "PostToolUse", parsed["hook_event"])
	require.LessOrEqual(t, len(meta), store.MaxEventMetadataLength)
}

func TestReadAutoMemory_EmptyCWD(t *testing.T) {
	got := hookcmd.ReadAutoMemory("", maxAutoMemoryChars)
	require.Empty(t, got)
}

func TestReadAutoMemory_NonexistentPath(t *testing.T) {
	got := hookcmd.ReadAutoMemory("/nonexistent/path/for/test", maxAutoMemoryChars)
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
