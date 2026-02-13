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
