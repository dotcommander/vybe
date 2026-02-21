# Command Reference

Purpose: authoritative command and subcommand map for the current `vybe` CLI.

Use this page for full command parity checks. For workflows, use `setup.md`, `common-tasks.md`, and `connect-assistant.md`.

## Top-level commands

- `help`
- `hook`
- `loop`
- `memory`
- `push`
- `resume`
- `status`
- `task`
- `upgrade`

## Subcommands by command

### `hook`

- `install`
- `uninstall`

Note: all other hook subcommands (`checkpoint`, `prompt`, `retrospective`, `retrospective-bg`, `session-end`, `session-start`, `stop`, `subagent-start`, `subagent-stop`, `task-completed`, `tool-failure`, `tool-success`) are hidden/internal and used by assistant integrations only.

### `loop`

- `stats`

### `memory`

- `delete`
- `gc`
- `get`
- `list`
- `set`

### `push`

Atomic batch mutation — combines event, memories, artifacts, and task status change into a single idempotent transaction.

**Flags:**
- `--json` — JSON input payload (alternative: pipe via stdin)
- `--agent` / `VYBE_AGENT` — Agent name (required)
- `--request-id` / `VYBE_REQUEST_ID` — Idempotency key (required)

**Input schema:**
```json
{
  "task_id": "task_...",
  "event": {"kind": "progress", "message": "...", "metadata": "{}"},
  "memories": [{"key": "k", "value": "v", "scope": "global"}],
  "artifacts": [{"file_path": "/path/to/file"}],
  "task_status": {"status": "completed", "summary": "Done"}
}
```

All fields are optional except where noted. `task_id` is required when `artifacts` or `task_status` are provided.

### `resume`

Fetch deltas since last cursor position, build a brief packet, and advance the cursor atomically.

**Flags:**
- `--peek` — Build brief without advancing the cursor (idempotent read)
- `--focus <task-id>` — Override focus task for this resume
- `--project <dir>` — Scope focus selection to a project directory
- `--limit N` — Limit number of recent events returned in the brief

### `status`

Inspect agent and DB state.

**Flags:**
- `--check` — Exit non-zero if agent has no focus task (health check mode)
- `--events` — Include recent events in output
- `--artifacts --task <task-id>` — List artifacts linked to a task
- `--schema` — Print the current DB schema

### `task`

- `add-dep`
- `begin`
- `complete`
- `create`
- `delete`
- `get`
- `list`
- `remove-dep`
- `set-priority`
- `set-status`

## Direct commands (no subcommands)

- `help`
- `push`
- `resume`
- `status`
- `upgrade`

## Verification checklist

Run after changing command wiring:

```bash
go run ./cmd/vybe --help
go run ./cmd/vybe task --help
go run ./cmd/vybe memory --help
go run ./cmd/vybe loop --help
go run ./cmd/vybe status --help
go run ./cmd/vybe resume --help
```

Pass condition: every command and subcommand listed above appears in help output.

## Related docs

- `setup.md` for operator setup and baseline loop
- `common-tasks.md` for runnable recipes
- `connect-assistant.md` for integration contract
- `change-vybe.md` for contributor implementation workflow
- `DECISIONS.md` for command rationale and anti-regression guardrails (see "Command Surface Guardrails (Do Not Regress)")
