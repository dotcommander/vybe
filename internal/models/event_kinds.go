package models

// System event kinds emitted by vybe's store and action layers.
const (
	EventKindTaskCreated       = "task_created"
	EventKindTaskDeleted       = "task_deleted"
	EventKindTaskStatus        = "task_status"
	EventKindProjectCreated    = "project_created"
	EventKindProjectDeleted    = "project_deleted"
	EventKindArtifactAdded     = "artifact_added"
	EventKindAgentFocus        = "agent_focus"
	EventKindAgentProjectFocus = "agent_project_focus"
	EventKindMemoryUpserted    = "memory_upserted"
	EventKindMemoryConflict    = "memory_conflict"
	EventKindMemoryDelete      = "memory_delete"
	EventKindMemoryGC          = "memory_gc"
	EventKindMemoryPin         = "memory_pin"
	EventKindEventsSummary     = "events_summary"
	EventKindTaskClosed        = "task_closed"
	EventKindRunCompleted      = "run_completed"
	EventKindCheckpoint        = "checkpoint"
)

// Agent event kinds with system significance.
// These are emitted by agents but are also filtered or queried by system logic
// (resume.go FetchSessionEvents, FetchRecentUserPrompts, FetchPriorReasoning).
const (
	EventKindUserPrompt  = "user_prompt"
	EventKindReasoning   = "reasoning"
	EventKindToolFailure = "tool_failure"
	EventKindProgress    = "progress"
)
