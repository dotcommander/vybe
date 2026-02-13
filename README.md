# vybe

`vybe` is a CLI that gives coding agents durable continuity across sessions.
It stores task state, event logs, memory, and artifacts in SQLite.
Agents can crash, restart, or change context and still resume deterministically.

## Why install vybe

Install `vybe` if you want agents to:

- continue after crashes, restarts, or context resets
- keep task state in one place (`pending`, `in_progress`, `completed`, `blocked`)
- save memory and artifacts between sessions
- write safe retried updates using `--request-id`
- run with Claude Code or OpenCode without custom glue code

## Quick Start (2 minutes)

```bash
# 1) install
go install github.com/dotcommander/vybe/cmd/vybe@latest

# 2) connect one assistant
vybe hook install            # Claude Code
# OR
vybe hook install --opencode # OpenCode

# 3) set agent name once
export VYBE_AGENT=worker1

# 4) verify installation
vybe status
```

If `vybe status` succeeds, setup is done.

## First task in 60 seconds

```bash
# create work
vybe task create --request-id task_1 --title "Ship feature" --desc "Implement X"

# get current focus + context
vybe resume --request-id resume_1

# close when done
vybe task complete --request-id close_1 --id <TASK_ID> --outcome done --summary "Finished"
```

If a session crashes, run `vybe resume` and keep going.

## Uninstall (one line)

```bash
vybe hook uninstall            # Claude Code
vybe hook uninstall --opencode # OpenCode
```

## Advanced docs

For setup, common tasks, assistant connection, implementation guides, and the full command map, go to `docs/README.md`.
