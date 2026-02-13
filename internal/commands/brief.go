package commands

import (
	"github.com/dotcommander/vybe/internal/actions"
	"github.com/dotcommander/vybe/internal/output"
	"github.com/dotcommander/vybe/internal/store"
	"github.com/spf13/cobra"
)

// NewBriefCmd creates the brief command
func NewBriefCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "brief",
		Short: "Get brief for agent's current focus task",
		Long: `Brief returns a brief packet for the agent's current focus task without advancing the cursor.
Useful for "remind me what I'm working on" scenarios.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			agentName, err := requireActorName(cmd, "")
			if err != nil {
				return cmdErr(err)
			}

			var brief *store.BriefPacket
			if err := withDB(func(db *DB) error {
				b, err := actions.Brief(db, agentName)
				if err != nil {
					return err
				}
				brief = b
				return nil
			}); err != nil {
				return err
			}

			type resp struct {
				AgentName string             `json:"agent_name"`
				Brief     *store.BriefPacket `json:"brief"`
			}
			return output.PrintSuccess(resp{AgentName: agentName, Brief: brief})
		},
	}

	return cmd
}
