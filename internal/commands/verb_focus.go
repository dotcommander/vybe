package commands

import (
	"github.com/spf13/cobra"

	"github.com/dotcommander/vybe/internal/actions"
	"github.com/dotcommander/vybe/internal/models"
	"github.com/dotcommander/vybe/internal/output"
	"github.com/dotcommander/vybe/internal/store"
)

// newFocusCmd prints the agent's current focus task. Read-only: no cursor
// advance, no request-id. Reads agent_state.focus_task_id then hydrates the task.
func newFocusCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "focus",
		Short: "Show the current focus task for the agent (read-only, no cursor advance)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			agentName, err := requireActorName(cmd, "")
			if err != nil {
				return cmdErr(err)
			}
			type resp struct {
				FocusTaskID string       `json:"focus_task_id"`
				Task        *models.Task `json:"task"`
				Summary     string       `json:"summary"`
			}
			var out resp
			if err := withDB(func(db *DB) error {
				state, err := store.GetAgentState(db, agentName)
				if err != nil {
					return err
				}
				if state == nil || state.FocusTaskID == "" {
					out.Summary = "no focus task"
					return nil
				}
				out.FocusTaskID = state.FocusTaskID
				task, err := actions.TaskGet(db, state.FocusTaskID)
				if err != nil {
					return err
				}
				out.Task = task
				out.Summary = task.Title + " [" + string(task.Status) + "]"
				return nil
			}); err != nil {
				return err
			}
			return output.PrintSuccess(out)
		},
	}
	return cmd
}
