# Refactoring: internal/models

**Date:** 2026-02-13
**Status:** Phase 1 Complete, Phase 2 Planned

---

## Executive Summary

Completed non-breaking type safety and behavior improvements to `internal/models`. Breaking changes (migrating fields to typed enums) documented but deferred pending explicit approval.

**Phase 1 Changes (✅ Complete):**
- Added typed aliases: `TaskStatus`, `MemoryScope`, `BlockedReason`
- Added constants for valid values
- Added 15 behavior methods to Task and Memory
- Added documentation for ID strategy and Event.Kind
- All changes backward-compatible (no breaking changes)

**Phase 2 Changes (📋 Planned):**
- Migrate struct fields to use typed enums
- Add store-layer validation
- Update 46 files referencing models
- Estimated 7-10 hours, medium risk

---

## Phase 1 Deliverables (Complete)

### 1. Type Aliases and Constants

**Added types:**
```go
type TaskStatus string       // "pending", "in_progress", "completed", "blocked"
type MemoryScope string      // "global", "project", "task", "agent"
type BlockedReason string    // "", "dependency", "failure:*"
```

**Why:** Provides type-safe constants for use throughout codebase without breaking existing `string` fields.

**Usage:**
```go
// Old (magic strings, error-prone)
if task.Status == "in_progress" { ... }

// New (type-safe, refactor-friendly)
if task.Status == string(TaskStatusInProgress) { ... }
```

### 2. Behavior Methods

**Task methods:**
- `IsClaimed()` - Check if task has been claimed
- `IsBlocked()` - Check if status is blocked
- `IsBlockedByDependency()` - Check if blocked by unresolved dependency
- `IsBlockedByFailure()` - Check if blocked by execution failure
- `HasClaimedAt()` - Check if claim timestamp is set

**Memory methods:**
- `IsExpired(now time.Time)` - Check if memory has expired
- `IsSuperseded()` - Check if superseded by another entry
- `IsGlobalScope()` - Check if global visibility
- `IsProjectScope()` - Check if project-scoped
- `IsTaskScope()` - Check if task-scoped
- `IsAgentScope()` - Check if agent-scoped

**BlockedReason methods:**
- `IsFailure()` - Check if failure-type reason
- `GetFailureReason()` - Extract failure message
- `NewBlockedReasonFailure(reason)` - Constructor for failure reasons

**TaskStatus methods:**
- `IsTerminal()` - Check if task is completed
- `IsPending()` - Check if task is pending

**Why:** Encapsulates business logic, improves readability, easier to refactor.

**Usage:**
```go
// Old (scattered string checks)
if task.Status == "blocked" && strings.HasPrefix(task.BlockedReason, "failure:") {
    reason := strings.TrimPrefix(task.BlockedReason, "failure:")
    // ...
}

// New (encapsulated logic)
if task.IsBlocked() && task.IsBlockedByFailure() {
    reason := BlockedReason(task.BlockedReason).GetFailureReason()
    // ...
}
```

### 3. Documentation

**Added:**
- Package-level comment explaining ID strategy (int64 vs string)
- Event.Kind godoc linking to event_kinds.go constants
- Comments for all new types and methods

**Why:** Helps developers understand design decisions and discover available constants.

---

## Phase 2 Plan (Deferred)

### Breaking Changes Required

**2.1 Update struct field types:**
```go
type Task struct {
    Status        TaskStatus    `json:"status"`        // was: string
    BlockedReason BlockedReason `json:"blocked_reason,omitempty"` // was: string
    // ...
}

type Memory struct {
    Scope MemoryScope `json:"scope"` // was: string
    // ...
}

type Project struct {
    Metadata json.RawMessage `json:"metadata"` // was: string
    // ...
}
```

**Impact:**
- JSON wire format unchanged (serializes to/from string)
- All assignments must use typed constants
- Invalid values rejected at type level
- 46 files need updates (store, actions, commands, tests)

**2.2 Add store-layer validation:**
```go
func (s *Store) CreateTaskTx(tx *sql.Tx, task *Task) error {
    // Validate TaskStatus
    switch TaskStatus(task.Status) {
    case TaskStatusPending, TaskStatusInProgress, TaskStatusCompleted, TaskStatusBlocked:
        // valid
    default:
        return fmt.Errorf("invalid task status: %q", task.Status)
    }
    // ... existing logic
}
```

**2.3 Update callers:**
- Store layer: Use typed constants in all SQL queries
- Action layer: Use new behavior methods
- Command layer: Convert user input to typed values
- Tests: Replace magic strings with constants

**2.4 Migration strategy:**
1. Keep JSON tags as `string` (backward compatible wire format)
2. Add custom JSON marshalers if validation at unmarshal time is needed
3. Deploy as minor version bump (additive, not breaking for clients)
4. Provide migration guide showing old → new patterns

---

## Risk Assessment

### Phase 1 (Complete) - ✅ Low Risk
- All changes additive (no breaking changes)
- Existing code continues to work unchanged
- New code can gradually adopt typed constants and methods
- Zero deployment risk

### Phase 2 (Planned) - ⚠️ Medium Risk

**High-risk areas:**
- Resume logic (store/resume.go) depends heavily on Status and BlockedReason matching
- Memory scope filtering in brief generation
- 46 files reference models package (widespread impact)

**Mitigation strategies:**
1. **Comprehensive test coverage** - Add tests for:
   - Status validation rejects invalid values
   - BlockedReason validation rejects malformed failures
   - MemoryScope validation rejects typos
   - Resume logic unchanged with typed values
   - Brief generation unchanged with typed scopes

2. **Incremental rollout:**
   - Step 1: Update struct definitions + add validation
   - Step 2: Update store layer (database boundary)
   - Step 3: Update action layer
   - Step 4: Update command layer + tests
   - Step 5: Add LSP linter rules to prevent string literals

3. **Backward compatibility:**
   - Keep JSON serialization as `string`
   - Add custom UnmarshalJSON if needed
   - Old database records work without migration

4. **Verification gates:**
   - `go build ./...` - Must compile
   - `go test ./...` - All tests must pass
   - `go vet ./...` - No warnings
   - Manual smoke test: create task, claim task, resume, brief

---

## Effort Estimation

| Activity | Time | Complexity |
|----------|------|------------|
| **Phase 1 (Complete)** | 2h | Low |
| - Add type aliases + constants | 30m | Low |
| - Add behavior methods | 1h | Low |
| - Add documentation | 30m | Low |
| **Phase 2 (Planned)** | 7-10h | Medium |
| - Update struct fields | 1h | Low |
| - Add store validation | 2h | Medium |
| - Update store layer (queries) | 2h | Medium |
| - Update action layer | 1-2h | Low |
| - Update command layer | 30m | Low |
| - Update tests | 1-2h | Medium |
| - Verification + fixes | 1-2h | Medium |

---

## Approval Required for Phase 2

**Phase 2 requires explicit approval due to:**
1. Breaking changes to struct fields (46 files affected)
2. 7-10 hour implementation effort
3. Medium risk (resume logic, widespread usage)
4. Requires comprehensive testing before deployment

**Next steps:**
1. Review Phase 1 changes (non-breaking, already complete)
2. Decide: proceed with Phase 2 or keep Phase 1 only?
3. If proceeding: allocate 7-10 hours, plan rollout strategy
4. If deferring: Phase 1 provides immediate value, Phase 2 can wait

---

## Usage Examples

### Using New Type-Safe Constants

```go
// Creating a task with typed status
task := &models.Task{
    ID:     "task_123",
    Title:  "Implement feature X",
    Status: string(models.TaskStatusPending), // type-safe
}

// Checking task state
if task.IsBlocked() && task.IsBlockedByDependency() {
    // Task waiting on dependency
}

// Setting blocked reason
task.BlockedReason = string(models.NewBlockedReasonFailure("build failed"))
if task.IsBlockedByFailure() {
    reason := models.BlockedReason(task.BlockedReason).GetFailureReason()
    fmt.Printf("Failed: %s\n", reason) // "build failed"
}
```

### Using Memory Scope Helpers

```go
// Filtering memories by scope
var globalMemories []models.Memory
for _, m := range allMemories {
    if m.IsGlobalScope() {
        globalMemories = append(globalMemories, m)
    }
}

// Checking expiration
now := time.Now()
if memory.IsExpired(now) {
    // Memory has expired
}
```

---

## Success Criteria

**Phase 1 (✅ Achieved):**
- [x] Type aliases and constants defined
- [x] Behavior methods added and documented
- [x] All changes backward-compatible
- [x] Package builds without errors
- [x] Existing tests pass unchanged
- [x] Documentation updated

**Phase 2 (Pending Approval):**
- [ ] Struct fields use typed enums
- [ ] Store layer validates inputs
- [ ] All 46 files updated
- [ ] Comprehensive test coverage
- [ ] Zero regressions in existing behavior
- [ ] Migration guide published
