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

Crash-safe continuity for autonomous agents. Use when building agents that need
to resume work after interruption, track progress across sessions, coordinate
multiple workers, or persist memory/artifacts durably. Triggers: "resume task",
"agent crash", "checkpoint progress", "multi-agent", "work queue", "autonomous loop".

NOT for: single-shot tasks completing in one session, tasks without crash recovery,
non-autonomous workflows requiring human approval.

## Quick Reference

| Problem | Command | When |
|---------|---------|------|
| Resume after crash/restart | `vybe resume --request-id R` | Session start, after interruption |
| Create work items | `vybe task create --request-id R --title T --desc D` | Planning phase, decomposing work |
| Log progress | `vybe push --json '{"event":{"kind":"progress","message":"M"},"task_id":"T"}'` | Meaningful checkpoints |
| Save cross-session facts | `vybe memory set --request-id R --key K --value V --scope S --scope-id SI` | Discoveries that must survive restarts |
| Attach output files | `vybe push --json '{"artifacts":[{"file_path":"P"}],"task_id":"T"}'` | Generated files linked to tasks |
| Read-only context snapshot | `vybe resume --peek` | Inspect state without advancing cursor |
| Run autonomous work loop | `vybe loop --request-id R --max-iterations N` | Continuous agent execution |
| Create project context | `vybe resume --project-dir P` (auto-creates) | Scoping tasks and memory to project |
| Focus on project | `vybe resume --focus T --project-dir P` | Filtering brief to project scope |

**MUST (BLOCKING):**
- Every write command MUST include `--request-id` (enables idempotent retries)
- Agent MUST set `VYBE_AGENT` env var or `--agent` flag (stable identity)
- Resume MUST be called at session start before accessing focus task
- Task completion MUST include `--outcome` and `--summary` (audit trail)

## Install (BLOCKING)

```bash
# MUST install vybe before using patterns
go install github.com/dotcommander/vybe/cmd/vybe@latest

# MUST install hooks for automatic Claude Code integration
vybe hook install

# MUST set stable agent identity (add to ~/.bashrc or ~/.zshrc)
export VYBE_AGENT=claude

# Verify setup
vybe status  # MUST show "agent: claude" and "db: connected"
```

**If `vybe status` fails:** Check `~/.config/vybe/config.yaml` exists and `db_path` is writable.

## Core Concepts

### Identity and Idempotency

Every agent needs a stable name and every write needs a request ID.

```bash
# Agent name: stable across sessions
export VYBE_AGENT=claude

# Request ID: enables safe retries (same ID = same result)
vybe task create --request-id "plan_step1_$(date +%s)" \
  --title "Implement auth" --desc "Add JWT middleware"
```

### Resume Cycle (MUST Follow)

The fundamental pattern: resume -> work -> log -> complete -> resume.

```bash
# 1. Get context (advances cursor, returns focus task + memory + events)
BRIEF=$(vybe resume --request-id "resume_$(date +%s)")

# 2. Extract focus task (MUST use jq for JSON parsing)
TASK_ID=$(echo "$BRIEF" | jq -r '.data.focus_task_id // ""')

# 3. MUST check for null before proceeding
if [ -z "$TASK_ID" ]; then
  echo "No focus task available"
  exit 0
fi

# 4. Do work, log progress
vybe push --agent claude --request-id "push_$(date +%s)" --json \
  "{\"task_id\":\"$TASK_ID\",\"event\":{\"kind\":\"progress\",\"message\":\"Implemented JWT validation\"}}"

# 5. Complete task (next resume auto-advances to next task)
vybe task complete --request-id "done_$(date +%s)" \
  --id "$TASK_ID" --outcome done --summary "Auth middleware shipped"
```

**BLOCKING:** Always check `focus_task_id` for null. Resume returns empty focus when no work available.

### Memory Scopes

Memory persists key-value pairs at four scopes:

| Scope | Use | Example |
|-------|-----|---------|
| `global` | Cross-project facts | `db_path=/opt/data/main.db` |
| `project` | Project-specific config | `api_base=https://staging.example.com` |
| `task` | Task-local state | `last_processed_row=6000` |
| `agent` | Agent-private state | `preferred_model=sonnet` |

```bash
# Save a discovery
vybe memory set --request-id "mem_$(date +%s)" \
  --key api_base --value "https://staging.example.com" \
  --scope project --scope-id "$PROJECT_DIR"

# Read it back (any session)
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

## Claude Code Integration

### Automatic (hooks)

After `vybe hook install`, Claude Code automatically:

- **SessionStart**: Injects task context, memory, and recent events
- **UserPromptSubmit**: Logs prompts for cross-session continuity
- **PostToolUseFailure**: Records failed tool calls for recovery
- **TaskCompleted**: Marks tasks complete and logs lifecycle signals
- **PreCompact**: Runs garbage collection and best-effort retrospective
- **SessionEnd**: Runs garbage collection

### Proactive usage in CLAUDE.md

Add to your project's `CLAUDE.md` to teach Claude Code to use vybe:

```markdown
## Vybe Integration

When working on multi-step tasks, use vybe for durable state:

# Store discoveries that should persist across sessions
vybe memory set --agent=claude --key=<key> --value=<value> \
  --scope=task --scope-id=<task_id> --request-id=mem_$(date +%s)

# Log significant progress + link output files in one atomic call
vybe push --agent=claude --request-id=push_$(date +%s) --json '{
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
TASK_ID=$(vybe resume --peek | jq '.data.focus_task_id')
vybe task complete --id "$TASK_ID" --outcome done
```

**After (crash-safe):**
```bash
#!/usr/bin/env bash
set -euo pipefail
AGENT="${VYBE_AGENT:-worker-001}"

while true; do
  RESUME=$(vybe resume --agent "$AGENT" --request-id "resume_$(date +%s%N)")
  TASK_ID=$(echo "$RESUME" | jq -r '.data.focus_task_id // ""')

  if [ -z "$TASK_ID" ]; then
    echo "No work available"
    sleep 10
    continue
  fi

  # Do work...
  vybe push --agent "$AGENT" --request-id "push_$(date +%s%N)" --json \
    "{\"task_id\":\"$TASK_ID\",\"event\":{\"kind\":\"progress\",\"message\":\"Processing...\"}}"

  vybe task complete --agent "$AGENT" --request-id "done_$(date +%s%N)" \
    --id "$TASK_ID" --outcome done --summary "Processed"
done
```

**Key improvements:** idempotent request IDs, null checks, resume instead of brief, explicit outcomes.

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
AGENT="${VYBE_AGENT:-planner}"
TS=$(date +%s)

# Create parent task
PARENT=$(vybe task create --agent "$AGENT" --request-id "parent_$TS" \
  --title "Ship v2.0" --desc "Release milestone" | jq -r '.data.task.id')

# Create subtasks with dependencies
STEP1=$(vybe task create --agent "$AGENT" --request-id "step1_$TS" \
  --title "Write migration" --desc "Schema changes for v2" | jq -r '.data.task.id')

STEP2=$(vybe task create --agent "$AGENT" --request-id "step2_$TS" \
  --title "Update API handlers" --desc "New endpoints" | jq -r '.data.task.id')

# Step 2 depends on step 1
vybe task add-dep --agent "$AGENT" --request-id "dep_$TS" \
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
# Before expensive operation, record intent
vybe memory set --agent "$AGENT" --request-id "intent_$(date +%s)" \
  --key current_operation --value "migrating_table_users" \
  --scope task --scope-id "$TASK_ID"

# Do the work...

# After success, clear checkpoint
vybe memory set --agent "$AGENT" --request-id "clear_$(date +%s)" \
  --key current_operation --value "completed" \
  --scope task --scope-id "$TASK_ID"

# On resume, check checkpoint
CHECKPOINT=$(vybe memory get --key current_operation \
  --scope task --scope-id "$TASK_ID" | jq -r '.data.value // ""')

if [ "$CHECKPOINT" = "migrating_table_users" ]; then
  # Resume from checkpoint
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

if [ -n "$TASK_ID" ]; then
  vybe task begin --request-id "begin_$TS" --id "$TASK_ID"

  # Execute work...

  vybe push --request-id "push_$TS" --json \
    "{\"task_id\":\"$TASK_ID\",\"artifacts\":[{\"file_path\":\"./output/papers.json\"}]}"

  vybe task complete --request-id "done_$TS" --id "$TASK_ID" \
    --outcome done --summary "Found 47 papers, extracted 1200 citations"
fi
```

### Session-Spanning Code Refactor

```bash
# Session 1: Start refactor, record progress
export VYBE_AGENT=refactor-agent

TASK_ID=$(vybe task create --request-id "refactor_$(date +%s)" \
  --title "Extract payment logic" \
  --desc "Move payment code to separate module" | jq -r '.data.task.id')

vybe memory set --request-id "mem_$(date +%s)" \
  --key files_refactored --value "checkout.go,payment.go" \
  --scope task --scope-id "$TASK_ID"

# Session crashes...

# Session 2: Resume from checkpoint
RESUME=$(vybe resume --request-id "resume_$(date +%s)")
FILES=$(echo "$RESUME" | jq -r '.relevant_memory[] | select(.key=="files_refactored") | .value')
echo "Resuming refactor of: $FILES"
```

## Command Cheatsheet

```bash
# Task lifecycle
vybe task create --request-id R --title T --desc D
vybe task begin --request-id R --id ID
vybe task complete --request-id R --id ID --outcome done --summary S
vybe task list
vybe task get --id ID

# Push (atomic: event + memory + artifacts + status in one call)
vybe push --request-id R --json '{"task_id":"T","event":{"kind":"K","message":"M"},"artifacts":[{"file_path":"P"}]}'

# Events (read)
vybe events list --task-id T

# Memory
vybe memory set --request-id R --key K --value V --scope S --scope-id SI
vybe memory get --key K --scope S --scope-id SI
vybe memory list --scope S --scope-id SI

# Artifacts (read)
vybe artifacts list --task-id T

# Context
vybe resume --request-id R
vybe resume --peek
vybe status
```

## Anti-Patterns

| Anti-Pattern | Problem | Fix |
|-------------|---------|-----|
| Missing `--request-id` | Duplicate events/tasks on retry, no idempotency | `--request-id "push_$(date +%s%N)"` for every write |
| Volatile agent names | Cursor/state lost between sessions, no continuity | `export VYBE_AGENT=stable_name` at shell init |
| Storing large blobs in memory | Memory is KV store, not file storage; 16KB limit | Use `vybe push --json '{"artifacts":[...]}'` for files/outputs |
| Polling `vybe resume --peek` in tight loop | DB lock contention, no cursor advancement | Call `vybe resume` once per session start, cache brief |
| Skipping `vybe resume` | No focus task, no memory, cold start every session | MUST `vybe resume` before accessing `focus_task_id` |
| Hardcoded task IDs | Brittle, breaks on schema changes | Use `jq -r '.data.focus_task_id'` to extract from resume |
| Ignoring `focus_task_id == null` | Crash when no work available | Check `if [ -z "$TASK_ID" ]` before processing |
| Manual JSON parsing | Shell quoting errors, fragile | Use `jq` for all JSON extraction (BLOCKING) |
| Skipping `--outcome` on complete | Audit trail incomplete, no success/failure signal | MUST provide `--outcome done|failed|skipped` |
