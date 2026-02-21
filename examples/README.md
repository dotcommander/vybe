# Examples

## Prerequisites

- `vybe` binary installed and on PATH
- `jq` installed (required for shell examples)
- Bun runtime (required for the OpenCode plugin)

```bash
go install github.com/dotcommander/vybe/cmd/vybe@latest
```

## Contents

| Directory | What it shows |
|-----------|---------------|
| [`vybe-agent-patterns/`](vybe-agent-patterns/) | Claude Code skill for crash-safe agent continuity. Covers the resume cycle, idempotent writes, memory scopes, worker loops, task decomposition, and crash-safe checkpoints. Install as a skill to teach Claude how to use vybe. |
| [`research-loop-vybe-demo/`](research-loop-vybe-demo/) | Autonomous research queue demo. Creates tasks with dependencies, runs `vybe loop` with a mock worker, evaluates which vybe capabilities were exercised. |
| [`opencode/`](opencode/) | TypeScript plugin connecting OpenCode to vybe. Wires all 8 OpenCode hook entry points to vybe events for session continuity. |

Core docs for these examples:

- `../docs/operator-guide.md` for runnable loop recipes
- `../docs/agent-contract.md` for machine I/O and retry contract
