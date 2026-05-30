package commands

import (
	"errors"
	"strings"

	"github.com/spf13/cobra"

	"github.com/dotcommander/vybe/internal/actions"
	"github.com/dotcommander/vybe/internal/models"
	"github.com/dotcommander/vybe/internal/output"
)

// newDoneCmd is sugar for `push {task_status: completed, [event: progress]}`.
func newDoneCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "done <task-id>",
		Short: "Mark a task completed (sugar for push task_status=completed)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			agentName, requestID, err := requireMutationParams(cmd)
			if err != nil {
				return err
			}
			taskID := args[0]
			note, _ := cmd.Flags().GetString("note")

			input := actions.PushInput{
				TaskID:     taskID,
				TaskStatus: &actions.PushTaskStatusInput{Status: "completed"},
			}
			if note != "" {
				input.Event = &actions.PushEventInput{Kind: models.EventKindProgress, Message: note}
			}

			var result *actions.PushResult
			if err := withDB(func(db *DB) error {
				r, err := actions.PushIdempotent(db, agentName, requestID, input)
				if err != nil {
					return err
				}
				result = r
				return nil
			}); err != nil {
				return err
			}

			return output.PrintSuccess(result)
		},
	}

	cmd.Flags().String("note", "", "Optional progress note recorded as a progress event")

	cmd.Annotations = map[string]string{"mutates": "true"}
	return cmd
}

// newBlockCmd is sugar for `task set-status blocked`.
func newBlockCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "block <task-id>",
		Short: "Mark a task blocked (sugar for task set-status blocked)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			taskID := args[0]
			reason, _ := cmd.Flags().GetString("reason")
			failure, _ := cmd.Flags().GetBool("failure")

			if reason == "" {
				return cmdErr(errors.New("--reason is required"))
			}

			blockedReason := reason
			if failure {
				blockedReason = string(models.NewFailureBlockedReason(reason))
			}

			return runTaskCmd(cmd, func(db *DB, agentName, requestID string) (taskCmdResult, error) {
				t, eid, err := actions.TaskSetStatusIdempotent(db, agentName, requestID, taskID, "blocked", blockedReason)
				return taskCmdResult{Task: t, EventID: eid}, err
			})
		},
	}

	cmd.Flags().String("reason", "", "Reason for blocking (required)")
	cmd.Flags().Bool("failure", false, "Mark as failure-blocked (failure:<reason>); resume skips to find new work")

	cmd.Annotations = map[string]string{"mutates": "true"}
	return cmd
}

// newNoteCmd is sugar for `push {event: progress}`.
func newNoteCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "note <task-id> <message>",
		Short: "Record a progress event on a task (sugar for push event=progress)",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			agentName, requestID, err := requireMutationParams(cmd)
			if err != nil {
				return err
			}
			taskID := args[0]
			message := args[1]

			input := actions.PushInput{
				TaskID: taskID,
				Event:  &actions.PushEventInput{Kind: models.EventKindProgress, Message: message},
			}

			var result *actions.PushResult
			if err := withDB(func(db *DB) error {
				r, err := actions.PushIdempotent(db, agentName, requestID, input)
				if err != nil {
					return err
				}
				result = r
				return nil
			}); err != nil {
				return err
			}

			return output.PrintSuccess(result)
		},
	}

	cmd.Annotations = map[string]string{"mutates": "true"}
	return cmd
}

// newRememberCmd is sugar for `memory set`.
func newRememberCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "remember <key=value>",
		Short: "Store a memory entry (sugar for memory set)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			agentName, requestID, err := requireMutationParams(cmd)
			if err != nil {
				return err
			}

			key, value, ok := strings.Cut(args[0], "=")
			if !ok || key == "" {
				return cmdErr(errors.New(`argument must be "key=value"`))
			}

			scope, _ := cmd.Flags().GetString("scope")
			scopeID, _ := cmd.Flags().GetString("scope-id")
			kind, _ := cmd.Flags().GetString("kind")
			if kind == "" {
				kind = "fact"
			}
			pinned, _ := cmd.Flags().GetBool("pin")

			var eventID int64
			if err := withDB(func(db *DB) error {
				eid, err := actions.MemorySetIdempotent(db, agentName, requestID, key, value, "", scope, scopeID, nil, pinned, kind, nil, "")
				if err != nil {
					return err
				}
				eventID = eid
				return nil
			}); err != nil {
				return err
			}

			type resp struct {
				EventID int64  `json:"event_id"`
				Key     string `json:"key"`
				Scope   string `json:"scope"`
				ScopeID string `json:"scope_id,omitempty"`
				Kind    string `json:"kind"`
				Pinned  bool   `json:"pinned"`
			}
			return output.PrintSuccess(resp{
				EventID: eventID, Key: key, Scope: scope, ScopeID: scopeID, Kind: kind, Pinned: pinned,
			})
		},
	}

	cmd.Flags().StringP("scope", "s", "global", "Scope (global, project, task, agent)")
	cmd.Flags().String("scope-id", "", "Scope ID (required for non-global scopes)")
	cmd.Flags().String("kind", "fact", "Memory kind: fact, directive, or lesson")
	cmd.Flags().Bool("pin", false, "Mark this memory as pinned")

	cmd.Annotations = map[string]string{"mutates": "true"}
	return cmd
}
