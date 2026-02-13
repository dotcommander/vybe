# Agent Install (vybe)

Use this when wiring an autonomous coding agent to `vybe` with minimal friction.

If you prefer one page to copy from, use sections in this order:

1. `Bootstrap`
2. `Core Loop`
3. `Safety Rules`

For assistant-agnostic lifecycle mapping, see `docs/integration-custom-assistant.md`.

## Operating Assumptions

- Success output is JSON on `stdout` in `{schema_version, success, data}` envelope.
- Failures are structured JSON logs on `stderr`.
- Mutating calls use `--request-id`.
- Agent identity is stable via `--agent` or `VYBE_AGENT`.

## Bootstrap

```bash
#!/usr/bin/env bash
set -euo pipefail

export VYBE_AGENT="${VYBE_AGENT:-worker-$(date +%s)}"
export VYBE_DB_PATH="${VYBE_DB_PATH:-$HOME/.config/vybe/vybe.db}"

req_id() { printf 'req_%s_%s\n' "$(date +%s)" "$RANDOM"; }

if ! command -v vybe >/dev/null 2>&1; then
  go install ./cmd/vybe
fi

vybe agent init --agent "$VYBE_AGENT" --request-id "$(req_id)" >/dev/null
```

## Learn Command Contracts Once

```bash
vybe schema > /tmp/vybe-schema.json
jq -r '.data.commands[] | .command' /tmp/vybe-schema.json
```

Use this to avoid invalid flags/status values.

## Core Loop

```bash
# resume -> act -> persist
RESUME_JSON="$(vybe resume --agent "$VYBE_AGENT" --request-id "$(req_id)")"
TASK_ID="$(echo "$RESUME_JSON" | jq -r '.data.focus_task_id // ""')"

if [ -n "$TASK_ID" ]; then
  vybe log --agent "$VYBE_AGENT" --request-id "$(req_id)" \
    --kind progress --task "$TASK_ID" --msg "working" >/dev/null
fi
```

### Alternative: Claim-Based Loop

Use `task claim` for server-side task selection (multi-agent queues):

```bash
# claim next eligible task -> work -> close
CLAIM="$(vybe task claim --agent "$VYBE_AGENT" --request-id "$(req_id)" --ttl-minutes 10)"
TASK_ID="$(echo "$CLAIM" | jq -r '.data.task.id // ""')"

if [ -n "$TASK_ID" ]; then
  # ... do work ...
  vybe task close --agent "$VYBE_AGENT" --request-id "$(req_id)" \
    --id "$TASK_ID" --outcome done --summary "Completed" >/dev/null
fi
```

## Task Status Values (Strict)

- `pending`
- `in_progress`
- `completed`
- `blocked`

Invalid status errors include machine-parseable `valid_options` hints
in `stderr` logs.

## Project Isolation

- Set project focus with `vybe agent focus --project <project_id> --request-id <id>`.
- When focus project is set, `resume` deltas are project-scoped.
- Task selection is strict to focused project.

## Event Compression Trigger

Use when old task history is too large for context.

```bash
vybe events summarize \
  --agent "$VYBE_AGENT" \
  --request-id "$(req_id)" \
  --from-id 1 \
  --to-id 50 \
  --task "$TASK_ID" \
  --summary "Condensed setup/debug history"
```

This archives that range and appends one `events_summary` event.

## OpenCode Bridge (Optional)

```bash
vybe hook install --opencode
```

Behavior:

- `session.created` -> project-scoped `vybe resume`
- `todo.updated` -> `todo_snapshot` event
- system prompt transform -> injected cached resume context

## Safety Rules For Agents

1. Parse JSON only, never human prose.
2. Retry writes with the same `--request-id`.
3. Treat `vybe resume` as truth, not local model memory.
