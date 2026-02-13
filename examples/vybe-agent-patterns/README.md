# Claude Code Skill: Vybe Agent Patterns

A ready-to-use Claude Code skill that teaches Claude how and when to use `vybe` for durable agent continuity.

## Install

Copy the skill directory into your Claude Code skills:

```bash
cp -r examples/vybe-agent-patterns ~/.claude/skills/vybe-agent-patterns
```

Or symlink it to track upstream changes:

```bash
ln -sf "$(pwd)/examples/vybe-agent-patterns" ~/.claude/skills/vybe-agent-patterns
```

## What it provides

The skill teaches Claude Code to:

- Resume context after crashes or session resets
- Create and manage task queues with deterministic focus selection
- Log progress events at meaningful checkpoints
- Persist cross-session memory (facts, decisions, checkpoints)
- Link artifacts to tasks
- Use idempotent writes with `--request-id` for safe retries
- Run autonomous worker loops
- Decompose work into dependent subtasks

## Prerequisites

- `vybe` installed and on PATH (`go install github.com/dotcommander/vybe/cmd/vybe@latest`)
- Claude Code hooks installed (`vybe hook install`)
- `VYBE_AGENT` environment variable set

## Verify

After installing the skill, start a new Claude Code session and ask:

> How do I track progress across sessions with vybe?

Claude should reference vybe commands directly from the skill.
