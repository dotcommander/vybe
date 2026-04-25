package commands

import (
	"github.com/spf13/cobra"

	"github.com/dotcommander/vybe/internal/actions"
	"github.com/dotcommander/vybe/internal/output"
)

func newMemoryGCCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "gc",
		Short: "Delete expired memory rows",
		RunE: func(cmd *cobra.Command, args []string) error {
			agentName, requestID, err := requireMutationParams(cmd)
			if err != nil {
				return err
			}
			limit, _ := cmd.Flags().GetInt("limit")

			var result *actions.MemoryGCResult
			if err := withDB(func(db *DB) error {
				r, err := actions.MemoryGCIdempotent(db, agentName, requestID, limit)
				if err != nil {
					return err
				}
				result = r
				return nil
			}); err != nil {
				return err
			}

			type resp struct {
				EventID int64 `json:"event_id"`
				Deleted int   `json:"deleted"`
				Limit   int   `json:"limit"`
			}
			return output.PrintSuccess(resp{EventID: result.EventID, Deleted: result.Deleted, Limit: limit})
		},
	}

	cmd.Flags().Int("limit", 500, "Maximum rows to delete in one run")
	cmd.Annotations = map[string]string{"mutates": "true", "request_id": "true"}
	return cmd
}

func newMemoryDeleteCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "delete",
		Short: "Delete a memory entry",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			agentName, requestID, err := requireMutationParams(cmd)
			if err != nil {
				return err
			}
			key, _ := cmd.Flags().GetString("key")
			scope, _ := cmd.Flags().GetString("scope")
			scopeID, _ := cmd.Flags().GetString("scope-id")

			var eventID int64
			if err := withDB(func(db *DB) error {
				eid, err := actions.MemoryDeleteIdempotent(ctx, db, agentName, requestID, key, scope, scopeID)
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
			}
			return output.PrintSuccess(resp{EventID: eventID, Key: key, Scope: scope, ScopeID: scopeID})
		},
	}

	cmd.Flags().StringP("key", "k", "", "Memory key (required)")
	cmd.Flags().StringP("scope", "s", "global", "Scope (global, project, task, agent)")
	cmd.Flags().String("scope-id", "", "Scope ID (required for non-global scopes)")

	_ = cmd.MarkFlagRequired("key")

	cmd.Annotations = map[string]string{"mutates": "true", "request_id": "true"}
	return cmd
}

func newMemoryPinCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "pin",
		Short: "Pin or unpin a memory entry",
		Long:  "Pinned memories always appear in the agent brief and ignore TTL expiry",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			agentName, requestID, err := requireMutationParams(cmd)
			if err != nil {
				return err
			}
			key, _ := cmd.Flags().GetString("key")
			scope, _ := cmd.Flags().GetString("scope")
			scopeID, _ := cmd.Flags().GetString("scope-id")
			unpin, _ := cmd.Flags().GetBool("unpin")

			var eventID int64
			if err := withDB(func(db *DB) error {
				eid, err := actions.MemoryPinIdempotent(ctx, db, agentName, requestID, key, scope, scopeID, !unpin)
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
				Pinned  bool   `json:"pinned"`
			}
			return output.PrintSuccess(resp{EventID: eventID, Key: key, Scope: scope, ScopeID: scopeID, Pinned: !unpin})
		},
	}

	cmd.Flags().StringP("key", "k", "", "Memory key (required)")
	cmd.Flags().StringP("scope", "s", "global", "Scope (global, project, task, agent)")
	cmd.Flags().String("scope-id", "", "Scope ID (required for non-global scopes)")
	cmd.Flags().Bool("unpin", false, "Remove pin (restore normal ACT-R decay)")

	_ = cmd.MarkFlagRequired("key")
	cmd.Annotations = map[string]string{"mutates": "true", "request_id": "true"}
	return cmd
}
