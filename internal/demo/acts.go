package demo

// DemoContext holds shared state passed between steps.
type DemoContext struct {
	ProjectID    string
	AuthTaskID   string
	TestsTaskID  string
	DeployTaskID string
	SessionID    string
	SessionID2   string
	TempDir      string
}

// StepFunc is a function that runs a single demo step.
type StepFunc func(r *Runner, ctx *DemoContext) error

// Step represents a single named step within an act.
type Step struct {
	Name    string
	Fn      StepFunc
	Insight string
}

// Act represents a named act with narration and steps.
type Act struct {
	Number    int
	Name      string
	Narration []string
	Steps     []Step
}

// BuildActs returns all 17 acts with their steps.
func BuildActs() []Act {
	return []Act{
		{
			Number: 1,
			Name:   "Building The World",
			Narration: []string{
				"Setting up the world an agent operates in.",
				"DB init, project creation, task graph with dependencies, memory at multiple scopes.",
				"Vybe is the durable backbone — everything an agent knows or intends lives here.",
			},
			Steps: []Step{
				{Name: "upgrade_database", Fn: stepUpgradeDatabase, Insight: "Your agent's first command in any environment. Creates all tables — ready to remember everything."},
				{Name: "create_project", Fn: stepCreateProject, Insight: "Projects scope work. Your agent groups related tasks and memory under one project."},
				{Name: "create_task_graph", Fn: stepCreateTaskGraph, Insight: "Three real tasks, just like your agent would create when breaking down a feature request."},
				{Name: "set_dependencies", Fn: stepSetDependencies, Insight: "'Write tests' can't start until 'Implement auth' is done. Your agent's task queue respects this automatically."},
				{Name: "store_global_memory", Fn: stepStoreGlobalMemory, Insight: "Global memory is visible to every agent, every session. Environment facts like Go version live here."},
				{Name: "store_project_memory", Fn: stepStoreProjectMemory, Insight: "Project memory is shared across agents on the same project but invisible to other projects."},
			},
		},
		{
			Number: 2,
			Name:   "The Agent Works",
			Narration: []string{
				"Simulating what happens when Claude Code starts a new session.",
				"Hooks fire automatically: session-start loads context, tool calls are logged,",
				"the agent claims work, logs discoveries, links artifacts, and marks tasks complete.",
			},
			Steps: []Step{
				{Name: "session_start_hook", Fn: stepSessionStartHook, Insight: "Claude Code hook: SessionStart. Fires automatically when a session opens — your agent gets context injected before it even asks."},
				{Name: "resume", Fn: stepResume, Insight: "The agent cursor advances and the brief packet loads: focus task, memory, recent events, artifacts. Everything needed to start working."},
				{Name: "prompt_logging", Fn: stepPromptLogging, Insight: "Claude Code hook: UserPromptSubmit. Every prompt you type is logged — future sessions see the full conversation trail."},
				{Name: "claim_focus_task", Fn: stepClaimFocusTask, Insight: "Your agent claims work before starting. No other agent can grab this task now."},
				{Name: "tool_success_tracking", Fn: stepToolSuccessTracking, Insight: "Claude Code hook: PostToolUse. Every successful tool call is logged — builds a complete execution history."},
				{Name: "tool_failure_tracking", Fn: stepToolFailureTracking, Insight: "Claude Code hook: PostToolUseFailure. When tools fail, the next session sees exactly what broke and where."},
				{Name: "log_progress_events", Fn: stepLogProgressEvents, Insight: "Your agent narrates its own work. Progress events are the journal that survives crashes."},
				{Name: "store_task_memory", Fn: stepStoreTaskMemory, Insight: "Task-scoped memory. A new agent picking up this task will know 'JWT' was the chosen auth strategy."},
				{Name: "link_artifact", Fn: stepLinkArtifact, Insight: "Output files linked to tasks. New sessions find what was built immediately — no archaeology."},
				{Name: "complete_task", Fn: stepCompleteTask, Insight: "Task done. Next resume auto-advances to the next task in the queue."},
				{Name: "task_completion_hook", Fn: stepTaskCompletionHook, Insight: "Claude Code hook: TaskCompleted. The IDE signals vybe so the event stream reflects IDE-level milestones."},
			},
		},
		{
			Number: 3,
			Name:   "The Agent Sleeps",
			Narration: []string{
				"Graceful shutdown. Agents crash; vybe persists.",
				"PreCompact compresses the memory space. SessionEnd closes out the session.",
			},
			Steps: []Step{
				{Name: "memory_checkpoint", Fn: stepMemoryCheckpoint, Insight: "Claude Code hook: PreCompact. Before context compression, vybe prunes stale memory entries."},
				{Name: "session_end", Fn: stepSessionEnd, Insight: "Claude Code hook: SessionEnd. Session over — everything is durable in SQLite, ready for the next agent."},
			},
		},
		{
			Number: 4,
			Name:   "The Agent Returns",
			Narration: []string{
				"A new session starts. The previous agent crashed (or the session ended).",
				"Can the new agent pick up exactly where the old one left off?",
				"THIS is the wow moment. This is why vybe exists.",
			},
			Steps: []Step{
				{Name: "new_session_start", Fn: stepNewSessionStart, Insight: "New session, fresh context window, zero memory of Act II. Watch vybe restore everything."},
				{Name: "cross_session_continuity", Fn: stepCrossSessionContinuity, Insight: "Artifacts, global memory, project memory — all survived the session boundary. This is why vybe exists."},
				{Name: "complete_deploy_task", Fn: stepCompleteDeployTask, Insight: "A completely new session picks up 'Deploy', works it, completes it. No handoff notes needed."},
				{Name: "resume_with_blocked_task", Fn: stepResumeWithBlockedTask, Insight: "Only 'Write tests' remains. The resume algorithm knows it's blocked and handles it."},
			},
		},
		{
			Number: 5,
			Name:   "The Queue Moves",
			Narration: []string{
				"Dependency-driven task flow. Removing blockers, observing focus auto-advance,",
				"completing the remaining work, and confirming the queue is empty.",
				"This closes the loop: every task created in Act I is now done.",
			},
			Steps: []Step{
				{Name: "remove_dependency", Fn: stepRemoveDependency, Insight: "Dependency removed, task unblocked. Your agent's queue adapts in real-time."},
				{Name: "resume_selects_unblocked", Fn: stepResumeSelectsUnblocked, Insight: "Resume picks 'Write tests' — the only remaining unblocked task. Priority order is automatic."},
				{Name: "complete_final_task", Fn: stepCompleteFinalTask, Insight: "Last task done. Your agent completed all three tasks across two sessions with zero data loss."},
				{Name: "empty_queue", Fn: stepEmptyQueue, Insight: "Resume returns null — the queue is empty, your agent's work here is genuinely done."},
			},
		},
		{
			Number: 6,
			Name:   "Auditing The Record",
			Narration: []string{
				"Auditing the event stream. Everything vybe recorded is queryable.",
				"Events, memories (all scopes), artifacts, snapshots, and system health.",
			},
			Steps: []Step{
				{Name: "query_event_stream", Fn: stepQueryEventStream, Insight: "The complete activity log — every tool call, every progress note, every prompt. Fully queryable by kind."},
				{Name: "query_all_memory_scopes", Fn: stepQueryAllMemoryScopes, Insight: "Three memory scopes: global (environment), project (team knowledge), task (work-specific decisions)."},
				{Name: "query_artifacts", Fn: stepQueryArtifacts, Insight: "Files from Act II are still here in Act VI. Artifacts persist across all sessions."},
				{Name: "capture_snapshot", Fn: stepCaptureSnapshot, Insight: "Point-in-time snapshot of the entire system. Useful for diffing state before/after operations."},
				{Name: "health_check", Fn: stepHealthCheck, Insight: "One command confirms the database is healthy and responsive. Agents call this before critical work."},
			},
		},
		{
			Number: 7,
			Name:   "Crash-Safe Retries",
			Narration: []string{
				"Agents crash. Networks fail. Commands get retried.",
				"Every mutation accepts a --request-id. Replaying the same request-id",
				"returns the original result — no duplicates, no side effects.",
			},
			Steps: []Step{
				{Name: "replay_task_create", Fn: stepReplayTaskCreate, Insight: "Same request-id, same result. Your agent can retry any command without creating duplicates."},
				{Name: "replay_memory_set", Fn: stepReplayMemorySet, Insight: "Original value preserved on replay. Networks fail, agents retry — vybe handles it safely."},
			},
		},
		{
			Number: 8,
			Name:   "Production Hardening",
			Narration: []string{
				"Edge cases that matter in real deployments:",
				"Heartbeats for agent liveness detection, TTL-based memory expiry, structured event metadata.",
			},
			Steps: []Step{
				{Name: "heartbeat_liveness", Fn: stepHeartbeatLiveness, Insight: "Agents send heartbeats to prove they're alive. Stale tasks without heartbeats get reclaimed by GC."},
				{Name: "ttl_expiry_and_gc", Fn: stepTTLExpiryAndGC, Insight: "Short-lived memory auto-expires. Your agent stores temporary context that cleans itself up."},
				{Name: "structured_metadata", Fn: stepStructuredMetadata, Insight: "JSON metadata on events makes them machine-queryable — filter by exit code, tool name, duration."},
			},
		},
		{
			Number: 9,
			Name:   "Task Intelligence",
			Narration: []string{
				"Agents query the task graph to understand what's available, what's blocked, and what's next.",
				"get, stats, next, unlocks — four ways to read the task state without modifying anything.",
			},
			Steps: []Step{
				{Name: "fetch_single_task", Fn: stepFetchSingleTask, Insight: "Fetch any task by ID — status, title, project, dependencies. Full detail in one call."},
				{Name: "aggregate_stats", Fn: stepAggregateStats, Insight: "Task stats at a glance: how many pending, in_progress, completed, blocked. Project health in one number."},
				{Name: "pending_queue", Fn: stepPendingQueue, Insight: "The prioritized work queue — what's next, in what order. Agents check this before claiming."},
				{Name: "dependency_impact", Fn: stepDependencyImpact, Insight: "'If I finish this task, what gets unblocked?' Agents use this to prioritize high-impact work."},
			},
		},
		{
			Number: 10,
			Name:   "Multi-Agent Coordination",
			Narration: []string{
				"Atomic task claiming prevents two agents from working on the same task simultaneously.",
				"`task claim` is a compare-and-swap operation — only one agent wins the race.",
				"`task gc` releases abandoned claim leases when agents crash without completing.",
			},
			Steps: []Step{
				{Name: "atomic_claim", Fn: stepAtomicClaim, Insight: "Compare-and-swap claim. Two agents race — only one wins. No double-work, no conflicts."},
				{Name: "claim_lease_renewal", Fn: stepClaimLeaseRenewal, Insight: "Heartbeat renews the claim lease. GC won't reclaim a task while its agent is actively pinging."},
				{Name: "lease_gc", Fn: stepLeaseGC, Insight: "Garbage collection for abandoned tasks. When agents crash, their stuck tasks return to the queue."},
			},
		},
		{
			Number: 11,
			Name:   "Task Lifecycle",
			Narration: []string{
				"Agents mutate task state throughout the work lifecycle.",
				"Priority boosts urgent work. Delete cleans up obsolete tasks. Status transitions track progress.",
			},
			Steps: []Step{
				{Name: "priority_boost", Fn: stepPriorityBoost, Insight: "Priority 10 jumps ahead of priority 0. Your agent can escalate urgent work instantly."},
				{Name: "delete_task", Fn: stepDeleteTask, Insight: "Obsolete tasks get pruned. Your agent keeps the queue clean as requirements evolve."},
				{Name: "status_transitions", Fn: stepStatusTransitions, Insight: "Any status to any status. Agents define their own workflow — vybe doesn't enforce rigid state machines."},
			},
		},
		{
			Number: 12,
			Name:   "Knowledge Management",
			Narration: []string{
				"Memory is a first-class system in vybe. Agents read, write, and manage knowledge across sessions.",
				"compact: merge/prune entries. touch: refresh access time. query: pattern search. delete: explicit removal.",
			},
			Steps: []Step{
				{Name: "compact_memory", Fn: stepCompactMemory, Insight: "Memory compaction reduces footprint by merging low-value entries. Keeps the brief packet lean."},
				{Name: "refresh_access_time", Fn: stepRefreshAccessTime, Insight: "Touch refreshes a key's timestamp without changing its value. Keeps important facts alive through GC cycles."},
				{Name: "pattern_search", Fn: stepPatternSearch, Insight: "Wildcard search across memory keys. Your agent finds related facts without knowing exact names."},
				{Name: "explicit_deletion", Fn: stepExplicitDeletion, Insight: "Explicit delete for facts that are no longer true. Clean knowledge means better agent decisions."},
			},
		},
		{
			Number: 13,
			Name:   "Agent Identity",
			Narration: []string{
				"Each agent has its own cursor and state record in vybe.",
				"init: create/reset state. status: read cursor position and current focus. focus: explicitly set focus task.",
			},
			Steps: []Step{
				{Name: "initialize_agent", Fn: stepInitializeAgent, Insight: "Each agent gets its own cursor in the event stream. Multiple agents track their own position independently."},
				{Name: "read_agent_state", Fn: stepReadAgentState, Insight: "Agent status shows cursor position and current focus. Operators can see exactly where each agent is."},
				{Name: "override_focus", Fn: stepOverrideFocus, Insight: "Manual focus override. Your agent can skip the queue and work on a specific task when needed."},
			},
		},
		{
			Number: 14,
			Name:   "The Event Stream",
			Narration: []string{
				"The event log is the source of truth. As it grows, agents need to manage it.",
				"summarize: archive a range of events into a single summary event.",
			},
			Steps: []Step{
				{Name: "compress_history", Fn: stepCompressHistory, Insight: "Event summarization compresses N events into one summary. Manages context window pressure over long sessions."},
				{Name: "recent_activity", Fn: stepRecentActivity, Insight: "Quick poll of recent events. Your agent checks what happened since it last looked."},
			},
		},
		{
			Number: 15,
			Name:   "System Introspection",
			Narration: []string{
				"Project detail, session digest, and schema introspection.",
				"These commands give operators and agents a broader view of the system state.",
			},
			Steps: []Step{
				{Name: "fetch_project", Fn: stepFetchProject, Insight: "Full project detail by ID — name, metadata, creation time. Projects are the top-level grouping."},
				{Name: "session_digest", Fn: stepSessionDigest, Insight: "Session digest: a structured summary of what happened. Agents use this for handoff notes."},
				{Name: "inspect_schema", Fn: stepInspectSchema, Insight: "Full SQLite schema returned. Agents and operators can inspect the exact database structure."},
			},
		},
		{
			Number: 16,
			Name:   "IDE Integration",
			Narration: []string{
				"Vybe hooks into the Claude Code IDE lifecycle via hidden subcommands.",
				"subagent-start/stop: track spawned agents. stop: log turn heartbeats.",
			},
			Steps: []Step{
				{Name: "track_subagent_spawn", Fn: stepTrackSubagentSpawn, Insight: "Claude Code hook: SubagentStart. When your agent spawns a sub-agent, vybe logs it for the parent to track."},
				{Name: "track_subagent_completion", Fn: stepTrackSubagentCompletion, Insight: "Claude Code hook: SubagentStop. Sub-agent finished — the parent agent sees the completion in the event stream."},
				{Name: "turn_boundary_heartbeat", Fn: stepTurnBoundaryHeartbeat, Insight: "Claude Code hook: Stop. Turn boundaries are logged so the event stream reflects IDE conversation flow."},
			},
		},
		{
			Number: 17,
			Name:   "The Full Surface",
			Narration: []string{
				"The remaining commands that round out the vybe surface area.",
				"Artifact retrieval, retrospective extraction, history import, loop stats,",
				"project lifecycle, read-only briefs, JSONL streaming, hook management.",
			},
			Steps: []Step{
				{Name: "artifact_get_by_id", Fn: stepArtifactGetByID, Insight: "Fetch any artifact by ID — file path, type, linked task. Direct lookup without listing."},
				{Name: "retrospective_extraction", Fn: stepRetrospectiveExtraction, Insight: "Retrospectives distill session activity into persistent memory. Your agent learns from its own history."},
				{Name: "history_import", Fn: stepHistoryImport, Insight: "Import Claude Code conversation history as vybe events. Backfill context from before vybe was installed."},
				{Name: "loop_iteration_stats", Fn: stepLoopIterationStats, Insight: "Loop stats track autonomous iteration cadence. Your agent monitors its own loop health."},
				{Name: "project_lifecycle", Fn: stepProjectLifecycle, Insight: "Create and delete projects. Full lifecycle management — no orphaned metadata."},
				{Name: "read_only_brief", Fn: stepReadOnlyBrief, Insight: "Brief without cursor advancement. Your agent peeks at context without consuming events."},
				{Name: "jsonl_streaming", Fn: stepJSONLStreaming, Insight: "JSONL output for streaming processors. Your agent pipes events directly into analysis pipelines."},
				{Name: "hook_install_uninstall", Fn: stepHookInstallUninstall, Insight: "One command wires vybe into Claude Code. One command removes it. Clean install, clean uninstall."},
				{Name: "loop_dry_run", Fn: stepLoopDryRun, Insight: "The autonomous loop finds pending tasks and reports what it would do. Dry-run mode for safe testing."},
				{Name: "loop_circuit_breaker", Fn: stepLoopCircuitBreaker, Insight: "When a spawned command exits without completing the task, the loop marks it blocked. Prevents runaway loops."},
				{Name: "background_retrospective", Fn: stepBackgroundRetrospective, Insight: "Background retrospective worker processes payloads asynchronously. Sessions don't block on LLM analysis."},
			},
		},
	}
}
