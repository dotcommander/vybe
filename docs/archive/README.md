# Archived Documentation

This directory contains historical documentation that is no longer actively maintained but preserved for reference.

## Contents

### `task-lease-heartbeat-implementation-notes.md`

**Status**: Fully implemented as of migration `00013_task_lease_heartbeat.sql`
**Original**: Phase E1 spec for task lease and heartbeat feature

Implementation details:
- Schema: `last_heartbeat_at`, `attempt` columns added to `tasks` table
- Command: `vibe task heartbeat` implemented in `internal/commands/task.go`
- Store primitives: `internal/store/task_heartbeat.go`
- Tests: `internal/store/task_heartbeat_test.go`

This spec served as the blueprint for Phase E1 (lease heartbeat semantics + execution telemetry). The implementation matches the spec's design decisions.

### `audit-unknowns-2026-02-13.md`

**Date**: 2026-02-13
**Type**: Point-in-time technical debt audit

Comprehensive audit identifying hidden dependencies, concurrency bugs, N+1 queries, and undocumented contracts. Contains 26 findings (8 high, 13 medium, 5 low severity).

**Note**: This is a snapshot. For current technical debt status, check:
- Open vibe tasks tagged with severity levels
- Recent git commit messages addressing audit findings
- Current test coverage in `docs/testing/important-features-matrix.md`

Key findings included:
- Memory canonical deduplication race condition
- N+1 query patterns in task dependencies
- Event kind taxonomy scattered across codebase
- Missing indexes for event filtering

Many of these findings have been or are being addressed in subsequent development.
