/**
 * Unit tests for pure utility functions from opencode_bridge_plugin.ts.
 *
 * The plugin file does NOT export its utility functions — they are
 * module-scoped because the file is embedded via Go's //go:embed and adding
 * exports would change its contract.  We copy the pure function
 * implementations here verbatim so they can be tested in isolation.
 */

import { describe, test, expect, beforeEach, afterEach } from "bun:test"

// ---------------------------------------------------------------------------
// Copied pure function implementations (no imports from plugin file)
// ---------------------------------------------------------------------------

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

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

describe("projectKey", () => {
  test("returns empty for undefined", () => {
    expect(projectKey(undefined)).toBe("")
  })

  test("returns empty for empty string", () => {
    expect(projectKey("")).toBe("")
  })

  test("returns empty for whitespace-only string", () => {
    expect(projectKey("   ")).toBe("")
  })

  test("extracts basename from Unix path", () => {
    expect(projectKey("/Users/vampire/go/src/vybe")).toBe("vybe")
  })

  test("extracts basename from shallow Unix path", () => {
    expect(projectKey("/home/user/myproject")).toBe("myproject")
  })

  test("extracts basename from Windows path", () => {
    expect(projectKey("C:\\Users\\foo\\bar")).toBe("bar")
  })

  test("sanitizes special characters to hyphens", () => {
    expect(projectKey("my project!")).toBe("my-project-")
  })

  test("lowercases the result", () => {
    expect(projectKey("MyProject")).toBe("myproject")
  })

  test("preserves hyphens and underscores", () => {
    expect(projectKey("/repos/my-cool_project")).toBe("my-cool_project")
  })
})

describe("stableAgent", () => {
  let savedEnv: string | undefined

  beforeEach(() => {
    savedEnv = process.env.VYBE_AGENT
    delete process.env.VYBE_AGENT
  })

  afterEach(() => {
    if (savedEnv !== undefined) {
      process.env.VYBE_AGENT = savedEnv
    } else {
      delete process.env.VYBE_AGENT
    }
  })

  test("VYBE_AGENT env takes priority over everything", () => {
    process.env.VYBE_AGENT = "custom"
    expect(stableAgent("abcdefghij", "/home/user/myproject")).toBe("custom")
  })

  test("VYBE_AGENT env is trimmed", () => {
    process.env.VYBE_AGENT = "  custom-agent  "
    expect(stableAgent()).toBe("custom-agent")
  })

  test("whitespace-only VYBE_AGENT is ignored", () => {
    process.env.VYBE_AGENT = "   "
    expect(stableAgent(undefined, "/home/user/myproject")).toBe("opencode-myproject")
  })

  test("falls back to project dir when no VYBE_AGENT", () => {
    expect(stableAgent(undefined, "/home/user/myproject")).toBe("opencode-myproject")
  })

  test("project dir takes priority over session ID", () => {
    expect(stableAgent("abcdefghij", "/home/user/myproject")).toBe("opencode-myproject")
  })

  test("falls back to session ID prefix (8 chars) when no project dir", () => {
    expect(stableAgent("abcdefghij")).toBe("opencode-abcdefgh")
  })

  test("short session ID (< 8 chars) falls through to default", () => {
    expect(stableAgent("abc")).toBe("opencode-agent")
  })

  test("session ID exactly 8 chars is accepted", () => {
    expect(stableAgent("abcdefgh")).toBe("opencode-abcdefgh")
  })

  test("default fallback with no arguments", () => {
    expect(stableAgent()).toBe("opencode-agent")
  })

  test("default fallback with empty session ID and no project dir", () => {
    expect(stableAgent("", "")).toBe("opencode-agent")
  })
})

describe("extractUserPrompt", () => {
  test("returns empty for non-array input", () => {
    expect(extractUserPrompt(null as any)).toBe("")
    expect(extractUserPrompt(undefined as any)).toBe("")
    expect(extractUserPrompt("string" as any)).toBe("")
    expect(extractUserPrompt({} as any)).toBe("")
  })

  test("returns empty for empty array", () => {
    expect(extractUserPrompt([])).toBe("")
  })

  test("extracts text from a single text part", () => {
    expect(extractUserPrompt([{ type: "text", text: "hello" }])).toBe("hello")
  })

  test("joins multiple text parts with newline", () => {
    expect(extractUserPrompt([
      { type: "text", text: "hello" },
      { type: "text", text: "world" },
    ])).toBe("hello\nworld")
  })

  test("skips ignored parts", () => {
    expect(extractUserPrompt([
      { type: "text", text: "a", ignored: true },
      { type: "text", text: "b" },
    ])).toBe("b")
  })

  test("skips non-text types", () => {
    expect(extractUserPrompt([
      { type: "image", text: "img" },
      { type: "text", text: "ok" },
    ])).toBe("ok")
  })

  test("skips parts with missing type", () => {
    expect(extractUserPrompt([
      { text: "no type" },
      { type: "text", text: "has type" },
    ])).toBe("has type")
  })

  test("skips parts where text is not a string", () => {
    expect(extractUserPrompt([
      { type: "text", text: 42 },
      { type: "text", text: "valid" },
    ])).toBe("valid")
  })

  test("handles null/undefined elements in array gracefully", () => {
    expect(extractUserPrompt([
      null,
      undefined,
      { type: "text", text: "ok" },
    ])).toBe("ok")
  })

  test("trims leading/trailing whitespace from joined result", () => {
    expect(extractUserPrompt([{ type: "text", text: "  hello  " }])).toBe("hello")
  })
})

describe("truncate", () => {
  test("returns empty for undefined", () => {
    expect(truncate(undefined, 10)).toBe("")
  })

  test("returns empty for empty string", () => {
    expect(truncate("", 10)).toBe("")
  })

  test("passthrough when string is within limit", () => {
    expect(truncate("hello", 10)).toBe("hello")
  })

  test("passthrough when string length equals limit", () => {
    expect(truncate("hello", 5)).toBe("hello")
  })

  test("truncates at character boundary", () => {
    expect(truncate("hello world", 5)).toBe("hello")
  })

  test("handles emoji (surrogate pairs) correctly — does not produce broken surrogates", () => {
    // 🌍 is a 4-byte emoji (2 UTF-16 code units / 1 Unicode code point)
    // truncate to 7 chars: "Hello " (6) + 🌍 (1) = "Hello 🌍"
    expect(truncate("Hello 🌍🌎🌏", 7)).toBe("Hello 🌍")
  })

  test("handles CJK characters correctly", () => {
    expect(truncate("你好世界测试", 4)).toBe("你好世界")
  })

  test("handles mixed ASCII and multi-byte characters", () => {
    // "Hi 🎉end" — truncate to 4: "Hi 🎉"
    expect(truncate("Hi 🎉end", 4)).toBe("Hi 🎉")
  })
})

describe("formatAgentProtocol", () => {
  test("returns empty for null", () => {
    expect(formatAgentProtocol(null)).toBe("")
  })

  test("returns empty for undefined", () => {
    expect(formatAgentProtocol(undefined)).toBe("")
  })

  test("returns empty for non-object primitives", () => {
    expect(formatAgentProtocol("string")).toBe("")
    expect(formatAgentProtocol(42)).toBe("")
    expect(formatAgentProtocol(true)).toBe("")
  })

  test("returns empty when resume_command is missing", () => {
    expect(formatAgentProtocol({
      focus_task_field: "task.id",
      terminal_status_command: "vybe task set-status",
    })).toBe("")
  })

  test("returns empty when focus_task_field is missing", () => {
    expect(formatAgentProtocol({
      resume_command: "vybe resume",
      terminal_status_command: "vybe task set-status",
    })).toBe("")
  })

  test("returns empty when terminal_status_command is missing", () => {
    expect(formatAgentProtocol({
      resume_command: "vybe resume",
      focus_task_field: "task.id",
    })).toBe("")
  })

  test("returns formatted output with all required fields", () => {
    const result = formatAgentProtocol({
      resume_command: "vybe resume --agent $AGENT",
      focus_task_field: "brief.task.id",
      terminal_status_command: "vybe task set-status --id $ID --status $STATUS",
    })
    expect(result).toContain("- Resume: `vybe resume --agent $AGENT`")
    expect(result).toContain("- Focus task id field: `brief.task.id`")
    expect(result).toContain("- Terminal status (required once): `vybe task set-status --id $ID --status $STATUS`")
    expect(result).toContain("- Allowed terminal statuses: `completed|blocked`")
  })

  test("uses default statuses when terminal_statuses not provided", () => {
    const result = formatAgentProtocol({
      resume_command: "vybe resume",
      focus_task_field: "task.id",
      terminal_status_command: "vybe task set-status",
    })
    expect(result).toContain("completed|blocked")
  })

  test("uses provided terminal_statuses when given", () => {
    const result = formatAgentProtocol({
      resume_command: "vybe resume",
      focus_task_field: "task.id",
      terminal_status_command: "vybe task set-status",
      terminal_statuses: ["done", "failed", "cancelled"],
    })
    expect(result).toContain("done|failed|cancelled")
    expect(result).not.toContain("completed|blocked")
  })

  test("filters empty strings from terminal_statuses", () => {
    const result = formatAgentProtocol({
      resume_command: "vybe resume",
      focus_task_field: "task.id",
      terminal_status_command: "vybe task set-status",
      terminal_statuses: ["done", "", "  ", "cancelled"],
    })
    // empty and whitespace-only entries are filtered out
    expect(result).toContain("done|cancelled")
  })

  test("falls back to default when terminal_statuses is empty array", () => {
    const result = formatAgentProtocol({
      resume_command: "vybe resume",
      focus_task_field: "task.id",
      terminal_status_command: "vybe task set-status",
      terminal_statuses: [],
    })
    // empty array → statuses join is "" → fallback to "completed|blocked"
    expect(result).toContain("completed|blocked")
  })

  test("includes optional progress command when present", () => {
    const result = formatAgentProtocol({
      resume_command: "vybe resume",
      focus_task_field: "task.id",
      terminal_status_command: "vybe task set-status",
      optional_progress_command: "vybe push --json ...",
    })
    expect(result).toContain("- Optional progress: `vybe push --json ...`")
  })

  test("omits optional progress line when not provided", () => {
    const result = formatAgentProtocol({
      resume_command: "vybe resume",
      focus_task_field: "task.id",
      terminal_status_command: "vybe task set-status",
    })
    expect(result).not.toContain("Optional progress")
  })

  test("includes rule when present", () => {
    const result = formatAgentProtocol({
      resume_command: "vybe resume",
      focus_task_field: "task.id",
      terminal_status_command: "vybe task set-status",
      rule: "Always call resume before starting work",
    })
    expect(result).toContain("- Rule: Always call resume before starting work")
  })

  test("omits rule line when not provided", () => {
    const result = formatAgentProtocol({
      resume_command: "vybe resume",
      focus_task_field: "task.id",
      terminal_status_command: "vybe task set-status",
    })
    expect(result).not.toContain("Rule:")
  })

  test("output lines are joined with newlines", () => {
    const result = formatAgentProtocol({
      resume_command: "vybe resume",
      focus_task_field: "task.id",
      terminal_status_command: "vybe task set-status",
    })
    const lines = result.split("\n")
    expect(lines.length).toBe(4)
    expect(lines[0]).toStartWith("- Resume:")
    expect(lines[1]).toStartWith("- Focus task id field:")
    expect(lines[2]).toStartWith("- Terminal status")
    expect(lines[3]).toStartWith("- Allowed terminal statuses:")
  })
})
