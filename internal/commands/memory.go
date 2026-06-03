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

	cmd.Annotations = map[string]string{"mutates": "true"}
	return cmd
}

func newMemoryGetCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "get",
		Short: "Get a memory value",
		RunE: func(cmd *cobra.Command, args []string) error {
			agentName := resolveActorName(cmd, "")
			key, _ := cmd.Flags().GetString("key")
			scope, _ := cmd.Flags().GetString("scope")
			scopeID, _ := cmd.Flags().GetString("scope-id")

			var mem *models.Memory
			if err := withDB(func(db *DB) error {
				m, err := actions.MemoryGet(db, agentName, key, scope, scopeID)
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
		Short: "List all memory entries for a scope, or by provenance source",
		RunE: func(cmd *cobra.Command, args []string) error {
			agentName := resolveActorName(cmd, "")
			scope, _ := cmd.Flags().GetString("scope")
			scopeID, _ := cmd.Flags().GetString("scope-id")
			bySourceTask, _ := cmd.Flags().GetString("by-source-task")
			bySourceEvent, _ := cmd.Flags().GetInt64("by-source-event")

			var sourceEventID *int64
			if cmd.Flags().Changed("by-source-event") {
				sourceEventID = &bySourceEvent
			}
			useSource := sourceEventID != nil || bySourceTask != ""

			var memories []*models.Memory
			if err := withDB(func(db *DB) error {
				var err error
				if useSource {
					memories, err = actions.MemoryListBySource(db, sourceEventID, bySourceTask)
				} else {
					memories, err = actions.MemoryList(db, agentName, scope, scopeID)
				}
				return err
			}); err != nil {
				return err
			}

			if useSource {
				type sourceResp struct {
					BySourceEvent *int64           `json:"by_source_event,omitempty"`
					BySourceTask  string           `json:"by_source_task,omitempty"`
					Count         int              `json:"count"`
					Memories      []*models.Memory `json:"memories"`
				}
				return output.PrintSuccess(sourceResp{BySourceEvent: sourceEventID, BySourceTask: bySourceTask, Count: len(memories), Memories: memories})
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
	cmd.Flags().String("by-source-task", "", "Filter by provenance source_task_id (overrides scope listing)")
	cmd.Flags().Int64("by-source-event", 0, "Filter by provenance source_event_id (overrides scope listing)")

	return cmd
}
