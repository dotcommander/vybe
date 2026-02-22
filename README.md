# vybe

Durable continuity for AI coding agents.

`vybe` gives autonomous agents crash-safe task tracking, append-only event logs, scoped memory, deterministic resume, and artifact linking — all backed by a single SQLite file. Agents pick up exactly where they left off across sessions without human intervention.

## The Problem

AI coding agents lose context when sessions end. Crashes, context resets, and handoffs between agents mean work gets repeated, forgotten, or abandoned. There is no standard place to store what an agent was doing, what it learned, or what it produced.

`vybe` is that place.

![vybe demo](demo.gif)

## Features

**Task lifecycle**
- Create, begin, complete, and block tasks with priority and dependencies
- Dependency-aware progression via deterministic resume focus selection
- Valid statuses: `pending`, `in_progress`, `completed`, `blocked`

**Event log**
- Append-only structured log of all agent activity
- Query via `events list`, summarize, and ingest from external sources

**Scoped memory**
- Key-value store scoped to global / project / task / agent
- TTL and GC support
- PreCompact runs checkpoint maintenance (event + memory hygiene)

**Deterministic resume**
- 5-rule focus selection algorithm — no ambiguity on restart
- Brief packets deliver task + memory + events + artifacts in one JSON call
- Cursor-based delta tracking per agent

**Idempotent mutations**
- Every mutating command accepts `--request-id`
- Duplicate requests replay the original result safely
- Safe for retries and at-least-once agent execution

**Multi-agent coordination**
- Per-agent state, focus tracking, and heartbeats
- Optimistic concurrency on task and agent state rows
- Retry-safe idempotent mutations for concurrent workers

**Hook integration**
- One-command install for Claude Code and OpenCode
- Hooks cover: SessionStart, UserPromptSubmit, PostToolUseFailure, TaskCompleted, PreCompact, SessionEnd
- Bidirectional context injection — resume data flows into each new session

**Internal maintenance**
- Checkpoint/session-end run event summarization and archived-event pruning automatically
- Maintenance policy is configurable in `config.yaml` (`events_retention_days`, `events_prune_batch`, `events_summarize_threshold`, `events_summarize_keep_recent`)
- `vybe status --check` verifies DB connectivity

**SQLite-backed**
- WAL mode, single file, no server required
- Busy-timeout and retry logic built in
- Schema introspection via `vybe schema commands`

**Project scoping**
- Group tasks and memory under named projects
- Focus project filters memory and task selection

## Quick Start

```bash
# 1) install
go install github.com/dotcommander/vybe/cmd/vybe@latest

# 2) connect your assistant
vybe hook install            # Claude Code
# OR
vybe hook install --opencode # OpenCode

# 3) set agent name
export VYBE_AGENT=worker1

# 4) verify
vybe status --check
```

If `vybe status --check` returns `query_ok=true`, setup is done.

## First Task

```bash
# create work
vybe task create --request-id task_1 --title "Ship feature" --desc "Implement X"

# get current focus + context
vybe resume --request-id resume_1

# close when done
vybe task complete --request-id close_1 --id <TASK_ID> --outcome done --summary "Finished"
```

If a session crashes, run `vybe resume` and keep going.

## Architecture

```
cmd/vybe/main.go
  ↓
internal/commands/     # CLI layer — parse flags, call actions
  ↓
internal/actions/      # Business logic — orchestrate store calls
  ↓
internal/store/        # SQLite persistence — transactions, retry, CAS
```

**Commands:** `artifacts`, `events`, `hook`, `loop`, `memory`, `push`, `resume`, `schema`, `status`, `task`, `upgrade`

See [`docs/`](docs/) for full documentation.

## Documentation

| Doc | Contents |
|-----|----------|
| [`docs/operator-guide.md`](docs/operator-guide.md) | Install/bootstrap plus operational loop recipes |
| [`docs/agent-contract.md`](docs/agent-contract.md) | Canonical machine I/O and integration contract |
| [`docs/contributor-guide.md`](docs/contributor-guide.md) | Contributor workflow for safe code changes |
| [`docs/DECISIONS.md`](docs/DECISIONS.md) | Why command-surface choices exist and what must not regress |

## Uninstall

```bash
vybe hook uninstall            # Claude Code
vybe hook uninstall --opencode # OpenCode
```

State is stored in `~/.config/vybe/`. Remove that directory to wipe all data.

## License

MIT
