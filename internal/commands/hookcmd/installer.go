package hookcmd

import "sort"

// InstallOptions carries the union of per-host install/uninstall knobs.
// Claude uses ProjectScoped (project- vs user-level settings.json);
// OpenCode uses Force (overwrite a locally-modified bridge plugin).
// A host ignores fields it does not use.
type InstallOptions struct {
	ProjectScoped bool
	Force         bool
}

// HostInstaller is the uniform backend contract for a hook host.
// Implementations wrap existing install logic — they add no new behavior.
type HostInstaller interface {
	Name() string // "claude" | "opencode"
	Detect() bool // is this host present on the machine?
	Install(opts InstallOptions) (any, error)
	Uninstall(opts InstallOptions) (any, error)
}

// hostInstallers is the ordered installer registry, populated by init()
// functions in the per-host adapter files.
var hostInstallers []HostInstaller

// registerHostInstaller appends an installer to the registry. Called from
// per-host file init() functions.
func registerHostInstaller(h HostInstaller) {
	hostInstallers = append(hostInstallers, h)
}

// HostInstallers returns the registered installers in a stable (name-sorted) order.
func HostInstallers() []HostInstaller {
	out := make([]HostInstaller, len(hostInstallers))
	copy(out, hostInstallers)
	sort.SliceStable(out, func(i, j int) bool { return out[i].Name() < out[j].Name() })
	return out
}

// DetectedHostInstallers returns only the installers whose host is present.
func DetectedHostInstallers() []HostInstaller {
	var out []HostInstaller
	for _, h := range HostInstallers() {
		if h.Detect() {
			out = append(out, h)
		}
	}
	return out
}
