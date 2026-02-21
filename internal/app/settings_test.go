package app

import (
	"os"
	"path/filepath"
	"strings"
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

func TestLoadSettingsFile_ReadsMaintenanceFields(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	content := "events_retention_days: 45\n" +
		"events_prune_batch: 1200\n" +
		"events_summarize_threshold: 300\n" +
		"events_summarize_keep_recent: 80\n"
	require.NoError(t, os.WriteFile(path, []byte(content), 0o600))

	s, err := loadSettingsFile(path)
	require.NoError(t, err)
	require.Equal(t, 45, s.EventsRetentionDays)
	require.Equal(t, 1200, s.EventsPruneBatch)
	require.Equal(t, 300, s.EventsSummarizeThreshold)
	require.Equal(t, 80, s.EventsSummarizeKeepRecent)
}

func TestEffectiveEventMaintenanceSettings_DefaultsAndClamp(t *testing.T) {
	resetSettingsStateForTest()
	t.Cleanup(resetSettingsStateForTest)

	home := t.TempDir()
	t.Setenv("HOME", home)

	// No config file: defaults
	cfg := EffectiveEventMaintenanceSettings()
	require.Equal(t, 30, cfg.RetentionDays)
	require.Equal(t, 500, cfg.PruneBatch)
	require.Equal(t, 200, cfg.SummarizeThreshold)
	require.Equal(t, 50, cfg.SummarizeKeepRecent)

	// Out-of-range config values should be clamped/sanitized
	userConfigPath := filepath.Join(home, ".config", "vybe", "config.yaml")
	require.NoError(t, os.MkdirAll(filepath.Dir(userConfigPath), 0o755))
	require.NoError(t, os.WriteFile(userConfigPath, []byte(strings.Join([]string{
		"events_retention_days: 99999",
		"events_prune_batch: 99999",
		"events_summarize_threshold: 1",
		"events_summarize_keep_recent: -2",
		"",
	}, "\n")), 0o600))

	resetSettingsStateForTest()
	cfg = EffectiveEventMaintenanceSettings()
	require.Equal(t, 3650, cfg.RetentionDays)
	require.Equal(t, 10000, cfg.PruneBatch)
	require.Equal(t, 20, cfg.SummarizeThreshold)
	require.Equal(t, 50, cfg.SummarizeKeepRecent)
}
