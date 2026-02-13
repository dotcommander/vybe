package commands

import (
	"github.com/spf13/cobra"

	"github.com/dotcommander/vybe/internal/app"
	"github.com/dotcommander/vybe/internal/output"
)

func NewDBCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "db",
		Short: "Database utilities",
	}

	cmd.AddCommand(newDBPathCmd())
	return cmd
}

func newDBPathCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "path",
		Short: "Print the resolved database path",
		RunE: func(cmd *cobra.Command, args []string) error {
			path, source, err := app.ResolveDBPathDetailed()
			if err != nil {
				return cmdErr(err)
			}

			type resp struct {
				Path   string `json:"path"`
				Source string `json:"source"`
			}
			return output.PrintSuccess(resp{Path: path, Source: source})
		},
	}
	return cmd
}
