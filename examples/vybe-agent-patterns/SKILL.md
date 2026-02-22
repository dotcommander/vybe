---
name: vybe-agent-patterns
description: |
  Crash-safe continuity for autonomous agents. Use when building agents that need
  to resume work after interruption, track progress across sessions, coordinate
  multiple workers, or persist memory/artifacts durably. Triggers: "resume task",
  "agent crash", "checkpoint progress", "multi-agent", "work queue", "autonomous loop".
  NOT for: single-shot tasks completing in one session, tasks without crash recovery,
  non-autonomous workflows requiring human approval.
---

# Vybe Agent Patterns

Patterns and commands for using vybe as the durable state layer in autonomous agent workflows.

## Quick Reference

| Problem | Command | When |
|---------|---------|------|
| Resume after crash/restart | `vybe resume --request-id R` | Session start, after interruption |
| Create work items | `vybe task create --request-id R --title T --desc D` | Planning phase, decomposing work |
| Log progress | `vybe push --request-id R --json '{"event":{"kind":"progress","message":"M"},"task_id":"T"}'` | Meaningful checkpoints |
| Save cross-session facts | `vybe memory set --request-id R --key K --value V --scope S --scope-id SI` | Discoveries that must survive restarts |
| Attach output files | `vybe push --request-id R --json '{"artifacts":[{"file_path":"P"}],"task_id":"T"}'` | Generated files linked to tasks |
| Read-only context snapshot | `vybe resume --peek` | Inspect state without advancing cursor |
| Run autonomous work loop | `vybe loop --max-tasks N --max-fails M` | Continuous agent execution |
| Create project context | `vybe resume --project-dir P` (auto-creates) | Scoping tasks and memory to project |
| Focus on project | `vybe resume --focus T --project-dir P` | Filtering brief to project scope |

**MUST (BLOCKING):**
- Every write command MUST include `--request-id` (enables idempotent retries)
- Agent MUST set `VYBE_AGENT` env var or `--agent` flag (stable identity)
- Resume MUST be called at session start before accessing focus task
- Task closure in autonomous loops MUST use `vybe task set-status --status completed|blocked`

## Install (BLOCKING)

```bash
# MUST install vybe before using patterns
go install github.com/dotcommander/vybe/cmd/vybe@latest

# MUST install hooks for automatic Claude Code integration
vybe hook install --claude

# MUST set stable agent identity (add to ~/.bashrc or ~/.zshrc)
export VYBE_AGENT=claude

# Verify setup
vybe status --agent claude | jq -e '.success and .data.db.ok' > /dev/null && echo "ok"
```

**If `vybe status` fails:** Check `~/.config/vybe/config.yaml` exists and `db_path` is writable.

**Config lookup order (first wins):** `~/.config/vybe/config.yaml` → `/etc/vybe/config.yaml` → `./config.yaml`

## Core Concepts

### Identity and Idempotency

Every agent needs a stable name and every write needs a request ID.

```bash
# Set agent identity once — used by all subsequent commands via env var
export VYBE_AGENT=claude

# Request ID: enables safe retries (same ID = same result)
vybe task create --request-id "plan_step1_$(date +%s)_$$" \
  --title "Implement auth" --desc "Add JWT middleware"
```

**Note:** `--agent` flag is the explicit alternative, but `VYBE_AGENT` env var is preferred — set once, no repetition.

### Resume Cycle (MUST Follow)

The fundamental pattern: resume -> work -> log -> set-status -> resume.

```bash
export VYBE_AGENT=claude

# 1. Get context (advances cursor, returns focus task + memory + events)
BRIEF=$(vybe resume --request-id "resume_$(date +%s)_$$")

# 2. MUST check success before proceeding
if [ "$(echo "$BRIEF" | jq -r '.success')" != "true" ]; then
  echo "Resume failed: $(echo "$BRIEF" | jq -r '.error')"
  exit 1
fi

# 3. Extract focus task ID (MUST use jq for JSON parsing)
TASK_ID=$(echo "$BRIEF" | jq -r '.data.focus_task_id // ""')

# 4. MUST check for null/empty before proceeding
if [ -z "$TASK_ID" ] || [ "$TASK_ID" = "null" ]; then
  echo "No focus task available"
  exit 0
fi

# 5. Do work, log progress
vybe push --request-id "push_$(date +%s)_$$" --json \
  "{\"task_id\":\"$TASK_ID\",\"event\":{\"kind\":\"progress\",\"message\":\"Implemented JWT validation\"}}"

# 6. Close task (canonical agent path: set terminal status)
vybe task set-status --request-id "done_$(date +%s)_$$" \
  --id "$TASK_ID" --status completed
```

**BLOCKING:** Always check `.data.focus_task_id` for null/empty. Resume returns empty focus when no work available.

**Response paths:** Use `data.focus_task_id` for the task ID string. Use `data.brief.task` for the full task object.

### Memory Scopes

Memory persists key-value pairs at four scopes:

| Scope | Use | Example |
|-------|-----|---------|
| `global` | Cross-project facts | `db_path=/opt/data/main.db` |
| `project` | Project-specific config | `api_base=https://staging.example.com` |
| `task` | Task-local state | `last_processed_row=6000` |
| `agent` | Agent-private state | `preferred_model=sonnet` |

```bash
export VYBE_AGENT=claude

# Save a discovery
vybe memory set --request-id "mem_$(date +%s)_$$" \
  --key api_base --value "https://staging.example.com" \
  --scope project --scope-id "$PROJECT_DIR"

# Read it back (any session, no --request-id needed for reads)
vybe memory get --key api_base --scope project --scope-id "$PROJECT_DIR"
```

## When to Use Vybe (BLOCKING Decision)

**MUST use vybe when:**
- Multi-step tasks span sessions or risk interruption
- Multiple agents work on the same project concurrently
- Progress must survive crashes, context resets, or session limits
- Task queues need deterministic focus selection
- Artifacts (generated files, reports) need linking to the task that produced them
- Cross-session memory is needed (facts, decisions, checkpoints)

**Skip vybe when:**
- Single-shot tasks that complete in one session
- No crash recovery needed
- No coordination between agents
- Ephemeral work with no continuity requirement

**If uncertain:** default to using vybe. Overhead is minimal; lost continuity is catastrophic.

## JSON Response Envelope (BLOCKING)

**MUST check `.success` field before using `.data`.**

```json
// Success
{"schema_version": "v1", "success": true, "data": {...}}

// Error
{"schema_version": "v1", "success": false, "error": "..."}
```

**Resume response structure:**
```json
{
  "schema_version": "v1",
  "success": true,
  "data": {
    "agent_name": "claude",
    "old_cursor": 42,
    "new_cursor": 58,
    "focus_task_id": "task_1234567890_a3f9",
    "focus_project_id": "project_1234567890_b2e8",
    "deltas": [...],
    "brief": {
      "task": {...},
      "project": {...},
      "relevant_memory": [...],
      "recent_events": [...],
      "artifacts": [...]
    }
  }
}
```

**Push response structure:**
```json
{
  "schema_version": "v1",
  "success": true,
  "data": {
    "event_id": 123,
    "memories": [{"key": "...", "canonical_key": "...", "reinforced": false}],
    "artifacts": [{"artifact_id": "artifact_...", "file_path": "..."}],
    "task_status": {"task_id": "task_...", "status": "completed"}
  }
}
```

## Claude Code Integration

### Automatic (hooks)

After `vybe hook install --claude`, Claude Code automatically:

- **SessionStart**: Injects task context, memory, and recent events
- **UserPromptSubmit**: Logs prompts for cross-session continuity
- **PostToolUseFailure**: Records failed tool calls for recovery
- **TaskCompleted**: Marks tasks complete and logs lifecycle signals
- **PreCompact**: Runs garbage collection and event maintenance
- **SessionEnd**: Runs garbage collection

### Proactive usage in CLAUDE.md

Add to your project's `CLAUDE.md` to teach Claude Code to use vybe:

```markdown
## Vybe Integration

When working on multi-step tasks, use vybe for durable state:

# Store discoveries that should persist across sessions
vybe memory set --key=<key> --value=<value> \
  --scope=task --scope-id=<task_id> --request-id=mem_$(date +%s)_$$

# Log significant progress + link output files in one atomic call
vybe push --request-id=push_$(date +%s)_$$ --json '{
  "task_id": "<task_id>",
  "event": {"kind": "progress", "message": "<what happened>"},
  "artifacts": [{"file_path": "<path>"}]
}'
```

## Patterns

### Worker Loop (Before/After)

**Before (fragile):**
```bash
#!/usr/bin/env bash
# Missing request IDs, no null checks, no error handling
TASK_ID=$(vybe resume --peek | jq '.data.brief.task.id')
vybe task set-status --id "$TASK_ID" --status completed
```

**After (crash-safe):**
```bash
#!/usr/bin/env bash
set -euo pipefail
export VYBE_AGENT="${VYBE_AGENT:-worker-001}"

while true; do
  RESUME=$(vybe resume --request-id "resume_$(date +%s)_$$")

  if [ "$(echo "$RESUME" | jq -r '.success')" != "true" ]; then
    echo "Resume failed: $(echo "$RESUME" | jq -r '.error')"
    exit 1
  fi

  TASK_ID=$(echo "$RESUME" | jq -r '.data.focus_task_id // ""')

  if [ -z "$TASK_ID" ] || [ "$TASK_ID" = "null" ]; then
    echo "No work available"
    sleep 10
    continue
  fi

  # Do work...
  vybe push --request-id "push_$(date +%s)_$$" --json \
    "{\"task_id\":\"$TASK_ID\",\"event\":{\"kind\":\"progress\",\"message\":\"Processing...\"}}"

  vybe task set-status --request-id "done_$(date +%s)_$$" \
    --id "$TASK_ID" --status completed
done
```

**Key improvements:** idempotent request IDs, success check, null checks, resume instead of brief, explicit terminal status.

### Task Decomposition (Before/After)

**Before (brittle):**
```bash
# No request IDs, hardcoded task IDs, no null checks
vybe task create --title "Step 1"
vybe task create --title "Step 2"
vybe task add-dep --id task_123 --depends-on task_456
```

**After (idempotent):**
```bash
export VYBE_AGENT="${VYBE_AGENT:-planner}"
TS=$(date +%s)

# Create parent task
PARENT=$(vybe task create --request-id "parent_$TS" \
  --title "Ship v2.0" --desc "Release milestone" | jq -r '.data.task.id')

# Create subtasks
STEP1=$(vybe task create --request-id "step1_$TS" \
  --title "Write migration" --desc "Schema changes for v2" | jq -r '.data.task.id')

STEP2=$(vybe task create --request-id "step2_$TS" \
  --title "Update API handlers" --desc "New endpoints" | jq -r '.data.task.id')

# Step 2 depends on step 1
vybe task add-dep --request-id "dep_$TS" \
  --id "$STEP2" --depends-on "$STEP1"
```

**Key improvements:** timestamp-based request IDs, `jq` extraction, dependency tracking.

### Crash-Safe Checkpoint (Before/After)

**Before (loses progress on crash):**
```bash
# In-memory progress counter, lost on restart
PROGRESS_COUNT=0
for item in "${ITEMS[@]}"; do
  process "$item"
  PROGRESS_COUNT=$((PROGRESS_COUNT + 1))
done
```

**After (resume from exact checkpoint):**
```bash
export VYBE_AGENT="${VYBE_AGENT:-worker}"

# Before expensive operation, record intent
vybe memory set --request-id "intent_$(date +%s)_$$" \
  --key current_operation --value "migrating_table_users" \
  --scope task --scope-id "$TASK_ID"

# Do the work...

# After success, clear checkpoint
vybe memory set --request-id "clear_$(date +%s)_$$" \
  --key current_operation --value "completed" \
  --scope task --scope-id "$TASK_ID"

# On resume, check checkpoint
CHECKPOINT=$(vybe memory get --key current_operation \
  --scope task --scope-id "$TASK_ID" | jq -r '.data.value // ""')

if [ "$CHECKPOINT" = "migrating_table_users" ]; then
  # Resume from checkpoint — operation was started but not completed
  echo "Resuming incomplete migration..."
fi
```

**Key improvements:** persistent checkpoint state, resume detection, idempotent operations.

## Examples

### Multi-Agent Research Pipeline

```bash
#!/usr/bin/env bash
# Coordinator agent creates tasks, worker agents claim and execute

# Coordinator: decompose research into tasks
export VYBE_AGENT=research-coordinator
TS=$(date +%s)

vybe task create --request-id "task1_$TS" \
  --title "Gather academic papers" \
  --desc "Search arxiv.org for relevant papers on topic X"

vybe task create --request-id "task2_$TS" \
  --title "Extract citations" \
  --desc "Parse PDFs and extract citation graphs"

vybe task create --request-id "task3_$TS" \
  --title "Synthesize findings" \
  --desc "Aggregate results into summary report"

# Worker agent: claim and execute
export VYBE_AGENT=research-worker-01

RESUME=$(vybe resume --request-id "resume_$TS")
TASK_ID=$(echo "$RESUME" | jq -r '.data.focus_task_id // ""')

if [ -n "$TASK_ID" ] && [ "$TASK_ID" != "null" ]; then
  vybe task begin --request-id "begin_$TS" --id "$TASK_ID"

  # Execute work...

  vybe push --request-id "push_$TS" --json \
    "{\"task_id\":\"$TASK_ID\",\"artifacts\":[{\"file_path\":\"./output/papers.json\"}]}"

  vybe task set-status --request-id "done_$TS" --id "$TASK_ID" \
    --status completed
fi
```

### Session-Spanning Code Refactor

```bash
# Session 1: Start refactor, record progress
export VYBE_AGENT=refactor-agent

TASK_ID=$(vybe task create --request-id "refactor_$(date +%s)_$$" \
  --title "Extract payment logic" \
  --desc "Move payment code to separate module" | jq -r '.data.task.id')

vybe memory set --request-id "mem_$(date +%s)_$$" \
  --key files_refactored --value "checkout.go,payment.go" \
  --scope task --scope-id "$TASK_ID"

# Session crashes or context resets...

# Session 2: Resume from checkpoint
RESUME=$(vybe resume --request-id "resume_$(date +%s)_$$")
TASK_ID=$(echo "$RESUME" | jq -r '.data.focus_task_id // ""')
FILES=$(echo "$RESUME" | jq -r '.data.brief.relevant_memory[] | select(.key=="files_refactored") | .value')
echo "Resuming refactor of: $FILES"
```

## Command Cheatsheet

```bash
# Set identity once
export VYBE_AGENT=claude

# Task lifecycle
vybe task create --request-id R --title T --desc D
vybe task begin  --request-id R --id ID
vybe task set-status --request-id R --id ID --status completed
vybe task list
vybe task get --id ID

# Push (atomic: event + memory + artifacts + status in one call)
vybe push --request-id R --json '{"task_id":"T","event":{"kind":"K","message":"M"},"artifacts":[{"file_path":"P"}]}'

# Events (read-only, no --request-id)
vybe events list --task-id T

# Memory
vybe memory set  --request-id R --key K --value V --scope S --scope-id SI
vybe memory get  --key K --scope S --scope-id SI
vybe memory list --scope S --scope-id SI

# Artifacts (read-only, no --request-id)
vybe artifacts list --task-id T

# Context
vybe resume --request-id R                  # advances cursor
vybe resume --peek                          # read-only, no cursor advance
vybe status                                 # agent state
vybe status --check                         # fast health gate (exit code)
```

## Anti-Patterns

| Anti-Pattern | Problem | Fix |
|-------------|---------|-----|
| Missing `--request-id` | Duplicate events/tasks on retry, no idempotency | `--request-id "push_$(date +%s)_$$"` for every write |
| Reused request-id | Second call returns cached first response | Generate a new unique ID per operation |
| Volatile agent names | Cursor/state lost between sessions, no continuity | `export VYBE_AGENT=stable_name` at shell init |
| Storing large blobs in memory | Memory is size-limited KV store, not file storage | Use `vybe push --json '{"artifacts":[...]}'` for files |
| Polling `resume --peek` in tight loop | DB lock contention, no cursor advancement | Call `vybe resume` once per session start, cache brief |
| Skipping `vybe resume` | No focus task, no memory, cold start every session | MUST `vybe resume` before accessing focus task |
| Hardcoded task IDs | Brittle, breaks on task recreation | Use `jq -r '.data.focus_task_id'` to extract from resume |
| Ignoring `focus_task_id == null` | Crash when no work available | Check `if [ -z "$TASK_ID" ]` before processing |
| Manual JSON parsing | Shell quoting errors, fragile | Use `jq` for all JSON extraction (BLOCKING) |
| Skipping terminal status update | Loop cannot classify task outcome | MUST run `task set-status --status completed\|blocked` once per focus task |
| Unchecked `.success` field | Silent failures, wrong data consumed | Always check `jq -r '.success'` before using `.data` |
| Global memory for task state | Data leaks across tasks | Use `--scope=task --scope-id=$TASK_ID` |
| `task start` instead of `task begin` | Command not found | Use `vybe task begin` |
| `project create` + `project focus` | Extra round-trips | Use `resume --project-dir <dir>` — auto-creates |
| `brief` command | Command not found | Use `resume --peek` |
| `artifact list` (no s) | Command not found | Use `artifacts list` (with s) |
| `date +%s%N` on macOS | Fails silently, no nanoseconds | Use `$(date +%s)_$$` |

## Common Errors

| Error | Fix |
|-------|-----|
| `agent is required` | Set `VYBE_AGENT` env var (preferred) or `--agent` flag |
| `request-id is required` | Set unique `--request-id` (not needed for `--peek` or read-only ops) |
| `task not found` | Verify with `vybe task list` |
| `database is locked` | Auto-retry (5s timeout built-in) |
| `idempotency replay` | Use a fresh unique request-id per operation |
| `scope_id is required` | Task/project/agent scopes require `--scope-id` |
