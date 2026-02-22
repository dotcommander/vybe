# Vybe Docs

Purpose: route you to the next correct action fast.

## Choose your role

| Role | Start here | Use this for |
| --- | --- | --- |
| Operator (run agent loops) | `operator-guide.md` | install/bootstrap, baseline loop, day-2 recipes |
| Integrator (connect tools/assistants) | `agent-contract.md` | machine I/O, idempotency/retries, command contract |
| Contributor (change vybe code) | `contributor-guide.md` | architecture, safe change workflow, verification |

Working examples (Claude Code skill, autonomous loop demo, OpenCode plugin) are in [`examples/`](../examples/).

For machine callers, use `vybe schema commands` as the source of truth for flags/required fields and the `agent_protocol` contract.

Beta policy: no backward-compatibility shims. Keep one canonical command/flag shape.

Command-surface guardrails and anti-regression rationale live in `DECISIONS.md`.

Minimal-core boundary and pruning checklist live in `minimal-surface.md`.

Audit and scratch material stay outside tracked docs under `.work/`.

---

Licensed under MIT. See `LICENSE` in the repo root.
