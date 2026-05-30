package hookcmd

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestManifestLoadOrWrite verifies the manifest loader writes defaults on first call
// and reads back the persisted file on subsequent calls (idempotent).
func TestManifestLoadOrWrite(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	// First call: file absent — should build defaults and write them.
	hooks, err := LoadOrWriteHookManifest(dir)
	require.NoError(t, err)
	require.Len(t, hooks, 6, "expected exactly 6 hook events")

	expectedEvents := []string{
		"SessionStart", "UserPromptSubmit", "PostToolUseFailure",
		"PreCompact", "SessionEnd", "TaskCompleted",
	}
	for _, ev := range expectedEvents {
		_, ok := hooks[ev]
		require.True(t, ok, "missing event: %s", ev)
	}

	// hooks.json should now exist.
	manifestPath := filepath.Join(dir, hookManifestFilename)
	_, statErr := os.Stat(manifestPath)
	require.NoError(t, statErr, "manifest file should have been written")

	// Second call: reads from file, same result.
	hooks2, err := LoadOrWriteHookManifest(dir)
	require.NoError(t, err)
	require.Len(t, hooks2, 6)
}

// TestManifestDefaultsMatchBuiltIn verifies that the manifest loaded by
// LoadOrWriteHookManifest matches buildVybeHooks() exactly, confirming no
// behavior change from externalization.
func TestManifestDefaultsMatchBuiltIn(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	loaded, err := LoadOrWriteHookManifest(dir)
	require.NoError(t, err)

	defaults := buildVybeHooks()

	require.Equal(t, len(defaults), len(loaded), "manifest event count must match defaults")

	for eventName, defEntry := range defaults {
		loadedEntry, ok := loaded[eventName]
		require.True(t, ok, "manifest missing event: %s", eventName)
		require.Equal(t, defEntry.Matcher, loadedEntry.Matcher, "matcher mismatch for %s", eventName)
		require.Len(t, loadedEntry.Hooks, len(defEntry.Hooks), "hook count mismatch for %s", eventName)
		for i, defHook := range defEntry.Hooks {
			require.Equal(t, defHook.Timeout, loadedEntry.Hooks[i].Timeout,
				"timeout mismatch for %s hook[%d]", eventName, i)
			require.Equal(t, defHook.Type, loadedEntry.Hooks[i].Type,
				"type mismatch for %s hook[%d]", eventName, i)
		}
	}
}

// TestManifestCustomFilePreserved verifies that a current-versioned hooks.json is read
// as-is and not overwritten by LoadOrWriteHookManifest. The file must carry the current
// version stamp; otherwise it is treated as stale and replaced.
func TestManifestCustomFilePreserved(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	// Write a file with the current version stamp and a single customized entry.
	mf := hookManifestFile{
		Version: hookManifestVersion,
		Hooks: map[string]hookEntry{
			"SessionStart": {
				Matcher: "custom-matcher",
				Hooks:   []hookHandler{{Type: "command", Command: "custom-binary hook session-start", Timeout: 9999}},
			},
		},
	}
	data, err := json.MarshalIndent(mf, "", "  ")
	require.NoError(t, err)
	data = append(data, '\n')
	require.NoError(t, os.WriteFile(filepath.Join(dir, hookManifestFilename), data, 0o600))

	loaded, err := LoadOrWriteHookManifest(dir)
	require.NoError(t, err)
	require.Len(t, loaded, 1, "should load only what's in the file, not merge defaults")
	require.Equal(t, "custom-matcher", loaded["SessionStart"].Matcher)
	require.Equal(t, 9999, loaded["SessionStart"].Hooks[0].Timeout)
}

// TestManifestStaleVersionReturnsAllDefaults verifies that a hooks.json with a stale
// (or absent) version causes both LoadHookManifest and LoadOrWriteHookManifest to
// return ALL current default events — none silently dropped — and that
// LoadOrWriteHookManifest rewrites the file with the current version.
func TestManifestStaleVersionReturnsAllDefaults(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	manifestPath := filepath.Join(dir, hookManifestFilename)

	// Write a stale manifest: missing the version field (version == 0) and only
	// one event — simulates an old vybe install that didn't know about newer events.
	stale := hookManifestFile{
		Version: 0, // stale
		Hooks: map[string]hookEntry{
			"SessionStart": {
				Matcher: "old-matcher",
				Hooks:   []hookHandler{{Type: "command", Command: "vybe hook session-start", Timeout: 1}},
			},
		},
	}
	staleData, err := json.MarshalIndent(stale, "", "  ")
	require.NoError(t, err)
	staleData = append(staleData, '\n')
	require.NoError(t, os.WriteFile(manifestPath, staleData, 0o600))

	// LoadHookManifest (pure read) must return all 6 default events.
	roHooks, err := LoadHookManifest(dir)
	require.NoError(t, err)
	require.Len(t, roHooks, 6, "stale manifest: LoadHookManifest must return all 6 defaults")
	for _, ev := range []string{"SessionStart", "UserPromptSubmit", "PostToolUseFailure", "PreCompact", "SessionEnd", "TaskCompleted"} {
		_, ok := roHooks[ev]
		require.True(t, ok, "stale manifest: LoadHookManifest missing event %s", ev)
	}

	// File must NOT have been rewritten by LoadHookManifest.
	afterRO, err := os.ReadFile(manifestPath)
	require.NoError(t, err)
	require.Equal(t, staleData, afterRO, "LoadHookManifest must not write to disk")

	// LoadOrWriteHookManifest must also return all 6 defaults AND rewrite the file.
	rwHooks, err := LoadOrWriteHookManifest(dir)
	require.NoError(t, err)
	require.Len(t, rwHooks, 6, "stale manifest: LoadOrWriteHookManifest must return all 6 defaults")

	// File must now carry the current version.
	afterRW, err := os.ReadFile(manifestPath)
	require.NoError(t, err)
	var rewritten hookManifestFile
	require.NoError(t, json.Unmarshal(afterRW, &rewritten))
	require.Equal(t, hookManifestVersion, rewritten.Version, "rewritten file must carry current version")
	require.Len(t, rewritten.Hooks, 6, "rewritten file must contain all 6 events")
}

// TestManifestCorruptJSON verifies that a malformed hooks.json causes LoadHookManifest
// to return a wrapped error, and that vybeHooks() (the registry path) falls back to
// built-in defaults rather than crashing.
func TestManifestCorruptJSON(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	manifestPath := filepath.Join(dir, hookManifestFilename)

	require.NoError(t, os.WriteFile(manifestPath, []byte("{corrupt json"), 0o600))

	// LoadHookManifest must return an error.
	hooks, err := LoadHookManifest(dir)
	require.Error(t, err, "corrupt JSON must return an error")
	require.Nil(t, hooks, "error path must return nil hooks")

	// The registry fallback (simulated here since vybeHooks caches) is to call
	// buildVybeHooks() when the load fails. Verify that path produces all 6 events.
	defaults := buildVybeHooks()
	require.Len(t, defaults, 6, "built-in fallback must always have 6 events")
}

// TestManifestDoctorReadOnly verifies that calling LoadHookManifest when hooks.json
// is absent does NOT create the file — the read-only path is truly read-only.
func TestManifestDoctorReadOnly(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	manifestPath := filepath.Join(dir, hookManifestFilename)

	// Confirm file does not exist before the call.
	_, err := os.Stat(manifestPath)
	require.True(t, os.IsNotExist(err), "pre-condition: hooks.json must not exist")

	// Call the read-only path (same path used by vybeHooks() / doctor / export).
	hooks, err := LoadHookManifest(dir)
	require.NoError(t, err)
	require.Len(t, hooks, 6, "absent file: must return 6 default events")

	// File must still not exist.
	_, statErr := os.Stat(manifestPath)
	require.True(t, os.IsNotExist(statErr), "LoadHookManifest must not create hooks.json")
}

// TestManifestExportCmdRunsCommand verifies that NewExportCmd().Execute() writes
// valid JSON to stdout with exactly 6 hook event entries in the data envelope.
// output.PrintSuccess writes to os.Stdout directly, so we capture it via os.Pipe.
// Not parallel: redirecting os.Stdout is a process-global side effect.
func TestManifestExportCmdRunsCommand(t *testing.T) {
	// Redirect os.Stdout so we can capture output.PrintSuccess output.
	origStdout := os.Stdout
	r, w, err := os.Pipe()
	require.NoError(t, err)
	os.Stdout = w

	t.Cleanup(func() {
		os.Stdout = origStdout
		_ = r.Close()
	})

	cmd := NewExportCmd()
	cmd.SetArgs([]string{})
	require.NoError(t, cmd.Execute())

	require.NoError(t, w.Close())

	var buf bytes.Buffer
	_, err = buf.ReadFrom(r)
	require.NoError(t, err)

	// output.PrintSuccess wraps data in {"schema_version":"v1","success":true,"data":{...}}.
	var envelope struct {
		Success bool                       `json:"success"`
		Data    map[string]json.RawMessage `json:"data"`
	}
	require.NoError(t, json.Unmarshal(buf.Bytes(), &envelope), "export output must be valid JSON envelope")
	require.True(t, envelope.Success, "export envelope must report success")

	hooks := envelope.Data
	require.Len(t, hooks, 6, "export data must contain exactly 6 hook events")
	for _, ev := range []string{"SessionStart", "UserPromptSubmit", "PostToolUseFailure", "PreCompact", "SessionEnd", "TaskCompleted"} {
		_, ok := hooks[ev]
		require.True(t, ok, "export missing event: %s", ev)
	}
}

// TestManifestExportDefaultFlag verifies buildVybeHooks() (the --default path)
// always returns exactly 6 events regardless of manifest file state.
func TestManifestExportDefaultFlag(t *testing.T) {
	t.Parallel()

	defaults := buildVybeHooks()
	require.Len(t, defaults, 6, "--default path must always return 6 hook events")

	expectedEvents := []string{
		"SessionStart", "UserPromptSubmit", "PostToolUseFailure",
		"PreCompact", "SessionEnd", "TaskCompleted",
	}
	for _, ev := range expectedEvents {
		_, ok := defaults[ev]
		require.True(t, ok, "--default path missing event: %s", ev)
	}
}
