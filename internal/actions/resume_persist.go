package actions

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"

	"github.com/dotcommander/vybe/internal/models"
	"github.com/dotcommander/vybe/internal/store"
)

func applyResumeFocusOverride(tx *sql.Tx, agentName string, opts ResumeOptions, resp ResumeResponse) (ResumeResponse, error) {
	if opts.FocusTaskOverride == "" {
		return resp, nil
	}

	resp.FocusTaskID = opts.FocusTaskOverride
	if _, err := store.InsertEventTx(tx, models.EventKindAgentFocus, agentName, opts.FocusTaskOverride, fmt.Sprintf("Focus set: %s", opts.FocusTaskOverride), ""); err != nil {
		return ResumeResponse{}, fmt.Errorf("failed to emit focus event: %w", err)
	}

	return resp, nil
}

func updateResumeAgentState(tx *sql.Tx, agentName string, opts ResumeOptions, resp ResumeResponse) error {
	if opts.ProjectDir != "" {
		return store.UpdateAgentStateAtomicWithProjectTx(tx, agentName, resp.NewCursor, resp.FocusTaskID, resp.FocusProjectID)
	}
	return store.UpdateAgentStateAtomicTx(tx, agentName, resp.NewCursor, resp.FocusTaskID)
}

func loadAuthoritativeResumeState(tx *sql.Tx, agentName string, resp ResumeResponse) (ResumeResponse, error) {
	cursorFocus, err := store.LoadAgentCursorAndFocusTx(tx, agentName)
	if err != nil {
		return ResumeResponse{}, err
	}

	resp.NewCursor = cursorFocus.Cursor
	resp.FocusTaskID = cursorFocus.TaskID
	resp.FocusProjectID = cursorFocus.ProjectID
	return resp, nil
}

func persistResumeResponse(db *sql.DB, agentName, requestID string, opts ResumeOptions, resp *ResumeResponse) (ResumeResponse, error) {
	persisted, _, err := store.RunIdempotentWithRetry(
		context.Background(),
		db,
		agentName,
		requestID,
		"resume",
		5,
		retryOnResumeConflict,
		func(tx *sql.Tx) (ResumeResponse, error) {
			applied := *resp

			appliedResp, err := applyResumeFocusOverride(tx, agentName, opts, applied)
			if err != nil {
				return ResumeResponse{}, err
			}
			applied = appliedResp

			if err := updateResumeAgentState(tx, agentName, opts, applied); err != nil {
				return ResumeResponse{}, err
			}

			return loadAuthoritativeResumeState(tx, agentName, applied)
		},
	)
	if err != nil {
		return ResumeResponse{}, err
	}

	return persisted, nil
}

func resumeStateChanged(pkt *resumePacket, resp ResumeResponse) bool {
	return resp.FocusTaskID != pkt.focusTaskID || resp.NewCursor != pkt.newCursor || resp.FocusProjectID != pkt.focusProjectID
}

func reconcileResumeContention(db *sql.DB, agentName string, pkt *resumePacket, resp *ResumeResponse) {
	if !resumeStateChanged(pkt, *resp) {
		return
	}

	newBrief, err := store.BuildBrief(db, resp.FocusTaskID, resp.FocusProjectID, agentName)
	if err != nil {
		slog.Default().Warn("failed to rebuild brief after contention", "error", err)
		resp.Brief = &store.BriefPacket{}
	} else {
		resp.Brief = newBrief
	}
	resp.Prompt = buildPrompt(agentName, resp.Brief, pkt.recentPrompts)
}
