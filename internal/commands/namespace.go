package commands

import (
	"github.com/dotcommander/vybe/internal/output"
	"github.com/spf13/cobra"
)

// namespaceIndex sets RunE on a parent command to emit a JSON subcommand index.
// Agents invoking a bare namespace (e.g. `vybe task`) get structured output
// instead of human help text.
func namespaceIndex(cmd *cobra.Command) {
	cmd.RunE = func(c *cobra.Command, args []string) error {
		type subCmd struct {
			Name        string `json:"name"`
			Description string `json:"description"`
		}
		type resp struct {
			Namespace   string   `json:"namespace"`
			Subcommands []subCmd `json:"subcommands"`
		}
		subs := []subCmd{}
		for _, child := range c.Commands() {
			if !child.Hidden {
				subs = append(subs, subCmd{Name: child.Name(), Description: child.Short})
			}
		}
		return output.PrintSuccess(resp{
			Namespace:   c.CommandPath(),
			Subcommands: subs,
		})
	}
}
