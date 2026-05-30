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
| Resume after crash/restart | `vybe resume` | Session start, after interruption |
| See current focus (read-only) | `vybe focus` | Quick "what am I on?" without advancing cursor |
| Create work items | `vybe task create --title T --desc D` | Planning phase, decomposing work |
| Log progress | `vybe note <id> "M"` | Meaningful checkpoints |
| Complete a task | `vybe done <id> [--note "M"]` | Work finished — one atomic call |
| Block a task | `vybe block <id> --reason "..." [--failure]` | Stuck (`--failure` makes resume skip it) |
| Save cross-session facts | `vybe remember "K=V" --scope S --scope-id SI` | Discoveries that must survive restarts (`--scope-id` optional for `task`/`project` when a focus is set) |
| Atomic multi-op batch | `vybe push --json '{"task_id":"T","event":{...},"memories":[...],"artifacts":[...]}'` | Several writes that must land together |
| Read-only context snapshot | `vybe resume --peek` | Inspect full brief without advancing cursor |
| Run autonomous work loop | `vybe loop --max-tasks N --max-fails M` | Continuous agent execution |
| Create project context | `vybe resume --project-dir P` (auto-creates) | Scoping tasks and memory to project |
| Focus on project | `vybe resume --focus T --project-dir P` | Filtering brief to project scope |

**MUST (BLOCKING):**
- Agent MUST have a stable identity: set `default_agent` in `~/.config/vybe/config.yaml`, `VYBE_AGENT` env var, or `--agent` flag
- Resume MUST be called at session start before accessing focus task
- Task closure in autonomous loops MUST use `vybe done <id>` (or `vybe block <id> --reason ...`)
- `--request-id` is OPTIONAL and omitted by default. A freshly-generated per-call id is identical to omitting it (vybe auto-generates one either way — both give at-least-once). Pass a STABLE `--request-id` ONLY to retry the exact same operation (or group a known set of ops). Never generate a fresh id per call.

## Install (BLOCKING)

```bash
# MUST install vybe before using patterns
go install github.com/dotcommander/vybe/cmd/vybe@latest

# MUST install hooks for automatic Claude Code integration
vybe hook install --claude

# MUST set stable agent identity. Easiest: set it once in config so every
# command can omit --agent:
#   echo 'default_agent: claude' >> ~/.config/vybe/config.yaml
# Or export an env var (add to ~/.bashrc or ~/.zshrc):
export VYBE_AGENT=claude

# Verify setup
vybe status | jq -e '.success and .data.db.ok' > /dev/null && echo "ok"
```

**If `vybe status` fails:** Check `~/.config/vybe/config.yaml` exists and `db_path` is writable.

**Config lookup order (first wins):** `~/.config/vybe/config.yaml` → `/etc/vybe/config.yaml` → `./config.yaml`

## Core Concepts

### Identity and Idempotency

Every agent needs a stable name. Set it once and forget it.

```bash
# Set agent identity once — used by all subsequent commands. Either:
#   ~/.config/vybe/config.yaml:  default_agent: claude
# or:
export VYBE_AGENT=claude

# With identity set, writes need no --request-id at all:
vybe task create --title "Implement auth" --desc "Add JWT middleware"
```

**Identity resolution order:** `--agent` flag → `VYBE_AGENT` env var → `config.yaml: default_agent`. Setting `default_agent` (or `VYBE_AGENT`) once lets you drop `--agent` from every command.

**Request IDs (idempotency):** `--request-id` is **optional and omitted by default**. vybe auto-generates a unique id for every mutation, so a freshly-generated per-call id (`done_$(date +%s)`, `$RANDOM`, etc.) is behaviorally **identical to omitting it** — both give at-least-once execution, and dedup only fires when the SAME `(agent, request-id, command)` recurs. Pass a STABLE `--request-id` ONLY when you intend to retry the exact same operation and want the original result replayed. Do not invent a new id on each call.

### Resume Cycle (MUST Follow)

The fundamental pattern: resume -> work -> note -> done -> resume.

```bash
export VYBE_AGENT=claude   # or set default_agent in config.yaml

# 1. Get context (advances cursor, returns focus task + memory + events)
BRIEF=$(vybe resume)

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
vybe note "$TASK_ID" "Implemented JWT validation"

# 6. Close task in one atomic call (with an optional final note)
vybe done "$TASK_ID" --note "JWT middleware shipped and tested"
```

**BLOCKING:** Always check `.data.focus_task_id` for null/empty. Resume returns empty focus when no work available.

**Read-only peek at the current focus:** `vybe focus` prints the focus task without advancing the cursor — handy for a quick "what am I on?" check between steps.

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
vybe remember "api_base=https://staging.example.com" \
  --scope project --scope-id "$PROJECT_DIR"

# Read it back (any session)
vybe memory get --key api_base --scope project --scope-id "$PROJECT_DIR"
```

`vybe remember "k=v"` takes the same `--scope`/`--scope-id`/`--kind`/`--pin` flags as `vybe memory set --key k --value v ...` — it's just the terse form. Use `--kind directive` for behavioral rules, `--pin` for durable strategic memory.

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
vybe remember "<key>=<value>" --scope task --scope-id <task_id>

# Log a progress checkpoint
vybe note <task_id> "<what happened>"

# Mark a task done (optionally with a closing note)
vybe done <task_id> --note "<summary>"

# When several writes must land together, use push (one atomic call)
vybe push --json '{
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
  RESUME=$(vybe resume)

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
  vybe note "$TASK_ID" "Processing..."

  vybe done "$TASK_ID"
done
```

**Key improvements:** stable identity, success check, null checks, resume instead of brief, explicit terminal status via `vybe done`. No per-call request-id noise — vybe auto-generates ids, and each loop pass is a genuinely new operation.

### Task Decomposition (Before/After)

**Before (brittle):**
```bash
# No request IDs, hardcoded task IDs, no null checks
vybe task create --title "Step 1"
vybe task create --title "Step 2"
```

**After (clean):**
```bash
export VYBE_AGENT="${VYBE_AGENT:-planner}"

# Create parent task — no --request-id needed
PARENT=$(vybe task create \
  --title "Ship v2.0" --desc "Release milestone" | jq -r '.data.task.id')

# Create subtasks
STEP1=$(vybe task create \
  --title "Write migration" --desc "Schema changes for v2" | jq -r '.data.task.id')

STEP2=$(vybe task create \
  --title "Update API handlers" --desc "New endpoints" | jq -r '.data.task.id')
```

**Key improvements:** stable identity, `jq` extraction, no request-id noise.

**Stable request-ids for safe batch retry (optional):** if this script may be re-run after a partial failure and you want each `task create` to be idempotent (no duplicate tasks on the second run), give each a *fixed, content-derived* id that is identical across runs — not a fresh timestamp:

```bash
# Re-running this exact block replays the original tasks instead of duplicating them.
vybe task create --request-id "v2_migration"   --title "Write migration"     --desc "Schema changes for v2"
vybe task create --request-id "v2_api_handlers" --title "Update API handlers" --desc "New endpoints"
```

The point is the id stays the SAME on retry. A `date +%s`-based id changes every run and would dedupe nothing — that's the cargo-cult to avoid.

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
vybe remember "current_operation=migrating_table_users" \
  --scope task --scope-id "$TASK_ID"

# Do the work...

# After success, clear checkpoint
vybe remember "current_operation=completed" \
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

vybe task create \
  --title "Gather academic papers" \
  --desc "Search arxiv.org for relevant papers on topic X"

vybe task create \
  --title "Extract citations" \
  --desc "Parse PDFs and extract citation graphs"

vybe task create \
  --title "Synthesize findings" \
  --desc "Aggregate results into summary report"

# Worker agent: claim and execute
export VYBE_AGENT=research-worker-01

RESUME=$(vybe resume)
TASK_ID=$(echo "$RESUME" | jq -r '.data.focus_task_id // ""')

if [ -n "$TASK_ID" ] && [ "$TASK_ID" != "null" ]; then
  vybe task begin --id "$TASK_ID"

  # Execute work...

  # push when the artifact link must land with the event atomically
  vybe push --json \
    "{\"task_id\":\"$TASK_ID\",\"artifacts\":[{\"file_path\":\"./output/papers.json\"}]}"

  vybe done "$TASK_ID"
fi
```

### Session-Spanning Code Refactor

```bash
# Session 1: Start refactor, record progress
export VYBE_AGENT=refactor-agent

TASK_ID=$(vybe task create \
  --title "Extract payment logic" \
  --desc "Move payment code to separate module" | jq -r '.data.task.id')

vybe remember "files_refactored=checkout.go,payment.go" \
  --scope task --scope-id "$TASK_ID"

# Session crashes or context resets...

# Session 2: Resume from checkpoint
RESUME=$(vybe resume)
TASK_ID=$(echo "$RESUME" | jq -r '.data.focus_task_id // ""')
FILES=$(echo "$RESUME" | jq -r '.data.brief.relevant_memory[] | select(.key=="files_refactored") | .value')
echo "Resuming refactor of: $FILES"
```

## Command Cheatsheet

```bash
# Set identity once (config.yaml: default_agent, or env var)
export VYBE_AGENT=claude

# Task lifecycle (sugar verbs — no --request-id needed)
vybe task create --title T --desc D
vybe task begin  --id ID
vybe note  ID "progress message"            # record a progress event
vybe done  ID [--note "closing summary"]    # complete (atomic)
vybe block ID --reason "..." [--failure]    # block; --failure → resume skips it
vybe task list
vybe task get --id ID
# Verbose equivalent of done/block: vybe task set-status --id ID --status completed|blocked

# Memory (--scope-id optional for task/project when a focus is set; required for agent scope)
vybe remember "K=V" --scope S --scope-id SI  # terse memory write (--kind, --pin also accepted)
vybe memory get  --key K --scope S --scope-id SI
vybe memory list --scope S --scope-id SI

# Push (atomic multi-op: event + memory + artifacts + status together). Use for
# genuine batches, or for reasoning/metadata events (e.g. kind=THINK).
vybe push --json '{"task_id":"T","event":{"kind":"K","message":"M"},"artifacts":[{"file_path":"P"}]}'

# Events / artifacts (read-only)
vybe events    --task-id T
vybe artifacts --task-id T

# Context
vybe focus                                  # print current focus, read-only
vybe resume                                 # advances cursor (full brief)
vybe resume --peek                          # read-only full brief, no cursor advance
vybe status                                 # agent state
vybe status --check                         # fast health gate (exit code)
```

## Anti-Patterns

| Anti-Pattern | Problem | Fix |
|-------------|---------|-----|
| Generating a fresh `--request-id` for every call (`done_$(date +%s)`, `$RANDOM`, `$$`) | Behaviorally identical to omitting it and never dedupes — just noise | Omit `--request-id`. Only reuse a STABLE id to retry the exact same operation |
| Reusing one stable request-id across *different* operations | Second op returns the first op's cached response | Distinct stable ID per logical operation, or just omit it |
| Volatile agent names | Cursor/state lost between sessions, no continuity | Set `default_agent` in config or `export VYBE_AGENT=stable_name` |
| Storing large blobs in memory | Memory is size-limited KV store, not file storage | Use `vybe push --json '{"artifacts":[...]}'` for files |
| Polling `resume --peek` in tight loop | DB lock contention, no cursor advancement | Use `vybe focus` for a cheap read, or call `vybe resume` once per session and cache the brief |
| Skipping `vybe resume` | No focus task, no memory, cold start every session | MUST `vybe resume` before accessing focus task |
| Hardcoded task IDs | Brittle, breaks on task recreation | Use `jq -r '.data.focus_task_id'` to extract from resume |
| Ignoring `focus_task_id == null` | Crash when no work available | Check `if [ -z "$TASK_ID" ]` before processing |
| Manual JSON parsing | Shell quoting errors, fragile | Use `jq` for all JSON extraction (BLOCKING) |
| Skipping terminal status update | Loop cannot classify task outcome | MUST run `vybe done <id>` (or `vybe block <id> --reason ...`) once per focus task |
| Wrapping a single progress log or close in `vybe push --json` | Verbose, error-prone quoting | Use `vybe note <id> "msg"` / `vybe done <id>`; reserve `push` for atomic multi-op batches |
| Unchecked `.success` field | Silent failures, wrong data consumed | Always check `jq -r '.success'` before using `.data` |
| Global memory for task state | Data leaks across tasks | Use `--scope task --scope-id $TASK_ID` |
| `task start` instead of `task begin` | Command not found | Use `vybe task begin` |
| `project create` + `project focus` | Extra round-trips | Use `resume --project-dir <dir>` — auto-creates |
| `brief` command | Command not found | Use `resume --peek` (or `vybe focus`) |
| `artifact list` (no s) | Command not found | Use `artifacts` (with s, no subcommand) |

## Common Errors

| Error | Fix |
|-------|-----|
| `agent is required` | Set `config.yaml: default_agent` (preferred) or `VYBE_AGENT` env var or `--agent` flag |
| `task not found` | Verify with `vybe task list` |
| `database is locked` | Auto-retry (5s timeout built-in) |
| `idempotency replay` | A stable request-id you reused replayed the original result — expected on retry. For a genuinely new operation, omit `--request-id` (or use a distinct stable id) |
| `scope_id is required` | `agent` scope always requires `--scope-id`; `task`/`project` scopes infer it from focus when set, else pass `--scope-id` |
