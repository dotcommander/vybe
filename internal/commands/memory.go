package commands

import (
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"github.com/dotcommander/vybe/internal/actions"
	"github.com/dotcommander/vybe/internal/models"
	"github.com/dotcommander/vybe/internal/output"
)

// NewMemoryCmd creates the memory command with subcommands.
func NewMemoryCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "memory",
		Short: "Manage memory key-value storage with scoping",
		Long:  "Store and retrieve key-value pairs with scope isolation (global, project, task, agent)",
		Args:  cobra.NoArgs,
	}

	cmd.AddCommand(newMemorySetCmd())
	cmd.AddCommand(newMemoryGCCmd())
	cmd.AddCommand(newMemoryGetCmd())
	cmd.AddCommand(newMemoryListCmd())
	cmd.AddCommand(newMemoryDeleteCmd())

	return cmd
}

func newMemoryGCCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "gc",
		Short: "Delete expired memory rows",
		RunE: func(cmd *cobra.Command, args []string) error {
			agentName, err := requireActorName(cmd, "")
			if err != nil {
				return cmdErr(err)
			}
			requestID, err := requireRequestID(cmd)
			if err != nil {
				return cmdErr(err)
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
	return cmd
}

func newMemorySetCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "set",
		Short: "Set a memory value",
		RunE: func(cmd *cobra.Command, args []string) error {
			agentName, err := requireActorName(cmd, "")
			if err != nil {
				return cmdErr(err)
			}
			requestID, err := requireRequestID(cmd)
			if err != nil {
				return cmdErr(err)
			}
			key, _ := cmd.Flags().GetString("key")
			value, _ := cmd.Flags().GetString("value")
			valueType, _ := cmd.Flags().GetString("type")
			scope, _ := cmd.Flags().GetString("scope")
			scopeID, _ := cmd.Flags().GetString("scope-id")
			expiresIn, _ := cmd.Flags().GetString("expires-in")

			expiresAt, err := actions.ParseExpiresIn(expiresIn)
			if err != nil {
				return cmdErr(fmt.Errorf("invalid expires-in duration: %w", err))
			}

			var eventID int64
			if err := withDB(func(db *DB) error {
				eid, err := actions.MemorySetIdempotent(db, agentName, requestID, key, value, valueType, scope, scopeID, expiresAt)
				if err != nil {
					return err
				}
				eventID = eid
				return nil
			}); err != nil {
				return err
			}

			type resp struct {
				EventID   int64      `json:"event_id"`
				Key       string     `json:"key"`
				Scope     string     `json:"scope"`
				ScopeID   string     `json:"scope_id,omitempty"`
				ExpiresAt *time.Time `json:"expires_at,omitempty"`
			}
			return output.PrintSuccess(resp{EventID: eventID, Key: key, Scope: scope, ScopeID: scopeID, ExpiresAt: expiresAt})
		},
	}

	cmd.Flags().StringP("key", "k", "", "Memory key (required)")
	cmd.Flags().StringP("value", "v", "", "Memory value (required)")
	cmd.Flags().StringP("type", "t", "", "Value type (string, number, boolean, json, array) - auto-detected if not specified")
	cmd.Flags().StringP("scope", "s", "global", "Scope (global, project, task, agent)")
	cmd.Flags().String("scope-id", "", "Scope ID (required for non-global scopes)")
	cmd.Flags().String("expires-in", "", "Expiration duration (e.g., 24h, 7d, 2w)")

	_ = cmd.MarkFlagRequired("key")
	_ = cmd.MarkFlagRequired("value")

	return cmd
}

func newMemoryGetCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "get",
		Short: "Get a memory value",
		RunE: func(cmd *cobra.Command, args []string) error {
			key, _ := cmd.Flags().GetString("key")
			scope, _ := cmd.Flags().GetString("scope")
			scopeID, _ := cmd.Flags().GetString("scope-id")

			var mem *models.Memory
			if err := withDB(func(db *DB) error {
				m, err := actions.MemoryGet(db, key, scope, scopeID)
				if err != nil {
					return err
				}
				mem = m
				return nil
			}); err != nil {
				return err
			}

			return output.PrintSuccess(mem)
		},
	}

	cmd.Flags().StringP("key", "k", "", "Memory key (required)")
	cmd.Flags().StringP("scope", "s", "global", "Scope (global, project, task, agent)")
	cmd.Flags().String("scope-id", "", "Scope ID (required for non-global scopes)")

	_ = cmd.MarkFlagRequired("key")

	return cmd
}

func newMemoryListCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List all memory entries for a scope",
		RunE: func(cmd *cobra.Command, args []string) error {
			scope, _ := cmd.Flags().GetString("scope")
			scopeID, _ := cmd.Flags().GetString("scope-id")

			var memories []*models.Memory
			if err := withDB(func(db *DB) error {
				m, err := actions.MemoryList(db, scope, scopeID)
				if err != nil {
					return err
				}
				memories = m
				return nil
			}); err != nil {
				return err
			}

			type resp struct {
				Scope    string           `json:"scope"`
				ScopeID  string           `json:"scope_id,omitempty"`
				Count    int              `json:"count"`
				Memories []*models.Memory `json:"memories"`
			}
			return output.PrintSuccess(resp{Scope: scope, ScopeID: scopeID, Count: len(memories), Memories: memories})
		},
	}

	cmd.Flags().StringP("scope", "s", "global", "Scope (global, project, task, agent)")
	cmd.Flags().String("scope-id", "", "Scope ID (required for non-global scopes)")

	return cmd
}

func newMemoryDeleteCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "delete",
		Short: "Delete a memory entry",
		RunE: func(cmd *cobra.Command, args []string) error {
			agentName, err := requireActorName(cmd, "")
			if err != nil {
				return cmdErr(err)
			}
			requestID, err := requireRequestID(cmd)
			if err != nil {
				return cmdErr(err)
			}
			key, _ := cmd.Flags().GetString("key")
			scope, _ := cmd.Flags().GetString("scope")
			scopeID, _ := cmd.Flags().GetString("scope-id")

			var eventID int64
			if err := withDB(func(db *DB) error {
				eid, err := actions.MemoryDeleteIdempotent(db, agentName, requestID, key, scope, scopeID)
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

	return cmd
}

