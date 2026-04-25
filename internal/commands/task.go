package commands

import (
	"errors"

	"github.com/dotcommander/vybe/internal/actions"
	"github.com/dotcommander/vybe/internal/models"
	"github.com/dotcommander/vybe/internal/output"
	"github.com/spf13/cobra"
)

// taskCmdResult is the common response for task mutation commands.
type taskCmdResult struct {
	Task    *models.Task `json:"task"`
	EventID int64        `json:"event_id"`
}

// requireMutationParams resolves the agent name and request ID required for all
// mutating commands. It returns a cmdErr-wrapped error ready to return from RunE.
func requireMutationParams(cmd *cobra.Command) (agentName, requestID string, err error) {
	agentName, err = requireActorName(cmd, "")
	if err != nil {
		return "", "", cmdErr(err)
	}
	requestID, err = requireRequestID(cmd)
	if err != nil {
		return "", "", cmdErr(err)
	}
	return agentName, requestID, nil
}

// runTaskCmd handles the common scaffold for task mutation commands:
// requireActorName, requireRequestID, withDB, and PrintSuccess.
func runTaskCmd(cmd *cobra.Command, fn func(db *DB, agentName, requestID string) (taskCmdResult, error)) error {
	agentName, requestID, err := requireMutationParams(cmd)
	if err != nil {
		return err
	}

	var result taskCmdResult
	if err := withDB(func(db *DB) error {
		r, err := fn(db, agentName, requestID)
		if err != nil {
			return err
		}
		result = r
		return nil
	}); err != nil {
		return err
	}

	return output.PrintSuccess(result)
}

// NewTaskCmd creates the task command group
func NewTaskCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "task",
		Short: "Manage tasks",
		Long:  "Create, update, and query tasks. Valid statuses: pending, in_progress, completed, blocked",
		Args:  cobra.NoArgs,
	}

	cmd.AddCommand(newTaskCreateCmd())
	cmd.AddCommand(newTaskBeginCmd())
	cmd.AddCommand(newTaskSetStatusCmd())
	cmd.AddCommand(newTaskGetCmd())
	cmd.AddCommand(newTaskListCmd())

	namespaceIndex(cmd)
	return cmd
}

func newTaskCreateCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "create",
		Short: "Create a new task",
		RunE: func(cmd *cobra.Command, args []string) error {
			title, _ := cmd.Flags().GetString("title")
			desc, _ := cmd.Flags().GetString("desc")
			projectID, _ := cmd.Flags().GetString("project-id")
			priority, _ := cmd.Flags().GetInt("priority")

			if title == "" {
				return cmdErr(errors.New("--title is required"))
			}

			return runTaskCmd(cmd, func(db *DB, agentName, requestID string) (taskCmdResult, error) {
				t, eid, err := actions.TaskCreateIdempotent(db, agentName, requestID, title, desc, projectID, priority)
				return taskCmdResult{Task: t, EventID: eid}, err
			})
		},
	}

	cmd.Flags().String("title", "", "Task title (required)")
	cmd.Flags().String("desc", "", "Task description")
	cmd.Flags().String("project-id", "", "Project ID to associate task with")
	cmd.Flags().Int("priority", 0, "Task priority (higher = more urgent, default 0)")

	cmd.Annotations = map[string]string{"mutates": "true", "request_id": "true"}
	return cmd
}

// newTaskSetStatusCmd updates task status
func newTaskSetStatusCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "set-status",
		Short: "Update task status (pending|in_progress|completed|blocked)",
		Long:  "Update task status (pending, in_progress, completed, blocked)",
		RunE: func(cmd *cobra.Command, args []string) error {
			taskID, _ := cmd.Flags().GetString("id")
			status, _ := cmd.Flags().GetString("status")
			blockedReason, _ := cmd.Flags().GetString("blocked-reason")

			if taskID == "" {
				return cmdErr(errors.New("--id is required"))
			}
			if status == "" {
				return cmdErr(errors.New("--status is required"))
			}

			return runTaskCmd(cmd, func(db *DB, agentName, requestID string) (taskCmdResult, error) {
				t, eid, err := actions.TaskSetStatusIdempotent(db, agentName, requestID, taskID, status, blockedReason)
				return taskCmdResult{Task: t, EventID: eid}, err
			})
		},
	}

	cmd.Flags().String("id", "", "Task ID (required)")
	cmd.Flags().String("status", "", "New status (required): pending|in_progress|completed|blocked")
	cmd.Flags().String("blocked-reason", "", "Reason for blocking (used with --status=blocked)")

	cmd.Annotations = map[string]string{"mutates": "true", "request_id": "true"}
	return cmd
}

func newTaskBeginCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "begin",
		Short: "Set task in_progress and focus it for the agent",
		RunE: func(cmd *cobra.Command, args []string) error {
			taskID, _ := cmd.Flags().GetString("id")
			if taskID == "" {
				return cmdErr(errors.New("--id is required"))
			}

			agentName, requestID, err := requireMutationParams(cmd)
			if err != nil {
				return err
			}

			var result *actions.TaskStartResult
			if err := withDB(func(db *DB) error {
				var startErr error
				result, startErr = actions.TaskStartIdempotent(db, agentName, requestID, taskID)
				return startErr
			}); err != nil {
				return err
			}

			type resp struct {
				Task          *models.Task `json:"task"`
				StatusEventID int64        `json:"status_event_id,omitempty"`
				FocusEventID  int64        `json:"focus_event_id"`
			}
			return output.PrintSuccess(resp{Task: result.Task, StatusEventID: result.StatusEventID, FocusEventID: result.FocusEventID})
		},
	}

	cmd.Flags().String("id", "", "Task ID (required)")
	cmd.Annotations = map[string]string{"mutates": "true", "request_id": "true"}
	return cmd
}

func newTaskListCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List all tasks (status filter supports pending|in_progress|completed|blocked)",
		RunE: func(cmd *cobra.Command, args []string) error {
			statusFilter, _ := cmd.Flags().GetString("status")
			projectFilter, _ := cmd.Flags().GetString("project-id")
			priorityFilter, _ := cmd.Flags().GetInt("priority")

			var tasks []*models.Task
			if err := withDB(func(db *DB) error {
				t, err := actions.TaskList(db, statusFilter, projectFilter, priorityFilter)
				if err != nil {
					return err
				}
				tasks = t
				return nil
			}); err != nil {
				return err
			}

			type resp struct {
				Count int            `json:"count"`
				Tasks []*models.Task `json:"tasks"`
			}
			return output.PrintSuccess(resp{Count: len(tasks), Tasks: tasks})
		},
	}

	cmd.Flags().String("status", "", "Filter by status: pending|in_progress|completed|blocked")
	cmd.Flags().String("project-id", "", "Filter by project ID")
	cmd.Flags().Int("priority", -1, "Filter by exact priority (default -1 = no filter)")

	return cmd
}

func newTaskGetCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "get",
		Short: "Get task details",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			taskID, _ := cmd.Flags().GetString("id")
			if taskID == "" {
				return cmdErr(errors.New("--id is required"))
			}

			var task *models.Task
			if err := withDB(func(db *DB) error {
				t, err := actions.TaskGet(db, taskID)
				if err != nil {
					return err
				}
				task = t
				return nil
			}); err != nil {
				return err
			}

			return output.PrintSuccess(task)
		},
	}

	cmd.Flags().String("id", "", "Task ID (required)")

	return cmd
}
