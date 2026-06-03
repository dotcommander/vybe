# vybe

Persistent memory for AI coding agents.

## The Problem

Your agent is halfway through a refactor — six files changed, two to go — and the session dies. Context window limit, crash, timeout, whatever.

Next session starts cold. The agent re-reads files it already understood, re-plans work it already decided on, and redoes changes it already made.

**Agents have no memory that survives a session boundary.** There's nowhere to store what they were doing, what they learned, or what they produced.

`vybe` is that storage layer — a single SQLite file that survives crashes and restores context on restart.

![vybe demo](demo.gif)

## What It Does

On session start, `vybe resume` returns everything the agent needs — its current task, saved memory, recent activity, and linked files.

Between sessions, vybe stores:
- **Tasks** with status, priority, and dependency tracking
- **Events** as an append-only log of everything the agent did
- **Memory** as key-value pairs scoped to global, project, task, or agent — with expiration
- **Artifacts** linking files and outputs to the work that produced them

Sending the same command twice is safe — duplicates are detected and ignored. Multiple agents share the same database without stepping on each other.

## Quick Start

```bash
# 1) install
go install github.com/dotcommander/vybe/cmd/vybe@latest

# 2) one-step setup: config dir, database, hooks, default agent
vybe init

# 3) verify
vybe doctor
```

`vybe init` is idempotent — re-running it is safe and skips already-applied steps. `vybe doctor` confirms the binary is on PATH, hooks are installed, and the database is reachable. Run it any time to validate setup.

## First Task

Set your agent identity once — export `VYBE_AGENT` (or set `default_agent` in `~/.config/vybe/config.yaml`) so you never pass `--agent` again:

```bash
export VYBE_AGENT=claude
```

```bash
# create work
vybe task create --title "Ship feature" --desc "Implement X"

# get current focus + context
vybe resume

# close when done (task ID is returned by create)
vybe done <TASK_ID> --note "<summary>"
```

Sugar verbs cover the common path: `vybe done <id>` (complete), `vybe block <id> --reason "..."` (block), `vybe note <id> "msg"` (log progress), `vybe remember "key=value"` (memory), `vybe focus` (read current focus, no cursor advance).

Omit `--request-id` by default — vybe auto-generates one per call. A freshly-generated id never dedupes (identical to omitting it); pass an explicit STABLE id only when retrying the exact same operation.

If a session crashes, run `vybe resume` and keep going.

## Capabilities

| Capability | What it does |
|------------|-------------|
| **Task lifecycle** | Create, begin, complete, and block tasks with priority and dependencies |
| **Event log** | Append-only log of agent activity — what happened, in order |
| **Scoped memory** | Key-value pairs stored per-project, per-task, or globally — with expiration. **Pins are sticky:** `memory set --pin` enables pinning; subsequent `memory set` calls without `--pin` preserve the pin. Only `memory pin --unpin` can clear it. This protects durable strategic memory from being unpinned by incidental writes |
| **Resume** | Restores the agent's full working context from a single command |
| **Safe retries** | `--request-id` is optional — auto-generated when omitted (at-least-once); pass the same one across retries of an operation for exactly-once dedup |
| **Multi-agent** | Multiple agents share the same database safely |
| **Hook integration** | One-command install for Claude Code and OpenCode |
| **Project scoping** | Group tasks and memory under named projects |
| **Maintenance** | Automatic cleanup of old events — configurable in `config.yaml` |
| **SQLite** | Single file, no server, handles concurrent access out of the box |

See [`docs/`](docs/) for internals: focus selection algorithm, concurrency model, idempotency protocol, event archiving.

## Architecture

```
cmd/vybe/main.go
  ↓
internal/commands/     # CLI layer — parse flags, call actions
  ↓
internal/actions/      # Business logic — orchestrate store calls
  ↓
internal/store/        # SQLite persistence — transactions, retry, conflict resolution
```

**Commands:** `artifacts`, `doctor`, `events`, `hook`, `init`, `memory`, `push`, `resume`, `schema`, `status`, `task`, `upgrade`

See [`docs/`](docs/) for full documentation.

## Documentation

| Doc | Contents |
|-----|----------|
| [`docs/operator-guide.md`](docs/operator-guide.md) | Install/bootstrap plus operational recipes |
| [`docs/agent-contract.md`](docs/agent-contract.md) | Canonical machine I/O and integration contract |
| [`docs/usage-examples.md`](docs/usage-examples.md) | 50 production-ready CLI command examples |
| [`docs/decisions.md`](docs/decisions.md) | Why command-surface choices exist and what must not regress |

## Uninstall

```bash
vybe hook uninstall            # Claude Code
vybe hook uninstall --opencode # OpenCode
```

State is stored in `~/.config/vybe/`. Remove that directory to wipe all data.

## License

MIT
