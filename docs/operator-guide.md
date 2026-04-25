# Operator Guide

This guide covers running `vybe` in autonomous loops. For integration contracts — machine I/O, retries, schema discovery — see `agent-contract.md`.

## Prerequisites

- `vybe` is installed and on `PATH`
- `jq` is installed for shell JSON parsing

## Operating rules (non-negotiable)

Your agent needs a stable identity. Pick a name, set it in `VYBE_AGENT`, and keep it across every call.

Every mutation — `push`, `resume` (non-`--peek`), `task *`, `memory set|delete|gc` — requires a `--request-id`. Without it, retries create duplicate state. Generate a fresh ID per call.

All output comes from `stdout` as a JSON envelope. `stderr` is diagnostics only — do not parse it.

Every loop starts with `resume`. If `focus_task_id` is empty, there's nothing to do — stop.

## Bootstrap

Run this once before your loop starts. It initializes agent state and confirms the DB is reachable. First call to `resume` auto-creates the agent record if it doesn't exist.

```bash
#!/usr/bin/env bash
set -euo pipefail

export VYBE_AGENT="${VYBE_AGENT:-worker-001}"
export VYBE_DB_PATH="${VYBE_DB_PATH:-$HOME/.config/vybe/vybe.db}"

req_id() { printf 'req_%s_%s\n' "$(date +%s)" "$RANDOM"; }

# Auto-creates agent state on first call
vybe resume --agent "$VYBE_AGENT" --request-id "$(req_id)" >/dev/null
```

## Baseline loop

`resume` returns the brief packet. Extract `focus_task_id` — if it's set, claim the task, do the work, then mark it complete. The next `resume` call will advance to the next task automatically.

```bash
#!/usr/bin/env bash
set -euo pipefail

req_id() { printf 'req_%s_%s\n' "$(date +%s)" "$RANDOM"; }

RESUME_JSON="$(vybe resume --agent "$VYBE_AGENT" --request-id "$(req_id)")"
TASK_ID="$(echo "$RESUME_JSON" | jq -r '.data.focus_task_id // ""')"

if [ -n "$TASK_ID" ]; then
  vybe task begin --agent "$VYBE_AGENT" --request-id "$(req_id)" --id "$TASK_ID" >/dev/null

  vybe push --agent "$VYBE_AGENT" --request-id "$(req_id)" --json \
    "{\"task_id\":\"$TASK_ID\",\"event\":{\"kind\":\"progress\",\"message\":\"working\"}}" >/dev/null

  # Do work...

  vybe task set-status --agent "$VYBE_AGENT" --request-id "$(req_id)" \
    --id "$TASK_ID" --status completed >/dev/null
fi
```

## Project-aware loop

When your agent works inside a specific workspace, pass `--project-dir` to `resume` so vybe associates the session with the right project. Then extract the resolved project ID from a `--peek` call and use it when creating tasks — this scopes memory and filtering to that project.

```bash
WORKSPACE="$(pwd)"

vybe resume --agent "$VYBE_AGENT" --request-id "$(req_id)" --project-dir "$WORKSPACE"
PROJECT_ID=$(vybe resume --agent "$VYBE_AGENT" --peek | jq -r '.data.project.id // ""')

vybe task create --agent "$VYBE_AGENT" --request-id "$(req_id)" \
  --project-id "$PROJECT_ID" --title "Example" --desc "Scoped task"
```

## Driver loop

`vybe loop` is the built-in autonomous driver. It runs the resume → claim → work → next-task cycle for you by spawning an external command (your assistant CLI) once per task, feeding it the resume brief, and classifying the outcome. It is a one-shot batch runner — not a daemon, not polling — so it exits cleanly when the queue drains, when the circuit breaker trips, or when `--max-tasks` is reached.

```bash
vybe loop --agent "$VYBE_AGENT" \
  --command "claude --dangerously-skip-permissions" \
  --project-dir "$(pwd)" \
  --max-tasks 10 \
  --max-fails 3 \
  --task-timeout 10m \
  --cooldown 5s
```

The spawned command receives `-p @<tempfile>` (the brief prompt — file form avoids the 256KB CLI argument limit) and `--project <dir>`. The wrapper injects autonomous-rules guidance into the prompt so the spawned agent knows there is no human to ask and must emit DONE or STUCK before exiting.

Outcome classification per task:

| Result | Trigger | Effect on circuit breaker |
| --- | --- | --- |
| `completed` | Spawned command exit 0 AND task status = completed | Resets fail counter |
| `blocked` | Task left in `pending`/`in_progress` after agent exits, or task already `blocked` | Increments fails |
| `timeout` | Spawned command exceeded `--task-timeout` | Increments fails |
| `failed` | Non-zero exit not attributable to timeout | Increments fails |

Safety rails:

- `--max-tasks N` — stop after N completions (default 10)
- `--max-fails N` — circuit breaker stops the loop after N CONSECUTIVE failures (default 3)
- `--task-timeout DUR` — kill the spawned command after this duration (default 10m; SIGTERM, then SIGKILL after 2s grace)
- `--cooldown DUR` — wait between tasks; signal-aware, so SIGINT/SIGTERM during cooldown exits cleanly (default 5s)
- `--dry-run` — print what would run without spawning anything
- `--spawn-disable-hooks` — for Claude command, injects `--settings '{"hooks":{}}'` and sets `VYBE_DISABLE_EXTERNAL_LLM=1` so the spawned agent does not recursively trigger vybe hooks

Loop output (stdout JSON envelope):

```json
{
  "completed": 7,
  "failed": 1,
  "total": 8,
  "duration_sec": 412.3,
  "results": [
    {"task_id": "task_...", "task_title": "...", "status": "completed", "duration": "1m23s"}
  ]
}
```

Optional `--post-hook "<cmd>"` runs after the loop exits and receives the results JSON on stdin (30s timeout, non-fatal if the hook errors). Use it for notifications, summaries, or chaining into another tool.

## Day-2 recipes

### Create and start task

Create the task, capture the ID from the response, then immediately claim it. Two calls, not one — `begin` is the claim step that transitions status to `in_progress`.

```bash
TASK_ID=$(vybe task create --agent "$VYBE_AGENT" --request-id "task_create_1" \
  --title "Process batch" --desc "Items 1-1000" | jq -r '.data.task.id')

vybe task begin --agent "$VYBE_AGENT" --request-id "task_begin_1" --id "$TASK_ID"
```

### Atomic progress + completion

`push` combines event logging, memory writes, artifact linking, and status updates into one atomic call. Use it at the end of a task instead of issuing four separate commands — it either all lands or none of it does.

```bash
vybe push --agent "$VYBE_AGENT" --request-id "close_1" --json '{
  "task_id": "task_123",
  "event": {"kind": "progress", "message": "Processed successfully"},
  "task_status": {"status": "completed", "summary": "Done"}
}'
```

### Task memory checkpoint

Write progress into task-scoped memory so a crash mid-task doesn't lose position. On restart, read the checkpoint and resume from where you stopped.

```bash
vybe memory set --agent "$VYBE_AGENT" --request-id "mem_set_1" \
  --key checkpoint --value "6000" --type number --scope task --scope-id "$TASK_ID"

vybe memory get --key checkpoint --scope task --scope-id "$TASK_ID" | jq -r '.data.value'
```

### Pin durable strategy

Use `--kind=directive` and `--pin` for behavioral rules that must survive decay and never drop out of the resume brief. Directives render first in the brief under `=== Directives ===` as bare values, before any facts.

```bash
# Write a directive and pin it
vybe memory set --agent "$VYBE_AGENT" --request-id "mem_dir_1" \
  --key always_run_tests \
  --value "Run go test ./... before reporting any task complete" \
  --scope global --kind directive --pin

# Unpin later if the directive no longer applies
vybe memory pin --agent "$VYBE_AGENT" --request-id "mem_unpin_1" \
  --key always_run_tests --scope global --unpin
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
vybe status --check
vybe resume --agent "$VYBE_AGENT" --request-id "verify_resume_1"
vybe schema
```

Pass condition: `status --check` JSON output contains `"query_ok": true`, and `resume` returns a packet. Note: `status --check` always exits 0; health is determined from the JSON payload, not the exit code.

## Related docs

- `agent-contract.md` for integration contracts and retry behavior
- `decisions.md` for command-surface guardrails
