# Schema Flow: `vibe status` Counts

This file documents the five count buckets returned by `vibe status` and how they map to storage, triggers, and code paths.

## Live sanity snapshot

- `tasks`: 12 total, all `completed`
- `events`: 32,176 total, 32,176 non-archived, 0 archived
- `memory`: 1 total, 1 active, 0 stale
- `agents`: 20 total, 20 active in last 7d
- `projects`: 2 total

## Where counts come from

`vibe status` calls `store.GetStatusCounts()` and reads these raw table counts in one query:

- task status buckets from `tasks.status`
- `COUNT(*)` from `events`
- `COUNT(*)` from `memory`
- `COUNT(*)` from `agent_state`
- `COUNT(*)` from `projects`

Code:

- command entry: `internal/commands/status.go`
- count query: `internal/store/status.go`

## Count-by-count flow

### 1) `tasks`

- **Stores**: mutable work items (`id`, `title`, `description`, `status`, `priority`, `project_id`, claim fields, version/timestamps).
- **What triggers writes**: `vibe task create`, `start`, `claim`, `close`, `set-status`, dependency operations, delete/GC paths.
- **Primary code paths that call it**:
  - commands: `internal/commands/task.go`
  - actions: `internal/actions/task.go`, `internal/actions/task_delete.go`
  - store: `internal/store/tasks.go`, `internal/store/task_start.go`, `internal/store/task_claim.go`, `internal/store/task_claim_next.go`, `internal/store/task_close.go`, `internal/store/task_gc.go`, `internal/store/task_deps.go`, `internal/store/task_delete.go`
- **How we use it**: focus selection/resume, execution lifecycle, dependency/unblock behavior, queue health.
- **Unique vs others**: this is the main mutable execution queue; unlike events/memory it is not append-only.

### 2) `events`

- **Stores**: append-only continuity log (`kind`, `agent_name`, optional `project_id`/`task_id`, `message`, `metadata`, `created_at`, optional `archived_at`).
- **What triggers writes**:
  - explicit logging (`vibe log`)
  - side effects from most mutating operations (task/project/memory/artifact/focus actions append events)
  - ingest flow (`vibe ingest history`) creates `user_prompt` events
  - compression flow (`vibe events summarize`) archives ranges and writes `events_summary`
- **Primary code paths that call it**:
  - commands: `internal/commands/log.go`, `internal/commands/events.go`, `internal/commands/ingest.go`
  - store APIs: `internal/store/events.go`, `internal/store/events_query.go`
  - common tx append helper used across actions/store: `store.InsertEventTx(...)`
- **How we use it**: resume deltas, brief context, audit trail, replay-safe continuity across sessions/agents.
- **Unique vs others**: highest-volume immutable history stream; designed for timeline continuity (not current-state truth).

### 3) `memory`

- **Stores**: scoped key/value knowledge (`scope`, `scope_id`, `key`, `value`, `value_type`) plus quality lifecycle fields (`canonical_key`, `confidence`, `last_seen_at`, `source_event_id`, `superseded_by`, `expires_at`).
- **What triggers writes**:
  - `vibe memory set` upsert/reinforcement
  - `vibe memory compact` summary + supersede marks
  - `vibe memory gc` deletes expired/superseded rows
  - `vibe memory delete`
- **Primary code paths that call it**:
  - commands: `internal/commands/memory.go`
  - actions: `internal/actions/memory.go`
  - store: `internal/store/memory.go`
- **How we use it**: durable context retrieval for brief/resume, scoped recall (global/project/task/agent), noise reduction via compaction and TTL.
- **Unique vs others**: persistent semantic memory with quality/decay semantics; unlike tasks/events it is key-scoped and canonicalized.

### 4) `agents`

- **Stores**: per-agent cursor/focus runtime state (`last_seen_event_id`, `focus_task_id`, `focus_project_id`, `version`, `last_active_at`).
- **What triggers writes**:
  - `vibe agent init`
  - `vibe agent focus`
  - `vibe resume` cursor/focus atomic updates
  - `vibe task start` (sets focus as part of start flow)
  - `vibe task claim` (sets focus as part of claim-next flow)
- **Primary code paths that call it**:
  - commands: `internal/commands/agent.go`, `internal/commands/resume.go`
  - store: `internal/store/agent_state.go`, `internal/store/resume.go`, `internal/store/task_start.go`
- **How we use it**: idempotent resume handoff, multi-agent coordination, deterministic cursor advancement.
- **Unique vs others**: operational control-plane state for agents, not business content/history.

### 5) `projects`

- **Stores**: project identities and metadata (`id`, `name`, `metadata`, `created_at`).
- **What triggers writes**: `vibe project create` and `vibe project delete`.
- **Primary code paths that call it**:
  - commands: `internal/commands/project.go`
  - actions: `internal/actions/project.go`, `internal/actions/project_delete.go`
  - store: `internal/store/projects.go`, `internal/store/project_delete.go`
- **How we use it**: isolation boundary for task/event/memory focus and filtering.
- **Unique vs others**: namespace/root partitioning entity rather than activity log, queue, or memory.

## Notes on current count semantics

- `events` in `status` is total rows (includes archived if present).
- `memory` in `status` is total rows (includes stale rows until GC removes them).
- `agents` in `status` is total known agents (not a recency-filtered metric).
- `tasks` currently counts only the four known statuses; unknown values would not appear in the status buckets.
