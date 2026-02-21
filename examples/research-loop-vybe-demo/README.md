# Vybe Research Loop Demo (Genealogy Style)

This demo models a genealogy-style research queue using `vybe` primitives.

It shows a loop like:

1. seed research tasks
2. resume deterministic focus
3. run worker on one task
4. log progress + memory + artifacts
5. mark task `completed` or `blocked`
6. continue automatically

The worker is intentionally simple and deterministic so we can measure what
`vybe` supports natively and what is still missing for a full genealogy loop.

## Requirements

- `vybe` binary on PATH
- `jq`

## Quick Run

```bash
cd ~/go/src/vybe

# 1) seed isolated demo DB + tasks
./examples/research-loop-vybe-demo/setup-demo.sh

# 2) run autonomous loop with mock worker
./examples/research-loop-vybe-demo/run-demo.sh

# 3) print support/missing report from this run
./examples/research-loop-vybe-demo/evaluate-support.sh
```

All demo state is isolated to:

- `./.work/demo/research-loop-vybe.db`
- `./output/research-loop-vybe-demo/`

## What The Mock Worker Does

- Reads current focus via `vybe resume --peek`
- Logs `research_started` and `research_finished` events via `vybe push`
- Writes task-scoped memory checkpoints
- Writes an artifact markdown file per task via `vybe push`
- Calls `vybe task complete --outcome blocked` when title contains `BLOCKED_DEMO`
- Calls `vybe task complete --outcome done` otherwise

All scripts are executable (`chmod +x` already applied).

This gives a realistic automation pass without needing an external LLM.

## Interpreting "Support vs Missing"

The evaluator script reports two sections:

- **Supported now**: capabilities proven by commands/events in this run
- **Missing for genealogy parity**: features you still implement outside vybe

Current expected "missing" areas are:

- no built-in source-tier evidence policy engine (Tier 1/2/3/4 conflict rules)
- no built-in queue statuses `queued`/`claimed` heartbeat semantics per task
- no built-in remote cache protocol (`sha256(url)` cache file convention)
- no built-in domain packet generator from ancestor facts files

These are workflow/domain layers that can be built on top of `vybe`.
