package hookcmd

import (
	"os"
	"path/filepath"
)

// claudeInstaller adapts the existing Claude install/uninstall logic to HostInstaller.
type claudeInstaller struct{}

func init() { registerHostInstaller(claudeInstaller{}) }

func (claudeInstaller) Name() string { return "claude" }

// Detect reports whether ~/.claude exists (or CLAUDE_SETTINGS_PATH is set).
// Claude is the historical default host; presence is keyed on the settings dir.
func (claudeInstaller) Detect() bool {
	if os.Getenv("CLAUDE_SETTINGS_PATH") != "" {
		return true
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return false
	}
	_, statErr := os.Stat(filepath.Join(home, ".claude"))
	return statErr == nil
}

func (claudeInstaller) Install(opts InstallOptions) (any, error) {
	return InstallClaudeHooks(opts.ProjectScoped)
}

func (claudeInstaller) Uninstall(opts InstallOptions) (any, error) {
	return uninstallClaudeHooks(opts.ProjectScoped)
}
