package models

// System event kinds emitted by vybe's store and action layers.
const (
	EventKindTaskCreated           = "task_created"
	EventKindTaskDeleted           = "task_deleted"
	EventKindTaskStatus            = "task_status"
	EventKindTaskDependencyAdded   = "task_dependency_added"
	EventKindTaskDependencyRemoved = "task_dependency_removed"
	EventKindProjectCreated        = "project_created"
	EventKindProjectDeleted        = "project_deleted"
	EventKindArtifactAdded         = "artifact_added"
	EventKindAgentFocus            = "agent_focus"
	EventKindAgentProjectFocus     = "agent_project_focus"
	EventKindMemoryUpserted        = "memory_upserted"
	EventKindMemoryDelete          = "memory_delete"
	EventKindMemoryGC              = "memory_gc"
	EventKindEventsSummary         = "events_summary"
	EventKindTaskClosed            = "task_closed"
	EventKindTaskPriorityChanged   = "task_priority_changed"
	EventKindRunCompleted          = "run_completed"
	EventKindCheckpoint            = "checkpoint"
)

// Agent event kinds with system significance.
// These are emitted by agents but are also filtered or queried by system logic
// (resume.go FetchSessionEvents, FetchRecentUserPrompts, FetchPriorReasoning;
// session.go extractRuleBasedLessons).
const (
	EventKindUserPrompt  = "user_prompt"
	EventKindReasoning   = "reasoning"
	EventKindToolFailure = "tool_failure"
	EventKindProgress    = "progress"
)

// Agent convention kinds — purely labels used at insertion time (e.g., hook.go).
// These are never filtered or queried by system logic.
// Agents may emit any kind string up to 128 characters; these constants exist
// only to avoid typos at call sites.
const (
	EventKindToolSuccess    = "tool_success"
	EventKindNote           = "note"
	EventKindTaskCompleted  = "task_completed"
	EventKindCommit         = "commit"
	EventKindAgentSpawned   = "agent_spawned"
	EventKindAgentCompleted = "agent_completed"
	EventKindHeartbeat      = "heartbeat"
)
