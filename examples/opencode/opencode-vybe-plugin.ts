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
    proc.exited,
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

// Minimal-intrusion OpenCode -> vybe bridge.
// Hooks wired:
// - session.created: hydrate state with vybe resume (project-scoped)
// - todo.updated: append a compact snapshot event (no task mutation yet)
export const VybeBridgePlugin: Plugin = async ({ client }) => {
  const sessionPrompts = new Map<string, string>()
  const sessionProjects = new Map<string, string>()
  const todoTimers = new Map<string, ReturnType<typeof setTimeout>>()
  const todoPending = new Map<string, any[]>()
  const TODO_DEBOUNCE_MS = 3000

  const hydrateSessionPrompt = async (sessionID: string, projectDir?: string) => {
    if (sessionPrompts.size >= 100) {
      const oldest = sessionPrompts.keys().next().value
      if (oldest) sessionPrompts.delete(oldest)
    }
    const agent = stableAgent(sessionID, projectDir)
    const args = ["resume", "--agent", agent, "--request-id", reqID("oc_session_start")]
    if (projectDir && projectDir.trim() !== "") {
      args.push("--project", projectDir)
    }

    const resume = await runVybeJSON(args)
    const prompt = String(resume?.data?.prompt ?? "")
    if (prompt.trim() !== "") {
      sessionPrompts.set(sessionID, prompt)
    }
  }

  return {
    event: async ({ event }) => {
      try {
        if (event.type === "session.created") {
          const session = event.properties.info
          if (session.directory && session.directory.trim() !== "") {
            sessionProjects.set(session.id, session.directory)
          }
          const agent = stableAgent(session.id, session.directory)
          await hydrateSessionPrompt(session.id, session.directory)
          await client.app.log({
            body: {
              level: "info",
              service: "vybe-bridge",
              message: "session.created -> vybe resume",
              extra: { sessionID: session.id, agent },
            },
          })
        }

        if (event.type === "session.deleted") {
          const sessionID = event.properties.info.id
          sessionPrompts.delete(sessionID)
          sessionProjects.delete(sessionID)
          if (todoTimers.has(sessionID)) {
            clearTimeout(todoTimers.get(sessionID))
            todoTimers.delete(sessionID)
          }
          todoPending.delete(sessionID)
        }

        if (event.type === "todo.updated") {
          const sessionID = event.properties.sessionID
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
                const agent = stableAgent(sessionID, sessionProjects.get(sessionID))
                await runVybe([
                  "events",
                  "add",
                  "--agent",
                  agent,
                  "--request-id",
                  reqID("oc_todo_updated"),
                  "--kind",
                  "todo_snapshot",
                  "--msg",
                  `todo.updated (${latestTodos.length} items)`,
                  "--metadata",
                  JSON.stringify({
                    session_id: sessionID,
                    count: latestTodos.length,
                    todos: latestTodos.map((t) => ({ id: t.id, status: t.status, priority: t.priority })),
                  }),
                ])
              } catch (err) {
                await client.app.log({
                  body: {
                    level: "warn",
                    service: "vybe-bridge",
                    message: "vybe bridge todo debounce flush failed",
                    extra: { error: err instanceof Error ? err.message : String(err) },
                  },
                })
              }
            }, TODO_DEBOUNCE_MS),
          )
        }
      } catch (err) {
        await client.app.log({
          body: {
            level: "warn",
            service: "vybe-bridge",
            message: "vybe bridge hook failed",
            extra: { error: err instanceof Error ? err.message : String(err) },
          },
        })
      }
    },

    "chat.message": async (input, output) => {
      try {
        const sessionID = input.sessionID
        const agent = stableAgent(sessionID, sessionProjects.get(sessionID))
        const prompt = extractUserPrompt(output.parts as any[])
        if (!prompt) return

        const truncated = prompt.length > 500 ? prompt.slice(0, 500) : prompt

        await runVybe([
          "events",
          "add",
          "--agent",
          agent,
          "--request-id",
          reqID("oc_user_prompt"),
          "--kind",
          "user_prompt",
          "--msg",
          truncated,
          "--metadata",
          JSON.stringify({
            source: "opencode",
            session_id: sessionID,
          }),
        ])
      } catch (err) {
        await client.app.log({
          body: {
            level: "warn",
            service: "vybe-bridge",
            message: "vybe bridge chat.message failed",
            extra: { error: err instanceof Error ? err.message : String(err) },
          },
        })
      }
    },

    "experimental.chat.system.transform": async (input, output) => {
      const existing = sessionPrompts.get(input.sessionID)
      if (!existing) {
        await hydrateSessionPrompt(input.sessionID, sessionProjects.get(input.sessionID))
      }
      const prompt = sessionPrompts.get(input.sessionID)
      if (!prompt || prompt.trim() === "") {
        return
      }

      output.system.push(`## Vybe Resume Context\n${prompt}`)
    },
  }
}
