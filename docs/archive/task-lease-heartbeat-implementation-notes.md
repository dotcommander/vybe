# Spec Plan: Task Lease and Heartbeat

Status: draft
Owner: core CLI
Scope: generic primitives only (no domain-specific logic)

## Goal

Strengthen autonomous worker coordination by making task leases explicit and
observable while preserving Vibe's current guarantees:

- idempotent writes
- append-only event truth
- deterministic resume behavior
- additive JSON/schema evolution

## Current Baseline (already implemented)

- Tasks already support lease fields: `claimed_by`, `claimed_at`,
  `claim_expires_at` (`internal/store/migrations/00004_task_claiming.sql`).
- Claim CAS logic already exists (`internal/store/task_claim.go`):
  claim succeeds only when unclaimed, self-claimed, or expired.
- `task start` and `resume` already claim tasks with a short TTL through
  `ClaimTaskTx` (`internal/store/task_start.go`, `internal/actions/resume.go`).
- Expired claims can be cleaned via `task gc` (`ReleaseExpiredClaims`).

This spec extends those primitives instead of replacing them.

## Decisions: Now vs Later vs Skip

## Needed Now (Phase E1)

1. First-class lease heartbeat semantics
   - Add `last_heartbeat_at` to tasks.
   - Define heartbeat as lease refresh by current claim owner.
   - Heartbeat must be idempotent + evented (same core mutation pattern).

2. Minimal execution telemetry needed for robust coordination
   - Add `attempt` to tasks (default 0).
   - Increment `attempt` when a new owner acquires a lease after release/expiry.
   - Reuse existing `claimed_at` and `claim_expires_at`.

Rationale: these are directly tied to crash recovery, contention behavior,
and autonomous retries in existing `resume`/`run` loops.

## Add Later (Phase E2)

1. Richer queue lifecycle metadata
   - Optional `queued_at` (if separate from `created_at` is needed).
   - Additional derived timestamps only when a concrete operator workflow
     requires them.

2. Policy extension points (framework hooks)
   - Pre-complete validator callback.
   - Conflict detector callback.
   - Default behavior remains no-op and core deterministic.

Rationale: useful for extensibility, but not required to stabilize lease
coordination immediately.

## Skip for Now (explicitly out of scope)

1. Generic cache registry primitives
   - Deferred until a concrete continuity/runtime use case appears.
   - Keep core focused on tasks/events/memory/artifacts/resume semantics.

Rationale: increases surface area without solving the immediate coordination
gap.

## Proposed Data Model (E1)

Add columns to `tasks`:

- `last_heartbeat_at TIMESTAMP NULL`
- `attempt INTEGER NOT NULL DEFAULT 0`

Notes:

- Additive migration only; no breaking field renames.
- Existing rows backfill safely with defaults.
- Keep lease truth in `tasks` (single source for claim lifecycle).

## Command + Behavior Plan (E1)

1. `vibe task start`
   - Continue current behavior (set `in_progress`, set focus, claim task).
   - On successful new lease acquisition, update:
     - `claimed_at`
     - `claim_expires_at`
     - `last_heartbeat_at = CURRENT_TIMESTAMP`
     - `attempt` according to claim transition rules.

2. `vibe resume`
   - When focus task is retained and claim owner matches current agent,
     refresh lease + heartbeat as part of the same idempotent transaction.
   - On contention, preserve existing behavior (clear focus/retry path).

3. New command: `vibe task heartbeat --id <task>`
   - Mutating command, requires `--agent` + `--request-id`.
   - Succeeds only for active claim owner.
   - Refreshes `claim_expires_at` and `last_heartbeat_at`.
   - Appends event (e.g., `task_heartbeat`).

4. `vibe task gc`
   - Keep current expiry release behavior.
   - Optionally include released task IDs in event metadata in a later increment
     (not required for E1).

## Attempt Counter Rules (E1)

- `attempt` increments when a lease is newly acquired by an agent and previous
  active owner was different or lease was expired/unclaimed.
- Heartbeats by the current owner do not increment `attempt`.
- Idempotent replay with same request id must not double-increment.

## Event + JSON Contract Plan (E1)

Events (additive):

- `task_claimed` (when a claim is established by an action path that does not
  already encode claim transition clearly)
- `task_heartbeat` (explicit heartbeat command)

JSON payloads (additive):

- Include `last_heartbeat_at` and `attempt` in task outputs
  (`task get`, `task list`, `resume` brief task object).

Compatibility:

- Keep envelope shape and `schema_version: v1`.
- Add fields only; do not rename/remove existing fields.

## Implementation Boundaries

- `internal/store/`: SQL primitives + tx variants (`*Tx`), no command text.
- `internal/actions/`: orchestration via `RunIdempotent*` + event append.
- `internal/commands/`: flag parsing and output shaping only.

## Verification Plan

Minimum test additions for E1:

1. Store tests
   - Claim + heartbeat refresh updates both expiry and heartbeat time.
   - Attempt increment rules across contention/expiry transitions.
   - Idempotent replay safety for claim/heartbeat mutation paths.

2. Action tests
   - `TaskStartIdempotent` and `ResumeWithOptionsIdempotent` preserve lease
     invariants under retry.

3. Command tests
   - `task heartbeat` requires agent/request-id and returns task state.
   - Task JSON includes `attempt` and `last_heartbeat_at` when set.

4. Regression gates
   - `go test ./...`
   - `go vet ./...`
   - `go build ./...`

## Done Criteria

E1 is done when:

- Lease heartbeat exists as first-class behavior.
- Attempt telemetry is available and correct under retries/contention.
- No breaking command contract changes.
- Existing resume/run behavior remains deterministic under concurrent agents.
