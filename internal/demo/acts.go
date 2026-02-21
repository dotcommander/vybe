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

// BuildActs returns all acts with their steps.
func BuildActs() []Act {
	return []Act{
		{
			Number: 1,
			Name:   "Building The World",
			Narration: []string{
				"Set up durable agent context: database, project scope, task graph, and scoped memory.",
				"Everything the agent knows or intends is persisted before work begins.",
			},
			Steps: []Step{
				{Name: "upgrade_database", Fn: stepUpgradeDatabase, Insight: "Your agent's first command in any environment. Creates all tables — ready to remember everything."},
				{Name: "create_project", Fn: stepCreateProject, Insight: "Projects are implicit — scoped by a project ID. The session-start hook ensures the project row exists."},
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
				"Run the active work loop for a session: hooks, task begin, progress events, memory, artifacts, and completion.",
				"This mirrors real Claude/OpenCode execution with machine-readable telemetry.",
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
				{Name: "link_artifact", Fn: stepLinkArtifact, Insight: "Output files linked to tasks via push. New sessions find what was built immediately — no archaeology."},
				{Name: "complete_task", Fn: stepCompleteTask, Insight: "Task done. Next resume auto-advances to the next task in the queue."},
				{Name: "task_completion_hook", Fn: stepTaskCompletionHook, Insight: "Claude Code hook: TaskCompleted. The IDE signals vybe so the event stream reflects IDE-level milestones."},
			},
		},
		{
			Number: 3,
			Name:   "The Agent Sleeps",
			Narration: []string{
				"Close the session safely with checkpoint and retrospective extraction.",
				"State remains durable in SQLite, so crashes do not lose context.",
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
				"Start a fresh session and restore continuity from persisted state.",
				"Focus, memory, and artifacts survive boundaries so a new agent resumes immediately.",
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
				"Unblock dependencies, let resume pick the next work, and finish remaining tasks.",
				"The queue drains deterministically to task=null when work is complete.",
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
				"Audit what happened using event, memory, artifact, and health queries.",
				"Every continuity primitive is machine-queryable for verification and debugging.",
			},
			Steps: []Step{
				{Name: "query_event_stream", Fn: stepQueryEventStream, Insight: "The complete activity log — every tool call, every progress note, every prompt. Fully queryable by kind."},
				{Name: "query_all_memory_scopes", Fn: stepQueryAllMemoryScopes, Insight: "Three memory scopes: global (environment), project (team knowledge), task (work-specific decisions)."},
				{Name: "query_artifacts", Fn: stepQueryArtifacts, Insight: "Files from Act II are still here in Act VI. Artifacts persist across all sessions."},
				{Name: "health_check", Fn: stepHealthCheck, Insight: "One command confirms the database is healthy and responsive. Agents call this before critical work."},
			},
		},
		{
			Number: 7,
			Name:   "Crash-Safe Retries",
			Narration: []string{
				"Retry the same mutation with the same request ID to prove idempotency.",
				"Replays return original results without duplicates or side effects.",
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
				"Exercise production safety edges: TTL expiry, garbage collection, and structured metadata.",
				"These controls keep memory fresh and event logs analyzable at scale.",
			},
			Steps: []Step{
				{Name: "ttl_expiry_and_gc", Fn: stepTTLExpiryAndGC, Insight: "Short-lived memory auto-expires. Your agent stores temporary context that cleans itself up."},
				{Name: "structured_metadata", Fn: stepStructuredMetadata, Insight: "JSON metadata on events makes them machine-queryable — filter by exit code, tool name, duration."},
			},
		},
		{
			Number: 9,
			Name:   "Task Intelligence",
			Narration: []string{
				"Read task state without mutation using task lookups.",
				"Agents verify title, status, and dependencies before acting.",
			},
			Steps: []Step{
				{Name: "fetch_single_task", Fn: stepFetchSingleTask, Insight: "Fetch any task by ID — status, title, project, dependencies. Full detail in one call."},
			},
		},
		{
			Number: 10,
			Name:   "Multi-Agent Coordination",
			Narration: []string{
				"Coordinate concurrent workers with atomic task acquisition.",
				"task begin uses CAS semantics so one worker wins each claim race.",
			},
			Steps: []Step{
				{Name: "atomic_claim", Fn: stepAtomicClaim, Insight: "Status change uses compare-and-swap on the version column. Two agents racing — only one succeeds."},
			},
		},
		{
			Number: 11,
			Name:   "Task Lifecycle",
			Narration: []string{
				"Mutate lifecycle state with priority, status updates, and deletion.",
				"Agents adapt queue shape quickly as requirements change.",
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
				"Maintain long-lived knowledge with explicit memory deletion.",
				"Removing stale facts improves downstream agent decisions.",
			},
			Steps: []Step{
				{Name: "explicit_deletion", Fn: stepExplicitDeletion, Insight: "Explicit delete for facts that are no longer true. Clean knowledge means better agent decisions."},
			},
		},
		{
			Number: 13,
			Name:   "Agent Identity",
			Narration: []string{
				"Track per-agent cursor and focus in durable agent state.",
				"Supervising agents can inspect and override focus when orchestration requires it.",
			},
			Steps: []Step{
				{Name: "read_agent_state", Fn: stepReadAgentState, Insight: "Status with --agent shows cursor position and current focus. Supervising agents can see exactly where each worker is."},
				{Name: "override_focus", Fn: stepOverrideFocus, Insight: "Manual focus override via resume --focus. Your agent can skip the queue and work on a specific task when needed."},
			},
		},
		{
			Number: 14,
			Name:   "The Event Stream",
			Narration: []string{
				"Use append-only events as the execution ledger.",
				"push appends atomically and events list provides fast recent snapshots.",
			},
			Steps: []Step{
				{Name: "compress_history", Fn: stepCompressHistory, Insight: "Push adds events atomically. events list queries the log. Together they manage the event history."},
				{Name: "recent_activity", Fn: stepRecentActivity, Insight: "Quick poll of recent events. Your agent checks what happened since it last looked."},
			},
		},
		{
			Number: 15,
			Name:   "System Introspection",
			Narration: []string{
				"Inspect command schemas for autonomous planning.",
				"Weak models can discover flags, required arguments, and mutation hints safely.",
			},
			Steps: []Step{
				{Name: "inspect_schema", Fn: stepInspectSchema, Insight: "Full command argument schema returned. Agents and operators can inspect the exact CLI surface."},
			},
		},
		{
			Number: 16,
			Name:   "IDE Integration",
			Narration: []string{
				"Capture IDE lifecycle signals through hook commands.",
				"Spawn and completion events plus heartbeats keep the stream aligned with conversation flow.",
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
				"Cover the remaining surface: artifacts, retrospectives, loop controls, and hook lifecycle.",
				"Background retrospective workers keep heavy extraction asynchronous.",
			},
			Steps: []Step{
				{Name: "artifact_get_by_id", Fn: stepArtifactGetByID, Insight: "List artifacts and inspect their metadata — file path, type, linked task. All via artifacts list."},
				{Name: "retrospective_extraction", Fn: stepRetrospectiveExtraction, Insight: "Retrospectives distill session activity into persistent memory. Your agent learns from its own history."},
				{Name: "loop_iteration_stats", Fn: stepLoopIterationStats, Insight: "Loop stats track autonomous iteration cadence. Your agent monitors its own loop health."},
				{Name: "read_only_brief", Fn: stepReadOnlyBrief, Insight: "Resume --peek: brief without cursor advancement. Your agent peeks at context without consuming events."},
				{Name: "hook_install_uninstall", Fn: stepHookInstallUninstall, Insight: "One command wires vybe into Claude Code. One command removes it. Clean install, clean uninstall."},
				{Name: "loop_dry_run", Fn: stepLoopDryRun, Insight: "The autonomous loop finds pending tasks and reports what it would do. Dry-run mode for safe testing."},
				{Name: "loop_circuit_breaker", Fn: stepLoopCircuitBreaker, Insight: "When a spawned command exits without completing the task, the loop marks it blocked. Prevents runaway loops."},
				{Name: "background_retrospective", Fn: stepBackgroundRetrospective, Insight: "Background retrospective worker processes payloads asynchronously. Sessions don't block on LLM analysis."},
			},
		},
	}
}
