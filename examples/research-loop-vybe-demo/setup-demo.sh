#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "$0")/../.." && pwd)"
DB_PATH="$ROOT_DIR/.work/demo/research-loop-vybe.db"
OUT_DIR="$ROOT_DIR/output/research-loop-vybe-demo"

mkdir -p "$ROOT_DIR/.work/demo" "$OUT_DIR"
rm -f "$DB_PATH"
rm -f "$OUT_DIR"/*.md 2>/dev/null || true

export VYBE_DB_PATH="$DB_PATH"
export VYBE_AGENT="genealogy-loop-demo"

# Use the demo DB directory as a stable project ID.
PROJECT_ID="$ROOT_DIR/.work/demo"

rid() {
  printf '%s_%s_%s' "$1" "$(date +%s%N)" "$RANDOM"
}

# resume auto-creates agent state on first call; no separate init needed.
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
  'ACF-1001 Eliza Louisa Smith burial lookup' \
  'Find burial location using local-first corpus and update facts note')"

T2="$(create_task \
  'ACF-1002 Rufus Leonard Ferrell parents verification' \
  'Confirm parent evidence and capture citation-level notes')"

T3="$(create_task \
  'ACF-1003 Isabella Elizabeth Nelson death window BLOCKED_DEMO' \
  'Demonstrate blocked path when evidence requires unavailable source')"

T4="$(create_task \
  'ACF-1004 Ballard P. Burgess 1870 census extraction' \
  'Extract and log household details for 1870 census candidate')"

# Chain dependencies to mimic queue progression.
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

printf 'Demo DB ready:\n'
printf '  VYBE_DB_PATH=%s\n' "$VYBE_DB_PATH"
printf '  VYBE_AGENT=%s\n' "$VYBE_AGENT"
printf '  PROJECT_ID=%s\n' "$PROJECT_ID"
printf '  TASKS=%s,%s,%s,%s\n' "$T1" "$T2" "$T3" "$T4"
