package commands

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/dotcommander/vybe/internal/store"
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
	raw := ""
	if perCmdFlag != "" {
		if v, err := cmd.Flags().GetString(perCmdFlag); err == nil && v != "" {
			raw = v
		}
	}
	if raw == "" {
		if v, err := cmd.Flags().GetString("agent"); err == nil && v != "" {
			raw = v
		}
	}
	if raw == "" {
		if v, err := cmd.Flags().GetString("actor"); err == nil && v != "" {
			raw = v
		}
	}
	if raw == "" {
		raw = os.Getenv("VYBE_AGENT")
	}
	return strings.ToLower(strings.TrimSpace(raw))
}

func requireActorName(cmd *cobra.Command, perCmdFlag string) (string, error) {
	agent := resolveActorName(cmd, perCmdFlag)
	if agent == "" {
		return "", errors.New("agent is required (set --agent or VYBE_AGENT)")
	}
	if len(agent) > store.MaxEventAgentNameLength {
		return "", fmt.Errorf("agent name exceeds maximum length (%d chars)", store.MaxEventAgentNameLength)
	}
	return agent, nil
}
