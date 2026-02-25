package commands

import (
	"github.com/dotcommander/vybe/internal/commands/hookcmd"
	"github.com/spf13/cobra"
)

// newHookInstallCmd creates the hook install command (delegates to hookcmd).
func newHookInstallCmd() *cobra.Command {
	return hookcmd.NewInstallCmd()
}

// newHookUninstallCmd creates the hook uninstall command (delegates to hookcmd).
func newHookUninstallCmd() *cobra.Command {
	return hookcmd.NewUninstallCmd()
}
