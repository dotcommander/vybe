import type { Plugin } from "@opencode-ai/plugin"

function reqID(prefix: string): string {
  return `${prefix}_${Date.now()}_${Math.random().toString(16).slice(2, 8)}`
}

function stableAgent(sessionID?: string): string {
  const envAgent = process.env.VIBE_AGENT
  if (envAgent && envAgent.trim() !== "") {
    return envAgent.trim()
  }
  if (sessionID && sessionID.length >= 8) {
    return `opencode-${sessionID.slice(0, 8)}`
  }
  return "opencode-agent"
}

async function runVibe(args: string[]): Promise<{ stdout: string; stderr: string }> {
  const proc = Bun.spawn({
    cmd: ["vibe", ...args],
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
    throw new Error(`vibe failed (${code}): ${errBuf || outBuf}`)
  }

  return { stdout: outBuf, stderr: errBuf }
}

async function runVibeJSON(args: string[]): Promise<any> {
  const out = await runVibe(args)
  try {
    return JSON.parse(out.stdout)
  } catch (e) {
    throw new Error("vibe returned invalid JSON: " + out.stdout.slice(0, 200))
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

// Minimal-intrusion OpenCode -> vibe bridge.
// Hooks wired:
// - session.created: hydrate state with vibe resume (project-scoped)
// - todo.updated: append a compact snapshot event (no task mutation yet)
export const VibeBridgePlugin: Plugin = async ({ client }) => {
  const sessionPrompts = new Map<string, string>()

  const hydrateSessionPrompt = async (sessionID: string, projectDir?: string) => {
    if (sessionPrompts.size >= 100) {
      const oldest = sessionPrompts.keys().next().value
      if (oldest) sessionPrompts.delete(oldest)
    }
    const agent = stableAgent(sessionID)
    const args = ["resume", "--agent", agent, "--request-id", reqID("oc_session_start")]
    if (projectDir && projectDir.trim() !== "") {
      args.push("--project", projectDir)
    }

    const resume = await runVibeJSON(args)
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
          const agent = stableAgent(session.id)
          await hydrateSessionPrompt(session.id, session.directory)
          await client.app.log({
            body: {
              level: "info",
              service: "vibe-bridge",
              message: "session.created -> vibe resume",
              extra: { sessionID: session.id, agent },
            },
          })
        }

        if (event.type === "session.deleted") {
          sessionPrompts.delete(event.properties.info.id)
        }

        if (event.type === "todo.updated") {
          const todos = event.properties.todos || []
          const agent = stableAgent(event.properties.sessionID)
          const requestID = reqID("oc_todo_updated")

          const summary = {
            session_id: event.properties.sessionID,
            count: todos.length,
            todos: todos.map((t) => ({
              id: t.id,
              status: t.status,
              priority: t.priority,
            })),
          }

          await runVibe([
            "log",
            "--agent",
            agent,
            "--request-id",
            requestID,
            "--kind",
            "todo_snapshot",
            "--msg",
            `todo.updated (${summary.count} items)`,
            "--metadata",
            JSON.stringify(summary),
          ])
        }
      } catch (err) {
        await client.app.log({
          body: {
            level: "warn",
            service: "vibe-bridge",
            message: "vibe bridge hook failed",
            extra: { error: err instanceof Error ? err.message : String(err) },
          },
        })
      }
    },

    "chat.message": async (input, output) => {
      try {
        const sessionID = input.sessionID
        const agent = stableAgent(sessionID)
        const prompt = extractUserPrompt(output.parts as any[])
        if (!prompt) return

        const truncated = prompt.length > 500 ? prompt.slice(0, 500) : prompt

        await runVibe([
          "log",
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
            service: "vibe-bridge",
            message: "vibe bridge chat.message failed",
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

      output.system.push(`## Vibe Resume Context\n${prompt}`)
    },
  }
}
