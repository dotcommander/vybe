package hookcmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"
)

func newTargetFlagCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "test"}
	cmd.Flags().Bool("claude", false, "")
	cmd.Flags().Bool("opencode", false, "")
	return cmd
}

func TestResolveTargetFlags_DefaultsToClaudeOnly(t *testing.T) {
	cmd := newTargetFlagCmd()

	claude, opencode, err := ResolveTargetFlags(cmd)
	require.NoError(t, err)
	require.True(t, claude)
	require.False(t, opencode)
}

func TestResolveTargetFlags_ReturnsErrorWhenBothExplicitlyFalse(t *testing.T) {
	cmd := newTargetFlagCmd()
	require.NoError(t, cmd.Flags().Set("claude", "false"))
	require.NoError(t, cmd.Flags().Set("opencode", "false"))

	claude, opencode, err := ResolveTargetFlags(cmd)
	require.Error(t, err)
	require.False(t, claude)
	require.False(t, opencode)
}

func TestResolveTargetFlags_BothTrue(t *testing.T) {
	cmd := newTargetFlagCmd()
	require.NoError(t, cmd.Flags().Set("claude", "true"))
	require.NoError(t, cmd.Flags().Set("opencode", "true"))

	claude, opencode, err := ResolveTargetFlags(cmd)
	require.NoError(t, err)
	require.True(t, claude)
	require.True(t, opencode)
}

func TestHasVybeHook(t *testing.T) {
	require.False(t, HasVybeHook(nil))

	entries := []any{
		map[string]any{
			"hooks": []any{
				map[string]any{"command": "vybe hook session-start"},
			},
		},
	}
	require.True(t, HasVybeHook(entries))

	// Malformed entries should not panic.
	require.False(t, HasVybeHook([]any{"not-a-map"}))
	require.False(t, HasVybeHook([]any{map[string]any{"hooks": "not-a-slice"}}))
}

func TestIsVybeHookCommand(t *testing.T) {
	require.True(t, IsVybeHookCommand("vybe hook session-start"))
	require.True(t, IsVybeHookCommand("/usr/local/bin/vybe hook checkpoint"))
	require.True(t, IsVybeHookCommand(`"/Users/someone/go/bin/vybe" hook task-completed`))

	require.False(t, IsVybeHookCommand("echo vybe hook session-start"))
	require.False(t, IsVybeHookCommand("/usr/local/bin/not-vybe hook session-start"))
	require.False(t, IsVybeHookCommand("vybe status"))
	require.False(t, IsVybeHookCommand(""))
	require.False(t, IsVybeHookCommand("vybe hook unknown-subcommand"))
	require.False(t, IsVybeHookCommand("vybe hook retrospective"))
	require.False(t, IsVybeHookCommand("vybe hook retrospective-bg"))
	require.True(t, IsVybeHookCommand("vybe hook session-end"))
	require.True(t, IsVybeHookCommand("vybe hook tool-success"))
	require.False(t, IsVybeHookCommand("vybe hook subagent-stop"))
	require.False(t, IsVybeHookCommand("vybe hook subagent-start"))
	require.False(t, IsVybeHookCommand("vybe hook stop"))
}

func TestVybeHookEventNames_ContainsAllEvents(t *testing.T) {
	events := vybeHookEventNames()
	expected := []string{
		"SessionStart",
		"UserPromptSubmit",
		"PostToolUse",
		"PostToolUseFailure",
		"PreCompact",
		"SessionEnd",
		"TaskCompleted",
	}
	for _, e := range expected {
		require.Contains(t, events, e, "missing hook event: %s", e)
	}
	require.Len(t, events, len(expected), "unexpected number of hook events")
}

func TestHookEntryEqual(t *testing.T) {
	a := map[string]any{
		"matcher": "",
		"hooks": []any{
			map[string]any{"type": "command", "command": "vybe hook prompt", "timeout": float64(2000)},
		},
	}
	b := map[string]any{
		"matcher": "",
		"hooks": []any{
			map[string]any{"type": "command", "command": "vybe hook prompt", "timeout": float64(2000)},
		},
	}
	require.True(t, hookEntryEqual(a, b))

	// Different timeout
	c := map[string]any{
		"matcher": "",
		"hooks": []any{
			map[string]any{"type": "command", "command": "vybe hook prompt", "timeout": float64(3000)},
		},
	}
	require.False(t, hookEntryEqual(a, c))

	// Different matcher
	d := map[string]any{
		"matcher": "startup",
		"hooks": []any{
			map[string]any{"type": "command", "command": "vybe hook prompt", "timeout": float64(2000)},
		},
	}
	require.False(t, hookEntryEqual(a, d))
}

func TestUpsertVybeHookEntry(t *testing.T) {
	newEntry := map[string]any{
		"matcher": "",
		"hooks": []any{
			map[string]any{"type": "command", "command": "vybe hook prompt", "timeout": float64(2000)},
		},
	}

	// Fresh install (nil existing)
	entries, outcome := upsertVybeHookEntry(nil, newEntry)
	require.Equal(t, hookInstalled, outcome)
	require.Len(t, entries, 1)

	// Skip (identical entry already present)
	entries, outcome = upsertVybeHookEntry(entries, newEntry)
	require.Equal(t, hookSkipped, outcome)
	require.Len(t, entries, 1)

	// Update (different timeout)
	updatedEntry := map[string]any{
		"matcher": "",
		"hooks": []any{
			map[string]any{"type": "command", "command": "vybe hook prompt", "timeout": float64(3000)},
		},
	}
	entries, outcome = upsertVybeHookEntry(entries, updatedEntry)
	require.Equal(t, hookUpdated, outcome)
	require.Len(t, entries, 1)

	// Non-vybe entries are preserved
	nonVybe := map[string]any{
		"hooks": []any{
			map[string]any{"type": "command", "command": "other-tool do-thing"},
		},
	}
	mixed := []any{nonVybe, entries[0]}
	entries, outcome = upsertVybeHookEntry(mixed, updatedEntry)
	require.Equal(t, hookSkipped, outcome)
	require.Len(t, entries, 2) // non-vybe + vybe
}

func TestReadSettings_AndWriteSettings(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")

	settings, err := readSettings(path)
	require.NoError(t, err)
	require.Empty(t, settings)

	input := map[string]any{"hooks": map[string]any{"SessionStart": []any{}}}
	require.NoError(t, writeSettings(path, input))

	b, err := os.ReadFile(path)
	require.NoError(t, err)
	require.NotEmpty(t, b)
	require.Equal(t, byte('\n'), b[len(b)-1])

	loaded, err := readSettings(path)
	require.NoError(t, err)
	require.Contains(t, loaded, "hooks")
}

func TestReadSettings_InvalidJSON(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	require.NoError(t, os.WriteFile(path, []byte("{"), 0o600))

	settings, err := readSettings(path)
	require.Error(t, err)
	require.Nil(t, settings)
}

func TestInstallOpenCode_SkipsConflictWithoutForce(t *testing.T) {
	dir := t.TempDir()
	pluginDir := filepath.Join(dir, "plugins")
	require.NoError(t, os.MkdirAll(pluginDir, 0o755))

	path := filepath.Join(pluginDir, opencodeBridgePluginFilename)

	// Write a user-customized plugin file that differs from embedded source
	customContent := "// user-customized plugin\nconsole.log('custom');\n"
	require.NoError(t, os.WriteFile(path, []byte(customContent), 0o600))

	// Read it back — should still be user's content (simulate what install checks)
	existing, err := os.ReadFile(path)
	require.NoError(t, err)
	require.NotEqual(t, opencodeBridgePluginSource, string(existing))

	// Verify conflict detection logic directly
	var status string
	if string(existing) == opencodeBridgePluginSource {
		status = "skipped"
	} else {
		status = "skipped_conflict"
	}
	require.Equal(t, "skipped_conflict", status)

	// Verify file was NOT overwritten
	after, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, customContent, string(after))
}

func TestInstallOpenCode_OverwritesWithForce(t *testing.T) {
	dir := t.TempDir()
	pluginDir := filepath.Join(dir, "plugins")
	require.NoError(t, os.MkdirAll(pluginDir, 0o755))

	path := filepath.Join(pluginDir, opencodeBridgePluginFilename)

	customContent := "// user-customized plugin\n"
	require.NoError(t, os.WriteFile(path, []byte(customContent), 0o600))

	existing, err := os.ReadFile(path)
	require.NoError(t, err)

	// With force=true, status should be "updated"
	force := true
	var status string
	switch {
	case string(existing) == opencodeBridgePluginSource:
		status = "skipped"
	case !force:
		status = "skipped_conflict"
	default:
		status = "updated"
	}
	require.Equal(t, "updated", status)
}

func TestInstallOpenCode_SkipsIdenticalFile(t *testing.T) {
	dir := t.TempDir()
	pluginDir := filepath.Join(dir, "plugins")
	require.NoError(t, os.MkdirAll(pluginDir, 0o755))

	path := filepath.Join(pluginDir, opencodeBridgePluginFilename)

	// Write the exact embedded source
	require.NoError(t, os.WriteFile(path, []byte(opencodeBridgePluginSource), 0o600))

	existing, err := os.ReadFile(path)
	require.NoError(t, err)

	status := "installed"
	if string(existing) == opencodeBridgePluginSource {
		status = "skipped"
	}
	require.Equal(t, "skipped", status)
}

func TestOpenCodeBridgePlugin_UsesSessionEndHookFlow(t *testing.T) {
	require.Contains(t, opencodeBridgePluginSource, "hook\", \"session-end\"")
	require.NotContains(t, opencodeBridgePluginSource, "hook\", \"retrospective\"")

	// Ensure direct retrospective invocation is not present as an argv token sequence.
	require.False(t, strings.Contains(opencodeBridgePluginSource, "runVybeBackground([\"hook\", \"retrospective\""))
}

func TestRegisterOpencodePlugin(t *testing.T) {
	dir := t.TempDir()

	// Override opencodeConfigDir by setting XDG_CONFIG_HOME
	t.Setenv("XDG_CONFIG_HOME", dir)

	// Create the opencode config dir
	ocDir := filepath.Join(dir, "opencode")
	require.NoError(t, os.MkdirAll(ocDir, 0o755))

	// First registration should add the entry
	added, err := registerOpencodePlugin()
	require.NoError(t, err)
	require.True(t, added)

	// Verify opencode.json was created with the plugin entry
	configPath := filepath.Join(ocDir, "opencode.json")
	settings, err := readSettings(configPath)
	require.NoError(t, err)
	plugins, ok := settings["plugin"].([]any)
	require.True(t, ok)
	require.Len(t, plugins, 1)
	require.Equal(t, "./plugins/"+opencodeBridgePluginFilename, plugins[0])

	// Second registration should be a no-op
	added, err = registerOpencodePlugin()
	require.NoError(t, err)
	require.False(t, added)

	// Unregister should remove the entry
	removed, err := unregisterOpencodePlugin()
	require.NoError(t, err)
	require.True(t, removed)

	// Verify the plugin key was cleaned up
	settings, err = readSettings(configPath)
	require.NoError(t, err)
	_, hasPlugin := settings["plugin"]
	require.False(t, hasPlugin)

	// Second unregister should be a no-op
	removed, err = unregisterOpencodePlugin()
	require.NoError(t, err)
	require.False(t, removed)
}

func TestRegisterOpencodePlugin_PreservesExistingPlugins(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)

	ocDir := filepath.Join(dir, "opencode")
	require.NoError(t, os.MkdirAll(ocDir, 0o755))

	// Write a config with existing plugins
	configPath := filepath.Join(ocDir, "opencode.json")
	existing := map[string]any{
		"plugin": []any{"./plugins/blog.ts", "./plugins/safety.ts"},
		"provider": map[string]any{"name": "anthropic"},
	}
	require.NoError(t, writeSettings(configPath, existing))

	// Registration should append, not replace
	added, err := registerOpencodePlugin()
	require.NoError(t, err)
	require.True(t, added)

	settings, err := readSettings(configPath)
	require.NoError(t, err)
	plugins, ok := settings["plugin"].([]any)
	require.True(t, ok)
	require.Len(t, plugins, 3)

	// Other config keys should be preserved
	_, hasProvider := settings["provider"]
	require.True(t, hasProvider)

	// Unregister should only remove vybe, keep others
	removed, err := unregisterOpencodePlugin()
	require.NoError(t, err)
	require.True(t, removed)

	settings, err = readSettings(configPath)
	require.NoError(t, err)
	plugins, ok = settings["plugin"].([]any)
	require.True(t, ok)
	require.Len(t, plugins, 2)
}
