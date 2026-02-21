package commands

import (
	"github.com/spf13/cobra"
)

// NewEventsCmd creates the events command group.
func NewEventsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "events",
		Short: "Query the event stream",
		Args:  cobra.NoArgs,
	}
	cmd.AddCommand(newEventsListCmd())
	namespaceIndex(cmd)
	return cmd
}

func newEventsListCmd() *cobra.Command {
	var (
		all             bool
		taskID          string
		kind            string
		limit           int
		since           int64
		asc             bool
		includeArchived bool
	)

	cmd := &cobra.Command{
		Use:   "list",
		Short: "List events from the event stream",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runEventsMode(cmd, all, taskID, kind, since, limit, asc, includeArchived)
		},
	}

	cmd.Flags().BoolVar(&all, "all", false, "List events across all agents (ignores --agent)")
	cmd.Flags().StringVar(&taskID, "task-id", "", "Filter events by task ID")
	cmd.Flags().StringVar(&kind, "kind", "", "Filter events by kind")
	cmd.Flags().IntVar(&limit, "limit", 50, "Max events to return")
	cmd.Flags().Int64Var(&since, "since-id", 0, "Only events with id > since-id")
	cmd.Flags().BoolVar(&asc, "asc", false, "Sort oldest first (default newest first)")
	cmd.Flags().BoolVar(&includeArchived, "include-archived", false, "Include archived events")

	return cmd
}
