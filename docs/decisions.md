# Decisions

Command surface guardrails and design principles for vybe.

For removed-command rationale and implementation investigations, see `.work/specs/decisions-history.md`.

## Guiding principle

Vybe is continuity infrastructure for autonomous LLM agents. If a feature exists because a human might want it but no agent needs it, it doesn't belong in the CLI.

## Command surface guardrails (do not regress)

These guardrails are for LLM/agent callers. They are not style preferences.
Breaking them increases tool-call error rates and retry noise in autonomous workflows.

### `status` as a mode multiplexer (`--events`, `--schema`, `--artifacts`)

**Decision:** Prefer explicit command paths for distinct operations (`events`, `artifacts`, `schema`) instead of mode flags on one command.

**Why not keep mode flags:** When an agent sets two mode flags at once, one silently wins. The agent never knows which.

**Guardrail:** When adding a new operation, do not add another `status --<mode>` flag. Add a dedicated command/subcommand.

### Root no-args output as human help text

**Decision:** Default root invocation should be machine-parseable in agent workflows.

**Why not keep help text default:** Agents call root during discovery or by accident. Help prose is not JSON. The parser breaks, the agent adds a fallback, the fallback is wrong half the time.

**Guardrail:** Keep prose help behind explicit `help` flows. Keep default output machine-first.

### Positional IDs for task commands

**Decision:** Use one canonical input form for identifiers (`--id` / `--task-id`), not dual positional + flag forms.

**Why not keep both forms:** Two valid call shapes for the same argument means the schema is ambiguous. Agents sample from that ambiguity. Over time, they drift toward one form or mix them. Tests pass. Prod breaks.

**Guardrail:** New ID-bearing commands must be flag-only.

### Overloaded `--project` semantics (path vs project id)

**Decision:** Use semantically explicit flags (`--project-dir` vs `--project-id`).

**Why not keep one overloaded flag:** A path and an ID look nothing alike to a human. To a model calling `--project`, they're the same slot. One misfire puts an agent's writes into the wrong project's context with no error returned.

**Guardrail:** Do not reuse a single `--project` flag name for different domain meanings across commands.

### Schema inference from usage text

**Decision:** Machine schemas should come from explicit metadata/annotations, not natural-language usage parsing.

**Why not parse help text:** Someone edits a word in the usage string. The inferred enum changes. Weaker models now call the command wrong. No test catches it because the help text still renders fine.

**Guardrail:** Treat help text as human documentation only; treat machine schema as the source of truth.

### "Required" labels without enforced validation

**Decision:** Required flags must be enforced in runtime validation and reflected in schema.

**Why not rely on label-only required markers:** A flag labeled required but not enforced is a lie. Agents learn from schema. If the schema says required but the runtime doesn't check, you've trained the agent into a broken call pattern.

**Guardrail:** Every required flag must be validated, and tests should fail if required semantics diverge from behavior.

### Token-budget prompt sections vs hard item limits

**Decision:** Variable resume prompt sections (memory, recent prompts, events, reasoning) share a fixed token budget filled by priority order, rather than each section having a hard item count limit.

**Why not keep hard limits:** Fixed per-section limits (e.g. 5 memories, 3 events) waste budget when one section is empty and starve it when another is rich. A shared budget lets high-priority sections expand into unused space.

**Guardrail:** When adding a new variable section, append it to the priority chain with `appendBudgetedLine`. Do not introduce a new hard item limit.

## Design principles (standing)

- **Resume is the entry point.** Agents call `resume` to get their focus task, context, and commands. Everything else is secondary.
- **Idempotency everywhere.** Every mutation accepts `--request-id`. Agents retry freely.
- **No human-in-the-loop.** No prompts, no confirmations, no "are you sure?" flows.
- **Machine-first I/O.** All output is JSON. Exit codes are reliable.
- **Append-only truth.** Events are the source of truth. Current state is derived.
