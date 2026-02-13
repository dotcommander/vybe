package commands

import (
	"fmt"

	"github.com/dotcommander/vybe/internal/output"
	"github.com/dotcommander/vybe/internal/store"
	"github.com/spf13/cobra"
)

// NewLogCmd creates the log command
func NewLogCmd() *cobra.Command {
	var (
		kind     string
		message  string
		taskID   string
		metadata string
	)

	cmd := &cobra.Command{
		Use:   "log",
		Short: "Log an event",
		Long:  "Create a log entry in the continuity system",
		RunE: func(cmd *cobra.Command, args []string) error {
			agentName, err := requireActorName(cmd, "")
			if err != nil {
				return cmdErr(err)
			}
			requestID, err := requireRequestID(cmd)
			if err != nil {
				return cmdErr(err)
			}
			if kind == "" {
				return fmt.Errorf("--kind is required")
			}
			if message == "" {
				return fmt.Errorf("--msg is required")
			}

			var eventID int64
			var logErr error
			if err := withDB(func(db *DB) error {
				if metadata == "" {
					eventID, logErr = store.AppendEventIdempotent(db, agentName, requestID, kind, taskID, message)
				} else {
					eventID, logErr = store.AppendEventWithMetadataIdempotent(db, agentName, requestID, kind, taskID, message, metadata)
				}
				return logErr
			}); err != nil {
				return err
			}

			type resp struct {
				EventID int64  `json:"event_id"`
				Kind    string `json:"kind"`
				Message string `json:"message"`
			}
			return output.PrintSuccess(resp{EventID: eventID, Kind: kind, Message: message})
		},
	}

	cmd.Flags().StringVar(&kind, "kind", "", "Event kind (required)")
	cmd.Flags().StringVar(&message, "msg", "", "Event message (required)")
	cmd.Flags().StringVar(&taskID, "task", "", "Task ID")
	cmd.Flags().StringVar(&metadata, "metadata", "", "Metadata JSON string")

	return cmd
}
