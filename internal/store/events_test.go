package store

import (
	"database/sql"
	"encoding/json"
	"strings"
	"testing"

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
