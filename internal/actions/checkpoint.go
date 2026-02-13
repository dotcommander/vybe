package actions

import (
	"database/sql"
	"encoding/json"
	"fmt"

	"github.com/dotcommander/vybe/internal/store"
)

// CheckpointIdempotent captures a point-in-time snapshot, computes diff against last checkpoint,
// and persists the result as a checkpoint event.
func CheckpointIdempotent(db *sql.DB, agentName, requestID, projectID string) (*store.CheckpointResult, error) {
	if agentName == "" {
		return nil, fmt.Errorf("agent name is required")
	}
	if requestID == "" {
		return nil, fmt.Errorf("request id is required")
	}

	// 1. Load agent state
	state, err := store.LoadOrCreateAgentState(db, agentName)
	if err != nil {
		return nil, fmt.Errorf("load agent state: %w", err)
	}

	// 2. Get task counts
	taskCounts, err := store.GetTaskStatusCounts(db, projectID)
	if err != nil {
		return nil, fmt.Errorf("get task status counts: %w", err)
	}

	// 3. Build snapshot
	snapshot := store.CheckpointSnapshot{
		AgentName:      agentName,
		FocusTaskID:    state.FocusTaskID,
		FocusProjectID: state.FocusProjectID,
		CursorPosition: state.LastSeenEventID,
		TaskCounts:     *taskCounts,
	}

	// 4. Find last checkpoint and compute diff
	var diff *store.CheckpointDiff
	lastCP, err := store.GetLastCheckpointEvent(db, agentName, projectID)
	if err != nil {
		return nil, fmt.Errorf("get last checkpoint: %w", err)
	}

	if lastCP != nil {
		// Parse the old snapshot from metadata to get old focus task
		var oldSnapshot store.CheckpointSnapshot
		if len(lastCP.Metadata) > 0 {
			_ = json.Unmarshal(lastCP.Metadata, &oldSnapshot)
		}

		diff, err = store.ComputeCheckpointDiff(db, agentName, lastCP.ID, projectID)
		if err != nil {
			return nil, fmt.Errorf("compute checkpoint diff: %w", err)
		}

		// Enrich diff with focus change info
		if oldSnapshot.FocusTaskID != snapshot.FocusTaskID {
			diff.FocusChanged = true
			diff.OldFocusTaskID = oldSnapshot.FocusTaskID
			diff.NewFocusTaskID = snapshot.FocusTaskID
		}
	}

	// 5. Persist via RunIdempotent
	snapshotJSON, err := json.Marshal(snapshot)
	if err != nil {
		return nil, fmt.Errorf("marshal checkpoint snapshot: %w", err)
	}

	type idemResult struct {
		EventID int64 `json:"event_id"`
	}

	r, err := store.RunIdempotent(db, agentName, requestID, "checkpoint.create", func(tx *sql.Tx) (idemResult, error) {
		eventID, err := store.CreateCheckpointEventTx(tx, agentName, string(snapshotJSON))
		if err != nil {
			return idemResult{}, err
		}
		return idemResult{EventID: eventID}, nil
	})
	if err != nil {
		return nil, fmt.Errorf("persist checkpoint: %w", err)
	}

	return &store.CheckpointResult{
		EventID:  r.EventID,
		Snapshot: snapshot,
		Diff:     diff,
	}, nil
}
