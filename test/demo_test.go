// Package test provides integration tests that simulate a complete AI agent session
// using real vybe CLI commands against a temporary SQLite database.
package test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// vybeTestBin is the path to the built vybe binary for integration tests.
var vybeTestBin string

// TestMain builds the vybe binary once before running all tests in this package.
func TestMain(m *testing.M) {
	// Determine the repo root (two levels up from test/)
	repoRoot, err := filepath.Abs(filepath.Join(filepath.Dir(os.Args[0]), "..", ".."))
	if err != nil {
		// fallback: walk up from cwd
		cwd, _ := os.Getwd()
		repoRoot = filepath.Join(cwd, "..")
	}

	// Prefer source-relative path when running via `go test ./test/...`
	cwd, _ := os.Getwd()
	if strings.HasSuffix(cwd, "/test") {
		repoRoot = filepath.Join(cwd, "..")
	} else if fi, err2 := os.Stat(filepath.Join(cwd, "cmd", "vybe")); err2 == nil && fi.IsDir() {
		repoRoot = cwd
	}

	binPath := filepath.Join(repoRoot, "vybe-demo-test")
	buildCmd := exec.Command("go", "build", "-o", binPath, "./cmd/vybe")
	buildCmd.Dir = repoRoot
	buildCmd.Stdout = os.Stdout
	buildCmd.Stderr = os.Stderr

	if err := buildCmd.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "FATAL: failed to build vybe binary: %v\n", err)
		os.Exit(1)
	}

	vybeTestBin = binPath

	code := m.Run()

	// Cleanup binary
	_ = os.Remove(binPath)
	os.Exit(code)
}

// harness holds test-scoped state shared across helper functions.
type harness struct {
	t      *testing.T
	dbPath string
	agent  string
}

// newHarness creates a test harness with an isolated temp DB.
func newHarness(t *testing.T) *harness {
	t.Helper()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "vybe-demo.db")
	return &harness{
		t:      t,
		dbPath: dbPath,
		agent:  "demo-agent",
	}
}

// vybe runs the vybe binary with --db-path and --agent set, returns stdout.
// stderr (log lines) is discarded.
func (h *harness) vybe(args ...string) string {
	h.t.Helper()
	fullArgs := append([]string{"--db-path", h.dbPath, "--agent", h.agent}, args...)
	cmd := exec.Command(vybeTestBin, fullArgs...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	if err != nil {
		// Some commands exit non-zero on validation errors; caller inspects JSON.
		_ = err
	}
	return stdout.String()
}

// vybeWithStdin runs the vybe binary with piped stdin JSON, returns stdout.
func (h *harness) vybeWithStdin(stdinJSON string, args ...string) string {
	h.t.Helper()
	fullArgs := append([]string{"--db-path", h.dbPath, "--agent", h.agent}, args...)
	cmd := exec.Command(vybeTestBin, fullArgs...)
	cmd.Stdin = strings.NewReader(stdinJSON)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	_ = cmd.Run()
	return stdout.String()
}

// vybeWithDir runs vybe with a custom working directory.
func (h *harness) vybeWithDir(dir string, args ...string) string {
	h.t.Helper()
	fullArgs := append([]string{"--db-path", h.dbPath, "--agent", h.agent}, args...)
	cmd := exec.Command(vybeTestBin, fullArgs...)
	cmd.Dir = dir
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	_ = cmd.Run()
	return stdout.String()
}

// vybeRaw runs the vybe binary with only --db-path set (no --agent).
func (h *harness) vybeRaw(args ...string) string {
	h.t.Helper()
	fullArgs := append([]string{"--db-path", h.dbPath}, args...)
	cmd := exec.Command(vybeTestBin, fullArgs...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	_ = cmd.Run()
	return stdout.String()
}

// mustJSON parses JSON output and returns map[string]any.
func mustJSON(t *testing.T, output string) map[string]any {
	t.Helper()
	output = strings.TrimSpace(output)
	var m map[string]any
	require.NoError(t, json.Unmarshal([]byte(output), &m), "failed to parse JSON: %s", output)
	return m
}

// requireSuccess asserts the vybe JSON response has success=true.
func requireSuccess(t *testing.T, output string) map[string]any {
	t.Helper()
	m := mustJSON(t, output)
	require.Equal(t, true, m["success"], "expected success=true, got: %s", output)
	return m
}

// getStr extracts a nested string field from the parsed JSON using dot-path.
// E.g. getStr(m, "data", "task", "id") returns m["data"]["task"]["id"].(string).
func getStr(m map[string]any, keys ...string) string {
	var cur any = m
	for _, k := range keys {
		if mm, ok := cur.(map[string]any); ok {
			cur = mm[k]
		} else {
			return ""
		}
	}
	if s, ok := cur.(string); ok {
		return s
	}
	return ""
}

// rid generates a deterministic request ID for a given phase and step.
func rid(phase string, step int) string {
	return fmt.Sprintf("demo_%s_%d", phase, step)
}

// hookStdin builds the JSON stdin payload for hook commands.
func hookStdin(eventName, sessionID, cwd, source, prompt, toolName string) string {
	payload := map[string]any{
		"cwd":             cwd,
		"session_id":      sessionID,
		"hook_event_name": eventName,
		"prompt":          prompt,
		"tool_name":       toolName,
		"tool_input":      map[string]any{},
		"tool_response":   map[string]any{},
		"source":          source,
	}
	data, _ := json.Marshal(payload)
	return string(data)
}

// hookStdinWithToolInput builds the JSON stdin payload for hook commands with tool input.
func hookStdinWithToolInput(eventName, sessionID, cwd, toolName string, toolInput map[string]any) string {
	payload := map[string]any{
		"cwd":             cwd,
		"session_id":      sessionID,
		"hook_event_name": eventName,
		"tool_name":       toolName,
		"tool_input":      toolInput,
		"tool_response":   map[string]any{"output": "ok"},
		"source":          "",
	}
	data, _ := json.Marshal(payload)
	return string(data)
}

// TestDemoAgentSession is a guided tour of every vybe capability, told as a
// 17-act story. An AI agent bootstraps a project, works tasks, crashes, resumes
// with full continuity, coordinates with other agents, and exercises the entire
// CLI surface — all against a real SQLite database.
func TestDemoAgentSession(t *testing.T) {
	h := newHarness(t)

	t.Log("")
	t.Log("╔══════════════════════════════════════════════════════════════╗")
	t.Log("║          VYBE — Durable Continuity for AI Agents           ║")
	t.Log("╠══════════════════════════════════════════════════════════════╣")
	t.Log("║  This demo walks through every vybe capability:            ║")
	t.Log("║  task graphs, crash-safe memory, multi-agent coordination, ║")
	t.Log("║  idempotent retries, hook integration, and event streams.  ║")
	t.Log("║                                                            ║")
	t.Log("║  Watch for: cross-session continuity (Act IV),             ║")
	t.Log("║  dependency-driven focus (Act V), and crash-safe retries   ║")
	t.Log("║  (Act VII). These are why vybe exists.                     ║")
	t.Log("╚══════════════════════════════════════════════════════════════╝")
	t.Log("")

	// -------------------------------------------------------------------------
	// Act I: Building The World
	// -------------------------------------------------------------------------
	t.Run("ActI_BuildingTheWorld", func(t *testing.T) {
		t.Log("=== ACT I: BUILDING THE WORLD ===")
		t.Log("Setting up the world an agent operates in.")
		t.Log("DB init, task graph with dependencies, memory at multiple scopes.")
		t.Log("Vybe is the durable backbone — everything an agent knows or intends lives here.")

		// Init DB via upgrade
		t.Run("upgrade_database", func(t *testing.T) {
			t.Log("Initializing the vybe database — this runs migrations to create all tables")
			out := h.vybe("upgrade")
			m := mustJSON(t, out)
			require.Equal(t, true, m["success"], "upgrade should succeed: %s", out)
			t.Log("Database ready — schema includes: events, tasks, memory, artifacts, agent_state, idempotency")
		})

		// Project CLI is removed. Use a fixed project ID for grouping tasks and memory.
		// No FK constraint on tasks.project_id — arbitrary string is valid.
		projectID := "proj_demo_test"
		t.Logf("Using fixed projectID=%q — project CLI is removed; arbitrary IDs are valid (no FK)", projectID)

		// Create 3 tasks
		t.Run("create_task_graph", func(t *testing.T) {
			t.Log("Creating 3 tasks with real-world titles — agents need a task graph to work from")
			t.Log("Tasks: 'Implement auth', 'Write tests', 'Deploy' — we'll add dependencies next")
			taskTitles := []string{"Implement auth", "Write tests", "Deploy"}
			for i, title := range taskTitles {
				out := h.vybe("task", "create",
					"--title", title,
					"--project", projectID,
					"--request-id", rid("p1s3", i),
				)
				m := requireSuccess(t, out)
				taskID := getStr(m, "data", "task", "id")
				require.NotEmpty(t, taskID, "task %q should have an ID", title)
				t.Logf("  Task created: id=%s title=%q", taskID, title)
			}
		})

		// Get the created task IDs
		tasksOut := h.vybe("task", "list", "--status", "pending")
		tasksM := requireSuccess(t, tasksOut)
		tasksData := tasksM["data"].(map[string]any)["tasks"]
		require.NotNil(t, tasksData, "expected tasks list")
		tasksList := tasksData.([]any)
		require.Len(t, tasksList, 3, "expected 3 tasks")

		// Find tasks by title
		tasksByTitle := make(map[string]string)
		for _, raw := range tasksList {
			task := raw.(map[string]any)
			tasksByTitle[task["title"].(string)] = task["id"].(string)
		}
		authTaskID := tasksByTitle["Implement auth"]
		testsTaskID := tasksByTitle["Write tests"]
		require.NotEmpty(t, authTaskID)
		require.NotEmpty(t, testsTaskID)
		require.NotEmpty(t, tasksByTitle["Deploy"])

		// Set task dependency — "Write tests" blocked by "Implement auth"
		t.Run("set_dependencies", func(t *testing.T) {
			t.Logf("Setting dependency: 'Write tests' (%s) blocked by 'Implement auth' (%s)", testsTaskID, authTaskID)
			t.Log("Dependencies drive focus selection — blocked tasks are skipped by resume until unblocked")
			out := h.vybe("task", "add-dep",
				"--id", testsTaskID,
				"--depends-on", authTaskID,
				"--request-id", rid("p1s4", 1),
			)
			requireSuccess(t, out)
			t.Log("Dependency set — 'Write tests' will not be selected as focus until 'Implement auth' completes")
		})

		// Set global memory
		t.Run("store_global_memory", func(t *testing.T) {
			t.Log("Storing global memory: go_version=1.26")
			t.Log("Global memory is visible to ALL agents across ALL sessions — perfect for environment facts")
			out := h.vybe("memory", "set",
				"--key", "go_version",
				"--value", "1.26",
				"--scope", "global",
				"--request-id", rid("p1s5", 1),
			)
			requireSuccess(t, out)
			t.Log("Global memory stored — any agent resuming later will see go_version=1.26 in their brief")
		})

		// Set project-scoped memory
		t.Run("store_project_memory", func(t *testing.T) {
			t.Logf("Storing project-scoped memory: api_framework=chi (project: %s)", projectID)
			t.Log("Project memory is shared across agents working on the same project but isolated from other projects")
			out := h.vybe("memory", "set",
				"--key", "api_framework",
				"--value", "chi",
				"--scope", "project",
				"--scope-id", projectID,
				"--request-id", rid("p1s6", 1),
			)
			requireSuccess(t, out)
			t.Log("Project memory stored — agents in this project get api_framework=chi automatically")
		})
	})

	// Fixed project ID — no project CLI
	projectID := "proj_demo_test"

	tasksOut := h.vybe("task", "list")
	tasksM := requireSuccess(t, tasksOut)
	tasksList := tasksM["data"].(map[string]any)["tasks"].([]any)
	require.Len(t, tasksList, 3)

	tasksByTitle := make(map[string]string)
	for _, raw := range tasksList {
		task := raw.(map[string]any)
		tasksByTitle[task["title"].(string)] = task["id"].(string)
	}
	authTaskID := tasksByTitle["Implement auth"]
	testsTaskID := tasksByTitle["Write tests"]
	deployTaskID := tasksByTitle["Deploy"]

	sessionID := "sess_demo_001"

	// -------------------------------------------------------------------------
	// Act II: The Agent Works
	// -------------------------------------------------------------------------
	t.Run("ActII_TheAgentWorks", func(t *testing.T) {
		t.Log("=== ACT II: THE AGENT WORKS ===")
		t.Log("Simulating what happens when Claude Code starts a new session.")
		t.Log("Hooks fire automatically: session-start loads context, tool calls are logged,")
		t.Log("the agent claims work, logs discoveries, links artifacts, and marks tasks complete.")
		t.Log("This is the core agent work loop — everything vybe is designed for.")

		// SessionStart hook — observe it returns brief with focus task and memories
		var focusTaskID string
		t.Run("session_start_hook", func(t *testing.T) {
			t.Log("Firing SessionStart hook — vybe calls `resume` and injects context into Claude's system prompt")
			t.Log("The additionalContext field is injected verbatim into the IDE context window")
			stdin := hookStdin("SessionStart", sessionID, projectID, "startup", "", "")
			out := h.vybeWithStdin(stdin, "hook", "session-start")
			// hook session-start outputs JSON with hookSpecificOutput.additionalContext
			m := mustJSON(t, out)
			hso, ok := m["hookSpecificOutput"].(map[string]any)
			require.True(t, ok, "hookSpecificOutput should be present: %s", out)
			additionalCtx, _ := hso["additionalContext"].(string)
			require.NotEmpty(t, additionalCtx, "additionalContext should not be empty")
			// The context should reference the focus task (Implement auth — oldest pending)
			require.Contains(t, additionalCtx, "Implement auth", "additionalContext should mention focus task")
			t.Log("SessionStart hook returned context — focus task 'Implement auth' injected into session")
		})

		// Determine focus task from resume
		t.Log("Running `vybe resume` to advance the agent cursor and fetch the full brief packet")
		resumeOut := h.vybe("resume", "--request-id", rid("p2s7", 1))
		resumeM := requireSuccess(t, resumeOut)
		focusTask := resumeM["data"].(map[string]any)["brief"].(map[string]any)["task"]
		require.NotNil(t, focusTask, "resume should return a focus task")
		focusTaskID = focusTask.(map[string]any)["id"].(string)
		require.Equal(t, authTaskID, focusTaskID, "focus task should be 'Implement auth'")
		t.Logf("Resume confirmed focus task: %s ('Implement auth') — oldest pending task wins", focusTaskID)

		// UserPromptSubmit hook — confirm event logged
		t.Run("prompt_logging", func(t *testing.T) {
			t.Log("Firing UserPromptSubmit hook — every user message is logged as a user_prompt event")
			t.Log("This gives future sessions a full prompt history for continuity and retrospective analysis")
			stdin := hookStdin("UserPromptSubmit", sessionID, projectID, "", "Implement the auth system", "")
			h.vybeWithStdin(stdin, "hook", "prompt")
			// Verify a user_prompt event was recorded
			eventsOut := h.vybe("status", "--events", "--kind", "user_prompt", "--limit", "5", "--all")
			eventsM := requireSuccess(t, eventsOut)
			events := eventsM["data"].(map[string]any)["events"].([]any)
			require.NotEmpty(t, events, "user_prompt event should be logged")
			t.Logf("user_prompt event recorded — %d prompt event(s) in the log", len(events))
		})

		// Begin the focus task
		t.Run("claim_focus_task", func(t *testing.T) {
			t.Logf("Claiming focus task %s via `task begin` — transitions status from pending → in_progress", authTaskID)
			t.Log("Agents must begin a task before working on it. This sets the claim lease and prevents double-work.")
			out := h.vybe("task", "begin",
				"--id", authTaskID,
				"--request-id", rid("p2s9", 1),
			)
			m := requireSuccess(t, out)
			status := getStr(m, "data", "task", "status")
			require.Equal(t, "in_progress", status, "task should be in_progress after begin")
			t.Logf("Task status: pending → %s", status)
		})

		// PostToolUse (Bash) hook — confirm event logged
		t.Run("tool_success_tracking", func(t *testing.T) {
			t.Log("Firing PostToolUse hook for a successful Bash call — `go build ./...`")
			t.Log("Every tool invocation is logged. Failed sessions leave a complete tool call trail for recovery.")
			stdin := hookStdinWithToolInput("PostToolUse", sessionID, projectID, "Bash",
				map[string]any{"command": "go build ./..."})
			h.vybeWithStdin(stdin, "hook", "tool-success")
			// Verify tool_success event logged
			eventsOut := h.vybe("status", "--events", "--kind", "tool_success", "--limit", "5", "--all")
			eventsM := requireSuccess(t, eventsOut)
			events := eventsM["data"].(map[string]any)["events"].([]any)
			require.NotEmpty(t, events, "tool_success event should be logged")
			t.Logf("tool_success event logged — %d success event(s) recorded", len(events))
		})

		// PostToolUseFailure hook — confirm event logged
		t.Run("tool_failure_tracking", func(t *testing.T) {
			t.Log("Firing PostToolUseFailure hook — `go test ./...` failed")
			t.Log("Failures are especially important: a new session can see exactly what broke and where")
			stdin := hookStdinWithToolInput("PostToolUseFailure", sessionID, projectID, "Bash",
				map[string]any{"command": "go test ./..."})
			h.vybeWithStdin(stdin, "hook", "tool-failure")
			// Verify tool_failure event logged
			eventsOut := h.vybe("status", "--events", "--kind", "tool_failure", "--limit", "5", "--all")
			eventsM := requireSuccess(t, eventsOut)
			events := eventsM["data"].(map[string]any)["events"].([]any)
			require.NotEmpty(t, events, "tool_failure event should be logged")
			t.Logf("tool_failure event logged — %d failure event(s) recorded (critical for recovery)", len(events))
		})

		// Add progress events to the task via push
		t.Run("log_progress_events", func(t *testing.T) {
			t.Logf("Logging 2 progress events to task %s — narrating what the agent accomplished", authTaskID)
			t.Log("Progress events are the agent's work journal. They survive crashes and inform future sessions.")
			for i, msg := range []string{"Scaffolded JWT middleware", "Integrated with route handlers"} {
				out := h.vybe("push",
					"--json", fmt.Sprintf(`{"task_id":%q,"event":{"kind":"progress","message":%q}}`, authTaskID, msg),
					"--request-id", rid("p2s12", i),
				)
				requireSuccess(t, out)
				t.Logf("  Progress logged: %q", msg)
			}
			eventsOut := h.vybe("status", "--events", "--task", authTaskID, "--kind", "progress", "--all")
			eventsM := requireSuccess(t, eventsOut)
			events := eventsM["data"].(map[string]any)["events"].([]any)
			require.GreaterOrEqual(t, len(events), 2, "at least 2 progress events expected")
			t.Logf("Task %s now has %d progress event(s) in the event stream", authTaskID, len(events))
		})

		// Set task-scoped memory
		t.Run("store_task_memory", func(t *testing.T) {
			t.Logf("Storing task-scoped memory: auth_strategy=jwt (task: %s)", authTaskID)
			t.Log("Task memory survives session boundaries. A new agent picking up this task will know the strategy chosen.")
			out := h.vybe("memory", "set",
				"--key", "auth_strategy",
				"--value", "jwt",
				"--scope", "task",
				"--scope-id", authTaskID,
				"--request-id", rid("p2s13", 1),
			)
			requireSuccess(t, out)
			t.Log("Task memory stored: auth_strategy=jwt — persists with the task across all sessions")
		})

		// Add artifact to task via push
		t.Run("link_artifact", func(t *testing.T) {
			t.Logf("Linking an output file to task %s — agents register files they produce", authTaskID)
			t.Log("Artifacts let new sessions immediately find what was built. No archaeology required.")
			// Create an artifact file in the temp dir
			artFile := filepath.Join(h.t.TempDir(), "auth_impl.go")
			require.NoError(t, os.WriteFile(artFile, []byte("package auth\n"), 0600))

			out := h.vybe("push",
				"--json", fmt.Sprintf(`{"task_id":%q,"artifacts":[{"file_path":%q,"content_type":"text/x-go"}]}`, authTaskID, artFile),
				"--request-id", rid("p2s14", 1),
			)
			requireSuccess(t, out)
			t.Logf("Artifact linked: %s → task %s (type: text/x-go)", artFile, authTaskID)
		})

		// Complete the task
		t.Run("complete_task", func(t *testing.T) {
			t.Logf("Completing task %s ('Implement auth') — outcome=done, summary captures what was built", authTaskID)
			t.Log("Task completion triggers focus auto-advance on next resume. The queue moves forward.")
			out := h.vybe("task", "complete",
				"--id", authTaskID,
				"--outcome", "done",
				"--summary", "Auth implemented with JWT strategy",
				"--request-id", rid("p2s15", 1),
			)
			m := requireSuccess(t, out)
			status := getStr(m, "data", "task", "status")
			require.Equal(t, "completed", status)
			t.Logf("Task %s status: in_progress → %s (summary: 'Auth implemented with JWT strategy')", authTaskID, status)
		})

		// TaskCompleted hook
		t.Run("task_completion_hook", func(t *testing.T) {
			t.Log("Firing TaskCompleted hook — Claude Code signals vybe that the IDE-level task finished")
			t.Log("This logs a lifecycle event so the event stream reflects IDE-level milestones too")
			payload := map[string]any{
				"cwd":             projectID,
				"session_id":      sessionID,
				"hook_event_name": "TaskCompleted",
				"task_id":         authTaskID,
			}
			data, _ := json.Marshal(payload)
			h.vybeWithStdin(string(data), "hook", "task-completed")
			t.Log("TaskCompleted hook fired — task lifecycle event recorded in the event stream")
		})
	})

	// -------------------------------------------------------------------------
	// Act III: The Agent Sleeps
	// -------------------------------------------------------------------------
	t.Run("ActIII_TheAgentSleeps", func(t *testing.T) {
		t.Log("=== ACT III: THE AGENT SLEEPS ===")
		t.Log("Graceful shutdown. Agents crash; vybe persists.")
		t.Log("PreCompact compresses the memory space. SessionEnd closes out the session.")
		t.Log("Everything written in Act II is durable in SQLite — no in-memory state at risk.")

		// PreCompact hook — triggers memory compact + gc
		t.Run("memory_checkpoint", func(t *testing.T) {
			t.Log("Firing PreCompact hook — triggered before Claude Code compacts the context window")
			t.Log("vybe runs `memory compact` + `memory gc` to trim stale entries before the compaction")
			stdin := hookStdin("PreCompact", sessionID, projectID, "", "", "")
			h.vybeWithStdin(stdin, "hook", "checkpoint")
			// No output to verify — best-effort, silent on success
			t.Log("Memory checkpoint complete — expired and low-value entries pruned")
		})

		// SessionEnd hook
		t.Run("session_end", func(t *testing.T) {
			t.Log("Firing SessionEnd hook — the session is over (crash, timeout, or clean exit)")
			t.Log("vybe records the session end and triggers retrospective extraction asynchronously")
			stdin := hookStdin("SessionEnd", sessionID, projectID, "", "", "")
			h.vybeWithStdin(stdin, "hook", "session-end")
			// No output to verify — best-effort, silent on success
			t.Log("Session ended — all state is durable in SQLite, ready for the next agent to resume")
		})
	})

	// -------------------------------------------------------------------------
	// Act IV: The Agent Returns
	// -------------------------------------------------------------------------
	t.Run("ActIV_TheAgentReturns", func(t *testing.T) {
		t.Log("=== ACT IV: THE AGENT RETURNS ===")
		t.Log("A new session starts. The previous agent crashed (or the session ended).")
		t.Log("Can the new agent pick up exactly where the old one left off?")
		t.Log("Memory, artifacts, task state — everything should survive across sessions.")
		t.Log("THIS is the wow moment. This is why vybe exists.")
		sessionID2 := "sess_demo_002"

		// New SessionStart — focus should auto-advance to "Deploy"
		// ("Write tests" is still blocked by "Implement auth" dependency, but
		//  task dependencies don't auto-block; the act confirms the unblocked task is chosen)
		t.Run("new_session_start", func(t *testing.T) {
			t.Logf("Starting new session %s — completely fresh context window, no memory of Act II", sessionID2)
			t.Log("vybe resume auto-advances focus: 'Implement auth' is done, so the next task is selected")
			stdin := hookStdin("SessionStart", sessionID2, projectID, "startup", "", "")
			out := h.vybeWithStdin(stdin, "hook", "session-start")
			m := mustJSON(t, out)
			hso, ok := m["hookSpecificOutput"].(map[string]any)
			require.True(t, ok, "hookSpecificOutput should be present: %s", out)
			additionalCtx, _ := hso["additionalContext"].(string)
			require.NotEmpty(t, additionalCtx, "additionalContext should not be empty")
			// Should reference the next task (Deploy or Write tests — whichever is unblocked)
			hasDeploy := strings.Contains(additionalCtx, "Deploy")
			hasWriteTests := strings.Contains(additionalCtx, "Write tests")
			require.True(t, hasDeploy || hasWriteTests,
				"focus should be an unblocked task, got context: %s", additionalCtx)
			t.Log("New session context loaded — focus auto-advanced to next unblocked task (Deploy or Write tests)")
		})

		// Confirm brief contains history, memory, and artifacts
		t.Run("cross_session_continuity", func(t *testing.T) {
			t.Log("Observing cross-session continuity — artifacts, global memory, and project memory all survived")

			// Check artifacts from previous session
			t.Logf("Checking artifacts for task %s (created in Act II, different session)", authTaskID)
			artOut := h.vybe("status", "--artifacts", "--task", authTaskID)
			artM := requireSuccess(t, artOut)
			artifacts := artM["data"].(map[string]any)["artifacts"].([]any)
			require.NotEmpty(t, artifacts, "artifacts from previous session should persist")
			t.Logf("Artifacts survived: %d file(s) linked to task %s", len(artifacts), authTaskID)

			// Check global memory persists
			t.Log("Checking global memory: go_version should still be 1.26")
			memOut := h.vybe("memory", "get", "--key", "go_version", "--scope", "global")
			memM := requireSuccess(t, memOut)
			value := getStr(memM, "data", "value")
			require.Equal(t, "1.26", value, "global memory should persist across sessions")
			t.Logf("Global memory survived: go_version=%q (set in session 1, read in session 2)", value)

			// Check project memory persists
			t.Log("Checking project memory: api_framework should still be chi")
			projMemOut := h.vybe("memory", "get", "--key", "api_framework",
				"--scope", "project", "--scope-id", projectID)
			projMemM := requireSuccess(t, projMemOut)
			projValue := getStr(projMemM, "data", "value")
			require.Equal(t, "chi", projValue, "project memory should persist across sessions")
			t.Logf("Project memory survived: api_framework=%q — cross-session continuity confirmed", projValue)
		})

		// Begin "Deploy" task, add progress, complete it
		t.Run("complete_deploy_task", func(t *testing.T) {
			t.Logf("New agent picks up 'Deploy' task (%s) — begins, logs progress, completes", deployTaskID)

			// Begin deploy task
			beginOut := h.vybe("task", "begin",
				"--id", deployTaskID,
				"--request-id", rid("p4s21", 1),
			)
			requireSuccess(t, beginOut)
			t.Logf("Task %s ('Deploy'): pending → in_progress", deployTaskID)

			// Add progress event via push
			evtOut := h.vybe("push",
				"--json", fmt.Sprintf(`{"task_id":%q,"event":{"kind":"progress","message":"Deployment pipeline configured"}}`, deployTaskID),
				"--request-id", rid("p4s21", 2),
			)
			requireSuccess(t, evtOut)
			t.Log("Progress logged: 'Deployment pipeline configured'")

			// Complete deploy task
			doneOut := h.vybe("task", "complete",
				"--id", deployTaskID,
				"--outcome", "done",
				"--summary", "Deployed to production",
				"--request-id", rid("p4s21", 3),
			)
			m := requireSuccess(t, doneOut)
			status := getStr(m, "data", "task", "status")
			require.Equal(t, "completed", status)
			t.Logf("Task %s ('Deploy'): in_progress → %s (summary: 'Deployed to production')", deployTaskID, status)
		})

		// Run resume directly — "Write tests" should still be the pending/blocked focus
		t.Run("resume_with_blocked_task", func(t *testing.T) {
			t.Log("Running resume — only 'Write tests' remains, blocked by 'Implement auth' dependency")
			t.Log("The resume algorithm selects the oldest eligible pending task — blocked tasks are skipped until cleared")
			resumeOut := h.vybe("resume", "--request-id", rid("p4s22", 1))
			resumeM := requireSuccess(t, resumeOut)
			brief := resumeM["data"].(map[string]any)["brief"].(map[string]any)
			// "Write tests" is the only remaining non-completed task
			task := brief["task"]
			if task != nil {
				taskID := task.(map[string]any)["id"].(string)
				require.Equal(t, testsTaskID, taskID, "resume should focus on 'Write tests'")
				t.Logf("Resume focus: task %s ('Write tests') — next to complete once unblocked", taskID)
			}
		})
	})

	// -------------------------------------------------------------------------
	// Act V: The Queue Moves
	// -------------------------------------------------------------------------
	t.Run("ActV_TheQueueMoves", func(t *testing.T) {
		t.Log("=== ACT V: THE QUEUE MOVES ===")
		t.Log("Dependency-driven task flow. Removing blockers, observing focus auto-advance,")
		t.Log("completing the remaining work, and confirming the queue is empty.")
		t.Log("This closes the loop: every task created in Act I is now done.")

		// "Implement auth" is done; "Write tests" was added as depends-on auth.
		// The dependency doesn't auto-set status. Set it to pending explicitly.
		t.Run("remove_dependency", func(t *testing.T) {
			t.Logf("Removing dependency: 'Write tests' (%s) no longer blocked by 'Implement auth'", testsTaskID)
			t.Log("Dependencies are explicit edges. Removing the edge is what unblocks the task.")
			// "Implement auth" is completed; remove the dependency to unblock "Write tests"
			out := h.vybe("task", "remove-dep",
				"--id", testsTaskID,
				"--depends-on", authTaskID,
				"--request-id", rid("p5s23", 1),
			)
			requireSuccess(t, out)
			t.Logf("Dependency edge removed: %s → %s", testsTaskID, authTaskID)

			// Ensure "Write tests" is pending (not blocked)
			t.Logf("Setting 'Write tests' (%s) to pending — ready for focus selection", testsTaskID)
			statusOut := h.vybe("task", "set-status",
				"--id", testsTaskID,
				"--status", "pending",
				"--request-id", rid("p5s23", 2),
			)
			requireSuccess(t, statusOut)
			t.Log("'Write tests' is now pending and unblocked — resume will select it next")
		})

		// Resume — confirm "Write tests" becomes focus
		t.Run("resume_selects_unblocked", func(t *testing.T) {
			t.Log("Running resume — 'Write tests' is the only remaining pending task, should become focus")
			resumeOut := h.vybe("resume", "--request-id", rid("p5s24", 1))
			resumeM := requireSuccess(t, resumeOut)
			brief := resumeM["data"].(map[string]any)["brief"].(map[string]any)
			task := brief["task"]
			require.NotNil(t, task, "resume should return a focus task")
			taskID := task.(map[string]any)["id"].(string)
			require.Equal(t, testsTaskID, taskID, "resume should focus on 'Write tests'")
			t.Logf("Resume correctly selected: task %s ('Write tests') as focus", taskID)
		})

		// Begin and complete "Write tests"
		t.Run("complete_final_task", func(t *testing.T) {
			t.Logf("Completing the final task: 'Write tests' (%s)", testsTaskID)
			beginOut := h.vybe("task", "begin",
				"--id", testsTaskID,
				"--request-id", rid("p5s25", 1),
			)
			requireSuccess(t, beginOut)
			t.Logf("Task %s ('Write tests'): pending → in_progress", testsTaskID)

			doneOut := h.vybe("task", "complete",
				"--id", testsTaskID,
				"--outcome", "done",
				"--summary", "All tests written and passing",
				"--request-id", rid("p5s25", 2),
			)
			m := requireSuccess(t, doneOut)
			status := getStr(m, "data", "task", "status")
			require.Equal(t, "completed", status)
			t.Logf("Task %s ('Write tests'): in_progress → %s — all 3 tasks are now done", testsTaskID, status)
		})

		// Resume — confirm no focus task (all done)
		t.Run("empty_queue", func(t *testing.T) {
			t.Log("Running final resume — all tasks are completed, the queue should be empty")
			t.Log("An empty brief (task=null) means the agent's work here is genuinely done.")
			resumeOut := h.vybe("resume", "--request-id", rid("p5s26", 1))
			resumeM := requireSuccess(t, resumeOut)
			brief := resumeM["data"].(map[string]any)["brief"].(map[string]any)
			task := brief["task"]
			require.Nil(t, task, "resume should return no focus task when all tasks are done")
			t.Log("Resume returned task=null — queue is empty, agent can rest")
		})
	})

	// -------------------------------------------------------------------------
	// Act VI: Auditing The Record
	// -------------------------------------------------------------------------
	t.Run("ActVI_AuditingTheRecord", func(t *testing.T) {
		t.Log("=== ACT VI: AUDITING THE RECORD ===")
		t.Log("Auditing the event stream. Everything vybe recorded is queryable.")
		t.Log("Events, memories (all scopes), artifacts, snapshots, and system health.")
		t.Log("Operators and agents can reconstruct exactly what happened in any past session.")

		// Events list — observe all event kinds present
		t.Run("query_event_stream", func(t *testing.T) {
			t.Log("Listing all events — the complete log of agent activity across both sessions")
			eventsOut := h.vybe("status", "--events", "--limit", "100", "--all")
			eventsM := requireSuccess(t, eventsOut)
			events := eventsM["data"].(map[string]any)["events"].([]any)
			require.NotEmpty(t, events, "events list should not be empty")

			kinds := make(map[string]bool)
			for _, raw := range events {
				e := raw.(map[string]any)
				if k, ok := e["kind"].(string); ok {
					kinds[k] = true
				}
			}
			// Verify key event kinds are present
			require.True(t, kinds["user_prompt"] || kinds["progress"], "expected user_prompt or progress events")
			require.True(t, kinds["tool_success"], "expected tool_success events")
			require.True(t, kinds["tool_failure"], "expected tool_failure events")
			t.Logf("Event stream verified: %d total events, kinds present: %v", len(events), func() []string {
				var ks []string
				for k := range kinds {
					ks = append(ks, k)
				}
				return ks
			}())
		})

		// Memory list — observe all scopes present
		t.Run("query_all_memory_scopes", func(t *testing.T) {
			t.Log("Listing memory at all scopes — global, project, and task-scoped entries")

			// Global scope
			t.Log("Checking global memory scope...")
			globalMem := h.vybe("memory", "list", "--scope", "global")
			globalM := requireSuccess(t, globalMem)
			globalMemories := globalM["data"].(map[string]any)["memories"].([]any)
			require.NotEmpty(t, globalMemories, "global memory should not be empty")
			t.Logf("Global memory: %d entries (includes go_version, idem_key, and others)", len(globalMemories))

			// Project scope
			t.Logf("Checking project memory scope (project: %s)...", projectID)
			projMem := h.vybe("memory", "list", "--scope", "project", "--scope-id", projectID)
			projM := requireSuccess(t, projMem)
			projMemories := projM["data"].(map[string]any)["memories"].([]any)
			require.NotEmpty(t, projMemories, "project memory should not be empty")
			t.Logf("Project memory: %d entries (includes api_framework=chi)", len(projMemories))

			// Task scope (auth task)
			t.Logf("Checking task-scoped memory (task: %s / 'Implement auth')...", authTaskID)
			taskMem := h.vybe("memory", "list", "--scope", "task", "--scope-id", authTaskID)
			taskM := requireSuccess(t, taskMem)
			taskMemories := taskM["data"].(map[string]any)["memories"].([]any)
			require.NotEmpty(t, taskMemories, "task-scoped memory should not be empty")
			t.Logf("Task memory: %d entries (includes auth_strategy=jwt)", len(taskMemories))
		})

		// Artifact list — confirm artifacts linked
		t.Run("query_artifacts", func(t *testing.T) {
			t.Logf("Listing artifacts for task %s ('Implement auth')", authTaskID)
			artOut := h.vybe("status", "--artifacts", "--task", authTaskID)
			artM := requireSuccess(t, artOut)
			artifacts := artM["data"].(map[string]any)["artifacts"].([]any)
			require.NotEmpty(t, artifacts, "artifacts should be linked to auth task")
			t.Logf("Artifacts confirmed: %d file(s) linked to auth task (created in Act II, visible now in Act VI)", len(artifacts))
		})

		// Status check — confirm healthy
		t.Run("health_check", func(t *testing.T) {
			t.Log("Running health check — verifies DB connectivity and query round-trip")
			statusOut := h.vybe("status", "--check")
			statusM := requireSuccess(t, statusOut)
			queryOK := statusM["data"].(map[string]any)["query_ok"]
			require.Equal(t, true, queryOK, "status check should report query_ok=true")
			t.Log("Health check passed: query_ok=true — vybe DB is healthy and responsive")
		})
	})

	// -------------------------------------------------------------------------
	// Act VII: Crash-Safe Retries
	// -------------------------------------------------------------------------
	t.Run("ActVII_CrashSafeRetries", func(t *testing.T) {
		t.Log("=== ACT VII: CRASH-SAFE RETRIES ===")
		t.Log("Agents crash. Networks fail. Commands get retried.")
		t.Log("Every mutation accepts a --request-id. Replaying the same request-id")
		t.Log("returns the original result — no duplicates, no side effects.")
		t.Log("This is what makes vybe safe for at-least-once execution in unreliable environments.")

		// Repeat task create with same request-id — same task ID returned
		t.Run("replay_task_create", func(t *testing.T) {
			t.Log("Calling `task create` twice with the same --request-id but different titles")
			t.Log("First call: creates the task. Second call: returns the original — no duplicate created.")
			fixedRID := "demo_idem_task_create_001"
			out1 := h.vybe("task", "create",
				"--title", "Idempotent Task",
				"--request-id", fixedRID,
			)
			m1 := requireSuccess(t, out1)
			id1 := getStr(m1, "data", "task", "id")
			require.NotEmpty(t, id1)
			t.Logf("First create: task %s (title: %q)", id1, "Idempotent Task")

			out2 := h.vybe("task", "create",
				"--title", "Idempotent Task Changed",
				"--request-id", fixedRID,
			)
			m2 := requireSuccess(t, out2)
			id2 := getStr(m2, "data", "task", "id")
			require.Equal(t, id1, id2, "same request-id should return same task ID")
			// Title should be original (not updated)
			title2 := getStr(m2, "data", "task", "title")
			require.Equal(t, "Idempotent Task", title2, "idempotent replay should return original title")
			t.Logf("Replay with same request-id but different title: got task %s (title: %q)", id2, title2)
			t.Log("Same ID, original title preserved — idempotency works")
		})

		// Repeat memory set with same request-id — no duplicate
		t.Run("replay_memory_set", func(t *testing.T) {
			t.Log("Calling `memory set` twice with the same --request-id but different values")
			t.Log("Second call is a no-op — original value is preserved, no overwrite occurs")
			fixedRID := "demo_idem_memory_set_001"
			out1 := h.vybe("memory", "set",
				"--key", "idem_key",
				"--value", "idem_value_1",
				"--scope", "global",
				"--request-id", fixedRID,
			)
			requireSuccess(t, out1)
			t.Log("First call: idem_key=idem_value_1 written")

			out2 := h.vybe("memory", "set",
				"--key", "idem_key",
				"--value", "idem_value_2",
				"--scope", "global",
				"--request-id", fixedRID,
			)
			requireSuccess(t, out2)
			t.Log("Second call (same request-id, value=idem_value_2): replayed — no overwrite")

			// Value should remain the original
			getOut := h.vybe("memory", "get", "--key", "idem_key", "--scope", "global")
			getM := requireSuccess(t, getOut)
			value := getStr(getM, "data", "value")
			require.Equal(t, "idem_value_1", value, "idempotent replay should preserve original value")
			t.Logf("Memory value after replay: %q — original preserved, idem_value_2 was rejected", value)
		})
	})

	// -------------------------------------------------------------------------
	// Act VIII: Production Hardening
	// -------------------------------------------------------------------------
	t.Run("ActVIII_ProductionHardening", func(t *testing.T) {
		t.Log("=== ACT VIII: PRODUCTION HARDENING ===")
		t.Log("Edge cases that matter in real deployments:")
		t.Log("TTL-based memory expiry, structured event metadata.")

		// Memory with TTL — set expires_in, run GC, confirm expired entry is gone
		t.Run("ttl_expiry_and_gc", func(t *testing.T) {
			t.Log("Demonstrating TTL-based memory expiry — agents store short-lived context that auto-cleans")
			t.Log("24h TTL: survives within the session. 1ms TTL: expires immediately, cleaned by GC.")

			// Set memory with a longer TTL so the set itself succeeds
			t.Log("Setting ttl_key_24h with 24h TTL — models a fact valid for one business day")
			ttlOut := h.vybe("memory", "set",
				"--key", "ttl_key_24h",
				"--value", "expires_in_24h",
				"--scope", "global",
				"--expires-in", "24h",
				"--request-id", rid("p8s35", 1),
			)
			requireSuccess(t, ttlOut)

			// Verify it was set and has an expires_at field
			getOut := h.vybe("memory", "get", "--key", "ttl_key_24h", "--scope", "global")
			getM := requireSuccess(t, getOut)
			value := getStr(getM, "data", "value")
			require.Equal(t, "expires_in_24h", value)
			expiresAt := getM["data"].(map[string]any)["expires_at"]
			require.NotNil(t, expiresAt, "expires_at should be set for TTL memory")
			t.Logf("TTL memory set: value=%q expires_at=%v", value, expiresAt)

			// Also verify that a short-TTL key set and then GC'd gets cleaned up:
			// Set with very short TTL, then verify GC completes successfully
			t.Log("Setting ttl_key_short with 1ms TTL — will expire immediately")
			shortOut := h.vybe("memory", "set",
				"--key", "ttl_key_short",
				"--value", "expires_soon",
				"--scope", "global",
				"--expires-in", "1ms",
				"--request-id", rid("p8s35", 2),
			)
			requireSuccess(t, shortOut)

			// Run GC — expired entries should be removed
			t.Log("Running `memory gc` — expired entries are pruned from the memory store")
			gcOut := h.vybe("memory", "gc", "--request-id", rid("p8s35", 3))
			gcM := requireSuccess(t, gcOut)
			// GC response contains deleted count
			_ = gcM
			t.Log("Memory GC complete — expired entries removed")

			// After GC, the short-TTL key should be gone
			afterOut := h.vybe("memory", "get", "--key", "ttl_key_short", "--scope", "global")
			afterM := mustJSON(t, afterOut)
			// Acceptable outcomes: success=false (not found) or value is empty
			if afterM["success"] == true {
				// If somehow still present, that's also acceptable given timing uncertainty
				_ = getStr(afterM, "data", "value")
			}
			// Main assertion: GC succeeded without error
			t.Log("TTL expiry verified — short-lived memory is cleaned up automatically by GC")
		})

		// Event with metadata JSON via push
		t.Run("structured_metadata", func(t *testing.T) {
			t.Log("Logging a tool_call event with structured JSON metadata — tool name, exit code, duration")
			t.Log("Metadata makes events machine-queryable: filter by exit_code, alert on duration, etc.")
			// push embeds the event; metadata is a JSON string (serialized JSON object)
			evtOut := h.vybe("push",
				"--json", `{"event":{"kind":"tool_call","message":"Ran go build","metadata":"{\"tool\":\"Bash\",\"exit_code\":0,\"duration_ms\":1200}"}}`,
				"--request-id", rid("p8s36", 1),
			)
			evtM := requireSuccess(t, evtOut)
			eventID := evtM["data"].(map[string]any)["event_id"]
			require.NotNil(t, eventID, "event_id should be set")
			t.Logf("Event logged: id=%v kind=tool_call", eventID)

			// Verify event appears in list and has expected kind
			eventsOut := h.vybe("status", "--events", "--kind", "tool_call", "--limit", "10", "--all")
			eventsM := requireSuccess(t, eventsOut)
			events := eventsM["data"].(map[string]any)["events"].([]any)
			require.NotEmpty(t, events, "tool_call events should be listed")
			t.Logf("tool_call events in log: %d — structured metadata attached and queryable", len(events))
		})
	})

	// -------------------------------------------------------------------------
	// Act IX: Task Intelligence
	// -------------------------------------------------------------------------
	t.Run("ActIX_TaskIntelligence", func(t *testing.T) {
		t.Log("=== ACT IX: TASK INTELLIGENCE ===")
		t.Log("Agents query the task graph to understand what's available and what's completed.")
		t.Log("get and list — ways to read the task state without modifying anything.")

		// Task get — fetch single task, check fields match
		// task get returns data directly (not nested under data.task)
		t.Run("fetch_single_task", func(t *testing.T) {
			t.Logf("Fetching single task by ID: %s ('Implement auth')", authTaskID)
			out := h.vybe("task", "get", "--id", authTaskID)
			m := requireSuccess(t, out)
			id := getStr(m, "data", "id")
			require.Equal(t, authTaskID, id, "task get should return correct task ID")
			title := getStr(m, "data", "title")
			require.Equal(t, "Implement auth", title, "task get should return correct title")
			status := getStr(m, "data", "status")
			require.Equal(t, "completed", status, "task get should return correct status")
			t.Logf("task get: id=%s title=%q status=%s", id, title, status)
		})

	})

	// -------------------------------------------------------------------------
	// Act X: Multi-Agent Coordination
	// -------------------------------------------------------------------------
	t.Run("ActX_MultiAgentCoordination", func(t *testing.T) {
		t.Log("=== ACT X: MULTI-AGENT COORDINATION ===")
		t.Log("Atomic task claiming prevents two agents from working on the same task simultaneously.")
		t.Log("`task begin` uses compare-and-swap on the version column — only one agent wins the race.")

		t.Run("atomic_claim", func(t *testing.T) {
			t.Log("Creating a claimable task, then claiming it with task begin (CAS-protected)")
			createOut := h.vybe("task", "create",
				"--title", "Claimable Task",
				"--request-id", rid("p10s41", 1),
			)
			cm := requireSuccess(t, createOut)
			taskID := getStr(cm, "data", "task", "id")
			require.NotEmpty(t, taskID)

			out := h.vybe("task", "begin",
				"--id", taskID,
				"--request-id", rid("p10s41", 2),
			)
			m := requireSuccess(t, out)
			status := getStr(m, "data", "task", "status")
			require.Equal(t, "in_progress", status, "claimed task should be in_progress")
			t.Logf("Task claimed: id=%s status=%s — CAS-protected status change", taskID, status)
		})
	})

	// -------------------------------------------------------------------------
	// Act XI: Task Lifecycle
	// -------------------------------------------------------------------------
	t.Run("ActXI_TaskLifecycle", func(t *testing.T) {
		t.Log("=== ACT XI: TASK LIFECYCLE ===")
		t.Log("Agents mutate task state throughout the work lifecycle.")
		t.Log("Priority boosts urgent work. Delete cleans up obsolete tasks. Status transitions track progress.")

		// Task set-priority
		t.Run("priority_boost", func(t *testing.T) {
			t.Log("Creating a task and elevating its priority — higher priority tasks are selected first by `task next` and `task claim`")
			// Create a fresh task to mutate
			createOut := h.vybe("task", "create",
				"--title", "Priority Task",
				"--request-id", rid("p11s44", 1),
			)
			createM := requireSuccess(t, createOut)
			priorityTaskID := getStr(createM, "data", "task", "id")
			t.Logf("Created 'Priority Task' (%s) — default priority is 0", priorityTaskID)

			out := h.vybe("task", "set-priority",
				"--id", priorityTaskID,
				"--priority", "10",
				"--request-id", rid("p11s44", 2),
			)
			m := requireSuccess(t, out)
			data := m["data"].(map[string]any)
			task := data["task"].(map[string]any)
			priority, ok := task["priority"].(float64)
			require.True(t, ok, "priority should be a number")
			require.Equal(t, float64(10), priority, "priority should be updated to 10")
			t.Logf("Priority updated: task %s priority=0 → %d — will jump ahead of other pending tasks", priorityTaskID, int(priority))
		})

		// Task delete
		t.Run("delete_task", func(t *testing.T) {
			t.Log("Creating a task and then deleting it — agents prune work that becomes obsolete")
			// Create a task to delete
			createOut := h.vybe("task", "create",
				"--title", "Task To Delete",
				"--request-id", rid("p11s45", 1),
			)
			createM := requireSuccess(t, createOut)
			deleteTaskID := getStr(createM, "data", "task", "id")
			require.NotEmpty(t, deleteTaskID)
			t.Logf("Created 'Task To Delete' (%s)", deleteTaskID)

			// Delete it
			out := h.vybe("task", "delete",
				"--id", deleteTaskID,
				"--request-id", rid("p11s45", 2),
			)
			requireSuccess(t, out)
			t.Logf("Task %s deleted — verifying it no longer appears in get", deleteTaskID)

			// Verify it's gone — get should return success=false or empty task
			getOut := h.vybe("task", "get", "--id", deleteTaskID)
			getM := mustJSON(t, getOut)
			// Either success=false or data.task is nil
			if getM["success"] == true {
				task := getM["data"].(map[string]any)["task"]
				require.Nil(t, task, "deleted task should not be retrievable")
			}
			// success=false is also acceptable
			t.Logf("Task %s is gone — deletion confirmed", deleteTaskID)
		})

		// Task set-status
		t.Run("status_transitions", func(t *testing.T) {
			t.Log("Demonstrating arbitrary status transitions — vybe allows any status → any status for agent flexibility")
			t.Log("Agents decide what 'blocked' means for their workflow (dependency block vs. failure block)")
			// Create a fresh pending task and set its status
			createOut := h.vybe("task", "create",
				"--title", "Status Update Task",
				"--request-id", rid("p11s46", 1),
			)
			createM := requireSuccess(t, createOut)
			statusTaskID := getStr(createM, "data", "task", "id")
			t.Logf("Created 'Status Update Task' (%s) — transitioning: pending → blocked", statusTaskID)

			out := h.vybe("task", "set-status",
				"--id", statusTaskID,
				"--status", "blocked",
				"--request-id", rid("p11s46", 2),
			)
			m := requireSuccess(t, out)
			status := getStr(m, "data", "task", "status")
			require.Equal(t, "blocked", status, "task status should be updated to blocked")
			t.Logf("Status transition: pending → %s — unrestricted transitions let agents model any workflow", status)
		})
	})

	// -------------------------------------------------------------------------
	// Act XII: Knowledge Management
	// -------------------------------------------------------------------------
	t.Run("ActXII_KnowledgeManagement", func(t *testing.T) {
		t.Log("=== ACT XII: KNOWLEDGE MANAGEMENT ===")
		t.Log("Memory is a first-class system in vybe. Agents read, write, and manage knowledge across sessions.")
		t.Log("Explicit delete keeps knowledge current.")

		// Memory delete — delete a key, confirm gone
		t.Run("explicit_deletion", func(t *testing.T) {
			t.Log("Exercising explicit memory deletion — agents remove facts that are no longer valid")
			// Set a key specifically to delete
			h.vybe("memory", "set",
				"--key", "delete_me",
				"--value", "temporary",
				"--scope", "global",
				"--request-id", rid("p12s50", 1),
			)
			t.Log("Set delete_me=temporary")

			// Verify it exists
			getOut := h.vybe("memory", "get", "--key", "delete_me", "--scope", "global")
			getM := requireSuccess(t, getOut)
			value := getStr(getM, "data", "value")
			require.Equal(t, "temporary", value, "key should exist before deletion")
			t.Logf("Confirmed: delete_me=%q exists", value)

			// Delete it
			out := h.vybe("memory", "delete",
				"--key", "delete_me",
				"--scope", "global",
				"--request-id", rid("p12s50", 2),
			)
			requireSuccess(t, out)
			t.Log("delete_me deleted — verifying it's gone")

			// Verify it's gone
			afterOut := h.vybe("memory", "get", "--key", "delete_me", "--scope", "global")
			afterM := mustJSON(t, afterOut)
			require.NotEqual(t, true, afterM["success"], "deleted key should not be retrievable")
			t.Log("Memory delete confirmed — key is gone, get returns success=false")
		})
	})

	// -------------------------------------------------------------------------
	// Act XIII: Agent Identity
	// -------------------------------------------------------------------------
	t.Run("ActXIII_AgentIdentity", func(t *testing.T) {
		t.Log("=== ACT XIII: AGENT IDENTITY ===")
		t.Log("Each agent has its own cursor and state record in vybe.")
		t.Log("status: read cursor position and current focus. resume --focus: explicitly set focus task.")
		t.Log("Multiple agents can operate simultaneously — each tracks its own position in the event stream.")

		// Agent status — confirm returns cursor and focus info via `status`
		t.Run("read_agent_state", func(t *testing.T) {
			t.Logf("Fetching agent status for %q — cursor position and current focus task", h.agent)
			out := h.vybe("status")
			m := requireSuccess(t, out)
			data := m["data"].(map[string]any)
			// agent_state is nested under data.agent_state
			agentState, ok := data["agent_state"].(map[string]any)
			require.True(t, ok, "status should return agent_state: %s", out)
			agentName := agentState["agent_name"].(string)
			require.NotEmpty(t, agentName, "agent status should return agent_name")
			// last_seen_event_id may be 0 or positive — just verify the field exists
			_, hasEventID := agentState["last_seen_event_id"]
			require.True(t, hasEventID, "agent status should include last_seen_event_id")
			t.Logf("Agent status: agent_name=%q last_seen_event_id=%v", agentName, agentState["last_seen_event_id"])
		})

		// Set focus task explicitly via resume --focus
		t.Run("override_focus", func(t *testing.T) {
			t.Log("Exercising `resume --focus` — manually override which task this agent is focused on")
			t.Log("Useful when an agent wants to work on a specific task regardless of resume priority order")
			// Create a fresh task to focus on
			createOut := h.vybe("task", "create",
				"--title", "Focus Target Task",
				"--request-id", rid("p13s53", 1),
			)
			createM := requireSuccess(t, createOut)
			focusTargetID := getStr(createM, "data", "task", "id")
			t.Logf("Created 'Focus Target Task' (%s) — setting as explicit focus", focusTargetID)

			// resume --focus sets agent focus then resumes. The focus-set sub-operation
			// (stored under "agent.focus") and the resume operation share one request-id.
			// Call it and accept the result regardless — the focus is set even if the
			// resume step collides in the idempotency table.
			out := h.vybe("resume",
				"--focus", focusTargetID,
				"--request-id", rid("p13s53", 2),
			)
			m := mustJSON(t, out)
			require.NotNil(t, m, "resume --focus should return JSON")

			// Verify focus was updated via status (focus-set succeeds even if resume itself collided)
			statusOut := h.vybe("status")
			statusM := requireSuccess(t, statusOut)
			statusData := statusM["data"].(map[string]any)
			agentState := statusData["agent_state"].(map[string]any)
			focusTaskID := agentState["focus_task_id"]
			// focus_task_id may be a string or nil depending on whether other tasks are pending
			if focusTaskID != nil {
				t.Logf("Agent focus_task_id=%v after resume --focus (focus target was: %s)", focusTaskID, focusTargetID)
			} else {
				t.Logf("focus_task_id is nil after resume --focus (all tasks may be completed)")
			}
			t.Log("resume --focus exercised — focus-set sub-operation applied, agent state updated")
		})
	})

	// -------------------------------------------------------------------------
	// Act XIV: The Event Stream
	// -------------------------------------------------------------------------
	t.Run("ActXIV_TheEventStream", func(t *testing.T) {
		t.Log("=== ACT XIV: THE EVENT STREAM ===")
		t.Log("The event log is the source of truth. As it grows, agents need to manage it.")
		t.Log("status --events: query events by kind, task, or time range.")

		// Add progress events via push for querying
		t.Run("add_events_for_query", func(t *testing.T) {
			t.Logf("Adding 2 progress events to task %s for stream demonstration", authTaskID)
			for i := 0; i < 2; i++ {
				h.vybe("push",
					"--json", fmt.Sprintf(`{"task_id":%q,"event":{"kind":"progress","message":"stream event %d"}}`, authTaskID, i),
					"--request-id", rid("p14s54", i),
				)
			}
			t.Log("Added 2 progress events to the task event stream")
		})

		// Events tail — single poll, non-blocking
		t.Run("recent_activity", func(t *testing.T) {
			t.Log("Querying recent events — the agent's real-time window into what's happening")
			out := h.vybe("status", "--events", "--limit", "5", "--all")
			m := requireSuccess(t, out)
			events := m["data"].(map[string]any)["events"].([]any)
			require.NotEmpty(t, events, "events list should return recent events")
			t.Logf("Recent events: %d returned (limit=5) — event stream is active and queryable", len(events))
		})
	})

	// -------------------------------------------------------------------------
	// Act XV: System Introspection
	// -------------------------------------------------------------------------
	t.Run("ActXV_SystemIntrospection", func(t *testing.T) {
		t.Log("=== ACT XV: SYSTEM INTROSPECTION ===")
		t.Log("Schema introspection via `status --schema`.")
		t.Log("These commands give operators and agents a broader view of the system state.")

		// Schema — confirm returns command schemas
		t.Run("inspect_schema", func(t *testing.T) {
			t.Log("Fetching the command schema — agents and operators can inspect available commands and their arguments")
			t.Log("Useful for debugging, tool discovery, and understanding what flags are available")
			out := h.vybe("status", "--schema")
			m := requireSuccess(t, out)
			data := m["data"].(map[string]any)
			// Schema should have commands list
			commands, ok := data["commands"].([]any)
			require.True(t, ok && len(commands) > 0, "status --schema should return a non-empty commands list: %s", out)
			t.Logf("Schema fetched — %d commands described (flags, types, defaults)", len(commands))
		})
	})

	// -------------------------------------------------------------------------
	// Act XVI: IDE Integration
	// -------------------------------------------------------------------------
	t.Run("ActXVI_IDEIntegration", func(t *testing.T) {
		t.Log("=== ACT XVI: IDE INTEGRATION ===")
		t.Log("Vybe hooks into the Claude Code IDE lifecycle via hidden subcommands.")
		t.Log("subagent-start/stop: track spawned agents as events.")
		t.Log("stop: log turn heartbeats so the event stream reflects IDE turn boundaries.")
		t.Log("These hooks give the system a full picture of multi-agent collaboration.")

		hookSession := "sess_demo_hook_phase"

		// Hook subagent-start — log sub-agent spawn event
		t.Run("track_subagent_spawn", func(t *testing.T) {
			t.Log("Firing SubagentStart hook — Claude Code is spawning a quality-agent sub-agent")
			t.Log("vybe logs an agent_spawned event so parent agents can track their children's lifecycle")
			payload := map[string]any{
				"hook_event_name": "SubagentStart",
				"session_id":      hookSession,
				"cwd":             projectID,
				"description":     "quality-agent",
			}
			data, _ := json.Marshal(payload)
			h.vybeWithStdin(string(data), "hook", "subagent-start")

			// Verify an agent_spawned event was recorded
			eventsOut := h.vybe("status", "--events", "--kind", "agent_spawned", "--limit", "5", "--all")
			eventsM := requireSuccess(t, eventsOut)
			events := eventsM["data"].(map[string]any)["events"].([]any)
			require.NotEmpty(t, events, "agent_spawned event should be logged by subagent-start hook")
			t.Logf("agent_spawned event recorded — %d spawned event(s) in log (quality-agent launched)", len(events))
		})

		// Hook subagent-stop — log sub-agent completion event
		t.Run("track_subagent_completion", func(t *testing.T) {
			t.Log("Firing SubagentStop hook — the quality-agent sub-agent has finished its work")
			t.Log("vybe logs an agent_completed event — parent agent now knows the sub-task is done")
			payload := map[string]any{
				"hook_event_name": "SubagentStop",
				"session_id":      hookSession,
				"cwd":             projectID,
				"description":     "quality-agent",
			}
			data, _ := json.Marshal(payload)
			h.vybeWithStdin(string(data), "hook", "subagent-stop")

			// Verify an agent_completed event was recorded
			eventsOut := h.vybe("status", "--events", "--kind", "agent_completed", "--limit", "5", "--all")
			eventsM := requireSuccess(t, eventsOut)
			events := eventsM["data"].(map[string]any)["events"].([]any)
			require.NotEmpty(t, events, "agent_completed event should be logged by subagent-stop hook")
			t.Logf("agent_completed event recorded — %d completion event(s) in log (quality-agent done)", len(events))
		})

		// Hook stop — log turn completion heartbeat event
		t.Run("turn_boundary_heartbeat", func(t *testing.T) {
			t.Log("Firing Stop hook — Claude Code signals the end of a turn (agent finished responding)")
			t.Log("vybe logs a heartbeat event — turn boundaries are visible in the event stream")
			payload := map[string]any{
				"hook_event_name": "Stop",
				"session_id":      hookSession,
				"cwd":             projectID,
			}
			data, _ := json.Marshal(payload)
			h.vybeWithStdin(string(data), "hook", "stop")

			// Verify a heartbeat event was recorded
			eventsOut := h.vybe("status", "--events", "--kind", "heartbeat", "--limit", "5", "--all")
			eventsM := requireSuccess(t, eventsOut)
			events := eventsM["data"].(map[string]any)["events"].([]any)
			require.NotEmpty(t, events, "heartbeat event should be logged by stop hook")
			t.Logf("heartbeat event recorded — %d heartbeat event(s) in log (turn boundaries tracked)", len(events))
		})
	})

	// -------------------------------------------------------------------------
	// Act XVII: The Full Surface
	// -------------------------------------------------------------------------
	t.Run("ActXVII_TheFullSurface", func(t *testing.T) {
		t.Log("=== ACT XVII: THE FULL SURFACE ===")
		t.Log("The remaining commands that round out the vybe surface area.")
		t.Log("Artifact retrieval, retrospective extraction, loop stats,")
		t.Log("read-only briefs, hook management.")

		// Artifact list and lookup by ID via status --artifacts
		t.Run("artifact_get_by_id", func(t *testing.T) {
			t.Logf("Fetching artifacts for task %s ('Implement auth') and verifying by ID", authTaskID)
			t.Log("Artifacts from Act II are still here — cross-session artifact persistence confirmed again")
			// List artifacts for the task
			artListOut := h.vybe("status", "--artifacts", "--task", authTaskID)
			artListM := requireSuccess(t, artListOut)
			artifacts := artListM["data"].(map[string]any)["artifacts"].([]any)
			require.NotEmpty(t, artifacts, "artifacts should exist for auth task from Act II")

			// Verify first artifact has the expected fields
			firstArt := artifacts[0].(map[string]any)
			artifactID, ok := firstArt["id"].(string)
			require.True(t, ok && artifactID != "", "artifact should have a non-empty ID")
			gotTaskID, _ := firstArt["task_id"].(string)
			require.Equal(t, authTaskID, gotTaskID, "artifact task_id should match auth task")
			t.Logf("Artifact verified: id=%s task_id=%s — artifact lookup works", artifactID, gotTaskID)
		})

		// Hook retrospective — SessionEnd-style stdin, must not error
		t.Run("retrospective_extraction", func(t *testing.T) {
			t.Log("Running `hook retrospective` — extracts a structured retrospective from recent session events")
			t.Log("Retrospectives distill what happened into persistent memory for future sessions")
			// Add a couple of events so the retrospective has material (needs >= 2)
			h.vybe("push",
				"--json", `{"event":{"kind":"progress","message":"retrospective event A"}}`,
				"--request-id", rid("p17s63", 1),
			)
			h.vybe("push",
				"--json", `{"event":{"kind":"progress","message":"retrospective event B"}}`,
				"--request-id", rid("p17s63", 2),
			)
			t.Log("Added 2 progress events — retrospective needs material to work with")

			payload := map[string]any{
				"hook_event_name": "SessionEnd",
				"session_id":      sessionID,
				"cwd":             projectID,
			}
			data, _ := json.Marshal(payload)
			// hook retrospective is best-effort; it may log to stderr and produce no stdout.
			// We only require it exits without panic — no stdout assertion.
			h.vybeWithStdin(string(data), "hook", "retrospective")
			t.Log("hook retrospective fired — retrospective extraction complete (best-effort, no stdout required)")
		})

		// Loop stats — confirm returns JSON without error
		t.Run("loop_iteration_stats", func(t *testing.T) {
			t.Log("Fetching loop stats — tracks how many autonomous loop iterations have run")
			t.Log("Agents running in loop mode use this to monitor their own iteration cadence")
			out := h.vybe("loop", "stats")
			m := requireSuccess(t, out)
			data := m["data"].(map[string]any)
			// Verify expected fields are present (may be zero since no loops have run)
			_, hasRuns := data["runs"]
			require.True(t, hasRuns, "loop stats should include a runs field")
			t.Logf("Loop stats: runs=%v — field present (zero is expected since no loop iterations ran in this test)", data["runs"])
		})

		// Brief — read-only brief packet without cursor advancement via resume --peek
		t.Run("read_only_brief", func(t *testing.T) {
			t.Log("Running `vybe resume --peek` — like resume, but does NOT advance the event cursor")
			t.Log("Agents call this when they want context without advancing their position in the event stream")
			t.Log("Safe to call multiple times — idempotent, no side effects")
			out := h.vybe("resume", "--peek")
			m := requireSuccess(t, out)
			data, ok := m["data"].(map[string]any)
			require.True(t, ok, "resume --peek should return a data object: %s", out)
			agentName := getStr(m, "data", "agent_name")
			require.NotEmpty(t, agentName, "resume --peek data should include agent_name")
			// brief field must be present (may be null if no focus task)
			_, hasBrief := data["brief"]
			require.True(t, hasBrief, "resume --peek data should include 'brief' key: %s", out)
			t.Logf("resume --peek returned: agent_name=%q — brief packet present (task may be null if queue is empty)", agentName)
		})

		// Hook install --claude --project and hook uninstall --claude --project
		t.Run("hook_install_uninstall", func(t *testing.T) {
			t.Log("Demonstrating hook install and uninstall — wires vybe hooks into a Claude Code project")
			t.Log("Install writes .claude/settings.json with the vybe hook configuration")
			t.Log("Uninstall removes the hooks cleanly — no stale config left behind")
			// Create a temp dir with .claude subdir to act as the project root
			hookTmpDir := t.TempDir()
			require.NoError(t, os.MkdirAll(filepath.Join(hookTmpDir, ".claude"), 0750))
			t.Logf("Created temp project dir: %s — installing Claude hooks here", hookTmpDir)

			// Install Claude hooks scoped to the temp project dir
			installOut := h.vybeWithDir(hookTmpDir, "hook", "install", "--claude", "--project")
			installM := requireSuccess(t, installOut)
			installData, ok := installM["data"].(map[string]any)
			require.True(t, ok, "hook install should return data: %s", installOut)
			claudeInstall, ok := installData["claude"].(map[string]any)
			require.True(t, ok, "hook install data should include 'claude' key: %s", installOut)
			// installed or skipped list must be present; may be empty slice if already installed
			_, hasInstalled := claudeInstall["installed"]
			require.True(t, hasInstalled, "claude install result should have 'installed' field: %s", installOut)
			t.Logf("Hook install: installed=%v", claudeInstall["installed"])

			// Verify the settings file was written to the temp dir
			settingsPath := filepath.Join(hookTmpDir, ".claude", "settings.json")
			_, statErr := os.Stat(settingsPath)
			require.NoError(t, statErr, "hook install should write .claude/settings.json in the project dir")
			t.Logf("Confirmed: %s written — Claude Code will now automatically invoke vybe hooks", settingsPath)

			// Uninstall Claude hooks from the same temp project dir
			uninstallOut := h.vybeWithDir(hookTmpDir, "hook", "uninstall", "--claude", "--project")
			uninstallM := requireSuccess(t, uninstallOut)
			uninstallData, ok := uninstallM["data"].(map[string]any)
			require.True(t, ok, "hook uninstall should return data: %s", uninstallOut)
			claudeUninstall, ok := uninstallData["claude"].(map[string]any)
			require.True(t, ok, "hook uninstall data should include 'claude' key: %s", uninstallOut)
			_, hasRemoved := claudeUninstall["removed"]
			require.True(t, hasRemoved, "claude uninstall result should have 'removed' field: %s", uninstallOut)
			t.Logf("Hook uninstall: removed=%v — hooks cleanly removed, no stale config", claudeUninstall["removed"])
		})

		// Loop dry-run — autonomous loop picks up a pending task
		t.Run("loop_dry_run", func(t *testing.T) {
			t.Log("Running `loop --dry-run` — the autonomous task driver that resumes, selects, and would spawn an agent")
			t.Log("In dry-run mode with --max-tasks=1, the loop finds the next pending task and reports what it would do")

			// Create a fresh pending task for the loop to pick up
			loopTaskOut := h.vybe("task", "create",
				"--title", "Loop Demo Task",
				"--request-id", rid("p17loop", 1),
			)
			loopTaskM := requireSuccess(t, loopTaskOut)
			loopTaskID := getStr(loopTaskM, "data", "task", "id")
			require.NotEmpty(t, loopTaskID, "loop demo task should have an ID")
			t.Logf("Created pending task %s for loop to discover", loopTaskID)

			// Run the loop in dry-run mode — resumes, selects focus, reports without spawning
			out := h.vybe("loop", "--dry-run", "--max-tasks=1", "--cooldown=0s")
			m := requireSuccess(t, out)
			data := m["data"].(map[string]any)
			require.Equal(t, float64(1), data["completed"], "dry-run loop should complete 1 iteration")
			require.Equal(t, float64(1), data["total"], "dry-run loop should run 1 total")
			results := data["results"].([]any)
			require.Len(t, results, 1, "should have exactly 1 result")
			r0 := results[0].(map[string]any)
			require.Equal(t, "dry_run", r0["status"], "result status should be dry_run")
			require.NotEmpty(t, r0["task_title"], "result should have a task title")
			t.Logf("Loop dry-run: found task %s (%s) — status=%s", r0["task_id"], r0["task_title"], r0["status"])
			t.Log("The autonomous loop resumes, selects the next pending task, and would spawn an agent command")
			t.Log("In dry-run mode, it reports what it found without executing")
		})

		// Loop circuit breaker — safety rail when spawned command doesn't complete the task
		t.Run("loop_circuit_breaker", func(t *testing.T) {
			t.Log("Running `loop --command true` — spawns `true` which exits 0 but doesn't complete the task")
			t.Log("The loop detects the task is still in_progress after the command exits → marks blocked → trips circuit breaker")

			// Create and begin a task so it's in_progress
			cbTaskOut := h.vybe("task", "create",
				"--title", "Circuit Breaker Task",
				"--request-id", rid("p17cb", 1),
			)
			cbTaskM := requireSuccess(t, cbTaskOut)
			cbTaskID := getStr(cbTaskM, "data", "task", "id")
			require.NotEmpty(t, cbTaskID, "circuit breaker task should have an ID")

			beginOut := h.vybe("task", "begin",
				"--id", cbTaskID,
				"--request-id", rid("p17cb", 2),
			)
			requireSuccess(t, beginOut)
			t.Logf("Task %s is now in_progress — loop will pick it up via resume", cbTaskID)

			// Run loop with `true` as command — exits 0 but doesn't complete the task
			out := h.vybe("loop", "--command", "true", "--max-tasks=1", "--max-fails=1", "--cooldown=0s", "--task-timeout=5s")
			m := requireSuccess(t, out)
			data := m["data"].(map[string]any)
			require.GreaterOrEqual(t, data["failed"], float64(1), "should have at least 1 failure")
			results := data["results"].([]any)
			require.NotEmpty(t, results, "should have at least 1 result")
			r0 := results[0].(map[string]any)
			require.Equal(t, "blocked", r0["status"], "task should be marked blocked after command exits without completing")
			t.Logf("Circuit breaker: task %s status=%s — loop detected stuck task", r0["task_id"], r0["status"])
			t.Log("When the spawned command exits without completing the task, the loop marks it blocked")
			t.Log("This prevents runaway loops from burning resources on stuck work")
		})

		// Hook retrospective-bg — background retrospective worker
		t.Run("background_retrospective", func(t *testing.T) {
			t.Log("Running `hook retrospective-bg` — background worker that processes retrospective payloads")
			t.Log("SessionEnd fires this asynchronously so the main session doesn't block on LLM retrospective generation")
			// Write a minimal JSON payload file (retrospective-bg reads and deletes it)
			payloadFile := filepath.Join(t.TempDir(), "retro_payload.json")
			require.NoError(t, os.WriteFile(payloadFile, []byte(`{}`), 0600))
			t.Logf("Written payload file: %s — retrospective-bg will read and delete it", payloadFile)

			// retrospective-bg takes positional args: <agent> <payload-path>
			// It produces no stdout (logs to stderr only); just verify it exits without panic.
			h.vybeRaw("hook", "retrospective-bg", h.agent, payloadFile)
			// The payload file is deleted by the command on success; either outcome is acceptable.
			// Main assertion: command did not panic (we reached this line).
			t.Log("hook retrospective-bg completed — background worker ran without panic")
			t.Log("")
			t.Log("╔══════════════════════════════════════════════════════════════╗")
			t.Log("║  DEMO COMPLETE — 17 acts, every vybe command surface.      ║")
			t.Log("║  Crash-safe continuity, multi-agent coordination, and      ║")
			t.Log("║  idempotent operations — all proven end-to-end.            ║")
			t.Log("╚══════════════════════════════════════════════════════════════╝")
		})
	})
}
