package store

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func setupTestDB(t *testing.T) (*sql.DB, func()) {
	t.Helper()

	tempDir := t.TempDir()
	testDBPath := tempDir + "/test.db"

	db, err := InitDBWithPath(testDBPath)
	if err != nil {
		t.Fatalf("Failed to initialize test database: %v", err)
	}

	cleanup := func() {
		_ = db.Close()
	}

	return db, cleanup
}

func TestAppendEvent(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	eventID := appendEvent(t, db, "test.event", "test-agent", "task-123", "Test message")

	if eventID <= 0 {
		t.Errorf("Expected positive event ID, got %d", eventID)
	}

	// Verify event was stored
	var kind, agentName, taskID, message string
	err := db.QueryRow("SELECT kind, agent_name, task_id, message FROM events WHERE id = ?", eventID).
		Scan(&kind, &agentName, &taskID, &message)
	if err != nil {
		t.Fatalf("Failed to query event: %v", err)
	}

	if kind != "test.event" {
		t.Errorf("Expected kind=test.event, got %s", kind)
	}
	if agentName != "test-agent" {
		t.Errorf("Expected agentName=test-agent, got %s", agentName)
	}
	if taskID != "task-123" {
		t.Errorf("Expected taskID=task-123, got %s", taskID)
	}
	if message != "Test message" {
		t.Errorf("Expected message='Test message', got %s", message)
	}
}

func TestAppendEventIdempotent_Replay(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()
	var err error

	agent := "agent1"
	req := "req_1"

	id1, err := AppendEventIdempotent(db, agent, req, "test.event", "task-1", "hello")
	require.NoError(t, err)
	id2, err := AppendEventIdempotent(db, agent, req, "test.event", "task-1", "hello")
	require.NoError(t, err)
	require.Equal(t, id1, id2)

	var cnt int
	err = db.QueryRow(`SELECT COUNT(*) FROM events WHERE agent_name = ? AND kind = ?`, agent, "test.event").Scan(&cnt)
	require.NoError(t, err)
	require.Equal(t, 1, cnt)
}

func TestAppendEventWithMetadata(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	metadata := `{"key": "value"}`
	eventID := appendEventWithMetadata(t, db, "test.event", "test-agent", "task-123", "Test message", metadata)

	if eventID <= 0 {
		t.Errorf("Expected positive event ID, got %d", eventID)
	}

	// Verify metadata was stored
	var storedMetadata string
	err := db.QueryRow("SELECT metadata FROM events WHERE id = ?", eventID).Scan(&storedMetadata)
	if err != nil {
		t.Fatalf("Failed to query event metadata: %v", err)
	}

	if storedMetadata != metadata {
		t.Errorf("Expected metadata=%s, got %s", metadata, storedMetadata)
	}
}

func TestListEvents_MetadataIsNativeJSON(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	metadata := `{"foo":"bar","count":2}`
	_ = appendEventWithMetadata(t, db, "test.event", "test-agent", "task-123", "Test message", metadata)
	var err error
	require.NoError(t, err)

	events, err := ListEvents(db, ListEventsParams{AgentName: "test-agent", Limit: 10})
	require.NoError(t, err)
	require.Len(t, events, 1)

	payload, err := json.Marshal(events[0])
	require.NoError(t, err)

	jsonOutput := string(payload)
	require.False(t, strings.Contains(jsonOutput, `"metadata":"{`), "metadata should not be double-encoded")

	var parsed map[string]any
	err = json.Unmarshal(payload, &parsed)
	require.NoError(t, err)

	meta, ok := parsed["metadata"].(map[string]any)
	require.True(t, ok, "metadata should decode as JSON object")
	require.Equal(t, "bar", meta["foo"])
	require.Equal(t, float64(2), meta["count"])
}

func TestFetchEventsSince_MetadataIsNativeJSON(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	metadata := `{"nested":{"ok":true}}`
	_ = appendEventWithMetadata(t, db, "test.event", "test-agent", "task-123", "Test message", metadata)
	var err error
	require.NoError(t, err)

	events, err := FetchEventsSince(db, 0, 10, "")
	require.NoError(t, err)
	require.Len(t, events, 1)

	payload, err := json.Marshal(events[0])
	require.NoError(t, err)

	var parsed map[string]any
	err = json.Unmarshal(payload, &parsed)
	require.NoError(t, err)

	meta, ok := parsed["metadata"].(map[string]any)
	require.True(t, ok, "metadata should decode as JSON object")
	nested, ok := meta["nested"].(map[string]any)
	require.True(t, ok, "nested metadata should decode as JSON object")
	require.Equal(t, true, nested["ok"])
}

func TestArchiveEventsRangeWithSummaryIdempotent(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	appendEvent(t, db, "task.note", "agent1", "task-1", "event one")
	appendEvent(t, db, "task.note", "agent1", "task-1", "event two")
	appendEvent(t, db, "task.note", "agent1", "task-1", "event three")

	summaryEventID, archivedCount, err := ArchiveEventsRangeWithSummaryIdempotent(db, "agent1", "req-archive-1", "", "task-1", 1, 2, "Compressed old events")
	require.NoError(t, err)
	require.Greater(t, summaryEventID, int64(0))
	require.Equal(t, int64(2), archivedCount)

	// Idempotent replay should not create a second summary event.
	replayedEventID, replayedCount, err := ArchiveEventsRangeWithSummaryIdempotent(db, "agent1", "req-archive-1", "", "task-1", 1, 2, "Compressed old events")
	require.NoError(t, err)
	require.Equal(t, summaryEventID, replayedEventID)
	require.Equal(t, archivedCount, replayedCount)

	visible, err := ListEvents(db, ListEventsParams{AgentName: "agent1", Limit: 20})
	require.NoError(t, err)
	require.Len(t, visible, 2)
	require.Equal(t, "event three", visible[0].Message)
	require.Equal(t, "events_summary", visible[1].Kind)

	all, err := ListEvents(db, ListEventsParams{AgentName: "agent1", Limit: 20, IncludeArchived: true})
	require.NoError(t, err)
	require.Len(t, all, 4)

	var summaryCount int
	err = db.QueryRow(`SELECT COUNT(*) FROM events WHERE kind = 'events_summary'`).Scan(&summaryCount)
	require.NoError(t, err)
	require.Equal(t, 1, summaryCount)
}

func TestFetchEventsSince_ExcludesArchived(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	appendEvent(t, db, "task.note", "agent1", "task-1", "event one")
	appendEvent(t, db, "task.note", "agent1", "task-1", "event two")

	_, _, err := ArchiveEventsRangeWithSummaryIdempotent(db, "agent1", "req-archive-2", "", "task-1", 1, 1, "Compressed oldest")
	require.NoError(t, err)

	events, err := FetchEventsSince(db, 0, 20, "")
	require.NoError(t, err)
	require.Len(t, events, 2)
	require.Equal(t, "event two", events[0].Message)
	require.Equal(t, "events_summary", events[1].Kind)
}

func TestCountActiveEvents(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	// Empty DB
	count, err := CountActiveEvents(db, "")
	require.NoError(t, err)
	require.Equal(t, int64(0), count)

	// Add events
	appendEvent(t, db, "note", "agent1", "", "event one")
	appendEvent(t, db, "note", "agent1", "", "event two")
	appendEvent(t, db, "note", "agent1", "", "event three")

	count, err = CountActiveEvents(db, "")
	require.NoError(t, err)
	require.Equal(t, int64(3), count)

	// Archive one event
	_, _, err = ArchiveEventsRangeWithSummaryIdempotent(db, "agent1", "req-cnt-1", "", "", 1, 1, "compressed")
	require.NoError(t, err)

	// Should count 3 active (event two + event three + summary event), not the archived one
	count, err = CountActiveEvents(db, "")
	require.NoError(t, err)
	require.Equal(t, int64(3), count)
}

func TestFindArchiveWindow(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	// Empty DB
	fromID, toID, err := FindArchiveWindow(db, "", 5)
	require.NoError(t, err)
	require.Equal(t, int64(0), fromID)
	require.Equal(t, int64(0), toID)

	// Add 5 events
	for i := range 5 {
		appendEvent(t, db, "note", "agent1", "", fmt.Sprintf("event %d", i+1))
	}

	// Keep 3 recent: should archive events 1-2
	fromID, toID, err = FindArchiveWindow(db, "", 3)
	require.NoError(t, err)
	require.Equal(t, int64(1), fromID)
	require.Equal(t, int64(2), toID)

	// Keep all: should return 0,0
	fromID, toID, err = FindArchiveWindow(db, "", 5)
	require.NoError(t, err)
	require.Equal(t, int64(0), fromID)
	require.Equal(t, int64(0), toID)

	// Keep more than available: should return 0,0
	fromID, toID, err = FindArchiveWindow(db, "", 100)
	require.NoError(t, err)
	require.Equal(t, int64(0), fromID)
	require.Equal(t, int64(0), toID)
}

func TestPruneArchivedEventsIdempotent(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	appendEvent(t, db, "note", "agent1", "", "event one")
	appendEvent(t, db, "note", "agent1", "", "event two")
	appendEvent(t, db, "note", "agent1", "", "event three")

	_, _, err := ArchiveEventsRangeWithSummaryIdempotent(db, "agent1", "req-prune-archive", "", "", 1, 2, "compressed")
	require.NoError(t, err)

	old := time.Now().Add(-45 * 24 * time.Hour).UTC().Format("2006-01-02 15:04:05")
	_, err = db.Exec(`UPDATE events SET archived_at = ? WHERE id IN (1,2)`, old)
	require.NoError(t, err)

	deleted, err := PruneArchivedEventsIdempotent(db, "agent1", "req-prune-1", "", 30, 10)
	require.NoError(t, err)
	require.Equal(t, int64(2), deleted)

	// Idempotent replay with same request ID returns same count.
	replayedDeleted, err := PruneArchivedEventsIdempotent(db, "agent1", "req-prune-1", "", 30, 10)
	require.NoError(t, err)
	require.Equal(t, deleted, replayedDeleted)

	var total int
	err = db.QueryRow(`SELECT COUNT(*) FROM events`).Scan(&total)
	require.NoError(t, err)
	// event 3 + summary event remain.
	require.Equal(t, 2, total)
}
