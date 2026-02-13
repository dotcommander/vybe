package actions

import (
	"strings"
	"testing"

	"github.com/dotcommander/vybe/internal/models"
	"github.com/dotcommander/vybe/internal/store"
	"github.com/stretchr/testify/require"
)

func TestSessionDigest_Empty(t *testing.T) {
	db, cleanup := setupTestDBWithCleanup(t)
	defer cleanup()

	result, err := SessionDigest(db, "test-agent")
	require.NoError(t, err)
	require.Equal(t, "test-agent", result.AgentName)
	require.Equal(t, 0, result.EventCount)
	require.Equal(t, int64(0), result.CursorEventID)
	require.Empty(t, result.EventsByKind)
}

func TestSessionDigest_WithEvents(t *testing.T) {
	db, cleanup := setupTestDBWithCleanup(t)
	defer cleanup()

	_, err := store.LoadOrCreateAgentState(db, "test-agent")
	require.NoError(t, err)

	_, err = store.AppendEvent(db, "user_prompt", "test-agent", "", "what is this?")
	require.NoError(t, err)
	_, err = store.AppendEvent(db, "progress", "test-agent", "", "working on it")
	require.NoError(t, err)
	_, err = store.AppendEvent(db, "tool_failure", "test-agent", "", "bash failed")
	require.NoError(t, err)

	result, err := SessionDigest(db, "test-agent")
	require.NoError(t, err)
	require.Equal(t, 3, result.EventCount)
	require.Len(t, result.Events, 3) // internal field still populated
}

func TestSessionDigest_CountsByKind(t *testing.T) {
	db, cleanup := setupTestDBWithCleanup(t)
	defer cleanup()

	_, err := store.LoadOrCreateAgentState(db, "test-agent")
	require.NoError(t, err)

	_, err = store.AppendEvent(db, "user_prompt", "test-agent", "", "prompt 1")
	require.NoError(t, err)
	_, err = store.AppendEvent(db, "user_prompt", "test-agent", "", "prompt 2")
	require.NoError(t, err)
	_, err = store.AppendEvent(db, "progress", "test-agent", "", "step done")
	require.NoError(t, err)

	result, err := SessionDigest(db, "test-agent")
	require.NoError(t, err)
	require.Equal(t, 2, result.EventsByKind["user_prompt"])
	require.Equal(t, 1, result.EventsByKind["progress"])
}

func TestSessionRetrospective_SkipsWhenCLIUnavailable(t *testing.T) {
	db, cleanup := setupTestDBWithCleanup(t)
	defer cleanup()

	// Use an agent name that maps to "opencode" CLI — which is unlikely to be in PATH
	// If opencode happens to be in PATH, the test still passes because we check the skip path
	_, err := store.LoadOrCreateAgentState(db, "opencode-test")
	require.NoError(t, err)

	// Insert enough events to pass the minimum threshold
	for range 5 {
		_, err = store.AppendEvent(db, "user_prompt", "opencode-test", "", "prompt")
		require.NoError(t, err)
	}

	// Clear PATH to ensure no CLI is found
	t.Setenv("PATH", t.TempDir())

	result, err := SessionRetrospective(db, "opencode-test", "retro_test")
	require.NoError(t, err)
	require.True(t, result.Skipped)
	require.Equal(t, "no lessons extracted", result.SkipReason)
}

func TestSessionRetrospective_SkipsWhenFewEvents(t *testing.T) {
	db, cleanup := setupTestDBWithCleanup(t)
	defer cleanup()

	_, err := store.LoadOrCreateAgentState(db, "test-agent")
	require.NoError(t, err)

	// Only 1 event — below minimum of 2
	_, err = store.AppendEvent(db, "user_prompt", "test-agent", "", "prompt 1")
	require.NoError(t, err)

	result, err := SessionRetrospective(db, "test-agent", "retro_test")
	require.NoError(t, err)
	require.True(t, result.Skipped)
	require.Contains(t, result.SkipReason, "insufficient events")
}

func TestAutoSummarizeEventsIdempotent(t *testing.T) {
	db, cleanup := setupTestDBWithCleanup(t)
	defer cleanup()

	_, err := store.LoadOrCreateAgentState(db, "test-agent")
	require.NoError(t, err)

	// Below threshold — no-op
	for range 5 {
		_, err = store.AppendEvent(db, "note", "test-agent", "", "event")
		require.NoError(t, err)
	}

	summaryID, archived, err := AutoSummarizeEventsIdempotent(db, "test-agent", "req-sum-1", "", 200, 50)
	require.NoError(t, err)
	require.Equal(t, int64(0), summaryID)
	require.Equal(t, int64(0), archived)
}

func TestExtractRuleBasedLessons_RepeatedToolFailure(t *testing.T) {
	events := []*models.Event{
		{Kind: "tool_failure", Message: "Bash failed"},
		{Kind: "user_prompt", Message: "try again"},
		{Kind: "tool_failure", Message: "Bash failed (PostToolUseFailure)"},
	}

	lessons := extractRuleBasedLessons(events)
	require.GreaterOrEqual(t, len(lessons), 1)

	found := false
	for _, l := range lessons {
		if l.Type == "correction" && strings.Contains(l.Key, "bash") {
			found = true
			break
		}
	}
	require.True(t, found, "expected correction lesson for repeated Bash failures")
}

func TestExtractRuleBasedLessons_TaskCompleted(t *testing.T) {
	events := []*models.Event{
		{Kind: "user_prompt", Message: "do the thing"},
		{Kind: "task_status", Message: "Status changed to completed"},
	}

	lessons := extractRuleBasedLessons(events)
	require.GreaterOrEqual(t, len(lessons), 1)

	found := false
	for _, l := range lessons {
		if l.Type == "knowledge" && l.Key == "task_completion_observed" {
			found = true
			break
		}
	}
	require.True(t, found, "expected knowledge lesson for task completion")
}

func TestExtractRuleBasedLessons_NoPatterns(t *testing.T) {
	events := []*models.Event{
		{Kind: "user_prompt", Message: "hello"},
		{Kind: "progress", Message: "working"},
	}

	lessons := extractRuleBasedLessons(events)
	require.Empty(t, lessons)
}

func TestPersistLessons(t *testing.T) {
	db, cleanup := setupTestDBWithCleanup(t)
	defer cleanup()

	lessons := []Lesson{
		{Type: "correction", Key: "test_lesson", Value: "test value", Scope: "global"},
	}

	eventIDs, err := persistLessons(db, "test-agent", "req_persist", "", lessons)
	require.NoError(t, err)
	require.Len(t, eventIDs, 1)
}

func TestPersistLessons_Batch(t *testing.T) {
	db, cleanup := setupTestDBWithCleanup(t)
	defer cleanup()

	lessons := []Lesson{
		{Type: "correction", Key: "lesson_1", Value: "value 1", Scope: "global"},
		{Type: "preference", Key: "lesson_2", Value: "value 2", Scope: "global"},
		{Type: "pattern", Key: "lesson_3", Value: "value 3", Scope: "global"},
		{Type: "knowledge", Key: "lesson_4", Value: "value 4", Scope: "global"},
	}

	eventIDs, err := persistLessons(db, "test-agent", "req_batch", "", lessons)
	require.NoError(t, err)
	require.Len(t, eventIDs, 4, "all 4 lessons should be persisted")

	// Verify all lessons exist in memory
	mem1, err := store.GetMemory(db, "lesson_1", "global", "")
	require.NoError(t, err)
	require.NotNil(t, mem1)
	require.Equal(t, "value 1", mem1.Value)

	mem2, err := store.GetMemory(db, "lesson_2", "global", "")
	require.NoError(t, err)
	require.NotNil(t, mem2)
	require.Equal(t, "value 2", mem2.Value)
}

func TestPersistLessons_Idempotent(t *testing.T) {
	db, cleanup := setupTestDBWithCleanup(t)
	defer cleanup()

	lessons := []Lesson{
		{Type: "correction", Key: "lesson_idem", Value: "value", Scope: "global"},
	}

	// First call
	eventIDs1, err := persistLessons(db, "test-agent", "req_idem", "", lessons)
	require.NoError(t, err)
	require.Len(t, eventIDs1, 1)

	// Second call with same request ID should return same result
	eventIDs2, err := persistLessons(db, "test-agent", "req_idem", "", lessons)
	require.NoError(t, err)
	require.Len(t, eventIDs2, 1)
	require.Equal(t, eventIDs1[0], eventIDs2[0], "idempotent call should return same event ID")
}

func TestPersistLessons_SkipsEmptyKeys(t *testing.T) {
	db, cleanup := setupTestDBWithCleanup(t)
	defer cleanup()

	lessons := []Lesson{
		{Type: "correction", Key: "", Value: "no key", Scope: "global"},
		{Type: "preference", Key: "valid_key", Value: "has key", Scope: "global"},
	}

	eventIDs, err := persistLessons(db, "test-agent", "req_skip", "", lessons)
	require.NoError(t, err)
	require.Len(t, eventIDs, 1, "only lesson with valid key should be persisted")
}
