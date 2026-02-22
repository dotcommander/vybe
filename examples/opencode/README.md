# OpenCode Vybe Bridge Plugin

TypeScript plugin connecting OpenCode to vybe for session continuity. Logs
prompts, tool calls, checkpoints, and heartbeats to the vybe event stream so
agents can resume exactly where they left off across OpenCode sessions.

## Prerequisites

- `vybe` binary installed and on PATH (`go install github.com/dotcommander/vybe/cmd/vybe@latest`)
- Bun runtime
- OpenCode

## Install

Register the plugin in your OpenCode config by pointing to the plugin file:

```json
{
  "plugins": ["path/to/examples/opencode/opencode-vybe-plugin.ts"]
}
```

Or symlink it into your OpenCode plugins directory and reference it by name.

## Configuration

The plugin uses the default vybe database at `~/.config/vybe/vybe.db`. To use a
different database, set `VYBE_DB_PATH` in your environment before launching
OpenCode.

## Hook Entry Points

The plugin wires 8 OpenCode hook entry points:

| Hook | What it does |
|------|-------------|
| `session.created` | Calls `vybe resume` (project-scoped) and hydrates session context |
| `session.deleted` | Fires `SessionEnd` hook for checkpoint garbage collection |
| `session.idle` | Emits a `heartbeat` event to record the agent is still alive |
| `todo.updated` | Appends a compact `todo_snapshot` event (debounced 3 seconds) |
| `tool.execute.after` | Logs all tool failures and mutating tool successes (Write, Edit, Bash, etc.) |
| `chat.message` | Logs user prompts as `user_prompt` events for cross-session continuity |
| `experimental.session.compacting` | Fires checkpoint maintenance before context compaction |
| `experimental.chat.system.transform` | Injects vybe resume context into the system prompt |

## Agent Identity

The plugin resolves a stable agent name in priority order:

1. `VYBE_AGENT` environment variable (highest priority)
2. Derived from the project directory basename: `opencode-<project-name>`
3. Derived from the session ID prefix: `opencode-<first-8-chars>`
4. Default fallback: `opencode-agent`

Set `VYBE_AGENT` in your environment to pin a specific agent identity across
all OpenCode sessions.
