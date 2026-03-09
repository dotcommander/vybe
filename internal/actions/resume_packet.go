package actions

import (
	"database/sql"
	"fmt"

	"github.com/dotcommander/vybe/internal/models"
	"github.com/dotcommander/vybe/internal/store"
)

// resumePacket holds pre-computed state for both resume variants.
type resumePacket struct {
	oldCursor      int64
	newCursor      int64
	oldFocusID     string
	focusProjectID string
	focusTaskID    string
	focusRule      string
	deltas         []*models.Event
	brief          *store.BriefPacket
	recentPrompts  []*models.Event
}

type resumeStateSnapshot struct {
	oldCursor      int64
	oldFocusID     string
	focusProjectID string
}

func normalizeResumeOptions(opts ResumeOptions) ResumeOptions {
	if opts.EventLimit <= 0 {
		opts.EventLimit = 1000
	}
	if opts.EventLimit > 1000 {
		opts.EventLimit = 1000
	}
	return opts
}

func loadResumeStateSnapshot(db *sql.DB, agentName string, opts ResumeOptions) (*resumeStateSnapshot, error) {
	state, err := store.LoadOrCreateAgentState(db, agentName)
	if err != nil {
		return nil, fmt.Errorf("failed to load agent state: %w", err)
	}

	focusProjectID := state.FocusProjectID
	if opts.ProjectDir != "" {
		focusProjectID = opts.ProjectDir
	}

	return &resumeStateSnapshot{
		oldCursor:      state.LastSeenEventID,
		oldFocusID:     state.FocusTaskID,
		focusProjectID: focusProjectID,
	}, nil
}

func calculateResumeCursor(oldCursor int64, deltas []*models.Event) int64 {
	newCursor := oldCursor
	for _, event := range deltas {
		if event.ID > newCursor {
			newCursor = event.ID
		}
	}
	return newCursor
}

func fetchResumeDeltas(db *sql.DB, snapshot *resumeStateSnapshot, opts ResumeOptions) ([]*models.Event, int64, error) {
	deltas, err := store.FetchEventsSince(db, snapshot.oldCursor, opts.EventLimit, snapshot.focusProjectID)
	if err != nil {
		return nil, 0, fmt.Errorf("failed to fetch deltas: %w", err)
	}
	return deltas, calculateResumeCursor(snapshot.oldCursor, deltas), nil
}

// computeResumePacket builds a resume response packet from current agent state.
// NOTE: This function reads agent state outside the idempotent transaction.
// A concurrent agent can change focus_project_id between this read and the
// subsequent state update. The response reflects computed state, not necessarily
// the final persisted state.
func computeResumePacket(db *sql.DB, agentName string, opts ResumeOptions) (*resumePacket, error) {
	snapshot, err := loadResumeStateSnapshot(db, agentName, opts)
	if err != nil {
		return nil, err
	}

	deltas, newCursor, err := fetchResumeDeltas(db, snapshot, opts)
	if err != nil {
		return nil, err
	}

	focusResult, err := store.DetermineFocusTask(db, agentName, snapshot.oldFocusID, deltas, snapshot.focusProjectID)
	if err != nil {
		return nil, fmt.Errorf("failed to determine focus task: %w", err)
	}

	brief, err := store.BuildBrief(db, focusResult.TaskID, snapshot.focusProjectID, agentName)
	if err != nil {
		return nil, fmt.Errorf("failed to build brief: %w", err)
	}

	recentPrompts, _ := store.FetchRecentUserPrompts(db, snapshot.focusProjectID, 5) //nolint:errcheck // supplementary context; nil slice is safe

	return &resumePacket{
		oldCursor:      snapshot.oldCursor,
		newCursor:      newCursor,
		oldFocusID:     snapshot.oldFocusID,
		focusProjectID: snapshot.focusProjectID,
		focusTaskID:    focusResult.TaskID,
		focusRule:      focusResult.Rule,
		deltas:         deltas,
		brief:          brief,
		recentPrompts:  recentPrompts,
	}, nil
}

func buildResumeResponse(agentName string, pkt *resumePacket) *ResumeResponse {
	return &ResumeResponse{
		AgentName:      agentName,
		OldCursor:      pkt.oldCursor,
		NewCursor:      pkt.newCursor,
		Deltas:         pkt.deltas,
		FocusTaskID:    pkt.focusTaskID,
		FocusProjectID: pkt.focusProjectID,
		FocusRule:      pkt.focusRule,
		Brief:          pkt.brief,
		Prompt:         buildPrompt(agentName, pkt.brief, pkt.recentPrompts),
	}
}
