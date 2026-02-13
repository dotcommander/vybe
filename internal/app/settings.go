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
	DBPath      string `yaml:"db_path"`
	PostRunHook string `yaml:"post_run_hook"`
}

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
