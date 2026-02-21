# vybe

Durable continuity for AI coding agents.

`vybe` gives autonomous agents crash-safe task tracking, append-only event logs, scoped memory, deterministic resume, and artifact linking — all backed by a single SQLite file. Agents pick up exactly where they left off across sessions without human intervention.

## The Problem

AI coding agents lose context when sessions end. Crashes, context resets, and handoffs between agents mean work gets repeated, forgotten, or abandoned. There is no standard place to store what an agent was doing, what it learned, or what it produced.

`vybe` is that place.

## Features

**Task lifecycle**
- Create, begin, claim, complete, block tasks with priority and dependencies
- Heartbeat and GC to reclaim stale claimed tasks
- Valid statuses: `pending`, `in_progress`, `completed`, `blocked`

**Event log**
- Append-only structured log of all agent activity
- Tail, summarize, and ingest from external sources

**Scoped memory**
- Key-value store scoped to global / project / task / agent
- TTL, compaction, GC, and query support
- PreCompact runs best-effort retrospective (synchronous, rule-based)

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
- Claims with expiry and automatic reclaim

**Hook integration**
- One-command install for Claude Code and OpenCode
- Hooks cover: SessionStart, UserPromptSubmit, PostToolUseFailure, TaskCompleted, PreCompact, SessionEnd
- Bidirectional context injection — resume data flows in, retrospectives extract lessons at compaction

**Internal maintenance**
- Checkpoint/session-end run event summarization and archived-event pruning automatically
- Maintenance policy is configurable in `config.yaml` (`events_retention_days`, `events_prune_batch`, `events_summarize_threshold`, `events_summarize_keep_recent`)
- `vybe status` reports effective maintenance settings

**SQLite-backed**
- WAL mode, single file, no server required
- Busy-timeout and retry logic built in
- Schema introspection via `vybe schema`

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
vybe status
```

If `vybe status` succeeds, setup is done.

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

**Commands:** `hook`, `loop`, `memory`, `push`, `resume`, `status`, `task`, `upgrade`

See [`docs/`](docs/) for full documentation.

## Documentation

| Doc | Contents |
|-----|----------|
| [`docs/setup.md`](docs/setup.md) | Install, bootstrap, and core agent loop |
| [`docs/common-tasks.md`](docs/common-tasks.md) | Copy-paste recipes for common workflows |
| [`docs/connect-assistant.md`](docs/connect-assistant.md) | Integration contract for connecting any assistant |
| [`docs/command-reference.md`](docs/command-reference.md) | Full command and subcommand map |
| [`docs/change-vybe.md`](docs/change-vybe.md) | Contributor guide for making code changes |

## Uninstall

```bash
vybe hook uninstall            # Claude Code
vybe hook uninstall --opencode # OpenCode
```

State is stored in `~/.config/vybe/`. Remove that directory to wipe all data.

## License

MIT
