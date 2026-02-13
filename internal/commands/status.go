package commands

import (
	"encoding/json"
	"os"
	"sort"

	"github.com/dotcommander/vybe/internal/app"
	"github.com/dotcommander/vybe/internal/output"
	"github.com/dotcommander/vybe/internal/store"
	"github.com/spf13/cobra"
)

func NewStatusCmd() *cobra.Command {
	var check bool

	cmd := &cobra.Command{
		Use:   "status",
		Short: "Show vybe installation status and system overview",
		RunE: func(cmd *cobra.Command, args []string) error {
			// 1. Resolve DB path
			dbPath, dbSource, err := app.ResolveDBPathDetailed()
			if err != nil {
				return cmdErr(err)
			}

			// 2. Build response structure
			type dbInfo struct {
				Path      string `json:"path"`
				Source    string `json:"source"`
				OK        bool   `json:"ok"`
				SizeBytes *int64 `json:"size_bytes,omitempty"`
				Error     string `json:"error,omitempty"`
			}

			type hooksInfo struct {
				Claude         bool            `json:"claude"`
				ClaudeEvents   map[string]bool `json:"claude_events,omitempty"`
				ClaudeSettings []string        `json:"claude_settings_paths,omitempty"`
				OpenCode       opencodeDetail  `json:"opencode"`
			}

			type resp struct {
				DB         dbInfo              `json:"db"`
				Hooks      hooksInfo           `json:"hooks"`
				Counts     *store.StatusCounts `json:"counts,omitempty"`
				QueryOK    *bool               `json:"query_ok,omitempty"`
				QueryError string              `json:"query_error,omitempty"`
				Hint       string              `json:"hint,omitempty"`
			}

			result := resp{
				DB: dbInfo{
					Path:   dbPath,
					Source: dbSource,
				},
			}

			// 3. Check hooks
			result.Hooks.Claude, result.Hooks.ClaudeEvents, result.Hooks.ClaudeSettings = checkClaudeHook()
			result.Hooks.OpenCode = checkOpenCodeHookDetail()

			// 4. Try to open DB
			db, err := store.InitDBWithPath(dbPath)
			if err != nil {
				result.DB.OK = false
				result.DB.Error = err.Error()
				if check {
					qOK := false
					result.QueryOK = &qOK
					result.QueryError = "db not available"
					result.Hint = "If this is running in a sandboxed environment, set db_path to a writable location or use --db-path."
				}
			} else {
				result.DB.OK = true
				defer db.Close()

				// 5. Get DB file size
				if stat, err := os.Stat(dbPath); err == nil {
					size := stat.Size()
					result.DB.SizeBytes = &size
				}

				// 6. Get counts
				if counts, err := store.GetStatusCounts(db); err == nil {
					result.Counts = counts
				}

				// 7. Health check (--check): run SELECT 1 to verify connectivity
				if check {
					var one int
					if qErr := db.QueryRow("SELECT 1").Scan(&one); qErr != nil {
						qOK := false
						result.QueryOK = &qOK
						result.QueryError = qErr.Error()
					} else {
						qOK := true
						result.QueryOK = &qOK
					}
				}
			}

			return output.PrintSuccess(result)
		},
	}

	cmd.Flags().BoolVar(&check, "check", false, "Run database connectivity check (SELECT 1)")

	return cmd
}

// checkClaudeHook checks if vybe hooks are installed in Claude settings.
func checkClaudeHook() (bool, map[string]bool, []string) {
	paths := []string{claudeSettingsPath(), projectClaudeSettingsPath()}
	events := make(map[string]bool)
	for _, name := range vybeHookEventNames() {
		events[name] = false
	}

	foundPaths := make([]string, 0, len(paths))
	installedAny := false

	for _, path := range paths {
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		foundPaths = append(foundPaths, path)

		var settings struct {
			Hooks map[string][]any `json:"hooks"`
		}
		if err := json.Unmarshal(data, &settings); err != nil {
			continue
		}

		for eventName, entries := range settings.Hooks {
			if !hasVybeHook(entries) {
				continue
			}
			installedAny = true
			events[eventName] = true
		}
	}

	sort.Strings(foundPaths)
	return installedAny, events, foundPaths
}

type opencodeDetail struct {
	Installed bool   `json:"installed"`
	Path      string `json:"path"`
	Status    string `json:"status"` // "current", "modified", "missing"
}

// checkOpenCodeHookDetail checks vybe bridge plugin status in OpenCode.
func checkOpenCodeHookDetail() opencodeDetail {
	path := opencodePluginPath()
	data, err := os.ReadFile(path)
	if err != nil {
		return opencodeDetail{Path: path, Status: "missing"}
	}
	status := "modified"
	if string(data) == opencodeBridgePluginSource {
		status = "current"
	}
	return opencodeDetail{Installed: true, Path: path, Status: status}
}
