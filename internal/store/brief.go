package store

import (
	"database/sql"
	"fmt"
	"time"

	"github.com/dotcommander/vybe/internal/models"
)

const andProjectIDFilter = " AND project_id = ?"

// scopedQuery appends "WHERE project_id = ?" to base when projectID is non-empty,
// returning the final SQL and its args. base must not already end with a WHERE clause.
func scopedQuery(base, projectID string) (string, []any) {
	if projectID == "" {
		return base, nil
	}
	return base + " WHERE project_id = ?", []any{projectID}
}

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
func BuildBrief(db *sql.DB, focusTaskID, focusProjectID, agentName string, asOf time.Time) (*BriefPacket, error) {
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
		memory, err := fetchRelevantMemory(db, "", focusProjectID, asOf)
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

	memory, err := fetchRelevantMemory(db, focusTaskID, focusProjectID, asOf)
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
