package commands

import (
	"github.com/dotcommander/vybe/internal/actions"
	"github.com/dotcommander/vybe/internal/output"
	"github.com/dotcommander/vybe/internal/store"
	"github.com/spf13/cobra"
)

// NewResumeCmd creates the resume command
func NewResumeCmd() *cobra.Command {
	var (
		limit      int
		projectDir string
		peek       bool
		focus      string
	)

	cmd := &cobra.Command{
		Use:   "resume",
		Short: "Resume agent work with deltas and focus task",
		Long: `Resume retrieves events since last cursor, determines focus task using deterministic rules,
and builds a brief packet with all context needed to resume work.

The cursor is advanced monotonically and the focus task is updated atomically.
Use --project-dir to scope resume to a specific project directory.
Use --peek to read the current brief without advancing the cursor (no request-id required).
Use --focus <task-id> to set the agent's focus task before resuming (request-id required).`,
		RunE: func(cmd *cobra.Command, args []string) error {
			agentName, err := requireActorName(cmd, "")
			if err != nil {
				return cmdErr(err)
			}

			if peek {
				type briefResponse struct {
					AgentName string             `json:"agent_name"`
					Brief     *store.BriefPacket `json:"brief"`
				}
				var resp briefResponse
				if err := withDB(func(db *DB) error {
					b, err := actions.Brief(db, agentName)
					if err != nil {
						return err
					}
					resp = briefResponse{AgentName: agentName, Brief: b}
					return nil
				}); err != nil {
					return err
				}
				return output.PrintSuccess(resp)
			}

			requestID, err := requireRequestID(cmd)
			if err != nil {
				return cmdErr(err)
			}

			var response *actions.ResumeResponse
			if err := withDB(func(db *DB) error {
				if focus != "" {
					if _, err := store.LoadOrCreateAgentState(db, agentName); err != nil {
						return err
					}
					if _, err := store.SetAgentFocusTaskWithEventIdempotent(db, agentName, requestID+"_focus", focus); err != nil {
						return err
					}
				}

				r, err := actions.ResumeWithOptionsIdempotent(db, agentName, requestID, actions.ResumeOptions{
					EventLimit: limit,
					ProjectDir: projectDir,
				})
				if err != nil {
					return err
				}
				response = r
				return nil
			}); err != nil {
				return err
			}

			return output.PrintSuccess(response)
		},
	}

	cmd.Flags().IntVar(&limit, "limit", 1000, "Max delta events to return (<= 1000)")
	cmd.Flags().StringVar(&projectDir, "project-dir", "", "Scope resume to a project directory path")
	cmd.Flags().BoolVar(&peek, "peek", false, "Read current brief without advancing cursor (no request-id required)")
	cmd.Flags().StringVar(&focus, "focus", "", "Set agent focus task before resuming (request-id required)")

	cmd.Annotations = map[string]string{"mutates": "true", "request_id": "conditional"}
	return cmd
}
