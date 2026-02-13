# Vibe Research Loop Demo (Genealogy Style)

This demo models a genealogy-style research queue using `vibe` primitives.

It shows a loop like:

1. seed research tasks
2. resume deterministic focus
3. run worker on one task
4. log progress + memory + artifacts
5. mark task `completed` or `blocked`
6. continue automatically

The worker is intentionally simple and deterministic so we can measure what
`vibe` supports natively and what is still missing for a full genealogy loop.

## Requirements

- `vibe` binary on PATH
- `jq`

## Quick Run

```bash
cd ~/go/src/vibe

# 1) seed isolated demo DB + tasks
./examples/research-loop-vibe-demo/setup-demo.sh

# 2) run autonomous loop with mock worker
./examples/research-loop-vibe-demo/run-demo.sh

# 3) print support/missing report from this run
./examples/research-loop-vibe-demo/evaluate-support.sh
```

All demo state is isolated to:

- `./.work/demo/research-loop-vibe.db`
- `./output/research-loop-vibe-demo/`

## What The Mock Worker Does

- Reads current focus via `vibe brief`
- Logs `research_started` and `research_finished` events
- Writes task-scoped memory checkpoints
- Writes an artifact markdown file per task
- Marks task `blocked` when title contains `BLOCKED_DEMO`
- Marks task `completed` otherwise

This gives a realistic automation pass without needing an external LLM.

## Interpreting "Support vs Missing"

The evaluator script reports two sections:

- **Supported now**: capabilities proven by commands/events in this run
- **Missing for genealogy parity**: features you still implement outside vibe

Current expected "missing" areas are:

- no built-in source-tier evidence policy engine (Tier 1/2/3/4 conflict rules)
- no built-in queue statuses `queued`/`claimed` heartbeat semantics per task
- no built-in remote cache protocol (`sha256(url)` cache file convention)
- no built-in domain packet generator from ancestor facts files

These are workflow/domain layers that can be built on top of `vibe`.
