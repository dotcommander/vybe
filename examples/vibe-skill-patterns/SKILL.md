---
name: vibe-skill-patterns
description: Integration patterns for connecting coding assistants to vibe with minimal intrusion. Use when wiring session start/resume, task sync, memory persistence, artifacts, and idempotent retries.
---

# Vibe Skill Patterns

Use this skill when integrating any assistant (CLI, plugin, IDE, web) with `vibe`.

## Fast Checklist

1. Stable identity (`--agent` or `VIBE_AGENT`)
2. `--request-id` on every write
3. Parse JSON envelope only
4. Pass `--project` on resume when workspace is known

## Core Principles

1. Stable identity: always pass `--agent` (or set `VIBE_AGENT`).
2. Idempotent writes: every mutating call includes `--request-id`.
3. Machine I/O only: parse JSON envelope on stdout and structured JSON logs on stderr.
4. Project scope first: pass `--project` on resume/focus to keep memory and events isolated.

## Minimal Event Mapping

- Session start -> `vibe resume --agent <agent> --request-id <id> --project <workspace>`
- Task lifecycle -> `vibe task create/start/set-status`
- Progress -> `vibe log --kind progress --task <id> --msg "..."`
- Durable memory -> `vibe memory set --scope task|project ...`
- Artifact persistence -> `vibe artifact add --task <id> --path <file>`

## Retry Pattern

- On network/tool failure: retry with the exact same `--request-id`.
- Never regenerate request id during retries.

## Lightweight Validation

1. Fresh session can answer: "what were we working on?"
2. Duplicate write replay does not duplicate events.
3. Task/memory created in one session appears in next resume.

## Example Request-ID Format

`<assistant>_<operation>_<unix_ms>_<randhex>`

Examples:

- `oc_resume_1739373000123_a19f2c`
- `claude_task_set_1739373000456_b72a9d`

## References

- `docs/integration-custom-assistant.md`
- `docs/agent-install.md`
- `examples/opencode/opencode-vibe-plugin.ts`
- `examples/opencode/opencode-plugin-setup.md`
