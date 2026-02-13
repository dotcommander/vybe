# Setup

Purpose: connect one autonomous agent and run the baseline loop.

For assistant/plugin contract details, use `connect-assistant.md`.

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

If your worker pool has multiple concurrent agents, use the claim-based loop below.

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

- Include `--request-id` on every mutating command.
- Keep a stable agent identity (`--agent` or `VYBE_AGENT`).
- For full machine I/O and idempotency policy, see `connect-assistant.md`.

## Verification

Run this after bootstrap:

```bash
vybe status
vybe resume --agent "$VYBE_AGENT" --request-id "verify_resume_1"
```

Pass condition: both commands return success JSON and `resume` returns a packet.

## Related docs

- `common-tasks.md` for copy/paste recipes
- `connect-assistant.md` for full integration contract
