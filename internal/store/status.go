package store

import (
	"context"
	"database/sql"
	"fmt"
)

// StatusCounts holds summary counts for all entity types tracked by vybe.
type StatusCounts struct {
	Tasks        TaskStatusCounts `json:"tasks"`
	Events       int              `json:"events"`
	Memory       int              `json:"memory"`
	Agents       int              `json:"agents"`
	Projects     int              `json:"projects"`
	EventsDetail *EventsDetail    `json:"events_detail,omitempty"`
	MemoryDetail *MemoryDetail    `json:"memory_detail,omitempty"`
	AgentsDetail *AgentsDetail    `json:"agents_detail,omitempty"`
	TasksDetail  *TasksDetail     `json:"tasks_detail,omitempty"`
}

// TaskStatusCounts breaks down task counts by status.
type TaskStatusCounts struct {
	Pending    int `json:"pending"`
	InProgress int `json:"in_progress"`
	Completed  int `json:"completed"`
	Blocked    int `json:"blocked"`
}

// EventsDetail breaks down event counts by archive state.
type EventsDetail struct {
	Active   int `json:"active"`
	Archived int `json:"archived"`
}

// MemoryDetail breaks down memory entry counts by health/state.
type MemoryDetail struct {
	Active  int `json:"active"`
	Expired int `json:"expired"`
}

// AgentsDetail provides agent activity counts.
type AgentsDetail struct {
	Active7d int `json:"active_7d"`
}

// TasksDetail provides additional task counts beyond status buckets.
type TasksDetail struct {
	Total   int `json:"total"`
	Unknown int `json:"unknown"`
}

// GetStatusCounts retrieves all status counts in a single atomic query with retry.
func GetStatusCounts(db *sql.DB) (*StatusCounts, error) {
	counts := &StatusCounts{}
	var evActive, evArchived int
	var memActive, memExpired int
	var agActive7d int
	var taskTotal, taskUnknown int

	err := RetryWithBackoff(context.Background(), func() error {
		return db.QueryRowContext(context.Background(), `
			SELECT
				COALESCE((SELECT SUM(CASE WHEN status = 'pending' THEN 1 ELSE 0 END) FROM tasks), 0),
				COALESCE((SELECT SUM(CASE WHEN status = 'in_progress' THEN 1 ELSE 0 END) FROM tasks), 0),
				COALESCE((SELECT SUM(CASE WHEN status = 'completed' THEN 1 ELSE 0 END) FROM tasks), 0),
				COALESCE((SELECT SUM(CASE WHEN status = 'blocked' THEN 1 ELSE 0 END) FROM tasks), 0),
				(SELECT COUNT(*) FROM events),
				(SELECT COUNT(*) FROM memory),
				(SELECT COUNT(*) FROM agent_state),
				(SELECT COUNT(*) FROM projects),
				(SELECT COUNT(*) FROM events WHERE archived_at IS NULL),
				(SELECT COUNT(*) FROM events WHERE archived_at IS NOT NULL),
				(SELECT COUNT(*) FROM memory WHERE expires_at IS NULL OR expires_at > CURRENT_TIMESTAMP),
				(SELECT COUNT(*) FROM memory WHERE expires_at IS NOT NULL AND expires_at <= CURRENT_TIMESTAMP),
				(SELECT COUNT(*) FROM agent_state WHERE last_active_at >= datetime('now', '-7 days')),
				(SELECT COUNT(*) FROM tasks),
				(SELECT COUNT(*) FROM tasks WHERE status NOT IN ('pending', 'in_progress', 'completed', 'blocked'))
		`).Scan(
			&counts.Tasks.Pending,
			&counts.Tasks.InProgress,
			&counts.Tasks.Completed,
			&counts.Tasks.Blocked,
			&counts.Events,
			&counts.Memory,
			&counts.Agents,
			&counts.Projects,
			&evActive,
			&evArchived,
			&memActive,
			&memExpired,
			&agActive7d,
			&taskTotal,
			&taskUnknown,
		)
	})
	if err != nil {
		return nil, fmt.Errorf("failed to get status counts: %w", err)
	}

	counts.EventsDetail = &EventsDetail{Active: evActive, Archived: evArchived}
	counts.MemoryDetail = &MemoryDetail{Active: memActive, Expired: memExpired}
	counts.AgentsDetail = &AgentsDetail{Active7d: agActive7d}
	counts.TasksDetail = &TasksDetail{Total: taskTotal, Unknown: taskUnknown}

	return counts, nil
}
