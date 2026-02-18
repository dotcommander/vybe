package models

import (
	"encoding/json"
	"strings"
	"time"
)

// ID Strategy:
// - Events and Memory use int64 (monotonic ordering, auto-increment)
// - Tasks, Projects, Artifacts use string (distributed generation, e.g., "task_1234567890_a3f9")
//
// This mixed strategy optimizes for different use cases:
// - Append-only logs benefit from sequential IDs (efficient indexing)
// - Distributed task creation benefits from collision-free string IDs

// TaskStatus represents the current state of a task.
type TaskStatus string

// Task status constants.
const (
	TaskStatusPending    TaskStatus = "pending"
	TaskStatusInProgress TaskStatus = "in_progress"
	TaskStatusCompleted  TaskStatus = "completed"
	TaskStatusBlocked    TaskStatus = "blocked"
)

// IsTerminal returns true if the task is in a completed state.
func (s TaskStatus) IsTerminal() bool {
	return s == TaskStatusCompleted
}

// IsPending returns true if the task is pending execution.
func (s TaskStatus) IsPending() bool {
	return s == TaskStatusPending
}

// MemoryScope represents the visibility scope of a memory entry.
type MemoryScope string

// Memory scope constants.
const (
	MemoryScopeGlobal  MemoryScope = "global"
	MemoryScopeProject MemoryScope = "project"
	MemoryScopeTask    MemoryScope = "task"
	MemoryScopeAgent   MemoryScope = "agent"
)

// Event represents a single event in the continuity log
type Event struct {
	ID int64 `json:"id"`
	// Kind is one of the EventKind* constants defined in event_kinds.go.
	// System events use predefined constants; agents may emit custom kinds up to 128 chars.
	Kind      string          `json:"kind"`
	AgentName string          `json:"agent_name"`
	ProjectID string          `json:"project_id,omitempty"`
	TaskID    string          `json:"task_id"`
	Message   string          `json:"message"`
	Metadata  json.RawMessage `json:"metadata"`
	CreatedAt time.Time       `json:"created_at"`
}

// BlockedReason represents why a task is blocked.
type BlockedReason string

// BlockedReasonDependency is set when a task is blocked because an unresolved
// dependency exists. Resume Rule 1.5 keeps dependency-blocked tasks in focus.
const BlockedReasonDependency BlockedReason = "dependency"

// BlockedReasonFailurePrefix is prepended to a freeform reason string when a
// task is blocked due to an execution failure (e.g., "failure:build error").
// Resume Rule 1.5 skips failure-blocked tasks and falls through to find new work.
const BlockedReasonFailurePrefix = "failure:"

// IsFailure returns true if the blocked reason indicates an execution failure.
func (br BlockedReason) IsFailure() bool {
	return strings.HasPrefix(string(br), BlockedReasonFailurePrefix)
}

// GetFailureReason extracts the failure message from a failure-type blocked reason.
// Returns empty string if not a failure reason.
func (br BlockedReason) GetFailureReason() string {
	if !br.IsFailure() {
		return ""
	}
	return strings.TrimPrefix(string(br), BlockedReasonFailurePrefix)
}


// Task represents a task in the system
type Task struct {
	ID              string        `json:"id"`
	Title           string        `json:"title"`
	Description     string        `json:"description"`
	Status          TaskStatus    `json:"status"`
	Priority        int           `json:"priority"`
	ProjectID       string        `json:"project_id,omitempty"`
	BlockedReason   BlockedReason `json:"blocked_reason,omitempty"`
	ClaimedBy       string        `json:"claimed_by,omitempty"`
	ClaimedAt       *time.Time    `json:"claimed_at,omitempty"`
	ClaimExpiresAt  *time.Time    `json:"claim_expires_at,omitempty"`
	LastHeartbeatAt *time.Time    `json:"last_heartbeat_at,omitempty"`
	Attempt         int           `json:"attempt"`
	DependsOn       []string      `json:"depends_on,omitempty"`
	Version         int           `json:"version"`
	CreatedAt       time.Time     `json:"created_at"`
	UpdatedAt       time.Time     `json:"updated_at"`
}

// IsClaimed returns true if the task has been claimed by an agent.
func (t *Task) IsClaimed() bool {
	return t.ClaimedBy != ""
}

// IsBlocked returns true if the task status is blocked.
func (t *Task) IsBlocked() bool {
	return t.Status == TaskStatusBlocked
}

// IsBlockedByDependency returns true if the task is blocked due to an unresolved dependency.
func (t *Task) IsBlockedByDependency() bool {
	return t.BlockedReason == BlockedReasonDependency
}

// IsBlockedByFailure returns true if the task is blocked due to an execution failure.
func (t *Task) IsBlockedByFailure() bool {
	return t.BlockedReason.IsFailure()
}

// HasClaimedAt returns true if the ClaimedAt timestamp is set.
func (t *Task) HasClaimedAt() bool {
	return t.ClaimedAt != nil
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
	ID            int64       `json:"id"`
	Key           string      `json:"key"`
	Canonical     string      `json:"canonical_key,omitempty"`
	Value         string      `json:"value"`
	ValueType     string      `json:"value_type"`
	Scope         MemoryScope `json:"scope"`
	ScopeID       string      `json:"scope_id"`
	Confidence    float64     `json:"confidence,omitempty"`
	LastSeenAt    *time.Time  `json:"last_seen_at,omitempty"`
	SourceEventID *int64      `json:"source_event_id,omitempty"`
	SupersededBy  string      `json:"superseded_by,omitempty"`
	ExpiresAt     *time.Time  `json:"expires_at,omitempty"`
	CreatedAt     time.Time   `json:"created_at"`
}

// IsExpired returns true if the memory has an expiration time and it has passed.
func (m *Memory) IsExpired(now time.Time) bool {
	return m.ExpiresAt != nil && m.ExpiresAt.Before(now)
}

// IsSuperseded returns true if this memory entry has been superseded by another.
func (m *Memory) IsSuperseded() bool {
	return m.SupersededBy != ""
}

// IsGlobalScope returns true if the memory has global visibility.
func (m *Memory) IsGlobalScope() bool {
	return m.Scope == MemoryScopeGlobal
}

// IsProjectScope returns true if the memory is scoped to a specific project.
func (m *Memory) IsProjectScope() bool {
	return m.Scope == MemoryScopeProject
}

// IsTaskScope returns true if the memory is scoped to a specific task.
func (m *Memory) IsTaskScope() bool {
	return m.Scope == MemoryScopeTask
}

// IsAgentScope returns true if the memory is scoped to a specific agent.
func (m *Memory) IsAgentScope() bool {
	return m.Scope == MemoryScopeAgent
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
