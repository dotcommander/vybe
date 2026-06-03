# Agent Contract

The canonical machine-facing contract for assistants, plugins, and autonomous workers.

## Fast checklist

1. Set a stable agent identity once via `default_agent` (or `VYBE_AGENT`) so `--agent` can be omitted.
2. Omit `--request-id` by default; pass a stable one only when retrying the exact same operation.
3. Parse `stdout` JSON envelope only.
4. Parse `stderr` logs as diagnostics only.
5. Discover command/flag schemas via `vybe schema commands`.

## Core invariants

### Identity

Every call that touches agent state needs `--agent`. Without it, vybe can't scope memory, cursor, or focus to your session. Identity resolves in precedence order: `--agent` flag → `VYBE_AGENT` env → `config.yaml: default_agent`. Set `default_agent` (or `VYBE_AGENT`) once to avoid passing `--agent` on every call. Format: `<assistant>-<workspace-or-session-prefix>`.

### Idempotency

Omit `--request-id` by default. When omitted, vybe auto-generates a unique one (`req_<nano>_<hex>`), giving at-least-once semantics — separate calls each get a distinct key and never collide. A freshly-generated per-call id is identical to omitting it: it never dedupes. Pass an explicit, STABLE `--request-id` only when retrying the *exact same* logical operation: `resume` without `--peek`, `push`, `task *`, `memory set|delete|gc`. When you retry, send the same `--request-id` you used the first time. Vybe replays the original result — no duplicate write, no side effect. Never mint a new request ID while replaying the same logical write.

### Machine I/O

Your agent will break if it parses the wrong stream. All protocol data comes from `stdout` only:

- Success: `{ "schema_version": "v1", "success": true, "data": ... }`
- Error: `{ "schema_version": "v1", "success": false, "error": ... }`

Structured logs go to `stderr`. Do not parse help prose as protocol data.

### Command discovery

Hardcoded flags break when the schema changes. `vybe` with no args returns a JSON command index. `vybe schema` returns argument schema, mutation hints, and `agent_protocol` guidance. Prefer schema-driven calls over hardcoded flags.

## Canonical command surface

Top-level commands:

- `artifacts`
- `block` (sugar: block focus/given task)
- `done` (sugar: complete focus/given task)
- `events`
- `focus` (sugar: read current focus, no cursor advance)
- `help`
- `hook`
- `memory`
- `note` (sugar: log a progress event)
- `push`
- `remember` (sugar: set a memory)
- `resume`
- `schema`
- `status`
- `task`
- `upgrade`

Primary subcommands:

- `hook install|uninstall`
- `memory set|get|list|delete|gc|pin`
- `task create|begin|get|list|set-status`

## Canonical flag semantics

- `--project-dir`: workspace directory scope (`resume`).
- `--project-id`: task/project entity association/filter (`task create`, `task list`).
- `--task-id`: artifacts/events read filters.

## Required mappings

### Session start

Your agent has no context without this. Run it at the top of every session:

```bash
vybe resume --project-dir "$WORKSPACE"
```

Inject `.data.prompt` (or `.data.brief`) into assistant context.

For autonomous agent work, the terminal verbs for the current `focus_task_id` are `vybe done <id>` (completed) and `vybe block <id> --reason "..." [--failure]` (blocked). Both are sugar over `task set-status --status completed|blocked`. Retries with the same `--request-id` are safe and will not duplicate the transition.

### Task sync

- create: `vybe task create ...`
- claim/start: `vybe task begin ...` or `vybe resume ...` (deterministic focus)
- terminal — completed (canonical): `vybe done <id> [--note "<summary>"]`
- terminal — blocked (canonical): `vybe block <id> --reason "..." [--failure]`
- terminal (equivalent verbose form): `vybe task set-status --id ... --status completed|blocked`
- task read: `vybe task get --id ...`
- queue read: `vybe task list --project-id ...`

### Progress log

For a single progress event, use the sugar verb:

```bash
vybe note <task_id> "what happened"
```

Use `push --json` for a genuine multi-op atomic batch (event + memory + artifacts + status in one call), or when you need to attach reasoning/metadata to a THINK event:

```bash
vybe push --json "{\"task_id\":\"$TASK_ID\",\"event\":{\"kind\":\"progress\",\"message\":\"...\"}}"
```

### Durable memory

The sugar verb takes a `key=value` pair:

```bash
vybe remember "key=value" --scope task --scope-id "$TASK_ID"
```

It accepts the same `--scope`, `--scope-id`, `--kind`, and `--pin` flags as `memory set`, which remains the equivalent verbose form:

```bash
vybe memory set --key ... --value ... --scope task --scope-id "$TASK_ID"
```

For `--scope task` or `--scope project`, `--scope-id` is inferred from the agent's focus (set via `vybe task begin`) when omitted. It is required for `--scope agent`, and for task/project when no focus is set.

`memory set` accepts a `--kind` to classify the entry. The kind controls how the memory renders in the resume brief and how fast it decays:

| Kind | Default half-life | Brief section | Use for |
| --- | --- | --- | --- |
| `directive` | never decays | `=== Directives ===` (first, value-only) | Imperative behavioral rules |
| `fact` (default) | 90 days | `=== Facts ===` (key=value form) | Project state, decisions, identifiers |
| `lesson` | 14 days | `=== Facts ===` (key=value form) | Tactical insights, post-incident notes |

Override per-entry decay with `--half-life-days <n>`. Pin a memory with `--pin` (or via `vybe memory pin`) to force it to sort first in the brief regardless of decay.

Pin semantics are sticky upward: `--pin` sets the flag, but a later `memory set` without `--pin` will NOT clear it. Only `vybe memory pin --unpin --key <k>` removes the pin. This protects durable strategic memory from incidental overwrites.

```bash
# Pin (or unpin) a memory entry
vybe memory pin --key <k> --scope task --scope-id "$TASK_ID"

vybe memory pin --key <k> --scope task --scope-id "$TASK_ID" --unpin
```

### Artifacts

```bash
vybe push --json "{\"task_id\":\"$TASK_ID\",\"artifacts\":[{\"file_path\":\"<file>\"}]}"
```

### Event and artifact reads

```bash
vybe events --task-id "$TASK_ID" --limit 50
vybe artifacts --task-id "$TASK_ID" --limit 50
```

## Retry contract

Transport failures and tool errors happen. Here's how to handle each:

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

- `operator-guide.md` for runnable operator recipes
- `decisions.md` for anti-regression guardrails
