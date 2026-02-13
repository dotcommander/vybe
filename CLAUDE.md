# CLAUDE.md

## Purpose

Durable continuity for AI coding agents. Vibe gives autonomous agents crash-safe task tracking, append-only event logs, scoped memory, deterministic resume/brief, and artifact linking — all backed by SQLite. Agents pick up exactly where they left off across sessions without human intervention.

## Global CLI Tool

This is a **global CLI tool** installed system-wide.

| Path | Purpose |
|------|---------|
| `~/.config/vibe/config.yaml` | User settings |
| `~/.config/vibe/vibe.db` | Runtime state (SQLite) |

### Build & Install

```bash
go build -o vibe ./cmd/vibe

# Option 1: Standard install
go install ./cmd/vibe

# Option 2: Symlink (keeps binary in project, linked to ~/go/bin)
ln -sf "$(pwd)/vibe" ~/go/bin/vibe
```

### Config Loading

Config is loaded from (in order, first found wins):
1. `~/.config/vibe/config.yaml`
2. `/etc/vibe/config.yaml`
3. `./config.yaml` (current directory; lowest priority)
4. Environment variables (prefix: `VIBE_`)

Relevant keys:

- `db_path` (in config.yaml)
- `VIBE_DB_PATH` (env override)
- `--db-path` (CLI override; highest priority)

### State Persistence

State is persisted in SQLite and managed through the CLI commands (tasks, events, memory, agent state).

## Design Principles

**Agents-Only CLI** - Continuity primitives for autonomous agents.

Humans may read logs for debugging, but the product is not designed around human interaction.

### Non-Negotiables (Agents-Only)

- **No human-in-the-loop requirements.**
  - Do not introduce workflows that require a human to approve, confirm, click, or provide input in order to make progress.
  - Avoid statuses like `needs_user_input` or "blocked on user". If something is blocked, it must be blocked on an external system/time, and the system must be able to retry/backoff autonomously.
- **Non-interactive by default.**
  - No prompts, no TTY UIs, no "Are you sure?" confirmations.
  - If an operation is dangerous, require an explicit flag (e.g. `--force`) and fail closed without it.
- **Machine-first I/O.**
  - All commands that are part of the agent workflow must emit JSON by default (and support `--jsonl` when streaming).
  - JSON schemas must be stable and versioned via additive changes only. Avoid breaking field renames/types.
  - Exit codes must be reliable and consistent; errors must be structured in JSON.

### Concurrency & Resilience Requirements

Assume **multiple concurrent agents and workers** operating on the same DB at once.

- **Idempotency everywhere.**
  - Mutating commands should accept/propagate idempotency keys and dedupe repeated requests safely.
  - Tool-like operations should be safe under retries (at-least-once execution).
- **Append-only truth.**
  - Model history as immutable events (append-only). Derive "current state" from projections.
  - Prefer content-addressed artifacts to dedupe repeated outputs.
- **Single-head semantics (no branching UX).**
  - If/when modeling an "active head" for a run/task stream, advance it with CAS/optimistic concurrency.
  - On conflicts, do not ask humans. Auto-rebase/retry with budgets (attempt limits, timeouts, backoff).
- **Crash-safe progress.**
  - Persist intent/checkpoints before side effects where possible, and always record completion/failure.
  - Resume must be deterministic: reconstruct from persisted state, not in-memory agent context.

For comprehensive examples, see:
- [Crash recovery scenarios](docs/usage-examples.md#3-crash-recovery-scenarios)
- [Concurrent agent coordination patterns](docs/usage-examples.md#4-concurrent-agent-scenarios)

## Architecture

```
cmd/vibe/main.go
  ↓
internal/commands/     # Cobra CLI layer (parse flags, call actions)
  ↓
internal/actions/      # Business logic (orchestrate store calls, build packets)
  ↓
internal/store/        # SQLite persistence + migrations (transactions, retry, CAS)
```

**Layers:** Commands → Actions → Store

**Models:** `internal/models/` (domain types shared across layers)

## Coding Guidelines (Backend + CLI)

- Keep diffs small and reviewable. Match existing patterns in `internal/commands`, `internal/actions`, `internal/store`.
- Prefer Go stdlib; use `context.Context` at boundaries; wrap errors with `%w`.
- Keep DB mutations transactional; avoid partial writes. Use optimistic concurrency where contention is expected.
- Tests:
  - If you change behavior or output, add/update tests.
  - Prefer integration tests for resume/concurrency semantics and retry/idempotency behavior.

## Key Patterns

| Pattern | Implementation |
|---------|----------------|
| **ID generation** | `{type}_{unix_nano}_{random_hex}` (e.g., `task_1234567890_a3f9`) |
| **Idempotency** | `--request-id` + `idempotency` table; replay original result on duplicates |
| **Optimistic concurrency** | `version` columns on tasks/agent_state; CAS updates with retry |
| **Monotonic cursor** | `UPDATE agent_state SET last_seen_event_id = MAX(last_seen_event_id, ?)` |
| **Retry logic** | `RetryWithBackoff()` for all DB ops; exponential backoff on SQLITE_BUSY |
| **Type inference** | Memory values auto-detect: string, number, boolean, json, array |

## Focus Selection Algorithm

Deterministic 5-rule system (in `internal/store/resume.go`):

1. Keep current focus if `in_progress` or `blocked`
2. Check deltas for `task_assigned` events
3. Resume old focus if unblocked
4. `SELECT` oldest pending task — when `focus_project_id` is set, prefer project-scoped tasks first, then fall through to global
5. Return empty if no work available

## Brief Packet Structure

```json
{
  "task": {...},                    // Focus task (null if none)
  "project": {...},                 // Focus project (null if none)
  "relevant_memory": [...],         // global + task-scoped + project-scoped (NOT agent-scoped)
  "recent_events": [...],           // Last 20 events for task
  "artifacts": [...]                // Files linked to task
}
```

When `focus_project_id` is set, project-scoped memory is filtered to that project only.
When unset, all project-scoped memory is included (legacy behavior).

**Resume vs Brief:**
- `vibe resume`: Fetch deltas + build brief + advance cursor atomically
- `vibe brief`: Build brief without cursor advancement (idempotent reads)

## Database Schema

| Table | Purpose |
|-------|---------|
| `events` | Append-only continuity log (id, kind, agent_name, task_id, message, metadata) |
| `tasks` | Mutable task definitions with optimistic concurrency (id, title, status, project_id, version) |
| `agent_state` | Cursor position + focus tracking per agent (last_seen_event_id, focus_task_id, focus_project_id) |
| `memory` | Scoped KV storage with TTL (scope: global/project/task/agent) |
| `artifacts` | Files/outputs linked to tasks (task_id, event_id, file_path) |
| `idempotency` | Request deduplication (agent_name + request_id composite PK) |
| `projects` | Project metadata (id, name, metadata, created_at) |

**SQLite Config:** WAL mode, busy_timeout=5000ms, synchronous=NORMAL, foreign_keys=ON

**SQLite CRITICAL:** Never issue `db.Query*` while a parent `rows` cursor is open on the same `*sql.DB`. SQLite single-connection tests deadlock silently. Always: scan into slice, close rows, THEN do follow-up queries.

## Verification Commands

```bash
gofmt -w ./cmd/vibe ./internal
go test ./...
go vet ./...
go build ./...
```

## Completion Status

**Phase 1-4: ✅ Complete**
- Core schema + migrations
- All CRUD operations (tasks, events, memory, artifacts, agent state)
- Idempotency system
- Resume/brief with deterministic focus selection
- Comprehensive test coverage (16 test files, >980 lines)

**Known Gaps:**
- No FK constraint on `tasks.project_id` or `agent_state.focus_project_id` — app layer validates
- Event guardrails enforced centrally in `store.InsertEventTx` / `store.ValidateEventPayload`:
  - `kind` max 128 chars
  - `agent_name` max 128 chars
  - `message` max 4096 chars
  - `metadata` max 16384 chars + must be valid JSON when present
- Expired memory is cleaned via `vibe memory gc` but has no automatic scheduled cleanup
- Task status transitions are intentionally unrestricted for agent flexibility (any status → any status). The `blocked_reason` column distinguishes dependency blocks (`"dependency"`) from failure blocks (`"failure:<reason>"`); resume Rule 1.5 uses this to decide whether to keep or skip a blocked focus task

## Vibe Integration (Claude Code)

Claude Code is integrated with vibe via hooks. The system automatically:
- **SessionStart**: Runs `vibe resume` and injects focus task + memory into context
- **UserPromptSubmit**: Logs user prompts for cross-session continuity
- **PostToolUseFailure**: Logs failed tool calls for recovery context
- **TaskCompleted**: Logs task completion lifecycle signals
- **PreCompact/SessionEnd**: Performs memory checkpoint (`memory compact` + `memory gc`)
- **SessionEnd**: Extracts session retrospective via `vibe hook retrospective`
- **Agent delegation**: Logs spawned agents as vibe events
- **Commits**: Logs git commits as vibe events

### Proactive Usage

When working on multi-step tasks, proactively use vibe for durable state:

```bash
# Store discoveries that should persist across sessions
vibe memory set --agent=claude --key=<key> --value=<value> --scope=task --scope-id=<task_id> --request-id=mem_$(date +%s)

# Log significant progress
vibe log --agent=claude --kind=progress --task=<task_id> --msg="<what happened>" --request-id=evt_$(date +%s)

# Link output files to tasks
vibe artifact add --agent=claude --task=<id> --path=<path> --request-id=art_$(date +%s)
```

### After Plan Approval

When a plan is approved via ExitPlanMode, create vibe tasks for each implementation step:

```bash
vibe task create --agent=claude --title="Step 1: ..." --desc="..." --request-id=plan_step_1_$(date +%s)
vibe task create --agent=claude --title="Step 2: ..." --desc="..." --request-id=plan_step_2_$(date +%s)
```

### Focus Task

The focus task from `vibe resume` is your primary work item. When starting work:
1. Check the brief for context (task, memory, events, artifacts)
2. Use `vibe task start` to claim and mark in_progress
3. Log progress events as you work
4. Set status to completed when done — next resume auto-advances to next task

## Operational Context

- DB path precedence: `--db-path` > `VIBE_DB_PATH` > `config.yaml: db_path` > `~/.config/vibe/vibe.db`
- Config lookup (first found wins): `~/.config/vibe/config.yaml`, `/etc/vibe/config.yaml`, `./config.yaml` (lowest priority)
- Agent identity: `--agent` flag or `VIBE_AGENT` env (required for most commands)
- Output format: JSON only (default)
- Idempotency: `--request-id` or `VIBE_REQUEST_ID` for safe retries
- JSON envelope contract is versioned via `schema_version` (current: `v1`) in success/error wrapper responses
- Reusable idempotent write pattern exists in `store.RunIdempotent[T]`; keep create SQL in `store/*Tx` helpers and call them from actions to avoid schema leakage
- Command wiring lives in `internal/commands/root.go`; tutorial `greet` command/action were removed to keep CLI surface minimal
- Conflict-aware idempotent retries use `store.RunIdempotentWithRetry[T]` (for CAS/version-conflict paths like resume/task status)
- In `RunIdempotent*` operation closures, avoid `db.Query*` calls while a tx is open; use `tx.Query*` to prevent sqlite in-memory single-connection stalls during tests
- Store transaction primitives are centralized in `store/tx.go` via `Querier` and `Transact`; action-layer SQL should remain zero (tests excluded)
- Manual `db.Begin` wrappers in store were collapsed to `Transact`; remaining direct `db.Begin` usage should be limited to `store/tx.go` and explicit idempotency tests
- Event metadata in `models.Event` is `json.RawMessage` and hydrated via `store.decodeEventMetadata`; event/list/resume outputs now emit native JSON objects instead of escaped JSON strings
- Task status enums are documented directly in Cobra help text (`task`, `task set-status`, `task list`, and `--status` flag) to keep agent usage aligned with valid values: pending, in_progress, completed, blocked
- `brief` output includes `approx_tokens`, estimated from recent event message chars using `ceil(total_message_chars/4)` for context-budget planning
- CLI error paths now emit structured JSON logs to stderr via stdlib `log/slog` JSON handler (configured in `commands.Execute`); command errors use `slog.Error` with machine-parseable fields
- Task status validation errors include machine-parseable recovery hints (`field`, `invalid_value`, `valid_options`) in structured stderr logs for autonomous self-correction
- `vibe schema` emits machine-readable argument schemas (flags/types/defaults/required/enum hints) for command introspection
- Event compression flow exists via `vibe events summarize`: archives event ID ranges (`archived_at`) and appends `events_summary`; list/tail/resume/fetchRecent exclude archived events by default
- Project-isolated event streams: events/artifacts now carry `project_id`; resume deltas use active `focus_project_id` with `(project_id = focus OR project_id IS NULL)` filtering, and `DetermineFocusTask` is strict to project scope when focus project is set
- `task remove-dependency` action paths now use `store.RemoveTaskDependencyTx` so dependency delete + event append share one transaction/idempotency envelope
- `resume --project` and `run --project` now scope and persist `focus_project_id` inside resume's idempotent state update (`UpdateAgentStateAtomicWithProject*`), avoiding separate pre-resume focus mutations
- Project focus writes now self-heal missing agent state (`INSERT OR IGNORE`) and validate project existence inside the store transaction, so idempotent replays are checked before mutable validation
- `events tail --jsonl --once` now emits raw event JSONL lines (not wrapped envelope), matching streaming contract and usage examples
- Root `--version` now emits standard JSON success envelope with `data.version` for machine-first consistency
- Phase A memory quality fields are live: `memory` rows now carry `canonical_key`, `confidence`, `last_seen_at`, `source_event_id`, and `superseded_by`; canonical-key dedupe/reinforcement is applied through `vibe memory set` with idempotent eventing (`memory_upserted` / `memory_reinforced`)
- Phase B routes `memory set` through canonical upsert semantics at the action layer, preserving command interface while deduping by canonical key
- Phase C adds `vibe memory compact` (summary compaction + supersede markers) and `vibe memory gc` (expired/superseded cleanup) with idempotent eventing (`memory_compacted`, `memory_gc`)
- Phase D brief memory retrieval now excludes superseded entries, filters stale low-confidence noise, and orders by confidence then recency (`COALESCE(last_seen_at, created_at)`)
- Important feature coverage matrix is tracked in `docs/testing/important-features-matrix.md`; high-value integration tests now exercise idempotent wrappers and tx helpers (`StartTaskAndFocus*`, `AddArtifactIdempotent`, `DeleteMemoryWithEventIdempotent`, `FetchRecentUserPrompts`, `UpdateAgentStateAtomic*`) to keep continuity-critical paths under explicit regression tests
- OpenCode manual example assets are now grouped under `examples/opencode/` (`opencode-vibe-plugin.ts`, `opencode-plugin-setup.md`); docs should not reference legacy root-level `examples/opencode-*.md|ts` paths
- Skill example moved under `examples/vibe-skill-patterns/SKILL.md`; update any legacy root-level references to avoid broken links
- Claude Code hooks reference uses snake_case common input fields (`session_id`, `hook_event_name`) and SessionStart `source` matcher values (`startup|resume|clear|compact`); avoid relying on camelCase fields in hook stdin parsers
- README canonical product statement: vibe is an agents-only continuity CLI for tasks, events, memory, and deterministic resume/brief so agents recover exactly after session loss/crash
- Zero-HITL posture is part of core product identity (no prompts/confirmations; machine-first JSON I/O), not just implementation detail
- Current top-level command surface (from `vibe --help`): `agent`, `artifact`, `brief`, `events`, `hook`, `ingest`, `log`, `memory`, `project`, `resume`, `run`, `schema`, `session`, `status`, `task`, `upgrade`
- High-signal operator path remains: `resume`/`brief` for context, `task` for lifecycle, `log` + `events` for timeline, `memory` for durable scoped facts, `artifact` for file linkage, `schema` for machine introspection
- Task claiming primitives (`claimed_by`, `claimed_at`, `claim_expires_at`, `ClaimTaskTx`, `ReleaseExpiredClaims`) are now exposed via `vibe task claim` (server-side next-eligible selection + claim + in_progress + focus in one tx) and `vibe task close` (atomic status + summary event + claim release); store layer in `task_claim_next.go` and `task_close.go`; shared `setAgentFocusTx` extracted in `task_start.go`; new event kinds: `task_claimed`, `task_closed`
- Task JSON hydration is centralized in `internal/store/tasks.go` (`CreateTaskTx`, `getTaskByQuerier`, `ListTasks`); adding task columns requires updating all three SELECT+Scan paths to keep command/action outputs consistent
- Task priority management: `task set-priority` (CAS update + `task_priority_changed` event via `UpdateTaskPriorityWithEventTx`); `task list --priority N` filters by exact priority; `ListTasks` now takes `priorityFilter int` (-1 = no filter) and orders by `priority DESC, created_at DESC`
- Pipeline visibility commands: `task next` (agent pipeline via `FetchPipelineTasks`), `task unlocks` (dependency unlock via `FetchUnlockedByCompletion`), `task stats` (status counts via `GetTaskStatusCounts`); all read-only, no idempotency needed
