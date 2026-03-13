package hookcmd

import (
	_ "embed"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
)

const opencodeBridgePluginFilename = "opencode-vybe-bridge.ts"

//go:embed opencode_bridge_plugin.ts
var opencodeBridgePluginSource string

func opencodeConfigDir() string {
	if xdg := os.Getenv("XDG_CONFIG_HOME"); xdg != "" {
		return filepath.Join(xdg, "opencode")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "opencode")
}

func opencodePluginDir() string {
	return filepath.Join(opencodeConfigDir(), "plugins")
}

func opencodePluginPath() string {
	return filepath.Join(opencodePluginDir(), opencodeBridgePluginFilename)
}

func opencodeConfigPath() string {
	return filepath.Join(opencodeConfigDir(), "opencode.json")
}

const opencodePluginEntry = "./plugins/" + opencodeBridgePluginFilename

// registerOpencodePlugin adds the vybe bridge plugin to opencode.json's plugin array.
// Returns true if a new entry was added, false if already present.
func registerOpencodePlugin() (bool, error) {
	configPath := opencodeConfigPath()
	added := false

	err := withLockedSettings(configPath, func(settings map[string]any) error {
		plugins, _ := settings["plugin"].([]any)
		for _, p := range plugins {
			if s, ok := p.(string); ok && s == opencodePluginEntry {
				return errSkipWrite
			}
		}
		plugins = append(plugins, opencodePluginEntry)
		settings["plugin"] = plugins
		added = true
		return nil
	})
	return added, err
}

// unregisterOpencodePlugin removes the vybe bridge plugin from opencode.json's plugin array.
// Returns true if an entry was removed.
func unregisterOpencodePlugin() (bool, error) {
	configPath := opencodeConfigPath()
	removed := false

	err := withLockedSettings(configPath, func(settings map[string]any) error {
		plugins, _ := settings["plugin"].([]any)
		if len(plugins) == 0 {
			return errSkipWrite
		}

		var kept []any
		for _, p := range plugins {
			if s, ok := p.(string); ok && s == opencodePluginEntry {
				removed = true
				continue
			}
			kept = append(kept, p)
		}

		if !removed {
			return errSkipWrite
		}

		if len(kept) == 0 {
			delete(settings, "plugin")
		} else {
			settings["plugin"] = kept
		}
		return nil
	})
	return removed, err
}

type opencodeInstallResult struct {
	Path       string `json:"path"`
	Status     string `json:"status"`
	Registered bool   `json:"registered"`
}

type opencodeUninstallResult struct {
	Path   string `json:"path"`
	Status string `json:"status"`
}

// installOpenCodePlugin installs the OpenCode bridge plugin file and registers it.
func installOpenCodePlugin() (*opencodeInstallResult, error) {
	path := opencodePluginPath()

	var status string
	if existing, readErr := os.ReadFile(path); readErr == nil {
		if string(existing) == opencodeBridgePluginSource {
			status = "skipped"
		} else {
			status = "updated"
		}
	} else {
		status = "installed"
	}

	if status == "installed" || status == "updated" {
		if err := os.MkdirAll(filepath.Dir(path), 0750); err != nil {
			return nil, fmt.Errorf("create opencode plugin directory: %w", err)
		}
		if err := os.WriteFile(path, []byte(opencodeBridgePluginSource), 0600); err != nil {
			return nil, fmt.Errorf("write opencode bridge plugin: %w", err)
		}
	}

	registered := false
	if reg, regErr := registerOpencodePlugin(); regErr != nil {
		slog.Default().Warn("hook install: register opencode plugin failed", "error", regErr)
	} else {
		registered = reg
	}

	ensureHookAgentStateBestEffort("opencode-agent")

	return &opencodeInstallResult{Path: path, Status: status, Registered: registered}, nil
}

// uninstallOpenCodePlugin removes the OpenCode bridge plugin file and unregisters it.
func uninstallOpenCodePlugin(force bool) (*opencodeUninstallResult, error) {
	path := opencodePluginPath()

	var status string
	existing, readErr := os.ReadFile(path)
	switch {
	case readErr != nil:
		status = "not_found"
	case string(existing) == opencodeBridgePluginSource:
		status = "removed"
	case force:
		status = "removed"
	default:
		status = "skipped_conflict"
	}

	if status == "removed" {
		if err := os.Remove(path); err != nil {
			return nil, fmt.Errorf("remove opencode bridge plugin: %w", err)
		}

		if _, unregErr := unregisterOpencodePlugin(); unregErr != nil {
			slog.Default().Warn("hook uninstall: unregister opencode plugin failed", "error", unregErr)
		}
	}

	return &opencodeUninstallResult{Path: path, Status: status}, nil
}
