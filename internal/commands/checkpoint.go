package commands

import (
	"github.com/dotcommander/vybe/internal/actions"
	"github.com/dotcommander/vybe/internal/output"

	"github.com/spf13/cobra"
)

// NewCheckpointCmd creates the checkpoint command.
func NewCheckpointCmd() *cobra.Command {
	var project string

	cmd := &cobra.Command{
		Use:   "checkpoint",
		Short: "Capture point-in-time state snapshot with diff against previous",
		Long: `Checkpoint captures the current agent state (focus task, cursor position,
task counts) and compares against the previous checkpoint to show what changed.

Useful for human-readable progress summaries across sessions.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			agentName, err := requireActorName(cmd, "")
			if err != nil {
				return cmdErr(err)
			}
			requestID, err := requireRequestID(cmd)
			if err != nil {
				return cmdErr(err)
			}

			return withDB(func(db *DB) error {
				result, err := actions.CheckpointIdempotent(db, agentName, requestID, project)
				if err != nil {
					return err
				}
				return output.PrintSuccess(result)
			})
		},
	}

	cmd.Flags().StringVar(&project, "project", "", "Project ID to scope checkpoint")

	return cmd
}
