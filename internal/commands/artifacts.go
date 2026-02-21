package commands

import "github.com/spf13/cobra"

// NewArtifactsCmd creates the artifacts command group.
func NewArtifactsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "artifacts",
		Short: "Query task artifacts",
		Args:  cobra.NoArgs,
	}
	cmd.AddCommand(newArtifactsListCmd())
	return cmd
}

func newArtifactsListCmd() *cobra.Command {
	var (
		taskID string
		limit  int
	)

	cmd := &cobra.Command{
		Use:   "list",
		Short: "List artifacts linked to a task",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runArtifactsMode(taskID, limit)
		},
	}

	cmd.Flags().StringVar(&taskID, "task-id", "", "Task ID (required)")
	cmd.Flags().IntVar(&limit, "limit", 50, "Max artifacts to return")

	return cmd
}
