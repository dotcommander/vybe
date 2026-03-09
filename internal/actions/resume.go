package actions

import (
	"database/sql"
	"errors"
	"fmt"

	"github.com/dotcommander/vybe/internal/models"
	"github.com/dotcommander/vybe/internal/store"
)

// ResumeResponse contains the complete response from a resume operation.
type ResumeResponse struct {
	AgentName      string             `json:"agent_name"`
	OldCursor      int64              `json:"old_cursor"`
	NewCursor      int64              `json:"new_cursor"`
	Deltas         []*models.Event    `json:"deltas"`
	FocusTaskID    string             `json:"focus_task_id"`
	FocusProjectID string             `json:"focus_project_id,omitempty"`
	FocusRule      string             `json:"focus_rule,omitempty"`
	Brief          *store.BriefPacket `json:"brief"`
	Prompt         string             `json:"prompt"`
}

// ResumeOptions controls the behavior of a resume operation.
type ResumeOptions struct {
	EventLimit        int
	ProjectDir        string // When set, scope resume to this project and include recent prompts for it
	FocusTaskOverride string // When set, override focus task atomically within the resume transaction
}

// ResumeWithOptionsIdempotent performs Resume once per (agentName, requestID); replays the original response on retries.
func ResumeWithOptionsIdempotent(db *sql.DB, agentName, requestID string, opts ResumeOptions) (*ResumeResponse, error) {
	if agentName == "" {
		return nil, errors.New("agent name is required")
	}
	if requestID == "" {
		return nil, errors.New("request id is required")
	}
	opts = normalizeResumeOptions(opts)

	pkt, err := computeResumePacket(db, agentName, opts)
	if err != nil {
		return nil, err
	}

	resp := buildResumeResponse(agentName, pkt)
	persisted, err := persistResumeResponse(db, agentName, requestID, opts, resp)
	if err != nil {
		return nil, err
	}

	reconcileResumeContention(db, agentName, pkt, &persisted)
	return &persisted, nil
}

// Brief returns a brief packet for an agent's current focus without advancing cursor.
func Brief(db *sql.DB, agentName string) (*store.BriefPacket, error) {
	if agentName == "" {
		return nil, errors.New("agent name is required")
	}

	state, err := store.LoadOrCreateAgentState(db, agentName)
	if err != nil {
		return nil, fmt.Errorf("failed to load agent state: %w", err)
	}

	brief, err := store.BuildBrief(db, state.FocusTaskID, state.FocusProjectID, agentName)
	if err != nil {
		return nil, fmt.Errorf("failed to build brief: %w", err)
	}

	return brief, nil
}
