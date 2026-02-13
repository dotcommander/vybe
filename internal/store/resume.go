package store

import (
	"database/sql"
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
)

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
// When projectID is non-empty, events are scoped to that project plus global events.
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
			query += " AND (project_id = ? OR project_id IS NULL)"
			args = append(args, projectID)
		}
		query += " ORDER BY id ASC LIMIT ?"
		args = append(args, limit)

		rows, err := db.Query(query, args...)
		if err != nil {
			return fmt.Errorf("failed to fetch events: %w", err)
		}
		defer rows.Close()

		events = make([]*models.Event, 0)
		for rows.Next() {
			var event models.Event
			var eventProjectID sql.NullString
			var metadata sql.NullString
			err := rows.Scan(
				&event.ID,
				&event.Kind,
				&event.AgentName,
				&eventProjectID,
				&event.TaskID,
				&event.Message,
				&metadata,
				&event.CreatedAt,
			)
			if err != nil {
				return fmt.Errorf("failed to scan event: %w", err)
			}

			if eventProjectID.Valid {
				event.ProjectID = eventProjectID.String
			}
			event.Metadata = decodeEventMetadata(metadata)

			events = append(events, &event)
		}

		return rows.Err()
	})

	if err != nil {
		return nil, err
	}

	return events, nil
}

// DetermineFocusTask selects a task to focus on using deterministic rules.
// Rule 4 only considers tasks that are available for claiming (unclaimed, self-claimed, or expired).
// When projectID is non-empty, Rule 4 is strict and only considers pending tasks in that project.
func DetermineFocusTask(db *sql.DB, agentName, currentFocusID string, deltas []*models.Event, projectID string) (string, error) {
	// Rule 1: If current focus is in_progress, always keep it
	if currentFocusID != "" {
		task, err := GetTask(db, currentFocusID)
		if err == nil {
			if task.Status == "in_progress" {
				return currentFocusID, nil
			}
			// Rule 1.5: If blocked, keep only if dependency-blocked.
			// Failure-blocked tasks (blocked_reason="failure:...") fall through to Rule 4.
			if task.Status == "blocked" {
				if task.BlockedReason.IsFailure() {
					// Explicit failure block: fall through to find new work
				} else {
					// Dependency-blocked or unknown reason: check unresolved deps
					hasUnresolved, depErr := HasUnresolvedDependencies(db, currentFocusID)
					if depErr == nil && hasUnresolved {
						return currentFocusID, nil
					}
					// No unresolved deps: fall through
				}
			}
		}
	}

	// Rule 2: Check deltas for explicit task assignment events
	for _, event := range deltas {
		if event.Kind == "task_assigned" && event.TaskID != "" {
			task, err := GetTask(db, event.TaskID)
			if err != nil {
				continue // Task doesn't exist, skip
			}
			// Must be pending
			if task.Status != "pending" {
				continue
			}
			// Must have no unresolved dependencies
			hasUnresolved, depErr := HasUnresolvedDependencies(db, event.TaskID)
			if depErr != nil || hasUnresolved {
				continue
			}
			// Must be claimable (unclaimed, self-claimed, or expired)
			if task.ClaimedBy != "" && task.ClaimedBy != agentName {
				if task.ClaimExpiresAt != nil && task.ClaimExpiresAt.After(time.Now()) {
					continue // Claimed by another agent and not expired
				}
			}
			// Project scope must match when set
			if projectID != "" && task.ProjectID != projectID {
				continue
			}
			return event.TaskID, nil
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
			err := db.QueryRow(`
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

		err := db.QueryRow(`
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

		rows, err := db.Query(query, args...)
		if err != nil {
			return fmt.Errorf("failed to fetch user prompts: %w", err)
		}
		defer rows.Close()

		events = make([]*models.Event, 0, limit)
		for rows.Next() {
			var event models.Event
			var eventProjectID sql.NullString
			var metadata sql.NullString
			if err := rows.Scan(
				&event.ID, &event.Kind, &event.AgentName, &eventProjectID,
				&event.TaskID, &event.Message, &metadata, &event.CreatedAt,
			); err != nil {
				return fmt.Errorf("failed to scan user prompt event: %w", err)
			}
			if eventProjectID.Valid {
				event.ProjectID = eventProjectID.String
			}
			event.Metadata = decodeEventMetadata(metadata)
			events = append(events, &event)
		}
		return rows.Err()
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
				  AND (project_id = ? OR project_id IS NULL)
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

		rows, err := db.Query(query, args...)
		if err != nil {
			return fmt.Errorf("failed to fetch prior reasoning: %w", err)
		}
		defer rows.Close()

		events = make([]*models.Event, 0, limit)
		for rows.Next() {
			var event models.Event
			var eventProjectID sql.NullString
			var metadata sql.NullString
			if err := rows.Scan(
				&event.ID, &event.Kind, &event.AgentName, &eventProjectID,
				&event.TaskID, &event.Message, &metadata, &event.CreatedAt,
			); err != nil {
				return fmt.Errorf("failed to scan reasoning event: %w", err)
			}
			if eventProjectID.Valid {
				event.ProjectID = eventProjectID.String
			}
			event.Metadata = decodeEventMetadata(metadata)
			events = append(events, &event)
		}
		return rows.Err()
	})

	if err != nil {
		return nil, err
	}

	return events, nil
}

// FetchSessionEvents retrieves events useful for session retrospective extraction.
// Filters to actionable event kinds and returns in chronological order (oldest first).
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
			query += " AND (project_id = ? OR project_id IS NULL)"
			args = append(args, projectID)
		}
		query += " ORDER BY id ASC LIMIT ?"
		args = append(args, limit)

		rows, err := db.Query(query, args...)
		if err != nil {
			return fmt.Errorf("failed to fetch session events: %w", err)
		}
		defer rows.Close()

		events = make([]*models.Event, 0, limit)
		for rows.Next() {
			var event models.Event
			var eventProjectID sql.NullString
			var metadata sql.NullString
			if err := rows.Scan(
				&event.ID, &event.Kind, &event.AgentName, &eventProjectID,
				&event.TaskID, &event.Message, &metadata, &event.CreatedAt,
			); err != nil {
				return fmt.Errorf("failed to scan session event: %w", err)
			}
			if eventProjectID.Valid {
				event.ProjectID = eventProjectID.String
			}
			event.Metadata = decodeEventMetadata(metadata)
			events = append(events, &event)
		}
		return rows.Err()
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

		return db.QueryRow(query, args...).Scan(
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
			query += " AND project_id = ?"
			args = append(args, projectID)
		}
		query += " ORDER BY priority DESC, created_at ASC LIMIT ?"
		args = append(args, limit)

		rows, err := db.Query(query, args...)
		if err != nil {
			return fmt.Errorf("failed to query pipeline tasks: %w", err)
		}
		defer rows.Close()

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
		rows, err := db.Query(query, focusTaskID, focusTaskID)
		if err != nil {
			return fmt.Errorf("failed to query unlocked tasks: %w", err)
		}
		defer rows.Close()

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
func fetchRelevantMemory(db *sql.DB, taskID, projectID string) ([]*models.Memory, error) {
	var memories []*models.Memory

	err := RetryWithBackoff(func() error {
		var query string
		var args []any

		if projectID != "" {
			query = fmt.Sprintf(`
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
					OR COALESCE(last_seen_at, created_at) >= datetime('now', '-%d days')
				)
				ORDER BY confidence DESC, COALESCE(last_seen_at, created_at) DESC, created_at DESC
				LIMIT 50
			`, MemoryRecencyDays)
			args = []any{taskID, projectID, MinMemoryConfidence}
		} else {
			query = fmt.Sprintf(`
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
					OR COALESCE(last_seen_at, created_at) >= datetime('now', '-%d days')
				)
				ORDER BY confidence DESC, COALESCE(last_seen_at, created_at) DESC, created_at DESC
				LIMIT 50
			`, MemoryRecencyDays)
			args = []any{taskID, MinMemoryConfidence}
		}

		rows, err := db.Query(query, args...)
		if err != nil {
			return fmt.Errorf("failed to query memory: %w", err)
		}
		defer rows.Close()

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
		rows, err := db.Query(query, taskID)
		if err != nil {
			return fmt.Errorf("failed to query events: %w", err)
		}
		defer rows.Close()

		events = make([]*models.Event, 0)
		for rows.Next() {
			var event models.Event
			var eventProjectID sql.NullString
			var metadata sql.NullString
			err := rows.Scan(
				&event.ID,
				&event.Kind,
				&event.AgentName,
				&eventProjectID,
				&event.TaskID,
				&event.Message,
				&metadata,
				&event.CreatedAt,
			)
			if err != nil {
				return fmt.Errorf("failed to scan event: %w", err)
			}

			if eventProjectID.Valid {
				event.ProjectID = eventProjectID.String
			}
			event.Metadata = decodeEventMetadata(metadata)

			events = append(events, &event)
		}

		return rows.Err()
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
		rows, err := db.Query(query, taskID)
		if err != nil {
			return fmt.Errorf("failed to query artifacts: %w", err)
		}
		defer rows.Close()

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

func applyAgentStateAtomicTx(tx *sql.Tx, agentName string, newCursor int64, focusTaskID string, focusProjectID *string) error {
	// Load current state for version check
	var currentVersion int
	err := tx.QueryRow(`
		SELECT version
		FROM agent_state
		WHERE agent_name = ?
	`, agentName).Scan(&currentVersion)

	if err == sql.ErrNoRows {
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
		result, err = tx.Exec(`
				UPDATE agent_state
				SET last_seen_event_id = MAX(last_seen_event_id, ?),
				    focus_task_id = ?,
				    focus_project_id = ?,
				    last_active_at = CURRENT_TIMESTAMP,
				    version = version + 1
				WHERE agent_name = ? AND version = ?
			`, newCursor, focusTaskID, *focusProjectID, agentName, currentVersion)
	case focusTaskID != "" && focusProjectID != nil:
		result, err = tx.Exec(`
				UPDATE agent_state
				SET last_seen_event_id = MAX(last_seen_event_id, ?),
				    focus_task_id = ?,
				    focus_project_id = NULL,
				    last_active_at = CURRENT_TIMESTAMP,
				    version = version + 1
				WHERE agent_name = ? AND version = ?
			`, newCursor, focusTaskID, agentName, currentVersion)
	case focusTaskID != "":
		result, err = tx.Exec(`
			UPDATE agent_state
			SET last_seen_event_id = MAX(last_seen_event_id, ?),
			    focus_task_id = ?,
			    last_active_at = CURRENT_TIMESTAMP,
			    version = version + 1
			WHERE agent_name = ? AND version = ?
		`, newCursor, focusTaskID, agentName, currentVersion)
	case focusProjectID != nil && *focusProjectID != "":
		result, err = tx.Exec(`
				UPDATE agent_state
				SET last_seen_event_id = MAX(last_seen_event_id, ?),
				    focus_task_id = NULL,
				    focus_project_id = ?,
				    last_active_at = CURRENT_TIMESTAMP,
				    version = version + 1
				WHERE agent_name = ? AND version = ?
			`, newCursor, *focusProjectID, agentName, currentVersion)
	case focusProjectID != nil:
		result, err = tx.Exec(`
				UPDATE agent_state
				SET last_seen_event_id = MAX(last_seen_event_id, ?),
				    focus_task_id = NULL,
				    focus_project_id = NULL,
				    last_active_at = CURRENT_TIMESTAMP,
				    version = version + 1
				WHERE agent_name = ? AND version = ?
			`, newCursor, agentName, currentVersion)
	default:
		result, err = tx.Exec(`
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
