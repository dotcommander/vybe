package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/dotcommander/vybe/internal/models"
)

// Memory quality thresholds used by fetchRelevantMemory to filter noise.
// MinMemoryConfidence: entries below this confidence AND older than MemoryRecencyDays are excluded.
// MemoryRecencyDays: entries younger than this are included regardless of confidence.
const (
	MinMemoryConfidence = 0.3
	MemoryRecencyDays   = 14

	// statusInProgress is the task status constant used in focus selection rules.
	statusInProgress = "in_progress"
	// statusBlocked is the task status constant used in focus selection rules.
	statusBlocked = "blocked"
	// andProjectIDFilter is the SQL fragment for filtering tasks to a project.
	andProjectIDFilter = " AND project_id = ?"
)

// scanEventRows extracts the repeated 8-column event scan loop used by all event
// fetch functions. Handles NullString decoding for project_id and metadata.
func scanEventRows(rows *sql.Rows) ([]*models.Event, error) {
	var events []*models.Event
	for rows.Next() {
		var event models.Event
		var eventProjectID sql.NullString
		var metadata sql.NullString
		if err := rows.Scan(
			&event.ID, &event.Kind, &event.AgentName, &eventProjectID,
			&event.TaskID, &event.Message, &metadata, &event.CreatedAt,
		); err != nil {
			return nil, fmt.Errorf("failed to scan event: %w", err)
		}
		if eventProjectID.Valid {
			event.ProjectID = eventProjectID.String
		}
		event.Metadata = decodeEventMetadata(metadata)
		events = append(events, &event)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return events, nil
}

// PipelineTask is a lightweight task reference for discovery context.
type PipelineTask struct {
	ID       string `json:"id"`
	Title    string `json:"title"`
	Priority int    `json:"priority"`
}

// BriefPacket contains all context needed for an agent to resume work
type BriefPacket struct {
	Task           *models.Task       `json:"task"`
	Project        *models.Project    `json:"project,omitempty"`
	RelevantMemory []*models.Memory   `json:"relevant_memory"`
	RecentEvents   []*models.Event    `json:"recent_events"`
	Artifacts      []*models.Artifact `json:"artifacts"`
	PriorReasoning []*models.Event    `json:"prior_reasoning"`
	ApproxTokens   int                `json:"approx_tokens"`
	Counts         *TaskStatusCounts  `json:"counts,omitempty"`
	Pipeline       []PipelineTask     `json:"pipeline,omitempty"`
	Unlocks        []PipelineTask     `json:"unlocks,omitempty"`
}

// FetchEventsSince retrieves events after a cursor position.
// When projectID is non-empty, events are scoped to that project plus global events,
// so agents resuming into a project context also see global continuity events.
//
//nolint:dupl // FetchSessionEvents has a similar structure but different SQL filter and default limit
func FetchEventsSince(db *sql.DB, cursorID int64, limit int, projectID string) ([]*models.Event, error) {
	if limit <= 0 {
		limit = 1000
	}

	var events []*models.Event

	err := RetryWithBackoff(func() error {
		query := `
			SELECT id, kind, agent_name, project_id, task_id, message, metadata, created_at
			FROM events
			WHERE id > ? AND archived_at IS NULL
		`
		args := []any{cursorID}
		if projectID != "" {
			query += " AND " + ProjectOrGlobalScopeClause
			args = append(args, projectID)
		}
		query += " ORDER BY id ASC LIMIT ?"
		args = append(args, limit)

		rows, err := db.QueryContext(context.Background(), query, args...)
		if err != nil {
			return fmt.Errorf("failed to fetch events: %w", err)
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

// keepCurrentFocus evaluates Rules 1 and 1.5 for DetermineFocusTask.
// Returns true if the current focus should be kept, false to fall through to lower rules.
// Any lookup error is treated as "task gone" and returns false.
func keepCurrentFocus(db *sql.DB, currentFocusID string) bool {
	if currentFocusID == "" {
		return false
	}
	task, err := GetTask(db, currentFocusID)
	if err != nil {
		return false // Task gone; fall through
	}
	if task.Status == statusInProgress {
		return true
	}
	// Rule 1.5: keep dependency-blocked focus only if still has unresolved deps.
	if task.Status == statusBlocked && !task.BlockedReason.IsFailure() {
		var hasUnresolved bool
		depErr := Transact(db, func(tx *sql.Tx) error {
			var txErr error
			hasUnresolved, txErr = HasUnresolvedDependenciesTx(tx, currentFocusID)
			return txErr
		})
		if depErr == nil && hasUnresolved {
			return true
		}
	}
	return false
}

// pickAssignedTask evaluates a candidate task from a task_assigned event for Rule 2.
// Returns the task ID if it is eligible, or "" to skip.
func pickAssignedTask(db *sql.DB, taskID, agentName, projectID string) string {
	task, err := GetTask(db, taskID)
	if err != nil {
		return ""
	}
	if task.Status != "pending" {
		return ""
	}
	var hasUnresolved bool
	if depErr := Transact(db, func(tx *sql.Tx) error {
		var txErr error
		hasUnresolved, txErr = HasUnresolvedDependenciesTx(tx, taskID)
		return txErr
	}); depErr != nil || hasUnresolved {
		return ""
	}
	if task.ClaimedBy != "" && task.ClaimedBy != agentName {
		if task.ClaimExpiresAt != nil && task.ClaimExpiresAt.After(time.Now()) {
			return "" // Claimed by another agent and not expired
		}
	}
	if projectID != "" && task.ProjectID != projectID {
		return ""
	}
	return taskID
}

// DetermineFocusTask selects a task to focus on using deterministic rules.
// Rule 4 only considers tasks that are available for claiming (unclaimed, self-claimed, or expired).
// When projectID is non-empty, Rule 4 is strict and only considers pending tasks in that project.
//
//nolint:gocognit,gocyclo // five-rule deterministic focus algorithm; each rule is a distinct priority level and cannot be split without losing the priority ordering
func DetermineFocusTask(db *sql.DB, agentName, currentFocusID string, deltas []*models.Event, projectID string) (string, error) {
	// Rule 1 + 1.5: Keep in_progress focus; keep dependency-blocked focus if still blocked.
	if keepCurrentFocus(db, currentFocusID) {
		return currentFocusID, nil
	}

	// Rule 2: Check deltas for explicit task assignment events
	for _, event := range deltas {
		if event.Kind != "task_assigned" || event.TaskID == "" {
			continue
		}
		if taskID := pickAssignedTask(db, event.TaskID, agentName, projectID); taskID != "" {
			return taskID, nil
		}
	}

	// Rule 3: If old focus was blocked, check if now unblocked
	if currentFocusID != "" {
		task, err := GetTask(db, currentFocusID)
		if err == nil && task.Status == "pending" {
			return currentFocusID, nil
		}
	}

	// Rule 4: Select highest priority pending task that is available for claiming.
	// Within same priority, select oldest (by created_at).
	// When projectID is set, only select tasks in that project.
	// Exclude tasks with unresolved dependencies.
	var taskID string
	err := RetryWithBackoff(func() error {
		if projectID != "" {
			err := db.QueryRowContext(context.Background(), `
				SELECT id FROM tasks
				WHERE status = 'pending' AND project_id = ?
				  AND (claimed_by IS NULL OR claimed_by = ? OR claim_expires_at < CURRENT_TIMESTAMP)
				  AND NOT EXISTS (
					SELECT 1 FROM task_dependencies td
					JOIN tasks dep ON dep.id = td.depends_on_task_id
					WHERE td.task_id = tasks.id AND dep.status != 'completed'
				  )
				ORDER BY priority DESC, created_at ASC LIMIT 1
			`, projectID, agentName).Scan(&taskID)
			if err == sql.ErrNoRows {
				taskID = ""
				return nil
			}
			return err
		}

		err := db.QueryRowContext(context.Background(), `
			SELECT id FROM tasks
			WHERE status = 'pending'
			  AND (claimed_by IS NULL OR claimed_by = ? OR claim_expires_at < CURRENT_TIMESTAMP)
			  AND NOT EXISTS (
				SELECT 1 FROM task_dependencies td
				JOIN tasks dep ON dep.id = td.depends_on_task_id
				WHERE td.task_id = tasks.id AND dep.status != 'completed'
			  )
			ORDER BY priority DESC, created_at ASC LIMIT 1
		`, agentName).Scan(&taskID)
		if err == sql.ErrNoRows {
			taskID = ""
			return nil
		}
		return err
	})

	if err != nil {
		return "", fmt.Errorf("failed to select focus task: %w", err)
	}

	// Rule 5: Return empty if no work available
	return taskID, nil
}

// BuildBrief constructs a brief packet for a focus task and optional project.
//
//nolint:gocognit,gocyclo,revive // brief assembly fetches task, project, memory, events, artifacts across multiple optional branches
func BuildBrief(db *sql.DB, focusTaskID, focusProjectID, agentName string) (*BriefPacket, error) {
	brief := &BriefPacket{
		RelevantMemory: []*models.Memory{},
		RecentEvents:   []*models.Event{},
		Artifacts:      []*models.Artifact{},
		PriorReasoning: []*models.Event{},
	}

	if focusTaskID == "" && focusProjectID == "" {
		return brief, nil
	}

	// Fetch focus project if set
	if focusProjectID != "" {
		project, err := GetProject(db, focusProjectID)
		if err == nil {
			brief.Project = project
		}
		// Non-fatal: project may have been deleted
	}

	if focusTaskID == "" {
		// Project-only brief: include project memory but no task/events/artifacts
		memory, err := fetchRelevantMemory(db, "", focusProjectID)
		if err != nil {
			return nil, fmt.Errorf("failed to fetch memory: %w", err)
		}
		brief.RelevantMemory = memory
		brief.ApproxTokens = 0
		reasoning, err := FetchPriorReasoning(db, focusProjectID, 10)
		if err == nil && len(reasoning) > 0 {
			brief.PriorReasoning = reasoning
		}
		return brief, nil
	}

	// Fetch the focus task
	task, err := GetTask(db, focusTaskID)
	if err != nil {
		return nil, fmt.Errorf("failed to get focus task: %w", err)
	}
	brief.Task = task

	// Fetch relevant memory
	memory, err := fetchRelevantMemory(db, focusTaskID, focusProjectID)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch memory: %w", err)
	}
	brief.RelevantMemory = memory

	// Fetch recent events for this task
	events, err := fetchRecentEvents(db, focusTaskID)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch events: %w", err)
	}
	brief.RecentEvents = events
	brief.ApproxTokens = estimateApproxTokensFromEventMessages(events)

	// Fetch artifacts for this task
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

	// Discovery context — all non-fatal, degrade to nil/empty on error.
	if counts, cErr := GetTaskStatusCounts(db, focusProjectID); cErr == nil {
		brief.Counts = counts
	}
	if pipeline, pErr := FetchPipelineTasks(db, focusTaskID, agentName, focusProjectID, 5); pErr == nil && len(pipeline) > 0 {
		brief.Pipeline = pipeline
	}
	if focusTaskID != "" {
		if unlocks, uErr := FetchUnlockedByCompletion(db, focusTaskID); uErr == nil && len(unlocks) > 0 {
			brief.Unlocks = unlocks
		}
	}

	return brief, nil
}

// FetchRecentUserPrompts retrieves the most recent user_prompt events for a project.
// When projectDir is non-empty, it matches against both project_id column and
// the metadata->project field (for events ingested from history.jsonl).
// Returns events in reverse chronological order (newest first).
func FetchRecentUserPrompts(db *sql.DB, projectDir string, limit int) ([]*models.Event, error) {
	if limit <= 0 {
		limit = 5
	}

	var events []*models.Event

	err := RetryWithBackoff(func() error {
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
// Returns events in reverse chronological order (newest first).
func FetchPriorReasoning(db *sql.DB, projectID string, limit int) ([]*models.Event, error) {
	if limit <= 0 {
		limit = 10
	}

	var events []*models.Event

	err := RetryWithBackoff(func() error {
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

// FetchSessionEvents retrieves events useful for session retrospective extraction.
// Filters to actionable event kinds and returns in chronological order (oldest first).
//
//nolint:dupl // FetchEventsSince has a similar structure but different SQL filter and default limit
func FetchSessionEvents(db *sql.DB, sinceID int64, projectID string, limit int) ([]*models.Event, error) {
	if limit <= 0 {
		limit = 200
	}

	var events []*models.Event

	err := RetryWithBackoff(func() error {
		query := `
			SELECT id, kind, agent_name, project_id, task_id, message, metadata, created_at
			FROM events
			WHERE id > ? AND archived_at IS NULL
			  AND kind IN ('user_prompt', 'reasoning', 'tool_failure', 'task_status', 'progress')
		`
		args := []any{sinceID}
		if projectID != "" {
			query += " AND " + ProjectScopeClause
			args = append(args, projectID)
		}
		query += " ORDER BY id ASC LIMIT ?"
		args = append(args, limit)

		rows, err := db.QueryContext(context.Background(), query, args...)
		if err != nil {
			return fmt.Errorf("failed to fetch session events: %w", err)
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

	err := RetryWithBackoff(func() error {
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

// FetchPipelineTasks returns the next pending tasks in queue order, excluding the
// current focus task, tasks claimed by other agents, and tasks with unresolved deps.
func FetchPipelineTasks(db *sql.DB, excludeTaskID, agentName, projectID string, limit int) ([]PipelineTask, error) {
	if limit <= 0 {
		limit = 5
	}

	var tasks []PipelineTask

	err := RetryWithBackoff(func() error {
		query := `
			SELECT id, title, priority FROM tasks
			WHERE status = 'pending'
			  AND id != ?
			  AND (claimed_by IS NULL OR claimed_by = ? OR claim_expires_at < CURRENT_TIMESTAMP)
			  AND NOT EXISTS (
				SELECT 1 FROM task_dependencies td
				JOIN tasks dep ON dep.id = td.depends_on_task_id
				WHERE td.task_id = tasks.id AND dep.status != 'completed'
			  )
		`
		args := []any{excludeTaskID, agentName}
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

// FetchUnlockedByCompletion finds tasks that would become unblocked if focusTaskID
// were completed — i.e., tasks whose ONLY remaining unresolved dependency is focusTaskID.
func FetchUnlockedByCompletion(db *sql.DB, focusTaskID string) ([]PipelineTask, error) {
	var tasks []PipelineTask

	err := RetryWithBackoff(func() error {
		query := `
			SELECT t.id, t.title, t.priority
			FROM task_dependencies td
			JOIN tasks t ON t.id = td.task_id
			WHERE td.depends_on_task_id = ?
			  AND t.status != 'completed'
			  AND NOT EXISTS (
				SELECT 1 FROM task_dependencies td2
				JOIN tasks dep ON dep.id = td2.depends_on_task_id
				WHERE td2.task_id = t.id
				  AND td2.depends_on_task_id != ?
				  AND dep.status != 'completed'
			  )
			ORDER BY t.priority DESC, t.created_at ASC
		`
		rows, err := db.QueryContext(context.Background(), query, focusTaskID, focusTaskID)
		if err != nil {
			return fmt.Errorf("failed to query unlocked tasks: %w", err)
		}
		defer func() { _ = rows.Close() }()

		tasks = make([]PipelineTask, 0)
		for rows.Next() {
			var pt PipelineTask
			if err := rows.Scan(&pt.ID, &pt.Title, &pt.Priority); err != nil {
				return fmt.Errorf("failed to scan unlocked task: %w", err)
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

	// Rough estimate used by agents for context-budget decisions.
	return (totalChars + 3) / 4
}

// fetchRelevantMemory retrieves memory relevant to a task and/or project.
// When projectID is non-empty, only project-scoped memory for that project is included.
// When projectID is empty, all project-scoped memory is included (legacy behavior).
//
//nolint:funlen // query construction varies across four scope combinations (global, task, project, all-project); reducing requires helper indirection
func fetchRelevantMemory(db *sql.DB, taskID, projectID string) ([]*models.Memory, error) {
	var memories []*models.Memory

	err := RetryWithBackoff(func() error {
		var query string
		var args []any

		recencyInterval := fmt.Sprintf("-%d days", MemoryRecencyDays)
		if projectID != "" {
			query = `
				SELECT id, key, canonical_key, value, value_type, scope, scope_id,
				       confidence, last_seen_at, source_event_id, superseded_by, expires_at, created_at
				FROM memory
				WHERE (
					scope = 'global'
					OR (scope = 'task' AND scope_id = ?)
					OR (scope = 'project' AND scope_id = ?)
				)
				AND superseded_by IS NULL
				AND (expires_at IS NULL OR expires_at > CURRENT_TIMESTAMP)
				AND (
					confidence >= ?
					OR COALESCE(last_seen_at, created_at) >= datetime('now', ?)
				)
				ORDER BY confidence DESC, COALESCE(last_seen_at, created_at) DESC, created_at DESC
				LIMIT 50
			`
			args = []any{taskID, projectID, MinMemoryConfidence, recencyInterval}
		} else {
			query = `
				SELECT id, key, canonical_key, value, value_type, scope, scope_id,
				       confidence, last_seen_at, source_event_id, superseded_by, expires_at, created_at
				FROM memory
				WHERE (
					scope = 'global'
					OR (scope = 'task' AND scope_id = ?)
					OR scope = 'project'
				)
				AND superseded_by IS NULL
				AND (expires_at IS NULL OR expires_at > CURRENT_TIMESTAMP)
				AND (
					confidence >= ?
					OR COALESCE(last_seen_at, created_at) >= datetime('now', ?)
				)
				ORDER BY confidence DESC, COALESCE(last_seen_at, created_at) DESC, created_at DESC
				LIMIT 50
			`
			args = []any{taskID, MinMemoryConfidence, recencyInterval}
		}

		rows, err := db.QueryContext(context.Background(), query, args...)
		if err != nil {
			return fmt.Errorf("failed to query memory: %w", err)
		}
		defer func() { _ = rows.Close() }()

		memories = make([]*models.Memory, 0)
		for rows.Next() {
			var mem models.Memory
			var (
				lastSeenAt    sql.NullTime
				sourceEventID sql.NullInt64
				supersededBy  sql.NullString
			)
			err := rows.Scan(
				&mem.ID,
				&mem.Key,
				&mem.Canonical,
				&mem.Value,
				&mem.ValueType,
				&mem.Scope,
				&mem.ScopeID,
				&mem.Confidence,
				&lastSeenAt,
				&sourceEventID,
				&supersededBy,
				&mem.ExpiresAt,
				&mem.CreatedAt,
			)
			if err != nil {
				return fmt.Errorf("failed to scan memory: %w", err)
			}
			if lastSeenAt.Valid {
				mem.LastSeenAt = &lastSeenAt.Time
			}
			if sourceEventID.Valid {
				id := sourceEventID.Int64
				mem.SourceEventID = &id
			}
			if supersededBy.Valid {
				mem.SupersededBy = supersededBy.String
			}
			memories = append(memories, &mem)
		}

		return rows.Err()
	})

	if err != nil {
		return nil, err
	}

	return memories, nil
}

// fetchRecentEvents retrieves recent events for a task
func fetchRecentEvents(db *sql.DB, taskID string) ([]*models.Event, error) {
	var events []*models.Event

	err := RetryWithBackoff(func() error {
		query := `
			SELECT id, kind, agent_name, project_id, task_id, message, metadata, created_at
			FROM events
			WHERE task_id = ? AND archived_at IS NULL
			ORDER BY id DESC
			LIMIT 20
		`
		rows, err := db.QueryContext(context.Background(), query, taskID)
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

// fetchArtifacts retrieves artifacts for a task
func fetchArtifacts(db *sql.DB, taskID string) ([]*models.Artifact, error) {
	var artifacts []*models.Artifact

	err := RetryWithBackoff(func() error {
		query := `
			SELECT id, task_id, event_id, file_path, content_type, created_at
			FROM artifacts
			WHERE task_id = ?
			ORDER BY created_at DESC
		`
		rows, err := db.QueryContext(context.Background(), query, taskID)
		if err != nil {
			return fmt.Errorf("failed to query artifacts: %w", err)
		}
		defer func() { _ = rows.Close() }()

		artifacts = make([]*models.Artifact, 0)
		for rows.Next() {
			var artifact models.Artifact
			var contentType sql.NullString
			err := rows.Scan(
				&artifact.ID,
				&artifact.TaskID,
				&artifact.EventID,
				&artifact.FilePath,
				&contentType,
				&artifact.CreatedAt,
			)
			if err != nil {
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

//nolint:funlen // agent_state update requires four UPDATE variants (task+project, task-only, project-only, cursor-only) to avoid nulling existing focus fields
func applyAgentStateAtomicTx(tx *sql.Tx, agentName string, newCursor int64, focusTaskID string, focusProjectID *string) error {
	// Load current state for version check
	var currentVersion int
	err := tx.QueryRowContext(context.Background(), `
		SELECT version
		FROM agent_state
		WHERE agent_name = ?
	`, agentName).Scan(&currentVersion)

	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("agent state not found: %s", agentName)
	}
	if err != nil {
		return fmt.Errorf("failed to load agent state: %w", err)
	}

	// Update with monotonic cursor advance.
	// IMPORTANT: focusTaskID == "" means clear focus (set NULL) so state matches the resume response.
	// If focusProjectID is nil, preserve existing project focus.
	var result sql.Result
	switch {
	case focusTaskID != "" && focusProjectID != nil && *focusProjectID != "":
		result, err = tx.ExecContext(context.Background(), `
				UPDATE agent_state
				SET last_seen_event_id = MAX(last_seen_event_id, ?),
				    focus_task_id = ?,
				    focus_project_id = ?,
				    last_active_at = CURRENT_TIMESTAMP,
				    version = version + 1
				WHERE agent_name = ? AND version = ?
			`, newCursor, focusTaskID, *focusProjectID, agentName, currentVersion)
	case focusTaskID != "" && focusProjectID != nil:
		result, err = tx.ExecContext(context.Background(), `
				UPDATE agent_state
				SET last_seen_event_id = MAX(last_seen_event_id, ?),
				    focus_task_id = ?,
				    focus_project_id = NULL,
				    last_active_at = CURRENT_TIMESTAMP,
				    version = version + 1
				WHERE agent_name = ? AND version = ?
			`, newCursor, focusTaskID, agentName, currentVersion)
	case focusTaskID != "":
		result, err = tx.ExecContext(context.Background(), `
			UPDATE agent_state
			SET last_seen_event_id = MAX(last_seen_event_id, ?),
			    focus_task_id = ?,
			    last_active_at = CURRENT_TIMESTAMP,
			    version = version + 1
			WHERE agent_name = ? AND version = ?
		`, newCursor, focusTaskID, agentName, currentVersion)
	case focusProjectID != nil && *focusProjectID != "":
		result, err = tx.ExecContext(context.Background(), `
				UPDATE agent_state
				SET last_seen_event_id = MAX(last_seen_event_id, ?),
				    focus_task_id = NULL,
				    focus_project_id = ?,
				    last_active_at = CURRENT_TIMESTAMP,
				    version = version + 1
				WHERE agent_name = ? AND version = ?
			`, newCursor, *focusProjectID, agentName, currentVersion)
	case focusProjectID != nil:
		result, err = tx.ExecContext(context.Background(), `
				UPDATE agent_state
				SET last_seen_event_id = MAX(last_seen_event_id, ?),
				    focus_task_id = NULL,
				    focus_project_id = NULL,
				    last_active_at = CURRENT_TIMESTAMP,
				    version = version + 1
				WHERE agent_name = ? AND version = ?
			`, newCursor, agentName, currentVersion)
	default:
		result, err = tx.ExecContext(context.Background(), `
			UPDATE agent_state
			SET last_seen_event_id = MAX(last_seen_event_id, ?),
			    focus_task_id = NULL,
			    last_active_at = CURRENT_TIMESTAMP,
			    version = version + 1
			WHERE agent_name = ? AND version = ?
		`, newCursor, agentName, currentVersion)
	}

	if err != nil {
		return fmt.Errorf("failed to update agent state: %w", err)
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to check rows affected: %w", err)
	}

	if rowsAffected == 0 {
		return ErrVersionConflict
	}

	return nil
}

// UpdateAgentStateAtomicTx is the in-transaction variant of UpdateAgentStateAtomic.
// It does not commit the transaction.
func UpdateAgentStateAtomicTx(tx *sql.Tx, agentName string, newCursor int64, focusTaskID string) error {
	return applyAgentStateAtomicTx(tx, agentName, newCursor, focusTaskID, nil)
}

// UpdateAgentStateAtomicWithProjectTx atomically updates cursor, focus task, and project focus.
// Passing an empty projectID clears focus_project_id.
func UpdateAgentStateAtomicWithProjectTx(tx *sql.Tx, agentName string, newCursor int64, focusTaskID, projectID string) error {
	return applyAgentStateAtomicTx(tx, agentName, newCursor, focusTaskID, &projectID)
}

// UpdateAgentStateAtomic atomically updates cursor and focus task.
func UpdateAgentStateAtomic(db *sql.DB, agentName string, newCursor int64, focusTaskID string) error {
	return Transact(db, func(tx *sql.Tx) error {
		return applyAgentStateAtomicTx(tx, agentName, newCursor, focusTaskID, nil)
	})
}

// UpdateAgentStateAtomicWithProject atomically updates cursor, focus task, and project focus.
// Passing an empty projectID clears focus_project_id.
func UpdateAgentStateAtomicWithProject(db *sql.DB, agentName string, newCursor int64, focusTaskID, projectID string) error {
	return Transact(db, func(tx *sql.Tx) error {
		return applyAgentStateAtomicTx(tx, agentName, newCursor, focusTaskID, &projectID)
	})
}
