# Agent Install (vibe)

Use this when wiring an autonomous coding agent to `vibe` with minimal friction.

If you prefer one page to copy from, use sections in this order:

1. `Bootstrap`
2. `Core Loop`
3. `Safety Rules`

For assistant-agnostic lifecycle mapping, see `docs/integration-custom-assistant.md`.

## Operating Assumptions

- Success output is JSON on `stdout` in `{schema_version, success, data}` envelope.
- Failures are structured JSON logs on `stderr`.
- Mutating calls use `--request-id`.
- Agent identity is stable via `--agent` or `VIBE_AGENT`.

## Bootstrap

```bash
#!/usr/bin/env bash
set -euo pipefail

export VIBE_AGENT="${VIBE_AGENT:-worker-$(date +%s)}"
export VIBE_DB_PATH="${VIBE_DB_PATH:-$HOME/.config/vibe/vibe.db}"

req_id() { printf 'req_%s_%s\n' "$(date +%s)" "$RANDOM"; }

if ! command -v vibe >/dev/null 2>&1; then
  go install ./cmd/vibe
fi

vibe agent init --agent "$VIBE_AGENT" --request-id "$(req_id)" >/dev/null
```

## Learn Command Contracts Once

```bash
vibe schema > /tmp/vibe-schema.json
jq -r '.data.commands[] | .command' /tmp/vibe-schema.json
```

Use this to avoid invalid flags/status values.

## Core Loop

```bash
# resume -> act -> persist
RESUME_JSON="$(vibe resume --agent "$VIBE_AGENT" --request-id "$(req_id)")"
TASK_ID="$(echo "$RESUME_JSON" | jq -r '.data.focus_task_id // ""')"

if [ -n "$TASK_ID" ]; then
  vibe log --agent "$VIBE_AGENT" --request-id "$(req_id)" \
    --kind progress --task "$TASK_ID" --msg "working" >/dev/null
fi
```

### Alternative: Claim-Based Loop

Use `task claim` for server-side task selection (multi-agent queues):

```bash
# claim next eligible task -> work -> close
CLAIM="$(vibe task claim --agent "$VIBE_AGENT" --request-id "$(req_id)" --ttl-minutes 10)"
TASK_ID="$(echo "$CLAIM" | jq -r '.data.task.id // ""')"

if [ -n "$TASK_ID" ]; then
  # ... do work ...
  vibe task close --agent "$VIBE_AGENT" --request-id "$(req_id)" \
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

- Set project focus with `vibe agent focus --project <project_id> --request-id <id>`.
- When focus project is set, `resume` deltas are project-scoped.
- Task selection is strict to focused project.

## Event Compression Trigger

Use when old task history is too large for context.

```bash
vibe events summarize \
  --agent "$VIBE_AGENT" \
  --request-id "$(req_id)" \
  --from-id 1 \
  --to-id 50 \
  --task "$TASK_ID" \
  --summary "Condensed setup/debug history"
```

This archives that range and appends one `events_summary` event.

## OpenCode Bridge (Optional)

```bash
vibe hook install --opencode
```

Behavior:

- `session.created` -> project-scoped `vibe resume`
- `todo.updated` -> `todo_snapshot` event
- system prompt transform -> injected cached resume context

## Safety Rules For Agents

1. Parse JSON only, never human prose.
2. Retry writes with the same `--request-id`.
3. Treat `vibe resume` as truth, not local model memory.
