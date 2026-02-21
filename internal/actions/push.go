package actions

import (
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/dotcommander/vybe/internal/store"
)

// PushEventInput describes the optional event to insert.
type PushEventInput struct {
	Kind     string `json:"kind"`
	Message  string `json:"message"`
	Metadata string `json:"metadata,omitempty"`
}

// PushMemoryInput describes one memory upsert.
type PushMemoryInput struct {
	Key        string     `json:"key"`
	Value      string     `json:"value"`
	ValueType  string     `json:"value_type,omitempty"`
	Scope      string     `json:"scope"`
	ScopeID    string     `json:"scope_id,omitempty"`
	ExpiresAt  *time.Time `json:"expires_at,omitempty"`
	Confidence *float64   `json:"confidence,omitempty"`
}

// PushArtifactInput describes one artifact to link.
type PushArtifactInput struct {
	FilePath    string `json:"file_path"`
	ContentType string `json:"content_type,omitempty"`
}

// PushTaskStatusInput describes the optional task status change.
type PushTaskStatusInput struct {
	Status        string `json:"status"`
	Summary       string `json:"summary,omitempty"`
	Label         string `json:"label,omitempty"`
	BlockedReason string `json:"blocked_reason,omitempty"`
}

// PushInput holds all the optional sub-operations for a push.
type PushInput struct {
	TaskID     string               `json:"task_id,omitempty"`
	Event      *PushEventInput      `json:"event,omitempty"`
	Memories   []PushMemoryInput    `json:"memories,omitempty"`
	Artifacts  []PushArtifactInput  `json:"artifacts,omitempty"`
	TaskStatus *PushTaskStatusInput `json:"task_status,omitempty"`
}

// PushMemoryResult holds the result of a single memory upsert within push.
type PushMemoryResult struct {
	Key          string  `json:"key"`
	CanonicalKey string  `json:"canonical_key"`
	Reinforced   bool    `json:"reinforced"`
	Confidence   float64 `json:"confidence"`
	EventID      int64   `json:"event_id"`
}

// PushArtifactResult holds the result of a single artifact add within push.
type PushArtifactResult struct {
	ArtifactID string `json:"artifact_id"`
	FilePath   string `json:"file_path"`
	EventID    int64  `json:"event_id"`
}

// PushTaskResult holds the task after status change.
type PushTaskResult struct {
	TaskID        string `json:"task_id"`
	Status        string `json:"status"`
	StatusEventID int64  `json:"status_event_id"`
	CloseEventID  int64  `json:"close_event_id,omitempty"`
}

// PushResult is the full response from a push operation.
type PushResult struct {
	EventID    int64                `json:"event_id,omitempty"`
	Memories   []PushMemoryResult   `json:"memories,omitempty"`
	Artifacts  []PushArtifactResult `json:"artifacts,omitempty"`
	TaskStatus *PushTaskResult      `json:"task_status,omitempty"`
}

// PushIdempotent executes all sub-operations in a single idempotent transaction.
// Uses RunIdempotentWithRetry with maxAttempts=3 to handle CAS version conflicts on task status.
//
//nolint:gocognit,gocyclo,funlen // batch orchestration requires sequential sub-operations in one tx
func PushIdempotent(db *sql.DB, agentName, requestID string, input PushInput) (*PushResult, error) {
	if agentName == "" {
		return nil, errors.New("agent name is required")
	}
	if requestID == "" {
		return nil, errors.New("request id is required")
	}

	// Validate: artifacts and task_status require task_id
	if len(input.Artifacts) > 0 && input.TaskID == "" {
		return nil, errors.New("task_id is required when artifacts are provided")
	}
	if input.TaskStatus != nil && input.TaskID == "" {
		return nil, errors.New("task_id is required when task_status is provided")
	}

	// Validate task_status if present
	if input.TaskStatus != nil {
		if err := validateTaskStatus(input.TaskStatus.Status); err != nil {
			return nil, fmt.Errorf("invalid task_status: %w", err)
		}
	}

	// Must have at least one operation
	if input.Event == nil && len(input.Memories) == 0 && len(input.Artifacts) == 0 && input.TaskStatus == nil {
		return nil, errors.New("push requires at least one operation (event, memories, artifacts, or task_status)")
	}

	r, _, err := store.RunIdempotentWithRetry(
		db, agentName, requestID, "push",
		3,
		func(err error) bool { return errors.Is(err, store.ErrVersionConflict) },
		func(tx *sql.Tx) (PushResult, error) {
			var result PushResult

			// 1. Insert event (if provided)
			if input.Event != nil {
				eventID, err := store.InsertEventTx(tx, input.Event.Kind, agentName, input.TaskID, input.Event.Message, input.Event.Metadata)
				if err != nil {
					return PushResult{}, fmt.Errorf("failed to insert event: %w", err)
				}
				result.EventID = eventID
			}

			// 2. Memory upserts
			if len(input.Memories) > 0 {
				result.Memories = make([]PushMemoryResult, 0, len(input.Memories))
				for _, mem := range input.Memories {
					var sourceEventID *int64
					if result.EventID != 0 {
						eid := result.EventID
						sourceEventID = &eid
					}
					eventID, reinforced, confidence, canonicalKey, err := store.UpsertMemoryTx(
						tx, agentName, mem.Key, mem.Value, mem.ValueType, mem.Scope, mem.ScopeID,
						mem.ExpiresAt, mem.Confidence, sourceEventID,
					)
					if err != nil {
						return PushResult{}, fmt.Errorf("failed to upsert memory %q: %w", mem.Key, err)
					}
					result.Memories = append(result.Memories, PushMemoryResult{
						Key:          mem.Key,
						CanonicalKey: canonicalKey,
						Reinforced:   reinforced,
						Confidence:   confidence,
						EventID:      eventID,
					})
				}
			}

			// 3. Artifact adds
			if len(input.Artifacts) > 0 {
				result.Artifacts = make([]PushArtifactResult, 0, len(input.Artifacts))
				for _, art := range input.Artifacts {
					artifactID, eventID, err := store.AddArtifactTx(tx, agentName, input.TaskID, art.FilePath, art.ContentType)
					if err != nil {
						return PushResult{}, fmt.Errorf("failed to add artifact %q: %w", art.FilePath, err)
					}
					result.Artifacts = append(result.Artifacts, PushArtifactResult{
						ArtifactID: artifactID,
						FilePath:   art.FilePath,
						EventID:    eventID,
					})
				}
			}

			// 4. Task status change
			if input.TaskStatus != nil {
				ts := input.TaskStatus
				status := ts.Status

				if status == completedStatus || status == blockedStatus {
					// Use CloseTaskTx for terminal states
					if ts.Summary == "" {
						ts.Summary = fmt.Sprintf("Task %s via push", status)
					}
					closeResult, err := store.CloseTaskTx(tx, store.CloseTaskParams{
						AgentName:     agentName,
						TaskID:        input.TaskID,
						Status:        status,
						Summary:       ts.Summary,
						Label:         ts.Label,
						BlockedReason: ts.BlockedReason,
					})
					if err != nil {
						return PushResult{}, fmt.Errorf("failed to close task: %w", err)
					}
					result.TaskStatus = &PushTaskResult{
						TaskID:        input.TaskID,
						Status:        status,
						StatusEventID: closeResult.StatusEventID,
						CloseEventID:  closeResult.CloseEventID,
					}
				} else {
					// Non-terminal: simple status update via CAS
					version, err := store.GetTaskVersionTx(tx, input.TaskID)
					if err != nil {
						return PushResult{}, fmt.Errorf("failed to get task version: %w", err)
					}
					statusEventID, err := store.UpdateTaskStatusWithEventTx(tx, agentName, input.TaskID, status, version)
					if err != nil {
						return PushResult{}, fmt.Errorf("failed to update task status: %w", err)
					}
					result.TaskStatus = &PushTaskResult{
						TaskID:        input.TaskID,
						Status:        status,
						StatusEventID: statusEventID,
					}
				}
			}

			return result, nil
		},
	)
	if err != nil {
		return nil, err
	}

	return &r, nil
}
