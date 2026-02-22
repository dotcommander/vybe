package commands

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
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
	home, _ := os.UserHomeDir()

	// Determine GOPATH (default: ~/go)
	gopath := os.Getenv("GOPATH")
	if gopath == "" {
		gopath = filepath.Join(home, "go")
	}

	candidates := []string{
		filepath.Join(gopath, "src", "vybe"),
		filepath.Join(gopath, "src", "github.com", "dotcommander", "vybe"),
		filepath.Join(home, "go", "src", "vybe"),
		filepath.Join(home, "src", "vybe"),
	}

	for _, dir := range candidates {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
	}

	// Check if the installed binary is a symlink pointing into a source tree.
	if binPath, err := exec.LookPath("vybe"); err == nil {
		if resolved, err := filepath.EvalSymlinks(binPath); err == nil {
			srcDir := filepath.Dir(resolved)
			if _, err := os.Stat(filepath.Join(srcDir, "go.mod")); err == nil {
				return srcDir
			}
			srcDir = filepath.Dir(srcDir)
			if _, err := os.Stat(filepath.Join(srcDir, "go.mod")); err == nil {
				return srcDir
			}
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
	hooksReinstalled, hooksErr := reinstallHooks()

	type result struct {
		Source           string `json:"source"`
		OldCommit        string `json:"old_commit"`
		NewCommit        string `json:"new_commit"`
		Updated          bool   `json:"updated"`
		Migrated         bool   `json:"migrated"`
		MigrateError     string `json:"migrate_error,omitempty"`
		HooksReinstalled bool   `json:"hooks_reinstalled"`
		HooksError       string `json:"hooks_error,omitempty"`
	}
	return output.PrintSuccess(result{
		Source:           srcDir,
		OldCommit:        oldVersion,
		NewCommit:        newVersion,
		Updated:          oldVersion != newVersion,
		Migrated:         migrated,
		MigrateError:     migrateErr,
		HooksReinstalled: hooksReinstalled,
		HooksError:       hooksErr,
	})
}

func upgradeViaGoInstall() error {
	oldVersion := getVybeVersion()

	installCtx, installCancel := context.WithTimeout(context.Background(), upgradeGoInstallTimeout)
	defer installCancel()

	install := exec.CommandContext(installCtx, "go", "install", "github.com/dotcommander/vybe/cmd/vybe@latest") //nolint:gosec // G204: go is a known system tool
	install.Stdout = os.Stderr
	install.Stderr = os.Stderr
	if err := install.Run(); err != nil {
		return cmdExecErr(installCtx, "go install", upgradeGoInstallTimeout, err)
	}

	newVersion := getVybeVersion()
	migrated, migrateErr := migrateAfterUpgrade()
	hooksReinstalled, hooksErr := reinstallHooks()

	type result struct {
		Source           string `json:"source"`
		Method           string `json:"method"`
		OldVersion       string `json:"old_version"`
		NewVersion       string `json:"new_version"`
		Updated          bool   `json:"updated"`
		Migrated         bool   `json:"migrated"`
		MigrateError     string `json:"migrate_error,omitempty"`
		HooksReinstalled bool   `json:"hooks_reinstalled"`
		HooksError       string `json:"hooks_error,omitempty"`
	}
	return output.PrintSuccess(result{
		Source:           "github.com/dotcommander/vybe@latest",
		Method:           "go install",
		OldVersion:       oldVersion,
		NewVersion:       newVersion,
		Updated:          oldVersion != newVersion,
		Migrated:         migrated,
		MigrateError:     migrateErr,
		HooksReinstalled: hooksReinstalled,
		HooksError:       hooksErr,
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

// reinstallHooks runs hook uninstall + install to pick up changes in the new binary.
// Best-effort: returns status and error string, never fails the upgrade.
func reinstallHooks() (bool, string) {
	uninstallCtx, uninstallCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer uninstallCancel()

	uninstall := exec.CommandContext(uninstallCtx, "vybe", "hook", "uninstall") //nolint:gosec // G204: vybe is the tool being upgraded
	uninstall.Stdout = os.Stderr
	uninstall.Stderr = os.Stderr
	if err := uninstall.Run(); err != nil {
		return false, fmt.Sprintf("hook uninstall failed: %v", err)
	}

	installCtx, installCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer installCancel()

	install := exec.CommandContext(installCtx, "vybe", "hook", "install") //nolint:gosec // G204: vybe is the tool being upgraded
	install.Stdout = os.Stderr
	install.Stderr = os.Stderr
	if err := install.Run(); err != nil {
		return false, fmt.Sprintf("hook install failed: %v", err)
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

func getVybeVersion() string {
	ctx, cancel := context.WithTimeout(context.Background(), upgradeVersionTimeout)
	defer cancel()

	out, err := exec.CommandContext(ctx, "vybe", "--version").Output() //nolint:gosec // G204: vybe is the tool being upgraded
	if err != nil {
		return "unknown"
	}

	var v struct {
		Version string `json:"version"`
	}
	if err := json.Unmarshal(out, &v); err != nil {
		return "unknown"
	}
	return v.Version
}
