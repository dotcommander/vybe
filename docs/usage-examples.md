# Vibe Usage Examples

Practical examples for autonomous agents using `vibe`.

Use this page as a copy-paste playbook. Pick the section you need and run it as-is.

## Reading Guide

1. Start with `Agent Bootstrap`
2. Use `Resume Loop` for crash-safe execution
3. Add `Memory`, `Artifacts`, and `Events` as needed

## JSON Contract

Successful command responses use this envelope:

```json
{
  "schema_version": "v1",
  "success": true,
  "data": {}
}
```

- Parse fields under `.data`
- Parse failures from `stderr` structured JSON logs
- `vibe events tail --jsonl` emits raw event objects (one per line)

## 1) Agent Bootstrap

```bash
#!/usr/bin/env bash
set -euo pipefail

AGENT="${VIBE_AGENT:-worker-001}"

vibe agent status --agent "$AGENT" >/dev/null || true

vibe agent init --agent "$AGENT" --request-id "init_${AGENT}_1" \
  | jq -r '.data.agent_name'

vibe agent status --agent "$AGENT" \
  | jq -r '.data.last_seen_event_id'
```

## 2) Create, Start, Complete Task

```bash
#!/usr/bin/env bash
set -euo pipefail

AGENT="${VIBE_AGENT:-worker-001}"

TASK_ID=$(vibe task create \
  --agent "$AGENT" \
  --request-id "task_create_1" \
  --title "Process batch 1" \
  --desc "Process items 1-1000" \
  | jq -r '.data.task.id')

vibe task start --agent "$AGENT" --request-id "task_start_1" \
  --id "$TASK_ID" >/dev/null

vibe task heartbeat --agent "$AGENT" --request-id "task_heartbeat_1" \
  --id "$TASK_ID" --ttl-minutes 5 >/dev/null

vibe log --agent "$AGENT" --request-id "log_progress_1" \
  --kind progress --task "$TASK_ID" \
  --msg "Processed 500/1000 items" >/dev/null

vibe task set-status --agent "$AGENT" --request-id "task_complete_1" \
  --id "$TASK_ID" --status completed >/dev/null
```

## 3) Resume Loop (Crash-Safe)

```bash
#!/usr/bin/env bash
set -euo pipefail

AGENT="${VIBE_AGENT:-worker-001}"

RESUME=$(vibe resume --agent "$AGENT" --request-id "resume_1")

FOCUS_TASK=$(echo "$RESUME" | jq -r '.data.focus_task_id // ""')
OLD_CURSOR=$(echo "$RESUME" | jq -r '.data.old_cursor')
NEW_CURSOR=$(echo "$RESUME" | jq -r '.data.new_cursor')

echo "cursor: ${OLD_CURSOR} -> ${NEW_CURSOR}" >&2
if [ -n "$FOCUS_TASK" ]; then
  echo "focus task: $FOCUS_TASK" >&2
fi
```

## 4) Brief (Read-Only)

```bash
#!/usr/bin/env bash
set -euo pipefail

AGENT="${VIBE_AGENT:-worker-001}"

CURSOR_BEFORE=$(vibe agent status --agent "$AGENT" | jq -r '.data.last_seen_event_id')

BRIEF=$(vibe brief --agent "$AGENT")
TASK_ID=$(echo "$BRIEF" | jq -r '.data.brief.task.id // ""')
MEMORY_COUNT=$(echo "$BRIEF" | jq -r '.data.brief.relevant_memory | length')

CURSOR_AFTER=$(vibe agent status --agent "$AGENT" | jq -r '.data.last_seen_event_id')

test "$CURSOR_BEFORE" = "$CURSOR_AFTER"
echo "task=${TASK_ID:-none} memory_entries=$MEMORY_COUNT" >&2
```

## 5) Memory (Scope + TTL)

```bash
#!/usr/bin/env bash
set -euo pipefail

AGENT="${VIBE_AGENT:-worker-001}"
TASK_ID="${1:-task_demo}"

vibe memory set --agent "$AGENT" --request-id "mem_set_1" \
  --key checkpoint --value "6000" --type number \
  --scope task --scope-id "$TASK_ID" --expires-in 24h >/dev/null

vibe memory get --key checkpoint --scope task --scope-id "$TASK_ID" | jq -r '.data.value'
```

## 6) Artifacts

```bash
#!/usr/bin/env bash
set -euo pipefail

AGENT="${VIBE_AGENT:-worker-001}"
TASK_ID="$1"

vibe artifact add --agent "$AGENT" --request-id "artifact_add_1" \
  --task "$TASK_ID" --path /tmp/output.json \
  --type application/json >/dev/null

vibe artifact list --task "$TASK_ID" | jq -r '.data.count'
```

## 7) Events Query + Tail

```bash
#!/usr/bin/env bash
set -euo pipefail

AGENT="${VIBE_AGENT:-worker-001}"

vibe events list --agent "$AGENT" --limit 20 \
  | jq -r '.data.events[] | "[\(.id)] \(.kind) \(.message)"'

vibe events tail --all --jsonl --once \
  | jq -r '.kind + " " + .message'
```

## 8) Idempotency Replay

```bash
#!/usr/bin/env bash
set -euo pipefail

AGENT="${VIBE_AGENT:-worker-001}"
REQ_ID="log_replay_123"

E1=$(vibe log --agent "$AGENT" --request-id "$REQ_ID" \
  --kind note --msg "hello" | jq -r '.data.event_id')
E2=$(vibe log --agent "$AGENT" --request-id "$REQ_ID" \
  --kind note --msg "hello" | jq -r '.data.event_id')

test "$E1" = "$E2"
echo "replayed event_id=$E1" >&2
```

## 9) Ingest Claude Code History

```bash
#!/usr/bin/env bash
set -euo pipefail

AGENT="${VIBE_AGENT:-claude}"

vibe ingest history --agent "$AGENT" --dry-run \
  | jq -r '.data | "total=\(.total) filtered=\(.filtered)"'
vibe ingest history --agent "$AGENT" \
  | jq -r '.data | "imported=\(.imported) skipped=\(.skipped)"'
vibe ingest history --agent "$AGENT" \
  --project /Users/me/myapp --since 2026-02-01 \
  | jq -r '.data | "imported=\(.imported)"'
```

## 10) Project-Scoped Resume

```bash
#!/usr/bin/env bash
set -euo pipefail

AGENT="${VIBE_AGENT:-claude}"
PROJECT="$(pwd)"

RESUME=$(vibe resume --agent "$AGENT" \
  --request-id "resume_$(date +%s)" --project "$PROJECT")
echo "$RESUME" | jq -r '.data.prompt'
echo "focus: $(echo "$RESUME" | jq -r '.data.focus_task_id // "none"')" >&2
```

## 11) OpenCode Bridge

```bash
vibe hook install --opencode

vibe events list --all --limit 100 \
  | jq -r '.data.events[] | select(.kind=="todo_snapshot" \
    or (.agent_name|startswith("opencode")))'
```

## 12) Genealogy-Style Research Loop Demo

```bash
cd ~/go/src/vibe

./examples/research-loop-vibe-demo/setup-demo.sh
./examples/research-loop-vibe-demo/run-demo.sh
./examples/research-loop-vibe-demo/evaluate-support.sh
```

Demo assets:

- `examples/research-loop-vibe-demo/README.md`
- `examples/research-loop-vibe-demo/setup-demo.sh`
- `examples/research-loop-vibe-demo/mock-research-worker.sh`
- `examples/research-loop-vibe-demo/run-demo.sh`
- `examples/research-loop-vibe-demo/evaluate-support.sh`

## 13) Claim Next Task (Server-Side Selection)

```bash
#!/usr/bin/env bash
set -euo pipefail

AGENT="${VIBE_AGENT:-worker-001}"

# Atomically pick next eligible pending task, claim it, set in_progress, focus agent.
# Returns null task when queue is empty.
CLAIM=$(vibe task claim \
  --agent "$AGENT" \
  --request-id "claim_$(date +%s)" \
  --ttl-minutes 10)

TASK_ID=$(echo "$CLAIM" | jq -r '.data.task.id // ""')
if [ -z "$TASK_ID" ]; then
  echo "no work available" >&2
  exit 0
fi

echo "claimed: $TASK_ID" >&2

# Do work...

# Close with structured outcome.
vibe task close \
  --agent "$AGENT" \
  --request-id "close_$(date +%s)" \
  --id "$TASK_ID" \
  --outcome done \
  --summary "Processed successfully" >/dev/null
```

## 14) Close Task (Blocked)

```bash
#!/usr/bin/env bash
set -euo pipefail

AGENT="${VIBE_AGENT:-worker-001}"
TASK_ID="$1"

vibe task close \
  --agent "$AGENT" \
  --request-id "close_$(date +%s)" \
  --id "$TASK_ID" \
  --outcome blocked \
  --summary "External API unreachable" \
  --blocked-reason "failure:api_timeout" >/dev/null
```

## Command Quick Reference

- `vibe agent init|status|focus`
- `vibe task create|start|claim|close|heartbeat|gc|set-status|get|list|delete|add-dep|remove-dep`
- `vibe log`
- `vibe memory set|get|list|delete|touch|query|compact|gc`
- `vibe artifact add|get|list`
- `vibe events list|tail|summarize`
- `vibe resume --project`
- `vibe brief`
- `vibe project create|get|list|delete`
- `vibe session digest`
- `vibe ingest history`
- `vibe run`
- `vibe hook install|uninstall|session-start|prompt|tool-failure|checkpoint|task-completed|retrospective`
- `vibe status`
- `vibe upgrade`
- `vibe schema`
