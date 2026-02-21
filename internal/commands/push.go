package commands

import (
	"encoding/json"
	"fmt"
	"io"
	"os"

	"github.com/dotcommander/vybe/internal/actions"
	"github.com/dotcommander/vybe/internal/output"
	"github.com/spf13/cobra"
)

// NewPushCmd creates the push command — atomic batch mutation.
func NewPushCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "push",
		Short: "Atomic batch mutation (event + memories + artifacts + task status)",
		Long:  "Combine multiple mutations into a single idempotent transaction. Input via --json flag or stdin pipe.",
		RunE: func(cmd *cobra.Command, args []string) error {
			agentName, err := requireActorName(cmd, "")
			if err != nil {
				return cmdErr(err)
			}
			requestID, err := requireRequestID(cmd)
			if err != nil {
				return cmdErr(err)
			}

			// Read JSON input from --json flag or stdin
			jsonStr, _ := cmd.Flags().GetString("json")
			var inputBytes []byte
			if jsonStr != "" {
				inputBytes = []byte(jsonStr)
			} else {
				// Check if stdin has data (piped)
				stat, statErr := os.Stdin.Stat()
				if statErr == nil && (stat.Mode()&os.ModeCharDevice) == 0 {
					inputBytes, err = io.ReadAll(io.LimitReader(os.Stdin, 1<<20))
					if err != nil {
						return cmdErr(fmt.Errorf("failed to read stdin: %w", err))
					}
				}
			}

			if len(inputBytes) == 0 {
				return cmdErr(fmt.Errorf("JSON input required via --json flag or stdin pipe"))
			}

			var input actions.PushInput
			if err := json.Unmarshal(inputBytes, &input); err != nil {
				return cmdErr(fmt.Errorf("invalid JSON input: %w", err))
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

	cmd.Flags().String("json", "", "JSON input payload")

	cmd.Annotations = map[string]string{"mutates": "true", "request_id": "true"}
	return cmd
}
