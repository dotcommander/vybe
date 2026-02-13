package commands

import (
	"github.com/dotcommander/vibe/internal/actions"
	"github.com/dotcommander/vibe/internal/output"
	"github.com/spf13/cobra"
)

// NewResumeCmd creates the resume command
func NewResumeCmd() *cobra.Command {
	var (
		limit   int
		project string
	)

	cmd := &cobra.Command{
		Use:   "resume",
		Short: "Resume agent work with deltas and focus task",
		Long: `Resume retrieves events since last cursor, determines focus task using deterministic rules,
and builds a brief packet with all context needed to resume work.

The cursor is advanced monotonically and the focus task is updated atomically.
Use --project to scope resume to a specific project directory.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			agentName, err := requireActorName(cmd, "")
			if err != nil {
				return cmdErr(err)
			}
			requestID, err := requireRequestID(cmd)
			if err != nil {
				return cmdErr(err)
			}

			var response *actions.ResumeResponse
			if err := withDB(func(db *DB) error {
				r, err := actions.ResumeWithOptionsIdempotent(db, agentName, requestID, actions.ResumeOptions{
					EventLimit: limit,
					ProjectDir: project,
				})
				if err != nil {
					return err
				}
				response = r
				return nil
			}); err != nil {
				return err
			}

			return output.PrintSuccess(response)
		},
	}

	cmd.Flags().IntVar(&limit, "limit", 1000, "Max delta events to return (<= 1000)")
	cmd.Flags().StringVar(&project, "project", "", "Scope resume to a project directory path")

	return cmd
}
