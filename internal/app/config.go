package app

import (
	"os"
	"path/filepath"
)

// ConfigDir returns ~/.config/vybe/ on all platforms.
func ConfigDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config", "vybe"), nil
}

// EnsureConfigDir creates the config directory and default config.yaml if missing.
func EnsureConfigDir() error {
	dir, err := ConfigDir()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0750); err != nil {
		return err
	}

	configFile := filepath.Join(dir, "config.yaml")
	if _, err := os.Stat(configFile); os.IsNotExist(err) {
		return os.WriteFile(configFile, []byte(defaultConfig), 0600)
	}
	return nil
}

const defaultConfig = `# vybe configuration
# Run: vybe --help

# Optional: override the SQLite database location.
# Can also be set via VYBE_DB_PATH or --db-path.
# db_path: ~/.config/vybe/vybe.db
`
