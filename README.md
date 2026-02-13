# vybe

`vybe` is a CLI that gives AI coding agents persistent working state.
It is designed for autonomous execution, with no human intervention required in the task loop.
Setup is quick, non-intrusive, and does not require changing your existing project architecture.
If you do not want it, uninstall is one command: `vybe hook uninstall` (or `vybe hook uninstall --opencode`).
It saves tasks, event history, memory, and artifacts in SQLite so agents can restart and continue from where they stopped.

![vybe CLI screenshot](docs/vybe.png)

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
vybe task close --request-id close_1 --id <TASK_ID> --outcome done --summary "Finished"
```

If a session crashes, run `vybe resume` and keep going.

## Uninstall (one line)

```bash
vybe hook uninstall            # Claude Code
vybe hook uninstall --opencode # OpenCode
```

## Advanced docs

Deeper usage, contracts, and contributor internals are routed in `docs/README.md`.

Architecture and internal design notes are in `CLAUDE.md`.
