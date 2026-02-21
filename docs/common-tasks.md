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

# Resume initializes agent state on first run; --peek avoids cursor advancement
vybe resume --agent "$AGENT" --peek | jq -r '.data.focus_task_id // ""'
vybe status --agent "$AGENT" | jq -r '.data.last_seen_event_id'
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

vybe task begin --agent "$AGENT" --request-id "task_start_1" --id "$TASK_ID"
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

# Log progress via push (preferred — single atomic call)
vybe push --agent "$AGENT" --request-id "log_progress_1" --json "{
  \"task_id\": \"$TASK_ID\",
  \"event\": {\"kind\": \"progress\", \"message\": \"Processed 500/1000 items\"},
  \"task_status\": {\"status\": \"completed\", \"summary\": \"Completed\"}
}"
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

vybe push --agent "$AGENT" --request-id "artifact_add_1" --json "{
  \"task_id\": \"$TASK_ID\",
  \"artifacts\": [{\"file_path\": \"/tmp/output.json\"}]
}"
```

## 7) Pick and complete the next task

```bash
#!/usr/bin/env bash
set -euo pipefail

AGENT="${VYBE_AGENT:-worker-001}"

# resume selects the next pending task deterministically
TASK_ID=$(vybe resume --agent "$AGENT" --request-id "resume_work_1" | jq -r '.data.focus_task_id // ""')

if [ -n "$TASK_ID" ]; then
  vybe task begin --agent "$AGENT" --request-id "begin_1" --id "$TASK_ID"
  # ... do work ...
  vybe push --agent "$AGENT" --request-id "close_1" --json "{
    \"task_id\": \"$TASK_ID\",
    \"task_status\": {\"status\": \"completed\", \"summary\": \"Processed successfully\"}
  }"
fi
```

## 8) Atomic batch push

Report results in a single call instead of 3-5 separate mutations:

```bash
vybe push --agent claude --request-id push_$(date +%s) --json '{
  "task_id": "task_123",
  "event": {"kind": "progress", "message": "Implemented feature X"},
  "memories": [{"key": "api_endpoint", "value": "/v2/users", "scope": "task", "scope_id": "task_123"}],
  "artifacts": [{"file_path": "/tmp/output.json"}],
  "task_status": {"status": "completed", "summary": "Feature X implemented and tested"}
}'
```

Or pipe via stdin:

```bash
echo '{"event": {"kind": "note", "message": "Session checkpoint"}}' | \
  vybe push --agent claude --request-id push_$(date +%s)
```

## 9) Install or remove hooks

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
- `command-reference.md` for full command/subcommand coverage
- `../README.md` for the shortest onboarding path
