package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/dotcommander/vybe/internal/models"
)

// CheckpointSnapshot captures point-in-time agent state.
type CheckpointSnapshot struct {
	AgentName      string           `json:"agent_name"`
	FocusTaskID    string           `json:"focus_task_id,omitempty"`
	FocusProjectID string           `json:"focus_project_id,omitempty"`
	CursorPosition int64            `json:"cursor_position"`
	TaskCounts     TaskStatusCounts `json:"task_counts"`
}

// CheckpointDiff captures what changed since the last checkpoint.
type CheckpointDiff struct {
	EventsSince      int      `json:"events_since"`
	TasksCreated     int      `json:"tasks_created"`
	TasksCompleted   int      `json:"tasks_completed"`
	TasksBlocked     int      `json:"tasks_blocked"`
	MemoriesChanged  int      `json:"memories_changed"`
	FocusChanged     bool     `json:"focus_changed"`
	OldFocusTaskID   string   `json:"old_focus_task_id,omitempty"`
	NewFocusTaskID   string   `json:"new_focus_task_id,omitempty"`
	ChangedMemoryIDs []string `json:"changed_memory_ids,omitempty"`
}

// CheckpointResult is the combined output of a checkpoint operation.
type CheckpointResult struct {
	EventID  int64              `json:"event_id"`
	Snapshot CheckpointSnapshot `json:"snapshot"`
	Diff     *CheckpointDiff    `json:"diff,omitempty"`
}

// CreateCheckpointEventTx inserts a checkpoint event inside an existing transaction.
func CreateCheckpointEventTx(tx *sql.Tx, agentName, snapshotJSON string) (int64, error) {
	return InsertEventTx(tx, models.EventKindCheckpoint, agentName, "", "Checkpoint snapshot", snapshotJSON)
}

// GetLastCheckpointEvent returns the most recent checkpoint event for an agent.
func GetLastCheckpointEvent(db *sql.DB, agentName, projectID string) (*models.Event, error) {
	var evt models.Event
	var projID, meta, taskID sql.NullString

	err := RetryWithBackoff(func() error {
		where := []string{"kind = ?", "agent_name = ?"}
		args := []interface{}{models.EventKindCheckpoint, agentName}

		if projectID != "" {
			where = append(where, ProjectOrGlobalScopeClause)
			args = append(args, projectID)
		}

		query := "SELECT id, kind, agent_name, project_id, task_id, message, metadata, created_at FROM events WHERE " +
			strings.Join(where, " AND ") +
			" ORDER BY id DESC LIMIT 1"

		return db.QueryRowContext(context.Background(), query, args...).Scan(
			&evt.ID, &evt.Kind, &evt.AgentName, &projID, &taskID, &evt.Message, &meta, &evt.CreatedAt,
		)
	})

	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get last checkpoint event: %w", err)
	}

	if projID.Valid {
		evt.ProjectID = projID.String
	}
	if taskID.Valid {
		evt.TaskID = taskID.String
	}
	evt.Metadata = decodeEventMetadata(meta)

	return &evt, nil
}

// ComputeCheckpointDiff scans events since a cursor to compute what changed.
func ComputeCheckpointDiff(db *sql.DB, agentName string, sinceEventID int64, projectID string) (*CheckpointDiff, error) {
	diff := &CheckpointDiff{}

	events, err := ListEvents(db, ListEventsParams{
		SinceID:   sinceEventID,
		ProjectID: projectID,
		Limit:     1000,
	})
	if err != nil {
		return nil, fmt.Errorf("list events for checkpoint diff: %w", err)
	}

	diff.EventsSince = len(events)
	memoryKeys := make(map[string]struct{})

	for _, e := range events {
		switch e.Kind {
		case models.EventKindTaskCreated:
			diff.TasksCreated++
		case models.EventKindTaskStatus:
			if strings.Contains(e.Message, "completed") {
				diff.TasksCompleted++
			} else if strings.Contains(e.Message, "blocked") {
				diff.TasksBlocked++
			}
		case models.EventKindTaskClosed:
			diff.TasksCompleted++
		case models.EventKindMemoryUpserted:
			// Parse metadata for key
			key := extractMemoryKeyFromEvent(e)
			if key != "" {
				memoryKeys[key] = struct{}{}
			}
		case models.EventKindAgentFocus:
			if e.AgentName == agentName {
				diff.FocusChanged = true
				// Parse task ID from message: "Focus set: <task_id>"
				if strings.HasPrefix(e.Message, "Focus set: ") {
					diff.NewFocusTaskID = strings.TrimPrefix(e.Message, "Focus set: ")
				}
			}
		}
	}

	diff.MemoriesChanged = len(memoryKeys)
	for k := range memoryKeys {
		diff.ChangedMemoryIDs = append(diff.ChangedMemoryIDs, k)
	}

	return diff, nil
}

// extractMemoryKeyFromEvent parses the key from a memory event's metadata.
func extractMemoryKeyFromEvent(e *models.Event) string {
	if len(e.Metadata) == 0 {
		// Fallback: parse from message "Memory upserted: <key>"
		if strings.HasPrefix(e.Message, "Memory upserted: ") {
			return strings.TrimPrefix(e.Message, "Memory upserted: ")
		}
		return ""
	}

	var meta struct {
		Key string `json:"key"`
	}
	if err := json.Unmarshal(e.Metadata, &meta); err == nil && meta.Key != "" {
		return meta.Key
	}

	return ""
}
