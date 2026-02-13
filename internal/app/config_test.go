package app

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestConfigDir_UsesHomeDirectory(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	dir, err := ConfigDir()
	require.NoError(t, err)
	require.Equal(t, filepath.Join(home, ".config", "vibe"), dir)
}

func TestEnsureConfigDir_CreatesDefaultConfigOnlyWhenMissing(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	err := EnsureConfigDir()
	require.NoError(t, err)

	dir, err := ConfigDir()
	require.NoError(t, err)

	configFile := filepath.Join(dir, "config.yaml")
	b, err := os.ReadFile(configFile)
	require.NoError(t, err)
	require.Equal(t, defaultConfig, string(b))

	custom := []byte("db_path: /tmp/custom.db\n")
	require.NoError(t, os.WriteFile(configFile, custom, 0o600))

	err = EnsureConfigDir()
	require.NoError(t, err)

	b, err = os.ReadFile(configFile)
	require.NoError(t, err)
	require.Equal(t, string(custom), string(b))
}
