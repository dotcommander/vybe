package commands

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/dotcommander/vybe/internal/app"
	"github.com/dotcommander/vybe/internal/output"
	"github.com/dotcommander/vybe/internal/store"
	"github.com/spf13/cobra"
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
	pull := exec.CommandContext(context.Background(), "git", "pull", "--ff-only") //nolint:gosec // G204: git is a known system tool
	pull.Dir = srcDir
	pull.Stdout = os.Stderr
	pull.Stderr = os.Stderr
	if err := pull.Run(); err != nil {
		return cmdErr(fmt.Errorf("git pull failed: %w", err))
	}

	// Build and install
	install := exec.CommandContext(context.Background(), "go", "install", "./cmd/vybe") //nolint:gosec // G204: go is a known system tool
	install.Dir = srcDir
	install.Stdout = os.Stderr
	install.Stderr = os.Stderr
	if err := install.Run(); err != nil {
		return cmdErr(fmt.Errorf("go install failed: %w", err))
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
	install := exec.CommandContext(context.Background(), "go", "install", "github.com/dotcommander/vybe/cmd/vybe@latest") //nolint:gosec // G204: go is a known system tool
	install.Stdout = os.Stderr
	install.Stderr = os.Stderr
	if err := install.Run(); err != nil {
		return cmdErr(fmt.Errorf("go install failed: %w", err))
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

func getGitVersion(dir string) string {
	out, err := exec.CommandContext(context.Background(), "git", "-C", dir, "rev-parse", "--short", "HEAD").Output() //nolint:gosec // G204: git is a known system tool
	if err != nil {
		return "unknown"
	}
	return strings.TrimSpace(string(out))
}
