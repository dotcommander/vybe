# Audit: internal/commands/

**Date:** 2026-02-13
**Scope:** internal/commands/*.go (33 files, ~5636 lines production, 191 functions)
**Mode:** Comprehensive (flow, query, concurrency, performance)
**Scouts:** 4 parallel haiku agents → sonnet fusion

---

## Executive Summary

**Critical Issues:** 0
**High Issues:** 0
**Total Findings:** 0 (above threshold)

This is a thin, well-structured CLI layer. All 33 files follow a consistent pattern: parse flags → resolve agent/requestID → `withDB` → call action → print JSON. Hook handlers correctly follow the "must never block Claude Code" contract. The package delegates all business logic to `internal/actions/` and all persistence to `internal/store/`, keeping the command layer focused on I/O marshaling.

---

## What Was Checked

| Area | Result |
|------|--------|
| **Flow** | 17 subcommands wired in root.go. Entry points clear. All mutating commands require `--agent` + `--request-id`. |
| **Query** | No direct SQL. All DB access via `withDB()` → `actions.*` → `store.*`. Connection lifecycle correct. |
| **Concurrency** | `sync.Once` for hook cache (correct). `sync/atomic` for hook sequence counter (correct). No shared mutable state in command handlers. |
| **Performance** | Hook payloads <1KB. All hot paths (hooks) have timeouts. Cold paths (ingest, tail) have configurable limits. |
| **Security** | Hook command strings built from hardcoded subcommands (not user input). Stdin capped at 1MB. No path traversal. |
| **Patterns** | `cmdErr()` wraps errors with structured slog. `printedError` prevents Cobra double-printing. `resolveActorName()` has 4-level precedence. |

---

## Observations (Below Threshold — Informational Only)

### 1. Non-Atomic Settings File Mutation (Score: 8)

`hook_install.go:378,413` — `readSettings()` then `writeSettings()` is a TOCTOU race. Two concurrent `vybe hook install` invocations could clobber each other's changes. Mitigated by: (a) manual one-time setup command, (b) idempotent `upsertVybeHookEntry` — re-running install self-heals, (c) no observed production impact. Same pattern in uninstall path (lines 531-591).

### 2. time.After Timer Leak in spawnAgent (Score: 7)

`run.go:306` — `time.After(timeout)` creates a timer that won't be GC'd until it fires. With 10m default timeout and `maxTasks=10`, at most 10 timers (~2KB total) accumulate. The process exits after the loop, reclaiming all. Fix would be `time.NewTimer` + `timer.Stop()` but practical impact is negligible.

### 3. Per-Entry Transactional Inserts in Ingest (Score: 7)

`ingest.go:117-149` — Each history entry gets its own idempotent insert (individual transaction). With 5000+ entries, creates 5000+ transactions. Mitigated by: (a) one-time import operation, (b) deterministic request IDs make re-runs safe, (c) configurable batch size. A bulk insert would be faster but would lose per-entry idempotency guarantees.

### 4. Silent UserHomeDir Error (Score: 4)

`hook_install.go:48-49` — `claudeSettingsPath()` and `opencodePluginPath()` ignore `os.UserHomeDir()` errors, producing relative paths if HOME is unset. Only affects extreme environments (containers without HOME). Same pattern at line 54.

### 5. Events Tail Has No Graceful Shutdown (Score: 3)

`events.go:149-208` — Infinite poll loop with no context cancellation or signal handling. Standard Go SIGINT handling terminates the process, so this works in practice. Would only matter if embedded as a library (not the case).

---

## Architecture Notes

### Consistent Command Pattern

All 33 files follow this template:

```go
func newXxxCmd() *cobra.Command {
    cmd := &cobra.Command{
        RunE: func(cmd *cobra.Command, args []string) error {
            // 1. Parse flags
            // 2. Resolve agent name (requireActorName or resolveActorName)
            // 3. Resolve request ID (requireRequestID)
            // 4. withDB(func(db *DB) error { ... })
            // 5. output.PrintSuccess(resp)
        },
    }
    // Register flags
    return cmd
}
```

### Hook Contract

All 7 hook handlers follow "must never block Claude Code":
- Errors logged via `slog.Warn`/`slog.Error`, never returned
- `SilenceUsage: true, SilenceErrors: true` on all hook commands
- Timeouts set per-hook (2-15 seconds)

### Notable Abstractions

| Helper | Location | Purpose |
|--------|----------|---------|
| `withDB()` | dbutil.go:30 | Open DB, run closure, close |
| `cmdErr()` | dbutil.go:44 | Structured error with slog |
| `resolveActorName()` | actor.go:11 | 4-level agent name precedence |
| `requireRequestID()` | request_id.go:14 | Enforce idempotency at CLI boundary |
| `hookRequestID()` | hook.go:105 | Generate unique hook request IDs |
| `appendEventWithFocusTask()` | hook.go:200 | Resolve focus task + append event |

---

## No Tasks Created

No findings met the Critical (15+) or High (10-14) threshold.

---

## Metrics

- **Files analyzed:** 33
- **Lines of code:** ~5636 (production)
- **Scouts deployed:** 4 (flow, query, concurrency, performance)
- **Total findings:** 14 (pre-filter from scouts)
- **Critical/High:** 0 (post-fusion calibration)
- **Highest score:** 8 (settings file race — mitigated)

---

## Related Files

- `internal/actions/*.go` — business logic (see `actions.md`)
- `internal/store/*.go` — persistence layer
- `internal/models/*.go` — domain types
- `internal/output/*.go` — JSON output helpers
