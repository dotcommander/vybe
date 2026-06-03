package commands

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/dotcommander/vybe/internal/actions"
	"github.com/dotcommander/vybe/internal/commands/hookcmd"
	"github.com/dotcommander/vybe/internal/models"
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

// --- Phase A: Hook deduplication tests ---
// These verify CURRENT behavior and must pass before Phase D refactors the code.

// TestCheckpointAndSessionEndSharePath verifies that runCheckpoint is the shared
// execution path for both the checkpoint and session-end hook handlers.
// A real DB is used; success means runCheckpoint ran without panic for both.
func TestCheckpointAndSessionEndSharePath(t *testing.T) {
	_, dbPath := initTestDB(t)
	t.Setenv("VYBE_DB_PATH", dbPath)

	hctx := hookContext{
		Input:     hookInput{SessionID: "sess-share-path", HookEventName: "PreCompact"},
		AgentName: "test-agent-share",
		CWD:       t.TempDir(),
	}

	// Checkpoint path: random request ID.
	checkpointReqID := hookRequestID("checkpoint", hctx.AgentName)
	{
		db, closeDB, err := openDB()
		require.NoError(t, err, "openDB must succeed for checkpoint path")
		defer closeDB()
		runCheckpoint(db, hctx, checkpointReqID) // must not panic
	}

	// Session-end path: stable (session-scoped) request ID.
	sessionEndReqID := stableHookRequestID("session_end", hctx.AgentName, hctx.Input.SessionID)
	{
		db, closeDB, err := openDB()
		require.NoError(t, err, "openDB must succeed for session-end path")
		defer closeDB()
		runCheckpoint(db, hctx, sessionEndReqID) // must not panic
	}

	// The two strategies must produce different IDs for the same invocation context.
	require.NotEqual(t, checkpointReqID, sessionEndReqID,
		"checkpoint (random) and session-end (stable) request IDs must differ")
}

// TestCheckpointUsesRandomReqID verifies the checkpoint request-ID strategy produces
// different IDs on each invocation — no idempotency collision across separate runs.
func TestCheckpointUsesRandomReqID(t *testing.T) {
	t.Parallel()

	id1 := hookRequestID("checkpoint", "claude")
	id2 := hookRequestID("checkpoint", "claude")

	require.Contains(t, id1, "hook_checkpoint_claude_",
		"checkpoint request ID must include expected prefix")
	require.NotEqual(t, id1, id2,
		"two checkpoint invocations must produce different request IDs (random suffix)")
}

// TestSessionEndUsesStableReqID verifies the session-end request-ID strategy produces
// the same ID for the same session — retried session-end hooks are idempotent.
func TestSessionEndUsesStableReqID(t *testing.T) {
	t.Parallel()

	sessionID := "sess-stable-test-abc123"

	id1 := stableHookRequestID("session_end", "claude", sessionID)
	id2 := stableHookRequestID("session_end", "claude", sessionID)

	require.Equal(t, id1, id2,
		"two session-end invocations with the same session_id must produce identical request IDs")
	require.Contains(t, id1, "hook_session_end_claude_",
		"session-end request ID must include expected prefix")
	require.Contains(t, id1, "sess-stable-test-abc123",
		"session-end request ID must embed the session ID")
}

// --- Phase A: UserPromptSubmit reminder tests ---

// TestPromptNonTriggerNoStdout asserts that a non-trigger prompt produces no stdout.
// EXPECTED TO FAIL in Phase A: per-turn reminder at hook.go:211-239 still emits stdout.
// Passes after Phase D-2 removes the reminder block.
func TestPromptNonTriggerNoStdout(t *testing.T) {
	db, dbPath := initTestDB(t)
	t.Setenv("VYBE_DB_PATH", dbPath)
	t.Setenv("VYBE_AGENT", "test-agent-nontrigger")

	// Create a task and set it as the agent's focus so the reminder fires.
	task, _, err := actions.TaskCreateIdempotent(db, "test-agent-nontrigger",
		"req-nontrigger-task-1", "Focus task for reminder test", "", "", 0)
	require.NoError(t, err)
	require.NotEmpty(t, task.ID)

	_, err = store.LoadOrCreateAgentState(db, "test-agent-nontrigger")
	require.NoError(t, err)
	_, err = store.SetAgentFocusTaskWithEventIdempotent(db,
		"test-agent-nontrigger", "req-nontrigger-focus-1", task.ID)
	require.NoError(t, err)

	payload := hookStdinPayload("sess-nontrigger", t.TempDir(), "what is the weather today?")
	cmd := newHookPromptCmd()

	hookTestStdinMu.Lock()
	out := captureStdout(t, func() {
		withHookStdin(t, payload, func() {
			_ = cmd.RunE(cmd, nil)
		})
	})
	hookTestStdinMu.Unlock()

	// EXPECTED TO FAIL in Phase A: reminder still emits stdout.
	// After Phase D-2 removes hook.go:211-239, this assertion passes.
	require.Empty(t, strings.TrimSpace(out),
		"non-trigger prompt must not emit stdout (Phase D-2: remove per-turn reminder)")
}

// TestPromptEventLogged asserts that a non-trigger prompt causes a user_prompt event
// to be recorded in the DB. Must PASS in Phase A — event logging is already present.
func TestPromptEventLogged(t *testing.T) {
	_, dbPath := initTestDB(t)
	t.Setenv("VYBE_DB_PATH", dbPath)
	t.Setenv("VYBE_AGENT", "test-agent-eventlog")

	payload := hookStdinPayload("sess-eventlog", t.TempDir(), "just a regular question")
	cmd := newHookPromptCmd()

	hookTestStdinMu.Lock()
	withHookStdin(t, payload, func() {
		_ = cmd.RunE(cmd, nil)
	})
	hookTestStdinMu.Unlock()

	// Open the DB independently to verify the event was written.
	db2, err := store.InitDBWithPath(dbPath)
	require.NoError(t, err)
	defer func() { _ = db2.Close() }()

	events, err := store.ListEvents(db2, store.ListEventsParams{
		AgentName: "test-agent-eventlog",
		Kind:      models.EventKindUserPrompt,
		Limit:     10,
	})
	require.NoError(t, err)
	require.NotEmpty(t, events,
		"user_prompt event must be recorded in DB after hook prompt runs")
}

// TestPromptTriggerEmitsRichBrief asserts that the trigger word "brief me" causes
// the hook to emit non-empty JSON hookOutput on stdout. Must PASS in Phase A.
func TestPromptTriggerEmitsRichBrief(t *testing.T) {
	db, dbPath := initTestDB(t)
	t.Setenv("VYBE_DB_PATH", dbPath)
	t.Setenv("VYBE_AGENT", "test-agent-trigger")

	// Create a task so the brief has content.
	_, _, err := actions.TaskCreateIdempotent(db, "test-agent-trigger",
		"req-trigger-task-1", "Task for trigger brief test", "", "", 0)
	require.NoError(t, err)

	payload := hookStdinPayload("sess-trigger", t.TempDir(), "brief me")
	cmd := newHookPromptCmd()

	var out string
	hookTestStdinMu.Lock()
	out = captureStdout(t, func() {
		withHookStdin(t, payload, func() {
			_ = cmd.RunE(cmd, nil)
		})
	})
	hookTestStdinMu.Unlock()

	trimmed := strings.TrimSpace(out)
	require.NotEmpty(t, trimmed,
		"trigger prompt 'brief me' must emit non-empty output on stdout")

	var hookOut map[string]any
	require.NoError(t, json.Unmarshal([]byte(trimmed), &hookOut),
		"trigger prompt output must be valid JSON: %s", trimmed)
	_, hasHookSpecific := hookOut["hookSpecificOutput"]
	require.True(t, hasHookSpecific,
		"trigger output must contain hookSpecificOutput field: %s", trimmed)
}

// TestHookPoliciesMatchLegacyWrapperChoice locks the hookPolicy table to the
// per-call-site DB-wrapper choice that existed before the data-driven refactor.
// swallowErrors=true  corresponds to the old withDBSilent call sites.
// swallowErrors=false corresponds to the old withDB call sites.
// If a future edit flips any flag, this test fails — that is intentional:
// which hooks swallow vs propagate is observable behavior and must not drift.
func TestHookPoliciesMatchLegacyWrapperChoice(t *testing.T) {
	t.Parallel()

	want := map[string]bool{
		"session-start-compact": true,  // was withDBSilent
		"prompt":                true,  // was withDBSilent
		"session-start-resume":  false, // was withDB
		"tool-failure":          false, // was withDB
		"task-completed":        false, // was withDB
		"maintenance":           false, // was withDB
	}

	require.Len(t, hookPolicies, len(want),
		"hookPolicies must list exactly the known hook db-call-sites")

	for name, swallow := range want {
		got, ok := hookPolicies[name]
		require.True(t, ok, "missing policy for hook db-call-site %q", name)
		require.Equal(t, swallow, got.swallowErrors,
			"swallowErrors for %q must match legacy wrapper choice", name)
	}
}
