package commands

import (
	"errors"

	"github.com/spf13/cobra"

	"github.com/dotcommander/vybe/internal/actions"
	"github.com/dotcommander/vybe/internal/models"
	"github.com/dotcommander/vybe/internal/output"
)

// NewArtifactCmd creates the artifact management command group.
func NewArtifactCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "artifact",
		Short: "Manage artifacts linked to tasks/events",
	}

	cmd.AddCommand(newArtifactAddCmd())
	cmd.AddCommand(newArtifactGetCmd())
	cmd.AddCommand(newArtifactListCmd())
	return cmd
}

func newArtifactAddCmd() *cobra.Command {
	var (
		taskID      string
		filePath    string
		contentType string
	)

	cmd := &cobra.Command{
		Use:   "add",
		Short: "Attach an artifact to a task (also logs an event)",
		RunE: func(cmd *cobra.Command, args []string) error {
			agentName, err := requireActorName(cmd, "")
			if err != nil {
				return cmdErr(err)
			}
			requestID, err := requireRequestID(cmd)
			if err != nil {
				return cmdErr(err)
			}
			if taskID == "" {
				return cmdErr(errors.New("--task is required"))
			}
			if filePath == "" {
				return cmdErr(errors.New("--path is required"))
			}

			var (
				artifact *models.Artifact
				eventID  int64
			)
			if err := withDB(func(db *DB) error {
				a, eid, err := actions.ArtifactAddIdempotent(db, agentName, requestID, taskID, filePath, contentType)
				if err != nil {
					return err
				}
				artifact = a
				eventID = eid
				return nil
			}); err != nil {
				return err
			}

			type resp struct {
				Artifact *models.Artifact `json:"artifact"`
				EventID  int64            `json:"event_id"`
			}
			return output.PrintSuccess(resp{Artifact: artifact, EventID: eventID})
		},
	}

	cmd.Flags().StringVar(&taskID, "task", "", "Task ID (required)")
	cmd.Flags().StringVar(&filePath, "path", "", "File path (required)")
	cmd.Flags().StringVar(&contentType, "type", "", "Content type (optional)")
	return cmd
}

func newArtifactGetCmd() *cobra.Command {
	var id string

	cmd := &cobra.Command{
		Use:   "get",
		Short: "Get artifact by id",
		RunE: func(cmd *cobra.Command, args []string) error {
			if id == "" {
				return cmdErr(errors.New("--id is required"))
			}

			var artifact *models.Artifact
			if err := withDB(func(db *DB) error {
				a, err := actions.ArtifactGet(db, id)
				if err != nil {
					return err
				}
				artifact = a
				return nil
			}); err != nil {
				return err
			}

			return output.PrintSuccess(artifact)
		},
	}

	cmd.Flags().StringVar(&id, "id", "", "Artifact ID (required)")
	return cmd
}

func newArtifactListCmd() *cobra.Command {
	var (
		taskID string
		limit  int
	)

	cmd := &cobra.Command{
		Use:   "list",
		Short: "List artifacts for a task",
		RunE: func(cmd *cobra.Command, args []string) error {
			if taskID == "" {
				return cmdErr(errors.New("--task is required"))
			}

			var artifacts []*models.Artifact
			if err := withDB(func(db *DB) error {
				a, err := actions.ArtifactListByTask(db, taskID, limit)
				if err != nil {
					return err
				}
				artifacts = a
				return nil
			}); err != nil {
				return err
			}

			type resp struct {
				TaskID    string             `json:"task_id"`
				Count     int                `json:"count"`
				Artifacts []*models.Artifact `json:"artifacts"`
			}
			return output.PrintSuccess(resp{TaskID: taskID, Count: len(artifacts), Artifacts: artifacts})
		},
	}

	cmd.Flags().StringVar(&taskID, "task", "", "Task ID (required)")
	cmd.Flags().IntVar(&limit, "limit", 50, "Max artifacts (<= 1000)")
	return cmd
}
