package commands

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/dotcommander/vibe/internal/output"
	"github.com/dotcommander/vibe/internal/store"
	"github.com/spf13/cobra"
)

const opencodeBridgePluginFilename = "vibe-bridge.js"

// opencodeBridgePluginSource is the embedded JS bridge installed by `vibe hook install`.
// This is the runtime artifact (plain JS, no type imports).
//
// The canonical TypeScript version lives at examples/opencode/opencode-vibe-plugin.ts
// and serves as documentation/reference. When adding features, update the TS
// version first, then backport to the embedded JS source.
//
//go:embed opencode_bridge_plugin.js
var opencodeBridgePluginSource string

const vibeCommandFallback = "vibe"

var (
	vibeHooksOnce  sync.Once
	vibeHooksCache map[string]hookEntry
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

func claudeSettingsPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".claude", "settings.json")
}

func opencodePluginPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "opencode", "plugins", opencodeBridgePluginFilename)
}

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

func vibeExecutable() string {
	exe, err := os.Executable()
	if err != nil || strings.TrimSpace(exe) == "" {
		return vibeCommandFallback
	}
	return exe
}

// buildVibeHookCommand constructs the hook command string for settings.json.
// Subcommands are hardcoded string literals (not user input) so concatenation is safe.
func buildVibeHookCommand(subcommand string) string {
	exe := vibeExecutable()
	if exe == vibeCommandFallback {
		return fmt.Sprintf("vibe hook %s", subcommand)
	}
	// Quote the executable path so hook commands are robust with spaces.
	return fmt.Sprintf("%q hook %s", exe, subcommand)
}

// vibeHooks returns the hook definitions for settings.json.
// Cached via sync.Once since buildVibeHookCommand resolves the executable path.
func vibeHooks() map[string]hookEntry {
	vibeHooksOnce.Do(func() {
		vibeHooksCache = buildVibeHooks()
	})
	return vibeHooksCache
}

func buildVibeHooks() map[string]hookEntry {
	return map[string]hookEntry{
		"SessionStart": {
			Matcher: "startup|resume|compact",
			Hooks: []hookHandler{{
				Type:    "command",
				Command: buildVibeHookCommand("session-start"),
				Timeout: 3000,
			}},
		},
		"UserPromptSubmit": {
			Matcher: "",
			Hooks: []hookHandler{{
				Type:    "command",
				Command: buildVibeHookCommand("prompt"),
				Timeout: 2000,
			}},
		},
		"PostToolUseFailure": {
			Matcher: "",
			Hooks: []hookHandler{{
				Type:    "command",
				Command: buildVibeHookCommand("tool-failure"),
				Timeout: 2000,
			}},
		},
		"PreCompact": {
			Matcher: "",
			Hooks: []hookHandler{{
				Type:    "command",
				Command: buildVibeHookCommand("checkpoint"),
				Timeout: 4000,
			}},
		},
		"SessionEnd": {
			Matcher: "",
			Hooks: []hookHandler{
				{
					Type:    "command",
					Command: buildVibeHookCommand("checkpoint"),
					Timeout: 4000,
				},
				{
					Type:    "command",
					Command: buildVibeHookCommand("retrospective"),
					Timeout: 15000,
				},
			},
		},
		"TaskCompleted": {
			Matcher: "",
			Hooks: []hookHandler{{
				Type:    "command",
				Command: buildVibeHookCommand("task-completed"),
				Timeout: 2000,
			}},
		},
	}
}

func vibeHookEventNames() []string {
	events := make([]string, 0, len(vibeHooks()))
	for name := range vibeHooks() {
		events = append(events, name)
	}
	sort.Strings(events)
	return events
}

// readSettings reads and parses ~/.claude/settings.json.
// Returns empty map if file doesn't exist.
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

// writeSettings writes settings back with 2-space indent.
func writeSettings(path string, settings map[string]any) error {
	data, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal settings: %w", err)
	}
	data = append(data, '\n')

	// Ensure directory exists
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return fmt.Errorf("create directory: %w", err)
	}

	return os.WriteFile(path, data, 0600)
}

// hasVibeHook checks if a hooks array already contains a vibe hook command.
func hasVibeHook(entries []any) bool {
	for _, entry := range entries {
		entryMap, ok := entry.(map[string]any)
		if !ok {
			continue
		}
		hooks, ok := entryMap["hooks"].([]any)
		if !ok {
			continue
		}
		for _, h := range hooks {
			hMap, ok := h.(map[string]any)
			if !ok {
				continue
			}
			cmd, _ := hMap["command"].(string)
			if isVibeHookCommand(cmd) {
				return true
			}
		}
	}
	return false
}

func isVibeHookCommand(command string) bool {
	cmd := strings.TrimSpace(command)
	if cmd == "" {
		return false
	}
	parts := strings.Fields(cmd)
	if len(parts) < 3 {
		return false
	}

	execToken := strings.Trim(parts[0], "\"'")
	if filepath.Base(execToken) != "vibe" {
		return false
	}
	if parts[1] != "hook" {
		return false
	}

	sub := parts[2]
	return sub == "session-start" ||
		sub == "prompt" ||
		sub == "tool-failure" ||
		sub == "checkpoint" ||
		sub == "task-completed" ||
		sub == "retrospective"
}

// hookEntryEqual compares two parsed hook entries by their JSON representation.
// Simpler than reflect.DeepEqual and sufficient since both sides originate from JSON.
func hookEntryEqual(a, b map[string]any) bool {
	aj, _ := json.Marshal(a)
	bj, _ := json.Marshal(b)
	return string(aj) == string(bj)
}

// installOutcome indicates what happened when upserting a hook entry.
type installOutcome int

const (
	hookInstalled installOutcome = iota
	hookUpdated
	hookSkipped
)

// upsertVibeHookEntry replaces any existing vibe hook entry or appends a new one.
// Non-vibe entries are preserved. Returns the updated slice and the outcome.
func upsertVibeHookEntry(existing []any, newEntry map[string]any) ([]any, installOutcome) {
	var kept []any
	hadVibe := false
	matchingVibe := false

	for _, currentEntry := range existing {
		entryObj, ok := currentEntry.(map[string]any)
		if !ok {
			kept = append(kept, currentEntry)
			continue
		}
		hooks, ok := entryObj["hooks"].([]any)
		if !ok {
			kept = append(kept, currentEntry)
			continue
		}
		isVibe := false
		for _, h := range hooks {
			hMap, ok := h.(map[string]any)
			if !ok {
				continue
			}
			cmd, _ := hMap["command"].(string)
			if isVibeHookCommand(cmd) {
				isVibe = true
				break
			}
		}
		if isVibe {
			hadVibe = true
			if hookEntryEqual(entryObj, newEntry) {
				matchingVibe = true
			}
			continue // strip old vibe entry; re-appended below
		}
		kept = append(kept, currentEntry)
	}

	entries := append(kept, newEntry)
	if matchingVibe {
		return entries, hookSkipped
	}
	if hadVibe {
		return entries, hookUpdated
	}
	return entries, hookInstalled
}

// resolveTargetFlags returns (claude, opencode) based on explicit flag usage.
// Default: Claude only when no flags specified.
func resolveTargetFlags(cmd *cobra.Command, claudeFlag, opencodeFlag string) (claude bool, opencode bool, err error) {
	claude = cmd.Flags().Changed(claudeFlag)
	opencode = cmd.Flags().Changed(opencodeFlag)

	if !claude && !opencode {
		return true, false, nil // default: Claude only
	}

	// Get actual values
	c, _ := cmd.Flags().GetBool(claudeFlag)
	o, _ := cmd.Flags().GetBool(opencodeFlag)

	if !c && !o {
		return false, false, fmt.Errorf("nothing selected: use --%s and/or --%s", claudeFlag, opencodeFlag)
	}

	return c, o, nil
}

func newHookInstallCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "install",
		Short: "Install vibe hooks for Claude and/or OpenCode",
		Long: `Installs Claude Code hooks and/or an OpenCode bridge plugin.
Idempotent — safe to run multiple times. Existing hooks/plugins are preserved.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			installClaude, installOpenCode, err := resolveTargetFlags(cmd, "claude", "opencode")
			if err != nil {
				return cmdErr(err)
			}

			type claudeResult struct {
				Path      string   `json:"path"`
				Installed []string `json:"installed"`
				Updated   []string `json:"updated,omitempty"`
				Skipped   []string `json:"skipped"`
			}
			type opencodeResult struct {
				Path   string `json:"path"`
				Status string `json:"status"` // installed, updated, skipped, skipped_conflict
			}
			type result struct {
				Message  string          `json:"message"`
				Claude   *claudeResult   `json:"claude,omitempty"`
				OpenCode *opencodeResult `json:"opencode,omitempty"`
			}

			resp := result{}
			projectScoped, _ := cmd.Flags().GetBool("project")

			if installClaude {
				path := resolveClaudeSettingsPath(projectScoped)

				settings, err := readSettings(path)
				if err != nil {
					return cmdErr(err)
				}

				hooksObj, _ := settings["hooks"].(map[string]any)
				if hooksObj == nil {
					hooksObj = map[string]any{}
				}

				var installed []string
				var updated []string
				var skipped []string

				for eventName, entry := range vibeHooks() {
					existing, _ := hooksObj[eventName].([]any)

					entryJSON, _ := json.Marshal(entry)
					var entryMap map[string]any
					_ = json.Unmarshal(entryJSON, &entryMap)

					entries, outcome := upsertVibeHookEntry(existing, entryMap)
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
				if err := writeSettings(path, settings); err != nil {
					return cmdErr(err)
				}

				_ = withDB(func(db *DB) error {
					_, err := store.LoadOrCreateAgentState(db, "claude")
					return err
				})

				sort.Strings(installed)
				sort.Strings(updated)
				sort.Strings(skipped)
				resp.Claude = &claudeResult{Path: path, Installed: installed, Updated: updated, Skipped: skipped}
			}

			if installOpenCode {
				path := opencodePluginPath()
				force, _ := cmd.Flags().GetBool("force")

				status := "installed"
				if existing, readErr := os.ReadFile(path); readErr == nil {
					if string(existing) == opencodeBridgePluginSource {
						status = "skipped"
					} else if !force {
						status = "skipped_conflict"
					} else {
						status = "updated"
					}
				}

				if status == "installed" || status == "updated" {
					if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
						return cmdErr(fmt.Errorf("create opencode plugin directory: %w", err))
					}
					if err := os.WriteFile(path, []byte(opencodeBridgePluginSource), 0600); err != nil {
						return cmdErr(fmt.Errorf("write opencode bridge plugin: %w", err))
					}
				}

				_ = withDB(func(db *DB) error {
					_, err := store.LoadOrCreateAgentState(db, "opencode-agent")
					return err
				})

				resp.OpenCode = &opencodeResult{Path: path, Status: status}
			}

			// Build confirmation message
			var parts []string
			if resp.Claude != nil {
				if len(resp.Claude.Installed) > 0 {
					parts = append(parts, fmt.Sprintf("Claude Code hooks installed (%s)", strings.Join(resp.Claude.Installed, ", ")))
				}
				if len(resp.Claude.Updated) > 0 {
					parts = append(parts, fmt.Sprintf("Claude Code hooks updated (%s)", strings.Join(resp.Claude.Updated, ", ")))
				}
				if len(resp.Claude.Installed) == 0 && len(resp.Claude.Updated) == 0 {
					parts = append(parts, "Claude Code hooks already installed")
				}
			}
			if resp.OpenCode != nil {
				switch resp.OpenCode.Status {
				case "installed":
					parts = append(parts, "OpenCode bridge plugin installed")
				case "updated":
					parts = append(parts, "OpenCode bridge plugin updated")
				case "skipped_conflict":
					parts = append(parts, "OpenCode bridge plugin skipped (file differs, use --force to overwrite)")
				default:
					parts = append(parts, "OpenCode bridge plugin already installed")
				}
			}
			if len(parts) > 0 {
				resp.Message = strings.Join(parts, "; ") + ". Run 'vibe status' to verify."
			}

			return output.PrintSuccess(resp)
		},
	}

	cmd.Flags().Bool("claude", false, "Install Claude Code hooks")
	cmd.Flags().Bool("opencode", false, "Install OpenCode bridge plugin")
	cmd.Flags().Bool("project", false, "Install Claude hooks in ./.claude/settings.json instead of ~/.claude/settings.json")
	cmd.Flags().Bool("force", false, "Overwrite existing OpenCode plugin even if it differs")

	return cmd
}

func newHookUninstallCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "uninstall",
		Short: "Remove vibe hooks for Claude and/or OpenCode",
		Long:  `Removes Claude Code hook entries and/or OpenCode bridge plugin.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			uninstallClaude, uninstallOpenCode, err := resolveTargetFlags(cmd, "claude", "opencode")
			if err != nil {
				return cmdErr(err)
			}

			type claudeResult struct {
				Path    string   `json:"path"`
				Removed []string `json:"removed"`
			}
			type opencodeResult struct {
				Path    string `json:"path"`
				Removed bool   `json:"removed"`
			}
			type result struct {
				Claude   *claudeResult   `json:"claude,omitempty"`
				OpenCode *opencodeResult `json:"opencode,omitempty"`
			}

			resp := result{}
			projectScoped, _ := cmd.Flags().GetBool("project")

			if uninstallClaude {
				path := resolveClaudeSettingsPath(projectScoped)

				settings, err := readSettings(path)
				if err != nil {
					return cmdErr(err)
				}

				hooksObj, _ := settings["hooks"].(map[string]any)
				if hooksObj == nil {
					resp.Claude = &claudeResult{Path: path, Removed: []string{}}
				} else {
					var removed []string

					for _, eventName := range vibeHookEventNames() {
						entries, ok := hooksObj[eventName].([]any)
						if !ok {
							continue
						}

						var kept []any
						for _, entry := range entries {
							entryMap, ok := entry.(map[string]any)
							if !ok {
								kept = append(kept, entry)
								continue
							}
							hooks, ok := entryMap["hooks"].([]any)
							if !ok {
								kept = append(kept, entry)
								continue
							}

							isVibe := false
							for _, h := range hooks {
								hMap, ok := h.(map[string]any)
								if !ok {
									continue
								}
								cmd, _ := hMap["command"].(string)
								if isVibeHookCommand(cmd) {
									isVibe = true
									break
								}
							}

							if !isVibe {
								kept = append(kept, entry)
							}
						}

						if len(kept) != len(entries) {
							removed = append(removed, eventName)
						}

						if len(kept) == 0 {
							delete(hooksObj, eventName)
						} else {
							hooksObj[eventName] = kept
						}
					}

					settings["hooks"] = hooksObj
					if err := writeSettings(path, settings); err != nil {
						return cmdErr(err)
					}

					resp.Claude = &claudeResult{Path: path, Removed: removed}
				}
			}

			if uninstallOpenCode {
				path := opencodePluginPath()
				removed := false
				if _, err := os.Stat(path); err == nil {
					if err := os.Remove(path); err != nil {
						return cmdErr(fmt.Errorf("remove opencode bridge plugin: %w", err))
					}
					removed = true
				}
				resp.OpenCode = &opencodeResult{Path: path, Removed: removed}
			}

			return output.PrintSuccess(resp)
		},
	}

	cmd.Flags().Bool("claude", false, "Uninstall Claude Code hooks")
	cmd.Flags().Bool("opencode", false, "Uninstall OpenCode bridge plugin")
	cmd.Flags().Bool("project", false, "Uninstall Claude hooks from ./.claude/settings.json instead of ~/.claude/settings.json")

	return cmd
}
