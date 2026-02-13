# Vibe Unknowns Audit

**Date**: 2026-02-13
**Mode**: unknowns (deps, wisdom, ancient, scale, implicit)
**Scouts**: 5 parallel haiku agents + sonnet fusion
**Total Findings**: 26 (8 high, 13 medium, 5 low)

## Executive Summary

This unknowns-mode audit identifies **hidden dependencies, tribal knowledge, legacy patterns, scalability bottlenecks, and implicit contracts** that could cause silent failures, contributor confusion, or performance degradation at scale.

**Critical Insights**:
- **Concurrency bugs exist** in memory canonical deduplication and resume focus projection
- **N+1 query patterns** will break at 10k+ tasks
- **Event kind taxonomy** is scattered across 20+ files with no central registry
- **Dependency assumptions** (goose dialect, SQLite pragmas, external CLIs) are undocumented

## Priority Matrix

| Severity | Count | Focus Areas |
|----------|-------|-------------|
| High | 8 | Concurrency races, N+1 queries, undocumented contracts |
| Medium | 13 | Missing indexes, magic constants, deprecated patterns |
| Low | 5 | Documentation debt, acceptable test patterns |

---

## Critical Findings (High Severity)

### C1. Memory Canonical Deduplication Race Condition

**Category**: implicit
**Severity**: HIGH
**Impact**: Data corruption

**Issue**: `UpsertMemoryWithEventIdempotent` queries for existing memory by `canonical_key` without locking. Two concurrent agents writing the same canonical key will both see no existing row and insert duplicates. Result: two active (`superseded_by=NULL`) entries with identical `(scope, scope_id, canonical_key)`. Reads become nondeterministic.

**Location**: `internal/store/memory.go:141-148` (lookup), `161-170` (insert)

**Root Cause**: No unique constraint on `(scope, scope_id, canonical_key) WHERE superseded_by IS NULL`

**Recommendation**:
```sql
CREATE UNIQUE INDEX idx_memory_canonical_unique
ON memory(scope, scope_id, canonical_key)
WHERE superseded_by IS NULL;
```

**Alternative**: Use `SELECT FOR UPDATE` to lock canonical_key row before insert/update.

---

### C2. N+1 Query Pattern in ListTasks

**Category**: scale
**Severity**: HIGH
**Impact**: Performance bottleneck at 10k+ tasks

**Issue**: `ListTasks()` queries all tasks, then calls `loadTaskDependencies(db, task.ID)` for each task. With 10,000 tasks: 1 + 10,000 = 10,001 queries.

**Location**: `internal/store/tasks.go:349-355`

**Code**:
```go
for _, task := range tasks {
    deps, err := loadTaskDependencies(db, task.ID)  // N additional queries
    if err != nil {
        return nil, fmt.Errorf("failed to load dependencies for task %s: %w", task.ID, err)
    }
    task.DependsOn = deps
}
```

**Recommendation**: Use a single `LEFT JOIN` to fetch all dependencies:
```go
SELECT t.*, td.depends_on_task_id
FROM tasks t
LEFT JOIN task_dependencies td ON td.task_id = t.id
WHERE 1=1  /* filters */
ORDER BY t.created_at DESC
```
Then post-process in Go to group dependencies by task ID.

---

### C3. UnblockDependentsTx Cascade Loop Without Limits

**Category**: scale
**Severity**: HIGH
**Impact**: Transaction timeout with 10k+ blocked tasks

**Issue**: When a task completes, `UnblockDependentsTx()` loads ALL dependent tasks and calls `HasUnresolvedDependenciesTx()` for each (O(N) inner queries). No batching or limit.

**Location**: `internal/store/task_deps.go:354-396`

**Recommendation**:
1. Add configurable limit (e.g., max 100 at a time)
2. Batch-update with single SQL using CTE:
   ```sql
   UPDATE tasks SET status = 'pending', blocked_reason = NULL
   WHERE id IN (
     SELECT DISTINCT td.task_id
     FROM task_dependencies td
     WHERE td.depends_on_task_id = ?
       AND NOT EXISTS (
         SELECT 1 FROM task_dependencies td2
         JOIN tasks dep ON dep.id = td2.depends_on_task_id
         WHERE td2.task_id = td.task_id
           AND td2.depends_on_task_id != ?
           AND dep.status != 'completed'
       )
   );
   ```
3. Consider background processing for large cascades

---

### C4. Resume Focus Projection Race

**Category**: implicit
**Severity**: HIGH
**Impact**: Context inconsistency

**Issue**: `computeResumePacket()` reads `agent_state.FocusProjectID` outside any transaction, then `opts.ProjectDir` optionally overrides it. A concurrent agent can change `focus_project_id` via `SetAgentFocusProject()`. The override is applied to brief computation but never persisted atomically with state update. Response context becomes inconsistent with persisted state.

**Location**: `internal/actions/resume.go:51-104` (computeResumePacket), `283-344` (ResumeWithOptions)

**Recommendation**: Move `opts.ProjectDir` validation and project-focus logic into the idempotent transaction so `computeResumePacket` uses only the `agent_state` that will be committed. Or document that `--project` scopes brief retrieval only; do not mutate `focus_project_id` unless explicitly passed to `UpdateAgentStateAtomicWithProject`.

---

### C5. Idempotency Transaction Contract Unenforced

**Category**: implicit
**Severity**: HIGH
**Impact**: Idempotency corruption

**Issue**: `BeginIdempotencyTx` comment states callers must execute begin+side-effects+complete in ONE transaction. `RunIdempotentWithRetry` does this correctly, but the contract is only documented in comments. If a future refactor extracts `BeginIdempotencyTx` into separate transaction context, system could leave idempotency rows with empty `result_json`, causing `ErrIdempotencyInProgress` for unrelated concurrent requests.

**Location**: `internal/store/idempotency.go:15-16` (comment), `52-56` (defensive check)

**Recommendation**:
1. Make `BeginIdempotencyTx` private and expose only `RunIdempotent[WithRetry]`
2. Add assertion/panic if `result_json` is empty on first load
3. Document this invariant in `CLAUDE.md` under "Key Patterns"

---

### C6. Event Kind Taxonomy Scattered

**Category**: wisdom
**Severity**: HIGH
**Impact**: Contributor confusion, duplicate event kinds

**Issue**: 20+ event kinds (`task_created`, `memory_upserted`, `user_prompt`, `reasoning`, etc.) are spread across `actions/` and `store/` files without a canonical list. New contributors will invent duplicates or miss distinctions between system kinds (prefixed) and agent kinds.

**Location**: `internal/store/events.go:18` (validates but doesn't enumerate); event kinds created ad-hoc in `actions/*.go`, `commands/*.go`

**Known Kinds**:
- System: `task_created`, `task_deleted`, `task_status`, `task_heartbeat`, `task_dependency_added`, `task_dependency_removed`, `project_created`, `project_deleted`, `artifact_added`, `agent_focus`, `agent_project_focus`, `memory_upserted`, `memory_reinforced`, `memory_compacted`, `memory_delete`, `memory_gc`, `memory_touched`, `events_summary`
- Agent: `progress`, `reasoning`, `tool_failure`, `user_prompt`, `note`

**Recommendation**: Create `internal/models/events.go` with constants:
```go
const (
    EventKindTaskCreated       = "task_created"
    EventKindMemoryUpserted    = "memory_upserted"
    EventKindUserPrompt        = "user_prompt"
    // ... all kinds
)
```
Export full taxonomy in godoc. Use constants throughout instead of string literals.

---

### C7. Goose SQLite Dialect Hardcoded

**Category**: deps
**Severity**: HIGH
**Impact**: Migration failure on driver/goose version changes

**Issue**: Code uses `modernc.org/sqlite` driver (DSN: `sql.Open("sqlite", ...)`) but goose dialect is hardcoded to `"sqlite3"` (`migrate.go:26`). Goose v3 expects driver registration names to match dialect. This works now but creates fragile coupling: if DSN driver name or goose version changes, migrations fail silently with cryptic errors.

**Location**: `internal/store/migrate.go:26`

**Recommendation**: Document in `CLAUDE.md`: "Goose dialect 'sqlite3' is used despite DSN driver name 'sqlite' due to goose v3 internal dialect mapping. This coupling is intentional but fragile." Add migration version check test that validates goose dialect at startup.

---

### C8. Memory Scope Agent Exclusion Undocumented

**Category**: wisdom
**Severity**: HIGH
**Impact**: Agent isolation breakage

**Issue**: Memory scopes are `global`, `task`, `project`, `agent`. In `resume.go GetRelevantMemory()`, agent-scoped memory is explicitly excluded from brief. This is deliberate (agent state is per-agent, not shared across sessions) but not explained anywhere. New contributors may add agent-scoped memory to brief and break agent isolation.

**Location**: `internal/store/memory.go:630-649` (ValidateMemoryScope), `internal/store/resume.go:657-695` (query excludes `scope='agent'`)

**Recommendation**: Add explicit comment in `ValidateMemoryScope` explaining agent scope lifecycle and why it's excluded from brief. Document in `CLAUDE.md` that agent-scoped memory is write-only for current session and never carried forward.

---

## High-Impact Findings (Medium Severity)

### M1. Missing Index on events(kind, archived_at)

**Category**: scale
**Severity**: MEDIUM
**Impact**: Full table scans on 100k+ event logs

**Issue**: `FetchRecentUserPrompts()` and `FetchPriorReasoning()` filter by `kind='user_prompt'/'reasoning' AND archived_at IS NULL`. No composite index exists. Current indexes: `idx_events_archived_at`, `idx_events_agent_name`—none accelerate kind-based filtering.

**Location**: `internal/store/resume.go:320-434`

**Recommendation**:
```sql
CREATE INDEX idx_events_kind_archived
ON events(kind, archived_at, id);
```

---

### M2. Memory Quality Thresholds Hardcoded

**Category**: wisdom
**Severity**: MEDIUM
**Impact**: Magic numbers buried in SQL

**Issue**: Resume memory filtering applies `confidence >= 0.3 OR age < 14 days`. Thresholds are hardcoded in `resume.go:669-690`. No constants or documentation. If future features adjust memory aging, these buried magic numbers create bugs.

**Location**: `internal/store/resume.go:669-670`

**Recommendation**: Extract as package-level constants:
```go
const (
    MinMemoryConfidence = 0.3
    MemoryRecencyDays   = 14
)
```
Add godoc: "confidence<0.3 + old=low signal noise; recency ensures recent context trumps old." Document in `CLAUDE.md` under Memory Lifecycle.

---

### M3. Blocked Reason Format Implicit Prefix

**Category**: wisdom
**Severity**: MEDIUM
**Impact**: Resume logic breakage

**Issue**: `blocked_reason` column has two semantic types: (1) `"dependency"` (auto-assigned), (2) `"failure:..."` (prefix-based). Resume rule 1.5 uses `strings.HasPrefix` to distinguish. This protocol is never documented. Agents may set `blocked_reason` without `"failure:"` prefix and break resume logic.

**Location**: `internal/store/task_delete.go:105`, `internal/store/resume.go:113`, `internal/commands/run.go:321`

**Recommendation**: Document in `CLAUDE.md` that `blocked_reason` format: empty/`'dependency'` for dependency blocks, `'failure:<reason>'` for failure blocks. Add constants:
```go
const (
    BlockedReasonDependency = "dependency"
    BlockedReasonPrefix     = "failure:"
)
```
Use throughout instead of string literals.

---

### M4. QueryMemory LIKE Without Index

**Category**: scale
**Severity**: MEDIUM
**Impact**: Full table scan on 50k+ memory rows

**Issue**: `QueryMemory()` uses `LIKE` on `key` and `canonical_key` columns. SQLite cannot efficiently index arbitrary wildcard patterns. Current index `idx_memory_scope_canonical_expires` doesn't accelerate LIKE searches.

**Location**: `internal/store/memory.go:796`

**Recommendation**:
1. Move pattern matching to Go post-fetch (slower but bounded)
2. Document that pattern MUST be prefix-anchored (`'foo%'` not `'%foo'`)
3. Use FTS virtual table for full-text search if wildcards required

---

### M5. Hardcoded 5-Second busy_timeout

**Category**: scale
**Severity**: MEDIUM
**Impact**: Transaction timeout under high contention

**Issue**: `PRAGMA busy_timeout=5000` is set once at initialization. With `MaxOpenConns=1`, any transaction lasting >5s will timeout waiting clients. High-volume event logging (>100 events/sec) or bulk dependency updates can exceed this window.

**Location**: `internal/store/db.go:44`

**Recommendation**:
1. Document timeout assumption in `CLAUDE.md`
2. Make configurable via env var (`VIBE_BUSY_TIMEOUT_MS`)
3. For bulk operations, batch and commit frequently to stay under 5s window
4. Add observability to measure actual transaction latencies

---

### M6. Resume CAS Conflict and Stale Focus

**Category**: implicit
**Severity**: MEDIUM
**Impact**: Visibility gap between computed and persisted focus

**Issue**: In `ResumeWithOptions`, if version CAS fails, update is skipped. But `computeResumePacket` was executed before tx, so `DetermineFocusTask` used stale `agent_state`. Response reflects pre-CAS focus, not post-CAS reality.

**Location**: `internal/actions/resume.go:283-344`, particularly `309-332`

**Recommendation**: Document this behavior: "Resume clears focus on claim contention or skips update on version conflict; response reflects computed state, not final persisted state." Or move `DetermineFocusTask` into idempotent tx. Include `LoadAgentCursorAndFocusTx` result in response to show actual persisted focus.

---

### M7. Task Claim/Focus Decouple

**Category**: implicit
**Severity**: MEDIUM
**Impact**: Agent resumes with unclaimed task in focus

**Issue**: In `startTaskAndFocusTx`, sequence is: (1) ensure agent_state, (2) update task status, (3) set agent focus, (4) claim task. If `ClaimTaskTx` fails at step 4, `agent_state.focus_task_id` was already updated. Agent resumes with unclaimed task. A concurrent agent may claim it, leaving two agents' focus pointers at same task.

**Location**: `internal/store/task_start.go:34-93`, especially `89-91`

**Recommendation**: Reorder: claim first (before status/focus update). Or add post-resume invariant check: after building brief, verify `focused_task.claimed_by == agent` or `claim_expires_at < now`. Or clear focus atomically in claim-contention handler.

---

### M8. ID Generation Suffix Length Undocumented

**Category**: wisdom
**Severity**: MEDIUM
**Impact**: Collision risk if changed without awareness

**Issue**: ID generation uses `generatePrefixedID(prefix)` → `prefix_unixnano_hexstring`. Suffix is 6 random bytes (12 hex chars). Documented in code comments but suffix length never specified. If changed to `[4]byte`, collision risk increases.

**Location**: `internal/store/id.go:10-18`, `internal/store/tasks.go:375`

**Recommendation**: Document in `CLAUDE.md`: ID formats: `task_{unix_nano_nanos}_{12-hex-suffix}` where suffix=6 bytes random. Add test in `store/id_test.go` checking suffix length. Clarify request-id has no enforced format but examples show `{command}_{timestamp}` or `{command}_$RANDOM`.

---

### M9. Status Transition Rules Intentionally Unrestricted

**Category**: wisdom
**Severity**: MEDIUM
**Impact**: Future contributors may add unwanted validation

**Issue**: Task status can transition any→any (`pending`, `in_progress`, `completed`, `blocked`). Mentioned once in `CLAUDE.md:193` as "intentionally unrestricted for agent flexibility." Code comments never explain. New contributors may add status validation thinking it's a bug.

**Location**: `internal/actions/task.go` (validTaskStatusOptions), `CLAUDE.md:193`

**Recommendation**: Add godoc in `internal/actions/task.go TaskSetStatusIdempotent`: "Status transitions are intentionally unrestricted (any→any) to give agents flexibility for error recovery. Agents must manage their own invariants." Prevents future contributors from enforcing `pending→in_progress→completed` rules.

---

### M10. SQLite Pragma Version Dependencies

**Category**: deps
**Severity**: MEDIUM
**Impact**: Undocumented minimum version requirement

**Issue**: Code sets `PRAGMA busy_timeout=5000ms`, `PRAGMA synchronous=NORMAL`, `PRAGMA journal_mode=WAL`. No documentation of minimum SQLite version (WAL added in 3.7.0, 2010). `modernc.org/sqlite` wraps SQLite 3.46+, so not a practical issue, but trade-offs (durability vs. performance) never explained.

**Location**: `internal/store/db.go:42-48`

**Recommendation**: Document in `CLAUDE.md`: "Requires SQLite 3.7.0+ (WAL support) and modernc.org/sqlite v1.45+. `PRAGMA busy_timeout=5000` allows 5s for concurrent writers; `PRAGMA synchronous=NORMAL` sacrifices fsync durability for write speed but maintains transaction isolation via WAL."

---

### M11. External Tool Requirements for Upgrade

**Category**: deps
**Severity**: MEDIUM
**Impact**: Confusing error on missing git/go

**Issue**: `vibe upgrade` relies on `git` and `go` binaries in PATH. No validation or helpful error if missing. Help text mentions "Requires git and go on PATH" but not enforced at runtime. Users get generic `exec.Command` error instead of clear message.

**Location**: `internal/commands/upgrade.go:18, 21-26, 52, 61, 86`

**Recommendation**: Add early validation: check for `git` and `go` via `exec.LookPath` before attempting upgrade. If missing, return clear error with installation instructions. Or document limitation prominently in `README.md`.

---

### M12. CLI Agent Dispatch Dependency

**Category**: deps
**Severity**: MEDIUM
**Impact**: Silent degradation

**Issue**: Session command (`llm/cli.go`) attempts to dispatch prompts to `'claude'` or `'opencode'` CLI tools. If neither found in PATH, `NewRunner` returns nil silently. Callers should check for nil, but no documented contract. Feature degrades without error.

**Location**: `internal/llm/cli.go:20-27`

**Recommendation**: Document in `CLAUDE.md`: "Session extraction requires claude or opencode CLI tools in PATH. If unavailable, session context extraction degrades gracefully (no error, just skipped). For Claude Code integration, ensure 'claude' binary is installed."

---

### M13. Error String Matching Assumptions

**Category**: deps
**Severity**: MEDIUM
**Impact**: Fragile error detection

**Issue**: Idempotency and retry logic match error strings (`'UNIQUE constraint failed'`, `'database is locked'`) from `modernc.org/sqlite`. Format depends on SQLite and wrapper version. If modernc.org/sqlite updates error messages in major version, constraint detection silently breaks.

**Location**: `internal/store/idempotency.go:87-90`, `internal/store/retry.go:47-48`

**Recommendation**: Document in `CLAUDE.md`: "Error detection relies on modernc.org/sqlite v1.45+ error message format. Pin to v1.45.x series or update string matchers on version bump." Add version constraint comment above `isUniqueConstraintErr` and `isRetryableError`.

---

## Low-Priority Findings

### L1. Deprecated --actor and --name Flags

**Category**: ancient
**Severity**: LOW
**Impact**: None (backward compat maintained)

**Issue**: Original `--actor` and `--name` flags deprecated in favor of `--agent`. Old flags still wired and functional via fallback logic. `MarkDeprecated()` calls present but flags remain fully functional.

**Location**: `internal/commands/root.go:52-53`, `internal/commands/agent.go:134-135`, `internal/commands/actor.go:16-26`

**Recommendation**: Phased removal: (1) Keep fallback for 2+ releases, (2) In major version, hard-fail with upgrade message. Document in CHANGELOG when removed.

---

### L2. OpenCode Plugin Duplication

**Category**: ancient
**Severity**: LOW
**Impact**: Version skew

**Issue**: OpenCode bridge plugin originally embedded in `internal/commands/opencode_bridge_plugin.js` (JavaScript). Canonical maintained version now exists in `examples/opencode/opencode-vibe-plugin.ts` (TypeScript). Hook install uses embedded JS source, not examples TS file—users get older version.

**Location**: `internal/commands/opencode_bridge_plugin.js`, `internal/commands/hook_install.go:18-21`

**Recommendation**: Migrate embedded JS to match TS version features, test, update, then archive or delete examples TS. Or keep examples as documentation reference and delete old embedded JS after confirming no active users.

---

### L3. setupTestDBWithCleanup Helper

**Category**: ancient
**Severity**: LOW
**Impact**: None (acceptable pattern)

**Issue**: Test helper in `internal/actions/testing.go` creates temp DB and cleanup callback. Cleanup only calls `db.Close()`. Since `t.TempDir()` handles file deletion and `Close()` just releases handles, callback does minimal work. Used 26+ times but could be inlined.

**Location**: `internal/actions/testing.go:11-27`

**Recommendation**: Evaluate: (1) If cleanup always does only `db.Close()`, consider inlining, (2) If `t.TempDir()` sufficient, remove callback. Keep only if future cleanup needs require it.

---

### L4. Tutorial Greet Command Removal

**Category**: ancient
**Severity**: LOW
**Impact**: None (documented intentional removal)

**Issue**: `CLAUDE.md` documents that tutorial greet command/action was removed to keep CLI surface minimal. No traces remain in current codebase.

**Location**: `CLAUDE.md` (Operational Context section)

**Recommendation**: No action—documented decision. If similar tutorial commands added in future, reference this pattern. Keep note as institutional knowledge.

---

### L5. Direct db.Begin in Tests

**Category**: ancient
**Severity**: LOW
**Impact**: None (acceptable for tx behavior tests)

**Issue**: Store package has centralized `Transact()` helper, but `internal/store/idempotency_test.go` uses raw `db.Begin()` calls. Acceptable for unit tests that explicitly test transaction behavior.

**Location**: `internal/store/idempotency_test.go`

**Recommendation**: Acceptable in test code where transaction behavior is under test. If new tests added, prefer `Transact()` unless explicitly testing Begin/Commit/Rollback. No refactoring required.

---

## Actionable Recommendations

### Immediate (Ship-Blockers)

1. **Add unique index for memory canonical deduplication** (C1)
   ```sql
   CREATE UNIQUE INDEX idx_memory_canonical_unique
   ON memory(scope, scope_id, canonical_key)
   WHERE superseded_by IS NULL;
   ```

2. **Refactor ListTasks to use single JOIN** (C2)

3. **Add batching/limits to UnblockDependentsTx** (C3)

4. **Fix resume focus projection race** (C4)

### High Priority (Before 1.0)

5. **Make BeginIdempotencyTx private** (C5)

6. **Create event kind constants file** (C6)

7. **Document goose dialect coupling** (C7)

8. **Document agent scope exclusion** (C8)

9. **Add composite index for event kind filtering** (M1)

10. **Extract memory quality threshold constants** (M2)

### Medium Priority (Quality Improvements)

11. **Document blocked_reason format** (M3)

12. **Add pattern validation for QueryMemory** (M4)

13. **Make busy_timeout configurable** (M5)

14. **Reorder claim/focus in startTaskAndFocusTx** (M7)

15. **Document ID suffix length** (M8)

### Low Priority (Documentation/Cleanup)

16. **Plan deprecation removal for --actor/--name** (L1)

17. **Consolidate OpenCode plugin versions** (L2)

---

## Testing Recommendations

### Add Integration Tests

1. **Memory canonical race test**: Two concurrent goroutines upserting same canonical_key
2. **Resume focus projection test**: Concurrent resume with --project override
3. **Task claim/focus test**: Claim contention after focus set
4. **ListTasks scale test**: Benchmark with 10k tasks + dependencies

### Add Regression Tests

5. **Event kind validation**: Ensure all system event kinds use constants
6. **Memory quality filtering**: Verify confidence/recency thresholds
7. **Blocked reason format**: Test `"dependency"` vs `"failure:..."` handling
8. **ID generation**: Verify 12-hex-char suffix length

---

## Conclusion

This unknowns audit revealed **8 high-severity issues** requiring immediate attention (concurrency races, N+1 queries, undocumented contracts) and **13 medium-severity issues** that should be addressed before 1.0 (missing indexes, magic constants, implicit protocols).

**Key Takeaways**:

1. **Concurrency bugs exist**: Memory canonical deduplication and resume focus projection have race conditions
2. **Performance bottlenecks**: N+1 queries in ListTasks and UnblockDependentsTx will fail at scale
3. **Documentation gaps**: Event taxonomy, memory semantics, and dependency assumptions are tribal knowledge
4. **Fragile coupling**: Goose dialect, error string matching, and external CLI dependencies are undocumented

**Next Steps**:

1. Create vibe tasks for high-severity findings
2. Add unique index for memory canonical deduplication (ship-blocker)
3. Refactor N+1 query patterns
4. Document all implicit contracts in CLAUDE.md
5. Add integration tests for concurrency scenarios
