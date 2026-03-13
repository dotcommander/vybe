// Package hookcmd provides hook installation and uninstallation commands.
// This package is separate from the main commands package to allow independent
// evolution of hook lifecycle management.
//
// Internal layout:
//   - registry.go  — hook definitions (what hooks exist and their timeouts)
//   - claude.go    — Claude Code settings I/O (read, merge, install, uninstall)
//   - opencode.go  — OpenCode config and plugin file management
//   - hookcmd.go   — thin coordinator: CLI commands and message assembly
package hookcmd

import (
	"fmt"
	"log/slog"
	"strings"

	"github.com/dotcommander/vybe/internal/app"
	"github.com/dotcommander/vybe/internal/output"
	"github.com/dotcommander/vybe/internal/store"
	"github.com/spf13/cobra"
)

func ensureHookAgentStateBestEffort(agentName string) {
	if strings.TrimSpace(agentName) == "" {
		return
	}

	dbPath, err := app.GetDBPath()
	if err != nil {
		slog.Default().Warn("hook install: resolve db path failed", "error", err)
		return
	}

	db, err := store.OpenDB(dbPath)
	if err != nil {
		slog.Default().Warn("hook install: open db failed", "error", err)
		return
	}
	defer func() { _ = store.CloseDB(db) }()

	if err := store.RunMigrations(db); err != nil {
		slog.Default().Warn("hook install: run migrations failed", "error", err)
		return
	}

	if _, err := store.LoadOrCreateAgentState(db, agentName); err != nil {
		slog.Default().Warn("hook install: initialize agent state failed", "agent", agentName, "error", err)
	}
}

// ResolveTargetFlags returns (claude, opencode) based on explicit flag usage.
// Default: Claude only when no flags specified.
func ResolveTargetFlags(cmd *cobra.Command) (claude bool, opencode bool, err error) {
	const claudeFlag = "claude"
	const opencodeFlag = "opencode"

	claude = cmd.Flags().Changed(claudeFlag)
	opencode = cmd.Flags().Changed(opencodeFlag)

	if !claude && !opencode {
		return true, false, nil
	}

	c, _ := cmd.Flags().GetBool(claudeFlag)
	o, _ := cmd.Flags().GetBool(opencodeFlag)

	if !c && !o {
		return false, false, fmt.Errorf("nothing selected: use --%s and/or --%s", claudeFlag, opencodeFlag)
	}

	return c, o, nil
}

// buildInstallMessage assembles a human-readable summary of the install operation.
func buildInstallMessage(claude *claudeInstallResult, opencode *opencodeInstallResult) string {
	var parts []string
	if claude != nil {
		if len(claude.Installed) > 0 {
			parts = append(parts, fmt.Sprintf("Claude Code hooks installed (%s)", strings.Join(claude.Installed, ", ")))
		}
		if len(claude.Updated) > 0 {
			parts = append(parts, fmt.Sprintf("Claude Code hooks updated (%s)", strings.Join(claude.Updated, ", ")))
		}
		if len(claude.Installed) == 0 && len(claude.Updated) == 0 {
			parts = append(parts, "Claude Code hooks already installed")
		}
	}
	if opencode != nil {
		switch opencode.Status {
		case "installed":
			parts = append(parts, "OpenCode bridge plugin installed")
		case "updated":
			parts = append(parts, "OpenCode bridge plugin updated")
		default:
			parts = append(parts, "OpenCode bridge plugin already installed")
		}
	}
	if len(parts) == 0 {
		return ""
	}
	return strings.Join(parts, "; ") + ". Run 'vybe status' to verify."
}

// NewInstallCmd creates the hook install command.
func NewInstallCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "install",
		Short: "Install vybe hooks for Claude and/or OpenCode",
		Long:  "Installs Claude Code hooks and/or OpenCode bridge plugin.",
		RunE: func(cmd *cobra.Command, args []string) error {
			installClaude, installOpenCode, err := ResolveTargetFlags(cmd)
			if err != nil {
				return err
			}

			projectScoped, _ := cmd.Flags().GetBool("project")

			type result struct {
				Message  string                 `json:"message"`
				Claude   *claudeInstallResult   `json:"claude,omitempty"`
				OpenCode *opencodeInstallResult `json:"opencode,omitempty"`
			}

			resp := result{}

			if installClaude {
				resp.Claude, err = installClaudeHooks(projectScoped)
				if err != nil {
					return err
				}
			}

			if installOpenCode {
				resp.OpenCode, err = installOpenCodePlugin()
				if err != nil {
					return err
				}
			}

			resp.Message = buildInstallMessage(resp.Claude, resp.OpenCode)

			return output.PrintSuccess(resp)
		},
	}

	cmd.Flags().Bool("claude", false, "Install Claude Code hooks")
	cmd.Flags().Bool("opencode", false, "Install OpenCode bridge plugin")
	cmd.Flags().Bool("project", false, "Install Claude hooks in ./.claude/settings.json")

	return cmd
}

// NewUninstallCmd creates the hook uninstall command.
func NewUninstallCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "uninstall",
		Short: "Remove vybe hooks for Claude and/or OpenCode",
		Long:  "Removes Claude Code hook entries and/or OpenCode bridge plugin.",
		RunE: func(cmd *cobra.Command, args []string) error {
			uninstallClaude, uninstallOpenCode, err := ResolveTargetFlags(cmd)
			if err != nil {
				return err
			}

			projectScoped, _ := cmd.Flags().GetBool("project")

			type result struct {
				Claude   *claudeUninstallResult   `json:"claude,omitempty"`
				OpenCode *opencodeUninstallResult `json:"opencode,omitempty"`
			}

			resp := result{}

			if uninstallClaude {
				resp.Claude, err = uninstallClaudeHooks(projectScoped)
				if err != nil {
					return err
				}
			}

			if uninstallOpenCode {
				force, _ := cmd.Flags().GetBool("force")
				resp.OpenCode, err = uninstallOpenCodePlugin(force)
				if err != nil {
					return err
				}
			}

			return output.PrintSuccess(resp)
		},
	}

	cmd.Flags().Bool("claude", false, "Uninstall Claude Code hooks")
	cmd.Flags().Bool("opencode", false, "Uninstall OpenCode bridge plugin")
	cmd.Flags().Bool("project", false, "Uninstall Claude hooks from ./.claude/settings.json")
	cmd.Flags().Bool("force", false, "Remove modified OpenCode plugin file")

	return cmd
}

// NewHookCmd creates the hook parent command with install and uninstall subcommands.
func NewHookCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "hook",
		Short: "Hook installation and management for Claude/OpenCode",
		Args:  cobra.NoArgs,
	}

	cmd.AddCommand(NewInstallCmd())
	cmd.AddCommand(NewUninstallCmd())

	return cmd
}
