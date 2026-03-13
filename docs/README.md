# Vybe docs

Route to the right doc fast.

## By role

| Role | Start here | Use this for |
| --- | --- | --- |
| Operator (run agent loops) | `operator-guide.md` | install/bootstrap, baseline loop, day-2 recipes |
| Integrator (connect tools/assistants) | `agent-contract.md` | machine I/O, idempotency/retries, command contract |

## All docs

| File | Contents |
| --- | --- |
| [`operator-guide.md`](operator-guide.md) | Bootstrap, baseline loop, and day-2 operational recipes |
| [`agent-contract.md`](agent-contract.md) | Machine I/O contract, idempotency, retry behavior, session mappings |
| [`decisions.md`](decisions.md) | Command-surface guardrails and design principles |

Working examples (Claude Code skill, autonomous loop demo, OpenCode plugin) are in [`examples/`](../examples/).

For machine callers, use `vybe schema` as the source of truth for flags and required fields.

Beta policy: no backward-compatibility shims. Keep one canonical command/flag shape.

Audit and scratch material stay outside tracked docs under `.work/`.

---

Licensed under MIT. See `LICENSE` in the repo root.
