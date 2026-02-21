# Agent Contract

Purpose: canonical machine-facing contract for assistants, plugins, and autonomous workers.

## Fast checklist

1. Set stable `--agent` identity.
2. Use `--request-id` on continuity mutations.
3. Parse `stdout` JSON envelope only.
4. Parse `stderr` logs as diagnostics only.
5. Discover command/flag schemas via `vybe schema commands`.

## Core invariants

### Identity

- Use `--agent` or `VYBE_AGENT` on all agent-scoped calls.
- Recommended format: `<assistant>-<workspace-or-session-prefix>`.

### Idempotency

- Continuity mutations (`resume` without `--peek`, `push`, `task *`, `memory set|delete|gc`) must include `--request-id`.
- On retry, reuse the same `--request-id`.
- Never mint a new request ID while replaying the same logical write.

### Machine I/O

- Success envelope (stdout): `{ "schema_version": "v1", "success": true, "data": ... }`
- Error envelope (stdout): `{ "schema_version": "v1", "success": false, "error": ... }`
- Structured logs go to `stderr`.
- Do not parse help prose as protocol data.

### Command discovery

- `vybe` (no args) returns JSON command index.
- `vybe schema commands` returns argument schema + mutation hints.
- Prefer schema-driven calls over hardcoded flags.

## Canonical command surface

Top-level commands:

- `artifacts`
- `events`
- `help`
- `hook`
- `loop`
- `memory`
- `push`
- `resume`
- `schema`
- `status`
- `task`
- `upgrade`

Primary subcommands:

- `artifacts list`
- `events list`
- `hook install|uninstall`
- `loop stats`
- `memory set|get|list|delete|gc`
- `schema commands`
- `task create|begin|get|list|set-status|set-priority|complete|add-dep|remove-dep|delete`

## Canonical flag semantics

- `--project-dir`: workspace directory scope (`resume`, `loop`).
- `--project-id`: task/project entity association/filter (`task create`, `task list`, `loop stats`).
- `--task-id`: artifacts/events read filters.

## Required mappings

### Session start

```bash
vybe resume --agent "$AGENT" --request-id "$REQ" --project-dir "$WORKSPACE"
```

Inject `.data.prompt` (or `.data.brief`) into assistant context.

### Task sync

- create: `vybe task create ...`
- claim/start: `vybe task begin ...` or `vybe resume ...` (deterministic focus)
- complete/blocked: `vybe task complete --outcome done|blocked --summary "..."`
- low-level transition: `vybe task set-status ...`
- task read: `vybe task get --id ...`
- queue read: `vybe task list --project-id ...`

### Progress log

```bash
vybe push --agent "$AGENT" --request-id "$REQ" \
  --json "{\"task_id\":\"$TASK_ID\",\"event\":{\"kind\":\"progress\",\"message\":\"...\"}}"
```

### Durable memory

```bash
vybe memory set --agent "$AGENT" --request-id "$REQ" \
  --key ... --value ... --scope task --scope-id "$TASK_ID"
```

### Artifacts

```bash
vybe push --agent "$AGENT" --request-id "$REQ" \
  --json "{\"task_id\":\"$TASK_ID\",\"artifacts\":[{\"file_path\":\"<file>\"}]}"
```

### Event and artifact reads

```bash
vybe events list --agent "$AGENT" --task-id "$TASK_ID" --limit 50
vybe artifacts list --task-id "$TASK_ID" --limit 50
```

## Retry contract

- Transport/tool failure: retry same command with same `--request-id`.
- `success: false`: inspect `.error`; retry only if operation is safe to replay.
- Never rotate request ID until operation is semantically complete.

Request ID format:

```text
<assistant>_<operation>_<timestamp_ms>_<rand>
```

Examples:

- `oc_resume_1739373000123_a19f2c`
- `oc_task_set_1739373000456_b72a9d`

## Integration verification

1. New session can answer "what were we working on?" from injected resume context.
2. Replaying same write with same `--request-id` does not duplicate side effects.
3. Task updates are visible via `vybe task list`.
4. Memory written in one session appears in later resume context.
5. Integration relies on `vybe schema commands`, not hardcoded flags.

## Related docs

- `operator-guide.md` for runnable operator loops and recipes
- `contributor-guide.md` for command-surface change process
- `DECISIONS.md` for anti-regression guardrails
