#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "$0")/../.." && pwd)"
DB_PATH="$ROOT_DIR/.work/demo/research-loop-vybe.db"
OUT_DIR="$ROOT_DIR/output/research-loop-vybe-demo"
WORKER="$ROOT_DIR/examples/research-loop-vybe-demo/worker.sh"

mkdir -p "$ROOT_DIR/.work/demo" "$OUT_DIR"
rm -f "$DB_PATH"
rm -f "$OUT_DIR"/*.md 2>/dev/null || true

export VYBE_DB_PATH="$DB_PATH"
export VYBE_AGENT="genealogy-loop-demo"

PROJECT_ID="$ROOT_DIR/.work/demo"

rid() {
  printf '%s_%s_%s' "$1" "$(date +%s)" "$$_$RANDOM"
}

# ---------- setup ----------

vybe resume \
  --agent "$VYBE_AGENT" \
  --request-id "$(rid init)" \
  --project-dir "$PROJECT_ID" >/dev/null

create_task() {
  local title="$1"
  local desc="$2"
  vybe task create \
    --agent "$VYBE_AGENT" \
    --request-id "$(rid task)" \
    --project-id "$PROJECT_ID" \
    --title "$title" \
    --desc "$desc" | jq -r '.data.task.id'
}

T1="$(create_task \
  'ACF-1001 Amelia Earhart disappearance records' \
  'Search for death/burial records and update facts note')"

T2="$(create_task \
  'ACF-1002 Frederick Douglass parentage verification' \
  'Confirm parent evidence and capture citation-level notes')"

T3="$(create_task \
  'ACF-1003 Harriet Tubman death window BLOCKED_DEMO' \
  'Demonstrate blocked path when evidence requires unavailable source')"

T4="$(create_task \
  'ACF-1004 Sojourner Truth 1870 census extraction' \
  'Extract and log household details for 1870 census candidate')"

vybe task add-dep --agent "$VYBE_AGENT" --request-id "$(rid dep)" --id "$T2" --depends-on "$T1" >/dev/null
vybe task add-dep --agent "$VYBE_AGENT" --request-id "$(rid dep)" --id "$T3" --depends-on "$T2" >/dev/null
vybe task add-dep --agent "$VYBE_AGENT" --request-id "$(rid dep)" --id "$T4" --depends-on "$T3" >/dev/null

vybe memory set \
  --agent "$VYBE_AGENT" \
  --request-id "$(rid mem)" \
  --key evidence_mode \
  --value strict-image \
  --scope project \
  --scope-id "$PROJECT_ID" >/dev/null

vybe memory set \
  --agent "$VYBE_AGENT" \
  --request-id "$(rid mem)" \
  --key local_first \
  --value true \
  --type boolean \
  --scope project \
  --scope-id "$PROJECT_ID" >/dev/null

printf 'Setup complete: 4 tasks seeded, DB at %s\n\n' "$DB_PATH"

# ---------- run loop ----------

vybe loop \
  --agent "$VYBE_AGENT" \
  --project-dir "$PROJECT_ID" \
  --max-tasks 10 \
  --max-fails 5 \
  --task-timeout 30s \
  --cooldown 100ms \
  --command "$WORKER"

# ---------- summary ----------

printf '\n'
TASKS_JSON="$(vybe task list)"
TOTAL="$(echo "$TASKS_JSON" | jq -r '.data.count')"
DONE="$(echo "$TASKS_JSON" | jq -r '[.data.tasks[] | select(.status=="completed")] | length')"
BLOCKED="$(echo "$TASKS_JSON" | jq -r '[.data.tasks[] | select(.status=="blocked")] | length')"
IN_PROGRESS="$(echo "$TASKS_JSON" | jq -r '[.data.tasks[] | select(.status=="in_progress")] | length')"
PENDING="$(echo "$TASKS_JSON" | jq -r '[.data.tasks[] | select(.status=="pending")] | length')"

printf 'Summary\n'
printf '=======\n'
printf 'tasks: total=%s completed=%s blocked=%s in_progress=%s pending=%s\n\n' \
  "$TOTAL" "$DONE" "$BLOCKED" "$IN_PROGRESS" "$PENDING"

printf 'Per-task status:\n'
echo "$TASKS_JSON" | jq -r '.data.tasks[] | "  " + .status + "  " + .title'
