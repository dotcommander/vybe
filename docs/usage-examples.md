# Vybe Usage Examples

This reference document catalogs 50 distinct, production-ready use cases for the `vybe` CLI. These examples cover task management, memory persistence, resume flows, multi-agent coordination, pipeline dependencies, and automation hooks.

---

## Category 1: Task Lifecycle & Queue Management

### 1. Creating a basic task queue item
Creates a new task in the queue.
```bash
vybe task create --title "Refactor logger" --desc "Change log format to structured JSON"
```
**Expected Output:**
```json
{
  "schema_version": "v1",
  "success": true,
  "data": {
    "task": {
      "id": "task_1716960000_2b3f",
      "title": "Refactor logger",
      "description": "Change log format to structured JSON",
      "status": "pending",
      "priority": 0,
      "version": 1,
      "created_at": "2026-05-30T03:38:36Z",
      "updated_at": "2026-05-30T03:38:36Z"
    },
    "event_id": 177579
  }
}
```

### 2. Beginning execution of a task
Claims the task and sets its status to `in_progress`.
```bash
vybe task begin --id "task_1716960000_2b3f"
```
**Expected Output:**
```json
{
  "schema_version": "v1",
  "success": true,
  "data": {
    "task": {
      "id": "task_1716960000_2b3f",
      "title": "Refactor logger",
      "description": "Change log format to structured JSON",
      "status": "in_progress",
      "priority": 0,
      "version": 2,
      "created_at": "2026-05-30T03:38:36Z",
      "updated_at": "2026-05-30T03:38:37Z"
    },
    "status_event_id": 177580,
    "focus_event_id": 177581
  }
}
```

### 3. Marking a task completed with a success summary
Uses the sugar verb to complete the active task and logs a summary event.
```bash
vybe done "task_1716960000_2b3f" --note "Structured JSON logs wired to stderr"
```
**Expected Output:**
```json
{
  "schema_version": "v1",
  "success": true,
  "data": {
    "event_id": 177584,
    "task_status": {
      "task_id": "task_1716960000_2b3f",
      "status": "completed",
      "status_event_id": 177585,
      "close_event_id": 177586
    }
  }
}
```

### 4. Blocking a task with a clear block reason
Transitions the task status to `blocked` without designating execution failure.
```bash
vybe block "task_1716960000_2b3f" --reason "Waiting on team lead to approve configuration schema"
```
**Expected Output:**
```json
{
  "schema_version": "v1",
  "success": true,
  "data": {
    "task": {
      "id": "task_1716960000_2b3f",
      "title": "Refactor logger",
      "description": "Change log format to structured JSON",
      "status": "blocked",
      "priority": 0,
      "blocked_reason": "Waiting on team lead to approve configuration schema",
      "version": 3,
      "created_at": "2026-05-30T03:38:36Z",
      "updated_at": "2026-05-30T03:38:37Z"
    },
    "event_id": 177582
  }
}
```

### 5. Marking a task as blocked due to failure
Designates a failure block so the deterministic focus selection algorithm will automatically skip it.
```bash
vybe block "task_1716960000_2b3f" --reason "Build compiler error on dependency x" --failure
```
**Expected Output:**
```json
{
  "schema_version": "v1",
  "success": true,
  "data": {
    "task": {
      "id": "task_1716960000_2b3f",
      "title": "Refactor logger",
      "description": "Change log format to structured JSON",
      "status": "blocked",
      "priority": 0,
      "blocked_reason": "failure:Build compiler error on dependency x",
      "version": 3,
      "created_at": "2026-05-30T03:38:36Z",
      "updated_at": "2026-05-30T03:38:37Z"
    },
    "event_id": 177582
  }
}
```

### 6. Creating a task with explicit high priority
Ensures this task is evaluated first in the pipeline when no active focus is set.
```bash
vybe task create --title "Patch security CVE" --priority 100 --desc "Urgent dependency update"
```

### 7. Querying all tasks in a project
Lists the tasks associated with a specific project directory.
```bash
vybe task list --project-id "proj_core_engine"
```

### 8. Checking status of a specific task
Fetches task metadata by its ID.
```bash
vybe task get --id "task_1716960000_2b3f"
```

### 9. Listing only blocked tasks to troubleshoot
Filters the task list by status.
```bash
vybe task list --status "blocked"
```

### 10. Deleting a task
Permanently removes a task record.
```bash
vybe task delete --id "task_1716960000_2b3f"
```

---

## Category 2: Context Resume & Continuity

### 11. Resuming agent session inside a specific project directory
Sets/updates active workspace context and returns the compiled prompt brief.
```bash
vybe resume --project-dir "/Users/vampire/go/src/vybe"
```

### 12. Peeking at the current task brief without advancing the cursor
Reads context details without consuming/advancing the agent's event cursor.
```bash
vybe resume --peek
```

### 13. Resuming with a cursor limit to prevent budget overflow
Restricts the number of event deltas fetched in this resume batch.
```bash
vybe resume --limit 50
```

### 14. Overriding the focus task atomically during a resume
Changes active focus task directly through the resume operation.
```bash
vybe resume --focus "task_1716960000_2b3f"
```

### 15. Restoring context after a terminal or agent crash
Re-runs `resume` to re-fetch the latest brief, ensuring no state is lost.
```bash
vybe resume
```

### 16. Transferring cursor state to a new agent replica
Initializes replica alignment by running resume under the same agent identifier.
```bash
vybe resume --agent "worker-replica-01"
```

### 17. Fetching recent user prompts for context reconstruction
Extracts recent conversational prompts logged to the database.
```bash
vybe events --kind "user_prompt" --limit 5
```

### 18. Viewing prior reasoning events from earlier sessions
Retrieves past reasoning/thinking checkpoints logged by the agent.
```bash
vybe events --kind "reasoning" --limit 10
```

---

## Category 3: Scoped Memory (KV Storage)

### 19. Storing a global memory
Stores variables visible across all projects.
```bash
vybe remember "preferred_compiler=go1.22" --scope global
```

### 20. Storing project-scoped memory
Scopes configuration variables to a specific project.
```bash
vybe remember "build_cmd=go build ./cmd/vybe" --scope project --scope-id "proj_core"
```

### 21. Storing task-scoped memory
Saves transient parameters for a specific task.
```bash
vybe remember "max_refactor_loops=3" --scope task --scope-id "task_1716960000_2b3f"
```

### 22. Storing agent-scoped memory
Scopes metrics or attributes to a specific agent identity.
```bash
vybe remember "preferred_model=gemini-2.5-pro" --scope agent --scope-id "worker-001"
```

### 23. Setting memory kind to "directive" for strict brief rules
Creates a behavioral directive that renders under `=== Directives ===` at the top of the brief.
```bash
vybe remember "no_mock_db=Never mock SQLite in integration tests" --kind directive --scope global
```

### 24. Setting memory kind to "lesson" for short-term tactical feedback
Stores retrospective notes with a fast-decaying default half-life (14 days).
```bash
vybe remember "use_wal_locks=WAL mode requires IMMEDIATE transaction lock" --kind lesson --scope project
```

### 25. Fetching a specific memory key
Retrieves a single memory entry value.
```bash
vybe memory get --key "build_cmd" --scope project --scope-id "proj_core"
```

### 26. Listing all memories in a given scope
Lists current memories matching the scope parameters.
```bash
vybe memory list --scope project --scope-id "proj_core"
```

### 27. Deleting a memory key from project scope
Erases a key-value entry.
```bash
vybe memory delete --key "max_refactor_loops" --scope task --scope-id "task_1716960000_2b3f"
```

### 28. Pinning a strategic decision
Applies a pin to force a memory entry to stay in the brief regardless of time-decay.
```bash
vybe memory pin --key "no_mock_db" --scope global
```

---

## Category 4: Memory Expiration, Decay, and Garbage Collection

### 29. Setting a memory with a hard TTL expiration
Configures a key-value entry that expires automatically after 1 hour.
```bash
vybe memory set --key "temp_token" --value "abc123_temp" --ttl 1h
```

### 30. Overriding decay behavior by setting custom half-life days
Sets custom mathematical relevance decay parameters (e.g. 5-day half-life).
```bash
vybe memory set --key "api_docs_cached" --value "https://..." --half-life-days 5
```

### 31. Running manual memory garbage collection
Forces deletion of expired memory entries from the database.
```bash
vybe memory gc
```

### 32. Tracking memory access metrics
Checks retrieval diagnostics by querying memory details.
```bash
vybe memory get --key "preferred_compiler" --scope global
```
*(Querying increments `access_count` and updates `last_accessed_at` internally).*

### 33. Pinning a memory key to protect it from incidental overwrites
Ensures that subsequent `memory set` commands without `--pin` cannot unpin it.
```bash
vybe remember "important_invariant=Use atomic operations" --pin
```

### 34. Unpinning a memory key when rules change
Removes the pin explicitly.
```bash
vybe memory pin --key "important_invariant" --unpin
```

---

## Category 5: Multi-Agent Coordination

### 35. Acquiring focus claim atomically across multiple concurrent agents
Claims a task. If another agent claimed it concurrently, the `version` CAS check will reject the write.
```bash
vybe task begin --id "task_1716960000_2b3f"
```

### 36. Detecting memory conflicts between concurrent workers
Logs progress and inspects output.
```bash
vybe remember "schema_ver=v2"
```
*(If a conflicting value is already written, vybe appends a `memory_conflict` system event details to the log).*

### 37. Listing active agents within the last 7 days
Queries system status to retrieve agent activity telemetry.
```bash
vybe status
```

### 38. Claiming a task using CAS optimistic locking
Performs status updates while enforcing record version isolation.
```bash
vybe task set-status --id "task_1716960000_2b3f" --status "in_progress" --version 2
```

### 39. Auto-rebasing cursor when multiple agents write concurrently
Resolves conflicting cursors automatically during resumption.
```bash
vybe resume
```
*(Cursor advances to the highest event ID, resolving concurrent log updates).*

---

## Category 6: Task Dependencies & Pipelines

### 40. Modeling task blocking task dependencies
Creates a task that blocks another task.
```bash
# Block task B on task A
vybe block "task_B" --reason "dependency"
```

### 41. Resolving block state once blocker task completes
Transitions a blocked task back to pending.
```bash
vybe task set-status --id "task_B" --status "pending"
```

### 42. Visualizing task pipeline queue ordering
Views future tasks ordered by priority and creation time in the brief.
```bash
vybe resume --peek
```
*(Parsed from the `.data.brief.pipeline` array).*

---

## Category 7: Idempotency & Retry Engineering

### 43. Retrying task creation with a stable request ID
Retries a failed API/CLI call safely. The request ID is unique to the attempt sequence.
```bash
vybe task create --title "Build index" --request-id "worker_task_build_index_101"
```

### 44. Replaying a session resume with the same request ID
Retrieves the exact cached resume response from the first attempt.
```bash
vybe resume --request-id "worker_resume_1716960000"
```

### 45. Executing batch push mutations idempotently
Pushes multiple updates in a single, safe transaction.
```bash
vybe push --request-id "worker_push_1716960000" --json '{"task_id":"task_123","event":{"kind":"progress","message":"Completed setup"}}'
```

---

## Category 8: Event Log & Audit Trails

### 46. Streaming live event log entries
Fetches recent events in raw output format.
```bash
vybe events --limit 20
```

### 47. Logging a progress milestone after completing a phase
Records a structured progress event so future resumes surface the milestone.
```bash
vybe push --json '{
  "task_id": "task_123",
  "event": {
    "kind": "progress",
    "message": "Setup phase completed successfully — proceeding to implementation"
  }
}'
```

### 48. Extracting task artifacts list for a closed task
Retrieves all output paths registered to a task.
```bash
vybe artifacts --task-id "task_123"
```

---

## Category 9: Hooks & Shell Integrations

### 49. Setting up Claude Code startup hook to load resume context
Integrates hooks into Claude Code configuration.
```bash
vybe hook install
```

### 50. Injecting Git commit details as vybe progress events
Logs code check-ins directly to the event stream.
```bash
vybe push --json "{\"task_id\":\"task_123\",\"event\":{\"kind\":\"progress\",\"message\":\"Git commit: $(git log -1 --oneline)\"}}"
```
