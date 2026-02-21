## Purpose

Durable continuity for AI coding agents. Vybe gives autonomous agents crash-safe task tracking, append-only event logs, scoped memory, deterministic resume/brief, and artifact linking — all backed by SQLite. Agents pick up exactly where they left off across sessions without human intervention.

## Global CLI Tool

This is a **global CLI tool** installed system-wide.

| Path | Purpose |
|------|---------|
| `~/.config/vybe/config.yaml` | User settings |
| `~/.config/vybe/vybe.db` | Runtime state (SQLite) |

### Build & Install

```bash
go build -o vybe ./cmd/vybe

#-Option 1: Standard install
go install ./cmd/vybe

#-Option 2: Symlink (keeps binary in project, linked to ~/go/bin)
ln -sf "$(pwd)/vybe" ~/go/bin/vybe
```

### Config Loading

Config is loaded from (in order, first found wins):
1. `~/.config/vybe/config.yaml`
2. `/etc/vybe/config.yaml`
3. `./config.yaml` (current directory; lowest priority)
4. Environment variables (prefix: `VYBE_`)

Relevant keys:

- `db_path` (in config.yaml)
- `VYBE_DB_PATH` (env override)
- `--db-path` (CLI override; highest priority)

### State Persistence

State is persisted in SQLite and managed through the CLI commands (tasks, events, memory, agent state).

## Design Principles

**Agents-Only CLI** - Continuity primitives for autonomous agents.

**Beta** — No backward compatibility, migration support, or deprecation shims. Breaking changes are acceptable. Rename flags, remove commands, change schemas freely.

Humans may read logs for debugging, but the product is not designed around human interaction.

### What Vybe Is

- Agent memory system (memories, compaction, GC, reinforcement, retrospectives)
- Multi-agent coordination (agent state, focus tasks, claims, heartbeats, dependencies)
- Claude Code hook integration (10 hook events, bidirectional context injection)
- Event stream (structured log of agent activity — prompts, tool calls, spawns, completions)
- Idempotent operations (request ID deduplication across all mutations)
- Session continuity (resume with context, auto-summarization)

### What Vybe Is Not

- Not a human issue tracker (no tags, epics, comments, kanban, search-by-keyword)
- Not a project management tool (no dashboards, no reporting)
- Not a general-purpose CLI tool (hooks are hidden, output is JSON for machine consumption)

### Why Not

- Agents know their task IDs — they don't need search
- Agents emit structured metadata — they don't need tags
- Events ARE the comment stream — no separate comment entity needed
- Projects are the only grouping level — no epics hierarchy
- Dependencies (blocks/blockedBy) model all task relationships — no subtask entity needed
- Every mutation is idempotent — agents retry freely without side effects

## Documentation Scope (Blocking)

- `docs/` is for **vybe users** only (operators and integrators using the tool).
- Do not place work-in-progress notes, temporary writeups, local-dev scratch files, refactor journals, or historical snapshots in `docs/`.
- Historical/audit/scratch material must stay outside tracked docs (for example in `.work/`), and must not be staged for commit.
- Contributor implementation process belongs in `CLAUDE.md` and code comments/tests where needed, not user docs.

### Scratch and Internal Artifacts (Blocking)

- Use `.work/` for local non-user artifacts.
- Preferred local structure:
  - `.work/scratch/` for in-progress notes
  - `.work/audits/` for audit outputs
  - `.work/refactors/` for refactor/implementation journals
  - `.work/archive/` for local historical snapshots
- Use `/tmp/vybe-*` only for ephemeral one-run artifacts that can be safely lost.
- Never stage `.work/**` or `/tmp` artifacts for commit.

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

For comprehensive examples, see [Common Tasks](docs/common-tasks.md).

## Architecture

```
cmd/vybe/main.go
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

**Resume vs Peek:**
- `vybe resume`: Fetch deltas + build brief + advance cursor atomically
- `vybe resume --peek`: Build brief without cursor advancement (idempotent read)

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

### SQLite Concurrency Model

Vybe uses two complementary concurrency mechanisms. They solve different problems:

| Mechanism | What it does | What it prevents |
|-----------|-------------|-----------------|
| **Transactions** (`Transact()`) | Pessimistic write serialization (SQLite WAL = one writer at a time) | Partial writes, torn state |
| **CAS versioning** (`WHERE version = ?`) | Optimistic read-modify-write safety | Silent overwrites when two agents read the same version, compute independently, and both try to write |

Transactions alone don't prevent read-modify-write races because the read and the decision to write can span different transactions (or different CLI invocations). CAS runs *inside* transactions — it's not an alternative to them.

**Implementation surface:**
- `version` column on `tasks` and `agent_state`
- `ErrVersionConflict` sentinel error
- `RetryWithBackoff()` handles both `SQLITE_BUSY` and version conflicts
- `RunIdempotentWithRetry()` adds configurable retry with conflict predicate

## Verification Commands

```bash
gofmt -w ./cmd/vybe ./internal
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
- Expired memory is cleaned via `vybe memory gc` but has no automatic scheduled cleanup
- Task status transitions are intentionally unrestricted for agent flexibility (any status → any status). The `blocked_reason` column distinguishes dependency blocks (`"dependency"`) from failure blocks (`"failure:<reason>"`); resume Rule 1.5 uses this to decide whether to keep or skip a blocked focus task

## Vybe Integration (Claude Code)

Claude Code is integrated with vybe via hooks. The system automatically:
- **SessionStart**: Runs `vybe resume` and injects focus task + memory into context
- **UserPromptSubmit**: Logs user prompts for cross-session continuity
- **PostToolUseFailure**: Logs failed tool calls for recovery context
- **TaskCompleted**: Logs task completion lifecycle signals
- **PreCompact/SessionEnd**: Performs memory checkpoint (`memory compact` + `memory gc`)
- **SessionEnd**: Extracts session retrospective via `vybe hook retrospective`
- **Agent delegation**: Logs spawned agents as vybe events
- **Commits**: Logs git commits as vybe events

### Proactive Usage

When working on multi-step tasks, proactively use vybe for durable state:

```bash
#-Atomic batch push (preferred — combines event, memory, artifacts, status in one call)
vybe push --agent=claude --request-id=push_$(date +%s) --json '{
  "task_id": "<task_id>",
  "event": {"kind": "progress", "message": "<what happened>"},
  "memories": [{"key": "<key>", "value": "<value>", "scope": "task", "scope_id": "<task_id>"}],
  "artifacts": [{"file_path": "<path>"}],
  "task_status": {"status": "completed", "summary": "<summary>"}
}'

#-Store a single memory when push is overkill
vybe memory set --agent=claude --key=<key> --value=<value> --scope=task --scope-id=<task_id> --request-id=mem_$(date +%s)

#-Read current state without advancing cursor
vybe resume --agent=claude --peek
```

### After Plan Approval

When a plan is approved via ExitPlanMode, create vybe tasks for each implementation step:

```bash
vybe task create --agent=claude --title="Step 1: ..." --desc="..." --request-id=plan_step_1_$(date +%s)
vybe task create --agent=claude --title="Step 2: ..." --desc="..." --request-id=plan_step_2_$(date +%s)
```

### Focus Task

The focus task from `vybe resume` is your primary work item. When starting work:
1. Check the brief for context (task, memory, events, artifacts)
2. Use `vybe task begin` to claim and mark in_progress
3. Log progress events as you work
4. Set status to completed when done — next resume auto-advances to next task

## Contributor Notes

- DB path precedence: `--db-path` > `VYBE_DB_PATH` > `config.yaml: db_path` > `~/.config/vybe/vybe.db`
- Agent identity: `--agent` flag or `VYBE_AGENT` env (required for most commands)
- Idempotency: `--request-id` or `VYBE_REQUEST_ID` for safe retries
- New features follow the idempotent action pattern: `store.*Tx` → `actions.RunIdempotent` → `commands`
- In `RunIdempotent*` closures, use `tx.Query*` not `db.Query*` — SQLite single-connection tests deadlock silently
- Task JSON hydration: `CreateTaskTx`, `getTaskByQuerier`, `ListTasks` must stay in sync when adding columns
- Command wiring: `internal/commands/root.go`
- Claude Code hooks use snake_case stdin fields (`session_id`, `hook_event_name`); SessionStart `source` matcher: `startup|resume|clear|compact`
- Command surface: `artifacts` (list), `events` (list), `hook` (install, uninstall), `loop` (stats), `memory` (set, get, list, delete, gc), `push`, `resume` (--peek, --focus, --project-dir, --limit), `schema` (commands), `status` (--check), `task` (create, begin, complete, get, list, delete, add-dep, remove-dep, set-status, set-priority), `upgrade`
- Valid task statuses: `pending`, `in_progress`, `completed`, `blocked`
- **After code changes**: rebuild binary and update symlink: `go build -o vybe ./cmd/vybe && ln -sf "$(pwd)/vybe" ~/go/bin/vybe`
