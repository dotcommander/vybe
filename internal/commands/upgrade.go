package commands

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/dotcommander/vybe/internal/app"
	"github.com/dotcommander/vybe/internal/output"
	"github.com/dotcommander/vybe/internal/store"
	"github.com/spf13/cobra"
)

const (
	upgradeGitPullTimeout   = 2 * time.Minute
	upgradeGoInstallTimeout = 10 * time.Minute
	upgradeVersionTimeout   = 5 * time.Second
)

// NewUpgradeCmd creates the upgrade command.
func NewUpgradeCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "upgrade",
		Short: "Upgrade vybe to the latest version",
		Long:  `Pulls latest from git and runs go install. Requires git and go on PATH.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			// Validate required external tools before attempting upgrade.
			for _, tool := range []string{"git", "go"} {
				if _, err := exec.LookPath(tool); err != nil {
					return cmdErr(fmt.Errorf("%s not found in PATH: install %s and retry", tool, tool))
				}
			}

			// Find the source directory by looking for go.mod
			srcDir := findSourceDir()
			if srcDir == "" {
				// Fallback: go install from module path
				return upgradeViaGoInstall()
			}
			return upgradeViaGitPull(srcDir)
		},
	}
	cmd.Annotations = map[string]string{"mutates": "true"}
	return cmd
}

func findSourceDir() string {
	// Check common locations
	home, _ := os.UserHomeDir()
	candidates := []string{
		home + "/go/src/vybe",
		home + "/src/vybe",
	}

	for _, dir := range candidates {
		if _, err := os.Stat(dir + "/go.mod"); err == nil {
			return dir
		}
	}
	return ""
}

func upgradeViaGitPull(srcDir string) error {
	// Get current version before pull
	oldVersion := getGitVersion(srcDir)

	// Pull latest
	pullCtx, pullCancel := context.WithTimeout(context.Background(), upgradeGitPullTimeout)
	defer pullCancel()

	pull := exec.CommandContext(pullCtx, "git", "pull", "--ff-only") //nolint:gosec // G204: git is a known system tool
	pull.Dir = srcDir
	pull.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	pull.Stdout = os.Stderr
	pull.Stderr = os.Stderr
	if err := pull.Run(); err != nil {
		return cmdExecErr(pullCtx, "git pull", upgradeGitPullTimeout, err)
	}

	// Build and install
	installCtx, installCancel := context.WithTimeout(context.Background(), upgradeGoInstallTimeout)
	defer installCancel()

	install := exec.CommandContext(installCtx, "go", "install", "./cmd/vybe") //nolint:gosec // G204: go is a known system tool
	install.Dir = srcDir
	install.Stdout = os.Stderr
	install.Stderr = os.Stderr
	if err := install.Run(); err != nil {
		return cmdExecErr(installCtx, "go install", upgradeGoInstallTimeout, err)
	}

	newVersion := getGitVersion(srcDir)

	migrated, migrateErr := migrateAfterUpgrade()

	type result struct {
		Source       string `json:"source"`
		OldCommit    string `json:"old_commit"`
		NewCommit    string `json:"new_commit"`
		Updated      bool   `json:"updated"`
		Migrated     bool   `json:"migrated"`
		MigrateError string `json:"migrate_error,omitempty"`
	}
	return output.PrintSuccess(result{
		Source:       srcDir,
		OldCommit:    oldVersion,
		NewCommit:    newVersion,
		Updated:      oldVersion != newVersion,
		Migrated:     migrated,
		MigrateError: migrateErr,
	})
}

func upgradeViaGoInstall() error {
	installCtx, installCancel := context.WithTimeout(context.Background(), upgradeGoInstallTimeout)
	defer installCancel()

	install := exec.CommandContext(installCtx, "go", "install", "github.com/dotcommander/vybe/cmd/vybe@latest") //nolint:gosec // G204: go is a known system tool
	install.Stdout = os.Stderr
	install.Stderr = os.Stderr
	if err := install.Run(); err != nil {
		return cmdExecErr(installCtx, "go install", upgradeGoInstallTimeout, err)
	}

	migrated, migrateErr := migrateAfterUpgrade()

	type result struct {
		Source       string `json:"source"`
		Method       string `json:"method"`
		Migrated     bool   `json:"migrated"`
		MigrateError string `json:"migrate_error,omitempty"`
	}
	return output.PrintSuccess(result{
		Source:       "github.com/dotcommander/vybe@latest",
		Method:       "go install",
		Migrated:     migrated,
		MigrateError: migrateErr,
	})
}

// migrateAfterUpgrade opens the database and runs pending migrations.
func migrateAfterUpgrade() (bool, string) {
	dbPath, err := app.GetDBPath()
	if err != nil {
		return false, err.Error()
	}
	db, err := store.OpenDB(dbPath)
	if err != nil {
		return false, err.Error()
	}
	defer func() { _ = store.CloseDB(db) }()
	if err := store.MigrateDB(db, dbPath); err != nil {
		return false, err.Error()
	}
	return true, ""
}

// cmdExecErr returns a timeout error when ctx deadline was exceeded, otherwise
// wraps err with the operation name. Centralises the repeated deadline-check
// pattern used after exec.Cmd.Run() calls.
func cmdExecErr(ctx context.Context, operation string, timeout time.Duration, err error) error {
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return cmdErr(fmt.Errorf("%s timed out after %s", operation, timeout))
	}
	return cmdErr(fmt.Errorf("%s failed: %w", operation, err))
}

func getGitVersion(dir string) string {
	ctx, cancel := context.WithTimeout(context.Background(), upgradeVersionTimeout)
	defer cancel()

	out, err := exec.CommandContext(ctx, "git", "-C", dir, "rev-parse", "--short", "HEAD").Output() //nolint:gosec // G204: git is a known system tool
	if err != nil {
		return "unknown"
	}
	return strings.TrimSpace(string(out))
}
