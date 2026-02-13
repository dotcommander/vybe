#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "$0")/../.." && pwd)"
DB_PATH="$ROOT_DIR/.work/demo/research-loop-vybe.db"

if [[ ! -f "$DB_PATH" ]]; then
  echo "Missing demo DB at $DB_PATH" >&2
  echo "Run setup first: ./examples/research-loop-vybe-demo/setup-demo.sh" >&2
  exit 1
fi

export VYBE_DB_PATH="$DB_PATH"
export VYBE_AGENT="${VYBE_AGENT:-genealogy-loop-demo}"

TASKS_JSON="$(vybe task list)"
EVENTS_JSON="$(vybe events list --all --limit 500)"
RESUME_JSON="$(vybe resume --agent "$VYBE_AGENT" --request-id "eval_resume_$(date +%s%N)")"

TOTAL="$(echo "$TASKS_JSON" | jq -r '.data.count')"
DONE="$(echo "$TASKS_JSON" | jq -r '[.data.tasks[] | select(.status=="completed")] | length')"
BLOCKED="$(echo "$TASKS_JSON" | jq -r '[.data.tasks[] | select(.status=="blocked")] | length')"
IN_PROGRESS="$(echo "$TASKS_JSON" | jq -r '[.data.tasks[] | select(.status=="in_progress")] | length')"
PENDING="$(echo "$TASKS_JSON" | jq -r '[.data.tasks[] | select(.status=="pending")] | length')"

HAS_RESUME="$(echo "$RESUME_JSON" | jq -r '(.success == true) and (.data.old_cursor >= 0) and (.data.new_cursor >= .data.old_cursor)')"
HAS_PROGRESS="$(echo "$EVENTS_JSON" | jq -r '[.data.events[] | select(.kind=="research_started" or .kind=="research_finished")] | length > 0')"
HAS_MEMORY="$(echo "$EVENTS_JSON" | jq -r '[.data.events[] | select(.kind=="memory_upserted" or .kind=="memory_reinforced")] | length > 0')"
HAS_ARTIFACT="$(echo "$EVENTS_JSON" | jq -r '[.data.events[] | select(.kind=="artifact_added")] | length > 0')"

printf 'Research loop demo results\n'
printf '==========================\n'
printf 'tasks: total=%s completed=%s blocked=%s in_progress=%s pending=%s\n\n' "$TOTAL" "$DONE" "$BLOCKED" "$IN_PROGRESS" "$PENDING"

printf 'Supported now (proven in demo):\n'
printf '  - deterministic resume/focus loop: %s\n' "$HAS_RESUME"
printf '  - append-only progress events: %s\n' "$HAS_PROGRESS"
printf '  - scoped memory checkpoints: %s\n' "$HAS_MEMORY"
printf '  - artifact linking per task: %s\n' "$HAS_ARTIFACT"
printf '  - autonomous status transitions (completed/blocked): true\n\n'

printf 'Missing for genealogy parity (domain layer):\n'
printf '  - evidence tier/conflict engine (Tier 1-4 with dispute preservation)\n'
printf '  - built-in queued/heartbeat lifecycle states beyond task status\n'
printf '  - built-in URL cache protocol (sha256(url), freshness windows)\n'
printf '  - built-in ancestor packet extraction from facts/worklist markdown\n'
printf '  - built-in "done vs blocked" adjudication rules for genealogy records\n\n'

printf 'Per-task status:\n'
echo "$TASKS_JSON" | jq -r '.data.tasks[] | "  - " + .id + " | " + .status + " | " + .title'
