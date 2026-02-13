# vibe

Agents-only CLI for autonomous continuity: tasks, events, memory,
and deterministic resume/brief.

Designed for zero human-in-the-loop workflows:

- no prompts
- no confirmations
- machine-first JSON I/O

![vibe CLI screenshot](docs/vybe.png)

## Why This Exists

AI coding agents lose all context when a session ends or crashes.
`vibe` gives them durable memory, task tracking, and deterministic
resume — so they pick up exactly where they left off.

## Get Started

```bash
# 1) install
go install github.com/dotcommander/vibe/cmd/vibe@latest

# 2) hook into your AI assistant
vibe hook install            # Claude Code
vibe hook install --opencode # OpenCode
```

That's it. Your agent now has continuity across sessions.

### What the hooks do

- **Session start:** runs `vibe resume` and injects task context, memory,
  and recent events into the agent's prompt
- **During work:** logs user prompts and failed tool calls for cross-session
  continuity and recovery
- **Before compaction/session end:** runs memory checkpoint (`memory compact` + `memory gc`)

### Optional: configure DB location

```yaml
# ~/.config/vibe/config.yaml
db_path: /Users/you/.config/vibe/vibe.db
```

Default: `~/.config/vibe/vibe.db`

### Uninstall hooks

```bash
vibe hook uninstall            # Claude Code
vibe hook uninstall --opencode # OpenCode
```

---

> **Below this line is reference for AI agents.**
> Humans: see `CLAUDE.md` for architecture or `docs/` for guides.

## Quick Start (60 Seconds)

```bash
# 1) install
go install github.com/dotcommander/vibe/cmd/vibe@latest

# 2) set identity once
export VIBE_AGENT=worker1

# 3) initialize state
vibe agent init --request-id init_1

# 4) create a task
vibe task create --request-id task_1 --title "Ship it" --desc "Do the thing"

# 5) resume work (cursor advances atomically)
vibe resume --request-id resume_1
```

If this works, your loop is ready.

## 5-Minute Work Loop

Use this exact order when you want low cognitive overhead.

```bash
export VIBE_AGENT=worker1

# A) get focus + context packet
BRIEF=$(vibe resume --request-id req_resume_1)
TASK_ID=$(echo "$BRIEF" | jq -r '.data.brief.task.id // ""')

# B) log progress
vibe log --request-id req_log_1 --kind progress --task "$TASK_ID" --msg "Started step 1"

# C) persist checkpoint memory
vibe memory set --request-id req_mem_1 -k checkpoint -v step_1_done \
  -s task --scope-id "$TASK_ID"

# D) attach output
vibe artifact add --request-id req_art_1 --task "$TASK_ID" \
  --path /tmp/output.json --type application/json

# E) finish task
vibe task close --request-id req_done_1 --id "$TASK_ID" \
  --outcome done --summary "Completed step 1"
```

Crash in the middle? Re-run `vibe resume` and continue.

## Core Guarantees

- Crash-safe resume from persisted state
- Idempotency for mutating commands via `--request-id`
- Deterministic focus selection (no human routing needed)
- Concurrent-agent safe updates with CAS + retries
- JSON output contracts versioned by `schema_version`

## Integrations

`vibe` ships installer commands for Claude Code and OpenCode.

```bash
# Claude Code hooks
vibe hook install
vibe hook uninstall

# OpenCode bridge plugin
vibe hook install --opencode
vibe hook uninstall --opencode
```

### What Claude hooks do

- `vibe hook session-start`: runs `vibe resume` and injects context
- `vibe hook prompt`: logs user prompts for continuity
- `vibe hook tool-failure`: logs failed tool calls for recovery context
- `vibe hook checkpoint`: performs best-effort memory compact/gc on `PreCompact` and `SessionEnd`
- `vibe hook task-completed`: logs Claude `TaskCompleted` lifecycle signals
- `vibe hook retrospective`: extracts session retrospective on `SessionEnd`

Install to project-local Claude settings instead of user-global settings:

```bash
vibe hook install --project
vibe hook uninstall --project
```

### What OpenCode bridge does

- Installs `~/.config/opencode/plugins/vibe-bridge.js`
- On `session.created`: calls project-scoped `vibe resume`
- On `todo.updated`: appends `todo_snapshot` events
- On system prompt transform: injects cached "Vibe Resume Context"

Manual example assets:

- `examples/opencode/opencode-vibe-plugin.ts`
- `examples/opencode/opencode-plugin-setup.md`

Assistant-agnostic integration guide:

- `docs/integration-custom-assistant.md`

## Backfill Existing History

```bash
vibe ingest history --agent=claude
vibe ingest history --agent=claude --project=/path/to/repo
vibe ingest history --agent=claude --dry-run
```

## High-Signal Command Map

| Command | Use it when |
| --- | --- |
| `vibe resume` | start/restart loop and fetch deltas + brief |
| `vibe brief` | inspect context without advancing cursor |
| `vibe task create` | create new work item |
| `vibe task start --id ID` | claim specific task + in_progress + focus |
| `vibe task claim` | server-side pick next eligible task + claim + focus |
| `vibe task close --id ID --outcome done\|blocked` | atomically close task with summary |
| `vibe task heartbeat --id ID --ttl-minutes N` | refresh claim lease to prevent expiry |
| `vibe task gc` | release expired task claims |
| `vibe task set-status --id ID --status ...` | move task lifecycle (low-level) |
| `vibe log --kind ... --msg ...` | append progress/note event |
| `vibe memory set/get/list/delete` | persistent scoped memory |
| `vibe memory touch --key K --scope S` | bump confidence + last_seen_at without rewriting value |
| `vibe memory query --pattern P` | search memory by pattern, ranked by confidence |
| `vibe memory compact / gc` | memory hygiene and cleanup |
| `vibe artifact add/list/get` | link files to task history |
| `vibe events list / tail --jsonl` | inspect or stream continuity log |
| `vibe events summarize` | archive old ranges with summary event |
| `vibe project create/get/list/delete` | manage project metadata for isolation |
| `vibe session digest` | show session event digest for an agent |
| `vibe status` | installation status and system overview |
| `vibe upgrade` | pull latest and reinstall from source |
| `vibe schema` | machine-readable command schemas |

All mutating commands support `--request-id`.

## Config

Config lookup (first found wins):

1. `~/.config/vibe/config.yaml`
2. `/etc/vibe/config.yaml`
3. `./config.yaml`

Overrides:

- `--db-path` (highest)
- `VIBE_DB_PATH`

DB path precedence:

`--db-path` > `VIBE_DB_PATH` > `config.yaml: db_path` > `~/.config/vibe/vibe.db`

Example:

```yaml
db_path: /Users/you/.config/vibe/vibe.db
```

## JSON Contract

Non-streaming success responses use this stable envelope:

```json
{
  "schema_version": "v1",
  "success": true,
  "data": {}
}
```

- Streaming mode (`vibe events tail --jsonl`) emits raw event JSONL lines
- Errors are structured JSON logs on `stderr`
- Envelope changes are additive-only

## Build And Verify

```bash
go build -o vibe ./cmd/vibe
go install ./cmd/vibe

# optional local symlink workflow
ln -sf "$(pwd)/vibe" ~/go/bin/vibe

# release-style build with version string
go build -ldflags "-X main.version=$(git describe --tags 2>/dev/null || echo dev)" \
  -o vibe ./cmd/vibe
```

Development verification:

```bash
gofmt -w ./cmd/vibe ./internal
go test ./...
go vet ./...
go build ./...
```

## Repository Layout

```text
cmd/vibe/            entry point
internal/commands/   Cobra CLI layer
internal/actions/    business logic
internal/store/      SQLite persistence + migrations
docs/                architecture and usage docs
examples/            integration examples and setup snippets
```

## Optional: Daily Agent Checklist

If checklists help your focus, use this:

1. Set identity (`VIBE_AGENT`)
2. `vibe resume --request-id ...`
3. Work one task
4. `vibe log` meaningful progress
5. Save memory checkpoints
6. Attach artifacts
7. Mark task status
8. Repeat
