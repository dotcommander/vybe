function reqID(prefix) {
  return prefix + "_" + Date.now() + "_" + Math.random().toString(16).slice(2, 8)
}

function stableAgent(sessionID) {
  const envAgent = process.env.VYBE_AGENT
  if (envAgent && envAgent.trim() !== "") return envAgent.trim()
  if (sessionID && sessionID.length >= 8) return "opencode-" + sessionID.slice(0, 8)
  return "opencode-agent"
}

async function runVybe(args) {
  const proc = Bun.spawn({ cmd: ["vybe", ...args], stdout: "pipe", stderr: "pipe" })
  const [stdout, stderr, code] = await Promise.all([
    new Response(proc.stdout).text(),
    new Response(proc.stderr).text(),
    proc.exited,
  ])
  if (code !== 0) throw new Error((stderr || stdout || "vybe failed").trim())
  return { stdout, stderr }
}

async function runVybeJSON(args) {
  const out = await runVybe(args)
  try {
    return JSON.parse(out.stdout)
  } catch (e) {
    throw new Error("vybe returned invalid JSON: " + out.stdout.slice(0, 200))
  }
}

function extractUserPrompt(parts) {
  if (!Array.isArray(parts)) return ""
  const texts = []
  for (const p of parts) {
    if (!p || p.type !== "text") continue
    if (typeof p.text !== "string") continue
    if (p.ignored === true) continue
    texts.push(p.text)
  }
  return texts.join("\n").trim()
}

export const VybeBridgePlugin = async ({ client }) => {
  const sessionPrompts = new Map()
  const todoTimers = new Map()
  const todoPending = new Map()
  const TODO_DEBOUNCE_MS = 3000

  const hydrateSessionPrompt = async (sessionID, projectDir) => {
    if (sessionPrompts.size >= 100) {
      const oldest = sessionPrompts.keys().next().value
      sessionPrompts.delete(oldest)
    }
    const agent = stableAgent(sessionID)
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
          const info = event.properties.info
          const agent = stableAgent(info.id)
          await hydrateSessionPrompt(info.id, info.directory)
          await client.app.log({
            body: {
              service: "vybe-bridge",
              level: "info",
              message: "session.created -> vybe resume",
              extra: { sessionID: info.id, agent },
            },
          })
        }

        if (event.type === "session.deleted") {
          const delID = event.properties.info.id
          sessionPrompts.delete(delID)
          if (todoTimers.has(delID)) {
            clearTimeout(todoTimers.get(delID))
            todoTimers.delete(delID)
          }
          todoPending.delete(delID)
        }

        if (event.type === "todo.updated") {
          const sessionID = event.properties.sessionID
          const todos = event.properties.todos || []
          todoPending.set(sessionID, todos)

          if (todoTimers.has(sessionID)) clearTimeout(todoTimers.get(sessionID))
          todoTimers.set(sessionID, setTimeout(async () => {
            todoTimers.delete(sessionID)
            const latestTodos = todoPending.get(sessionID)
            todoPending.delete(sessionID)
            if (!latestTodos) return
            try {
              const agent = stableAgent(sessionID)
              await runVybe([
                "log",
                "--agent", agent,
                "--request-id", reqID("oc_todo_updated"),
                "--kind", "todo_snapshot",
                "--msg", "todo.updated (" + latestTodos.length + " items)",
                "--metadata", JSON.stringify({
                  session_id: sessionID,
                  count: latestTodos.length,
                  todos: latestTodos.map((t) => ({ id: t.id, status: t.status, priority: t.priority })),
                }),
              ])
            } catch (err) {
              await client.app.log({
                body: {
                  service: "vybe-bridge",
                  level: "warn",
                  message: "vybe bridge todo debounce flush failed",
                  extra: { error: err instanceof Error ? err.message : String(err) },
                },
              })
            }
          }, TODO_DEBOUNCE_MS))
        }
      } catch (err) {
        await client.app.log({
          body: {
            service: "vybe-bridge",
            level: "warn",
            message: "vybe bridge hook failed",
            extra: { error: err instanceof Error ? err.message : String(err) },
          },
        })
      }
    },

    "chat.message": async (input, output) => {
      try {
        const sessionID = input.sessionID
        const agent = stableAgent(sessionID)

        const prompt = extractUserPrompt(output.parts)
        if (!prompt) return

        const truncated = prompt.length > 500 ? prompt.slice(0, 500) : prompt

        await runVybe([
          "log",
          "--agent", agent,
          "--request-id", reqID("oc_user_prompt"),
          "--kind", "user_prompt",
          "--msg", truncated,
          "--metadata", JSON.stringify({
            source: "opencode",
            session_id: sessionID,
          }),
        ])
      } catch (err) {
        await client.app.log({
          body: {
            service: "vybe-bridge",
            level: "warn",
            message: "vybe bridge chat.message failed",
            extra: { error: err instanceof Error ? err.message : String(err) },
          },
        })
      }
    },

    "experimental.chat.system.transform": async (input, output) => {
      const existing = sessionPrompts.get(input.sessionID)
      if (!existing) {
        await hydrateSessionPrompt(input.sessionID)
      }
      const prompt = sessionPrompts.get(input.sessionID)
      if (!prompt || prompt.trim() === "") {
        return
      }

      output.system.push("## Vybe Resume Context\n" + prompt)
    },
  }
}
