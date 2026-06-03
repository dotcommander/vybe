# Operator Guide

This guide covers running `vybe` autonomously. For integration contracts — machine I/O, retries, schema discovery — see `agent-contract.md`.

## Prerequisites

- `vybe` is installed and on `PATH`
- `jq` is installed for shell JSON parsing

## Operating rules (non-negotiable)

Your agent needs a stable identity. Pick a name and keep it across every call. Set it once via `VYBE_AGENT`, or set `default_agent` in `~/.config/vybe/config.yaml` (e.g. `default_agent: worker-001`). Resolution order: `--agent` flag → `VYBE_AGENT` env → `config.yaml: default_agent`. With either set, **omit `--agent` entirely** in single-agent setups — the examples below assume this.

**Omit `--request-id` by default.** A freshly-generated request-id is behaviorally identical to omitting it: vybe auto-generates one (`req_<nano>_<hex>`) and both give at-least-once semantics. Dedup only fires when the *same* `(agent, request-id, command)` tuple recurs — a timestamp+random id never recurs, so it never dedupes. Generating a fresh id per call is pure cargo-cult. Pass an explicit, **stable** `--request-id` *only* when you are deliberately retrying the exact same logical operation and want exactly-once dedup across those retries.

All output comes from `stdout` as a JSON envelope. `stderr` is diagnostics only — do not parse it.

Every loop starts with `resume`. If `focus_task_id` is empty, there's nothing to do — stop.

## The two-verb thesis

Every loop step reduces to two verbs:

1. `vybe resume` — get the focus task.
2. `vybe done <id>` (or `vybe block <id> --reason "..."`) — close it.

Everything else is optional sugar over the same idempotent actions:

- `vybe note <id> "msg"` — log a progress event.
- `vybe remember "key=value"` — store a memory.
- `vybe focus` — re-read the current focus task without advancing the cursor.

These thin verbs call the identical idempotent actions as their verbose forms (`task set-status`, `push`, `memory set`, `resume --peek`), so anything they do can also be done the long way — they just remove ceremony.

## Bootstrap

### First-time install

Run once. It creates the config dir, initializes the database, installs hooks, and sets `default_agent: claude` so you never need to pass `--agent` in single-agent setups.

```bash
vybe init
```

Output is structured JSON — check `status: "ok"` to confirm all steps succeeded. Subsequent runs are a no-op: already-applied steps report `"skipped"`.

To validate setup at any time (read-only, no side effects):

```bash
vybe doctor
```

`doctor` reports `healthy: true` when the binary is on PATH, hooks are installed, the database is reachable, and the config dir exists. If anything is wrong, it prints a repair hint.

### Loop bootstrap

Before the autonomous loop starts, confirm the DB is reachable and auto-create agent state. First call to `resume` creates the agent record if it doesn't exist.

```bash
#!/usr/bin/env bash
set -euo pipefail

export VYBE_AGENT="${VYBE_AGENT:-worker-001}"
export VYBE_DB_PATH="${VYBE_DB_PATH:-$HOME/.config/vybe/vybe.db}"

# Auto-creates agent state on first call
vybe resume >/dev/null
```

## Baseline loop

`resume` returns the brief packet. Extract `focus_task_id` — if it's set, claim the task, do the work, then mark it complete. The next `resume` call will advance to the next task automatically.

```bash
#!/usr/bin/env bash
set -euo pipefail

# Set VYBE_AGENT once (or config.yaml: default_agent) and omit --agent below.
RESUME_JSON="$(vybe resume)"
TASK_ID="$(echo "$RESUME_JSON" | jq -r '.data.focus_task_id // ""')"

if [ -n "$TASK_ID" ]; then
  vybe task begin --id "$TASK_ID" >/dev/null
  vybe note "$TASK_ID" "working" >/dev/null
  # Do work...
  vybe done "$TASK_ID" --note "completed: <summary>" >/dev/null
fi
```

## Project-aware loop

When your agent works inside a specific workspace, pass `--project-dir` to `resume` so vybe associates the session with the right project. Then extract the resolved project ID from a `--peek` call and use it when creating tasks — this scopes memory and filtering to that project.

```bash
WORKSPACE="$(pwd)"

vybe resume --project-dir "$WORKSPACE"
PROJECT_ID=$(vybe resume --peek | jq -r '.data.project.id // ""')

vybe task create --project-id "$PROJECT_ID" --title "Example" --desc "Scoped task"
```

## Day-2 recipes

### Create and start task

Create the task, capture the ID from the response, then immediately claim it. Two calls, not one — `begin` is the claim step that transitions status to `in_progress`.

```bash
TASK_ID=$(vybe task create --title "Process batch" --desc "Items 1-1000" \
  | jq -r '.data.task.id')

vybe task begin --id "$TASK_ID"
```

### Atomic progress + completion

For a simple close, `vybe done` sets status and logs an optional note in one atomic call:

```bash
vybe done "$TASK_ID" --note "Processed successfully"
```

When you need to combine event logging, memory writes, artifact linking, and status updates in a single atomic operation, reach for `push` — it either all lands or none of it does:

```bash
vybe push --json '{
  "task_id": "task_123",
  "event": {"kind": "progress", "message": "Processed successfully"},
  "memories": [{"key": "result", "value": "ok", "scope": "task", "scope_id": "task_123"}],
  "task_status": {"status": "completed", "summary": "Done"}
}'
```

### Task memory checkpoint

Write progress into task-scoped memory so a crash mid-task doesn't lose position. On restart, read the checkpoint and resume from where you stopped.

```bash
vybe remember "checkpoint=6000" --scope task --scope-id "$TASK_ID"

vybe memory get --key checkpoint --scope task --scope-id "$TASK_ID" | jq -r '.data.value'
```

For `--scope task` (and `--scope project`), `--scope-id` can be omitted when the agent has a focus task (or project) set via `vybe task begin` — vybe infers the scope-id from the agent's focus. Pass `--scope-id` explicitly only for `--scope agent`, or for task/project when no focus is set.

### Pin durable strategy

Use `--kind=directive` and `--pin` for behavioral rules that must survive decay and never drop out of the resume brief. Directives render first in the brief under `=== Directives ===` as bare values, before any facts.

```bash
# Write a directive and pin it
vybe remember "always_run_tests=Run go test ./... before reporting any task complete" \
  --scope global --kind directive --pin

# Unpin later if the directive no longer applies
vybe memory pin --key always_run_tests --scope global --unpin
```

A subsequent `memory set` for the same key WITHOUT `--pin` will not clear the pin — only `memory pin --unpin` can.

### Read events and artifacts

```bash
vybe events --task-id "$TASK_ID" --limit 100
vybe artifacts --task-id "$TASK_ID" --limit 100
```

### Install/uninstall hooks

```bash
vybe hook install
vybe hook uninstall

vybe hook install --opencode
vybe hook uninstall --opencode
```

Inspect the hook manifest (event names, matchers, timeouts, command paths):

```bash
vybe hook export
```

The manifest is stored at `~/.config/vybe/hooks.json` and is editable. Re-run `vybe hook install` after editing to apply changes.

### Discover current command surface

```bash
# JSON command index
vybe

# JSON schemas + mutation hints
vybe schema
```

## Verification

Run after setup or upgrades:

```bash
vybe doctor          # checks binary on PATH, hooks installed, DB reachable, config dir present
vybe status --check  # low-level DB connectivity probe
vybe resume
vybe schema
```

Pass condition: `doctor` returns `healthy: true`; `status --check` JSON output contains `"query_ok": true`; `resume` returns a packet. Both `doctor` and `status --check` always exit 0 — health is determined from the JSON payload, not the exit code.

## Related docs

- `agent-contract.md` for integration contracts and retry behavior
- `decisions.md` for command-surface guardrails
