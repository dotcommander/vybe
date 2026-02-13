# Common Tasks

Purpose: copy/paste recipes for common `vybe` workflows.

Assumes:

- `VYBE_AGENT` is set
- `jq` is installed
- mutating commands include `--request-id`

Policy owner for machine I/O and retry behavior: `connect-assistant.md`.

## 1) Initialize agent state

```bash
#!/usr/bin/env bash
set -euo pipefail

AGENT="${VYBE_AGENT:-worker-001}"

vybe agent init --agent "$AGENT" --request-id "init_${AGENT}_1"
vybe agent status --agent "$AGENT" | jq -r '.data.last_seen_event_id'
```

## 2) Create task and start work

```bash
#!/usr/bin/env bash
set -euo pipefail

AGENT="${VYBE_AGENT:-worker-001}"

TASK_ID=$(vybe task create \
  --agent "$AGENT" \
  --request-id "task_create_1" \
  --title "Process batch 1" \
  --desc "Process items 1-1000" \
  | jq -r '.data.task.id')

vybe task start --agent "$AGENT" --request-id "task_start_1" --id "$TASK_ID"
```

## 3) Resume after restart/crash

```bash
#!/usr/bin/env bash
set -euo pipefail

AGENT="${VYBE_AGENT:-worker-001}"

RESUME=$(vybe resume --agent "$AGENT" --request-id "resume_1")
echo "$RESUME" | jq -r '.data.focus_task_id // ""'
echo "$RESUME" | jq -r '.data.prompt'
```

## 4) Log progress and close task

```bash
#!/usr/bin/env bash
set -euo pipefail

AGENT="${VYBE_AGENT:-worker-001}"
TASK_ID="$1"

vybe log --agent "$AGENT" --request-id "log_progress_1" \
  --kind progress --task "$TASK_ID" --msg "Processed 500/1000 items"

vybe task close --agent "$AGENT" --request-id "task_close_1" \
  --id "$TASK_ID" --outcome done --summary "Completed"
```

## 5) Save and read memory

```bash
#!/usr/bin/env bash
set -euo pipefail

AGENT="${VYBE_AGENT:-worker-001}"
TASK_ID="$1"

vybe memory set --agent "$AGENT" --request-id "mem_set_1" \
  --key checkpoint --value "6000" --type number \
  --scope task --scope-id "$TASK_ID"

vybe memory get --key checkpoint --scope task --scope-id "$TASK_ID" \
  | jq -r '.data.value'
```

## 6) Attach artifact

```bash
#!/usr/bin/env bash
set -euo pipefail

AGENT="${VYBE_AGENT:-worker-001}"
TASK_ID="$1"

vybe artifact add --agent "$AGENT" --request-id "artifact_add_1" \
  --task "$TASK_ID" --path /tmp/output.json --type application/json
```

## 7) Claim next task (queue workers)

```bash
#!/usr/bin/env bash
set -euo pipefail

AGENT="${VYBE_AGENT:-worker-001}"

CLAIM=$(vybe task claim --agent "$AGENT" --request-id "claim_1" --ttl-minutes 10)
TASK_ID=$(echo "$CLAIM" | jq -r '.data.task.id // ""')

if [ -n "$TASK_ID" ]; then
  vybe task close --agent "$AGENT" --request-id "close_1" \
    --id "$TASK_ID" --outcome done --summary "Processed successfully"
fi
```

## 8) Install or remove hooks

```bash
# Claude Code
vybe hook install
vybe hook uninstall

# OpenCode
vybe hook install --opencode
vybe hook uninstall --opencode
```

## More details

- `setup.md` for install and baseline loop
- `connect-assistant.md` for custom integration contracts
- `../README.md` for the shortest onboarding path
