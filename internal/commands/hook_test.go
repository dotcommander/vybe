package commands

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/dotcommander/vybe/internal/actions"
	"github.com/dotcommander/vybe/internal/models"
	"github.com/dotcommander/vybe/internal/store"
	"github.com/stretchr/testify/require"
)

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
	ev := CanonicalEvent{SessionID: "sess-share-path", HostEventName: "PreCompact"}

	// Checkpoint path: random request ID.
	checkpointReqID := hookRequestID("checkpoint", hctx.AgentName)
	{
		db, closeDB, err := openDB()
		require.NoError(t, err, "openDB must succeed for checkpoint path")
		defer closeDB()
		runCheckpoint(db, ev, hctx, checkpointReqID) // must not panic
	}

	// Session-end path: stable (session-scoped) request ID.
	sessionEndReqID := stableHookRequestID("session_end", hctx.AgentName, hctx.Input.SessionID)
	{
		db, closeDB, err := openDB()
		require.NoError(t, err, "openDB must succeed for session-end path")
		defer closeDB()
		runCheckpoint(db, ev, hctx, sessionEndReqID) // must not panic
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
