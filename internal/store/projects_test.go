package store

import (
	"regexp"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var projIDPattern = regexp.MustCompile(`^proj_\d+(_[0-9a-f]{12})?$`)

func TestGenerateProjectIDFormat(t *testing.T) {
	id := GenerateProjectID()
	require.True(t, projIDPattern.MatchString(id), "unexpected project id format: %s", id)
}

func TestCreateAndGetProject(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	project, err := CreateProject(db, "Test Project", `{"repo":"github.com/test/repo"}`)
	require.NoError(t, err)
	require.NotNil(t, project)

	assert.True(t, projIDPattern.MatchString(project.ID), "unexpected project id format: %s", project.ID)
	assert.Equal(t, "Test Project", project.Name)
	assert.Equal(t, `{"repo":"github.com/test/repo"}`, project.Metadata)
	assert.False(t, project.CreatedAt.IsZero())

	// Round-trip via GetProject
	fetched, err := GetProject(db, project.ID)
	require.NoError(t, err)
	assert.Equal(t, project.ID, fetched.ID)
	assert.Equal(t, project.Name, fetched.Name)
	assert.Equal(t, project.Metadata, fetched.Metadata)
}

func TestCreateProjectNoMetadata(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	project, err := CreateProject(db, "Bare Project", "")
	require.NoError(t, err)
	assert.Equal(t, "Bare Project", project.Name)
	assert.Equal(t, "", project.Metadata)
}

func TestGetProjectNotFound(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	project, err := GetProject(db, "proj_nonexistent")
	assert.Error(t, err)
	assert.Nil(t, project)
	assert.Contains(t, err.Error(), "project not found")
}

func TestListProjectsEmpty(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	projects, err := ListProjects(db)
	require.NoError(t, err)
	assert.Empty(t, projects)
}

func TestListProjectsMultiple(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	_, err := CreateProject(db, "Alpha", "")
	require.NoError(t, err)

	_, err = CreateProject(db, "Beta", `{"lang":"go"}`)
	require.NoError(t, err)

	_, err = CreateProject(db, "Gamma", "")
	require.NoError(t, err)

	projects, err := ListProjects(db)
	require.NoError(t, err)
	assert.Len(t, projects, 3)

	names := make(map[string]bool)
	for _, p := range projects {
		names[p.Name] = true
	}
	assert.True(t, names["Alpha"])
	assert.True(t, names["Beta"])
	assert.True(t, names["Gamma"])
}

func TestGetProject_RetryOnBusy(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	project, err := CreateProject(db, "Retry Test", "")
	require.NoError(t, err)

	// Call GetProject multiple times to exercise the RetryWithBackoff wrapper.
	// With single-connection SQLite, any leaked state would cause deadlock.
	for i := 0; i < 10; i++ {
		fetched, err := GetProject(db, project.ID)
		require.NoError(t, err)
		assert.Equal(t, project.ID, fetched.ID)
		assert.Equal(t, "Retry Test", fetched.Name)
	}

	// Not-found should still work correctly through retry
	_, err = GetProject(db, "proj_nonexistent")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "project not found")
}

func TestEnsureProjectByID_CreatesNew(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	project, err := EnsureProjectByID(db, "/home/user/myproject", "myproject")
	require.NoError(t, err)
	require.NotNil(t, project)
	assert.Equal(t, "/home/user/myproject", project.ID)
	assert.Equal(t, "myproject", project.Name)
	assert.False(t, project.CreatedAt.IsZero())
}

func TestEnsureProjectByID_Idempotent(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	p1, err := EnsureProjectByID(db, "/home/user/myproject", "myproject")
	require.NoError(t, err)

	p2, err := EnsureProjectByID(db, "/home/user/myproject", "different-name")
	require.NoError(t, err)

	assert.Equal(t, p1.ID, p2.ID)
	assert.Equal(t, "myproject", p2.Name)          // Original name preserved
	assert.NotEqual(t, "different-name", p2.Name)   // Second call's name ignored
}

func TestEnsureProjectByID_EmptyID(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	project, err := EnsureProjectByID(db, "", "name")
	assert.Error(t, err)
	assert.Nil(t, project)
	assert.Contains(t, err.Error(), "project id is required")
}

func TestEnsureProjectByID_EmptyName(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	project, err := EnsureProjectByID(db, "/home/user/proj", "")
	assert.Error(t, err)
	assert.Nil(t, project)
	assert.Contains(t, err.Error(), "project name is required")
}

func TestSetAgentFocusProject(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	_, err := LoadOrCreateAgentState(db, "test-agent")
	require.NoError(t, err)

	project, err := CreateProject(db, "Test Project", "")
	require.NoError(t, err)

	err = SetAgentFocusProject(db, "test-agent", project.ID)
	require.NoError(t, err)

	state, err := LoadOrCreateAgentState(db, "test-agent")
	require.NoError(t, err)
	assert.Equal(t, project.ID, state.FocusProjectID)
}

func TestSetAgentFocusProjectWithEvent(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	_, err := LoadOrCreateAgentState(db, "test-agent")
	require.NoError(t, err)

	project, err := CreateProject(db, "Test Project", "")
	require.NoError(t, err)

	eventID, err := SetAgentFocusProjectWithEvent(db, "test-agent", project.ID)
	require.NoError(t, err)
	assert.Greater(t, eventID, int64(0))

	state, err := LoadOrCreateAgentState(db, "test-agent")
	require.NoError(t, err)
	assert.Equal(t, project.ID, state.FocusProjectID)
}

func TestSetAgentFocusProjectClear(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	_, err := LoadOrCreateAgentState(db, "test-agent")
	require.NoError(t, err)

	project, err := CreateProject(db, "Test Project", "")
	require.NoError(t, err)

	err = SetAgentFocusProject(db, "test-agent", project.ID)
	require.NoError(t, err)

	// Clear
	err = SetAgentFocusProject(db, "test-agent", "")
	require.NoError(t, err)

	state, err := LoadOrCreateAgentState(db, "test-agent")
	require.NoError(t, err)
	assert.Equal(t, "", state.FocusProjectID)
}
