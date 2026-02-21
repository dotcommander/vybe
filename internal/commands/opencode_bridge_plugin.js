function reqID(prefix) {
  return prefix + "_" + Date.now() + "_" + Math.random().toString(16).slice(2, 8)
}

const IS_RETRO_CHILD = !!(process.env.VYBE_RETRO_CHILD && process.env.VYBE_RETRO_CHILD.trim() !== "")

function projectKey(projectDir) {
  if (!projectDir || projectDir.trim() === "") return ""
  const base = projectDir.split(/[\\/]/).filter(Boolean).pop() || ""
  if (base === "") return ""
  return base.replace(/[^A-Za-z0-9_-]/g, "-").toLowerCase()
}

function stableAgent(sessionID, projectDir) {
  const envAgent = process.env.VYBE_AGENT
  if (envAgent && envAgent.trim() !== "") return envAgent.trim()
  const pkey = projectKey(projectDir)
  if (pkey !== "") return "opencode-" + pkey
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

// Fire-and-forget: spawn vybe process without awaiting completion.
// Optional env object is merged into process.env for the child.
// Optional stdinPayload is written to child stdin.
function runVybeBackground(args, env, stdinPayload) {
  try {
    var opts = { cmd: ["vybe", ...args], stdout: "ignore", stderr: "ignore" }
    if (env) opts.env = Object.assign({}, process.env, env)
    if (typeof stdinPayload === "string") opts.stdin = new TextEncoder().encode(stdinPayload)
    Bun.spawn(opts)
  } catch (_) {
    // best-effort — don't block caller
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

function truncate(str, max) {
  if (!str || str.length <= max) return str
  return str.slice(0, max)
}

var MUTATING_TOOLS = { Write: 1, Edit: 1, MultiEdit: 1, Bash: 1, NotebookEdit: 1 }

export const VybeBridgePlugin = async ({ client }) => {
  const sessionPrompts = new Map()
  const sessionProjects = new Map()
  const todoTimers = new Map()
  const todoPending = new Map()
  const TODO_DEBOUNCE_MS = 3000

  const log = async (level, message, extra) => {
    try {
      await client.app.log({ body: { service: "vybe-bridge", level, message, extra } })
    } catch (_) {}
  }

  const agentForSession = (sessionID) => {
    return stableAgent(sessionID, sessionProjects.get(sessionID))
  }

  const hydrateSessionPrompt = async (sessionID, projectDir) => {
    if (sessionPrompts.size >= 100) {
      const oldest = sessionPrompts.keys().next().value
      sessionPrompts.delete(oldest)
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
        if (IS_RETRO_CHILD) return

        if (event.type === "session.created") {
          const info = event.properties.info
          if (info.directory && info.directory.trim() !== "") {
            sessionProjects.set(info.id, info.directory)
          }
          const agent = stableAgent(info.id, info.directory)
          await hydrateSessionPrompt(info.id, info.directory)
          await log("info", "session.created -> vybe resume", { sessionID: info.id, agent })
        }

        if (event.type === "session.deleted") {
          const delID = event.properties.info.id
          const projectDir = sessionProjects.get(delID)
          const agent = agentForSession(delID)

          // Fire-and-forget SessionEnd hook (checkpoint gc only; retrospective runs at PreCompact).
          const payload = JSON.stringify({
            session_id: delID,
            hook_event_name: "SessionEnd",
            cwd: projectDir || "",
          })
          runVybeBackground(["hook", "session-end", "--agent", agent], null, payload)

          // Clean up maps
          sessionPrompts.delete(delID)
          sessionProjects.delete(delID)
          if (todoTimers.has(delID)) {
            clearTimeout(todoTimers.get(delID))
            todoTimers.delete(delID)
          }
          todoPending.delete(delID)
        }

        if (event.type === "session.idle") {
          // Property structure varies by OpenCode version; try both known shapes.
          const sessionID = event.properties?.sessionID || event.properties?.info?.id
          if (sessionID) {
            const agent = agentForSession(sessionID)
            await runVybe([
              "events", "add",
              "--agent", agent,
              "--request-id", reqID("oc_idle"),
              "--kind", "heartbeat",
              "--msg", "session_idle",
              "--metadata", JSON.stringify({ source: "opencode", session_id: sessionID }),
            ]).catch(() => {})
          }
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
              const agent = agentForSession(sessionID)
              await runVybe([
                "events", "add",
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
              await log("warn", "vybe bridge todo debounce flush failed", {
                error: err instanceof Error ? err.message : String(err),
              })
            }
          }, TODO_DEBOUNCE_MS))
        }
      } catch (err) {
        await log("warn", "vybe bridge event hook failed", {
          error: err instanceof Error ? err.message : String(err),
        })
      }
    },

    "tool.execute.after": async (input) => {
      try {
        if (IS_RETRO_CHILD) return

        const tool = input.tool
        if (!tool) return

        const sessionID = input.sessionID
        const agent = agentForSession(sessionID)
        const isError = !!input.error

        if (isError) {
          // Log all tool failures
          const msg = truncate(tool + " failed", 500)
          const metadata = JSON.stringify({
            source: "opencode",
            session_id: sessionID,
            tool_name: tool,
            error: truncate(String(input.error || ""), 2048),
            metadata_schema_version: "v1",
          })
          await runVybe([
            "events", "add",
            "--agent", agent,
            "--request-id", reqID("oc_tool_failure"),
            "--kind", "tool_failure",
            "--msg", msg,
            "--metadata", metadata,
          ])
        } else if (MUTATING_TOOLS[tool]) {
          // Only log mutating tool successes
          let msg = tool
          let args = input.args || {}
          if (args.file_path) msg = tool + ": " + args.file_path
          else if (args.notebook_path) msg = tool + ": " + args.notebook_path
          else if (args.command) msg = tool + ": " + truncate(String(args.command), 120)
          msg = truncate(msg, 500)

          const metadata = JSON.stringify({
            source: "opencode",
            session_id: sessionID,
            tool_name: tool,
            metadata_schema_version: "v1",
          })
          await runVybe([
            "events", "add",
            "--agent", agent,
            "--request-id", reqID("oc_tool_success"),
            "--kind", "tool_success",
            "--msg", msg,
            "--metadata", metadata,
          ])
        }
      } catch (err) {
        await log("warn", "vybe bridge tool.execute.after failed", {
          error: err instanceof Error ? err.message : String(err),
        })
      }
    },

    "chat.message": async (input, output) => {
      try {
        if (IS_RETRO_CHILD) return

        const sessionID = input.sessionID
        const agent = agentForSession(sessionID)

        const prompt = extractUserPrompt(output.parts)
        if (!prompt) return

        const truncated = prompt.length > 500 ? prompt.slice(0, 500) : prompt

        await runVybe([
          "events", "add",
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
        await log("warn", "vybe bridge chat.message failed", {
          error: err instanceof Error ? err.message : String(err),
        })
      }
    },

    "experimental.session.compacting": async (input, output) => {
      try {
        if (IS_RETRO_CHILD) return

        const sessionID = input.sessionID
        const agent = agentForSession(sessionID)
        const projectDir = sessionProjects.get(sessionID)

        // Checkpoint (gc + retrospective) via unified hook command.
        var payload = JSON.stringify({
          session_id: sessionID,
          hook_event_name: "PreCompact",
          cwd: projectDir || "",
        })
        runVybeBackground(["hook", "checkpoint", "--agent", agent], null, payload)
      } catch (err) {
        await log("warn", "vybe bridge compacting checkpoint failed", {
          error: err instanceof Error ? err.message : String(err),
        })
      }
    },

    "experimental.chat.system.transform": async (input, output) => {
      if (IS_RETRO_CHILD) return

      const existing = sessionPrompts.get(input.sessionID)
      if (!existing) {
        await hydrateSessionPrompt(input.sessionID, sessionProjects.get(input.sessionID))
      }
      const prompt = sessionPrompts.get(input.sessionID)
      if (!prompt || prompt.trim() === "") {
        return
      }

      output.system.push("## Vybe Resume Context\n" + prompt)
    },
  }
}
