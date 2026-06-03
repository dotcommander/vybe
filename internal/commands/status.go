package commands

import (
	"context"
	"errors"

	"github.com/spf13/cobra"

	"github.com/dotcommander/vybe/internal/actions"
	"github.com/dotcommander/vybe/internal/app"
	"github.com/dotcommander/vybe/internal/models"
	"github.com/dotcommander/vybe/internal/output"
	"github.com/dotcommander/vybe/internal/store"
)

// NewStatusCmd creates the status command. Pass the root command so --schema can collect schemas.
// Callers in root.go must call NewStatusCmd(root) after the root command is fully wired.
//
//nolint:revive,funlen // status display requires many conditional checks for completeness; splitting degrades the linear status-collection flow
func NewStatusCmd(root *cobra.Command) *cobra.Command {
	var check bool

	cmd := &cobra.Command{
		Use:   "status",
		Short: "Show minimal status and optional health check",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runDefaultStatus(cmd, check)
		},
	}

	cmd.Flags().BoolVar(&check, "check", false, "Run database connectivity check (SELECT 1)")

	return cmd
}

func runEventsMode(cmd *cobra.Command, all bool, taskID, kind string, since int64, limit int, asc, includeArchived bool) error {
	agentName := resolveActorName(cmd, "")
	if all {
		agentName = ""
	}
	if !all && agentName == "" {
		return cmdErr(errors.New("agent is required unless --all is set (set --agent or VYBE_AGENT)"))
	}

	var events []*models.Event
	if err := withDB(func(db *DB) error {
		ev, err := store.ListEvents(db, store.ListEventsParams{
			AgentName:       agentName,
			TaskID:          taskID,
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
		TaskID string          `json:"task_id,omitempty"`
		Kind   string          `json:"kind,omitempty"`
		Since  int64           `json:"since_id,omitempty"`
		Count  int             `json:"count"`
		Events []*models.Event `json:"events"`
	}
	return output.PrintSuccess(resp{
		Agent:  agentName,
		TaskID: taskID,
		Kind:   kind,
		Since:  since,
		Count:  len(events),
		Events: events,
	})
}

func runArtifactsMode(taskID string, limit int) error {
	if taskID == "" {
		return cmdErr(errors.New("--task-id is required"))
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
	return output.PrintSuccess(resp{
		TaskID:    taskID,
		Count:     len(artifacts),
		Artifacts: artifacts,
	})
}

func runDefaultStatus(cmd *cobra.Command, check bool) error {
	dbPath, _, err := app.ResolveDBPathDetailed()
	if err != nil {
		return cmdErr(err)
	}

	type dbInfo struct {
		Path  string `json:"path"`
		OK    bool   `json:"ok"`
		Error string `json:"error,omitempty"`
	}

	type resp struct {
		DB         dbInfo             `json:"db"`
		AgentState *models.AgentState `json:"agent_state,omitempty"`
		QueryOK    *bool              `json:"query_ok,omitempty"`
		QueryError string             `json:"query_error,omitempty"`
	}

	result := resp{
		DB: dbInfo{
			Path: dbPath,
		},
	}

	db, err := store.OpenDB(dbPath)
	if err != nil {
		result.DB.OK = false
		result.DB.Error = err.Error()
		if check {
			qOK := false
			result.QueryOK = &qOK
			result.QueryError = "db not available"
		}
		return output.PrintSuccess(result)
	}

	result.DB.OK = true
	defer func() { _ = db.Close() }()

	agentName := resolveActorName(cmd, "")
	if agentName != "" {
		if state, err := store.LoadOrCreateAgentState(db, agentName); err == nil {
			result.AgentState = state
		}
	}

	if check {
		var one int
		qErr := db.QueryRowContext(context.Background(), "SELECT 1").Scan(&one)
		qOK := qErr == nil
		result.QueryOK = &qOK
		if !qOK {
			result.QueryError = qErr.Error()
		}
	}

	return output.PrintSuccess(result)
}
