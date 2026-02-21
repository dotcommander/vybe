#!/usr/bin/env bash
# e2e_test.sh — End-to-end tests for the vybe CLI
# Uses a temporary SQLite DB; cleans up on exit.
#
# Usage: bash test/e2e_test.sh
#   (must be run from the repo root where the vybe binary lives)

set -uo pipefail

# ---------------------------------------------------------------------------
# Setup
# ---------------------------------------------------------------------------

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
VYBE="${SCRIPT_DIR}/../vybe"

if [[ ! -x "$VYBE" ]]; then
  echo "ERROR: vybe binary not found at $VYBE — run: go build -o vybe ./cmd/vybe" >&2
  exit 1
fi

if ! command -v jq &>/dev/null; then
  echo "ERROR: jq is required but not installed" >&2
  exit 1
fi

DB="/tmp/vybe-e2e-test-$$.db"
ARTIFACT_FILE="/tmp/vybe-e2e-artifact-$$.txt"
INT_ART_FILE="/tmp/vybe-e2e-int-artifact-$$.txt"
export VYBE_AGENT="e2e-test"

cleanup() {
  local ts
  ts=$(date +%s)
  mv "$DB" "/tmp/deleted-e2e-db-${ts}" 2>/dev/null || true
  mv "${DB}-shm" "/tmp/deleted-e2e-shm-${ts}" 2>/dev/null || true
  mv "${DB}-wal" "/tmp/deleted-e2e-wal-${ts}" 2>/dev/null || true
  mv "$ARTIFACT_FILE" "/tmp/deleted-e2e-art-${ts}" 2>/dev/null || true
  mv "$INT_ART_FILE" "/tmp/deleted-e2e-int-art-${ts}" 2>/dev/null || true
}
trap cleanup EXIT

# Initialize DB (run migrations)
"$VYBE" --db-path "$DB" upgrade 2>/dev/null > /dev/null

PASS=0
FAIL=0
FAIL_NAMES=()

# ---------------------------------------------------------------------------
# Colors
# ---------------------------------------------------------------------------

CYAN='\033[0;36m'
GREEN='\033[0;32m'
RED='\033[0;31m'
YELLOW='\033[0;33m'
RESET='\033[0m'

section() {
  echo ""
  echo -e "${CYAN}=== $1 ===${RESET}"
}

pass() {
  echo -e "  ${GREEN}PASS${RESET}  $1"
  PASS=$((PASS + 1))
}

fail() {
  echo -e "  ${RED}FAIL${RESET}  $1"
  echo -e "         ${YELLOW}$2${RESET}"
  FAIL=$((FAIL + 1))
  FAIL_NAMES+=("$1")
}

# ---------------------------------------------------------------------------
# Run vybe capturing stdout only (stderr has log lines, not JSON)
# ---------------------------------------------------------------------------
vybe() {
  "$VYBE" --db-path "$DB" "$@" 2>/dev/null
}

# ---------------------------------------------------------------------------
# Assertions
# ---------------------------------------------------------------------------

# Assert .success == true in JSON envelope
assert_success() {
  local test_name="$1"
  local json="$2"
  local ok
  ok=$(echo "$json" | jq -r '.success' 2>/dev/null)
  if [[ "$ok" == "true" ]]; then
    pass "$test_name"
  else
    local err
    err=$(echo "$json" | jq -r '.error // empty' 2>/dev/null)
    fail "$test_name" "success=false error='$err'"
  fi
}

# Assert jq expression evaluates to truthy (non-null, non-false, non-empty)
# Usage: assert_jq "name" "$json" 'jq_expression'
assert_jq() {
  local test_name="$1"
  local json="$2"
  local expr="$3"
  local result
  result=$(echo "$json" | jq -r "$expr" 2>/dev/null)
  if [[ "$result" == "null" || "$result" == "false" || "$result" == "" ]]; then
    fail "$test_name" "jq '$expr' => '$result' (expected truthy)"
  else
    pass "$test_name"
  fi
}

# Assert jq expression with --arg substitution
# Usage: assert_jq_arg "name" "$json" 'jq_expression' arg_name arg_value
assert_jq_arg() {
  local test_name="$1"
  local json="$2"
  local expr="$3"
  local arg_name="$4"
  local arg_val="$5"
  local result
  result=$(echo "$json" | jq -r --arg "$arg_name" "$arg_val" "$expr" 2>/dev/null)
  if [[ "$result" == "null" || "$result" == "false" || "$result" == "" ]]; then
    fail "$test_name" "jq --arg $arg_name='$arg_val' '$expr' => '$result' (expected truthy)"
  else
    pass "$test_name"
  fi
}

# Assert two string values are equal
assert_eq() {
  local test_name="$1"
  local got="$2"
  local want="$3"
  if [[ "$got" == "$want" ]]; then
    pass "$test_name"
  else
    fail "$test_name" "got='$got' want='$want'"
  fi
}

# Assert a jq result equals a string
assert_jq_eq() {
  local test_name="$1"
  local json="$2"
  local expr="$3"
  local want="$4"
  local got
  got=$(echo "$json" | jq -r "$expr" 2>/dev/null)
  if [[ "$got" == "$want" ]]; then
    pass "$test_name"
  else
    fail "$test_name" "jq '$expr' => '$got' want '$want'"
  fi
}

# ---------------------------------------------------------------------------
# Request ID generation — timestamp + RANDOM to avoid collisions.
# NOTE: rid() is called inside $(...) subshells, so file-based or
# array-based counters do not work (subshell changes don't persist).
# Using date nanoseconds + RANDOM gives unique IDs without shared state.
# ---------------------------------------------------------------------------
rid() {
  local prefix="$1"
  echo "e2e_${prefix}_$(date +%s%N)_${RANDOM}"
}

# ===========================================================================
# INDIVIDUAL COMMAND TESTS
# ===========================================================================

section "schema"

SCHEMA=$(vybe schema commands)
assert_success "schema: success" "$SCHEMA"
assert_jq "schema: data.commands is array" "$SCHEMA" '.data.commands | type == "array"'
assert_jq "schema: data.commands non-empty" "$SCHEMA" '.data.commands | length > 0'

# ---------------------------------------------------------------------------
section "status --check"

STATUS=$(vybe status --check)
assert_success "status: success" "$STATUS"
assert_jq "status: query_ok is true" "$STATUS" '.data.query_ok == true'
assert_jq "status: db path set" "$STATUS" '.data.db.path | length > 0'

# ---------------------------------------------------------------------------
# Project CLI is removed. Use a fixed project ID for grouping tasks and memory.
# No FK constraint on tasks.project_id — arbitrary string is valid.
section "project (fixed ID — no project CLI)"

PROJECT_ID="e2e-test-project"

# ---------------------------------------------------------------------------
section "task create & list"

TASK=$(vybe task create \
  --title "Initial Task" \
  --desc "Task description for e2e" \
  --project "$PROJECT_ID" \
  --request-id "$(rid task_create)")
assert_success "task create: success" "$TASK"
assert_jq "task create: has id" "$TASK" '.data.task.id | length > 0'
assert_jq_eq "task create: title matches" "$TASK" '.data.task.title' "Initial Task"
assert_jq_eq "task create: status is pending" "$TASK" '.data.task.status' "pending"
assert_jq_arg "task create: project_id matches" "$TASK" \
  '.data.task.project_id == $pid' "pid" "$PROJECT_ID"

TASK_ID=$(echo "$TASK" | jq -r '.data.task.id')

TASK_LIST=$(vybe task list)
assert_success "task list: success" "$TASK_LIST"
assert_jq "task list: tasks is array" "$TASK_LIST" '.data.tasks | type == "array"'
assert_jq_arg "task list: contains created task" "$TASK_LIST" \
  '.data.tasks[] | select(.id == $id) | .title == "Initial Task"' "id" "$TASK_ID"

# ---------------------------------------------------------------------------
section "task get"

TASK_GET=$(vybe task get --id "$TASK_ID")
assert_success "task get: success" "$TASK_GET"
# task get returns the task fields directly in .data (not nested under .data.task)
assert_jq_arg "task get: id matches" "$TASK_GET" '.data.id == $id' "id" "$TASK_ID"

# ---------------------------------------------------------------------------
section "task begin"

BEGIN=$(vybe task begin --id "$TASK_ID" --agent e2e-test --request-id "$(rid task_begin)")
assert_success "task begin: success" "$BEGIN"
assert_jq_eq "task begin: status is in_progress" "$BEGIN" '.data.task.status' "in_progress"

TASK_AFTER_BEGIN=$(vybe task get --id "$TASK_ID")
assert_jq_eq "task begin: persisted status" "$TASK_AFTER_BEGIN" '.data.status' "in_progress"

# ---------------------------------------------------------------------------
section "push event & events list"

EVT=$(vybe push \
  --json "{\"task_id\":\"$TASK_ID\",\"event\":{\"kind\":\"progress\",\"message\":\"First progress event\"}}" \
  --request-id "$(rid evt_add)")
assert_success "push event: success" "$EVT"
assert_jq "push event: has event_id" "$EVT" '.data.event_id > 0'
assert_jq_eq "push event: kind matches" "$EVT" '.data.kind' "progress"

EVENT_ID=$(echo "$EVT" | jq '.data.event_id')  # integer, not string

EVT_LIST=$(vybe events list --task-id "$TASK_ID")
assert_success "events list: success" "$EVT_LIST"
assert_jq "events list: events is array" "$EVT_LIST" '.data.events | type == "array"'
# event_id is an integer — use --argjson for comparison, extract result then assert_eq
EVT_FOUND=$(echo "$EVT_LIST" | jq -r --argjson id "$EVENT_ID" \
  '.data.events[] | select(.id == $id) | .kind' 2>/dev/null)
assert_eq "events list: contains created event with correct kind" "$EVT_FOUND" "progress"

# Add a second event with metadata (use distinct rid prefix to avoid command-type collision)
EVT2=$(vybe push \
  --json "{\"task_id\":\"$TASK_ID\",\"event\":{\"kind\":\"tool_call\",\"message\":\"Called a tool\",\"metadata\":{\"tool\":\"bash\",\"result\":\"ok\"}}}" \
  --request-id "$(rid evt_meta)")
assert_success "push event with metadata: success" "$EVT2"
assert_jq "push event with metadata: event_id present" "$EVT2" '.data.event_id > 0'

# ---------------------------------------------------------------------------
section "memory set, get, list, delete"

MEM=$(vybe memory set \
  -k "test-key" \
  -v "hello world" \
  --scope global \
  --request-id "$(rid mem_set)")
assert_success "memory set (global): success" "$MEM"
assert_jq_eq "memory set (global): key returned" "$MEM" '.data.key' "test-key"

MEM_GET=$(vybe memory get -k "test-key" --scope global)
assert_success "memory get (global): success" "$MEM_GET"
assert_jq_eq "memory get (global): value matches" "$MEM_GET" '.data.value' "hello world"

MEM_TASK=$(vybe memory set \
  -k "task-key" \
  -v "task-scoped-value" \
  --scope task \
  --scope-id "$TASK_ID" \
  --request-id "$(rid mem_set)")
assert_success "memory set (task-scoped): success" "$MEM_TASK"
assert_jq_eq "memory set (task-scoped): key returned" "$MEM_TASK" '.data.key' "task-key"

MEM_TASK_GET=$(vybe memory get -k "task-key" --scope task --scope-id "$TASK_ID")
assert_success "memory get (task-scoped): success" "$MEM_TASK_GET"
assert_jq_eq "memory get (task-scoped): value matches" "$MEM_TASK_GET" '.data.value' "task-scoped-value"

MEM_LIST=$(vybe memory list --scope global)
assert_success "memory list (global): success" "$MEM_LIST"
assert_jq "memory list (global): memories is array" "$MEM_LIST" '.data.memories | type == "array"'
assert_jq "memory list (global): contains test-key" "$MEM_LIST" \
  '.data.memories[] | select(.key == "test-key") | .value == "hello world"'

MEM_DEL=$(vybe memory delete -k "test-key" --scope global --request-id "$(rid mem_del)")
assert_success "memory delete: success" "$MEM_DEL"
assert_jq_eq "memory delete: key in response" "$MEM_DEL" '.data.key' "test-key"

MEM_LIST_AFTER=$(vybe memory list --scope global)
KEY_GONE=$(echo "$MEM_LIST_AFTER" | jq '[.data.memories[] | select(.key == "test-key")] | length == 0')
assert_eq "memory delete: key removed from list" "$KEY_GONE" "true"

# ---------------------------------------------------------------------------
section "memory gc"

GC=$(vybe memory gc --request-id "$(rid mem_gc)")
assert_success "memory gc: success" "$GC"

# ---------------------------------------------------------------------------
section "memory list (replaces memory query)"

vybe memory set -k "queryable-key" -v "queryable value" --scope global \
  --request-id "$(rid mem_set)" > /dev/null

MEM_LIST_QUERY=$(vybe memory list --scope global)
assert_success "memory list (query pattern): success" "$MEM_LIST_QUERY"
assert_jq "memory list (query pattern): results is array" "$MEM_LIST_QUERY" '.data.memories | type == "array"'
assert_jq "memory list (query pattern): contains matching entry" "$MEM_LIST_QUERY" \
  '.data.memories[] | select(.key == "queryable-key") | .key == "queryable-key"'

# ---------------------------------------------------------------------------
section "memory with TTL"

MEM_TTL=$(vybe memory set \
  -k "expiring-key" \
  -v "expires soon" \
  --scope global \
  --expires-in "24h" \
  --request-id "$(rid mem_set)")
assert_success "memory set with TTL: success" "$MEM_TTL"
assert_jq_eq "memory set with TTL: key returned" "$MEM_TTL" '.data.key' "expiring-key"

# Verify expires_at by getting it back
MEM_TTL_GET=$(vybe memory get -k "expiring-key" --scope global)
assert_success "memory get TTL entry: success" "$MEM_TTL_GET"
assert_jq "memory get TTL entry: expires_at set" "$MEM_TTL_GET" '.data.expires_at != null'

# ---------------------------------------------------------------------------
section "push artifact & artifacts list"

echo "test artifact content" > "$ARTIFACT_FILE"

ART=$(vybe push \
  --json "{\"task_id\":\"$TASK_ID\",\"artifacts\":[{\"file_path\":\"$ARTIFACT_FILE\",\"content_type\":\"text/plain\"}]}" \
  --request-id "$(rid art_add)")
assert_success "push artifact: success" "$ART"

ART_LIST=$(vybe artifacts list --task-id "$TASK_ID")
assert_success "artifacts list: success" "$ART_LIST"
assert_jq "artifacts list: artifacts is array" "$ART_LIST" '.data.artifacts | type == "array"'
assert_jq_arg "artifacts list: contains artifact" "$ART_LIST" \
  '.data.artifacts[] | select(.file_path == $path) | .file_path == $path' "path" "$ARTIFACT_FILE"

# ---------------------------------------------------------------------------
section "resume (replaces agent init/status/focus)"

# resume auto-creates agent state on first call
AGENT_RESUME=$(vybe resume --agent e2e-test --request-id "$(rid agent_resume)")
assert_success "resume (agent init): success" "$AGENT_RESUME"
assert_jq_eq "resume (agent init): agent_name returned" "$AGENT_RESUME" '.data.agent_name' "e2e-test"

AGENT_STATUS=$(vybe status --agent e2e-test)
assert_success "status --agent: success" "$AGENT_STATUS"
assert_jq "status --agent: has agent_state" "$AGENT_STATUS" '.data.agent_state != null'

# ---------------------------------------------------------------------------
section "resume --focus (replaces agent focus)"

FOCUS=$(vybe resume \
  --agent e2e-test \
  --focus "$TASK_ID" \
  --project "$PROJECT_ID" \
  --request-id "$(rid agent_focus)")
assert_success "resume --focus: success" "$FOCUS"
FOCUS_TASK_ID=$(echo "$FOCUS" | jq -r '.data.brief.task.id // empty')
assert_eq "resume --focus: task_id matches" "$FOCUS_TASK_ID" "$TASK_ID"

# ---------------------------------------------------------------------------
section "resume --peek (replaces brief)"

BRIEF=$(vybe resume --peek --agent e2e-test)
assert_success "resume --peek: success" "$BRIEF"
assert_jq "resume --peek: has brief field" "$BRIEF" '.data.brief != null'
assert_jq "resume --peek: brief.task is not null" "$BRIEF" '.data.brief.task != null'
assert_jq_arg "resume --peek: brief.task.id matches" "$BRIEF" \
  '.data.brief.task.id == $id' "id" "$TASK_ID"
assert_jq "resume --peek: has relevant_memory" "$BRIEF" '.data.brief.relevant_memory != null'
assert_jq "resume --peek: has recent_events" "$BRIEF" '.data.brief.recent_events != null'
assert_jq "resume --peek: has artifacts" "$BRIEF" '.data.brief.artifacts != null'

# ---------------------------------------------------------------------------
section "resume"

RESUME=$(vybe resume --agent e2e-test --request-id "$(rid resume)")
assert_success "resume: success" "$RESUME"
assert_jq "resume: has brief field" "$RESUME" '.data.brief != null'
assert_jq "resume: brief.task is not null" "$RESUME" '.data.brief.task != null'
assert_jq_arg "resume: brief.task.id matches" "$RESUME" \
  '.data.brief.task.id == $id' "id" "$TASK_ID"

# ---------------------------------------------------------------------------
section "task complete"

COMPLETE=$(vybe task complete \
  --id "$TASK_ID" \
  --outcome done \
  --summary "All done" \
  --agent e2e-test \
  --request-id "$(rid task_complete)")
assert_success "task complete: success" "$COMPLETE"
assert_jq_eq "task complete: status is completed" "$COMPLETE" '.data.task.status' "completed"

TASK_AFTER_COMPLETE=$(vybe task get --id "$TASK_ID")
assert_jq_eq "task complete: persisted status" "$TASK_AFTER_COMPLETE" '.data.status' "completed"

# ---------------------------------------------------------------------------
section "task set-status (blocked)"

BLOCK_TASK=$(vybe task create \
  --title "Blocked Task" \
  --desc "This will be blocked" \
  --request-id "$(rid task_create)")
BLOCK_TASK_ID=$(echo "$BLOCK_TASK" | jq -r '.data.task.id')

BLOCK=$(vybe task set-status \
  --id "$BLOCK_TASK_ID" \
  --status blocked \
  --request-id "$(rid task_setstatus)")
assert_success "task set-status (blocked): success" "$BLOCK"
assert_jq_eq "task set-status (blocked): status is blocked" "$BLOCK" '.data.task.status' "blocked"

# ---------------------------------------------------------------------------
section "status (replaces task stats)"

STATS=$(vybe status)
assert_success "status (task counts): success" "$STATS"
# status returns task counts under data.tasks
assert_jq "status (task counts): has tasks key" "$STATS" '.data | has("tasks")'

# ---------------------------------------------------------------------------
section "task list with status filter"

PENDING_LIST=$(vybe task list --status pending)
assert_success "task list --status pending: success" "$PENDING_LIST"
# tasks may be null when no results; coerce to empty array for type check
assert_jq "task list --status pending: tasks is array or null" "$PENDING_LIST" \
  '(.data.tasks // []) | type == "array"'

COMPLETED_LIST=$(vybe task list --status completed)
assert_success "task list --status completed: success" "$COMPLETED_LIST"
assert_jq "task list --status completed: tasks is array or null" "$COMPLETED_LIST" \
  '(.data.tasks // []) | type == "array"'
assert_jq_arg "task list --status completed: contains completed task" "$COMPLETED_LIST" \
  '(.data.tasks // [])[] | select(.id == $id) | .status == "completed"' "id" "$TASK_ID"

# ---------------------------------------------------------------------------
section "events list with kind filter"

KIND_LIST=$(vybe events list --kind progress --task-id "$TASK_ID")
assert_success "events list --kind: success" "$KIND_LIST"
assert_jq "events list --kind: events is array" "$KIND_LIST" '.data.events | type == "array"'
# All returned events must match the filter kind
ALL_MATCH=$(echo "$KIND_LIST" | jq '[.data.events[] | select(.kind != "progress")] | length == 0')
assert_eq "events list --kind: all events match kind" "$ALL_MATCH" "true"

# ===========================================================================
# INTEGRATION FLOW
# ===========================================================================

section "Integration Flow: project -> task -> events -> memory -> artifact -> resume --peek -> resume"

# 1. Use a fixed project ID (no project CLI)
INT_PROJECT_ID="e2e-integration-project"

# 2. Create task in project
INT_TASK=$(vybe task create \
  --title "Integration Task" \
  --desc "Full integration test task" \
  --project "$INT_PROJECT_ID" \
  --request-id "$(rid int_task)")
assert_success "integration: task create" "$INT_TASK"
INT_TASK_ID=$(echo "$INT_TASK" | jq -r '.data.task.id')
assert_jq_arg "integration: task project_id matches" "$INT_TASK" \
  '.data.task.project_id == $pid' "pid" "$INT_PROJECT_ID"

# 3. Begin the task
INT_BEGIN=$(vybe task begin \
  --id "$INT_TASK_ID" \
  --agent e2e-test \
  --request-id "$(rid int_begin)")
assert_success "integration: task begin" "$INT_BEGIN"
assert_jq_eq "integration: task in_progress after begin" "$INT_BEGIN" '.data.task.status' "in_progress"

# 4. Push events to task
vybe push \
  --json "{\"task_id\":\"$INT_TASK_ID\",\"event\":{\"kind\":\"task_started\",\"message\":\"Integration task started\"}}" \
  --agent e2e-test \
  --request-id "$(rid int_evt)" > /dev/null
vybe push \
  --json "{\"task_id\":\"$INT_TASK_ID\",\"event\":{\"kind\":\"progress\",\"message\":\"Integration step 1 complete\"}}" \
  --agent e2e-test \
  --request-id "$(rid int_evt)" > /dev/null

INT_EVENTS=$(vybe events list --task-id "$INT_TASK_ID")
assert_jq "integration: events recorded (>= 2)" "$INT_EVENTS" '.data.events | length >= 2'

# 5. Set task-scoped memory
vybe memory set \
  -k "int-task-context" \
  -v "integration context value" \
  --scope task \
  --scope-id "$INT_TASK_ID" \
  --request-id "$(rid int_mem)" > /dev/null

# 6. Set global memory
vybe memory set \
  -k "int-global-key" \
  -v "global value for integration" \
  --scope global \
  --request-id "$(rid int_mem)" > /dev/null

INT_GMEM=$(vybe memory get -k "int-global-key" --scope global)
assert_jq_eq "integration: global memory set and retrieved" \
  "$INT_GMEM" '.data.value' "global value for integration"

# 7. Push artifact to task
echo "integration artifact" > "$INT_ART_FILE"
vybe push \
  --json "{\"task_id\":\"$INT_TASK_ID\",\"artifacts\":[{\"file_path\":\"$INT_ART_FILE\"}]}" \
  --agent e2e-test \
  --request-id "$(rid int_art)" > /dev/null

# 8. Set focus on integration task then get brief via resume --peek
vybe resume \
  --agent e2e-test \
  --focus "$INT_TASK_ID" \
  --project "$INT_PROJECT_ID" \
  --request-id "$(rid int_focus)" > /dev/null

INT_BRIEF=$(vybe resume --peek --agent e2e-test)
assert_success "integration: resume --peek success" "$INT_BRIEF"
assert_jq "integration: brief has task" "$INT_BRIEF" '.data.brief.task != null'
assert_jq_arg "integration: brief task id matches" "$INT_BRIEF" \
  '.data.brief.task.id == $id' "id" "$INT_TASK_ID"
assert_jq "integration: brief has relevant_memory" "$INT_BRIEF" '.data.brief.relevant_memory != null'
assert_jq "integration: brief has recent_events" "$INT_BRIEF" '.data.brief.recent_events != null'
assert_jq "integration: brief has artifacts" "$INT_BRIEF" '.data.brief.artifacts != null'
assert_jq "integration: brief artifacts non-empty" "$INT_BRIEF" '.data.brief.artifacts | length > 0'

INT_RESUME=$(vybe resume --agent e2e-test --request-id "$(rid int_resume)")
assert_success "integration: resume success" "$INT_RESUME"
assert_jq "integration: resume brief has task" "$INT_RESUME" '.data.brief.task != null'

# 9. Complete the integration task
INT_DONE=$(vybe task complete \
  --id "$INT_TASK_ID" \
  --outcome done \
  --summary "Integration flow completed" \
  --agent e2e-test \
  --request-id "$(rid int_complete)")
assert_success "integration: task complete" "$INT_DONE"
assert_jq_eq "integration: task status is completed" "$INT_DONE" '.data.task.status' "completed"

# 10. Create second task; verify resume auto-advances to it
INT_TASK2=$(vybe task create \
  --title "Second Integration Task" \
  --project "$INT_PROJECT_ID" \
  --request-id "$(rid int_task)")
assert_success "integration: second task create" "$INT_TASK2"
INT_TASK2_ID=$(echo "$INT_TASK2" | jq -r '.data.task.id')

INT_RESUME2=$(vybe resume --agent e2e-test --request-id "$(rid int_resume)")
assert_success "integration: resume after complete" "$INT_RESUME2"
INT_RESUME2_TASK_ID=$(echo "$INT_RESUME2" | jq -r '.data.brief.task.id // empty')
assert_eq "integration: resume auto-advances to second task" \
  "$INT_RESUME2_TASK_ID" "$INT_TASK2_ID"

# ===========================================================================
# IDEMPOTENCY TESTS
# ===========================================================================

section "Idempotency: repeat create with same request-id"

IDEM_ID="e2e_idem_fixed_1"
IDEM1=$(vybe task create \
  --title "Idempotent Task" \
  --desc "First call" \
  --request-id "$IDEM_ID")
assert_success "idempotency: first create succeeds" "$IDEM1"
IDEM1_ID=$(echo "$IDEM1" | jq -r '.data.task.id')

IDEM2=$(vybe task create \
  --title "Idempotent Task Changed" \
  --desc "Second call same request-id" \
  --request-id "$IDEM_ID")
assert_success "idempotency: second create succeeds (replayed)" "$IDEM2"
IDEM2_ID=$(echo "$IDEM2" | jq -r '.data.task.id')

assert_eq "idempotency: same request-id returns same task id" "$IDEM1_ID" "$IDEM2_ID"
assert_jq_eq "idempotency: result title is original (not updated)" \
  "$IDEM2" '.data.task.title' "Idempotent Task"

# ===========================================================================
# EDGE CASES
# ===========================================================================

section "Edge Cases"

# Invalid scope — should return success=false
INVALID_SCOPE_OUT=$(VYBE_AGENT="e2e-test" "$VYBE" --db-path "$DB" memory set \
  -k "bad-key" -v "bad" --scope invalid_scope --request-id "$(rid edge)" 2>/dev/null)
INVALID_SCOPE_OK=$(echo "$INVALID_SCOPE_OUT" | jq -r '.success' 2>/dev/null)
if [[ "$INVALID_SCOPE_OK" == "false" || -z "$INVALID_SCOPE_OK" ]]; then
  pass "edge: invalid scope returns error"
else
  fail "edge: invalid scope returns error" "expected success=false, got success=$INVALID_SCOPE_OK"
fi

# Missing required --agent — should return success=false
EMPTY_AGENT_OUT=$(VYBE_AGENT="" "$VYBE" --db-path "$DB" task create \
  --title "no agent" --request-id "$(rid edge)" 2>/dev/null)
EMPTY_AGENT_OK=$(echo "$EMPTY_AGENT_OUT" | jq -r '.success' 2>/dev/null)
if [[ "$EMPTY_AGENT_OK" == "false" || -z "$EMPTY_AGENT_OK" ]]; then
  pass "edge: missing agent name returns error"
else
  fail "edge: missing agent name returns error" "expected success=false, got success=$EMPTY_AGENT_OK"
fi

# Task with priority
PRIO_TASK=$(vybe task create \
  --title "Priority Task" \
  --priority 10 \
  --request-id "$(rid edge_prio)")
assert_success "edge: task with priority: success" "$PRIO_TASK"
assert_jq "edge: task with priority: priority set to 10" "$PRIO_TASK" '.data.task.priority == 10'

# Task get for non-existent ID — should return success=false
BAD_GET=$(vybe task get --id "task_nonexistent_0000")
BAD_GET_OK=$(echo "$BAD_GET" | jq -r '.success')
if [[ "$BAD_GET_OK" == "false" ]]; then
  pass "edge: task get non-existent returns error"
else
  fail "edge: task get non-existent returns error" "expected success=false, got success=$BAD_GET_OK"
fi

# Task-scoped memory without scope-id — should return success=false
NO_SCOPE_ID_OUT=$(VYBE_AGENT="e2e-test" "$VYBE" --db-path "$DB" memory set \
  -k "bad" -v "bad" --scope task --request-id "$(rid edge)" 2>/dev/null)
NO_SCOPE_ID_OK=$(echo "$NO_SCOPE_ID_OUT" | jq -r '.success' 2>/dev/null)
if [[ "$NO_SCOPE_ID_OK" == "false" || -z "$NO_SCOPE_ID_OK" ]]; then
  pass "edge: task-scoped memory without scope-id returns error"
else
  fail "edge: task-scoped memory without scope-id returns error" "expected success=false, got success=$NO_SCOPE_ID_OK"
fi

# ===========================================================================
# TASK DEPENDENCY TESTS
# ===========================================================================

section "Task Dependencies"

DEP_A=$(vybe task create \
  --title "Dependency A" \
  --request-id "$(rid dep_a)")
DEP_A_ID=$(echo "$DEP_A" | jq -r '.data.task.id')

DEP_B=$(vybe task create \
  --title "Dependency B (depends on A)" \
  --request-id "$(rid dep_b)")
DEP_B_ID=$(echo "$DEP_B" | jq -r '.data.task.id')

ADD_DEP=$(vybe task add-dep \
  --id "$DEP_B_ID" \
  --depends-on "$DEP_A_ID" \
  --request-id "$(rid dep_add)")
assert_success "task add-dep: success" "$ADD_DEP"

REMOVE_DEP=$(vybe task remove-dep \
  --id "$DEP_B_ID" \
  --depends-on "$DEP_A_ID" \
  --request-id "$(rid dep_rem)")
assert_success "task remove-dep: success" "$REMOVE_DEP"

# ===========================================================================
# TASK SET-PRIORITY TEST
# ===========================================================================

section "task set-priority"

PRIO_T=$(vybe task create \
  --title "Priority Change Task" \
  --priority 0 \
  --request-id "$(rid prio_task)")
PRIO_T_ID=$(echo "$PRIO_T" | jq -r '.data.task.id')

PRIO_SET=$(vybe task set-priority \
  --id "$PRIO_T_ID" \
  --priority 5 \
  --request-id "$(rid prio_set)")
assert_success "task set-priority: success" "$PRIO_SET"
assert_jq "task set-priority: priority updated to 5" "$PRIO_SET" '.data.task.priority == 5'

# ===========================================================================
# FINAL REPORT
# ===========================================================================

echo ""
echo "========================================"
TOTAL=$((PASS + FAIL))
if [[ "$FAIL" -eq 0 ]]; then
  echo -e "${GREEN}ALL $TOTAL TESTS PASSED${RESET}"
else
  echo -e "${RED}$FAIL/$TOTAL TESTS FAILED${RESET}"
  echo ""
  echo "Failed tests:"
  for name in "${FAIL_NAMES[@]}"; do
    echo -e "  ${RED}x${RESET} $name"
  done
fi
echo "========================================"

[[ "$FAIL" -eq 0 ]]
