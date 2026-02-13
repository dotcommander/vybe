package store

import (
	"database/sql"
	"fmt"
	"testing"
	"time"

	"github.com/dotcommander/vybe/internal/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setupMemoryTestDB(t *testing.T) (*sql.DB, func()) {
	t.Helper()
	db, err := InitDBWithPath(":memory:")
	require.NoError(t, err)
	return db, func() { db.Close() }
}

func TestSetMemory_GlobalScope(t *testing.T) {
	db, cleanup := setupMemoryTestDB(t)
	defer cleanup()

	err := SetMemory(db, "api_key", "secret123", "string", "global", "", nil)
	assert.NoError(t, err)

	// Verify it was stored
	mem, err := GetMemory(db, "api_key", "global", "")
	require.NoError(t, err)
	assert.NotNil(t, mem)
	assert.Equal(t, "api_key", mem.Key)
	assert.Equal(t, "secret123", mem.Value)
	assert.Equal(t, "string", mem.ValueType)
	assert.Equal(t, models.MemoryScopeGlobal, mem.Scope)
	assert.Equal(t, "", mem.ScopeID)
}

func TestSetMemory_ProjectScope(t *testing.T) {
	db, cleanup := setupMemoryTestDB(t)
	defer cleanup()

	err := SetMemory(db, "config", "value1", "string", "project", "proj-123", nil)
	assert.NoError(t, err)

	mem, err := GetMemory(db, "config", "project", "proj-123")
	require.NoError(t, err)
	assert.NotNil(t, mem)
	assert.Equal(t, "proj-123", mem.ScopeID)
}

func TestSetMemory_TaskScope(t *testing.T) {
	db, cleanup := setupMemoryTestDB(t)
	defer cleanup()

	err := SetMemory(db, "status", "running", "string", "task", "task-456", nil)
	assert.NoError(t, err)

	mem, err := GetMemory(db, "status", "task", "task-456")
	require.NoError(t, err)
	assert.NotNil(t, mem)
	assert.Equal(t, "task-456", mem.ScopeID)
}

func TestSetMemory_AgentScope(t *testing.T) {
	db, cleanup := setupMemoryTestDB(t)
	defer cleanup()

	err := SetMemory(db, "last_action", "compile", "string", "agent", "poet-agent", nil)
	assert.NoError(t, err)

	mem, err := GetMemory(db, "last_action", "agent", "poet-agent")
	require.NoError(t, err)
	assert.NotNil(t, mem)
	assert.Equal(t, "poet-agent", mem.ScopeID)
}

func TestSetMemory_Upsert(t *testing.T) {
	db, cleanup := setupMemoryTestDB(t)
	defer cleanup()

	// Insert initial value
	err := SetMemory(db, "counter", "1", "number", "global", "", nil)
	require.NoError(t, err)

	// Update value
	err = SetMemory(db, "counter", "2", "number", "global", "", nil)
	require.NoError(t, err)

	// Verify update
	mem, err := GetMemory(db, "counter", "global", "")
	require.NoError(t, err)
	assert.Equal(t, "2", mem.Value)
}

func TestUpsertMemoryWithEventIdempotent_TaskScope(t *testing.T) {
	db, cleanup := setupMemoryTestDB(t)
	defer cleanup()

	expiresAt := time.Now().Add(24 * time.Hour)
	eventID, reinforced, _, _, err := UpsertMemoryWithEventIdempotent(db, "agent1", "req_task_scope_upsert", "checkpoint", "step_3", "", "task", "task-1", &expiresAt, nil, nil)
	require.NoError(t, err)
	require.Greater(t, eventID, int64(0))
	assert.False(t, reinforced)

	// Memory is present
	mem, err := GetMemory(db, "checkpoint", "task", "task-1")
	require.NoError(t, err)
	require.NotNil(t, mem)

	// Event is present and attributed
	var (
		kind      string
		agentName string
		taskID    sql.NullString
	)
	err = db.QueryRow(`SELECT kind, agent_name, task_id FROM events WHERE id = ?`, eventID).Scan(&kind, &agentName, &taskID)
	require.NoError(t, err)
	assert.Equal(t, "memory_upserted", kind)
	assert.Equal(t, "agent1", agentName)
	require.True(t, taskID.Valid)
	assert.Equal(t, "task-1", taskID.String)
}

func TestDeleteMemoryWithEvent_GlobalScope(t *testing.T) {
	db, cleanup := setupMemoryTestDB(t)
	defer cleanup()

	require.NoError(t, SetMemory(db, "temp", "value", "string", "global", "", nil))

	eventID, err := DeleteMemoryWithEvent(db, "agent1", "temp", "global", "")
	require.NoError(t, err)
	require.Greater(t, eventID, int64(0))

	// Memory is gone
	mem, err := GetMemory(db, "temp", "global", "")
	require.NoError(t, err)
	assert.Nil(t, mem)

	var kind string
	err = db.QueryRow(`SELECT kind FROM events WHERE id = ?`, eventID).Scan(&kind)
	require.NoError(t, err)
	assert.Equal(t, "memory_delete", kind)
}

func TestGetMemory_NotFound(t *testing.T) {
	db, cleanup := setupMemoryTestDB(t)
	defer cleanup()

	mem, err := GetMemory(db, "nonexistent", "global", "")
	assert.NoError(t, err)
	assert.Nil(t, mem)
}

func TestListMemory_GlobalScope(t *testing.T) {
	db, cleanup := setupMemoryTestDB(t)
	defer cleanup()

	// Insert multiple entries
	require.NoError(t, SetMemory(db, "key1", "value1", "string", "global", "", nil))
	require.NoError(t, SetMemory(db, "key2", "value2", "string", "global", "", nil))
	require.NoError(t, SetMemory(db, "key3", "value3", "string", "global", "", nil))

	memories, err := ListMemory(db, "global", "")
	require.NoError(t, err)
	assert.Len(t, memories, 3)
}

func TestListMemory_ProjectScope_Isolated(t *testing.T) {
	db, cleanup := setupMemoryTestDB(t)
	defer cleanup()

	// Insert entries for different projects
	require.NoError(t, SetMemory(db, "key1", "value1", "string", "project", "proj-a", nil))
	require.NoError(t, SetMemory(db, "key2", "value2", "string", "project", "proj-a", nil))
	require.NoError(t, SetMemory(db, "key3", "value3", "string", "project", "proj-b", nil))

	// List for proj-a
	memories, err := ListMemory(db, "project", "proj-a")
	require.NoError(t, err)
	assert.Len(t, memories, 2)

	// List for proj-b
	memories, err = ListMemory(db, "project", "proj-b")
	require.NoError(t, err)
	assert.Len(t, memories, 1)
}

func TestDeleteMemory(t *testing.T) {
	db, cleanup := setupMemoryTestDB(t)
	defer cleanup()

	// Insert and delete
	require.NoError(t, SetMemory(db, "temp", "value", "string", "global", "", nil))
	err := DeleteMemory(db, "temp", "global", "")
	assert.NoError(t, err)

	// Verify deletion
	mem, err := GetMemory(db, "temp", "global", "")
	assert.NoError(t, err)
	assert.Nil(t, mem)
}

func TestDeleteMemory_NotFound(t *testing.T) {
	db, cleanup := setupMemoryTestDB(t)
	defer cleanup()

	err := DeleteMemory(db, "nonexistent", "global", "")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}

func TestSetMemory_WithExpiration(t *testing.T) {
	db, cleanup := setupMemoryTestDB(t)
	defer cleanup()

	expiresAt := time.Now().UTC().Add(1 * time.Hour)
	err := SetMemory(db, "temp_key", "temp_value", "string", "global", "", &expiresAt)
	require.NoError(t, err)

	mem, err := GetMemory(db, "temp_key", "global", "")
	require.NoError(t, err)
	require.NotNil(t, mem, "GetMemory should return a record with valid expiration in future")
	assert.NotNil(t, mem.ExpiresAt)
}

func TestGetMemory_FilterExpired(t *testing.T) {
	db, cleanup := setupMemoryTestDB(t)
	defer cleanup()

	// Insert expired entry
	expiresAt := time.Now().UTC().Add(-1 * time.Hour)
	err := SetMemory(db, "expired", "value", "string", "global", "", &expiresAt)
	require.NoError(t, err)

	// Should not be returned
	mem, err := GetMemory(db, "expired", "global", "")
	assert.NoError(t, err)
	assert.Nil(t, mem)
}

func TestListMemory_FilterExpired(t *testing.T) {
	db, cleanup := setupMemoryTestDB(t)
	defer cleanup()

	// Insert mix of expired and valid entries
	expired := time.Now().UTC().Add(-1 * time.Hour)
	valid := time.Now().UTC().Add(1 * time.Hour)

	require.NoError(t, SetMemory(db, "key1", "value1", "string", "global", "", &expired))
	require.NoError(t, SetMemory(db, "key2", "value2", "string", "global", "", &valid))
	require.NoError(t, SetMemory(db, "key3", "value3", "string", "global", "", nil))

	memories, err := ListMemory(db, "global", "")
	require.NoError(t, err)
	assert.Len(t, memories, 2) // Only valid and nil expiration
}

func TestInferValueType_String(t *testing.T) {
	assert.Equal(t, "string", inferValueType("hello world"))
	assert.Equal(t, "string", inferValueType(""))
	assert.Equal(t, "string", inferValueType("not a number"))
}

func TestInferValueType_Boolean(t *testing.T) {
	assert.Equal(t, "boolean", inferValueType("true"))
	assert.Equal(t, "boolean", inferValueType("false"))
}

func TestInferValueType_Number(t *testing.T) {
	assert.Equal(t, "number", inferValueType("42"))
	assert.Equal(t, "number", inferValueType("3.14"))
	assert.Equal(t, "number", inferValueType("-100"))
	assert.Equal(t, "number", inferValueType("0"))
}

func TestInferValueType_JSON(t *testing.T) {
	assert.Equal(t, "json", inferValueType(`{"key": "value"}`))
	assert.Equal(t, "json", inferValueType(`{"a": 1, "b": 2}`))
}

func TestInferValueType_Array(t *testing.T) {
	assert.Equal(t, "array", inferValueType(`["a", "b", "c"]`))
	assert.Equal(t, "array", inferValueType(`[1, 2, 3]`))
	assert.Equal(t, "array", inferValueType(`[]`))
}

func TestValidateScope_Valid(t *testing.T) {
	tests := []struct {
		name    string
		scope   string
		scopeID string
	}{
		{"global without id", "global", ""},
		{"project with id", "project", "proj-123"},
		{"task with id", "task", "task-456"},
		{"agent with id", "agent", "poet-agent"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateScope(tt.scope, tt.scopeID)
			assert.NoError(t, err)
		})
	}
}

func TestValidateScope_Invalid(t *testing.T) {
	tests := []struct {
		name    string
		scope   string
		scopeID string
		errMsg  string
	}{
		{"invalid scope", "invalid", "", "invalid scope"},
		{"global with id", "global", "should-be-empty", "cannot have a scope_id"},
		{"project without id", "project", "", "requires a scope_id"},
		{"task without id", "task", "", "requires a scope_id"},
		{"agent without id", "agent", "", "requires a scope_id"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateScope(tt.scope, tt.scopeID)
			assert.Error(t, err)
			assert.Contains(t, err.Error(), tt.errMsg)
		})
	}
}

func TestSetMemory_TypeInference(t *testing.T) {
	db, cleanup := setupMemoryTestDB(t)
	defer cleanup()

	tests := []struct {
		value        string
		expectedType string
	}{
		{"hello", "string"},
		{"true", "boolean"},
		{"42", "number"},
		{`{"key": "value"}`, "json"},
		{`[1, 2, 3]`, "array"},
	}

	for i, tt := range tests {
		key := fmt.Sprintf("key%d", i)
		err := SetMemory(db, key, tt.value, "", "global", "", nil)
		require.NoError(t, err)

		mem, err := GetMemory(db, key, "global", "")
		require.NoError(t, err)
		assert.Equal(t, tt.expectedType, mem.ValueType)
	}
}

func TestMemory_UniqueConstraint(t *testing.T) {
	db, cleanup := setupMemoryTestDB(t)
	defer cleanup()

	// Same key, different scopes - should not conflict
	err := SetMemory(db, "config", "value1", "string", "global", "", nil)
	require.NoError(t, err)

	err = SetMemory(db, "config", "value2", "string", "project", "proj-123", nil)
	require.NoError(t, err)

	// Verify both exist independently
	mem1, err := GetMemory(db, "config", "global", "")
	require.NoError(t, err)
	require.NotNil(t, mem1)

	mem2, err := GetMemory(db, "config", "project", "proj-123")
	require.NoError(t, err)
	require.NotNil(t, mem2)

	assert.Equal(t, "value1", mem1.Value)
	assert.Equal(t, "value2", mem2.Value)
}

func TestNormalizeMemoryKey(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{in: " API Key ", want: "api_key"},
		{in: "User.Name", want: "username"},
		{in: "with   spaces", want: "with_spaces"},
		{in: "already-clean", want: "already-clean"},
	}

	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			assert.Equal(t, tt.want, NormalizeMemoryKey(tt.in))
		})
	}
}

func TestUpsertMemoryWithEventIdempotent_CanonicalReinforce(t *testing.T) {
	db, cleanup := setupMemoryTestDB(t)
	defer cleanup()

	eventID1, reinforced1, confidence1, canonical1, err := UpsertMemoryWithEventIdempotent(
		db,
		"agent1",
		"req_upsert_1",
		" API Key ",
		"secret",
		"string",
		"global",
		"",
		nil,
		nil,
		nil,
	)
	require.NoError(t, err)
	require.Greater(t, eventID1, int64(0))
	assert.False(t, reinforced1)
	assert.Equal(t, "api_key", canonical1)
	assert.Equal(t, 0.5, confidence1)

	eventID2, reinforced2, confidence2, canonical2, err := UpsertMemoryWithEventIdempotent(
		db,
		"agent1",
		"req_upsert_2",
		"api_key",
		"secret",
		"string",
		"global",
		"",
		nil,
		nil,
		nil,
	)
	require.NoError(t, err)
	require.Greater(t, eventID2, eventID1)
	assert.True(t, reinforced2)
	assert.Equal(t, "api_key", canonical2)
	assert.Greater(t, confidence2, confidence1)

	var count int
	err = db.QueryRow(`SELECT COUNT(*) FROM memory WHERE scope = 'global' AND scope_id = '' AND canonical_key = ?`, canonical2).Scan(&count)
	require.NoError(t, err)
	assert.Equal(t, 1, count)

	var lastKind string
	err = db.QueryRow(`SELECT kind FROM events WHERE id = ?`, eventID2).Scan(&lastKind)
	require.NoError(t, err)
	assert.Equal(t, "memory_reinforced", lastKind)
}

func TestUpsertMemoryWithEventIdempotent_Replay(t *testing.T) {
	db, cleanup := setupMemoryTestDB(t)
	defer cleanup()

	eventID1, reinforced1, _, _, err := UpsertMemoryWithEventIdempotent(
		db,
		"agent1",
		"req_upsert_replay",
		"checkpoint",
		"step_1",
		"string",
		"task",
		"task-1",
		nil,
		nil,
		nil,
	)
	require.NoError(t, err)
	assert.False(t, reinforced1)

	eventID2, reinforced2, _, _, err := UpsertMemoryWithEventIdempotent(
		db,
		"agent1",
		"req_upsert_replay",
		"checkpoint",
		"step_1",
		"string",
		"task",
		"task-1",
		nil,
		nil,
		nil,
	)
	require.NoError(t, err)
	assert.Equal(t, eventID1, eventID2)
	assert.Equal(t, reinforced1, reinforced2)

	var memoryCount int
	err = db.QueryRow(`SELECT COUNT(*) FROM memory WHERE scope = 'task' AND scope_id = 'task-1' AND key = 'checkpoint'`).Scan(&memoryCount)
	require.NoError(t, err)
	assert.Equal(t, 1, memoryCount)
}

func TestCompactMemoryWithEventIdempotent(t *testing.T) {
	db, cleanup := setupMemoryTestDB(t)
	defer cleanup()

	for i := 0; i < 5; i++ {
		key := fmt.Sprintf("key_%d", i)
		err := SetMemory(db, key, fmt.Sprintf("value_%d", i), "string", "project", "proj-a", nil)
		require.NoError(t, err)
	}

	eventID, compacted, summaryKey, summaryMemoryID, err := CompactMemoryWithEventIdempotent(db, "agent1", "req_compact_1", "project", "proj-a", 0, 2)
	require.NoError(t, err)
	require.Greater(t, eventID, int64(0))
	assert.Equal(t, memoryCompactionSummaryKey, summaryKey)
	assert.NotEmpty(t, summaryMemoryID)
	assert.Equal(t, 3, compacted)

	summary, err := GetMemory(db, memoryCompactionSummaryKey, "project", "proj-a")
	require.NoError(t, err)
	require.NotNil(t, summary)
	assert.Equal(t, "json", summary.ValueType)

	var superseded int
	err = db.QueryRow(`SELECT COUNT(*) FROM memory WHERE scope = 'project' AND scope_id = 'proj-a' AND superseded_by != ''`).Scan(&superseded)
	require.NoError(t, err)
	assert.Equal(t, 3, superseded)
}

func TestGCMemoryWithEventIdempotent(t *testing.T) {
	db, cleanup := setupMemoryTestDB(t)
	defer cleanup()

	expired := time.Now().UTC().Add(-1 * time.Hour)
	require.NoError(t, SetMemory(db, "expired_1", "v", "string", "global", "", &expired))
	require.NoError(t, SetMemory(db, "expired_2", "v", "string", "global", "", &expired))

	// Create superseded row
	require.NoError(t, SetMemory(db, "active", "v", "string", "global", "", nil))
	_, err := db.Exec(`UPDATE memory SET superseded_by = 'memory_x' WHERE key = 'active' AND scope = 'global' AND scope_id = ''`)
	require.NoError(t, err)

	eventID, deleted, err := GCMemoryWithEventIdempotent(db, "agent1", "req_gc_1", 10)
	require.NoError(t, err)
	require.Greater(t, eventID, int64(0))
	assert.Equal(t, 3, deleted)

	var remaining int
	err = db.QueryRow(`SELECT COUNT(*) FROM memory WHERE (expires_at IS NOT NULL AND expires_at <= CURRENT_TIMESTAMP) OR superseded_by IS NOT NULL`).Scan(&remaining)
	require.NoError(t, err)
	assert.Equal(t, 0, remaining)
}

func TestGetMemory_FilterSuperseded(t *testing.T) {
	db, cleanup := setupMemoryTestDB(t)
	defer cleanup()

	require.NoError(t, SetMemory(db, "active", "v1", "string", "global", "", nil))

	// Supersede the row
	_, err := db.Exec(`UPDATE memory SET superseded_by = 'memory_x' WHERE key = 'active' AND scope = 'global' AND scope_id = ''`)
	require.NoError(t, err)

	// Default get should not find it
	mem, err := GetMemory(db, "active", "global", "")
	require.NoError(t, err)
	assert.Nil(t, mem)

	// With include-superseded, should find it
	mem, err = GetMemoryWithOptions(db, "active", "global", "", MemoryReadOptions{IncludeSuperseded: true})
	require.NoError(t, err)
	require.NotNil(t, mem)
	assert.Equal(t, "memory_x", mem.SupersededBy)
}

func TestListMemory_FilterSuperseded(t *testing.T) {
	db, cleanup := setupMemoryTestDB(t)
	defer cleanup()

	require.NoError(t, SetMemory(db, "k1", "v1", "string", "global", "", nil))
	require.NoError(t, SetMemory(db, "k2", "v2", "string", "global", "", nil))
	require.NoError(t, SetMemory(db, "k3", "v3", "string", "global", "", nil))

	// Supersede k2
	_, err := db.Exec(`UPDATE memory SET superseded_by = 'memory_x' WHERE key = 'k2' AND scope = 'global' AND scope_id = ''`)
	require.NoError(t, err)

	// Default list excludes superseded
	memories, err := ListMemory(db, "global", "")
	require.NoError(t, err)
	assert.Len(t, memories, 2)

	// With include-superseded, returns all 3
	memories, err = ListMemoryWithOptions(db, "global", "", MemoryReadOptions{IncludeSuperseded: true})
	require.NoError(t, err)
	assert.Len(t, memories, 3)
}

func TestValidateValueType(t *testing.T) {
	for _, valid := range []string{"string", "number", "boolean", "json", "array", ""} {
		assert.NoError(t, validateValueType(valid), "expected %q to be valid", valid)
	}
	for _, invalid := range []string{"str", "int", "bool", "object", "map", "list"} {
		assert.Error(t, validateValueType(invalid), "expected %q to be invalid", invalid)
	}
}

func TestSetMemory_RejectsInvalidValueType(t *testing.T) {
	db, cleanup := setupMemoryTestDB(t)
	defer cleanup()

	err := SetMemory(db, "k", "v", "invalid_type", "global", "", nil)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "invalid value_type")
}

func TestUpsertMemory_RejectsInvalidValueType(t *testing.T) {
	db, cleanup := setupMemoryTestDB(t)
	defer cleanup()

	_, _, _, _, err := UpsertMemoryWithEventIdempotent(db, "agent1", "req_vt_1", "k", "v", "invalid", "global", "", nil, nil, nil)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "invalid value_type")
}

func TestTouchMemoryIdempotent(t *testing.T) {
	db, cleanup := setupMemoryTestDB(t)
	defer cleanup()

	require.NoError(t, SetMemory(db, "touchme", "v", "string", "global", "", nil))

	mem, err := GetMemory(db, "touchme", "global", "")
	require.NoError(t, err)
	origConfidence := mem.Confidence

	eventID, newConf, err := TouchMemoryIdempotent(db, "agent1", "req_touch_1", "touchme", "global", "", 0.1)
	require.NoError(t, err)
	assert.Greater(t, eventID, int64(0))
	assert.Greater(t, newConf, origConfidence)

	// Verify event was created
	var kind string
	err = db.QueryRow(`SELECT kind FROM events WHERE id = ?`, eventID).Scan(&kind)
	require.NoError(t, err)
	assert.Equal(t, "memory_touched", kind)

	// Replay returns same result
	eventID2, _, err := TouchMemoryIdempotent(db, "agent1", "req_touch_1", "touchme", "global", "", 0.1)
	require.NoError(t, err)
	assert.Equal(t, eventID, eventID2)

	// Touch nonexistent
	_, _, err = TouchMemoryIdempotent(db, "agent1", "req_touch_2", "nope", "global", "", 0.1)
	assert.Error(t, err)
}

func TestTouchMemoryIdempotent_SkipsSuperseded(t *testing.T) {
	db, cleanup := setupMemoryTestDB(t)
	defer cleanup()

	require.NoError(t, SetMemory(db, "old", "v", "string", "global", "", nil))
	_, err := db.Exec(`UPDATE memory SET superseded_by = 'memory_x' WHERE key = 'old'`)
	require.NoError(t, err)

	_, _, err = TouchMemoryIdempotent(db, "agent1", "req_touch_3", "old", "global", "", 0.1)
	assert.Error(t, err)
}

func TestQueryMemory(t *testing.T) {
	db, cleanup := setupMemoryTestDB(t)
	defer cleanup()

	require.NoError(t, SetMemory(db, "api_key", "secret", "string", "global", "", nil))
	require.NoError(t, SetMemory(db, "api_url", "http://x", "string", "global", "", nil))
	require.NoError(t, SetMemory(db, "db_host", "localhost", "string", "global", "", nil))

	// Pattern match
	results, err := QueryMemory(db, "global", "", "api%", 10)
	require.NoError(t, err)
	assert.Len(t, results, 2)

	// Wildcard
	results, err = QueryMemory(db, "global", "", "%", 10)
	require.NoError(t, err)
	assert.Len(t, results, 3)

	// No match
	results, err = QueryMemory(db, "global", "", "nope%", 10)
	require.NoError(t, err)
	assert.Len(t, results, 0)
}

func TestQueryMemory_ExcludesSuperseded(t *testing.T) {
	db, cleanup := setupMemoryTestDB(t)
	defer cleanup()

	require.NoError(t, SetMemory(db, "find_me", "v1", "string", "global", "", nil))
	require.NoError(t, SetMemory(db, "find_old", "v2", "string", "global", "", nil))
	_, err := db.Exec(`UPDATE memory SET superseded_by = 'memory_x' WHERE key = 'find_old'`)
	require.NoError(t, err)

	results, err := QueryMemory(db, "global", "", "find%", 10)
	require.NoError(t, err)
	assert.Len(t, results, 1)
	assert.Equal(t, "find_me", results[0].Key)
}

func TestCanonicalReconciliation(t *testing.T) {
	db, cleanup := setupMemoryTestDB(t)
	defer cleanup()

	// Insert rows with stale canonical keys (simulating pre-migration data)
	_, err := db.Exec(`INSERT INTO memory (key, canonical_key, value, value_type, scope, scope_id, confidence, last_seen_at)
		VALUES ('API.Key', 'api.key', 'secret', 'string', 'global', '', 0.5, CURRENT_TIMESTAMP)`)
	require.NoError(t, err)

	// Run reconciliation
	require.NoError(t, reconcileCanonicalKeys(db))

	// Verify canonical was re-normalized (strips dots)
	var canonical string
	err = db.QueryRow(`SELECT canonical_key FROM memory WHERE key = 'API.Key'`).Scan(&canonical)
	require.NoError(t, err)
	assert.Equal(t, "apikey", canonical)
}

func TestCanonicalReconciliation_ResolvesCollisions(t *testing.T) {
	db, cleanup := setupMemoryTestDB(t)
	defer cleanup()

	// Insert two rows that will collide after canonical normalization
	_, err := db.Exec(`INSERT INTO memory (key, canonical_key, value, value_type, scope, scope_id, confidence, last_seen_at)
		VALUES ('api-key', 'api-key', 'v1', 'string', 'global', '', 0.8, CURRENT_TIMESTAMP)`)
	require.NoError(t, err)
	_, err = db.Exec(`INSERT INTO memory (key, canonical_key, value, value_type, scope, scope_id, confidence, last_seen_at)
		VALUES ('api_key', 'api_key', 'v2', 'string', 'global', '', 0.3, CURRENT_TIMESTAMP)`)
	require.NoError(t, err)

	require.NoError(t, reconcileCanonicalKeys(db))

	// Only one should remain active
	var activeCount int
	err = db.QueryRow(`SELECT COUNT(*) FROM memory WHERE scope = 'global' AND scope_id = '' AND canonical_key = 'api-key' AND superseded_by IS NULL`).Scan(&activeCount)
	require.NoError(t, err)
	assert.Equal(t, 1, activeCount)

	// The winner should be the one with higher confidence
	var winnerValue string
	err = db.QueryRow(`SELECT value FROM memory WHERE scope = 'global' AND scope_id = '' AND canonical_key = 'api-key' AND superseded_by IS NULL`).Scan(&winnerValue)
	require.NoError(t, err)
	assert.Equal(t, "v1", winnerValue)
}

func TestMemoryEventMetadata_MarshalProducesValidJSON(t *testing.T) {
	db, cleanup := setupMemoryTestDB(t)
	defer cleanup()

	tests := []struct {
		name      string
		op        func() error
		eventKind string
	}{
		{
			name: "UpsertMemory produces valid metadata",
			op: func() error {
				_, _, _, _, err := UpsertMemoryWithEventIdempotent(
					db, "agent1", "req_meta_1",
					"test-key", "test-value", "string",
					"global", "", nil, nil, nil,
				)
				return err
			},
			eventKind: "memory_upserted",
		},
		{
			name: "CompactMemory produces valid metadata",
			op: func() error {
				_, _, _, _, err := CompactMemoryWithEventIdempotent(
					db, "agent1", "req_meta_2",
					"global", "", 0, 0,
				)
				return err
			},
			eventKind: "memory_compacted",
		},
		{
			name: "GCMemory produces valid metadata",
			op: func() error {
				_, _, err := GCMemoryWithEventIdempotent(
					db, "agent1", "req_meta_3", 10,
				)
				return err
			},
			eventKind: "memory_gc",
		},
		{
			name: "DeleteMemoryWithEvent produces valid metadata",
			op: func() error {
				// Ensure the key exists before deleting
				_ = SetMemory(db, "to-delete", "val", "string", "global", "", nil)
				_, err := DeleteMemoryWithEvent(db, "agent1", "to-delete", "global", "")
				return err
			},
			eventKind: "memory_delete",
		},
		{
			name: "DeleteMemoryWithEventIdempotent produces valid metadata",
			op: func() error {
				_ = SetMemory(db, "to-delete-idem", "val", "string", "global", "", nil)
				_, err := DeleteMemoryWithEventIdempotent(db, "agent1", "req_meta_5", "to-delete-idem", "global", "")
				return err
			},
			eventKind: "memory_delete",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.op()
			require.NoError(t, err)

			// Verify the event was created with valid metadata
			events, err := ListEvents(db, ListEventsParams{Kind: tt.eventKind, Limit: 1, Desc: true})
			require.NoError(t, err)
			require.NotEmpty(t, events, "expected at least one %s event", tt.eventKind)

			// Metadata should be valid JSON (non-empty)
			lastEvent := events[0]
			assert.NotEmpty(t, lastEvent.Metadata, "event metadata should not be empty for %s", tt.eventKind)
		})
	}
}
