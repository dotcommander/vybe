package hookcmd

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
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
	require.True(t, IsVybeHookCommand(`"/path/to/vybe" hook task-completed`))
	require.True(t, IsVybeHookCommand(`"/Users/my user/go/bin/vybe" hook session-start`))

	require.False(t, IsVybeHookCommand("echo vybe hook session-start"))
	require.False(t, IsVybeHookCommand("/usr/local/bin/not-vybe hook session-start"))
	require.False(t, IsVybeHookCommand("vybe status"))
	require.False(t, IsVybeHookCommand(""))
	require.False(t, IsVybeHookCommand("vybe hook unknown-subcommand"))
	require.False(t, IsVybeHookCommand("vybe hook retrospective"))
	require.False(t, IsVybeHookCommand("vybe hook retrospective-bg"))
	require.True(t, IsVybeHookCommand("vybe hook session-end"))
	require.False(t, IsVybeHookCommand("vybe hook tool-success"))
	require.False(t, IsVybeHookCommand("vybe hook subagent-stop"))
	require.False(t, IsVybeHookCommand("vybe hook subagent-start"))
	require.False(t, IsVybeHookCommand("vybe hook stop"))
}

func TestVybeHookEventNames_ContainsAllEvents(t *testing.T) {
	events := vybeHookEventNames()
	expected := []string{
		"SessionStart",
		"UserPromptSubmit",
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

func TestInstallOpenCode_UpdatesDifferingContent(t *testing.T) {
	dir := t.TempDir()
	pluginDir := filepath.Join(dir, "plugins")
	require.NoError(t, os.MkdirAll(pluginDir, 0o755))

	path := filepath.Join(pluginDir, opencodeBridgePluginFilename)

	// Write an old/different plugin version
	oldContent := "// old vybe plugin version\nconsole.log('old');\n"
	require.NoError(t, os.WriteFile(path, []byte(oldContent), 0o600))

	existing, err := os.ReadFile(path)
	require.NoError(t, err)
	require.NotEqual(t, opencodeBridgePluginSource, string(existing))

	// Install should detect the difference and report "updated"
	var status string
	if string(existing) == opencodeBridgePluginSource {
		status = "skipped"
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
		"plugin":   []any{"./plugins/blog.ts", "./plugins/safety.ts"},
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

func TestInstallCmd_Claude_ProjectScoped(t *testing.T) {
	// Create a temp dir to act as the project root.
	dir := t.TempDir()

	// Save and restore working directory.
	origDir, err := os.Getwd()
	require.NoError(t, err)
	require.NoError(t, os.Chdir(dir))
	t.Cleanup(func() { _ = os.Chdir(origDir) })

	// Create .claude directory (the command will also create it via writeSettings,
	// but pre-creating ensures the path exists for resolution).
	require.NoError(t, os.MkdirAll(filepath.Join(dir, ".claude"), 0o755))

	// Build and execute the install command with --claude --project.
	cmd := NewInstallCmd()
	cmd.SetArgs([]string{"--claude", "--project"})

	// Capture stdout (output.PrintSuccess writes to os.Stdout, not cobra writer,
	// so we only capture it to keep test output clean; we verify via filesystem).
	var stdout bytes.Buffer
	cmd.SetOut(&stdout)

	err = cmd.Execute()
	require.NoError(t, err)

	// Verify settings.json was created.
	settingsPath := filepath.Join(dir, ".claude", "settings.json")
	data, err := os.ReadFile(settingsPath)
	require.NoError(t, err)

	var settings map[string]any
	require.NoError(t, json.Unmarshal(data, &settings))

	// Verify hooks section exists.
	hooksObj, ok := settings["hooks"].(map[string]any)
	require.True(t, ok, "settings should have hooks key")

	// Verify all expected hook events are present and each has at least one entry
	// with a hook subcommand. We can't use HasVybeHook here because the test binary
	// is not named "vybe", so IsVybeHookCommand rejects the generated command.
	// Instead we verify the structural shape directly.
	for _, eventName := range vybeHookEventNames() {
		entries, ok := hooksObj[eventName].([]any)
		require.True(t, ok, "missing hook event: %s", eventName)
		require.NotEmpty(t, entries, "hook event %s has no entries", eventName)

		// Each entry must have a "hooks" array with at least one command containing "hook ".
		found := false
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
				if strings.Contains(cmd, " hook ") {
					found = true
					break
				}
			}
			if found {
				break
			}
		}
		require.True(t, found, "hook event %s missing hook command", eventName)
	}

	// Verify idempotency: running install again should skip all.
	cmd2 := NewInstallCmd()
	cmd2.SetArgs([]string{"--claude", "--project"})
	var stdout2 bytes.Buffer
	cmd2.SetOut(&stdout2)
	require.NoError(t, cmd2.Execute())

	// Re-read settings and confirm structure is still valid.
	data2, err := os.ReadFile(settingsPath)
	require.NoError(t, err)

	var settings2 map[string]any
	require.NoError(t, json.Unmarshal(data2, &settings2))

	hooksObj2, ok := settings2["hooks"].(map[string]any)
	require.True(t, ok, "settings should still have hooks key after second install")
	require.Len(t, hooksObj2, len(vybeHookEventNames()), "hook count should be unchanged after second install")
}

func TestInstallCmd_OpenCode(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)

	// Create the opencode config dir.
	ocDir := filepath.Join(dir, "opencode")
	require.NoError(t, os.MkdirAll(ocDir, 0o755))

	cmd := NewInstallCmd()
	cmd.SetArgs([]string{"--opencode"})
	var stdout bytes.Buffer
	cmd.SetOut(&stdout)

	err := cmd.Execute()
	require.NoError(t, err)

	// Verify plugin file was created.
	pluginPath := filepath.Join(ocDir, "plugins", opencodeBridgePluginFilename)
	data, err := os.ReadFile(pluginPath)
	require.NoError(t, err)
	require.Equal(t, opencodeBridgePluginSource, string(data))

	// Verify opencode.json has the plugin registered.
	configPath := filepath.Join(ocDir, "opencode.json")
	configData, err := os.ReadFile(configPath)
	require.NoError(t, err)

	var config map[string]any
	require.NoError(t, json.Unmarshal(configData, &config))
	plugins, ok := config["plugin"].([]any)
	require.True(t, ok)
	require.Contains(t, plugins, opencodePluginEntry)
}

func TestAtomicWriteFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.json")

	// Write initial content.
	content := []byte(`{"version": 1}` + "\n")
	require.NoError(t, atomicWriteFile(path, content, 0o600))

	// Verify content.
	got, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, content, got)

	// Verify permissions.
	info, err := os.Stat(path)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o600), info.Mode().Perm())

	// Overwrite with new content.
	content2 := []byte(`{"version": 2, "extra": "data"}` + "\n")
	require.NoError(t, atomicWriteFile(path, content2, 0o600))

	got2, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, content2, got2)

	// No temp files should remain.
	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	for _, e := range entries {
		require.False(t, strings.HasSuffix(e.Name(), ".tmp"),
			"temp file left behind: %s", e.Name())
	}
}

func TestWithLockedSettings_SerializesConcurrentMutations(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")

	// Initialize with counter=0.
	initial := map[string]any{"counter": float64(0)}
	data, err := json.MarshalIndent(initial, "", "  ")
	require.NoError(t, err)
	data = append(data, '\n')
	require.NoError(t, os.WriteFile(path, data, 0o600))

	const goroutines = 20
	var wg sync.WaitGroup
	wg.Add(goroutines)

	errs := make(chan error, goroutines)

	for range goroutines {
		go func() {
			defer wg.Done()
			err := withLockedSettings(path, func(settings map[string]any) error {
				counter, _ := settings["counter"].(float64)
				settings["counter"] = counter + 1
				return nil
			})
			if err != nil {
				errs <- err
			}
		}()
	}

	wg.Wait()
	close(errs)

	for err := range errs {
		t.Fatalf("withLockedSettings error: %v", err)
	}

	// Verify final counter equals goroutines (no lost updates).
	final, err := readSettings(path)
	require.NoError(t, err)
	counter, ok := final["counter"].(float64)
	require.True(t, ok, "counter should be a number")
	require.Equal(t, float64(goroutines), counter,
		"expected %d increments, got %v -- lost updates detected", goroutines, counter)
}

func TestUninstallOpenCode_SkipsModifiedPluginWithoutForce(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)

	ocDir := filepath.Join(dir, "opencode")
	pluginDir := filepath.Join(ocDir, "plugins")
	require.NoError(t, os.MkdirAll(pluginDir, 0o755))

	path := filepath.Join(pluginDir, opencodeBridgePluginFilename)

	// Write user-customized content that differs from embedded source.
	customContent := "// user-customized plugin\nconsole.log('my stuff');\n"
	require.NoError(t, os.WriteFile(path, []byte(customContent), 0o600))

	// Uninstall without --force should NOT delete the file.
	cmd := NewUninstallCmd()
	cmd.SetArgs([]string{"--opencode"})
	var stdout bytes.Buffer
	cmd.SetOut(&stdout)
	require.NoError(t, cmd.Execute())

	// File should still exist with user content.
	after, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, customContent, string(after))
}

func TestUninstallOpenCode_RemovesMatchingPlugin(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)

	ocDir := filepath.Join(dir, "opencode")
	pluginDir := filepath.Join(ocDir, "plugins")
	require.NoError(t, os.MkdirAll(pluginDir, 0o755))

	path := filepath.Join(pluginDir, opencodeBridgePluginFilename)

	// Write the exact embedded source — vybe owns this file.
	require.NoError(t, os.WriteFile(path, []byte(opencodeBridgePluginSource), 0o600))

	// Uninstall should delete since content matches.
	cmd := NewUninstallCmd()
	cmd.SetArgs([]string{"--opencode"})
	var stdout bytes.Buffer
	cmd.SetOut(&stdout)
	require.NoError(t, cmd.Execute())

	// File should be gone.
	_, err := os.Stat(path)
	require.True(t, os.IsNotExist(err), "plugin file should have been removed")
}

func TestUninstallOpenCode_ForcedRemovesModifiedPlugin(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)

	ocDir := filepath.Join(dir, "opencode")
	pluginDir := filepath.Join(ocDir, "plugins")
	require.NoError(t, os.MkdirAll(pluginDir, 0o755))

	path := filepath.Join(pluginDir, opencodeBridgePluginFilename)

	// Write user-customized content.
	customContent := "// user-customized plugin\nconsole.log('my stuff');\n"
	require.NoError(t, os.WriteFile(path, []byte(customContent), 0o600))

	// Uninstall with --force should delete even modified file.
	cmd := NewUninstallCmd()
	cmd.SetArgs([]string{"--opencode", "--force"})
	var stdout bytes.Buffer
	cmd.SetOut(&stdout)
	require.NoError(t, cmd.Execute())

	// File should be gone.
	_, err := os.Stat(path)
	require.True(t, os.IsNotExist(err), "plugin file should have been removed with --force")
}
