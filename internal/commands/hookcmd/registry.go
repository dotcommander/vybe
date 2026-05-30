package hookcmd

import (
	"fmt"
	"log/slog"
	"os"
	"sort"
	"strings"
	"sync"

	"github.com/dotcommander/vybe/internal/app"
)

const vybeCommandFallback = "vybe"

//nolint:gochecknoglobals // sync.Once singleton cache for hook definitions; required by the sync.Once pattern
var (
	vybeHooksOnce  sync.Once
	vybeHooksCache map[string]hookEntry
)

type hookHandler struct {
	Type    string `json:"type"`
	Command string `json:"command"`
	Timeout int    `json:"timeout"`
}

type hookEntry struct {
	Matcher string        `json:"matcher"`
	Hooks   []hookHandler `json:"hooks"`
}

// vybeHooks returns the hook manifest, loading from ~/.config/vybe/hooks.json
// on first call and caching for the process lifetime. Falls back to built-in
// defaults when the config dir is unavailable.
func vybeHooks() map[string]hookEntry {
	vybeHooksOnce.Do(func() {
		configDir, err := app.ConfigDir()
		if err != nil {
			slog.Default().Warn("hook registry: config dir unavailable, using defaults", "error", err)
			vybeHooksCache = buildVybeHooks()
			return
		}
		manifest, loadErr := LoadHookManifest(configDir)
		if loadErr != nil {
			slog.Default().Warn("hook registry: manifest load failed, using defaults", "error", loadErr)
			vybeHooksCache = buildVybeHooks()
			return
		}
		vybeHooksCache = manifest
	})
	return vybeHooksCache
}

// VybeHookEventNames returns the sorted list of Claude hook event names vybe installs.
// Exported so commands outside this package (e.g. vybe doctor) can check hook coverage.
func VybeHookEventNames() []string {
	hooks := vybeHooks()
	events := make([]string, 0, len(hooks))
	for name := range hooks {
		events = append(events, name)
	}
	sort.Strings(events)
	return events
}

func vybeExecutable() string {
	exe, err := os.Executable()
	if err != nil || strings.TrimSpace(exe) == "" {
		return vybeCommandFallback
	}
	return exe
}

func buildVybeHookCommand(subcommand string) string {
	exe := vybeExecutable()
	if exe == vybeCommandFallback {
		return fmt.Sprintf("vybe hook %s", subcommand)
	}
	return fmt.Sprintf("%q hook %s", exe, subcommand)
}
