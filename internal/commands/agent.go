package commands

import (
	"errors"

	"github.com/dotcommander/vybe/internal/models"
	"github.com/dotcommander/vybe/internal/output"
	"github.com/dotcommander/vybe/internal/store"
	"github.com/spf13/cobra"
)

// NewAgentCmd creates the agent command group
func NewAgentCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "agent",
		Short: "Manage agent state",
		Long:  "Initialize and query agent state",
	}

	cmd.AddCommand(newAgentInitCmd())
	cmd.AddCommand(newAgentStatusCmd())
	cmd.AddCommand(newAgentFocusCmd())

	return cmd
}

//nolint:gocognit // focus command handles multiple optional args (task, project, clear) with validation and conflict detection
func newAgentFocusCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "focus",
		Short: "Set focus task and/or project for the current agent",
		RunE: func(cmd *cobra.Command, args []string) error {
			taskID, _ := cmd.Flags().GetString("task")
			projectID, _ := cmd.Flags().GetString("project")
			if taskID == "" && projectID == "" {
				return cmdErr(errors.New("--task and/or --project is required"))
			}

			agentName, err := requireActorName(cmd, "")
			if err != nil {
				return cmdErr(err)
			}
			requestID, err := requireRequestID(cmd)
			if err != nil {
				return cmdErr(err)
			}

			var taskEventID, projectEventID int64
			if err := withDB(func(db *DB) error {
				// Ensure agent state exists.
				if _, err := store.LoadOrCreateAgentState(db, agentName); err != nil {
					return err
				}
				if taskID != "" {
					eid, err := store.SetAgentFocusTaskWithEventIdempotent(db, agentName, requestID, taskID)
					if err != nil {
						return err
					}
					taskEventID = eid
				}
				if projectID != "" {
					// Use a distinct request ID for the project focus to avoid collision
					projRequestID := requestID + ":project"
					eid, err := store.SetAgentFocusProjectWithEventIdempotent(db, agentName, projRequestID, projectID)
					if err != nil {
						return err
					}
					projectEventID = eid
				}
				return nil
			}); err != nil {
				return err
			}

			type resp struct {
				Agent          string `json:"agent"`
				TaskID         string `json:"task_id,omitempty"`
				ProjectID      string `json:"project_id,omitempty"`
				TaskEventID    int64  `json:"task_event_id,omitempty"`
				ProjectEventID int64  `json:"project_event_id,omitempty"`
			}
			return output.PrintSuccess(resp{
				Agent:          agentName,
				TaskID:         taskID,
				ProjectID:      projectID,
				TaskEventID:    taskEventID,
				ProjectEventID: projectEventID,
			})
		},
	}

	cmd.Flags().String("task", "", "Task ID to focus")
	cmd.Flags().String("project", "", "Project ID to focus")
	return cmd
}

func newAgentInitCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "init",
		Short: "Initialize or update agent state",
		RunE: func(cmd *cobra.Command, args []string) error {
			agentName, _ := cmd.Flags().GetString("name") // legacy
			if agentName == "" {
				var err error
				agentName, err = requireActorName(cmd, "")
				if err != nil {
					return cmdErr(err)
				}
			}
			requestID, err := requireRequestID(cmd)
			if err != nil {
				return cmdErr(err)
			}

			var state *models.AgentState
			if err := withDB(func(db *DB) error {
				s, err := store.LoadOrCreateAgentStateIdempotent(db, agentName, requestID)
				if err != nil {
					return err
				}
				state = s
				return nil
			}); err != nil {
				return err
			}

			type resp struct {
				AgentName string             `json:"agent_name"`
				State     *models.AgentState `json:"state"`
			}
			return output.PrintSuccess(resp{AgentName: agentName, State: state})
		},
	}

	cmd.Flags().String("name", "", "Deprecated: use --agent")
	_ = cmd.Flags().MarkDeprecated("name", "use --agent")

	return cmd
}

func newAgentStatusCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Get agent status",
		RunE: func(cmd *cobra.Command, args []string) error {
			agentName, _ := cmd.Flags().GetString("name") // legacy
			if agentName == "" {
				var err error
				agentName, err = requireActorName(cmd, "")
				if err != nil {
					return cmdErr(err)
				}
			}

			var state *models.AgentState
			if err := withDB(func(db *DB) error {
				s, err := store.GetAgentState(db, agentName)
				if err != nil {
					return err
				}
				state = s
				return nil
			}); err != nil {
				return err
			}

			return output.PrintSuccess(state)
		},
	}

	cmd.Flags().String("name", "", "Deprecated: use --agent")
	_ = cmd.Flags().MarkDeprecated("name", "use --agent")

	return cmd
}
