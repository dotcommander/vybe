# Custom Assistant Integration Contract

Use this to connect any coding assistant (CLI, IDE, web agent, plugin host) to `vibe`.

Design target: minimal coupling, deterministic behavior, easy retries.

## Fast Integration Checklist

1. Choose stable `--agent` identity.
2. Add `--request-id` to every write.
3. Parse JSON envelope from `stdout`.
4. Parse structured error logs from `stderr`.
5. Map session/task/progress/memory/artifact events.

## Core Contract

### Identity

- Always use stable identity via `--agent` or `VIBE_AGENT`.
- Recommended shape: `<assistant>-<workspace-or-session-prefix>`.

### Idempotency

- Every mutating command must include `--request-id`.
- On retry, reuse the same `--request-id`.
- Never generate a new request id while replaying a failed write.

### Machine I/O

- Success payload comes from `stdout` JSON envelope:
  `{ schema_version, success, data }`.
- Failure details come from structured JSON logs on `stderr`.
- Do not parse human prose.

### Project Scope

- Pass workspace context at resume time:

```bash
vibe resume --agent "$AGENT" --request-id "$REQ" --project "$WORKSPACE"
```

- Keep resume/task/memory operations aligned to active project.

## Required Event Mappings

### Session Start

On new session (or restored session first turn):

```bash
vibe resume --agent "$AGENT" --request-id "$REQ" --project "$WORKSPACE"
```

Inject `.data.prompt` (or `.data.brief`) into assistant system/session context.

### Task Sync

- create -> `vibe task create ...`
- start/in_progress -> `vibe task start ...` (specific task) or
  `vibe task claim ...` (server-side next-eligible selection)
- complete/blocked -> `vibe task close --outcome done|blocked --summary "..."` or
  `vibe task set-status ...` (low-level)

### Progress Log

At meaningful checkpoints:

```bash
vibe log --agent "$AGENT" --request-id "$REQ" \
  --kind progress --task "$TASK_ID" --msg "..."
```

### Durable Memory

Persist stable facts/decisions:

```bash
vibe memory set --agent "$AGENT" --request-id "$REQ" \
  --key ... --value ... --scope task --scope-id "$TASK_ID"
```

Use project scope for cross-task facts.

### Artifacts

Persist output files:

```bash
vibe artifact add --agent "$AGENT" --request-id "$REQ" \
  --task "$TASK_ID" --path <file>
```

## Optional Event Mappings

- Commit event -> `vibe log --kind commit --metadata ...`
- Delegated subagent start/finish -> `vibe log --kind subtask_* ...`
- Session idle -> `vibe memory compact` then `vibe memory gc`

## Request-ID Template

```text
<assistant>_<operation>_<timestamp_ms>_<rand>
```

Examples:

- `oc_resume_1739373000123_a19f2c`
- `oc_task_set_1739373000456_b72a9d`

## Verification Checklist

1. New session can answer "what were we working on?" from injected resume context.
2. Replaying same write with same `--request-id` does not duplicate events.
3. Task updates are visible in `vibe task list`.
4. Memory from one session appears in next resume/brief.
5. Assistant handles write failures by reading `stderr` JSON and retrying safely.

## Reference Integrations

- Claude hooks -> see `README.md` and `docs/agent-install.md`
- OpenCode bridge installer -> `vibe hook install --opencode`
- OpenCode examples -> `examples/opencode/opencode-vibe-plugin.ts`,
  `examples/opencode/opencode-plugin-setup.md`
