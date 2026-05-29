package commands

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/dotcommander/vybe/internal/app"
	"github.com/dotcommander/vybe/internal/store"
)

// resolveActorName resolves the agent used for event attribution and agent_state identity.
// Precedence:
//  1. per-command flag override (when a command defines one)
//  2. global flag --agent
//  3. env var VYBE_AGENT
//  4. config.default_agent (persistent fallback; best-effort — a config read error
//     is treated as no value, never as a hard failure here)
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
		raw = os.Getenv("VYBE_AGENT")
	}
	if raw == "" {
		if s, err := app.LoadSettings(); err == nil {
			raw = s.DefaultAgent
		}
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
