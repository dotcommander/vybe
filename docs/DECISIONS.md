# Decisions

Architectural decisions and rationale for vybe's command surface.

## Guiding Principle

Vybe is continuity infrastructure for autonomous LLM agents. If a feature exists because a human might want it but no agent needs it, it doesn't belong in the CLI.

## Removed Commands

### `memory query` (pattern search)

**Removed:** v0.x simplification
**Reason:** Agents use `memory get` (known key) or `memory list` (full scope). LIKE pattern search is a human debugging tool — agents know their key names.
**Alternative:** `memory list` + client-side filtering.

### `events tail` (streaming)

**Removed:** v0.x simplification
**Reason:** Agents fetch bounded event lists via `events list`. Real-time streaming is a human operator feature for watching event flow. Agents don't poll continuously.
**Alternative:** `events list --limit N --since-id X` for incremental fetching.

### `task unlocks` (dependency impact analysis)

**Removed:** v0.x simplification
**Reason:** Pipeline analysis — "if I finish this, what gets unblocked?" Agents work their assigned task; they don't analyze dependency graphs to decide what to prioritize. The resume algorithm handles prioritization.
**Alternative:** `task get --id X` to inspect a specific task's dependencies.

### `task next` (pending queue view)

**Removed:** v0.x simplification
**Reason:** Agents don't browse the task queue. `resume` deterministically selects the next focus task via the 5-rule algorithm (priority DESC, created_at ASC, dependency-aware). The queue is an implementation detail.
**Note:** The underlying store function (`SelectPendingTasks`) remains — it's used by `resume` for the brief's pipeline field.

### `snapshot` (point-in-time capture)

**Removed:** v0.x simplification
**Reason:** Human progress reporting. Captures system state for diffing before/after operations. Agents don't diff system state — they resume from cursor position. Not a continuity primitive.

### `session digest` (CLI command only)

**Removed:** v0.x simplification
**Reason:** The CLI command exposed session analytics (event counts by kind). Agents don't introspect their own session metadata.
**Note:** The `SessionDigest` function remains in `internal/actions/session.go` — it's called by `SessionRetrospective` for lesson extraction.

### `task stats` (CLI command only)

**Removed:** v0.x simplification
**Reason:** Dashboard counting (N pending, N completed, N blocked). Agents don't query aggregate statistics — they work their assigned task. This is a human progress visualization.
**Alternative:** `task list --status=pending` to check remaining work.

### `agent init` (merged into `agent status`)

**Removed:** v0.x simplification
**Reason:** `resume` auto-creates agent state on first call. Explicit initialization ceremony adds nothing — agents don't need a separate "create my state record" step before they can work.
**Merged into:** `agent status` now performs load-or-create (was read-only, now idempotent).

### `--actor` flag (deprecated alias)

**Removed:** v0.x cleanup
**Reason:** Legacy alias for `--agent`. Maintained for backward compatibility during v0.x. No external consumers remain.

## Kept (Investigated but Retained)

### `task set-priority`

**Kept.** The focus selection algorithm (Rule 4 in `DetermineFocusTask`) sorts by `priority DESC, created_at ASC`. Priority is genuinely functional — agents spawned by the loop driver use it to escalate urgent work.

### `task set-status`

**Kept.** The loop command's `markTaskBlocked` calls `set-status` to transition tasks to blocked. `task complete --outcome=blocked` is semantically different (closes with summary). `set-status` is the raw status transition agents need.

### `brief`

**Kept.** Hooks use `brief` — the `session-start` hook calls it to inject context without advancing the agent cursor. It's agent infrastructure, not human convenience.

### `loop`

**Kept.** The autonomous driver is core product functionality. It spawns external agents, manages the task queue, and handles circuit-breaking. Not experimental.

### `hook retrospective` / `hook retrospective-bg`

**Kept.** Two entry points for session retrospective extraction — synchronous (stdin-based hook handler) and async (positional args for background worker). Both serve different deployment patterns.

### `task set-priority`

**Kept.** Focus algorithm Rule 4 uses `priority DESC` ordering. Agents genuinely use this.

## Design Principles (Standing)

- **Resume is the entry point.** Agents call `resume` to get their focus task, context, and commands. Everything else is secondary.
- **Idempotency everywhere.** Every mutation accepts `--request-id`. Agents retry freely.
- **No human-in-the-loop.** No prompts, no confirmations, no "are you sure?" flows.
- **Machine-first I/O.** All output is JSON. Exit codes are reliable.
- **Append-only truth.** Events are the source of truth. Current state is derived.
