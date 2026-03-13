package commands

import "github.com/spf13/cobra"

// NewSchemaCmd creates the schema command. root is used to collect command schemas.
func NewSchemaCmd(root *cobra.Command) *cobra.Command {
	return &cobra.Command{
		Use:   "schema",
		Short: "Show command argument schemas with mutation hints",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runSchemaMode(root)
		},
	}
}
