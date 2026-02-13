package commands

import (
	"github.com/spf13/cobra"

	"github.com/dotcommander/vibe/internal/app"
	"github.com/dotcommander/vibe/internal/output"
	"github.com/dotcommander/vibe/internal/store"
)

func NewDoctorCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "doctor",
		Short: "Check configuration and database connectivity",
		RunE: func(cmd *cobra.Command, args []string) error {
			agent := resolveActorName(cmd, "")
			dbPath, dbSource, err := app.ResolveDBPathDetailed()
			if err != nil {
				return cmdErr(err)
			}

			var (
				dbOK     bool
				dbErr    string
				queryOK  bool
				queryErr string
			)

			db, err := store.InitDBWithPath(dbPath)
			if err != nil {
				dbOK = false
				dbErr = err.Error()
			} else {
				dbOK = true
				defer db.Close()
			}

			if dbOK {
				var one int
				if err := db.QueryRow("SELECT 1").Scan(&one); err != nil {
					queryOK = false
					queryErr = err.Error()
				} else {
					queryOK = true
				}
			} else {
				queryOK = false
				queryErr = "db not available"
			}

			type resp struct {
				Agent    string `json:"agent,omitempty"`
				DBPath   string `json:"db_path"`
				DBSource string `json:"db_source"`
				DBOK     bool   `json:"db_ok"`
				DBErr    string `json:"db_error,omitempty"`
				QueryOK  bool   `json:"query_ok"`
				QueryErr string `json:"query_error,omitempty"`
				Hint     string `json:"hint,omitempty"`
			}
			hint := ""
			if !dbOK {
				hint = "If this is running in a sandboxed environment, set db_path to a writable location or use --db-path."
			}
			return output.PrintSuccess(resp{
				Agent:    agent,
				DBPath:   dbPath,
				DBSource: dbSource,
				DBOK:     dbOK,
				DBErr:    dbErr,
				QueryOK:  queryOK,
				QueryErr: queryErr,
				Hint:     hint,
			})
		},
	}

	// keep a local hidden flag in case we want to expand later without changing UX
	cmd.Flags().Bool("verbose", false, "Show more details")
	_ = cmd.Flags().MarkHidden("verbose")

	return cmd
}
