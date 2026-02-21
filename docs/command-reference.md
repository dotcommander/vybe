# Command Reference

Purpose: authoritative command and subcommand map for the current `vybe` CLI.

Use this page for full command parity checks. For workflows, use `setup.md`, `common-tasks.md`, and `connect-assistant.md`.

## Top-level commands

- `agent`
- `artifact`
- `brief`
- `events`
- `help`
- `hook`
- `ingest`
- `loop`
- `memory`
- `project`
- `push`
- `resume`
- `schema`
- `session`
- `snapshot`
- `status`
- `task`
- `upgrade`

## Subcommands by command

### `agent`

- `focus`
- `init`
- `status`

### `artifact`

- `add`
- `get`
- `list`

### `events`

- `add`
- `list`
- `summarize`
- `tail`

### `hook`

- `install`
- `uninstall`

### `ingest`

- `history`

### `loop`

- `stats`

### `memory`

- `compact`
- `delete`
- `gc`
- `get`
- `list`
- `query`
- `set`
- `touch`

### `project`

- `create`
- `delete`
- `get`
- `list`

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

All fields except constraints are optional. `task_id` is required when `artifacts` or `task_status` are provided.

### `session`

- `digest`

### `task`

- `add-dep`
- `begin`
- `claim`
- `complete`
- `create`
- `delete`
- `gc`
- `get`
- `heartbeat`
- `list`
- `next`
- `remove-dep`
- `set-priority`
- `set-status`
- `stats`
- `unlocks`

## Direct commands (no subcommands)

- `brief`
- `help`
- `push`
- `resume`
- `schema`
- `snapshot`
- `status`
- `upgrade`

## Verification checklist

Run after changing command wiring:

```bash
go run ./cmd/vybe --help
go run ./cmd/vybe task --help
go run ./cmd/vybe memory --help
go run ./cmd/vybe events --help
go run ./cmd/vybe loop --help
```

Pass condition: every command and subcommand listed above appears in help output.

## Related docs

- `setup.md` for operator setup and baseline loop
- `common-tasks.md` for runnable recipes
- `connect-assistant.md` for integration contract
- `change-vybe.md` for contributor implementation workflow
