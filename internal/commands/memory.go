package commands

import (
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"github.com/dotcommander/vibe/internal/actions"
	"github.com/dotcommander/vibe/internal/models"
	"github.com/dotcommander/vibe/internal/output"
)

// NewMemoryCmd creates the memory command with subcommands.
func NewMemoryCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "memory",
		Short: "Manage memory key-value storage with scoping",
		Long:  "Store and retrieve key-value pairs with scope isolation (global, project, task, agent)",
	}

	cmd.AddCommand(newMemorySetCmd())
	cmd.AddCommand(newMemoryCompactCmd())
	cmd.AddCommand(newMemoryGCCmd())
	cmd.AddCommand(newMemoryGetCmd())
	cmd.AddCommand(newMemoryListCmd())
	cmd.AddCommand(newMemoryDeleteCmd())
	cmd.AddCommand(newMemoryTouchCmd())
	cmd.AddCommand(newMemoryQueryCmd())

	return cmd
}

func newMemoryCompactCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "compact",
		Short: "Compact stale low-priority memory into a summary",
		RunE: func(cmd *cobra.Command, args []string) error {
			agentName, err := requireActorName(cmd, "")
			if err != nil {
				return cmdErr(err)
			}
			requestID, err := requireRequestID(cmd)
			if err != nil {
				return cmdErr(err)
			}

			scope, _ := cmd.Flags().GetString("scope")
			scopeID, _ := cmd.Flags().GetString("scope-id")
			maxAgeRaw, _ := cmd.Flags().GetString("max-age")
			keepTop, _ := cmd.Flags().GetInt("keep-top")

			maxAge, err := actions.ParseMaxAge(maxAgeRaw)
			if err != nil {
				return cmdErr(fmt.Errorf("invalid max-age: %w", err))
			}

			var result *actions.MemoryCompactResult
			if err := withDB(func(db *DB) error {
				r, err := actions.MemoryCompactIdempotent(db, agentName, requestID, scope, scopeID, maxAge, keepTop)
				if err != nil {
					return err
				}
				result = r
				return nil
			}); err != nil {
				return err
			}

			type resp struct {
				EventID       int64  `json:"event_id"`
				Scope         string `json:"scope"`
				ScopeID       string `json:"scope_id,omitempty"`
				Compacted     int    `json:"compacted"`
				KeepTop       int    `json:"keep_top"`
				MaxAge        string `json:"max_age,omitempty"`
				SummaryKey    string `json:"summary_key"`
				SummaryMemory string `json:"summary_memory_id,omitempty"`
			}

			return output.PrintSuccess(resp{
				EventID:       result.EventID,
				Scope:         scope,
				ScopeID:       scopeID,
				Compacted:     result.Compacted,
				KeepTop:       keepTop,
				MaxAge:        maxAgeRaw,
				SummaryKey:    result.SummaryKey,
				SummaryMemory: result.SummaryMemory,
			})
		},
	}

	cmd.Flags().StringP("scope", "s", "global", "Scope (global, project, task, agent)")
	cmd.Flags().String("scope-id", "", "Scope ID (required for non-global scopes)")
	cmd.Flags().String("max-age", "14d", "Only compact memories not seen within this duration (e.g., 7d, 14d, 168h)")
	cmd.Flags().Int("keep-top", 10, "Keep top N memories by confidence/recency before compacting")

	return cmd
}

func newMemoryGCCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "gc",
		Short: "Delete expired and superseded memory rows",
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
			includeSuperseded, _ := cmd.Flags().GetBool("include-superseded")

			var mem *models.Memory
			if err := withDB(func(db *DB) error {
				m, err := actions.MemoryGet(db, key, scope, scopeID, includeSuperseded)
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
	cmd.Flags().Bool("include-superseded", false, "Include superseded (compacted) entries")

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
			includeSuperseded, _ := cmd.Flags().GetBool("include-superseded")

			var memories []*models.Memory
			if err := withDB(func(db *DB) error {
				m, err := actions.MemoryList(db, scope, scopeID, includeSuperseded)
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
	cmd.Flags().Bool("include-superseded", false, "Include superseded (compacted) entries")

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

func newMemoryTouchCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "touch",
		Short: "Update last_seen_at and bump confidence for a memory entry (idempotent, evented)",
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
			bump, _ := cmd.Flags().GetFloat64("bump")

			var result *actions.MemoryTouchResult
			if err := withDB(func(db *DB) error {
				r, err := actions.MemoryTouchIdempotent(db, agentName, requestID, key, scope, scopeID, bump)
				if err != nil {
					return err
				}
				result = r
				return nil
			}); err != nil {
				return err
			}

			type resp struct {
				EventID       int64   `json:"event_id"`
				Key           string  `json:"key"`
				Scope         string  `json:"scope"`
				ScopeID       string  `json:"scope_id,omitempty"`
				NewConfidence float64 `json:"new_confidence"`
			}
			return output.PrintSuccess(resp{EventID: result.EventID, Key: key, Scope: scope, ScopeID: scopeID, NewConfidence: result.Confidence})
		},
	}

	cmd.Flags().StringP("key", "k", "", "Memory key (required)")
	cmd.Flags().StringP("scope", "s", "global", "Scope (global, project, task, agent)")
	cmd.Flags().String("scope-id", "", "Scope ID (required for non-global scopes)")
	cmd.Flags().Float64("bump", 0.05, "Confidence bump amount (0.0-1.0)")

	_ = cmd.MarkFlagRequired("key")

	return cmd
}

func newMemoryQueryCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "query",
		Short: "Search memory entries by pattern, ranked by confidence and recency",
		RunE: func(cmd *cobra.Command, args []string) error {
			scope, _ := cmd.Flags().GetString("scope")
			scopeID, _ := cmd.Flags().GetString("scope-id")
			pattern, _ := cmd.Flags().GetString("pattern")
			limit, _ := cmd.Flags().GetInt("limit")

			var memories []*models.Memory
			if err := withDB(func(db *DB) error {
				m, err := actions.MemoryQuery(db, scope, scopeID, pattern, limit)
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
				Pattern  string           `json:"pattern"`
				Count    int              `json:"count"`
				Memories []*models.Memory `json:"memories"`
			}
			return output.PrintSuccess(resp{Scope: scope, ScopeID: scopeID, Pattern: pattern, Count: len(memories), Memories: memories})
		},
	}

	cmd.Flags().StringP("scope", "s", "global", "Scope (global, project, task, agent)")
	cmd.Flags().String("scope-id", "", "Scope ID (required for non-global scopes)")
	cmd.Flags().StringP("pattern", "p", "%", "Key pattern for LIKE matching (e.g., 'api%', '%config%')")
	cmd.Flags().IntP("limit", "n", 20, "Maximum results to return")

	return cmd
}
