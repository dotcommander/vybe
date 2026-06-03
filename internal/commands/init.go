package commands

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/dotcommander/vybe/internal/app"
	"github.com/dotcommander/vybe/internal/commands/hookcmd"
	"github.com/dotcommander/vybe/internal/output"
	"github.com/dotcommander/vybe/internal/store"
	"github.com/spf13/cobra"
)

type initStep struct {
	Name   string `json:"name"`
	Status string `json:"status"` // "ok", "skipped", "error"
	Detail string `json:"detail,omitempty"`
}

type initResult struct {
	Steps  []initStep `json:"steps"`
	Status string     `json:"status"` // "ok", "partial", or "failed"
}

// NewInitCmd returns the init command: idempotent first-run onboarding.
// Does NOT require --agent (it is the bootstrap that sets the default).
func NewInitCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "init",
		Short: "Initialize vybe: config dir, DB, hooks, default agent",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runInit()
		},
	}
}

func runInit() error {
	var steps []initStep
	overall := "ok"

	// Step 1: config dir
	steps = append(steps, runInitConfigDir())

	// Step 2: default_agent in config.yaml
	steps = append(steps, runInitDefaultAgent())

	// Step 3: db migrate
	dbStep, db := runInitDB()
	steps = append(steps, dbStep)

	// Step 4: write hooks.json if absent or stale (idempotent, best-effort).
	steps = append(steps, runInitHookManifest())

	// Step 4b: write agent_protocol.json if absent or stale (idempotent, best-effort).
	steps = append(steps, runInitAgentProtocol())

	// Step 4c: write loop_prompt.json if absent or stale (idempotent, best-effort).
	steps = append(steps, runInitLoopPrompt())

	// Step 5: install Claude hooks (best-effort; failure → partial, not fatal)
	steps = append(steps, runInitHooks())

	// Step 6: connectivity check (only if db opened successfully)
	steps = append(steps, runInitConnectivity(db))
	if db != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = store.CloseDB(ctx, db)
	}

	okCount := 0
	errCount := 0
	for _, s := range steps {
		switch s.Status {
		case "ok":
			okCount++
		case "error":
			errCount++
		}
	}
	switch {
	case errCount == 0:
		overall = "ok"
	case okCount == 0:
		overall = "failed"
	default:
		overall = "partial"
	}

	_ = output.PrintSuccess(initResult{Steps: steps, Status: overall})
	if overall != "ok" {
		// JSON already emitted above; return printedError so cobra exits non-zero
		// without re-printing the error.
		return printedError{err: fmt.Errorf("vybe init: %s", overall)}
	}
	return nil
}

func runInitConfigDir() initStep {
	if err := app.EnsureConfigDir(); err != nil {
		return initStep{Name: "config_dir", Status: "error", Detail: err.Error()}
	}
	return initStep{Name: "config_dir", Status: "ok"}
}

// runInitDefaultAgent writes `default_agent: claude` to config.yaml if no active
// (uncommented) default_agent line exists. Does not overwrite an existing value.
func runInitDefaultAgent() initStep {
	dir, err := app.ConfigDir()
	if err != nil {
		return initStep{Name: "default_agent", Status: "error", Detail: err.Error()}
	}
	configPath := filepath.Join(dir, "config.yaml")

	raw, err := os.ReadFile(configPath)
	if err != nil && !os.IsNotExist(err) {
		return initStep{Name: "default_agent", Status: "error", Detail: fmt.Sprintf("read config: %v", err)}
	}

	if hasActiveDefaultAgent(string(raw)) {
		return initStep{Name: "default_agent", Status: "skipped", Detail: "already set"}
	}

	// Append the active line to the existing file content.
	updated := strings.TrimRight(string(raw), "\n") + "\ndefault_agent: claude\n"
	if err := os.WriteFile(configPath, []byte(updated), 0o600); err != nil {
		return initStep{Name: "default_agent", Status: "error", Detail: err.Error()}
	}
	return initStep{Name: "default_agent", Status: "ok", Detail: "wrote claude"}
}

// hasActiveDefaultAgent returns true if content contains an uncommented default_agent line.
func hasActiveDefaultAgent(content string) bool {
	scanner := bufio.NewScanner(strings.NewReader(content))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, "default_agent:") {
			return true
		}
	}
	return false
}

// runInitDB opens and migrates the DB. Returns the step and the open *store.DB
// so step 5 can reuse it. Caller must close db when non-nil.
func runInitDB() (initStep, *DB) {
	dbPath, err := app.GetDBPath()
	if err != nil {
		return initStep{Name: "db_migrate", Status: "error", Detail: err.Error()}, nil
	}

	db, err := store.InitDBWithPath(dbPath)
	if err != nil {
		return initStep{Name: "db_migrate", Status: "error", Detail: err.Error()}, nil
	}
	return initStep{Name: "db_migrate", Status: "ok"}, db
}

// runInitHookManifest writes hooks.json if absent or stale. Best-effort:
// failure is reported as a step error but does not abort init.
func runInitHookManifest() initStep {
	dir, err := app.ConfigDir()
	if err != nil {
		return initStep{Name: "hook_manifest", Status: "error", Detail: err.Error()}
	}
	if _, err := hookcmd.LoadOrWriteHookManifest(dir); err != nil {
		return initStep{Name: "hook_manifest", Status: "error", Detail: err.Error()}
	}
	return initStep{Name: "hook_manifest", Status: "ok"}
}

// runInitAgentProtocol writes agent_protocol.json if absent or stale. Best-effort:
// failure is reported as a step error but does not abort init.
func runInitAgentProtocol() initStep {
	dir, err := app.ConfigDir()
	if err != nil {
		return initStep{Name: "agent_protocol", Status: "error", Detail: err.Error()}
	}
	if _, err := LoadOrWriteAgentProtocol(dir); err != nil {
		return initStep{Name: "agent_protocol", Status: "error", Detail: err.Error()}
	}
	return initStep{Name: "agent_protocol", Status: "ok"}
}

// runInitLoopPrompt writes loop_prompt.json if absent or stale. Best-effort:
// failure is reported as a step error but does not abort init.
func runInitLoopPrompt() initStep {
	dir, err := app.ConfigDir()
	if err != nil {
		return initStep{Name: "loop_prompt", Status: "error", Detail: err.Error()}
	}
	if _, err := LoadOrWriteLoopPrompt(dir); err != nil {
		return initStep{Name: "loop_prompt", Status: "error", Detail: err.Error()}
	}
	return initStep{Name: "loop_prompt", Status: "ok"}
}

func runInitHooks() initStep {
	res, err := hookcmd.InstallClaudeHooks(false)
	if err != nil {
		return initStep{Name: "hooks_install", Status: "error", Detail: err.Error()}
	}
	if len(res.Installed) == 0 && len(res.Updated) == 0 {
		return initStep{Name: "hooks_install", Status: "skipped", Detail: "already installed"}
	}
	return initStep{Name: "hooks_install", Status: "ok"}
}

func runInitConnectivity(db *DB) initStep {
	if db == nil {
		// DB step already failed; skip rather than double-count the root cause.
		return initStep{Name: "connectivity", Status: "skipped", Detail: "db unavailable"}
	}
	var one int
	if err := db.QueryRowContext(context.Background(), "SELECT 1").Scan(&one); err != nil {
		return initStep{Name: "connectivity", Status: "error", Detail: err.Error()}
	}
	return initStep{Name: "connectivity", Status: "ok"}
}
