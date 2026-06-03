package commands

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dotcommander/vybe/internal/app"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"
)

// buildTestRoot constructs a cobra command tree mirroring Execute() for in-process testing.
// When a command is added to root.go it must be added here too, so tests exercise the full
// registered surface.
func buildTestRoot(t *testing.T) *cobra.Command {
	t.Helper()
	root := &cobra.Command{
		Use:           "vybe",
		SilenceErrors: true,
		SilenceUsage:  true,
		RunE:          func(cmd *cobra.Command, args []string) error { return nil },
	}
	root.PersistentFlags().String("db-path", "", "Override database path")
	root.PersistentFlags().StringP("agent", "a", "", "Agent name")
	root.PersistentFlags().String("request-id", "", "Idempotency key")

	// Mirror PersistentPreRunE from root.go so --db-path is wired into app.SetDBPathOverride.
	root.PersistentPreRunE = func(cmd *cobra.Command, args []string) error {
		if dbp, err := cmd.Flags().GetString("db-path"); err == nil && dbp != "" {
			SetDBPathOverrideForTest(t, dbp)
		}
		return nil
	}

	root.AddCommand(NewInitCmd())
	root.AddCommand(NewDoctorCmd())
	root.AddCommand(NewTaskCmd())
	root.AddCommand(NewMemoryCmd())
	root.AddCommand(NewResumeCmd())
	root.AddCommand(NewHookCmd())
	root.AddCommand(NewStatusCmd(root))
	root.AddCommand(NewUpgradeCmd())
	root.AddCommand(NewPushCmd())
	root.AddCommand(NewEventsCmd())
	root.AddCommand(NewArtifactsCmd())
	root.AddCommand(NewSchemaCmd(root))
	root.AddCommand(newDoneCmd())
	root.AddCommand(newBlockCmd())
	root.AddCommand(newNoteCmd())
	root.AddCommand(newRememberCmd())
	root.AddCommand(newFocusCmd())

	return root
}

// SetDBPathOverrideForTest wires the --db-path override into the app layer for tests
// that build their own root (bypassing root.go's PersistentPreRunE). Mirrors the real
// root.go path: calls app.SetDBPathOverride(path) and registers a t.Cleanup to clear
// the process-global override after the test, preventing cross-test bleed.
func SetDBPathOverrideForTest(t *testing.T, path string) {
	t.Helper()
	app.SetDBPathOverride(path)
	t.Cleanup(func() { app.SetDBPathOverride("") })
}

// runInitViaRoot exercises the init subcommand in-process using a fresh root.
// Returns the captured stdout output and the execution error.
func runInitViaRoot(t *testing.T, dbPath string) (string, error) {
	t.Helper()
	var execErr error
	out := captureStdout(t, func() {
		r := buildTestRoot(t)
		r.SetArgs([]string{"--db-path", dbPath, "init"})
		execErr = r.Execute()
	})
	return strings.TrimSpace(out), execErr
}

// assertInitRegistered fails the test immediately if "init" is not registered on root.
// This is the Phase A gate: the test body below it is only reached in Phase B+.
func assertInitRegistered(t *testing.T, root *cobra.Command) {
	t.Helper()
	sub, _, _ := root.Find([]string{"init"})
	require.Equal(t, "init", sub.Name(),
		"vybe init command is not registered — add NewInitCmd() and wire it in root.go (Phase B)")
}

func TestInitFirstRun(t *testing.T) {
	app.ResetSettingsForTest()
	t.Cleanup(app.ResetSettingsForTest)

	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)
	dbPath := filepath.Join(tmpDir, "vybe.db")
	t.Setenv("VYBE_DB_PATH", dbPath)

	// Phase A gate — fails until Phase B lands.
	assertInitRegistered(t, buildTestRoot(t))

	// Phase B+: first run must exit 0 with JSON status "ok", no error steps.
	out, err := runInitViaRoot(t, dbPath)
	require.NoError(t, err, "vybe init must exit 0")

	var envelope map[string]any
	require.NoError(t, json.Unmarshal([]byte(out), &envelope), "output must be valid JSON: %s", out)
	require.Equal(t, true, envelope["success"], "envelope.success must be true")

	data, ok := envelope["data"].(map[string]any)
	require.True(t, ok, "envelope.data must be an object")
	require.Equal(t, "ok", data["status"], "init status must be 'ok'")

	steps, ok := data["steps"].([]any)
	require.True(t, ok, "init result must contain a steps array")
	require.NotEmpty(t, steps, "steps must not be empty")
	for _, s := range steps {
		step := s.(map[string]any)
		require.NotEqual(t, "error", step["status"], "step %q must not have status 'error'", step["name"])
	}
}

func TestInitIdempotent(t *testing.T) {
	app.ResetSettingsForTest()
	t.Cleanup(app.ResetSettingsForTest)

	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)
	dbPath := filepath.Join(tmpDir, "vybe.db")
	t.Setenv("VYBE_DB_PATH", dbPath)

	// Phase A gate.
	assertInitRegistered(t, buildTestRoot(t))

	// Phase B+: run init twice; both runs must succeed with no error steps.
	for run := 1; run <= 2; run++ {
		out, err := runInitViaRoot(t, dbPath)
		require.NoError(t, err, "run %d: vybe init must exit 0", run)

		var envelope map[string]any
		require.NoError(t, json.Unmarshal([]byte(out), &envelope),
			"run %d: output must be valid JSON: %s", run, out)
		require.Equal(t, true, envelope["success"], "run %d: success must be true", run)

		data, _ := envelope["data"].(map[string]any)
		steps, ok := data["steps"].([]any)
		require.True(t, ok, "run %d: steps must be an array", run)
		for _, s := range steps {
			step := s.(map[string]any)
			require.NotEqual(t, "error", step["status"],
				"run %d: step %q must not be 'error'", run, step["name"])
		}
	}
}

func TestInitDefaultAgent(t *testing.T) {
	app.ResetSettingsForTest()
	t.Cleanup(app.ResetSettingsForTest)

	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)
	dbPath := filepath.Join(tmpDir, "vybe.db")
	t.Setenv("VYBE_DB_PATH", dbPath)
	t.Setenv("VYBE_AGENT", "")

	// Phase A gate.
	assertInitRegistered(t, buildTestRoot(t))

	// Phase B+: after init, config.yaml must contain active default_agent: claude.
	_, err := runInitViaRoot(t, dbPath)
	require.NoError(t, err, "vybe init must exit 0")

	configPath := filepath.Join(tmpDir, ".config", "vybe", "config.yaml")
	raw, readErr := os.ReadFile(configPath)
	require.NoError(t, readErr, "config.yaml must exist after vybe init")
	require.Contains(t, string(raw), "default_agent: claude",
		"config.yaml must contain active (uncommented) default_agent: claude")
}

func TestInitNoAgent(t *testing.T) {
	app.ResetSettingsForTest()
	t.Cleanup(app.ResetSettingsForTest)

	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)
	dbPath := filepath.Join(tmpDir, "vybe.db")
	t.Setenv("VYBE_DB_PATH", dbPath)
	t.Setenv("VYBE_AGENT", "")

	// Phase A gate.
	assertInitRegistered(t, buildTestRoot(t))

	// Phase B+: after init, task create must succeed without --agent.
	_, err := runInitViaRoot(t, dbPath)
	require.NoError(t, err, "vybe init must exit 0")

	var taskOut string
	taskErr := (error)(nil)
	taskOut = captureStdout(t, func() {
		r := buildTestRoot(t)
		r.SetArgs([]string{"--db-path", dbPath, "task", "create",
			"--title", "no-agent-task",
			"--request-id", "req-no-agent-1",
		})
		taskErr = r.Execute()
	})

	require.NoError(t, taskErr, "task create must exit 0 after vybe init sets default_agent")

	var envelope map[string]any
	require.NoError(t, json.Unmarshal([]byte(strings.TrimSpace(taskOut)), &envelope),
		"task create output must be valid JSON")
	require.Equal(t, true, envelope["success"],
		"task create must succeed without --agent when default_agent is set")
}

// TestInitDefaultAgentAppend verifies that init appends default_agent to a pre-existing
// config.yaml (one that has other content but no default_agent line), and that running
// init again does NOT produce a second default_agent line (idempotent append).
func TestInitDefaultAgentAppend(t *testing.T) {
	app.ResetSettingsForTest()
	t.Cleanup(app.ResetSettingsForTest)

	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)
	dbPath := filepath.Join(tmpDir, "vybe.db")
	t.Setenv("VYBE_DB_PATH", dbPath)

	// Phase A gate.
	assertInitRegistered(t, buildTestRoot(t))

	// Pre-create the config dir and write a config.yaml WITHOUT default_agent.
	configDir := filepath.Join(tmpDir, ".config", "vybe")
	require.NoError(t, os.MkdirAll(configDir, 0o700))
	configPath := filepath.Join(configDir, "config.yaml")
	require.NoError(t, os.WriteFile(configPath, []byte("db_path: "+dbPath+"\n"), 0o600))

	// First run: default_agent step must report "ok" (it appended).
	out, err := runInitViaRoot(t, dbPath)
	require.NoError(t, err, "vybe init must exit 0")

	var envelope map[string]any
	require.NoError(t, json.Unmarshal([]byte(out), &envelope), "output must be valid JSON: %s", out)
	data, _ := envelope["data"].(map[string]any)
	steps, ok := data["steps"].([]any)
	require.True(t, ok, "steps must be an array")

	foundDefaultAgentOK := false
	for _, s := range steps {
		step := s.(map[string]any)
		if step["name"] == "default_agent" {
			require.Equal(t, "ok", step["status"], "default_agent step must be 'ok' on append")
			foundDefaultAgentOK = true
		}
	}
	require.True(t, foundDefaultAgentOK, "default_agent step must appear in results")

	// Verify exactly one default_agent line in config.yaml.
	raw, readErr := os.ReadFile(configPath)
	require.NoError(t, readErr)
	count := strings.Count(string(raw), "default_agent:")
	require.Equal(t, 1, count, "config.yaml must contain exactly one default_agent: line after first init")

	// Second run: default_agent step must be "skipped" (already set), not a second append.
	out2, err2 := runInitViaRoot(t, dbPath)
	require.NoError(t, err2, "second vybe init must exit 0")

	var envelope2 map[string]any
	require.NoError(t, json.Unmarshal([]byte(out2), &envelope2), "output must be valid JSON: %s", out2)
	data2, _ := envelope2["data"].(map[string]any)
	steps2, ok2 := data2["steps"].([]any)
	require.True(t, ok2, "steps must be an array on second run")

	for _, s := range steps2 {
		step := s.(map[string]any)
		if step["name"] == "default_agent" {
			require.Equal(t, "skipped", step["status"], "default_agent step must be 'skipped' on second init (idempotent)")
		}
	}

	// Still exactly one line after second run.
	raw2, _ := os.ReadFile(configPath)
	count2 := strings.Count(string(raw2), "default_agent:")
	require.Equal(t, 1, count2, "config.yaml must still contain exactly one default_agent: line after second init")
}
