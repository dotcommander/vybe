package hookcmd

import "os"

// opencodeInstaller adapts the existing OpenCode plugin logic to HostInstaller.
type opencodeInstaller struct{}

func init() { registerHostInstaller(opencodeInstaller{}) }

func (opencodeInstaller) Name() string { return "opencode" }

// Detect reports whether the OpenCode config dir exists
// (XDG_CONFIG_HOME/opencode or ~/.config/opencode).
func (opencodeInstaller) Detect() bool {
	_, err := os.Stat(opencodeConfigDir())
	return err == nil
}

func (opencodeInstaller) Install(opts InstallOptions) (any, error) {
	return installOpenCodePlugin()
}

func (opencodeInstaller) Uninstall(opts InstallOptions) (any, error) {
	return uninstallOpenCodePlugin(opts.Force)
}
