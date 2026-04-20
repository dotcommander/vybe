package store

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"strings"

	"github.com/dotcommander/vybe/internal/models"
)

const andProjectIDFilter = " AND project_id = ?"

// memoryBriefLimit caps the number of memory entries returned in a brief packet.
// Keep in sync with the LIMIT clause in fetchRelevantMemory SQL queries.
const memoryBriefLimit = 50

// PipelineTask is a lightweight task reference for discovery context.
type PipelineTask struct {
	ID       string `json:"id"`
	Title    string `json:"title"`
	Priority int    `json:"priority"`
}

// BriefPacket contains all context needed for an agent to resume work.
type BriefPacket struct {
	BriefVersion   string             `json:"brief_version"`
	Task           *models.Task       `json:"task"`
	Project        *models.Project    `json:"project,omitempty"`
	RelevantMemory []*models.Memory   `json:"relevant_memory"`
	RecentEvents   []*models.Event    `json:"recent_events"`
	Artifacts      []*models.Artifact `json:"artifacts"`
	PriorReasoning []*models.Event    `json:"prior_reasoning"`
	ApproxTokens   int                `json:"approx_tokens"`
	Counts         *TaskStatusCounts  `json:"counts,omitempty"`
	Pipeline       []PipelineTask     `json:"pipeline,omitempty"`
}

// BuildBrief constructs a brief packet for a focus task and optional project.
func BuildBrief(db *sql.DB, focusTaskID, focusProjectID, agentName string) (*BriefPacket, error) {
	brief := &BriefPacket{
		BriefVersion:   "v1",
		RelevantMemory: []*models.Memory{},
		RecentEvents:   []*models.Event{},
		Artifacts:      []*models.Artifact{},
		PriorReasoning: []*models.Event{},
	}

	if focusTaskID == "" && focusProjectID == "" {
		return brief, nil
	}

	if focusProjectID != "" {
		project, err := GetProject(db, focusProjectID)
		if err == nil {
			brief.Project = project
		}
	}

	if focusTaskID == "" {
		memory, err := fetchRelevantMemory(db, "", focusProjectID)
		if err != nil {
			return nil, fmt.Errorf("failed to fetch memory: %w", err)
		}
		brief.RelevantMemory = memory
		reasoning, err := FetchPriorReasoning(db, focusProjectID, 10)
		if err == nil && len(reasoning) > 0 {
			brief.PriorReasoning = reasoning
		}
		return brief, nil
	}

	task, err := GetTask(db, focusTaskID)
	if err != nil {
		return nil, fmt.Errorf("failed to get focus task: %w", err)
	}
	brief.Task = task

	memory, err := fetchRelevantMemory(db, focusTaskID, focusProjectID)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch memory: %w", err)
	}
	brief.RelevantMemory = memory

	events, err := fetchRecentEvents(db, focusTaskID)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch events: %w", err)
	}
	brief.RecentEvents = events
	brief.ApproxTokens = estimateApproxTokensFromEventMessages(events)

	artifacts, err := fetchArtifacts(db, focusTaskID)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch artifacts: %w", err)
	}
	brief.Artifacts = artifacts

	reasoning, err := FetchPriorReasoning(db, focusProjectID, 10)
	if err == nil && len(reasoning) > 0 {
		brief.PriorReasoning = reasoning
		brief.ApproxTokens += estimateApproxTokensFromEventMessages(reasoning)
	}

	if counts, cErr := GetTaskStatusCounts(db, focusProjectID); cErr == nil {
		brief.Counts = counts
	}
	if pipeline, pErr := FetchPipelineTasks(db, focusTaskID, agentName, focusProjectID, 5); pErr == nil && len(pipeline) > 0 {
		brief.Pipeline = pipeline
	}

	return brief, nil
}

// FetchRecentUserPrompts retrieves the most recent user_prompt events for a project.
func FetchRecentUserPrompts(db *sql.DB, projectDir string, limit int) ([]*models.Event, error) {
	if limit <= 0 {
		limit = 5
	}

	var events []*models.Event
	err := RetryWithBackoff(context.Background(), func() error {
		var query string
		var args []any

		if projectDir != "" {
			query = `
				SELECT id, kind, agent_name, project_id, task_id, message, metadata, created_at
				FROM events
				WHERE kind = 'user_prompt' AND archived_at IS NULL
				  AND (project_id = ? OR json_extract(metadata, '$.project') = ?)
				ORDER BY id DESC LIMIT ?
			`
			args = []any{projectDir, projectDir, limit}
		} else {
			query = `
				SELECT id, kind, agent_name, project_id, task_id, message, metadata, created_at
				FROM events
				WHERE kind = 'user_prompt' AND archived_at IS NULL
				ORDER BY id DESC LIMIT ?
			`
			args = []any{limit}
		}

		rows, err := db.QueryContext(context.Background(), query, args...)
		if err != nil {
			return fmt.Errorf("failed to fetch user prompts: %w", err)
		}
		defer func() { _ = rows.Close() }()

		events, err = scanEventRows(rows)
		return err
	})
	if err != nil {
		return nil, err
	}

	return events, nil
}

// FetchPriorReasoning retrieves the most recent reasoning events for a project.
func FetchPriorReasoning(db *sql.DB, projectID string, limit int) ([]*models.Event, error) {
	if limit <= 0 {
		limit = 10
	}

	var events []*models.Event
	err := RetryWithBackoff(context.Background(), func() error {
		var query string
		var args []any

		if projectID != "" {
			query = `
				SELECT id, kind, agent_name, project_id, task_id, message, metadata, created_at
				FROM events
				WHERE kind = 'reasoning' AND archived_at IS NULL
				  AND ` + ProjectOrGlobalScopeClause + `
				ORDER BY id DESC LIMIT ?
			`
			args = []any{projectID, limit}
		} else {
			query = `
				SELECT id, kind, agent_name, project_id, task_id, message, metadata, created_at
				FROM events
				WHERE kind = 'reasoning' AND archived_at IS NULL
				ORDER BY id DESC LIMIT ?
			`
			args = []any{limit}
		}

		rows, err := db.QueryContext(context.Background(), query, args...)
		if err != nil {
			return fmt.Errorf("failed to fetch prior reasoning: %w", err)
		}
		defer func() { _ = rows.Close() }()

		events, err = scanEventRows(rows)
		return err
	})
	if err != nil {
		return nil, err
	}

	return events, nil
}

// GetTaskStatusCounts returns task status aggregation, optionally scoped to a project.
func GetTaskStatusCounts(db *sql.DB, projectID string) (*TaskStatusCounts, error) {
	counts := &TaskStatusCounts{}
	err := RetryWithBackoff(context.Background(), func() error {
		var query string
		var args []any

		if projectID != "" {
			query = `
				SELECT
					COALESCE(SUM(CASE WHEN status = 'pending' THEN 1 ELSE 0 END), 0),
					COALESCE(SUM(CASE WHEN status = 'in_progress' THEN 1 ELSE 0 END), 0),
					COALESCE(SUM(CASE WHEN status = 'completed' THEN 1 ELSE 0 END), 0),
					COALESCE(SUM(CASE WHEN status = 'blocked' THEN 1 ELSE 0 END), 0)
				FROM tasks
				WHERE project_id = ?
			`
			args = []any{projectID}
		} else {
			query = `
				SELECT
					COALESCE(SUM(CASE WHEN status = 'pending' THEN 1 ELSE 0 END), 0),
					COALESCE(SUM(CASE WHEN status = 'in_progress' THEN 1 ELSE 0 END), 0),
					COALESCE(SUM(CASE WHEN status = 'completed' THEN 1 ELSE 0 END), 0),
					COALESCE(SUM(CASE WHEN status = 'blocked' THEN 1 ELSE 0 END), 0)
				FROM tasks
			`
		}

		return db.QueryRowContext(context.Background(), query, args...).Scan(
			&counts.Pending,
			&counts.InProgress,
			&counts.Completed,
			&counts.Blocked,
		)
	})
	if err != nil {
		return nil, fmt.Errorf("failed to get task status counts: %w", err)
	}

	return counts, nil
}

// FetchPipelineTasks returns the next pending tasks in queue order, excluding the current focus task.
func FetchPipelineTasks(db *sql.DB, excludeTaskID, _ /*agentName*/, projectID string, limit int) ([]PipelineTask, error) {
	if limit <= 0 {
		limit = 5
	}

	var tasks []PipelineTask
	err := RetryWithBackoff(context.Background(), func() error {
		query := `
			SELECT id, title, priority FROM tasks
			WHERE status = 'pending'
			  AND id != ?
		`
		args := []any{excludeTaskID}
		if projectID != "" {
			query += andProjectIDFilter
			args = append(args, projectID)
		}
		query += " ORDER BY priority DESC, created_at ASC LIMIT ?"
		args = append(args, limit)

		rows, err := db.QueryContext(context.Background(), query, args...)
		if err != nil {
			return fmt.Errorf("failed to query pipeline tasks: %w", err)
		}
		defer func() { _ = rows.Close() }()

		tasks = make([]PipelineTask, 0, limit)
		for rows.Next() {
			var pt PipelineTask
			if err := rows.Scan(&pt.ID, &pt.Title, &pt.Priority); err != nil {
				return fmt.Errorf("failed to scan pipeline task: %w", err)
			}
			tasks = append(tasks, pt)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, err
	}

	return tasks, nil
}

func estimateApproxTokensFromEventMessages(events []*models.Event) int {
	totalChars := 0
	for _, event := range events {
		totalChars += len(event.Message)
	}
	if totalChars == 0 {
		return 0
	}

	return (totalChars + 3) / 4
}

// fetchRelevantMemory retrieves memory relevant to a task and/or project, ranked by ACT-R score.
func fetchRelevantMemory(db *sql.DB, taskID, projectID string) ([]*models.Memory, error) {
	var memories []*models.Memory
	var ids []int64

	err := RetryWithBackoff(context.Background(), func() error {
		var query string
		var args []any

		relevanceExpr := `(1.0 + access_count) / (1.0 + MAX(julianday('now') - julianday(COALESCE(last_accessed_at, updated_at)), 0.0)) AS relevance`

		if projectID != "" {
			query = `
				SELECT id, key, value, value_type, scope, scope_id, expires_at, updated_at, created_at, access_count, last_accessed_at, pinned, ` + relevanceExpr + `
				FROM memory
				WHERE (
					scope = 'global'
					OR (scope = 'task' AND scope_id = ?)
					OR (scope = 'project' AND scope_id = ?)
				)
				AND (pinned = 1 OR expires_at IS NULL OR expires_at > CURRENT_TIMESTAMP)
				ORDER BY pinned DESC, relevance DESC
				LIMIT 50
			`
			args = []any{taskID, projectID}
		} else {
			query = `
				SELECT id, key, value, value_type, scope, scope_id, expires_at, updated_at, created_at, access_count, last_accessed_at, pinned, ` + relevanceExpr + `
				FROM memory
				WHERE (
					scope = 'global'
					OR (scope = 'task' AND scope_id = ?)
					OR scope = 'project'
				)
				AND (pinned = 1 OR expires_at IS NULL OR expires_at > CURRENT_TIMESTAMP)
				ORDER BY pinned DESC, relevance DESC
				LIMIT 50
			`
			args = []any{taskID}
		}

		rows, err := db.QueryContext(context.Background(), query, args...)
		if err != nil {
			return fmt.Errorf("failed to query memory: %w", err)
		}
		defer func() { _ = rows.Close() }()

		memories = make([]*models.Memory, 0, memoryBriefLimit)
		ids = make([]int64, 0, memoryBriefLimit)
		for rows.Next() {
			var mem models.Memory
			if err := rows.Scan(
				&mem.ID, &mem.Key, &mem.Value, &mem.ValueType, &mem.Scope, &mem.ScopeID,
				&mem.ExpiresAt, &mem.UpdatedAt, &mem.CreatedAt, &mem.AccessCount, &mem.LastAccessedAt,
				&mem.Pinned, &mem.Relevance,
			); err != nil {
				return fmt.Errorf("failed to scan memory: %w", err)
			}
			memories = append(memories, &mem)
			ids = append(ids, mem.ID)
		}

		return rows.Err()
	})
	if err != nil {
		return nil, err
	}

	if len(ids) > 0 {
		placeholders := strings.Repeat("?,", len(ids))
		placeholders = placeholders[:len(placeholders)-1]
		updateQuery := fmt.Sprintf(`UPDATE memory SET access_count = access_count + 1, last_accessed_at = CURRENT_TIMESTAMP WHERE id IN (%s)`, placeholders) //nolint:gosec // G201: placeholders are safe "?,?" repetitions
		args := make([]any, len(ids))
		for i, id := range ids {
			args[i] = id
		}
		if _, err := db.ExecContext(context.Background(), updateQuery, args...); err != nil {
			slog.Warn("failed to update memory access counts", "error", err)
		}
	}

	return memories, nil
}

func fetchRecentEvents(db *sql.DB, taskID string) ([]*models.Event, error) {
	var events []*models.Event
	err := RetryWithBackoff(context.Background(), func() error {
		rows, err := db.QueryContext(context.Background(), `
			SELECT id, kind, agent_name, project_id, task_id, message, metadata, created_at
			FROM events
			WHERE task_id = ? AND archived_at IS NULL
			ORDER BY id DESC
			LIMIT 20
		`, taskID)
		if err != nil {
			return fmt.Errorf("failed to query events: %w", err)
		}
		defer func() { _ = rows.Close() }()

		events, err = scanEventRows(rows)
		return err
	})
	if err != nil {
		return nil, err
	}

	return events, nil
}

func fetchArtifacts(db *sql.DB, taskID string) ([]*models.Artifact, error) {
	var artifacts []*models.Artifact
	err := RetryWithBackoff(context.Background(), func() error {
		rows, err := db.QueryContext(context.Background(), `
			SELECT id, task_id, event_id, file_path, content_type, created_at
			FROM artifacts
			WHERE task_id = ?
			ORDER BY created_at DESC
			LIMIT 100
		`, taskID)
		if err != nil {
			return fmt.Errorf("failed to query artifacts: %w", err)
		}
		defer func() { _ = rows.Close() }()

		artifacts = make([]*models.Artifact, 0)
		for rows.Next() {
			var artifact models.Artifact
			var contentType sql.NullString
			if err := rows.Scan(
				&artifact.ID,
				&artifact.TaskID,
				&artifact.EventID,
				&artifact.FilePath,
				&contentType,
				&artifact.CreatedAt,
			); err != nil {
				return fmt.Errorf("failed to scan artifact: %w", err)
			}
			if contentType.Valid {
				artifact.ContentType = contentType.String
			}
			artifacts = append(artifacts, &artifact)
		}

		return rows.Err()
	})
	if err != nil {
		return nil, err
	}

	return artifacts, nil
}
