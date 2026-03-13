package hookcmd

import (
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"
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

func vybeHooks() map[string]hookEntry {
	vybeHooksOnce.Do(func() {
		vybeHooksCache = buildVybeHooks()
	})
	return vybeHooksCache
}

func buildVybeHooks() map[string]hookEntry {
	return map[string]hookEntry{
		"SessionStart": {
			Matcher: "startup|resume|clear|compact",
			Hooks: []hookHandler{{
				Type:    "command",
				Command: buildVybeHookCommand("session-start"),
				Timeout: 3000,
			}},
		},
		"UserPromptSubmit": {
			Matcher: "",
			Hooks: []hookHandler{{
				Type:    "command",
				Command: buildVybeHookCommand("prompt"),
				Timeout: 2000,
			}},
		},
		"PostToolUseFailure": {
			Matcher: "",
			Hooks: []hookHandler{{
				Type:    "command",
				Command: buildVybeHookCommand("tool-failure"),
				Timeout: 2000,
			}},
		},
		"PreCompact": {
			Matcher: "",
			Hooks: []hookHandler{{
				Type:    "command",
				Command: buildVybeHookCommand("checkpoint"),
				Timeout: 4000,
			}},
		},
		"SessionEnd": {
			Matcher: "",
			Hooks: []hookHandler{{
				Type:    "command",
				Command: buildVybeHookCommand("session-end"),
				Timeout: 5000,
			}},
		},
		"TaskCompleted": {
			Matcher: "",
			Hooks: []hookHandler{{
				Type:    "command",
				Command: buildVybeHookCommand("task-completed"),
				Timeout: 2000,
			}},
		},
	}
}

func vybeHookEventNames() []string {
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
