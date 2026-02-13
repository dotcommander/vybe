#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "$0")/../.." && pwd)"
DB_PATH="$ROOT_DIR/.work/demo/research-loop-vybe.db"
WORKER="$ROOT_DIR/examples/research-loop-vybe-demo/mock-research-worker.sh"

if [[ ! -f "$DB_PATH" ]]; then
  echo "Missing demo DB at $DB_PATH" >&2
  echo "Run setup first: ./examples/research-loop-vybe-demo/setup-demo.sh" >&2
  exit 1
fi

if [[ -z "${VYBE_AGENT:-}" ]]; then
  export VYBE_AGENT="genealogy-loop-demo"
fi
export VYBE_DB_PATH="$DB_PATH"

PROJECT_ID="$(vybe project list | jq -r '.data.projects[0].id // ""')"

if [[ -z "$PROJECT_ID" ]]; then
  echo "No demo project found. Re-run setup script." >&2
  exit 1
fi

vybe loop \
  --agent "$VYBE_AGENT" \
  --project "$PROJECT_ID" \
  --max-tasks 10 \
  --max-fails 5 \
  --task-timeout 30s \
  --cooldown 100ms \
  --command "$WORKER"
