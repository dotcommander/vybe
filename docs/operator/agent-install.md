# Agent Install

Use this guide to connect an autonomous agent to `vybe` with minimal setup.

For custom plugin/assistant lifecycle mapping, see `../integrator/custom-assistant.md`.

## Prerequisites

- `vybe` is installed and available in `PATH`
- `jq` is installed for JSON parsing in shell scripts

## Bootstrap

```bash
#!/usr/bin/env bash
set -euo pipefail

export VYBE_AGENT="${VYBE_AGENT:-worker-001}"
export VYBE_DB_PATH="${VYBE_DB_PATH:-$HOME/.config/vybe/vybe.db}"

req_id() { printf 'req_%s_%s\n' "$(date +%s)" "$RANDOM"; }

vybe agent init --agent "$VYBE_AGENT" --request-id "$(req_id)" >/dev/null
```

## Core loop

```bash
#!/usr/bin/env bash
set -euo pipefail

req_id() { printf 'req_%s_%s\n' "$(date +%s)" "$RANDOM"; }

RESUME_JSON="$(vybe resume --agent "$VYBE_AGENT" --request-id "$(req_id)")"
TASK_ID="$(echo "$RESUME_JSON" | jq -r '.data.focus_task_id // ""')"

if [ -n "$TASK_ID" ]; then
  vybe log --agent "$VYBE_AGENT" --request-id "$(req_id)" \
    --kind progress --task "$TASK_ID" --msg "working" >/dev/null

  # Do work...

  vybe task close --agent "$VYBE_AGENT" --request-id "$(req_id)" \
    --id "$TASK_ID" --outcome done --summary "Completed" >/dev/null
fi
```

## Claim-based loop (multi-agent queues)

```bash
#!/usr/bin/env bash
set -euo pipefail

req_id() { printf 'req_%s_%s\n' "$(date +%s)" "$RANDOM"; }

CLAIM="$(vybe task claim --agent "$VYBE_AGENT" --request-id "$(req_id)" --ttl-minutes 10)"
TASK_ID="$(echo "$CLAIM" | jq -r '.data.task.id // ""')"

if [ -n "$TASK_ID" ]; then
  # Do work...
  vybe task close --agent "$VYBE_AGENT" --request-id "$(req_id)" \
    --id "$TASK_ID" --outcome done --summary "Completed" >/dev/null
fi
```

## Required operating rules

1. Parse success JSON from `stdout` (`{schema_version, success, data}`).
2. Parse structured errors from `stderr`.
3. Include `--request-id` on every mutating command.
4. Reuse the same `--request-id` for retries.
5. Keep a stable agent identity (`--agent` or `VYBE_AGENT`).

## Related docs

- `usage-examples.md` for more command recipes
- `../integrator/custom-assistant.md` for full integration contract
- `../contributor/idempotent-action-pattern.md` for contributor implementation details
