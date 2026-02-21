package commands

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/dotcommander/vybe/internal/app"
	"github.com/dotcommander/vybe/internal/output"
	"github.com/dotcommander/vybe/internal/store"
	"github.com/spf13/cobra"
)

const opencodeBridgePluginFilename = "vybe-bridge.js"

// opencodeBridgePluginSource is the embedded JS bridge installed by `vybe hook install`.
// This is the runtime artifact (plain JS, no type imports).
//
// The canonical TypeScript version lives at examples/opencode/opencode-vybe-plugin.ts
// and serves as documentation/reference. When adding features, update the TS
// version first, then backport to the embedded JS source.
//
//go:embed opencode_bridge_plugin.js
var opencodeBridgePluginSource string

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

// ensureHookAgentStateBestEffort initializes agent_state for hook integration.
// It opportunistically runs migrations so `vybe hook install` works even when
// the DB schema is behind the current binary.
func ensureHookAgentStateBestEffort(agentName string) {
	if strings.TrimSpace(agentName) == "" {
		return
	}

	dbPath, err := app.GetDBPath()
	if err != nil {
		slog.Default().Warn("hook install: resolve db path failed", "error", err)
		return
	}

	db, err := store.OpenDB(dbPath)
	if err != nil {
		slog.Default().Warn("hook install: open db failed", "error", err)
		return
	}
	defer func() { _ = store.CloseDB(db) }()

	if err := store.RunMigrations(db); err != nil {
		slog.Default().Warn("hook install: run migrations failed", "error", err)
		return
	}

	if _, err := store.LoadOrCreateAgentState(db, agentName); err != nil {
		slog.Default().Warn("hook install: initialize agent state failed", "agent", agentName, "error", err)
	}
}

func vybeExecutable() string {
	exe, err := os.Executable()
	if err != nil || strings.TrimSpace(exe) == "" {
		return vybeCommandFallback
	}
	return exe
}

// buildVybeHookCommand constructs the hook command string for settings.json.
// Subcommands are hardcoded string literals (not user input) so concatenation is safe.
func buildVybeHookCommand(subcommand string) string {
	exe := vybeExecutable()
	if exe == vybeCommandFallback {
		return fmt.Sprintf("vybe hook %s", subcommand)
	}
	// Quote the executable path so hook commands are robust with spaces.
	return fmt.Sprintf("%q hook %s", exe, subcommand)
}

// vybeHooks returns the hook definitions for settings.json.
// Cached via sync.Once since buildVybeHookCommand resolves the executable path.
func vybeHooks() map[string]hookEntry {
	vybeHooksOnce.Do(func() {
		vybeHooksCache = buildVybeHooks()
	})
	return vybeHooksCache
}

//nolint:funlen // hook definitions are data-heavy; all 10 hook event entries must be defined together for maintainability
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
		"PostToolUse": {
			Matcher: "Write|Edit|MultiEdit|Bash|NotebookEdit",
			Hooks: []hookHandler{{
				Type:    "command",
				Command: buildVybeHookCommand("tool-success"),
				Timeout: 2000,
			}},
		},
		"SubagentStop": {
			Matcher: "",
			Hooks: []hookHandler{{
				Type:    "command",
				Command: buildVybeHookCommand("subagent-stop"),
				Timeout: 2000,
			}},
		},
		"SubagentStart": {
			Matcher: "",
			Hooks: []hookHandler{{
				Type:    "command",
				Command: buildVybeHookCommand("subagent-start"),
				Timeout: 2000,
			}},
		},
		"Stop": {
			Matcher: "",
			Hooks: []hookHandler{{
				Type:    "command",
				Command: buildVybeHookCommand("stop"),
				Timeout: 1000,
			}},
		},
	}
}

func vybeHookEventNames() []string {
	events := make([]string, 0, len(vybeHooks()))
	for name := range vybeHooks() {
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
	if err := os.MkdirAll(filepath.Dir(path), 0750); err != nil {
		return fmt.Errorf("create directory: %w", err)
	}

	return os.WriteFile(path, data, 0600)
}

// hasVybeHook checks if a hooks array already contains a vybe hook command.
func hasVybeHook(entries []any) bool {
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
			if isVybeHookCommand(cmd) {
				return true
			}
		}
	}
	return false
}

//nolint:gocyclo // recognizer checks multiple vybe command patterns across both legacy and current CLI shapes
func isVybeHookCommand(command string) bool {
	cmd := strings.TrimSpace(command)
	if cmd == "" {
		return false
	}
	parts := strings.Fields(cmd)
	if len(parts) < 3 {
		return false
	}

	execToken := strings.Trim(parts[0], "\"'")
	if filepath.Base(execToken) != "vybe" {
		return false
	}
	if parts[1] != "hook" {
		return false
	}

	sub := parts[2]
	return sub == "session-start" ||
		sub == "session-end" ||
		sub == "retrospective-worker" ||
		sub == "prompt" ||
		sub == "tool-failure" ||
		sub == "tool-success" ||
		sub == "checkpoint" ||
		sub == "task-completed" ||
		sub == "retrospective" ||
		sub == "retrospective-bg" ||
		sub == "subagent-stop" ||
		sub == "subagent-start" ||
		sub == "stop"
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

// upsertVybeHookEntry replaces any existing vybe hook entry or appends a new one.
// Non-vybe entries are preserved. Returns the updated slice and the outcome.
func upsertVybeHookEntry(existing []any, newEntry map[string]any) ([]any, installOutcome) {
	var kept []any
	hadVybe := false
	matchingVybe := false

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
		isVybe := false
		for _, h := range hooks {
			hMap, ok := h.(map[string]any)
			if !ok {
				continue
			}
			cmd, _ := hMap["command"].(string)
			if isVybeHookCommand(cmd) {
				isVybe = true
				break
			}
		}
		if isVybe {
			hadVybe = true
			if hookEntryEqual(entryObj, newEntry) {
				matchingVybe = true
			}
			continue // strip old vybe entry; re-appended below
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

// resolveTargetFlags returns (claude, opencode) based on explicit flag usage.
// Default: Claude only when no flags specified.
func resolveTargetFlags(cmd *cobra.Command) (claude bool, opencode bool, err error) {
	const claudeFlag = "claude"
	const opencodeFlag = "opencode"

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

//nolint:gocognit,gocyclo,funlen,nestif,revive // install command handles multiple targets (claude+opencode) with tty branching; splitting degrades cohesion
func newHookInstallCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "install",
		Short: "Install vybe hooks for Claude and/or OpenCode",
		Long: `Installs Claude Code hooks and/or an OpenCode bridge plugin.
Idempotent — safe to run multiple times. Existing hooks/plugins are preserved.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			installClaude, installOpenCode, err := resolveTargetFlags(cmd)
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
				if err := writeSettings(path, settings); err != nil {
					return cmdErr(err)
				}

				ensureHookAgentStateBestEffort("claude")

				sort.Strings(installed)
				sort.Strings(updated)
				sort.Strings(skipped)
				resp.Claude = &claudeResult{Path: path, Installed: installed, Updated: updated, Skipped: skipped}
			}

			if installOpenCode {
				path := opencodePluginPath()

				status := "installed"
				if existing, readErr := os.ReadFile(path); readErr == nil {
					if string(existing) == opencodeBridgePluginSource {
						status = "skipped"
					} else {
						// File differs — auto-overwrite with updated version
						status = "updated"
					}
				}

				if status == "installed" || status == "updated" {
					if err := os.MkdirAll(filepath.Dir(path), 0750); err != nil {
						return cmdErr(fmt.Errorf("create opencode plugin directory: %w", err))
					}
					if err := os.WriteFile(path, []byte(opencodeBridgePluginSource), 0600); err != nil {
						return cmdErr(fmt.Errorf("write opencode bridge plugin: %w", err))
					}
				}

				ensureHookAgentStateBestEffort("opencode-agent")

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
				default:
					parts = append(parts, "OpenCode bridge plugin already installed")
				}
			}
			if len(parts) > 0 {
				resp.Message = strings.Join(parts, "; ") + ". Run 'vybe status' to verify."
			}

			return output.PrintSuccess(resp)
		},
	}

	cmd.Flags().Bool("claude", false, "Install Claude Code hooks")
	cmd.Flags().Bool("opencode", false, "Install OpenCode bridge plugin")
	cmd.Flags().Bool("project", false, "Install Claude hooks in ./.claude/settings.json instead of ~/.claude/settings.json")

	return cmd
}

//nolint:gocognit,gocyclo,funlen,nestif,revive // uninstall command mirrors install branching; same rationale applies
func newHookUninstallCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "uninstall",
		Short: "Remove vybe hooks for Claude and/or OpenCode",
		Long:  `Removes Claude Code hook entries and/or OpenCode bridge plugin.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			uninstallClaude, uninstallOpenCode, err := resolveTargetFlags(cmd)
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

					for _, eventName := range vybeHookEventNames() {
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

							isVybe := false
							for _, h := range hooks {
								hMap, ok := h.(map[string]any)
								if !ok {
									continue
								}
								cmd, _ := hMap["command"].(string)
								if isVybeHookCommand(cmd) {
									isVybe = true
									break
								}
							}

							if !isVybe {
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
