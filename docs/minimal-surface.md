# Minimal Surface

Purpose: keep vybe focused on one job only: durable continuity for autonomous agents.

Main question for every command and flag:

> Does this directly help an agent resume work, mutate durable state idempotently, or read continuity context?

If not, it is optional and a pruning candidate.

## Keep vs optional matrix

### Keep (core continuity)

| Surface | Why it is core |
| --- | --- |
| `resume` (`--peek`, non-`--peek`, `--focus`, `--project-dir`) | Entry point for deterministic focus + brief packet + cursor semantics |
| `task create|begin|complete|get|list` | Minimal queue lifecycle and task state reads |
| `task add-dep` | Needed for dependency-aware focus selection in real workflows |
| `push` | Atomic write path for event + memory + artifacts + task status |
| `events list` | Readable append-only history for recovery and debugging |
| `memory set|get|list` | Durable scoped knowledge across sessions |
| `artifacts list` | Reattach file outputs to task context after crashes/restarts |
| `status --check` | Fast machine health gate before loop work |
| `schema commands` | Machine-discoverable contract for weak-model callers |
| `upgrade` | Required bootstrap/migration path for durable state |

### Keep but secondary (operational hygiene)

| Surface | Why it stays for now |
| --- | --- |
| `memory delete|gc` | Prevent stale/expired memory from degrading continuity quality |
| `task set-status` | Raw state correction and loop/circuit-breaker interoperability |

### Optional (integration and convenience)

| Surface | Why optional |
| --- | --- |
| `hook *` | Integration adapters; useful, but not required for continuity primitives |
| `loop` | Driver/orchestration convenience over core state primitives |
| `task set-priority` | Useful queue shaping, but not required for basic continuity |

## Pruning checklist

Use this for any command/flag removal decision.

1. **Continuity test**: if removed, can agent still answer "what were we doing?" via `resume` + reads?
2. **Idempotency test**: all remaining writes still accept/replay `--request-id` safely.
3. **Replaceability test**: feature can be recreated with existing core commands in 1-2 calls.
4. **Schema test**: `vybe schema commands` remains stable and additive for callers.
5. **Verification gate**:
   - `go test ./...`
   - `go build ./...`
   - `go run ./cmd/demo -fast`
6. **Docs gate**: update `agent-contract.md`, `operator-guide.md`, and demo artifacts in the same change.

## Minimal acceptance criteria (release gate)

Ship a change only if all remain true:

- New session resumes deterministically with correct focus task.
- Replay of same request ID does not duplicate side effects.
- Task lifecycle works end-to-end (`create → begin → complete`).
- Cross-session memory and artifact reads still work.
- Machine caller can discover valid flags via `schema commands`.

## Current recommendation

Short term: keep the core and secondary surfaces above stable; avoid adding new command families.

If you need to reduce further, prune in this order:

1. selected `hook` handlers not used by your active integrations
