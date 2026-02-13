package app

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// GetDBPath resolves the database path.
// Order of precedence:
// 1) CLI override (e.g. --db-path)
// 2) Environment variable: VYBE_DB_PATH
// 3) config.yaml: db_path
// 4) Default: ~/.config/vybe/vybe.db
// Returns an absolute path to vybe.db and ensures the parent directory exists.
func GetDBPath() (string, error) {
	if override := getDBPathOverride(); override != "" {
		return EnsureDBDir(override)
	}

	if envPath := os.Getenv("VYBE_DB_PATH"); envPath != "" {
		return EnsureDBDir(envPath)
	}

	cfg, err := LoadSettings()
	if err != nil {
		return "", fmt.Errorf("failed to load config: %w", err)
	}
	if cfg.DBPath != "" {
		return EnsureDBDir(cfg.DBPath)
	}

	configDir, err := ConfigDir()
	if err != nil {
		return "", fmt.Errorf("failed to determine config directory: %w", err)
	}
	return EnsureDBDir(filepath.Join(configDir, "vybe.db"))
}

// ResolveDBPathDetailed returns the resolved DB path along with the source of that decision.
// This is for debugging/reporting; normal code should use GetDBPath.
func ResolveDBPathDetailed() (path string, source string, err error) {
	if override := getDBPathOverride(); override != "" {
		resolvedPath, ensureErr := EnsureDBDir(override)
		return resolvedPath, "cli(--db-path)", ensureErr
	}

	if envPath := os.Getenv("VYBE_DB_PATH"); envPath != "" {
		resolvedPath, ensureErr := EnsureDBDir(envPath)
		return resolvedPath, "env(VYBE_DB_PATH)", ensureErr
	}

	dir, err := ConfigDir()
	if err != nil {
		return "", "", fmt.Errorf("failed to determine config directory: %w", err)
	}

	// Config file order must match LoadSettings.
	configPaths := []string{
		filepath.Join(dir, "config.yaml"),
		filepath.Join(string(os.PathSeparator), "etc", "vybe", "config.yaml"),
		"config.yaml",
	}

	for _, p := range configPaths {
		s, loadErr := loadSettingsFile(p)
		if loadErr == nil {
			if s.DBPath != "" {
				resolvedPath, ensureErr := EnsureDBDir(s.DBPath)
				return resolvedPath, fmt.Sprintf("config(%s)", p), ensureErr
			}
			// File exists but no db_path set; keep looking.
			continue
		}
		if errors.Is(loadErr, os.ErrNotExist) {
			continue
		}
		return "", "", fmt.Errorf("failed to load config %s: %w", p, loadErr)
	}

	configDir, err := ConfigDir()
	if err != nil {
		return "", "", fmt.Errorf("failed to determine config directory: %w", err)
	}
	resolved, err := EnsureDBDir(filepath.Join(configDir, "vybe.db"))
	return resolved, "default(~/.config/vybe/vybe.db)", err
}

func EnsureDBDir(dbPath string) (string, error) {
	dir := filepath.Dir(dbPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", fmt.Errorf("failed to create database directory: %w", err)
	}
	return dbPath, nil
}
