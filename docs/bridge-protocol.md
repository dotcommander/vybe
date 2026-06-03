# vybe Bridge Protocol

## Overview

The **vybe Bridge Protocol** is the public contract for integrating any agent
harness (Cursor, Zed, a custom CLI, a homegrown agent loop, …) with vybe. A
harness does not need a vybe-specific Go plugin: it integrates by piping
**vybe-native JSON** into a `vybe hook` subcommand under the **generic host**.

```
<harness event> --JSON--> stdin --> vybe hook <subcommand> --host generic --> stdout --> {"context":"..."}
```

The flow:

1. The harness serializes one event as a small JSON object (the
   [stdin schema](#stdin-schema)).
2. It runs `vybe hook <subcommand> --host generic` (or sets `VYBE_HOST=generic`)
   and writes the JSON to that process's stdin.
3. vybe parses the event, records it, and runs the subcommand's handler.
4. For the **session-start** and **prompt-trigger** paths, vybe may write
   `{"context":"<string>"}` on stdout. The harness injects that string into the
   model's context window. All other hooks are **silent** (no stdout).

### Why "generic"

vybe's hook commands default to the **claude** host, which speaks Claude Code's
hook JSON. The generic host accepts vybe's own canonical schema, so a harness can
emit one shape that maps directly onto vybe's event taxonomy with no Claude-isms.

### Relationship to the OpenCode bridge

The proven reference integration is the OpenCode bridge:
[`internal/commands/hookcmd/opencode_bridge_plugin.ts`](../internal/commands/hookcmd/opencode_bridge_plugin.ts).
It demonstrates the integration shape end-to-end (resume hydration, prompt
capture, tool-failure logging, checkpoint/session-end maintenance).

Note the distinction:

- The OpenCode bridge routes **arbitrary events** through `vybe push --json '{…}'`
  (heartbeats, todo snapshots, tool successes/failures) and uses `vybe hook
  session-end` / `vybe hook checkpoint` for **maintenance** hooks.
- The **generic host protocol** lets a bridge route **everything** through
  `vybe hook <event> --host generic`, with vybe deriving the canonical event from
  the subcommand plus the JSON payload. New bridges should prefer this path: one
  uniform invocation pattern, vybe-native schema, stdout context injection for the
  prompt/session-start paths.

## Host selection

The host protocol is selected by this precedence:

1. **`--host` flag** (e.g. `--host generic`)
2. **`VYBE_HOST` env var** (e.g. `VYBE_HOST=generic`)
3. **`claude`** (default)

An **unknown value falls back to `claude`** (fail-safe: a typo such as
`--host genric` never silently breaks an installed Claude hook).

## Stdin schema

Each event is a single JSON object read from stdin. Field names mirror Claude's
hook tags where they overlap (so a Claude-shaped payload works unchanged), plus
vybe-native extensions (`kind`, `event_name`, `host_source`, `agent`).

| JSON field      | Type   | Meaning | Used by |
|-----------------|--------|---------|---------|
| `kind`          | string | vybe `EventKind` override. **Optional** — the subcommand implies the kind; an explicit `kind` overrides it. Values: `session_start`, `prompt`, `tool_failure`, `checkpoint`, `session_end`, `task_completed`. | all (override) |
| `cwd`           | string | Working directory the event occurred in. Falls back to the process cwd when empty. | all (recommended) |
| `session_id`    | string | Harness session identifier; scopes resume/checkpoint state. | all (recommended) |
| `prompt`        | string | The user prompt text. | `prompt` |
| `tool_name`     | string | Name of the tool that ran (e.g. `Edit`, `Bash`). | `tool_failure` |
| `tool_input`    | object (raw JSON) | The tool's input arguments, passed through verbatim. | `tool_failure` |
| `tool_response` | object (raw JSON) | The tool's response/result, passed through verbatim. | `tool_failure` |
| `source`        | string | Host **sub-source** (e.g. `"compact"` for a compaction-driven session-start) → recorded as metadata `resume_source`. | `session_start` (optional) |
| `event_name`    | string | The harness's **native** event label (e.g. `"UserPromptSubmit"`) → recorded as metadata `hook_event`. | all (optional) |
| `host_source`   | string | Metadata **`source`** attribution (e.g. `"cursor"`). Defaults to `"generic"` when empty. | all (optional) |
| `task_id`       | string | Task identifier for task-completion events. | `task_completed` (optional) |
| `agent`         | string | Identity **fallback** when no `--agent` / `VYBE_AGENT` / config `default_agent` is set. See [Identity](#identity). | all (fallback) |

### Required-per-kind matrix

| Event kind (subcommand)            | Required           | Recommended            | Optional                       |
|------------------------------------|--------------------|------------------------|--------------------------------|
| `session_start` (`session-start`)  | —                  | `session_id`, `cwd`    | `source`, `event_name`         |
| `prompt` (`prompt`)                | `prompt`           | `session_id`, `cwd`    | `event_name`                   |
| `tool_failure` (`tool-failure`)    | `tool_name`        | `session_id`, `cwd`    | `tool_input`, `tool_response`  |
| `task_completed` (`task-completed`)| —                  | `session_id`, `cwd`    | `task_id`                      |
| `checkpoint` / `session_end`       | —                  | `session_id`, `cwd`    | `event_name`                   |

Notes:

- **`prompt`** is required for the `prompt` event.
- **`tool_name`** is required for the `tool_failure` event.
- **`task_id`** is optional for `task_completed`.
- **`session_id`** and **`cwd`** are recommended on every event — they scope
  resume/checkpoint state and project attribution.
- **`kind`** is optional: the subcommand implies the kind; an explicit `kind`
  overrides it (forward-compat with a future single-endpoint daemon).
- **`host_source`** sets the metadata `source` attribution (default `"generic"`),
  so a bridge can stamp e.g. `"cursor"`.
- **`source`** is the host **sub-source** (e.g. `"compact"` for a session-start)
  → metadata `resume_source`.
- **`event_name`** is the **native** event label → metadata `hook_event`.

## Stdout

The only stdout shape is:

```json
{"context":"<string>"}
```

- **Empty or absent context ⇒ NO output** (nothing is written; the harness sees
  an empty stream).
- Only the **session-start** path and the **prompt-trigger** path emit context.
- All other hooks (`tool_failure`, `checkpoint`, `session_end`, `task_completed`)
  are **silent**.

A bridge should read stdout, parse `{"context":"..."}` when non-empty, and inject
`.context` into the model's context window. Treat empty stdout as "nothing to
inject."

## Identity

The agent identity used for event attribution and per-agent state resolves by
this precedence:

1. **`--agent` flag**
2. **`VYBE_AGENT` env var**
3. **config `default_agent`** (persistent fallback)
4. **`agent` JSON field** (the generic-input fallback)
5. **`"generic"`** (last-resort default)

The name is lowercased and trimmed.

A bridge **should set `--agent` or `VYBE_AGENT`** to a stable, harness-specific
identity (e.g. `cursor`, or `opencode-<project>` as the OpenCode bridge does). The
identity scopes resume context, checkpoints, and event attribution; if every
session falls through to `"generic"`, distinct harnesses and projects contaminate
one another's cross-session state. A stable identity keeps attribution clean
across sessions.

## Event metadata

When vybe records the event, it automatically stamps:

- **`source`** = `host_source` if provided, else `"generic"`.
- **`session_id`** = the `session_id` field.
- **`hook_event`** = the `event_name` field (the harness's native label).

Additional sub-source fields follow the schema above (`resume_source` from
`source`, etc.). A recorded prompt event's metadata looks roughly like:

```json
{
  "source": "cursor",
  "session_id": "abc123",
  "hook_event": "UserPromptSubmit"
}
```

## Minimal bridge example

Pipe a **prompt** event (stamping `cursor` as both source and agent):

```sh
echo '{"prompt":"refactor the parser","session_id":"abc123","cwd":"'"$PWD"'","host_source":"cursor"}' \
  | vybe hook prompt --host generic --agent cursor
```

Capture the context injected at **session-start**:

```sh
ctx=$(echo '{"session_id":"abc123","cwd":"'"$PWD"'"}' | VYBE_HOST=generic vybe hook session-start --agent cursor)
# ctx is {"context":"..."} or empty; inject .context into the model's context window
```

The first call records the prompt and is silent. The second may emit
`{"context":"..."}`; the harness injects `.context` (e.g. via `jq -r .context`)
into the model before the turn.

## See also

- [docs/decoupling-roadmap.md](decoupling-roadmap.md) — the broader decoupling
  program this protocol is part of.
- [internal/commands/hookcmd/opencode_bridge_plugin.ts](../internal/commands/hookcmd/opencode_bridge_plugin.ts)
  — the proven reference bridge.
