# Change vybe

Purpose: help contributors make safe code changes without breaking agent workflows.

## Prerequisites

- Go toolchain installed
- Repo cloned and writable
- You can run `go test ./...` locally

## Main workflow

1. Find the existing command/action/store path before editing.
2. Make the smallest change that fixes the behavior.
3. Keep idempotency and JSON output behavior intact.
4. Update tests when behavior or output changes.

Architecture route for most changes:

- `internal/commands/` parses flags and calls actions
- `internal/actions/` orchestrates business logic
- `internal/store/` handles transactional SQLite persistence

Critical implementation rules:

- Mutations must remain retry-safe with request id dedupe.
- Keep DB writes transactional and append-only event logs intact.
- Use optimistic concurrency (`version` + CAS) where contention exists.
- Avoid `db.Query*` while parent rows are open in SQLite flows.

## Verification

Run before you open a PR:

```bash
gofmt -w ./cmd/vybe ./internal
go test ./...
go vet ./...
go build ./...
```

Pass condition: all commands succeed with no new warnings or failures.

## Related docs

- `../CLAUDE.md` for repository operating constraints
- `setup.md` for operator runtime expectations
- `connect-assistant.md` for machine I/O and idempotency contract
- `command-reference.md` for current command/subcommand surface
