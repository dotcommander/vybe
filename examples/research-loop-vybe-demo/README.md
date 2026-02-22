# Vybe Research Loop Demo

Demonstrates `vybe loop` driving a mock worker through a dependency chain of four tasks.

## Requirements

- `vybe` binary on PATH
- `jq`

## Run

```bash
./examples/research-loop-vybe-demo/demo.sh
```

This single command:

1. Creates an isolated demo DB at `.work/demo/research-loop-vybe.db`
2. Seeds 4 genealogy-themed research tasks with a dependency chain
3. Sets project-scoped memory (`evidence_mode`, `local_first`)
4. Runs `vybe loop` with the mock worker
5. Prints a summary of task statuses

## What The Worker Does

- Reads current focus via `vybe resume --peek`
- Logs `research_started` / `research_finished` events via `vybe push`
- Writes task-scoped memory checkpoints
- Writes an artifact markdown file per task
- Marks task `blocked` when title contains `BLOCKED_DEMO`
- Marks task `done` otherwise

## Files

| File | Purpose |
|------|---------|
| `demo.sh` | Setup + run + summary (single entry point) |
| `worker.sh` | Mock worker invoked by `vybe loop --command` |

## Demo State

All state is isolated and gitignored:

- `.work/demo/research-loop-vybe.db`
- `output/research-loop-vybe-demo/`
