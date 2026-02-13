# Audit: internal/actions/

**Date:** 2026-02-13
**Scope:** internal/actions/*.go (18 files, 735 lines)
**Mode:** Comprehensive (flow, query, concurrency, performance)
**Scouts:** 4 parallel haiku agents → sonnet fusion

---

## Executive Summary

**Critical Issues:** 3 (score 15+)
**High Issues:** 7 (score 10-14)
**Total Findings:** 10

The most severe issue is a **TOCTOU race in resume operations** (severity 25 compound): focus determination happens outside transaction boundaries, creating a window where tasks can be claimed/completed by other agents before the current agent attempts to claim. This causes stale brief packets and unclaimed task returns.

The second critical issue is **N+1 query pattern in lesson persistence** (severity 18 compound): `persistLessons()` executes 10+ individual database transactions instead of batching, multiplying lock contention and roundtrips.

---

## Critical Findings (15+)

### 1. Resume TOCTOU + Stale Brief Compound (Severity: 25)

**Files:**
- `internal/actions/resume.go:319` (TOCTOU race)
- `internal/actions/resume.go:318` (effectiveFocus mutation)
- `internal/actions/resume.go:345` (stale brief packet)

**Root Cause:** Focus determination (`computeResumePacket()`, lines 49-114) executes outside transaction boundaries. Between focus determination and claim attempt (line 323), another agent can claim or complete the task. When `ClaimTaskTx` fails with `ErrClaimContention`, `effectiveFocus` is cleared (line 327), but `pkt.brief` was already computed with the original focus. Post-hoc reconciliation (lines 345-350) nulls the task in brief but doesn't rebuild it, creating inconsistent state.

**Impact:**
- Agent receives brief with task it failed to claim
- Cursor advances without valid focus, losing sync
- Prompt references unclaimed task, confusing agents

**Fix:**
```go
// Move focus determination inside transaction
err = store.Transact(db, func(tx *sql.Tx) error {
    // 1. Load fresh state inside tx
    state, err := store.LoadOrCreateAgentStateTx(tx, agentName)
    if err != nil { return err }

    // 2. Fetch deltas inside tx
    deltas, err := store.FetchEventsSinceTx(tx, state.LastSeenEventID, opts.EventLimit, focusProjectID)
    if err != nil { return err }

    // 3. Determine focus atomically
    focusTaskID, err := store.DetermineFocusTaskTx(tx, agentName, state.FocusTaskID, deltas, focusProjectID)
    if err != nil { return err }

    // 4. Claim before persisting
    if focusTaskID != "" {
        if claimErr := store.ClaimTaskTx(tx, agentName, focusTaskID, 5); claimErr != nil {
            if errors.Is(claimErr, store.ErrClaimContention) {
                focusTaskID = "" // Clear focus on contention
            } else {
                return claimErr
            }
        }
    }

    // 5. Update state atomically
    return store.UpdateAgentStateAtomicTx(tx, agentName, newCursor, focusTaskID)
})

// 6. Build brief AFTER transaction with authoritative focus
brief, err := store.BuildBrief(db, effectiveFocus, focusProjectID, agentName)
```

**Priority:** P0 — Correctness violation

---

### 2. N+1 Query + Silent Error Swallowing in Lesson Persistence (Severity: 18)

**Files:**
- `internal/actions/session.go:152` (N+1 loop)
- `internal/actions/session.go:189` (error swallowing)
- `internal/actions/session.go:274` (unverified persistence)

**Root Cause:** `persistLessons()` calls `MemoryUpsertIdempotent()` in a loop (lines 162-193), generating one transaction per lesson. With 10 lessons × ~3 queries each, this creates 30+ database roundtrips. Upsert failures are logged but swallowed (line 190: `continue`), making `LessonsCount` misleading.

**Impact:**
- 10× database contention vs. single batch
- Silent data loss on partial failures
- Callers can't detect incomplete persistence

**Fix:**
```go
// 1. Create batch store function
func (s *Store) BulkUpsertMemoryWithEventsIdempotent(
    db *sql.DB, agentName, requestID string,
    lessons []MemoryLesson,
) (eventIDs []int64, err error) {
    return RunIdempotent(db, agentName, requestID, "memory.bulk_upsert", func(tx *sql.Tx) ([]int64, error) {
        var ids []int64
        for _, lesson := range lessons {
            eventID, _, _, _, err := UpsertMemoryWithEventTx(tx, agentName, lesson.Key, lesson.Value, ...)
            if err != nil {
                return nil, err // Fail fast, rollback entire batch
            }
            ids = append(ids, eventID)
        }
        return ids, nil
    })
}

// 2. Update persistLessons
func persistLessons(db *sql.DB, agentName, requestIDPrefix, projectID string, lessons []Lesson) ([]int64, error) {
    memLessons := make([]MemoryLesson, 0, len(lessons))
    for _, l := range lessons {
        memLessons = append(memLessons, MemoryLesson{
            Key: truncate(l.Key, 64),
            Value: truncate(l.Value, 256),
            Scope: l.Scope,
            ScopeID: projectID,
            Confidence: confidenceMap[l.Type],
        })
    }
    return store.BulkUpsertMemoryWithEventsIdempotent(db, agentName, requestIDPrefix, memLessons)
}
```

**Priority:** P1 — Performance + data integrity

---

### 3. CAS Retry Without Claim Re-validation (Severity: 15)

**File:** `internal/actions/task.go:196`

**Root Cause:** `TaskSetStatus()` implements optimistic concurrency with 3 retries (line 158). Side effects (release claim line 173, unblock dependents line 179) execute inside the transaction but AFTER status update. On version conflict retry, the function doesn't re-check task ownership — if another agent claimed the task between retries, this agent still triggers `UnblockDependentsTx`, violating ownership invariants.

**Impact:**
- Agent can unblock dependents for tasks it doesn't own
- Claim contention can cause double-unblock
- Orphaned claims if release fails silently (line 174: `_ = ReleaseTaskClaimTx`)

**Fix:**
```go
// Inside the transaction (after line 161)
owned, err := store.IsTaskOwnedByAgentTx(tx, agentName, taskID)
if err != nil {
    return fmt.Errorf("failed to verify ownership: %w", err)
}
if !owned && (status == completedStatus || status == "blocked") {
    return fmt.Errorf("cannot release claim for task not owned by agent")
}

// Log claim release failures
if status == completedStatus || status == "blocked" {
    if releaseErr := store.ReleaseTaskClaimTx(tx, agentName, taskID); releaseErr != nil {
        slog.Warn("failed to release task claim", "task", taskID, "agent", agentName, "error", releaseErr)
        // Optional: return error instead of continuing
    }
}
```

**Priority:** P1 — Ownership invariant violation

---

## High Findings (10-14)

### 4. Non-Atomic Claim-Then-Persist Pattern (Severity: 14)

**File:** `internal/actions/resume.go:319`

**Description:** After computing resume packet outside transaction (line 305), `ResumeWithOptions` enters transaction to claim focus task (line 323) then update agent state (lines 335-337). Between packet computation and claim, another agent could complete/unblock the task. Claim failure clears focus but advances cursor, losing synchronization.

**Fix:** See Finding #1 (same root cause, compound issue).

---

### 5. Unclaimed Task Return Without Heartbeat (Severity: 14)

**File:** `internal/actions/resume.go:319`

**Description:** State update transaction only executes if cursor/focus changed (line 319 condition). When no changes detected, no claim is attempted and no heartbeat is issued. Existing claimed task lease expires without refresh.

**Fix:**
```go
// Always attempt claim if focus exists, even when cursor unchanged
if pkt.focusTaskID != "" {
    if err := store.ClaimTaskTx(db, agentName, pkt.focusTaskID, 5); err != nil {
        // Handle contention...
    }
}
```

**Priority:** P2 — Lease expiration risk

---

### 6. Missing Error Context in Status Update (Severity: 14)

**File:** `internal/actions/task.go:166`

**Description:** `UpdateTaskStatusWithEventTx` failures return unwrapped with `uErr` (line 168). Caller can't distinguish status update failures from event insertion failures during CAS retries.

**Fix:**
```go
if uErr != nil {
    return fmt.Errorf("failed to update task status: %w", uErr)
}
```

**Priority:** P3 — Debugging clarity

---

### 7. Stale Brief Reconciliation (Severity: 12)

**File:** `internal/actions/resume.go:396`

**Description:** In `ResumeWithOptionsIdempotent`, brief computed outside transaction (line 376), then final state loaded inside (line 425). Creates temporary inconsistency where response focus != loaded focus. Reconciliation at lines 429-431 overwrites computed focus with loaded focus.

**Fix:** Documentation-only (already noted in lines 374-375 that brief is advisory).

**Priority:** P3 — Document as intentional design

---

### 8. Silent Lesson Loss in Retrospective (Severity: 12)

**File:** `internal/actions/session.go:189`

**Description:** When `MemoryUpsertIdempotent` fails, error is logged but execution continues (line 190). `LessonsCount` doesn't reflect failed upserts.

**Fix:** See Finding #2 (compound issue).

---

### 9. Hot Path String Allocation in Tool Failure Parsing (Severity: 12)

**File:** `internal/actions/session.go:97`

**Description:** `strings.Fields()` allocates new slice for every event in `extractRuleBasedLessons()`. With 200+ events, creates hundreds of allocations.

**Fix:**
```go
// Inline parse to avoid Field allocation
idx := strings.Index(e.Message, " ")
if idx > 0 {
    tool := e.Message[:idx]
    toolFailures[tool]++
}
```

**Priority:** P3 — Performance optimization

---

### 10. Idempotency Key Collision Risk (Severity: 11)

**File:** `internal/actions/memory.go:19`

**Description:** `MemorySet()` generates requestID with `time.Now().UnixNano()`. Rapid-fire calls within same nanosecond can collide. Same logical operation with different requestIDs creates duplicate records.

**Fix:**
```go
// Make requestID deterministic
func MemorySet(db *sql.DB, agentName, key, value, valueType, scope, scopeID string, expiresAt *time.Time) (int64, error) {
    requestID := fmt.Sprintf("mem_set_%s_%s_%s_%s", agentName, scope, scopeID, key)
    // ... rest of function
}
```

**Priority:** P2 — Idempotency guarantee

---

## Recommendations

1. **Immediate (P0):** Fix resume TOCTOU race (Finding #1) — move focus determination inside transaction
2. **High Priority (P1):** Batch lesson persistence (Finding #2), add ownership validation to CAS retry (Finding #3)
3. **Medium Priority (P2):** Fix idempotency key collision (Finding #10), ensure claim heartbeat refresh (Finding #5)
4. **Low Priority (P3):** Wrap errors with context (Finding #6), optimize string parsing (Finding #9), document brief staleness (Finding #7)

---

## Test Coverage Recommendations

```go
// Test TOCTOU race (Finding #1)
func TestResume_ClaimContentionClearsFocus(t *testing.T)

// Test N+1 query batching (Finding #2)
func TestPersistLessons_BatchesUpserts(t *testing.T)

// Test ownership re-validation (Finding #3)
func TestTaskSetStatus_DoesNotUnblockWhenNotOwned(t *testing.T)

// Test idempotency collision (Finding #10)
func TestMemorySet_DeterministicRequestID(t *testing.T)
```

---

## Metrics

- **Files analyzed:** 18
- **Lines of code:** ~735 (production) + ~980 (tests)
- **Scouts deployed:** 4 (flow, query, concurrency, performance)
- **Total findings:** 20 (pre-filter)
- **Critical/High:** 10 (post-filter)
- **Compound issues:** 2

---

## Related Files

- `internal/store/*.go` — persistence layer (mutations, transactions)
- `internal/models/*.go` — domain types
- `docs/contributor/idempotent-action-pattern.md` — pattern reference
