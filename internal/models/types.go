package models

import (
	"encoding/json"
	"time"
)

// Event represents a single event in the continuity log
type Event struct {
	ID        int64           `json:"id"`
	Kind      string          `json:"kind"`
	AgentName string          `json:"agent_name"`
	ProjectID string          `json:"project_id,omitempty"`
	TaskID    string          `json:"task_id"`
	Message   string          `json:"message"`
	Metadata  json.RawMessage `json:"metadata"`
	CreatedAt time.Time       `json:"created_at"`
}

// BlockedReasonDependency is set when a task is blocked because an unresolved
// dependency exists. Resume Rule 1.5 keeps dependency-blocked tasks in focus.
const BlockedReasonDependency = "dependency"

// BlockedReasonFailurePrefix is prepended to a freeform reason string when a
// task is blocked due to an execution failure (e.g., "failure:build error").
// Resume Rule 1.5 skips failure-blocked tasks and falls through to find new work.
const BlockedReasonFailurePrefix = "failure:"

// Task represents a task in the system
type Task struct {
	ID              string     `json:"id"`
	Title           string     `json:"title"`
	Description     string     `json:"description"`
	Status          string     `json:"status"`
	Priority        int        `json:"priority"`
	ProjectID       string     `json:"project_id,omitempty"`
	BlockedReason   string     `json:"blocked_reason,omitempty"`
	ClaimedBy       string     `json:"claimed_by,omitempty"`
	ClaimedAt       *time.Time `json:"claimed_at,omitempty"`
	ClaimExpiresAt  *time.Time `json:"claim_expires_at,omitempty"`
	LastHeartbeatAt *time.Time `json:"last_heartbeat_at,omitempty"`
	Attempt         int        `json:"attempt"`
	DependsOn       []string   `json:"depends_on,omitempty"`
	Version         int        `json:"version"`
	CreatedAt       time.Time  `json:"created_at"`
	UpdatedAt       time.Time  `json:"updated_at"`
}

// AgentState tracks the last known state for an agent
type AgentState struct {
	AgentName       string    `json:"agent_name"`
	LastSeenEventID int64     `json:"last_seen_event_id"`
	FocusTaskID     string    `json:"focus_task_id"`
	FocusProjectID  string    `json:"focus_project_id,omitempty"`
	Version         int       `json:"version"`
	LastActiveAt    time.Time `json:"last_active_at"`
}

// Memory represents a key-value storage entry with scoping
type Memory struct {
	ID            int64      `json:"id"`
	Key           string     `json:"key"`
	Canonical     string     `json:"canonical_key,omitempty"`
	Value         string     `json:"value"`
	ValueType     string     `json:"value_type"`
	Scope         string     `json:"scope"`
	ScopeID       string     `json:"scope_id"`
	Confidence    float64    `json:"confidence,omitempty"`
	LastSeenAt    *time.Time `json:"last_seen_at,omitempty"`
	SourceEventID *int64     `json:"source_event_id,omitempty"`
	SupersededBy  string     `json:"superseded_by,omitempty"`
	ExpiresAt     *time.Time `json:"expires_at,omitempty"`
	CreatedAt     time.Time  `json:"created_at"`
}

// Artifact represents a file or output artifact
type Artifact struct {
	ID          string    `json:"id"`
	TaskID      string    `json:"task_id"`
	EventID     int64     `json:"event_id"`
	FilePath    string    `json:"file_path"`
	ContentType string    `json:"content_type"`
	CreatedAt   time.Time `json:"created_at"`
}

// Project represents a project in the system
type Project struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Metadata  string    `json:"metadata"` // JSON string
	CreatedAt time.Time `json:"created_at"`
}
