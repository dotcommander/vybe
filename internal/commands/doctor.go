package commands

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"time"

	"github.com/spf13/cobra"

	"github.com/dotcommander/vybe/internal/app"
	"github.com/dotcommander/vybe/internal/commands/hookcmd"
	"github.com/dotcommander/vybe/internal/output"
	"github.com/dotcommander/vybe/internal/store"
)

type doctorResult struct {
	Healthy bool          `json:"healthy"`
	Checks  []checkResult `json:"checks"`
}

type checkResult struct {
	Name   string `json:"name"`
	OK     bool   `json:"ok"`
	Detail string `json:"detail,omitempty"`
	Repair string `json:"repair,omitempty"` // per-check fix hint (only on failure)
}

// NewDoctorCmd returns the doctor command: read-only health checks, no side effects.
func NewDoctorCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "doctor",
		Short: "Read-only health check: binary on PATH, hooks installed, DB reachable, config dir exists",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runDoctor()
		},
	}
}

func runDoctor() error {
	var checks []checkResult

	// 1. Binary on PATH.
	checks = append(checks, checkBinaryOnPath())

	// 2. Claude settings.json present and contains vybe hooks for each expected event.
	checks = append(checks, checkHooksInstalled())

	// Report any additional detected non-Claude hosts (presence of bridge install).
	checks = append(checks, checkDetectedHostHooks()...)

	// 3. DB reachable: open + SELECT 1.
	checks = append(checks, checkDBReachable())

	// 4. Config dir exists.
	checks = append(checks, checkConfigDir())

	healthy := true
	for _, c := range checks {
		if !c.OK {
			healthy = false
			break
		}
	}

	result := doctorResult{Healthy: healthy, Checks: checks}
	_ = output.PrintSuccess(result)
	if !healthy {
		// JSON already emitted above; return printedError so cobra exits non-zero
		// without re-printing the error.
		return printedError{err: fmt.Errorf("vybe doctor: unhealthy")}
	}
	return nil
}

// checkBinaryOnPath verifies the vybe binary is findable on PATH.
func checkBinaryOnPath() checkResult {
	path, err := exec.LookPath("vybe")
	if err != nil {
		return checkResult{Name: "binary_on_path", OK: false, Detail: err.Error(),
			Repair: "add vybe binary location to PATH (e.g. export PATH=$PATH:~/go/bin)"}
	}
	return checkResult{Name: "binary_on_path", OK: true, Detail: path}
}

// checkHooksInstalled reads the Claude settings file (respecting CLAUDE_SETTINGS_PATH
// env override) and verifies each expected hook event has a vybe hook entry registered.
// Uses HasVybeHook which matches on the command string's basename ("vybe"), not the
// running binary name — so test-written entries with literal "vybe hook <sub>" commands pass.
func checkHooksInstalled() checkResult {
	settingsPath := hookcmd.ClaudeSettingsPath()

	data, err := os.ReadFile(settingsPath)
	if err != nil {
		if os.IsNotExist(err) {
			return checkResult{Name: "hooks_installed", OK: false, Detail: "settings.json not found",
				Repair: "run 'vybe init' to install hooks"}
		}
		return checkResult{Name: "hooks_installed", OK: false, Detail: err.Error(),
			Repair: "run 'vybe init' to install hooks"}
	}

	var settings map[string]any
	if err := json.Unmarshal(data, &settings); err != nil {
		return checkResult{Name: "hooks_installed", OK: false, Detail: "settings.json is not valid JSON",
			Repair: "fix or remove ~/.claude/settings.json, then run 'vybe init'"}
	}

	hooksObj, _ := settings["hooks"].(map[string]any)

	for _, eventName := range hookcmd.VybeHookEventNames() {
		entries, _ := hooksObj[eventName].([]any)
		if !hookcmd.HasVybeHook(entries) {
			return checkResult{
				Name:   "hooks_installed",
				OK:     false,
				Detail: "missing vybe hook for event: " + eventName,
				Repair: "run 'vybe init' to install hooks",
			}
		}
	}

	return checkResult{Name: "hooks_installed", OK: true}
}

// checkDetectedHostHooks emits one check per detected non-Claude host, reporting
// that the host is present. Claude is covered by checkHooksInstalled (deep per-event
// audit) and is skipped here to avoid double-reporting.
func checkDetectedHostHooks() []checkResult {
	var out []checkResult
	for _, h := range hookcmd.DetectedHostInstallers() {
		if h.Name() == "claude" {
			continue
		}
		out = append(out, checkResult{
			Name:   "host_detected_" + h.Name(),
			OK:     true,
			Detail: h.Name() + " host detected",
		})
	}
	return out
}

// checkDBReachable opens the DB and runs SELECT 1.
func checkDBReachable() checkResult {
	dbPath, err := app.GetDBPath()
	if err != nil {
		return checkResult{Name: "db_reachable", OK: false, Detail: err.Error(),
			Repair: "run 'vybe init' to create the database"}
	}

	db, err := store.OpenDB(dbPath)
	if err != nil {
		return checkResult{Name: "db_reachable", OK: false, Detail: err.Error(),
			Repair: "run 'vybe init' to create the database"}
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = store.CloseDB(ctx, db)
	}()

	var one int
	if err := db.QueryRowContext(context.Background(), "SELECT 1").Scan(&one); err != nil {
		return checkResult{Name: "db_reachable", OK: false, Detail: err.Error(),
			Repair: "run 'vybe init' to create the database"}
	}

	return checkResult{Name: "db_reachable", OK: true}
}

// checkConfigDir verifies the vybe config directory exists.
func checkConfigDir() checkResult {
	dir, err := app.ConfigDir()
	if err != nil {
		return checkResult{Name: "config_dir", OK: false, Detail: err.Error(),
			Repair: "run 'vybe init' to create the config directory"}
	}

	if _, err := os.Stat(dir); err != nil {
		if os.IsNotExist(err) {
			return checkResult{Name: "config_dir", OK: false, Detail: "config dir not found: " + dir,
				Repair: "run 'vybe init' to create the config directory"}
		}
		return checkResult{Name: "config_dir", OK: false, Detail: err.Error(),
			Repair: "run 'vybe init' to create the config directory"}
	}

	return checkResult{Name: "config_dir", OK: true, Detail: dir}
}
