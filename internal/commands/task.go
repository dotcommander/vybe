package commands

import (
	"errors"
	"fmt"

	"github.com/dotcommander/vybe/internal/actions"
	"github.com/dotcommander/vybe/internal/models"
	"github.com/dotcommander/vybe/internal/output"
	"github.com/spf13/cobra"
)

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
	cmd.AddCommand(newTaskAddDepCmd())
	cmd.AddCommand(newTaskRemoveDepCmd())
	cmd.AddCommand(newTaskDeleteCmd())
	cmd.AddCommand(newTaskCompleteCmd())
	cmd.AddCommand(newTaskSetPriorityCmd())

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
			agentName, err := requireActorName(cmd, "")
			if err != nil {
				return cmdErr(err)
			}
			requestID, err := requireRequestID(cmd)
			if err != nil {
				return cmdErr(err)
			}

			if title == "" {
				return cmdErr(errors.New("--title is required"))
			}

			var task *models.Task
			var eventID int64
			if err := withDB(func(db *DB) error {
				t, eid, err := actions.TaskCreateIdempotent(db, agentName, requestID, title, desc, projectID, priority)
				if err != nil {
					return err
				}
				task = t
				eventID = eid
				return nil
			}); err != nil {
				return err
			}

			type resp struct {
				Task    *models.Task `json:"task"`
				EventID int64        `json:"event_id"`
			}
			return output.PrintSuccess(resp{Task: task, EventID: eventID})
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
			agentName, err := requireActorName(cmd, "")
			if err != nil {
				return cmdErr(err)
			}
			requestID, err := requireRequestID(cmd)
			if err != nil {
				return cmdErr(err)
			}

			if taskID == "" {
				return cmdErr(errors.New("--id is required"))
			}
			if status == "" {
				return cmdErr(errors.New("--status is required"))
			}

			var (
				task    *models.Task
				eventID int64
			)
			if err := withDB(func(db *DB) error {
				t, eid, err := actions.TaskSetStatusIdempotent(db, agentName, requestID, taskID, status, "")
				if err != nil {
					return err
				}
				task = t
				eventID = eid
				return nil
			}); err != nil {
				return err
			}

			type resp struct {
				Task    *models.Task `json:"task"`
				EventID int64        `json:"event_id"`
			}
			return output.PrintSuccess(resp{Task: task, EventID: eventID})
		},
	}

	cmd.Flags().String("id", "", "Task ID (required)")
	cmd.Flags().String("status", "", "New status (required): pending|in_progress|completed|blocked")

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

			agentName, err := requireActorName(cmd, "")
			if err != nil {
				return cmdErr(err)
			}
			requestID, err := requireRequestID(cmd)
			if err != nil {
				return cmdErr(err)
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

func newTaskDepCmd(use, short, successFmt string, apply func(db *DB, agentName, requestID, taskID, dependsOn string) error) *cobra.Command {
	cmd := &cobra.Command{
		Use:   use,
		Short: short,
		RunE: func(cmd *cobra.Command, args []string) error {
			taskID, _ := cmd.Flags().GetString("id")
			dependsOn, _ := cmd.Flags().GetString("depends-on")
			if taskID == "" {
				return cmdErr(errors.New("--id is required"))
			}
			if dependsOn == "" {
				return cmdErr(errors.New("--depends-on is required"))
			}

			agentName, err := requireActorName(cmd, "")
			if err != nil {
				return cmdErr(err)
			}
			requestID, err := requireRequestID(cmd)
			if err != nil {
				return cmdErr(err)
			}

			if err := withDB(func(db *DB) error {
				return apply(db, agentName, requestID, taskID, dependsOn)
			}); err != nil {
				return err
			}

			type resp struct {
				Message string `json:"message"`
			}
			return output.PrintSuccess(resp{Message: fmt.Sprintf(successFmt, taskID, dependsOn)})
		},
	}

	cmd.Flags().String("id", "", "Task ID (required)")
	cmd.Flags().String("depends-on", "", "Dependency task ID (required)")

	cmd.Annotations = map[string]string{"mutates": "true", "request_id": "true"}
	return cmd
}

func newTaskAddDepCmd() *cobra.Command {
	return newTaskDepCmd(
		"add-dep",
		"Add a task dependency (task depends on another task)",
		"Dependency added: %s depends on %s",
		actions.TaskAddDependencyIdempotent,
	)
}

func newTaskRemoveDepCmd() *cobra.Command {
	return newTaskDepCmd(
		"remove-dep",
		"Remove a task dependency",
		"Dependency removed: %s no longer depends on %s",
		actions.TaskRemoveDependencyIdempotent,
	)
}

func newTaskDeleteCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "delete",
		Short: "Delete a task",
		RunE: func(cmd *cobra.Command, args []string) error {
			taskID, _ := cmd.Flags().GetString("id")
			agentName, err := requireActorName(cmd, "")
			if err != nil {
				return cmdErr(err)
			}
			requestID, err := requireRequestID(cmd)
			if err != nil {
				return cmdErr(err)
			}

			if taskID == "" {
				return cmdErr(errors.New("--id is required"))
			}

			var eventID int64
			if err := withDB(func(db *DB) error {
				eid, deleteErr := actions.TaskDeleteIdempotent(db, agentName, requestID, taskID)
				if deleteErr != nil {
					return deleteErr
				}
				eventID = eid
				return nil
			}); err != nil {
				return err
			}

			type resp struct {
				TaskID  string `json:"task_id"`
				EventID int64  `json:"event_id"`
			}
			return output.PrintSuccess(resp{TaskID: taskID, EventID: eventID})
		},
	}

	cmd.Flags().String("id", "", "Task ID to delete (required)")

	cmd.Annotations = map[string]string{"mutates": "true", "request_id": "true"}
	return cmd
}

func newTaskCompleteCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "complete",
		Short: "Atomically complete a task with structured outcome",
		Long:  "Sets status + emits task_closed event with summary/label metadata in one transaction. Outcome: done|blocked.",
		RunE: func(cmd *cobra.Command, args []string) error {
			taskID, _ := cmd.Flags().GetString("id")
			outcome, _ := cmd.Flags().GetString("outcome")
			summary, _ := cmd.Flags().GetString("summary")
			label, _ := cmd.Flags().GetString("label")
			blockedReason, _ := cmd.Flags().GetString("blocked-reason")

			if taskID == "" {
				return cmdErr(errors.New("--id is required"))
			}
			if outcome == "" {
				return cmdErr(errors.New("--outcome is required (done|blocked)"))
			}
			if summary == "" {
				return cmdErr(errors.New("--summary is required"))
			}

			agentName, err := requireActorName(cmd, "")
			if err != nil {
				return cmdErr(err)
			}
			requestID, err := requireRequestID(cmd)
			if err != nil {
				return cmdErr(err)
			}

			var result *actions.TaskCloseResult
			if err := withDB(func(db *DB) error {
				r, closeErr := actions.TaskCloseIdempotent(db, agentName, requestID, taskID, outcome, summary, label, blockedReason)
				if closeErr != nil {
					return closeErr
				}
				result = r
				return nil
			}); err != nil {
				return err
			}

			return output.PrintSuccess(result)
		},
	}

	cmd.Flags().String("id", "", "Task ID (required)")
	cmd.Flags().String("outcome", "", "Close outcome: done|blocked (required)")
	cmd.Flags().String("summary", "", "Closure summary (required)")
	cmd.Flags().String("label", "", "Optional label stored in event metadata")
	cmd.Flags().String("blocked-reason", "", "Reason for blocking (only used with --outcome=blocked)")

	cmd.Annotations = map[string]string{"mutates": "true", "request_id": "true"}
	return cmd
}

func newTaskSetPriorityCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "set-priority",
		Short: "Update task priority",
		RunE: func(cmd *cobra.Command, args []string) error {
			taskID, _ := cmd.Flags().GetString("id")
			if !cmd.Flags().Changed("priority") {
				return cmdErr(errors.New("--priority is required"))
			}
			priority, _ := cmd.Flags().GetInt("priority")
			agentName, err := requireActorName(cmd, "")
			if err != nil {
				return cmdErr(err)
			}
			requestID, err := requireRequestID(cmd)
			if err != nil {
				return cmdErr(err)
			}

			if taskID == "" {
				return cmdErr(errors.New("--id is required"))
			}

			var (
				task    *models.Task
				eventID int64
			)
			if err := withDB(func(db *DB) error {
				t, eid, err := actions.TaskSetPriorityIdempotent(db, agentName, requestID, taskID, priority)
				if err != nil {
					return err
				}
				task = t
				eventID = eid
				return nil
			}); err != nil {
				return err
			}

			type resp struct {
				Task    *models.Task `json:"task"`
				EventID int64        `json:"event_id"`
			}
			return output.PrintSuccess(resp{Task: task, EventID: eventID})
		},
	}

	cmd.Flags().String("id", "", "Task ID (required)")
	cmd.Flags().Int("priority", 0, "New priority value (required)")

	cmd.Annotations = map[string]string{"mutates": "true", "request_id": "true"}
	return cmd
}
