# Connect an Assistant

Use this to connect any coding assistant (CLI, IDE, web agent, plugin host) to `vybe`.

## Fast Integration Checklist

1. Choose stable `--agent` identity.
2. Add `--request-id` to every write.
3. Parse JSON envelope from `stdout`.
4. Parse structured error logs from `stderr`.
5. Map session/task/progress/memory/artifact events.

## Core rules

### Identity

- Always use stable identity via `--agent` or `VYBE_AGENT`.
- Recommended format: `<assistant>-<workspace-or-session-prefix>`.

### Idempotency

- Every mutating command must include `--request-id`.
- On retry, reuse the same `--request-id`.
- Never generate a new request id while replaying a failed write.

### Machine I/O

- Success payload comes from `stdout` JSON envelope:
  `{ schema_version, success, data }`.
- Failure details come from structured JSON logs on `stderr`.
- Do not parse human prose.

### Project scope

- Pass workspace context at resume time:

```bash
vybe resume --agent "$AGENT" --request-id "$REQ" --project "$WORKSPACE"
```

- Keep resume/task/memory operations aligned to active project.

## Required command mappings

### Session Start

On new session (or restored session first turn):

```bash
vybe resume --agent "$AGENT" --request-id "$REQ" --project "$WORKSPACE"
```

Inject `.data.prompt` (or `.data.brief`) into assistant system/session context.

### Task Sync

- create -> `vybe task create ...`
- begin/in_progress -> `vybe task begin ...` (specific task) or
  `vybe task claim ...` (server-side next-eligible selection)
- complete/blocked -> `vybe task complete --outcome done|blocked --summary "..."` or
  `vybe task set-status ...` (low-level)

### Progress Log

At meaningful checkpoints:

```bash
vybe events add --agent "$AGENT" --request-id "$REQ" \
  --kind progress --task "$TASK_ID" --msg "..."
```

### Durable Memory

Persist stable facts/decisions:

```bash
vybe memory set --agent "$AGENT" --request-id "$REQ" \
  --key ... --value ... --scope task --scope-id "$TASK_ID"
```

Use project scope for cross-task facts.

### Artifacts

Persist output files:

```bash
vybe artifact add --agent "$AGENT" --request-id "$REQ" \
  --task "$TASK_ID" --path <file>
```

## Optional mappings

- Commit event -> `vybe events add --kind commit --metadata ...`
- Delegated subagent start/finish -> `vybe events add --kind subtask_* ...`
- Session idle -> `vybe memory compact` then `vybe memory gc`

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
3. Task updates are visible in `vybe task list`.
4. Memory from one session appears in next resume/brief.
5. Assistant handles write failures by reading `stderr` JSON and retrying safely.

## Related docs

- Claude hooks -> see `../README.md` and `setup.md`
- OpenCode bridge installer -> `vybe hook install --opencode`
- OpenCode example -> `../examples/opencode/opencode-vybe-plugin.ts`
- Full command/subcommand map -> `command-reference.md`
