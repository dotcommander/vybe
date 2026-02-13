package commands

import (
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/dotcommander/vybe/internal/output"
	"github.com/spf13/cobra"
)

// NewUpgradeCmd creates the upgrade command.
func NewUpgradeCmd() *cobra.Command {
	return &cobra.Command{
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
	pull := exec.Command("git", "pull", "--ff-only")
	pull.Dir = srcDir
	pull.Stdout = os.Stderr
	pull.Stderr = os.Stderr
	if err := pull.Run(); err != nil {
		return cmdErr(fmt.Errorf("git pull failed: %w", err))
	}

	// Build and install
	install := exec.Command("go", "install", "./cmd/vybe")
	install.Dir = srcDir
	install.Stdout = os.Stderr
	install.Stderr = os.Stderr
	if err := install.Run(); err != nil {
		return cmdErr(fmt.Errorf("go install failed: %w", err))
	}

	newVersion := getGitVersion(srcDir)

	type result struct {
		Source    string `json:"source"`
		OldCommit string `json:"old_commit"`
		NewCommit string `json:"new_commit"`
		Updated   bool   `json:"updated"`
	}
	return output.PrintSuccess(result{
		Source:    srcDir,
		OldCommit: oldVersion,
		NewCommit: newVersion,
		Updated:   oldVersion != newVersion,
	})
}

func upgradeViaGoInstall() error {
	install := exec.Command("go", "install", "github.com/dotcommander/vybe/cmd/vybe@latest")
	install.Stdout = os.Stderr
	install.Stderr = os.Stderr
	if err := install.Run(); err != nil {
		return cmdErr(fmt.Errorf("go install failed: %w", err))
	}

	type result struct {
		Source string `json:"source"`
		Method string `json:"method"`
	}
	return output.PrintSuccess(result{
		Source: "github.com/dotcommander/vybe@latest",
		Method: "go install",
	})
}

func getGitVersion(dir string) string {
	out, err := exec.Command("git", "-C", dir, "rev-parse", "--short", "HEAD").Output()
	if err != nil {
		return "unknown"
	}
	return strings.TrimSpace(string(out))
}
