package commands

import "github.com/spf13/cobra"

// NewArtifactsCmd creates the artifacts command.
func NewArtifactsCmd() *cobra.Command {
	var (
		taskID string
		limit  int
	)

	cmd := &cobra.Command{
		Use:   "artifacts",
		Short: "List artifacts linked to a task",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runArtifactsMode(taskID, limit)
		},
	}

	cmd.Flags().StringVar(&taskID, "task-id", "", "Task ID (required)")
	cmd.Flags().IntVar(&limit, "limit", 50, "Max artifacts to return")

	return cmd
}
