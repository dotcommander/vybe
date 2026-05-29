package actions

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dotcommander/vybe/internal/store"
)

func TestParseExpiresIn_Valid(t *testing.T) {
	tests := []struct {
		duration string
		want     time.Duration
	}{
		{"1h", 1 * time.Hour},
		{"24h", 24 * time.Hour},
		{"30m", 30 * time.Minute},
		{"168h", 7 * 24 * time.Hour}, // 7 days
		{"1h30m", 90 * time.Minute},
	}

	for _, tt := range tests {
		t.Run(tt.duration, func(t *testing.T) {
			result, err := ParseExpiresIn(tt.duration)
			require.NoError(t, err)
			require.NotNil(t, result)

			// Check that the expiration is approximately correct (within 1 second)
			expectedTime := time.Now().Add(tt.want)
			diff := result.Sub(expectedTime).Abs()
			assert.Less(t, diff, 1*time.Second)
		})
	}
}

func TestParseExpiresIn_Empty(t *testing.T) {
	result, err := ParseExpiresIn("")
	assert.NoError(t, err)
	assert.Nil(t, result)
}

func TestParseExpiresIn_Invalid(t *testing.T) {
	tests := []string{
		"invalid",
		"1x",
		"abc",
		"",
	}

	for _, duration := range tests {
		t.Run(duration, func(t *testing.T) {
			if duration == "" {
				// Empty is valid (returns nil)
				return
			}
			result, err := ParseExpiresIn(duration)
			assert.Error(t, err)
			assert.Nil(t, result)
		})
	}
}

func TestMemorySetIdempotent_RejectsInvalidValueType(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	_, err := MemorySetIdempotent(db, "agent1", "req_bad_vt", "k", "v", "invalid_type", "global", "", nil, false, "", nil, "")
	require.Error(t, err)
	require.Contains(t, err.Error(), "invalid value_type")
}

func TestMemoryGet_NotFound(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	_, err := MemoryGet(db, "", "nonexistent", "global", "")
	require.ErrorContains(t, err, "not found")
}

func TestMemoryGet_Found(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	_, err := MemorySetIdempotent(db, "agent-a", "req-mem-get-1", "k1", "v1", "", "global", "", nil, false, "", nil, "")
	require.NoError(t, err)

	mem, err := MemoryGet(db, "", "k1", "global", "")
	require.NoError(t, err)
	require.Equal(t, "v1", mem.Value)
}

func TestMemoryList_Basic(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	_, err := MemorySetIdempotent(db, "agent-a", "req-mem-list-1", "x1", "v1", "", "global", "", nil, false, "", nil, "")
	require.NoError(t, err)
	_, err = MemorySetIdempotent(db, "agent-a", "req-mem-list-2", "x2", "v2", "", "global", "", nil, false, "", nil, "")
	require.NoError(t, err)

	list, err := MemoryList(db, "", "global", "")
	require.NoError(t, err)
	require.Len(t, list, 2)
}

func TestValidateMemoryKind(t *testing.T) {
	assert.NoError(t, ValidateMemoryKind("fact"))
	assert.NoError(t, ValidateMemoryKind("directive"))
	assert.NoError(t, ValidateMemoryKind("lesson"))
	// Empty is rejected by ValidateMemoryKind; callers (MemorySetIdempotent) default it to "fact" before validating.
	assert.Error(t, ValidateMemoryKind(""), "empty string is not a valid kind")
	assert.Error(t, ValidateMemoryKind("opinion"))
	assert.Error(t, ValidateMemoryKind("FACT"), "kind validation must be case-sensitive")
}

func TestMemorySetIdempotent_DefaultsKindToFact(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	_, err := MemorySetIdempotent(db, "agent1", "req-kind-default-1", "k", "v", "", "global", "", nil, false, "", nil, "")
	require.NoError(t, err)

	mem, err := MemoryGet(db, "", "k", "global", "")
	require.NoError(t, err)
	assert.Equal(t, "fact", mem.Kind, "omitted kind must default to 'fact'")
}

func TestMemorySetIdempotent_RejectsInvalidKind(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	_, err := MemorySetIdempotent(db, "agent1", "req-kind-invalid-1", "k", "v", "", "global", "", nil, false, "opinion", nil, "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid kind")
}

func TestMemoryGCIdempotent(t *testing.T) {
	t.Parallel()
	db, cleanup := setupTestDB(t)
	defer cleanup()

	expired := time.Now().UTC().Add(-1 * time.Hour)
	_, err := MemorySetIdempotent(db, "agent1", "req_expire_setup", "expired", "v", "string", "global", "", &expired, false, "", nil, "")
	require.NoError(t, err)

	gc, err := MemoryGCIdempotent(db, "agent1", "req_gc_action", 100)
	require.NoError(t, err)
	require.NotNil(t, gc)
	assert.GreaterOrEqual(t, gc.Deleted, 1)
}

// TestMemory_TaskScopeInfersFocusTaskID verifies that scope="task" with no scope-id resolves
// to the agent's focus task id.
func TestMemory_TaskScopeInfersFocusTaskID(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	const agent = "agent-infer-task"

	task, err := store.CreateTask(db, "Infer Task", "desc", "", 0)
	require.NoError(t, err)

	state, err := store.LoadOrCreateAgentState(db, agent)
	require.NoError(t, err)
	require.NoError(t, store.UpdateAgentStateAtomic(db, state.AgentName, 0, task.ID))

	_, err = MemorySetIdempotent(db, agent, "req-infer-task-set", "mykey", "myval", "", "task", "", nil, false, "", nil, "")
	require.NoError(t, err)

	mem, err := MemoryGet(db, agent, "mykey", "task", "")
	require.NoError(t, err)
	require.Equal(t, task.ID, mem.ScopeID)
	require.Equal(t, "myval", mem.Value)
}

// TestMemory_TaskScopeNoFocusErrors verifies that scope="task" with no scope-id and no focus
// returns an actionable error containing "scope-id".
func TestMemory_TaskScopeNoFocusErrors(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	const agent = "agent-no-focus"

	_, err := MemorySetIdempotent(db, agent, "req-infer-nofocus", "k", "v", "", "task", "", nil, false, "", nil, "")
	require.Error(t, err)
	require.Contains(t, err.Error(), "scope-id")
}

// TestMemory_ProjectScopeInfersFocusProjectID verifies that scope="project" with no scope-id
// resolves to the agent's focus project id.
func TestMemory_ProjectScopeInfersFocusProjectID(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	const agent = "agent-infer-project"

	project, err := store.CreateProject(db, "Infer Project", "")
	require.NoError(t, err)

	state, err := store.LoadOrCreateAgentState(db, agent)
	require.NoError(t, err)
	require.NoError(t, store.UpdateAgentStateAtomicWithProject(db, state.AgentName, 0, "", project.ID))

	_, err = MemorySetIdempotent(db, agent, "req-infer-proj-set", "projkey", "projval", "", "project", "", nil, false, "", nil, "")
	require.NoError(t, err)

	mem, err := MemoryGet(db, agent, "projkey", "project", "")
	require.NoError(t, err)
	require.Equal(t, project.ID, mem.ScopeID)
	require.Equal(t, "projval", mem.Value)
}

// TestMemory_GlobalScopeIgnoresFocus verifies that scope="global" with an empty scope-id
// succeeds regardless of focus state (no inference needed, ScopeID stays empty).
func TestMemory_GlobalScopeIgnoresFocus(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	_, err := MemorySetIdempotent(db, "agent-global", "req-global-infer", "gkey", "gval", "", "global", "", nil, false, "", nil, "")
	require.NoError(t, err)

	mem, err := MemoryGet(db, "", "gkey", "global", "")
	require.NoError(t, err)
	require.Equal(t, "", mem.ScopeID)
	require.Equal(t, "gval", mem.Value)
}
