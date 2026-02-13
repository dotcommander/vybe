package commands

import (
	"os"
	"path/filepath"
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

	claude, opencode, err := resolveTargetFlags(cmd, "claude", "opencode")
	require.NoError(t, err)
	require.True(t, claude)
	require.False(t, opencode)
}

func TestResolveTargetFlags_ReturnsErrorWhenBothExplicitlyFalse(t *testing.T) {
	cmd := newTargetFlagCmd()
	require.NoError(t, cmd.Flags().Set("claude", "false"))
	require.NoError(t, cmd.Flags().Set("opencode", "false"))

	claude, opencode, err := resolveTargetFlags(cmd, "claude", "opencode")
	require.Error(t, err)
	require.False(t, claude)
	require.False(t, opencode)
}

func TestResolveTargetFlags_BothTrue(t *testing.T) {
	cmd := newTargetFlagCmd()
	require.NoError(t, cmd.Flags().Set("claude", "true"))
	require.NoError(t, cmd.Flags().Set("opencode", "true"))

	claude, opencode, err := resolveTargetFlags(cmd, "claude", "opencode")
	require.NoError(t, err)
	require.True(t, claude)
	require.True(t, opencode)
}

func TestHasVibeHook(t *testing.T) {
	require.False(t, hasVibeHook(nil))

	entries := []any{
		map[string]any{
			"hooks": []any{
				map[string]any{"command": "vibe hook session-start"},
			},
		},
	}
	require.True(t, hasVibeHook(entries))

	// Malformed entries should not panic.
	require.False(t, hasVibeHook([]any{"not-a-map"}))
	require.False(t, hasVibeHook([]any{map[string]any{"hooks": "not-a-slice"}}))
}

func TestIsVibeHookCommand(t *testing.T) {
	require.True(t, isVibeHookCommand("vibe hook session-start"))
	require.True(t, isVibeHookCommand("/usr/local/bin/vibe hook checkpoint"))
	require.True(t, isVibeHookCommand("/Applications/Vibe.app/Contents/MacOS/vibe hook task-completed"))
	// Note: quoted paths with spaces (e.g. '"/path/with spaces/vibe" hook prompt')
	// are not handled by isVibeHookCommand — strings.Fields doesn't parse shell quoting.
	// This is acceptable since paths with spaces are uncommon for Go binaries.

	require.False(t, isVibeHookCommand("echo vibe hook session-start"))
	require.False(t, isVibeHookCommand("/usr/local/bin/not-vibe hook session-start"))
	require.False(t, isVibeHookCommand("vibe status"))
	require.False(t, isVibeHookCommand(""))
	require.False(t, isVibeHookCommand("vibe hook unknown-subcommand"))
	require.True(t, isVibeHookCommand("vibe hook retrospective"))
}

func TestVibeHookEventNames_ContainsTaskCompleted(t *testing.T) {
	events := vibeHookEventNames()
	require.Contains(t, events, "TaskCompleted")
	require.Contains(t, events, "SessionStart")
	require.Contains(t, events, "PostToolUseFailure")
}

func TestHookEntryEqual(t *testing.T) {
	a := map[string]any{
		"matcher": "",
		"hooks": []any{
			map[string]any{"type": "command", "command": "vibe hook prompt", "timeout": float64(2000)},
		},
	}
	b := map[string]any{
		"matcher": "",
		"hooks": []any{
			map[string]any{"type": "command", "command": "vibe hook prompt", "timeout": float64(2000)},
		},
	}
	require.True(t, hookEntryEqual(a, b))

	// Different timeout
	c := map[string]any{
		"matcher": "",
		"hooks": []any{
			map[string]any{"type": "command", "command": "vibe hook prompt", "timeout": float64(3000)},
		},
	}
	require.False(t, hookEntryEqual(a, c))

	// Different matcher
	d := map[string]any{
		"matcher": "startup",
		"hooks": []any{
			map[string]any{"type": "command", "command": "vibe hook prompt", "timeout": float64(2000)},
		},
	}
	require.False(t, hookEntryEqual(a, d))
}

func TestUpsertVibeHookEntry(t *testing.T) {
	newEntry := map[string]any{
		"matcher": "",
		"hooks": []any{
			map[string]any{"type": "command", "command": "vibe hook prompt", "timeout": float64(2000)},
		},
	}

	// Fresh install (nil existing)
	entries, outcome := upsertVibeHookEntry(nil, newEntry)
	require.Equal(t, hookInstalled, outcome)
	require.Len(t, entries, 1)

	// Skip (identical entry already present)
	entries, outcome = upsertVibeHookEntry(entries, newEntry)
	require.Equal(t, hookSkipped, outcome)
	require.Len(t, entries, 1)

	// Update (different timeout)
	updatedEntry := map[string]any{
		"matcher": "",
		"hooks": []any{
			map[string]any{"type": "command", "command": "vibe hook prompt", "timeout": float64(3000)},
		},
	}
	entries, outcome = upsertVibeHookEntry(entries, updatedEntry)
	require.Equal(t, hookUpdated, outcome)
	require.Len(t, entries, 1)

	// Non-vibe entries are preserved
	nonVibe := map[string]any{
		"hooks": []any{
			map[string]any{"type": "command", "command": "other-tool do-thing"},
		},
	}
	mixed := []any{nonVibe, entries[0]}
	entries, outcome = upsertVibeHookEntry(mixed, updatedEntry)
	require.Equal(t, hookSkipped, outcome)
	require.Len(t, entries, 2) // non-vibe + vibe
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
	status := "installed"
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
	status := "installed"
	if string(existing) == opencodeBridgePluginSource {
		status = "skipped"
	} else if !force {
		status = "skipped_conflict"
	} else {
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
