package app

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestLoadSettings_PrefersUserConfigOverLocal(t *testing.T) {
	resetSettingsStateForTest()
	t.Cleanup(resetSettingsStateForTest)

	home := t.TempDir()
	t.Setenv("HOME", home)

	workdir := t.TempDir()
	oldwd, err := os.Getwd()
	require.NoError(t, err)
	require.NoError(t, os.Chdir(workdir))
	t.Cleanup(func() { _ = os.Chdir(oldwd) })

	userConfigPath := filepath.Join(home, ".config", "vybe", "config.yaml")
	require.NoError(t, os.MkdirAll(filepath.Dir(userConfigPath), 0o755))
	require.NoError(t, os.WriteFile(userConfigPath, []byte("db_path: /tmp/from-user.db\n"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(workdir, "config.yaml"), []byte("db_path: /tmp/from-local.db\n"), 0o600))

	s, err := LoadSettings()
	require.NoError(t, err)
	require.Equal(t, "/tmp/from-user.db", s.DBPath)
}

func TestLoadSettings_FallsBackToLocalConfig(t *testing.T) {
	resetSettingsStateForTest()
	t.Cleanup(resetSettingsStateForTest)

	home := t.TempDir()
	t.Setenv("HOME", home)

	workdir := t.TempDir()
	oldwd, err := os.Getwd()
	require.NoError(t, err)
	require.NoError(t, os.Chdir(workdir))
	t.Cleanup(func() { _ = os.Chdir(oldwd) })

	require.NoError(t, os.WriteFile(filepath.Join(workdir, "config.yaml"), []byte("db_path: /tmp/from-local.db\n"), 0o600))

	s, err := LoadSettings()
	require.NoError(t, err)
	require.Equal(t, "/tmp/from-local.db", s.DBPath)
}

func TestLoadSettings_InvalidYAMLReturnsError(t *testing.T) {
	resetSettingsStateForTest()
	t.Cleanup(resetSettingsStateForTest)

	home := t.TempDir()
	t.Setenv("HOME", home)

	userConfigPath := filepath.Join(home, ".config", "vybe", "config.yaml")
	require.NoError(t, os.MkdirAll(filepath.Dir(userConfigPath), 0o755))
	require.NoError(t, os.WriteFile(userConfigPath, []byte("db_path: ["), 0o600))

	_, err := LoadSettings()
	require.Error(t, err)
}

func TestLoadSettingsFile_ReadsYAML(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	require.NoError(t, os.WriteFile(path, []byte("db_path: /tmp/read.db\n"), 0o600))

	s, err := loadSettingsFile(path)
	require.NoError(t, err)
	require.Equal(t, "/tmp/read.db", s.DBPath)
}
