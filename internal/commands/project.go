package commands

import (
	"errors"

	"github.com/dotcommander/vybe/internal/actions"
	"github.com/dotcommander/vybe/internal/models"
	"github.com/dotcommander/vybe/internal/output"
	"github.com/spf13/cobra"
)

// NewProjectCmd creates the project command group.
func NewProjectCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "project",
		Short: "Manage projects",
		Long:  "Create and query projects",
	}

	cmd.AddCommand(newProjectCreateCmd())
	cmd.AddCommand(newProjectGetCmd())
	cmd.AddCommand(newProjectListCmd())
	cmd.AddCommand(newProjectDeleteCmd())

	return cmd
}

func newProjectCreateCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "create",
		Short: "Create a new project",
		RunE: func(cmd *cobra.Command, args []string) error {
			name, _ := cmd.Flags().GetString("name")
			metadata, _ := cmd.Flags().GetString("metadata")
			agentName, err := requireActorName(cmd, "")
			if err != nil {
				return cmdErr(err)
			}
			requestID, err := requireRequestID(cmd)
			if err != nil {
				return cmdErr(err)
			}

			if name == "" {
				return cmdErr(errors.New("--name is required"))
			}

			var project *models.Project
			var eventID int64
			if err := withDB(func(db *DB) error {
				p, eid, err := actions.ProjectCreateIdempotent(db, agentName, requestID, name, metadata)
				if err != nil {
					return err
				}
				project = p
				eventID = eid
				return nil
			}); err != nil {
				return err
			}

			type resp struct {
				Project *models.Project `json:"project"`
				EventID int64           `json:"event_id"`
			}
			return output.PrintSuccess(resp{Project: project, EventID: eventID})
		},
	}

	cmd.Flags().String("name", "", "Project name (required)")
	cmd.Flags().String("metadata", "", "Project metadata (JSON)")

	return cmd
}

func newProjectGetCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "get",
		Short: "Get project details",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			projectID, _ := cmd.Flags().GetString("id")
			if projectID != "" && len(args) == 1 {
				return cmdErr(errors.New("provide either --id or a positional project id, not both"))
			}
			if projectID == "" && len(args) == 1 {
				projectID = args[0]
			}
			if projectID == "" {
				return cmdErr(errors.New("--id is required"))
			}

			var project *models.Project
			if err := withDB(func(db *DB) error {
				p, err := actions.ProjectGet(db, projectID)
				if err != nil {
					return err
				}
				project = p
				return nil
			}); err != nil {
				return err
			}

			return output.PrintSuccess(project)
		},
	}

	cmd.Flags().String("id", "", "Project ID (required)")

	return cmd
}

func newProjectListCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List all projects",
		RunE: func(cmd *cobra.Command, args []string) error {
			var projects []*models.Project
			if err := withDB(func(db *DB) error {
				p, err := actions.ProjectList(db)
				if err != nil {
					return err
				}
				projects = p
				return nil
			}); err != nil {
				return err
			}

			type resp struct {
				Count    int               `json:"count"`
				Projects []*models.Project `json:"projects"`
			}
			return output.PrintSuccess(resp{Count: len(projects), Projects: projects})
		},
	}

	return cmd
}

//nolint:dupl // newTaskDeleteCmd has the same cobra pattern; they operate on different entities and actions
func newProjectDeleteCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "delete",
		Short: "Delete a project",
		RunE: func(cmd *cobra.Command, args []string) error {
			projectID, _ := cmd.Flags().GetString("id")
			agentName, err := requireActorName(cmd, "")
			if err != nil {
				return cmdErr(err)
			}
			requestID, err := requireRequestID(cmd)
			if err != nil {
				return cmdErr(err)
			}

			if projectID == "" {
				return cmdErr(errors.New("--id is required"))
			}

			var eventID int64
			if err := withDB(func(db *DB) error {
				eid, deleteErr := actions.ProjectDeleteIdempotent(db, agentName, requestID, projectID)
				if deleteErr != nil {
					return deleteErr
				}
				eventID = eid
				return nil
			}); err != nil {
				return err
			}

			type resp struct {
				ProjectID string `json:"project_id"`
				EventID   int64  `json:"event_id"`
			}
			return output.PrintSuccess(resp{ProjectID: projectID, EventID: eventID})
		},
	}

	cmd.Flags().String("id", "", "Project ID to delete (required)")

	return cmd
}
