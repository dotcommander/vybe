# Decisions

Architectural decisions and rationale for vybe's command surface.

For the current keep-vs-optional command matrix and pruning workflow, see `minimal-surface.md`.

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
**Alternative:** `vybe events list` for recent events, or `vybe resume` to advance the cursor and get delta events.

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
**Alternative:** `vybe task list --status=pending|in_progress|completed|blocked`.

### `loop stats` (CLI command only)

**Removed:** v0.8.x simplification
**Reason:** Aggregate loop dashboards are operational convenience, not a continuity primitive. Agents can proceed using `resume`, `task`, and `events` without this read path.
**Alternative:** `vybe events list --kind run_result --limit N --all` for run history and client-side aggregation.

### `task delete` (CLI command only)

**Removed:** v0.8.x simplification
**Reason:** Hard deletion is destructive and not required for continuity workflows. Agents should keep immutable history and retire work by lifecycle state (`completed`/`blocked`) instead of removing rows.
**Alternative:** `vybe task complete --outcome done|blocked --summary "..."` or `vybe task set-status ...`.

### `task remove-dep` (CLI command only)

**Removed:** v0.8.x simplification
**Reason:** Editing dependency edges post-creation is not required for the primary continuity loop. Typical workflows unblock via dependency completion, not graph surgery.
**Alternative:** complete the blocker task; if work changes, create a new task with the correct dependency shape.

### `status` full diagnostics payload (CLI output shape)

**Removed:** v0.8.x simplification
**Reason:** Large installation dashboards (hooks, maintenance policy, aggregate counts, consistency diagnostics) are operator convenience, not continuity primitives.
**Alternative:** keep `vybe status --check` for health gating and use scoped read commands (`task list`, `events list`, `memory list`, `artifacts list`) for details.

### Telemetry-heavy hook handlers (`hook tool-success`, `hook subagent-start`, `hook subagent-stop`, `hook stop`)

**Removed:** v0.8.x simplification
**Reason:** These hooks primarily add high-volume observability events (`tool_success`, `agent_spawned`, `agent_completed`, `heartbeat`) and do not affect deterministic resume, task lifecycle, memory, or artifacts.
**Continuity impact:** None for core continuity semantics; lower event-stream verbosity.
**Alternative:** keep `hook session-start`, `hook prompt`, `hook tool-failure`, `hook checkpoint`, `hook task-completed`, and `hook session-end` as the minimal integration set.

### `agent init` / `agent status` / `agent focus` (removed)

**Removed:** v0.x simplification
**Reason:** `resume` auto-creates agent state on first call. Explicit initialization ceremony adds nothing — agents don't need a separate "create my state record" step before they can work. Agent status is available via `vybe status --agent A`. Focus override is via `vybe resume --focus T --project-dir P`.
**Alternative:** `vybe resume` (auto-creates state + returns brief), `vybe status --agent A` (read agent state), `vybe resume --focus T --project-dir P` (override focus).

### `--actor` flag alias (removed)

**Removed:** v0.x cleanup
**Reason:** Single canonical flag (`--agent`) keeps schemas and tool calls deterministic.

## Kept (Investigated but Retained)

### `task set-priority`

**Kept.** The focus selection algorithm (Rule 4 in `DetermineFocusTask`) sorts by `priority DESC, created_at ASC`. Priority is genuinely functional — agents spawned by the loop driver use it to escalate urgent work.

### `task set-status`

**Kept.** The loop command's `markTaskBlocked` calls `set-status` to transition tasks to blocked. `task complete --outcome=blocked` is semantically different (closes with summary). `set-status` is the raw status transition agents need.

### `brief` (removed)

**Removed:** v0.x simplification — merged into `resume`.
**Reason:** `vybe resume --peek` provides an idempotent read (brief without cursor advancement). Separate `brief` command is redundant.
**Alternative:** `vybe resume --peek`

### `loop`

**Kept.** The autonomous driver is core product functionality. It spawns external agents, manages the task queue, and handles circuit-breaking. Not experimental.

### `task set-priority`

**Kept.** Focus algorithm Rule 4 uses `priority DESC` ordering. Agents genuinely use this.

## Command Surface Guardrails (Do Not Regress)

These guardrails are for LLM/agent callers. They are not style preferences.
Breaking them increases tool-call error rates and retry noise in autonomous workflows.

### `status` as a mode multiplexer (`--events`, `--schema`, `--artifacts`)

**Decision:** Prefer explicit command paths for distinct operations (`events list`, `artifacts list`, `schema commands`) instead of mode flags on one command.

**Why not keep mode flags:** Mode precedence is implicit and easy for agents to invoke incorrectly when multiple flags are set.

**Guardrail:** When adding a new operation, do not add another `status --<mode>` flag. Add a dedicated command/subcommand.

### Root no-args output as human help text

**Decision:** Default root invocation should be machine-parseable in agent workflows.

**Why not keep help text default:** Agents may call root during discovery or by mistake. Human help output breaks strict JSON parsing and forces brittle fallback logic.

**Guardrail:** Keep prose help behind explicit `help` flows. Keep default output machine-first.

### Positional IDs for task commands

**Decision:** Use one canonical input form for identifiers (`--id` / `--task-id`), not dual positional + flag forms.

**Why not keep both forms:** Dual forms create schema drift and increase LLM invocation variance.

**Guardrail:** New ID-bearing commands must be flag-only.

### Overloaded `--project` semantics (path vs project id)

**Decision:** Use semantically explicit flags (`--project-dir` vs `--project-id`).

**Why not keep one overloaded flag:** Path-vs-id ambiguity can cause subtle cross-project context mistakes in autonomous loops.

**Guardrail:** Do not reuse a single `--project` flag name for different domain meanings across commands.

### Schema inference from usage text

**Decision:** Machine schemas should come from explicit metadata/annotations, not natural-language usage parsing.

**Why not parse help text:** Small wording edits can silently change inferred enum/required behavior and break weaker models.

**Guardrail:** Treat help text as human documentation only; treat machine schema as the source of truth.

### "Required" labels without enforced validation

**Decision:** Required flags must be enforced in runtime validation and reflected in schema.

**Why not rely on label-only required markers:** Label-only requirements drift from behavior and train agents into invalid call patterns.

**Guardrail:** Every required flag must be validated, and tests should fail if required semantics diverge from behavior.

## Design Principles (Standing)

- **Resume is the entry point.** Agents call `resume` to get their focus task, context, and commands. Everything else is secondary.
- **Idempotency everywhere.** Every mutation accepts `--request-id`. Agents retry freely.
- **No human-in-the-loop.** No prompts, no confirmations, no "are you sure?" flows.
- **Machine-first I/O.** All output is JSON. Exit codes are reliable.
- **Append-only truth.** Events are the source of truth. Current state is derived.
