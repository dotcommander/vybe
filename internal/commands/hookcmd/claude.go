package hookcmd

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

func claudeSettingsPath() string {
	if p := os.Getenv("CLAUDE_SETTINGS_PATH"); p != "" {
		return p
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".claude", "settings.json")
}

// ClaudeSettingsPath returns the resolved Claude settings.json path.
// Checks CLAUDE_SETTINGS_PATH env var first, then falls back to ~/.claude/settings.json.
func ClaudeSettingsPath() string { return claudeSettingsPath() }

func projectClaudeSettingsPath() string {
	wd, err := os.Getwd()
	if err != nil {
		return filepath.Join(".", ".claude", "settings.json")
	}
	return filepath.Join(wd, ".claude", "settings.json")
}

func resolveClaudeSettingsPath(projectScoped bool) string {
	if projectScoped {
		return projectClaudeSettingsPath()
	}
	return claudeSettingsPath()
}

func readSettings(path string) (map[string]any, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]any{}, nil
		}
		return nil, fmt.Errorf("read %s: %w", path, err)
	}

	var settings map[string]any
	if err := json.Unmarshal(data, &settings); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	return settings, nil
}

func writeSettings(path string, settings map[string]any) error {
	data, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal settings: %w", err)
	}
	data = append(data, '\n')

	return atomicWriteFile(path, data, 0o600)
}

// HasVybeHook checks if a hooks array already contains a vybe hook command.
func HasVybeHook(entries []any) bool {
	for _, entry := range entries {
		if isVybeHookEntry(entry) {
			return true
		}
	}
	return false
}

// IsVybeHookCommand checks if a command string is a vybe hook command.
// Handles quoted executable paths that may contain spaces (e.g. "/path with spaces/vybe").
func IsVybeHookCommand(command string) bool {
	cmd := strings.TrimSpace(command)
	if cmd == "" {
		return false
	}

	var exe, rest string
	if cmd[0] == '"' {
		// Quoted executable: find closing quote
		end := strings.Index(cmd[1:], "\"")
		if end < 0 {
			return false
		}
		exe = cmd[1 : end+1]
		rest = strings.TrimSpace(cmd[end+2:])
	} else if cmd[0] == '\'' {
		end := strings.Index(cmd[1:], "'")
		if end < 0 {
			return false
		}
		exe = cmd[1 : end+1]
		rest = strings.TrimSpace(cmd[end+2:])
	} else {
		// Unquoted: split on first space
		idx := strings.IndexByte(cmd, ' ')
		if idx < 0 {
			return false
		}
		exe = cmd[:idx]
		rest = strings.TrimSpace(cmd[idx+1:])
	}

	if filepath.Base(exe) != "vybe" {
		return false
	}

	parts := strings.Fields(rest)
	if len(parts) < 2 || parts[0] != "hook" {
		return false
	}

	switch parts[1] {
	case "session-start", "session-end", "prompt", "tool-failure",
		"checkpoint", "task-completed":
		return true
	default:
		return false
	}
}

func hookEntryEqual(a, b map[string]any) bool {
	aj, _ := json.Marshal(a)
	bj, _ := json.Marshal(b)
	return string(aj) == string(bj)
}

// isVybeHookEntry reports whether a single hook-array entry contains a vybe hook command.
func isVybeHookEntry(entry any) bool {
	entryMap, ok := entry.(map[string]any)
	if !ok {
		return false
	}
	hooks, ok := entryMap["hooks"].([]any)
	if !ok {
		return false
	}
	for _, h := range hooks {
		hMap, ok := h.(map[string]any)
		if !ok {
			continue
		}
		cmd, _ := hMap["command"].(string)
		if IsVybeHookCommand(cmd) {
			return true
		}
	}
	return false
}

// filterVybeEntries removes vybe hook entries from entries, returning the
// remaining entries and whether any were removed.
func filterVybeEntries(entries []any) (kept []any, removedAny bool) {
	for _, entry := range entries {
		if isVybeHookEntry(entry) {
			removedAny = true
			continue
		}
		kept = append(kept, entry)
	}
	return kept, removedAny
}

type installOutcome int

const (
	hookInstalled installOutcome = iota
	hookUpdated
	hookSkipped
)

func upsertVybeHookEntry(existing []any, newEntry map[string]any) ([]any, installOutcome) {
	var kept []any
	hadVybe := false
	matchingVybe := false

	for _, currentEntry := range existing {
		if isVybeHookEntry(currentEntry) {
			hadVybe = true
			entryObj, _ := currentEntry.(map[string]any)
			if hookEntryEqual(entryObj, newEntry) {
				matchingVybe = true
			}
			continue
		}
		kept = append(kept, currentEntry)
	}

	kept = append(kept, newEntry)
	entries := kept
	if matchingVybe {
		return entries, hookSkipped
	}
	if hadVybe {
		return entries, hookUpdated
	}
	return entries, hookInstalled
}

// ClaudeInstallResult reports the outcome of a Claude hook install operation.
// Exported so callers outside this package (e.g. vybe init) can use the type.
type ClaudeInstallResult struct {
	Path      string   `json:"path"`
	Installed []string `json:"installed"`
	Updated   []string `json:"updated,omitempty"`
	Skipped   []string `json:"skipped"`
}

// InstallClaudeHooks installs vybe hooks into the Claude Code settings file.
// Exported so commands outside this package (e.g. vybe init) can call it directly.
func InstallClaudeHooks(projectScoped bool) (*ClaudeInstallResult, error) {
	path := resolveClaudeSettingsPath(projectScoped)

	var installed []string
	var updated []string
	var skipped []string

	if err := withLockedSettings(path, func(settings map[string]any) error {
		hooksObj, _ := settings["hooks"].(map[string]any)
		if hooksObj == nil {
			hooksObj = map[string]any{}
		}

		for eventName, entry := range vybeHooks() {
			existing, _ := hooksObj[eventName].([]any)

			entryJSON, _ := json.Marshal(entry)
			var entryMap map[string]any
			_ = json.Unmarshal(entryJSON, &entryMap)

			entries, outcome := upsertVybeHookEntry(existing, entryMap)
			hooksObj[eventName] = entries

			switch outcome {
			case hookInstalled:
				installed = append(installed, eventName)
			case hookUpdated:
				updated = append(updated, eventName)
			case hookSkipped:
				skipped = append(skipped, eventName)
			}
		}

		settings["hooks"] = hooksObj
		return nil
	}); err != nil {
		return nil, err
	}

	ensureHookAgentStateBestEffort("claude")

	sort.Strings(installed)
	sort.Strings(updated)
	sort.Strings(skipped)
	return &ClaudeInstallResult{Path: path, Installed: installed, Updated: updated, Skipped: skipped}, nil
}
