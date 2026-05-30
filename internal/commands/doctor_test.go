package commands

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"

	"github.com/dotcommander/vybe/internal/commands/hookcmd"
)

// assertDoctorRegistered fails the test immediately if "doctor" is not registered on root.
// This is the Phase A gate: the test body below it is only reached in Phase C+.
func assertDoctorRegistered(t *testing.T, root *cobra.Command) {
	t.Helper()
	sub, _, _ := root.Find([]string{"doctor"})
	require.Equal(t, "doctor", sub.Name(),
		"vybe doctor command is not registered — add NewDoctorCmd() and wire it in root.go (Phase C)")
}

// runDoctorViaRoot exercises the doctor subcommand in-process using a fresh root.
// Returns the captured stdout output and the execution error.
// Stdout is captured before the error is inspected so JSON is always available.
func runDoctorViaRoot(t *testing.T, dbPath string) (string, error) {
	t.Helper()
	var execErr error
	out := captureStdout(t, func() {
		r := buildTestRoot(t)
		r.SetArgs([]string{"--db-path", dbPath, "doctor"})
		execErr = r.Execute()
	})
	return strings.TrimSpace(out), execErr
}

func TestDoctorAllHealthy(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)
	dbPath := filepath.Join(tmpDir, "vybe.db")
	t.Setenv("VYBE_DB_PATH", dbPath)

	// Point CLAUDE_SETTINGS_PATH at a temp file we control so we can write literal
	// "vybe hook <subcommand>" command strings. IsVybeHookCommand matches on the
	// command string's basename, not the running binary, so this passes strict checking.
	settingsPath := filepath.Join(tmpDir, "settings.json")
	t.Setenv("CLAUDE_SETTINGS_PATH", settingsPath)

	// Phase A gate — fails until Phase C lands.
	assertDoctorRegistered(t, buildTestRoot(t))

	// Use init to set up the DB and config dir (hooks check is handled separately below).
	captureStdout(t, func() {
		r := buildTestRoot(t)
		r.SetArgs([]string{"--db-path", dbPath, "init"})
		_ = r.Execute()
	})

	// Overwrite the settings.json init wrote with proper vybe command strings so
	// HasVybeHook returns true under the strict check.
	writeFullVybeSettings(t, settingsPath)

	out, err := runDoctorViaRoot(t, dbPath)
	require.NoError(t, err, "doctor must exit 0 when healthy")

	var envelope map[string]any
	require.NoError(t, json.Unmarshal([]byte(out), &envelope), "doctor output must be valid JSON: %s", out)
	require.Equal(t, true, envelope["success"], "envelope.success must be true")

	data, ok := envelope["data"].(map[string]any)
	require.True(t, ok, "envelope.data must be an object")
	require.Equal(t, true, data["healthy"], "doctor must report healthy:true after full init")

	checks, ok := data["checks"].([]any)
	require.True(t, ok, "doctor result must contain a checks array")
	require.NotEmpty(t, checks, "checks must not be empty")
	for _, c := range checks {
		check := c.(map[string]any)
		require.Equal(t, true, check["ok"],
			"check %q must be ok in a healthy environment", check["name"])
	}
}

func TestDoctorMissingHooks(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)
	dbPath := filepath.Join(tmpDir, "vybe.db")
	t.Setenv("VYBE_DB_PATH", dbPath)
	// CLAUDE_SETTINGS_PATH points to a file with no vybe hooks.
	settingsPath := filepath.Join(tmpDir, "settings.json")
	t.Setenv("CLAUDE_SETTINGS_PATH", settingsPath)

	// Phase A gate — fails until Phase C lands.
	assertDoctorRegistered(t, buildTestRoot(t))

	// Phase C+: write settings.json with no vybe hooks so the hooks check fails.
	require.NoError(t, os.WriteFile(settingsPath, []byte(`{"hooks":{}}`), 0600))

	out, err := runDoctorViaRoot(t, dbPath)
	require.Error(t, err, "doctor must exit non-zero when unhealthy")

	var envelope map[string]any
	require.NoError(t, json.Unmarshal([]byte(out), &envelope), "doctor output must be valid JSON: %s", out)
	require.Equal(t, true, envelope["success"],
		"envelope.success must be true (JSON result always emitted)")

	data, ok := envelope["data"].(map[string]any)
	require.True(t, ok, "envelope.data must be an object")
	require.Equal(t, false, data["healthy"],
		"doctor must report healthy:false when hooks are missing")

	checks, ok := data["checks"].([]any)
	require.True(t, ok, "checks must be an array")

	foundFailedHooksCheck := false
	for _, c := range checks {
		check := c.(map[string]any)
		name, _ := check["name"].(string)
		okVal, _ := check["ok"].(bool)
		if strings.Contains(name, "hook") && !okVal {
			foundFailedHooksCheck = true
		}
	}
	require.True(t, foundFailedHooksCheck,
		"a hooks-related check must fail when settings.json has no vybe hooks")
}

// TestDoctorForeignHookFalseNegative is the regression test for the false-negative bug
// where doctor reported healthy even when only a foreign (non-vybe) hook was present.
//
// Old logic: !HasVybeHook(entries) && len(entries) == 0
//
//	→ foreign entry: HasVybeHook=false, len=1 → condition false → wrongly healthy
//
// New logic: !HasVybeHook(entries)
//
//	→ foreign entry: HasVybeHook=false → condition true → correctly unhealthy
func TestDoctorForeignHookFalseNegative(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)
	dbPath := filepath.Join(tmpDir, "vybe.db")
	t.Setenv("VYBE_DB_PATH", dbPath)

	settingsPath := filepath.Join(tmpDir, "settings.json")
	t.Setenv("CLAUDE_SETTINGS_PATH", settingsPath)

	// Phase A gate — fails until Phase C lands.
	assertDoctorRegistered(t, buildTestRoot(t))

	// Build a settings.json where SessionStart has a foreign (non-vybe) hook entry
	// and no vybe hook — this is the exact scenario the bug masked.
	firstEvent := hookcmd.VybeHookEventNames()[0] // deterministic: sorted, first event

	foreignEntry := map[string]any{
		"matcher": "",
		"hooks": []any{
			map[string]any{
				"type":    "command",
				"command": "some-other-tool --flag",
				"timeout": float64(1000),
			},
		},
	}

	hooksObj := map[string]any{
		firstEvent: []any{foreignEntry},
	}
	settings := map[string]any{"hooks": hooksObj}
	data, err := json.MarshalIndent(settings, "", "  ")
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(settingsPath, data, 0o600))

	out, err := runDoctorViaRoot(t, dbPath)
	require.Error(t, err, "doctor must exit non-zero when unhealthy")

	var envelope map[string]any
	require.NoError(t, json.Unmarshal([]byte(out), &envelope), "doctor output must be valid JSON: %s", out)
	require.Equal(t, true, envelope["success"],
		"envelope.success must be true (JSON result always emitted)")

	result, ok := envelope["data"].(map[string]any)
	require.True(t, ok, "envelope.data must be an object")
	require.Equal(t, false, result["healthy"],
		"doctor must report healthy:false when only a foreign hook is present for event %q (no vybe hook)", firstEvent)

	checks, ok := result["checks"].([]any)
	require.True(t, ok, "checks must be an array")

	foundFailedHooksCheck := false
	for _, c := range checks {
		check := c.(map[string]any)
		name, _ := check["name"].(string)
		okVal, _ := check["ok"].(bool)
		if strings.Contains(name, "hook") && !okVal {
			foundFailedHooksCheck = true
		}
	}
	require.True(t, foundFailedHooksCheck,
		"hooks check must fail when only a foreign hook is present (not a vybe hook)")
}

// TestDoctorInvalidSettingsJSON verifies that a malformed settings.json causes
// doctor to report healthy:false with a clear detail.
func TestDoctorInvalidSettingsJSON(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)
	dbPath := filepath.Join(tmpDir, "vybe.db")
	t.Setenv("VYBE_DB_PATH", dbPath)

	settingsPath := filepath.Join(tmpDir, "settings.json")
	t.Setenv("CLAUDE_SETTINGS_PATH", settingsPath)

	// Phase A gate — fails until Phase C lands.
	assertDoctorRegistered(t, buildTestRoot(t))

	// Write invalid JSON.
	require.NoError(t, os.WriteFile(settingsPath, []byte(`{not valid json`), 0o600))

	out, err := runDoctorViaRoot(t, dbPath)
	require.Error(t, err, "doctor must exit non-zero when unhealthy")

	var envelope map[string]any
	require.NoError(t, json.Unmarshal([]byte(out), &envelope), "doctor output must be valid JSON: %s", out)
	require.Equal(t, true, envelope["success"],
		"envelope.success must be true (JSON result always emitted)")

	result, ok := envelope["data"].(map[string]any)
	require.True(t, ok, "envelope.data must be an object")
	require.Equal(t, false, result["healthy"],
		"doctor must report healthy:false when settings.json is not valid JSON")

	checks, ok := result["checks"].([]any)
	require.True(t, ok, "checks must be an array")

	foundFailedHooksCheck := false
	for _, c := range checks {
		check := c.(map[string]any)
		name, _ := check["name"].(string)
		okVal, _ := check["ok"].(bool)
		detail, _ := check["detail"].(string)
		if strings.Contains(name, "hook") && !okVal {
			require.NotEmpty(t, detail, "failed hooks check must include a detail message")
			foundFailedHooksCheck = true
		}
	}
	require.True(t, foundFailedHooksCheck,
		"a hooks-related check must fail when settings.json is invalid JSON")
}

func TestDoctorDBUnreachable(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)
	// Point VYBE_DB_PATH at a directory — SQLite cannot open a directory as a database.
	badDBPath := filepath.Join(tmpDir, "vybe-dir-not-file")
	require.NoError(t, os.MkdirAll(badDBPath, 0755))
	t.Setenv("VYBE_DB_PATH", badDBPath)

	// Phase A gate — fails until Phase C lands.
	assertDoctorRegistered(t, buildTestRoot(t))

	// Phase C+: doctor must detect db unreachable and report healthy:false with non-zero exit.
	out, err := runDoctorViaRoot(t, badDBPath)
	require.Error(t, err, "doctor must exit non-zero when unhealthy")

	var envelope map[string]any
	require.NoError(t, json.Unmarshal([]byte(out), &envelope), "doctor output must be valid JSON: %s", out)
	require.Equal(t, true, envelope["success"],
		"envelope.success must be true (JSON result always emitted)")

	data, ok := envelope["data"].(map[string]any)
	require.True(t, ok, "envelope.data must be an object")
	require.Equal(t, false, data["healthy"],
		"doctor must report healthy:false when DB is unreachable")

	checks, ok := data["checks"].([]any)
	require.True(t, ok, "checks must be an array")

	foundFailedDBCheck := false
	for _, c := range checks {
		check := c.(map[string]any)
		name, _ := check["name"].(string)
		okVal, _ := check["ok"].(bool)
		if strings.Contains(name, "db") && !okVal {
			foundFailedDBCheck = true
		}
	}
	require.True(t, foundFailedDBCheck,
		"a db-related check must fail when the database path is unreachable")
}
