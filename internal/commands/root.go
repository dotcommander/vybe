package commands

import (
	"errors"
	"log/slog"
	"os"

	"github.com/spf13/cobra"

	"github.com/dotcommander/vybe/internal/app"
	"github.com/dotcommander/vybe/internal/output"
)

// Execute runs the CLI application.
func Execute(version string) error {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stderr, nil)))

	root := &cobra.Command{
		Use:           "vybe",
		Short:         "Agent continuity primitives (resume, push, task, memory, status)",
		SilenceUsage:  true,
		SilenceErrors: true,
		CompletionOptions: cobra.CompletionOptions{
			DisableDefaultCmd: true,
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			showVersion, _ := cmd.Flags().GetBool("version")
			if showVersion {
				type resp struct {
					Version string `json:"version"`
				}
				return output.PrintSuccess(resp{Version: version})
			}
			return cmd.Help()
		},
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			if err := app.EnsureConfigDir(); err != nil {
				return err
			}

			// Wire --db-path into app-level resolver.
			if dbPath, err := cmd.Flags().GetString("db-path"); err == nil && dbPath != "" {
				app.SetDBPathOverride(dbPath)
			}

			return nil
		},
	}

	root.PersistentFlags().String("db-path", "", "Override database path")
	root.PersistentFlags().StringP("agent", "a", "", "Agent name (default: $VYBE_AGENT)")
	root.PersistentFlags().String("actor", "", "Deprecated: use --agent")
	_ = root.PersistentFlags().MarkDeprecated("actor", "use --agent")
	root.PersistentFlags().String("request-id", "", "Idempotency key for mutating operations (default: $VYBE_REQUEST_ID)")
	root.Flags().BoolP("version", "v", false, "version for vybe")

	root.AddCommand(NewTaskCmd())
	root.AddCommand(NewMemoryCmd())
	root.AddCommand(NewResumeCmd())
	root.AddCommand(NewLoopCmd())
	root.AddCommand(NewHookCmd())
	root.AddCommand(NewStatusCmd(root)) // root passed for --schema mode
	root.AddCommand(NewUpgradeCmd())
	root.AddCommand(NewPushCmd())

	err := root.Execute()
	if err != nil {
		var pe printedError
		if !errors.As(err, &pe) {
			slog.Default().Error("command failed", "error", err.Error())
		}
	}
	return err
}
