package commands

import (
	"github.com/dotcommander/vybe/internal/actions"
	"github.com/dotcommander/vybe/internal/output"
	"github.com/spf13/cobra"
)

// NewSessionCmd creates the session parent command.
func NewSessionCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "session",
		Short: "Session lifecycle commands",
	}

	cmd.AddCommand(newSessionDigestCmd())

	return cmd
}

func newSessionDigestCmd() *cobra.Command {
	return &cobra.Command{
		Use:           "digest",
		Short:         "Show session event digest for an agent",
		Long:          `Summarizes the current session's events by kind for the active agent.`,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			agentName, err := requireActorName(cmd, "")
			if err != nil {
				return cmdErr(err)
			}

			var result *actions.SessionDigestResult
			if err := withDB(func(db *DB) error {
				var err error
				result, err = actions.SessionDigest(db, agentName)
				return err
			}); err != nil {
				return cmdErr(err)
			}

			return output.PrintSuccess(result)
		},
	}
}
