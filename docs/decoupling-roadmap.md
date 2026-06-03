# Vybe Decoupling Roadmap → vybe-as-a-service

## Goal

Decouple vybe from Claude Code so it integrates with any agent harness (Claude Code, OpenCode, Cursor, Windsurf, Zed, Aider, …), with the chosen north star being **vybe-as-a-service**: a local daemon that any harness can feed events to.

## Verified starting position

Vybe's core is already host-agnostic. `internal/store/`, `internal/actions/`, `internal/models/` have **zero** Claude references — a generic SQLite continuity engine (events, tasks, memory, agent cursors). All Claude coupling lives in a thin shell with a symmetric shape:

```
stdin → [host INPUT parser] → CanonicalEvent → core (resume/push) → ContextResult → [host OUTPUT renderer] → stdout
```

Only the two end boxes are coupled. Known coupling points:

- `internal/commands/hook_shared.go` — `hookInput`/`hookOutput` structs = Claude's stdin/stdout JSON schema; `defaultAgentName = "claude"`.
- `internal/commands/hookcmd/claude.go` — writes `~/.claude/settings.json`.
- `internal/commands/hookcmd/manifest.go` — Claude event names: SessionStart, UserPromptSubmit, PostToolUseFailure, PreCompact, SessionEnd, TaskCompleted.
- `internal/commands/hookcmd/claude_adapter.go` — reads `~/.claude/projects/<encoded-cwd>/` transcripts + MEMORY.md. Worst coupling; already carries a removal TODO.

Two integration philosophies already coexist: Claude's **tight** native-hook coupling (vybe parses Claude's stdin schema) vs OpenCode's **loose** bridge (`opencode_bridge_plugin.ts` translates host events → generic `vybe push`/`vybe resume` CLI calls). The bridge pattern is the decoupled future.

## Keystone insight

`vybe serve` is just the canonical event spine exposed over a Unix socket instead of one-shot stdin/stdout. Both transports carry the same `CanonicalEvent`:

```
                    ┌─ one-shot: CLI stdin → host parser ─┐
CanonicalEvent ─────┤                                     ├──▶ core ──▶ ContextResult
                    └─ daemon: socket/HTTP ───────────────┘
```

Therefore Phase 0 (extract `CanonicalEvent`/`ContextResult`) is the keystone for the entire program — serve mode becomes a transport wrapper, not a rewrite.

## Phased plan

| Phase | Moves | Deliverable | Risk |
|---|---|---|---|
| 0 — Keystone | canonical types, neutral identity | `CanonicalEvent`/`ContextResult` types; handlers consume only those; Claude parser/renderer become the default impls; `defaultAgentName` neutralized | None (pure refactor, Claude path stays green) |
| 1 — Decouple | host selector, generic protocol | `--host claude\|generic` selector; vybe-native JSON in/out; documented bridge protocol | Low |
| 2 — Multi-host | verb rename, installer registry | `vybe event` verbs (alias `hook`); `HostInstaller` interface + registry; `vybe init` host auto-detect | Low |
| 3 — Service | serve daemon | `vybe serve` over Unix socket; one-shot CLI auto-proxies to daemon if socket present else inline; lazy-start | Medium |
| 4 — Cleanup | quarantine, cross-host | quarantine/remove `claude_adapter.go` transcript reading; cross-host shared continuity | Low |

## Locked serve-mode decisions (Phase 3)

- **Transport:** Unix domain socket by default (no port, filesystem perms = local-only security); optional `--http localhost:PORT`.
- **DB ownership:** daemon holds one WAL connection + caches and is authoritative; one-shot CLI invocations proxy to the socket if it exists, else open the DB directly. Never two writers with stale caches.
- **Lifecycle:** lazy-start (a bridge spawns the daemon if the socket is absent) — more portable than requiring launchd/systemd.
- **Back-compat is non-negotiable:** `vybe hook session-start` and the existing Claude hook path keep working throughout; serve mode is purely additive.

## Discipline

- Refactor-first: Phase 0 must land as a no-op with a green Claude path before any new host work.
- Don't build a host DSL prematurely — start with a Go interface; extract to data only after 3+ hosts reveal the real shape.
- Keep transcript-reading out of the core; it's the one thing that can't generalize.
