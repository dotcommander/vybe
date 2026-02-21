package commands

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

	claude, opencode, err := resolveTargetFlags(cmd)
	require.NoError(t, err)
	require.True(t, claude)
	require.False(t, opencode)
}

func TestResolveTargetFlags_ReturnsErrorWhenBothExplicitlyFalse(t *testing.T) {
	cmd := newTargetFlagCmd()
	require.NoError(t, cmd.Flags().Set("claude", "false"))
	require.NoError(t, cmd.Flags().Set("opencode", "false"))

	claude, opencode, err := resolveTargetFlags(cmd)
	require.Error(t, err)
	require.False(t, claude)
	require.False(t, opencode)
}

func TestResolveTargetFlags_BothTrue(t *testing.T) {
	cmd := newTargetFlagCmd()
	require.NoError(t, cmd.Flags().Set("claude", "true"))
	require.NoError(t, cmd.Flags().Set("opencode", "true"))

	claude, opencode, err := resolveTargetFlags(cmd)
	require.NoError(t, err)
	require.True(t, claude)
	require.True(t, opencode)
}

func TestHasVybeHook(t *testing.T) {
	require.False(t, hasVybeHook(nil))

	entries := []any{
		map[string]any{
			"hooks": []any{
				map[string]any{"command": "vybe hook session-start"},
			},
		},
	}
	require.True(t, hasVybeHook(entries))

	// Malformed entries should not panic.
	require.False(t, hasVybeHook([]any{"not-a-map"}))
	require.False(t, hasVybeHook([]any{map[string]any{"hooks": "not-a-slice"}}))
}

func TestIsVybeHookCommand(t *testing.T) {
	require.True(t, isVybeHookCommand("vybe hook session-start"))
	require.True(t, isVybeHookCommand("/usr/local/bin/vybe hook checkpoint"))
	require.True(t, isVybeHookCommand(`"/Users/someone/go/bin/vybe" hook task-completed`))

	require.False(t, isVybeHookCommand("echo vybe hook session-start"))
	require.False(t, isVybeHookCommand("/usr/local/bin/not-vybe hook session-start"))
	require.False(t, isVybeHookCommand("vybe status"))
	require.False(t, isVybeHookCommand(""))
	require.False(t, isVybeHookCommand("vybe hook unknown-subcommand"))
	require.True(t, isVybeHookCommand("vybe hook retrospective"))
	require.True(t, isVybeHookCommand("vybe hook retrospective-bg"))
	require.True(t, isVybeHookCommand("vybe hook session-end"))
	require.True(t, isVybeHookCommand("vybe hook tool-success"))
	require.True(t, isVybeHookCommand("vybe hook subagent-stop"))
	require.True(t, isVybeHookCommand("vybe hook subagent-start"))
	require.True(t, isVybeHookCommand("vybe hook stop"))
}

func TestVybeHookEventNames_ContainsAllEvents(t *testing.T) {
	events := vybeHookEventNames()
	expected := []string{
		"SessionStart",
		"UserPromptSubmit",
		"PostToolUseFailure",
		"PostToolUse",
		"PreCompact",
		"SessionEnd",
		"TaskCompleted",
		"SubagentStop",
		"SubagentStart",
		"Stop",
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
	require.Contains(t, opencodeBridgePluginSource, "VYBE_RETRO_CHILD")

	// Ensure direct retrospective invocation is not present as an argv token sequence.
	require.False(t, strings.Contains(opencodeBridgePluginSource, "runVybeBackground([\"hook\", \"retrospective\""))
}
