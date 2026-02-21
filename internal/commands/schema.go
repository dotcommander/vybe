package commands

import "github.com/spf13/cobra"

// NewSchemaCmd creates the schema command. root is used by schema commands to collect command schemas.
func NewSchemaCmd(root *cobra.Command) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "schema",
		Short: "Inspect command schemas for agent planning",
		Args:  cobra.NoArgs,
	}
	cmd.AddCommand(newSchemaCommandsCmd(root))
	return cmd
}

func newSchemaCommandsCmd(root *cobra.Command) *cobra.Command {
	return &cobra.Command{
		Use:   "commands",
		Short: "Show command argument schemas with mutation hints",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runSchemaMode(root)
		},
	}
}
