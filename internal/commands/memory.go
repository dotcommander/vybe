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
// Admin subcommands (gc, delete, pin) live in memory_admin.go.
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
	cmd.AddCommand(newMemoryPinCmd())

	namespaceIndex(cmd)
	return cmd
}

func newMemorySetCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "set",
		Short: "Set a memory value",
		RunE: func(cmd *cobra.Command, args []string) error {
			agentName, requestID, err := requireMutationParams(cmd)
			if err != nil {
				return err
			}
			key, _ := cmd.Flags().GetString("key")
			value, _ := cmd.Flags().GetString("value")
			valueType, _ := cmd.Flags().GetString("type")
			scope, _ := cmd.Flags().GetString("scope")
			scopeID, _ := cmd.Flags().GetString("scope-id")
			expiresIn, _ := cmd.Flags().GetString("expires-in")
			pinned, _ := cmd.Flags().GetBool("pin")
			kind, _ := cmd.Flags().GetString("kind")
			if kind == "" {
				kind = "fact"
			}
			halfLifeRaw, _ := cmd.Flags().GetFloat64("half-life-days")
			var halfLifeDays *float64
			if halfLifeRaw >= 0 {
				halfLifeDays = &halfLifeRaw
			}
			sourceTaskID, _ := cmd.Flags().GetString("source-task-id")

			expiresAt, err := actions.ParseExpiresIn(expiresIn)
			if err != nil {
				return cmdErr(fmt.Errorf("invalid expires-in duration: %w", err))
			}

			var eventID int64
			if err := withDB(func(db *DB) error {
				eid, err := actions.MemorySetIdempotent(db, agentName, requestID, key, value, valueType, scope, scopeID, expiresAt, pinned, kind, halfLifeDays, sourceTaskID)
				if err != nil {
					return err
				}
				eventID = eid
				return nil
			}); err != nil {
				return err
			}

			type resp struct {
				EventID      int64      `json:"event_id"`
				Key          string     `json:"key"`
				Scope        string     `json:"scope"`
				ScopeID      string     `json:"scope_id,omitempty"`
				ExpiresAt    *time.Time `json:"expires_at,omitempty"`
				Pinned       bool       `json:"pinned"`
				Kind         string     `json:"kind"`
				HalfLifeDays *float64   `json:"half_life_days,omitempty"`
				SourceTaskID string     `json:"source_task_id,omitzero"`
			}
			return output.PrintSuccess(resp{
				EventID: eventID, Key: key, Scope: scope, ScopeID: scopeID,
				ExpiresAt: expiresAt, Pinned: pinned, Kind: kind, HalfLifeDays: halfLifeDays,
				SourceTaskID: sourceTaskID,
			})
		},
	}

	cmd.Flags().StringP("key", "k", "", "Memory key (required)")
	cmd.Flags().StringP("value", "v", "", "Memory value (required)")
	cmd.Flags().StringP("type", "t", "", "Value type (string, number, boolean, json, array) - auto-detected if not specified")
	cmd.Flags().StringP("scope", "s", "global", "Scope (global, project, task, agent)")
	cmd.Flags().String("scope-id", "", "Scope ID (required for non-global scopes)")
	cmd.Flags().String("expires-in", "", "Expiration duration (e.g., 24h, 7d, 2w)")
	cmd.Flags().Bool("pin", false, "Mark this memory as pinned (bypasses TTL and always appears in brief)")
	cmd.Flags().String("kind", "fact", "Memory kind: fact (key=value claim), directive (imperative behavioral rule), or lesson (short-lived insight)")
	cmd.Flags().Float64("half-life-days", -1, "Override decay half-life in days (-1 = use kind default)")
	cmd.Flags().String("source-task-id", "", "Optional task ID that this memory was derived from (provenance)")

	_ = cmd.MarkFlagRequired("key")
	_ = cmd.MarkFlagRequired("value")

	cmd.Annotations = map[string]string{"mutates": "true", "request_id": "true"}
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
