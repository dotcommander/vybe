# Contributor Guide

Purpose: make safe code changes without breaking autonomous agent workflows.

## Before editing

1. Find the existing command -> action -> store path.
2. Read `DECISIONS.md` guardrails before command-surface changes.
3. Keep diffs small and behavior-preserving unless intentionally changing contract.

## Architecture route

- `internal/commands/` parses flags and calls actions
- `internal/actions/` orchestrates business logic
- `internal/store/` handles transactional SQLite persistence

## Non-negotiable implementation rules

- Keep idempotent mutation behavior intact (`--request-id` + dedupe).
- Keep JSON envelope contract stable (`stdout` machine data, `stderr` diagnostics).
- Keep DB writes transactional and append-only event semantics intact.
- Use optimistic concurrency (`version` + CAS) where contention exists.
- Avoid `db.Query*` while parent rows are open in SQLite flows.

## `nolint` policy

- Prefer fixing the code over adding `//nolint`.
- If suppression is required, scope it to explicit rules (`//nolint:gosec`) with a short reason.
- Do not use blanket suppressions (`//nolint` without rule names).
- Remove stale suppressions when touching a file.
- Treat `gosec` suppressions as trust-boundary declarations; reason must state why input is trusted.

## Command-surface changes

When commands/flags/subcommands change:

1. Update docs and examples in the same change set.
2. Verify command surface with `vybe schema commands`.
3. Add or update tests for changed behavior.
4. Update `DECISIONS.md` when introducing or modifying guardrails.

## Verification

Run before opening a PR:

```bash
gofmt -w ./cmd/vybe ./internal
go test ./...
go vet ./...
go build ./...
golangci-lint run ./...
go run ./cmd/vybe schema commands >/dev/null
```

Pass condition: all commands succeed with no new warnings/failures, and schema output is valid.

## Related docs

- `operator-guide.md` for runtime/operator expectations
- `agent-contract.md` for integration and machine I/O contract
- `DECISIONS.md` for command-surface guardrails and rationale
