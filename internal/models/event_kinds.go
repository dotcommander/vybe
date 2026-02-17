package models

// System event kinds emitted by vybe's store and action layers.
// Agents may also emit freeform kinds (e.g., "progress", "reasoning", "note")
// but these are not defined as constants since they're agent-controlled.
const (
	EventKindTaskCreated           = "task_created"
	EventKindTaskDeleted           = "task_deleted"
	EventKindTaskStatus            = "task_status"
	EventKindTaskHeartbeat         = "task_heartbeat"
	EventKindTaskDependencyAdded   = "task_dependency_added"
	EventKindTaskDependencyRemoved = "task_dependency_removed"
	EventKindProjectCreated        = "project_created"
	EventKindProjectDeleted        = "project_deleted"
	EventKindArtifactAdded         = "artifact_added"
	EventKindAgentFocus            = "agent_focus"
	EventKindAgentProjectFocus     = "agent_project_focus"
	EventKindMemoryUpserted        = "memory_upserted"
	EventKindMemoryReinforced      = "memory_reinforced"
	EventKindMemoryCompacted       = "memory_compacted"
	EventKindMemoryDelete          = "memory_delete"
	EventKindMemoryGC              = "memory_gc"
	EventKindMemoryTouched         = "memory_touched"
	EventKindEventsSummary         = "events_summary"
	EventKindTaskClaimed           = "task_claimed"
	EventKindTaskClosed            = "task_closed"
	EventKindTaskPriorityChanged   = "task_priority_changed"
	EventKindRunCompleted          = "run_completed"
	EventKindCheckpoint            = "checkpoint"
)

// Well-known agent event kinds. These are conventions, not enforced.
// Agents may emit any kind string up to 128 characters.
const (
	EventKindUserPrompt    = "user_prompt"
	EventKindReasoning     = "reasoning"
	EventKindToolFailure   = "tool_failure"
	EventKindToolSuccess   = "tool_success"
	EventKindProgress      = "progress"
	EventKindNote          = "note"
	EventKindTaskCompleted = "task_completed"
	EventKindCommit        = "commit"
	EventKindAgentSpawned   = "agent_spawned"
	EventKindAgentCompleted = "agent_completed"
	EventKindHeartbeat      = "heartbeat"
)
