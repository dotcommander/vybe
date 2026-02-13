package app

import (
	"path/filepath"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

func resetSettingsStateForTest() {
	settingsOnce = sync.Once{}
	settings = Settings{}
	settingsErr = nil
	SetDBPathOverride("")
}

func TestGetDBPath_PrioritizesCLIOverride(t *testing.T) {
	resetSettingsStateForTest()
	t.Cleanup(resetSettingsStateForTest)

	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("VYBE_DB_PATH", filepath.Join(home, "env", "vybe.db"))

	overridePath := filepath.Join(home, "cli", "vybe.db")
	SetDBPathOverride(overridePath)

	resolved, err := GetDBPath()
	require.NoError(t, err)
	require.Equal(t, overridePath, resolved)
}

func TestGetDBPath_UsesEnvWithoutOverride(t *testing.T) {
	resetSettingsStateForTest()
	t.Cleanup(resetSettingsStateForTest)

	home := t.TempDir()
	t.Setenv("HOME", home)

	envPath := filepath.Join(home, "env", "vybe.db")
	t.Setenv("VYBE_DB_PATH", envPath)

	resolved, err := GetDBPath()
	require.NoError(t, err)
	require.Equal(t, envPath, resolved)
}

func TestResolveDBPathDetailed_ReportsSourceForEnv(t *testing.T) {
	resetSettingsStateForTest()
	t.Cleanup(resetSettingsStateForTest)

	home := t.TempDir()
	t.Setenv("HOME", home)

	envPath := filepath.Join(home, "env", "vybe.db")
	t.Setenv("VYBE_DB_PATH", envPath)

	resolved, source, err := ResolveDBPathDetailed()
	require.NoError(t, err)
	require.Equal(t, envPath, resolved)
	require.Equal(t, "env(VYBE_DB_PATH)", source)
}

func TestEnsureDBDir_CreatesParentDirectories(t *testing.T) {
	base := t.TempDir()
	dbPath := filepath.Join(base, "nested", "deep", "vybe.db")

	resolved, err := EnsureDBDir(dbPath)
	require.NoError(t, err)
	require.Equal(t, dbPath, resolved)
	require.DirExists(t, filepath.Dir(dbPath))
}
