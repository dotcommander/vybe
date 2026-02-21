package app

import (
	"errors"
	"os"
	"path/filepath"
	"sync"

	"gopkg.in/yaml.v3"
)

// Settings represents configuration loaded from config.yaml.
// Field names match snake_case YAML keys.
type Settings struct {
	DBPath                    string `yaml:"db_path"`
	PostRunHook               string `yaml:"post_run_hook"`
	EventsRetentionDays       int    `yaml:"events_retention_days"`
	EventsPruneBatch          int    `yaml:"events_prune_batch"`
	EventsSummarizeThreshold  int    `yaml:"events_summarize_threshold"`
	EventsSummarizeKeepRecent int    `yaml:"events_summarize_keep_recent"`
}

// EventMaintenanceSettings are effective runtime values used by checkpoint/session-end maintenance.
type EventMaintenanceSettings struct {
	RetentionDays       int `json:"retention_days"`
	PruneBatch          int `json:"prune_batch"`
	SummarizeThreshold  int `json:"summarize_threshold"`
	SummarizeKeepRecent int `json:"summarize_keep_recent"`
}

const (
	defaultEventsRetentionDays   = 30
	defaultEventsPruneBatch      = 500
	defaultEventsSummarizeThresh = 200
	defaultEventsSummarizeKeep   = 50
)

// EffectiveEventMaintenanceSettings returns validated maintenance settings with defaults.
// Invalid or missing config values fall back to safe defaults.
func EffectiveEventMaintenanceSettings() EventMaintenanceSettings {
	cfg := EventMaintenanceSettings{
		RetentionDays:       defaultEventsRetentionDays,
		PruneBatch:          defaultEventsPruneBatch,
		SummarizeThreshold:  defaultEventsSummarizeThresh,
		SummarizeKeepRecent: defaultEventsSummarizeKeep,
	}

	s, err := LoadSettings()
	if err != nil {
		return cfg
	}

	if s.EventsRetentionDays > 0 {
		cfg.RetentionDays = s.EventsRetentionDays
	}
	if s.EventsPruneBatch > 0 {
		cfg.PruneBatch = s.EventsPruneBatch
	}
	if s.EventsSummarizeThreshold > 0 {
		cfg.SummarizeThreshold = s.EventsSummarizeThreshold
	}
	if s.EventsSummarizeKeepRecent > 0 {
		cfg.SummarizeKeepRecent = s.EventsSummarizeKeepRecent
	}

	if cfg.RetentionDays > 3650 {
		cfg.RetentionDays = 3650
	}
	if cfg.PruneBatch > 10000 {
		cfg.PruneBatch = 10000
	}
	if cfg.SummarizeThreshold < 20 {
		cfg.SummarizeThreshold = 20
	}
	return cfg
}

// settingsOnce, settings, settingsErr implement the sync.Once lazy-load singleton for config.
// dbPathOverrideMu and dbPathOverride implement a mutex-protected process-wide override for CLI --db-path.
// These globals are required by the sync.Once pattern and the RWMutex pattern; they cannot be avoided.
//
//nolint:gochecknoglobals // sync.Once singleton + RWMutex override are intentional process-wide state
var (
	settingsOnce sync.Once
	settings     Settings
	settingsErr  error

	dbPathOverrideMu sync.RWMutex
	dbPathOverride   string
)

// SetDBPathOverride sets a process-wide database path override.
// Intended for CLI flag support (e.g. --db-path).
func SetDBPathOverride(path string) {
	dbPathOverrideMu.Lock()
	dbPathOverride = path
	dbPathOverrideMu.Unlock()
}

func getDBPathOverride() string {
	dbPathOverrideMu.RLock()
	v := dbPathOverride
	dbPathOverrideMu.RUnlock()
	return v
}

// LoadSettings loads configuration once using the documented lookup order.
// Lookup order (first found wins):
// 1) ~/.config/vybe/config.yaml
// 2) /etc/vybe/config.yaml
// 3) ./config.yaml (lowest priority; allows repo-local overrides if desired)
// Environment variables are handled separately.
func LoadSettings() (Settings, error) {
	settingsOnce.Do(func() {
		settings = Settings{}

		// 1) User config (~/.config/vybe/config.yaml)
		dir, err := ConfigDir()
		if err != nil {
			settingsErr = err
			return
		}
		if s, err := loadSettingsFile(filepath.Join(dir, "config.yaml")); err == nil {
			settings = s
			return
		} else if err != nil && !errors.Is(err, os.ErrNotExist) {
			settingsErr = err
			return
		}

		// 2) /etc
		if s, err := loadSettingsFile(filepath.Join(string(os.PathSeparator), "etc", "vybe", "config.yaml")); err == nil {
			settings = s
			return
		} else if err != nil && !errors.Is(err, os.ErrNotExist) {
			settingsErr = err
			return
		}

		// 3) Local ./config.yaml (lowest priority)
		if s, err := loadSettingsFile("config.yaml"); err == nil {
			settings = s
			return
		} else if err != nil && !errors.Is(err, os.ErrNotExist) {
			settingsErr = err
			return
		}
	})

	return settings, settingsErr
}

func loadSettingsFile(path string) (Settings, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return Settings{}, err
	}

	var s Settings
	if err := yaml.Unmarshal(b, &s); err != nil {
		return Settings{}, err
	}
	return s, nil
}
