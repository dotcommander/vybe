package commands

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

// resolveActorName resolves the agent used for event attribution and agent_state identity.
// Precedence:
// 1) per-command flag (legacy, e.g. --agent on a subcommand)
// 2) global flag --agent
// 3) legacy global flag --actor (DEPRECATED — will be removed in v1.0)
// 4) env var VYBE_AGENT
//
// Deprecation plan: --actor is kept for backward compatibility through v0.x.
// It will be removed in v1.0. Callers should migrate to --agent or VYBE_AGENT.
func resolveActorName(cmd *cobra.Command, perCmdFlag string) string {
	if perCmdFlag != "" {
		if v, err := cmd.Flags().GetString(perCmdFlag); err == nil && v != "" {
			return v
		}
	}
	if v, err := cmd.Flags().GetString("agent"); err == nil && v != "" {
		return v
	}
	if v, err := cmd.Flags().GetString("actor"); err == nil && v != "" {
		return v
	}
	return os.Getenv("VYBE_AGENT")
}

func requireActorName(cmd *cobra.Command, perCmdFlag string) (string, error) {
	agent := resolveActorName(cmd, perCmdFlag)
	if agent == "" {
		return "", fmt.Errorf("agent is required (set --agent or VYBE_AGENT)")
	}
	return agent, nil
}
