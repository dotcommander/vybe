package commands

import (
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"github.com/dotcommander/vybe/internal/models"
	"github.com/dotcommander/vybe/internal/output"
	"github.com/dotcommander/vybe/internal/store"
)

func NewEventsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "events",
		Short: "Inspect the continuity event log",
	}

	cmd.AddCommand(newEventsListCmd())
	cmd.AddCommand(newEventsTailCmd())
	cmd.AddCommand(newEventsSummarizeCmd())
	return cmd
}

func newEventsListCmd() *cobra.Command {
	var (
		all             bool
		task            string
		kind            string
		limit           int
		since           int64
		asc             bool
		includeArchived bool
	)

	cmd := &cobra.Command{
		Use:   "list",
		Short: "List events (filterable)",
		RunE: func(cmd *cobra.Command, args []string) error {
			agentName := resolveActorName(cmd, "")
			if all {
				agentName = ""
			}
			if !all && agentName == "" {
				return cmdErr(fmt.Errorf("agent is required unless --all is set (set --agent or VYBE_AGENT)"))
			}

			var events []*models.Event
			if err := withDB(func(db *DB) error {
				ev, err := store.ListEvents(db, store.ListEventsParams{
					AgentName:       agentName,
					TaskID:          task,
					Kind:            kind,
					SinceID:         since,
					Limit:           limit,
					Desc:            !asc,
					IncludeArchived: includeArchived,
				})
				if err != nil {
					return err
				}
				events = ev
				return nil
			}); err != nil {
				return err
			}

			type resp struct {
				Agent  string          `json:"agent,omitempty"`
				Task   string          `json:"task,omitempty"`
				Kind   string          `json:"kind,omitempty"`
				Since  int64           `json:"since_id,omitempty"`
				Count  int             `json:"count"`
				Events []*models.Event `json:"events"`
			}
			return output.PrintSuccess(resp{
				Agent:  agentName,
				Task:   task,
				Kind:   kind,
				Since:  since,
				Count:  len(events),
				Events: events,
			})
		},
	}

	cmd.Flags().BoolVar(&all, "all", false, "List across all agents (ignores --agent)")
	cmd.Flags().StringVar(&task, "task", "", "Filter by task id")
	cmd.Flags().StringVar(&kind, "kind", "", "Filter by kind")
	cmd.Flags().IntVar(&limit, "limit", 50, "Max events (<= 1000)")
	cmd.Flags().Int64Var(&since, "since-id", 0, "Only events with id > since-id")
	cmd.Flags().BoolVar(&asc, "asc", false, "Sort oldest first (default newest first)")
	cmd.Flags().BoolVar(&includeArchived, "include-archived", false, "Include archived events")

	return cmd
}

func newEventsTailCmd() *cobra.Command {
	var (
		all             bool
		task            string
		kind            string
		limit           int
		since           int64
		interval        time.Duration
		once            bool
		jsonl           bool
		fromCursor      bool
		includeArchived bool
	)

	cmd := &cobra.Command{
		Use:   "tail",
		Short: "Continuously poll and print new events",
		RunE: func(cmd *cobra.Command, args []string) error {
			// Agents-only default is machine output. For streaming, always emit JSONL.
			if !once {
				jsonl = true
			}

			agentName := resolveActorName(cmd, "")
			if all {
				agentName = ""
			}
			if !all && agentName == "" {
				return cmdErr(fmt.Errorf("agent is required unless --all is set (set --agent or VYBE_AGENT)"))
			}

			// Default to agent cursor (if available) to avoid dumping history.
			if since == 0 && fromCursor && agentName != "" && !all {
				_ = withDB(func(db *DB) error {
					s, err := store.GetAgentState(db, agentName)
					if err == nil && s != nil {
						since = s.LastSeenEventID
					}
					return nil
				})
			}

			printEvent := func(e *models.Event) error {
				if jsonl {
					// JSONL: one event per line, raw event object.
					return output.Print(e)
				}
				return output.PrintSuccess(e)
			}

			for {
				var events []*models.Event
				if err := withDB(func(db *DB) error {
					ev, err := store.ListEvents(db, store.ListEventsParams{
						AgentName:       agentName,
						TaskID:          task,
						Kind:            kind,
						SinceID:         since,
						Limit:           limit,
						Desc:            false,
						IncludeArchived: includeArchived,
					})
					if err != nil {
						return err
					}
					events = ev
					return nil
				}); err != nil {
					return err
				}

				if once {
					if jsonl {
						for _, e := range events {
							if err := output.Print(e); err != nil {
								return err
							}
						}
						return nil
					}

					type resp struct {
						Agent  string          `json:"agent,omitempty"`
						Task   string          `json:"task,omitempty"`
						Kind   string          `json:"kind,omitempty"`
						Since  int64           `json:"since_id,omitempty"`
						Count  int             `json:"count"`
						Events []*models.Event `json:"events"`
					}
					return output.PrintSuccess(resp{
						Agent:  agentName,
						Task:   task,
						Kind:   kind,
						Since:  since,
						Count:  len(events),
						Events: events,
					})
				}

				for _, e := range events {
					if e.ID > since {
						since = e.ID
					}
					if err := printEvent(e); err != nil {
						return err
					}
				}

				time.Sleep(interval)
			}
		},
	}

	cmd.Flags().BoolVar(&all, "all", false, "Tail across all agents (ignores --agent)")
	cmd.Flags().StringVar(&task, "task", "", "Filter by task id")
	cmd.Flags().StringVar(&kind, "kind", "", "Filter by kind")
	cmd.Flags().IntVar(&limit, "limit", 50, "Max events per poll (<= 1000)")
	cmd.Flags().Int64Var(&since, "since-id", 0, "Only events with id > since-id")
	cmd.Flags().DurationVar(&interval, "interval", 1*time.Second, "Poll interval")
	cmd.Flags().BoolVar(&once, "once", false, "Fetch once and exit")
	cmd.Flags().BoolVar(&jsonl, "jsonl", false, "Stream events as JSON Lines (one event per line)")
	cmd.Flags().BoolVar(&fromCursor, "from-cursor", true, "If since-id is 0, start from agent cursor (agent-scoped only)")
	cmd.Flags().BoolVar(&includeArchived, "include-archived", false, "Include archived events")

	return cmd
}

func newEventsSummarizeCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "summarize",
		Short: "Archive an event range and append a summary event",
		Long:  "Mark events in an ID range as archived and append one events_summary event for compressed continuity.",
		RunE: func(cmd *cobra.Command, args []string) error {
			agentName, err := requireActorName(cmd, "")
			if err != nil {
				return cmdErr(err)
			}
			requestID, err := requireRequestID(cmd)
			if err != nil {
				return cmdErr(err)
			}

			fromID, _ := cmd.Flags().GetInt64("from-id")
			toID, _ := cmd.Flags().GetInt64("to-id")
			taskID, _ := cmd.Flags().GetString("task")
			summary, _ := cmd.Flags().GetString("summary")

			if fromID <= 0 {
				return cmdErr(fmt.Errorf("--from-id is required and must be > 0"))
			}
			if toID <= 0 {
				return cmdErr(fmt.Errorf("--to-id is required and must be > 0"))
			}
			if summary == "" {
				return cmdErr(fmt.Errorf("--summary is required"))
			}

			var (
				summaryEventID int64
				archivedCount  int64
			)
			if err := withDB(func(db *DB) error {
				eid, count, err := store.ArchiveEventsRangeWithSummaryIdempotent(db, agentName, requestID, "", taskID, fromID, toID, summary)
				if err != nil {
					return err
				}
				summaryEventID = eid
				archivedCount = count
				return nil
			}); err != nil {
				return err
			}

			type resp struct {
				SummaryEventID int64  `json:"summary_event_id"`
				ArchivedCount  int64  `json:"archived_count"`
				FromID         int64  `json:"from_id"`
				ToID           int64  `json:"to_id"`
				TaskID         string `json:"task_id,omitempty"`
			}
			return output.PrintSuccess(resp{
				SummaryEventID: summaryEventID,
				ArchivedCount:  archivedCount,
				FromID:         fromID,
				ToID:           toID,
				TaskID:         taskID,
			})
		},
	}

	cmd.Flags().Int64("from-id", 0, "Archive events with id >= from-id (required)")
	cmd.Flags().Int64("to-id", 0, "Archive events with id <= to-id (required)")
	cmd.Flags().String("task", "", "Optional task id filter for the archive range")
	cmd.Flags().String("summary", "", "Summary message to store in the replacement event (required)")

	return cmd
}
