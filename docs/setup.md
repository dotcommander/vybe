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

# resume auto-creates agent state on first call — no separate init needed
vybe resume --agent "$VYBE_AGENT" --request-id "$(req_id)" >/dev/null
```

## Core loop

```bash
#!/usr/bin/env bash
set -euo pipefail

req_id() { printf 'req_%s_%s\n' "$(date +%s)" "$RANDOM"; }

RESUME_JSON="$(vybe resume --agent "$VYBE_AGENT" --request-id "$(req_id)")"
TASK_ID="$(echo "$RESUME_JSON" | jq -r '.data.focus_task_id // ""')"

if [ -n "$TASK_ID" ]; then
  vybe push --agent "$VYBE_AGENT" --request-id "$(req_id)" --json \
    "{\"task_id\":\"$TASK_ID\",\"event\":{\"kind\":\"progress\",\"message\":\"working\"}}" >/dev/null

  # Do work...

  vybe task complete --agent "$VYBE_AGENT" --request-id "$(req_id)" \
    --id "$TASK_ID" --outcome done --summary "Completed" >/dev/null
fi
```

If your worker pool has multiple concurrent agents, use `vybe resume` — it applies the deterministic 5-rule focus selection algorithm, which is concurrency-safe and claim-free. Call `vybe task begin` after resume to mark the task in_progress.

## Required operating rules

- Include `--request-id` on every mutating command.
- Keep a stable agent identity (`--agent` or `VYBE_AGENT`).
- For full machine I/O and idempotency policy, see `connect-assistant.md`.

## Optional maintenance tuning

`vybe` runs maintenance internally during checkpoint/session-end. You can tune it in `~/.config/vybe/config.yaml`:

```yaml
events_retention_days: 30
events_prune_batch: 500
events_summarize_threshold: 200
events_summarize_keep_recent: 50
```

Use `vybe status` to confirm effective values in `.data.maintenance`.

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
- `command-reference.md` for complete command/subcommand map
