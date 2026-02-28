import type { Plugin } from "@opencode-ai/plugin"

function reqID(prefix: string): string {
  return `${prefix}_${Date.now()}_${Math.random().toString(16).slice(2, 8)}`
}

function projectKey(projectDir?: string): string {
  if (!projectDir || projectDir.trim() === "") return ""
  const base = projectDir.split(/[\\/]/).filter(Boolean).pop() ?? ""
  if (base === "") return ""
  return base.replace(/[^A-Za-z0-9_-]/g, "-").toLowerCase()
}

function stableAgent(sessionID?: string, projectDir?: string): string {
  const envAgent = process.env.VYBE_AGENT
  if (envAgent && envAgent.trim() !== "") {
    return envAgent.trim()
  }
  const pkey = projectKey(projectDir)
  if (pkey !== "") {
    return `opencode-${pkey}`
  }
  if (sessionID && sessionID.length >= 8) {
    return `opencode-${sessionID.slice(0, 8)}`
  }
  return "opencode-agent"
}

async function runVybe(args: string[]): Promise<{ stdout: string; stderr: string }> {
  const proc = Bun.spawn({
    cmd: ["vybe", ...args],
    stdout: "pipe",
    stderr: "pipe",
  })

  const [outBuf, errBuf] = await Promise.all([
    new Response(proc.stdout).text(),
    new Response(proc.stderr).text(),
  ])

  const code = await proc.exited
  if (code !== 0) {
    throw new Error(`vybe failed (${code}): ${errBuf || outBuf}`)
  }

  return { stdout: outBuf, stderr: errBuf }
}

async function runVybeJSON(args: string[]): Promise<any> {
  const out = await runVybe(args)
  try {
    return JSON.parse(out.stdout)
  } catch (e) {
    throw new Error("vybe returned invalid JSON: " + out.stdout.slice(0, 200))
  }
}

// Fire-and-forget: spawn vybe process without awaiting completion.
// Optional env object is merged into process.env for the child.
// Optional stdinPayload is written to child stdin.
function runVybeBackground(args: string[], env?: Record<string, string>, stdinPayload?: string): void {
  try {
    const opts: any = { cmd: ["vybe", ...args], stdout: "ignore", stderr: "ignore" }
    if (env) opts.env = { ...process.env, ...env }
    if (typeof stdinPayload === "string") opts.stdin = new TextEncoder().encode(stdinPayload)
    Bun.spawn(opts)
  } catch (err) {
    if (!spawnFailureLogged) {
      spawnFailureLogged = true
      console.error(`[vybe-bridge] spawn failed: ${err instanceof Error ? err.message : String(err)}`)
    }
  }
}

function runVybePushBackground(agent: string, requestPrefix: string, payloadObj: any): void {
  runVybeBackground([
    "push",
    "--agent", agent,
    "--request-id", reqID(requestPrefix),
    "--json", JSON.stringify(payloadObj),
  ])
}

function extractUserPrompt(parts: any[]): string {
  if (!Array.isArray(parts)) return ""
  const texts: string[] = []
  for (const p of parts) {
    if (!p || p.type !== "text") continue
    if (typeof p.text !== "string") continue
    if (p.ignored === true) continue
    texts.push(p.text)
  }
  return texts.join("\n").trim()
}

function truncate(str: string | undefined, max: number): string {
  if (!str) return ""
  const chars = Array.from(str)
  if (chars.length <= max) return str
  return chars.slice(0, max).join("")
}

/** Touch a Map key to mark it as recently used (moves it to end of iteration order). */
function touchKey<K, V>(map: Map<K, V>, key: K): V | undefined {
  const val = map.get(key)
  if (val !== undefined) {
    map.delete(key)
    map.set(key, val)
  }
  return val
}

/** Evict the least-recently-used entry if map is at capacity. */
function evictLRU<K, V>(map: Map<K, V>, cap: number): void {
  if (map.size < cap) return
  const oldest = map.keys().next().value
  if (oldest !== undefined) map.delete(oldest)
}

/** Evict the least-recently-used timer entry if map is at capacity, clearing the timeout. */
function evictLRUTimer(map: Map<string, ReturnType<typeof setTimeout>>, cap: number): void {
  if (map.size < cap) return
  const oldest = map.keys().next().value
  if (oldest !== undefined) {
    clearTimeout(map.get(oldest))
    map.delete(oldest)
  }
}

function formatAgentProtocol(protocol: any): string {
  if (!protocol || typeof protocol !== "object") return ""

  const resumeCommand = typeof protocol.resume_command === "string" ? protocol.resume_command : ""
  const focusTaskField = typeof protocol.focus_task_field === "string" ? protocol.focus_task_field : ""
  const terminalStatusCommand = typeof protocol.terminal_status_command === "string" ? protocol.terminal_status_command : ""
  const optionalProgressCommand = typeof protocol.optional_progress_command === "string" ? protocol.optional_progress_command : ""
  const rule = typeof protocol.rule === "string" ? protocol.rule : ""

  if (!resumeCommand || !focusTaskField || !terminalStatusCommand) return ""

  const statuses = Array.isArray(protocol.terminal_statuses)
    ? protocol.terminal_statuses.filter((s: any) => typeof s === "string" && s.trim() !== "").join("|")
    : "completed|blocked"

  const lines = [
    `- Resume: \`${resumeCommand}\``,
    `- Focus task id field: \`${focusTaskField}\``,
    `- Terminal status (required once): \`${terminalStatusCommand}\``,
    `- Allowed terminal statuses: \`${statuses || "completed|blocked"}\``,
  ]

  if (optionalProgressCommand) {
    lines.push(`- Optional progress: \`${optionalProgressCommand}\``)
  }
  if (rule) {
    lines.push(`- Rule: ${rule}`)
  }

  return lines.join("\n")
}

let spawnFailureLogged = false

const MUTATING_TOOLS: Record<string, boolean> = {
  Write: true,
  Edit: true,
  MultiEdit: true,
  Bash: true,
  NotebookEdit: true,
}

// Minimal-intrusion OpenCode -> vybe bridge.
// Hooks wired:
// - session.created: hydrate state with vybe resume (project-scoped)
// - session.deleted: session-end hook (checkpoint gc only)
// - session.idle: heartbeat event
// - todo.updated: append a compact snapshot event (debounced 3s)
// - tool.execute.after: log tool failures + mutating tool successes
// - experimental.session.compacting: checkpoint maintenance
// - chat.message: log user prompts
// - experimental.chat.system.transform: inject vybe resume context
export const VybeBridgePlugin: Plugin = async ({ client }) => {
  const sessionPrompts = new Map<string, string>()
  const sessionProjects = new Map<string, string>()
  const todoTimers = new Map<string, ReturnType<typeof setTimeout>>()
  const todoPending = new Map<string, any[]>()
  let cachedAgentProtocolPrompt = ""
  let cachedAgentProtocolAt = 0
  const PROTOCOL_CACHE_TTL_MS = 5 * 60 * 1000 // 5 minutes
  const TODO_DEBOUNCE_MS = 3000

  const log = async (level: string, message: string, extra?: Record<string, any>) => {
    try {
      await client.app.log({ body: { service: "vybe-bridge", level, message, extra } })
    } catch (_) {}
  }

  const logBackground = (level: string, message: string, extra?: Record<string, any>) => {
    void log(level, message, extra)
  }

  const agentForSession = (sessionID: string): string => {
    return stableAgent(sessionID, touchKey(sessionProjects, sessionID))
  }

  const hydrateSessionPrompt = async (sessionID: string, projectDir?: string) => {
    evictLRU(sessionPrompts, 100)
    const agent = stableAgent(sessionID, projectDir)
    const args = ["resume", "--agent", agent, "--request-id", reqID("oc_session_start")]
    if (projectDir && projectDir.trim() !== "") {
      args.push("--project-dir", projectDir)
    }

    const resume = await runVybeJSON(args)
    const prompt = String(resume?.data?.prompt ?? "")
    if (prompt.trim() !== "") {
      sessionPrompts.set(sessionID, prompt)
    }
  }

  const hydrateAgentProtocolPrompt = async () => {
    if (cachedAgentProtocolPrompt !== "" && Date.now() - cachedAgentProtocolAt < PROTOCOL_CACHE_TTL_MS) return
    try {
      const schema = await runVybeJSON(["schema", "commands"])
      const protocolPrompt = formatAgentProtocol(schema?.data?.agent_protocol)
      if (protocolPrompt.trim() !== "") {
        cachedAgentProtocolPrompt = protocolPrompt
        cachedAgentProtocolAt = Date.now()
      }
    } catch (_) {
      // best-effort only
    }
  }

  return {
    event: async ({ event }) => {
      try {
        if (event.type === "session.created") {
          const session = event.properties.info
          if (session.directory && session.directory.trim() !== "") {
            // Evict before insert — guarantees capacity without relying on async eviction in hydrateSessionPrompt.
            evictLRU(sessionProjects, 100)
            sessionProjects.set(session.id, session.directory)
          }
          const agent = stableAgent(session.id, session.directory)
          void hydrateAgentProtocolPrompt()
          logBackground("info", "session.created tracked", { sessionID: session.id, agent })
          void hydrateSessionPrompt(session.id, session.directory)
        }

        if (event.type === "session.deleted") {
          const sessionID = event.properties.info.id
          const projectDir = touchKey(sessionProjects, sessionID)
          const agent = agentForSession(sessionID)

          // Fire-and-forget SessionEnd hook (checkpoint maintenance only).
          const payload = JSON.stringify({
            session_id: sessionID,
            hook_event_name: "SessionEnd",
            cwd: projectDir || "",
          })
          runVybeBackground(["hook", "session-end", "--agent", agent], undefined, payload)

          // Clean up maps
          sessionPrompts.delete(sessionID)
          sessionProjects.delete(sessionID)
          if (todoTimers.has(sessionID)) {
            clearTimeout(todoTimers.get(sessionID))
            todoTimers.delete(sessionID)
          }
          todoPending.delete(sessionID)
        }

        if (event.type === "session.idle") {
          // Property structure varies by OpenCode version; try both known shapes.
          const sessionID = (event.properties as any)?.sessionID || (event.properties as any)?.info?.id
          if (sessionID) {
            const agent = agentForSession(sessionID)
            runVybePushBackground(agent, "oc_idle", {
              event: {
                kind: "heartbeat",
                message: "session_idle",
                metadata: { source: "opencode", session_id: sessionID },
              },
            })
          }
        }

        if (event.type === "todo.updated") {
          const sessionID = event.properties.sessionID
          if (!sessionID) return
          evictLRUTimer(todoTimers, 100)
          evictLRU(todoPending, 100)
          const todos = event.properties.todos || []
          todoPending.set(sessionID, todos)

          if (todoTimers.has(sessionID)) {
            clearTimeout(todoTimers.get(sessionID))
          }

          todoTimers.set(
            sessionID,
            setTimeout(async () => {
              todoTimers.delete(sessionID)
              const latestTodos = todoPending.get(sessionID)
              todoPending.delete(sessionID)
              if (!latestTodos) return

              try {
                const agent = agentForSession(sessionID)
                runVybePushBackground(agent, "oc_todo_updated", {
                  event: {
                    kind: "todo_snapshot",
                    message: `todo.updated (${latestTodos.length} items)`,
                    metadata: {
                      session_id: sessionID,
                      count: latestTodos.length,
                      todos: latestTodos.map((t: any) => ({ id: t.id, status: t.status, priority: t.priority })),
                    },
                  },
                })
              } catch (err) {
                logBackground("warn", "vybe bridge todo debounce flush failed", {
                  error: err instanceof Error ? err.message : String(err),
                })
              }
            }, TODO_DEBOUNCE_MS),
          )
        }
      } catch (err) {
        logBackground("warn", "vybe bridge event hook failed", {
          error: err instanceof Error ? err.message : String(err),
        })
      }
    },

    "tool.execute.after": async (input: any) => {
      try {
        const tool: string = input.tool
        if (!tool) return

        const sessionID: string = input.sessionID
        if (!sessionID) return
        const agent = agentForSession(sessionID)
        const isError = !!input.error

        if (isError) {
          // Log all tool failures
          const msg = `${tool} failed`
          const metadata = {
            source: "opencode",
            session_id: sessionID,
            tool_name: tool,
            error: truncate(String(input.error || ""), 2048),
            metadata_schema_version: "v1",
          }
          runVybePushBackground(agent, "oc_tool_failure", {
            event: { kind: "tool_failure", message: msg, metadata },
          })
        } else if (MUTATING_TOOLS[tool]) {
          // Only log mutating tool successes
          const args = input.args || {}
          let msg = tool
          if (args.file_path) msg = `${tool}: ${args.file_path}`
          else if (args.notebook_path) msg = `${tool}: ${args.notebook_path}`
          else if (args.command) msg = `${tool}: ${truncate(String(args.command), 120)}`
          msg = truncate(msg, 500)

          const metadata = {
            source: "opencode",
            session_id: sessionID,
            tool_name: tool,
            metadata_schema_version: "v1",
          }
          runVybePushBackground(agent, "oc_tool_success", {
            event: { kind: "tool_success", message: msg, metadata },
          })
        }
      } catch (err) {
        logBackground("warn", "vybe bridge tool.execute.after failed", {
          error: err instanceof Error ? err.message : String(err),
        })
      }
    },

    "chat.message": async (input, output) => {
      try {
        const sessionID = input.sessionID
        if (!sessionID) return
        const agent = agentForSession(sessionID)
        const prompt = extractUserPrompt(output.parts as any[])
        if (!prompt) return

        const truncated = truncate(prompt, 500)

        runVybePushBackground(agent, "oc_user_prompt", {
          event: {
            kind: "user_prompt",
            message: truncated,
            metadata: { source: "opencode", session_id: sessionID },
          },
        })
      } catch (err) {
        logBackground("warn", "vybe bridge chat.message failed", {
          error: err instanceof Error ? err.message : String(err),
        })
      }
    },

    "experimental.session.compacting": async (input: any) => {
      try {
        const sessionID: string = input.sessionID
        const agent = agentForSession(sessionID)
        const projectDir = touchKey(sessionProjects, sessionID)

        // Checkpoint maintenance via unified hook command.
        const payload = JSON.stringify({
          session_id: sessionID,
          hook_event_name: "PreCompact",
          cwd: projectDir || "",
        })
        runVybeBackground(["hook", "checkpoint", "--agent", agent], undefined, payload)
      } catch (err) {
        logBackground("warn", "vybe bridge compacting checkpoint failed", {
          error: err instanceof Error ? err.message : String(err),
        })
      }
    },

    "experimental.chat.system.transform": async (input, output) => {
      if (!input.sessionID) return
      let prompt = touchKey(sessionPrompts, input.sessionID)
      if (!prompt || prompt.trim() === "") {
        try {
          await hydrateSessionPrompt(input.sessionID, touchKey(sessionProjects, input.sessionID))
        } catch (_) {
          // If immediate hydration fails, skip prompt injection for this turn.
        }
        prompt = touchKey(sessionPrompts, input.sessionID)
      }

      if (!cachedAgentProtocolPrompt || Date.now() - cachedAgentProtocolAt >= PROTOCOL_CACHE_TTL_MS) {
        try {
          await hydrateAgentProtocolPrompt()
        } catch (_) {
          // best-effort only
        }
      }

      if (!prompt || prompt.trim() === "") {
        return
      }

      if (cachedAgentProtocolPrompt) {
        output.system.push(`## Vybe Agent Protocol\n${cachedAgentProtocolPrompt}`)
      }
      output.system.push(`## Vybe Resume Context\n${prompt}`)
    },
  }
}
