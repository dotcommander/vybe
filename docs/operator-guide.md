# Operator Guide

Purpose: run `vybe` in production-like autonomous loops with minimal ceremony.

For integration contracts (machine I/O, retries, schema discovery), see `agent-contract.md`.

## Prerequisites

- `vybe` is installed and on `PATH`
- `jq` is installed for shell JSON parsing

## Operating rules (non-negotiable)

- Use stable identity via `--agent` or `VYBE_AGENT`
- Include `--request-id` on continuity mutations (`push`, `resume` non-`--peek`, `task *`, `memory set|delete|gc`)
- Parse JSON envelope from `stdout`; treat `stderr` as diagnostics only
- Call `resume` before work; check `focus_task_id` for empty

## Bootstrap

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

  vybe task complete --agent "$VYBE_AGENT" --request-id "$(req_id)" \
    --id "$TASK_ID" --outcome done --summary "Completed" >/dev/null
fi
```

## Project-aware loop (recommended)

Use `--project-dir` for workspace scope and `--project-id` for task association/filtering.

```bash
WORKSPACE="$(pwd)"

vybe resume --agent "$VYBE_AGENT" --request-id "$(req_id)" --project-dir "$WORKSPACE"
vybe task create --agent "$VYBE_AGENT" --request-id "$(req_id)" \
  --project-id "$WORKSPACE" --title "Example" --desc "Scoped task"
```

## Day-2 recipes

### Create and start task

```bash
TASK_ID=$(vybe task create --agent "$VYBE_AGENT" --request-id "task_create_1" \
  --title "Process batch" --desc "Items 1-1000" | jq -r '.data.task.id')

vybe task begin --agent "$VYBE_AGENT" --request-id "task_begin_1" --id "$TASK_ID"
```

### Atomic progress + completion

```bash
vybe push --agent "$VYBE_AGENT" --request-id "close_1" --json '{
  "task_id": "task_123",
  "event": {"kind": "progress", "message": "Processed successfully"},
  "task_status": {"status": "completed", "summary": "Done"}
}'
```

### Task memory checkpoint

```bash
vybe memory set --agent "$VYBE_AGENT" --request-id "mem_set_1" \
  --key checkpoint --value "6000" --type number --scope task --scope-id "$TASK_ID"

vybe memory get --key checkpoint --scope task --scope-id "$TASK_ID" | jq -r '.data.value'
```

### Read events and artifacts

```bash
vybe events list --task-id "$TASK_ID" --limit 100
vybe artifacts list --task-id "$TASK_ID" --limit 100
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
vybe schema commands
```

## Verification

Run after setup or upgrades:

```bash
vybe status
vybe resume --agent "$VYBE_AGENT" --request-id "verify_resume_1"
vybe schema commands
```

Pass condition: all commands return success JSON and `resume` returns a packet.

## Related docs

- `agent-contract.md` for integration contracts and retry behavior
- `contributor-guide.md` for safe code changes
- `DECISIONS.md` for command-surface guardrails
