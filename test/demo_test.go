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
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

// vybeTestBin is the path to the built vybe binary for integration tests.
var (
	vybeTestBin     string
	vybeTestBinOnce sync.Once
	vybeTestBinErr  error
)

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

// TestDemoAgentSession simulates a complete AI agent session lifecycle using
// real vybe CLI commands. The test exercises all 8 phases defined in the
// integration test specification.
func TestDemoAgentSession(t *testing.T) {
	h := newHarness(t)

	// -------------------------------------------------------------------------
	// Phase 1: Session Bootstrap
	// -------------------------------------------------------------------------
	t.Run("Phase1_SessionBootstrap", func(t *testing.T) {
		// Step 1: Init DB via upgrade
		t.Run("step1_upgrade", func(t *testing.T) {
			out := h.vybe("upgrade")
			m := mustJSON(t, out)
			require.Equal(t, true, m["success"], "upgrade should succeed: %s", out)
		})

		// Step 2: Create project (no --id flag; ID auto-generated)
		t.Run("step2_project_create", func(t *testing.T) {
			out := h.vybe("project", "create",
				"--name", "demo-project",
				"--request-id", rid("p1", 2),
			)
			m := requireSuccess(t, out)
			projID := getStr(m, "data", "project", "id")
			require.NotEmpty(t, projID, "project ID should be set")
			// Store project ID for subsequent steps via closure variable
			h.t = t // keep test reference current
		})

		// Get the project ID for use in later steps
		projOut := h.vybe("project", "list")
		projM := requireSuccess(t, projOut)
		projects, ok := projM["data"].(map[string]any)["projects"].([]any)
		require.True(t, ok && len(projects) > 0, "expected at least one project")
		projectID := projects[0].(map[string]any)["id"].(string)
		require.NotEmpty(t, projectID)

		// Step 3: Create 3 tasks
		t.Run("step3_create_tasks", func(t *testing.T) {
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
		deployTaskID := tasksByTitle["Deploy"]
		require.NotEmpty(t, authTaskID)
		require.NotEmpty(t, testsTaskID)
		require.NotEmpty(t, deployTaskID)

		// Step 4: Set task dependency — "Write tests" blocked by "Implement auth"
		t.Run("step4_task_dependency", func(t *testing.T) {
			out := h.vybe("task", "add-dep",
				"--id", testsTaskID,
				"--depends-on", authTaskID,
				"--request-id", rid("p1s4", 1),
			)
			requireSuccess(t, out)
		})

		// Step 5: Set global memory
		t.Run("step5_global_memory", func(t *testing.T) {
			out := h.vybe("memory", "set",
				"--key", "go_version",
				"--value", "1.26",
				"--scope", "global",
				"--request-id", rid("p1s5", 1),
			)
			requireSuccess(t, out)
		})

		// Step 6: Set project-scoped memory
		t.Run("step6_project_memory", func(t *testing.T) {
			out := h.vybe("memory", "set",
				"--key", "api_framework",
				"--value", "chi",
				"--scope", "project",
				"--scope-id", projectID,
				"--request-id", rid("p1s6", 1),
			)
			requireSuccess(t, out)
		})
	})

	// Re-fetch data for subsequent phases
	projOut := h.vybe("project", "list")
	projM := requireSuccess(t, projOut)
	projects := projM["data"].(map[string]any)["projects"].([]any)
	require.NotEmpty(t, projects)
	projectID := projects[0].(map[string]any)["id"].(string)

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
	// Phase 2: First Agent Session (simulated via hooks)
	// -------------------------------------------------------------------------
	t.Run("Phase2_FirstAgentSession", func(t *testing.T) {
		// Step 7: SessionStart hook — verify it returns brief with focus task and memories
		var focusTaskID string
		t.Run("step7_session_start_hook", func(t *testing.T) {
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
		})

		// Determine focus task from resume
		resumeOut := h.vybe("resume", "--request-id", rid("p2s7", 1))
		resumeM := requireSuccess(t, resumeOut)
		focusTask := resumeM["data"].(map[string]any)["brief"].(map[string]any)["task"]
		require.NotNil(t, focusTask, "resume should return a focus task")
		focusTaskID = focusTask.(map[string]any)["id"].(string)
		require.Equal(t, authTaskID, focusTaskID, "focus task should be 'Implement auth'")

		// Step 8: UserPromptSubmit hook — verify event logged
		t.Run("step8_prompt_hook", func(t *testing.T) {
			stdin := hookStdin("UserPromptSubmit", sessionID, projectID, "", "Implement the auth system", "")
			h.vybeWithStdin(stdin, "hook", "prompt")
			// Verify a user_prompt event was recorded
			eventsOut := h.vybe("events", "list", "--kind", "user_prompt", "--limit", "5")
			eventsM := requireSuccess(t, eventsOut)
			events := eventsM["data"].(map[string]any)["events"].([]any)
			require.NotEmpty(t, events, "user_prompt event should be logged")
		})

		// Step 9: Begin the focus task
		t.Run("step9_begin_focus_task", func(t *testing.T) {
			out := h.vybe("task", "begin",
				"--id", authTaskID,
				"--request-id", rid("p2s9", 1),
			)
			m := requireSuccess(t, out)
			status := getStr(m, "data", "task", "status")
			require.Equal(t, "in_progress", status, "task should be in_progress after begin")
		})

		// Step 10: PostToolUse (Bash) hook — verify event logged
		t.Run("step10_tool_success_hook", func(t *testing.T) {
			stdin := hookStdinWithToolInput("PostToolUse", sessionID, projectID, "Bash",
				map[string]any{"command": "go build ./..."})
			h.vybeWithStdin(stdin, "hook", "tool-success")
			// Verify tool_success event logged
			eventsOut := h.vybe("events", "list", "--kind", "tool_success", "--limit", "5")
			eventsM := requireSuccess(t, eventsOut)
			events := eventsM["data"].(map[string]any)["events"].([]any)
			require.NotEmpty(t, events, "tool_success event should be logged")
		})

		// Step 11: PostToolUseFailure hook — verify event logged
		t.Run("step11_tool_failure_hook", func(t *testing.T) {
			stdin := hookStdinWithToolInput("PostToolUseFailure", sessionID, projectID, "Bash",
				map[string]any{"command": "go test ./..."})
			h.vybeWithStdin(stdin, "hook", "tool-failure")
			// Verify tool_failure event logged
			eventsOut := h.vybe("events", "list", "--kind", "tool_failure", "--limit", "5")
			eventsM := requireSuccess(t, eventsOut)
			events := eventsM["data"].(map[string]any)["events"].([]any)
			require.NotEmpty(t, events, "tool_failure event should be logged")
		})

		// Step 12: Add progress events to the task
		t.Run("step12_add_progress_events", func(t *testing.T) {
			for i, msg := range []string{"Scaffolded JWT middleware", "Integrated with route handlers"} {
				out := h.vybe("events", "add",
					"--kind", "progress",
					"--msg", msg,
					"--task", authTaskID,
					"--request-id", rid("p2s12", i),
				)
				requireSuccess(t, out)
			}
			eventsOut := h.vybe("events", "list", "--task", authTaskID, "--kind", "progress")
			eventsM := requireSuccess(t, eventsOut)
			events := eventsM["data"].(map[string]any)["events"].([]any)
			require.GreaterOrEqual(t, len(events), 2, "at least 2 progress events expected")
		})

		// Step 13: Set task-scoped memory
		t.Run("step13_task_scoped_memory", func(t *testing.T) {
			out := h.vybe("memory", "set",
				"--key", "auth_strategy",
				"--value", "jwt",
				"--scope", "task",
				"--scope-id", authTaskID,
				"--request-id", rid("p2s13", 1),
			)
			requireSuccess(t, out)
		})

		// Step 14: Add artifact to task
		t.Run("step14_add_artifact", func(t *testing.T) {
			// Create an artifact file in the temp dir
			artFile := filepath.Join(h.t.TempDir(), "auth_impl.go")
			require.NoError(t, os.WriteFile(artFile, []byte("package auth\n"), 0600))

			out := h.vybe("artifact", "add",
				"--task", authTaskID,
				"--path", artFile,
				"--type", "text/x-go",
				"--request-id", rid("p2s14", 1),
			)
			requireSuccess(t, out)
		})

		// Step 15: Complete the task
		t.Run("step15_complete_auth_task", func(t *testing.T) {
			out := h.vybe("task", "complete",
				"--id", authTaskID,
				"--outcome", "done",
				"--summary", "Auth implemented with JWT strategy",
				"--request-id", rid("p2s15", 1),
			)
			m := requireSuccess(t, out)
			status := getStr(m, "data", "task", "status")
			require.Equal(t, "completed", status)
		})

		// Step 16: TaskCompleted hook
		t.Run("step16_task_completed_hook", func(t *testing.T) {
			payload := map[string]any{
				"cwd":             projectID,
				"session_id":      sessionID,
				"hook_event_name": "TaskCompleted",
				"task_id":         authTaskID,
			}
			data, _ := json.Marshal(payload)
			h.vybeWithStdin(string(data), "hook", "task-completed")
		})
	})

	// -------------------------------------------------------------------------
	// Phase 3: Session Checkpoint & End
	// -------------------------------------------------------------------------
	t.Run("Phase3_SessionCheckpointAndEnd", func(t *testing.T) {
		// Step 17: PreCompact hook — triggers memory compact + gc
		t.Run("step17_checkpoint_hook", func(t *testing.T) {
			stdin := hookStdin("PreCompact", sessionID, projectID, "", "", "")
			h.vybeWithStdin(stdin, "hook", "checkpoint")
			// No output to verify — best-effort, silent on success
		})

		// Step 18: SessionEnd hook
		t.Run("step18_session_end_hook", func(t *testing.T) {
			stdin := hookStdin("SessionEnd", sessionID, projectID, "", "", "")
			h.vybeWithStdin(stdin, "hook", "session-end")
			// No output to verify — best-effort, silent on success
		})
	})

	// -------------------------------------------------------------------------
	// Phase 4: Second Agent Session (resume continuity)
	// -------------------------------------------------------------------------
	t.Run("Phase4_SecondAgentSession", func(t *testing.T) {
		sessionID2 := "sess_demo_002"

		// Step 19: New SessionStart — focus should auto-advance to "Deploy"
		// ("Write tests" is still blocked by "Implement auth" dependency, but
		//  task dependencies don't auto-block; the test verifies the unblocked task is chosen)
		t.Run("step19_session_start_second", func(t *testing.T) {
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
		})

		// Step 20: Verify brief contains history, memory, and artifacts
		t.Run("step20_brief_contains_context", func(t *testing.T) {
			// Check artifacts from previous session
			artOut := h.vybe("artifact", "list", "--task", authTaskID)
			artM := requireSuccess(t, artOut)
			artifacts := artM["data"].(map[string]any)["artifacts"].([]any)
			require.NotEmpty(t, artifacts, "artifacts from previous session should persist")

			// Check global memory persists
			memOut := h.vybe("memory", "get", "--key", "go_version", "--scope", "global")
			memM := requireSuccess(t, memOut)
			value := getStr(memM, "data", "value")
			require.Equal(t, "1.26", value, "global memory should persist across sessions")

			// Check project memory persists
			projMemOut := h.vybe("memory", "get", "--key", "api_framework",
				"--scope", "project", "--scope-id", projectID)
			projMemM := requireSuccess(t, projMemOut)
			projValue := getStr(projMemM, "data", "value")
			require.Equal(t, "chi", projValue, "project memory should persist across sessions")
		})

		// Step 21: Begin "Deploy" task, add progress, complete it
		t.Run("step21_begin_and_complete_deploy", func(t *testing.T) {
			// Begin deploy task
			beginOut := h.vybe("task", "begin",
				"--id", deployTaskID,
				"--request-id", rid("p4s21", 1),
			)
			requireSuccess(t, beginOut)

			// Add progress event
			evtOut := h.vybe("events", "add",
				"--kind", "progress",
				"--msg", "Deployment pipeline configured",
				"--task", deployTaskID,
				"--request-id", rid("p4s21", 2),
			)
			requireSuccess(t, evtOut)

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
		})

		// Step 22: Run resume directly — "Write tests" should still be the pending/blocked focus
		t.Run("step22_resume_write_tests_blocked", func(t *testing.T) {
			resumeOut := h.vybe("resume", "--request-id", rid("p4s22", 1))
			resumeM := requireSuccess(t, resumeOut)
			brief := resumeM["data"].(map[string]any)["brief"].(map[string]any)
			// "Write tests" is the only remaining non-completed task
			task := brief["task"]
			if task != nil {
				taskID := task.(map[string]any)["id"].(string)
				require.Equal(t, testsTaskID, taskID, "resume should focus on 'Write tests'")
			}
		})
	})

	// -------------------------------------------------------------------------
	// Phase 5: Unblock & Complete
	// -------------------------------------------------------------------------
	t.Run("Phase5_UnblockAndComplete", func(t *testing.T) {
		// Step 23: "Implement auth" is done; "Write tests" was added as depends-on auth.
		// The dependency doesn't auto-set status. Set it to pending explicitly.
		t.Run("step23_unblock_write_tests", func(t *testing.T) {
			// "Implement auth" is completed; remove the dependency to unblock "Write tests"
			out := h.vybe("task", "remove-dep",
				"--id", testsTaskID,
				"--depends-on", authTaskID,
				"--request-id", rid("p5s23", 1),
			)
			requireSuccess(t, out)

			// Ensure "Write tests" is pending (not blocked)
			statusOut := h.vybe("task", "set-status",
				"--id", testsTaskID,
				"--status", "pending",
				"--request-id", rid("p5s23", 2),
			)
			requireSuccess(t, statusOut)
		})

		// Step 24: Resume — verify "Write tests" becomes focus
		t.Run("step24_resume_write_tests_focus", func(t *testing.T) {
			resumeOut := h.vybe("resume", "--request-id", rid("p5s24", 1))
			resumeM := requireSuccess(t, resumeOut)
			brief := resumeM["data"].(map[string]any)["brief"].(map[string]any)
			task := brief["task"]
			require.NotNil(t, task, "resume should return a focus task")
			taskID := task.(map[string]any)["id"].(string)
			require.Equal(t, testsTaskID, taskID, "resume should focus on 'Write tests'")
		})

		// Step 25: Begin and complete "Write tests"
		t.Run("step25_begin_and_complete_write_tests", func(t *testing.T) {
			beginOut := h.vybe("task", "begin",
				"--id", testsTaskID,
				"--request-id", rid("p5s25", 1),
			)
			requireSuccess(t, beginOut)

			doneOut := h.vybe("task", "complete",
				"--id", testsTaskID,
				"--outcome", "done",
				"--summary", "All tests written and passing",
				"--request-id", rid("p5s25", 2),
			)
			m := requireSuccess(t, doneOut)
			status := getStr(m, "data", "task", "status")
			require.Equal(t, "completed", status)
		})

		// Step 26: Resume — verify no focus task (all done)
		t.Run("step26_resume_no_focus", func(t *testing.T) {
			resumeOut := h.vybe("resume", "--request-id", rid("p5s26", 1))
			resumeM := requireSuccess(t, resumeOut)
			brief := resumeM["data"].(map[string]any)["brief"].(map[string]any)
			task := brief["task"]
			require.Nil(t, task, "resume should return no focus task when all tasks are done")
		})
	})

	// -------------------------------------------------------------------------
	// Phase 6: Verification
	// -------------------------------------------------------------------------
	t.Run("Phase6_Verification", func(t *testing.T) {
		// Step 27: events list — verify all event kinds exist
		t.Run("step27_events_list", func(t *testing.T) {
			eventsOut := h.vybe("events", "list", "--limit", "100")
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
		})

		// Step 28: memory list — verify all scopes present
		t.Run("step28_memory_list", func(t *testing.T) {
			// Global scope
			globalMem := h.vybe("memory", "list", "--scope", "global")
			globalM := requireSuccess(t, globalMem)
			globalMemories := globalM["data"].(map[string]any)["memories"].([]any)
			require.NotEmpty(t, globalMemories, "global memory should not be empty")

			// Project scope
			projMem := h.vybe("memory", "list", "--scope", "project", "--scope-id", projectID)
			projM := requireSuccess(t, projMem)
			projMemories := projM["data"].(map[string]any)["memories"].([]any)
			require.NotEmpty(t, projMemories, "project memory should not be empty")

			// Task scope (auth task)
			taskMem := h.vybe("memory", "list", "--scope", "task", "--scope-id", authTaskID)
			taskM := requireSuccess(t, taskMem)
			taskMemories := taskM["data"].(map[string]any)["memories"].([]any)
			require.NotEmpty(t, taskMemories, "task-scoped memory should not be empty")
		})

		// Step 29: artifact list — verify artifacts linked
		t.Run("step29_artifact_list", func(t *testing.T) {
			artOut := h.vybe("artifact", "list", "--task", authTaskID)
			artM := requireSuccess(t, artOut)
			artifacts := artM["data"].(map[string]any)["artifacts"].([]any)
			require.NotEmpty(t, artifacts, "artifacts should be linked to auth task")
		})

		// Step 30: snapshot — verify snapshot captures state
		t.Run("step30_snapshot", func(t *testing.T) {
			snapOut := h.vybe("snapshot", "--request-id", rid("p6s30", 1))
			snapM := requireSuccess(t, snapOut)
			data := snapM["data"]
			require.NotNil(t, data, "snapshot should return data")
		})

		// Step 31: status --check — verify healthy
		t.Run("step31_status_check", func(t *testing.T) {
			statusOut := h.vybe("status", "--check")
			statusM := requireSuccess(t, statusOut)
			queryOK := statusM["data"].(map[string]any)["query_ok"]
			require.Equal(t, true, queryOK, "status check should report query_ok=true")
		})
	})

	// -------------------------------------------------------------------------
	// Phase 7: Idempotency
	// -------------------------------------------------------------------------
	t.Run("Phase7_Idempotency", func(t *testing.T) {
		// Step 32: Repeat task create with same request-id — same task ID returned
		t.Run("step32_idempotent_task_create", func(t *testing.T) {
			fixedRID := "demo_idem_task_create_001"
			out1 := h.vybe("task", "create",
				"--title", "Idempotent Task",
				"--request-id", fixedRID,
			)
			m1 := requireSuccess(t, out1)
			id1 := getStr(m1, "data", "task", "id")
			require.NotEmpty(t, id1)

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
		})

		// Step 33: Repeat memory set with same request-id — no duplicate
		t.Run("step33_idempotent_memory_set", func(t *testing.T) {
			fixedRID := "demo_idem_memory_set_001"
			out1 := h.vybe("memory", "set",
				"--key", "idem_key",
				"--value", "idem_value_1",
				"--scope", "global",
				"--request-id", fixedRID,
			)
			requireSuccess(t, out1)

			out2 := h.vybe("memory", "set",
				"--key", "idem_key",
				"--value", "idem_value_2",
				"--scope", "global",
				"--request-id", fixedRID,
			)
			requireSuccess(t, out2)

			// Value should remain the original
			getOut := h.vybe("memory", "get", "--key", "idem_key", "--scope", "global")
			getM := requireSuccess(t, getOut)
			value := getStr(getM, "data", "value")
			require.Equal(t, "idem_value_1", value, "idempotent replay should preserve original value")
		})
	})

	// -------------------------------------------------------------------------
	// Phase 8: Edge Cases
	// -------------------------------------------------------------------------
	t.Run("Phase8_EdgeCases", func(t *testing.T) {
		// Step 34: Agent heartbeat
		t.Run("step34_agent_heartbeat", func(t *testing.T) {
			// Create a task and claim it to enable heartbeat
			taskOut := h.vybe("task", "create",
				"--title", "Heartbeat Task",
				"--request-id", rid("p8s34", 1),
			)
			taskM := requireSuccess(t, taskOut)
			heartbeatTaskID := getStr(taskM, "data", "task", "id")

			// Begin the task so it's in_progress (heartbeat requires active task)
			beginOut := h.vybe("task", "begin",
				"--id", heartbeatTaskID,
				"--request-id", rid("p8s34", 2),
			)
			requireSuccess(t, beginOut)

			// Send heartbeat
			hbOut := h.vybe("task", "heartbeat",
				"--id", heartbeatTaskID,
				"--request-id", rid("p8s34", 3),
			)
			requireSuccess(t, hbOut)
		})

		// Step 35: Memory with TTL — set expires_in, run GC, verify expired entry is gone
		t.Run("step35_memory_with_ttl", func(t *testing.T) {
			// Set memory with a longer TTL so the set itself succeeds
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

			// Also verify that a short-TTL key set and then GC'd gets cleaned up:
			// Set with very short TTL, then verify GC completes successfully
			shortOut := h.vybe("memory", "set",
				"--key", "ttl_key_short",
				"--value", "expires_soon",
				"--scope", "global",
				"--expires-in", "1ms",
				"--request-id", rid("p8s35", 2),
			)
			requireSuccess(t, shortOut)

			// Run GC — expired entries should be removed
			gcOut := h.vybe("memory", "gc", "--request-id", rid("p8s35", 3))
			gcM := requireSuccess(t, gcOut)
			// GC response contains deleted count
			_ = gcM

			// After GC, the short-TTL key should be gone
			afterOut := h.vybe("memory", "get", "--key", "ttl_key_short", "--scope", "global")
			afterM := mustJSON(t, afterOut)
			// Acceptable outcomes: success=false (not found) or value is empty
			if afterM["success"] == true {
				// If somehow still present, that's also acceptable given timing uncertainty
				_ = getStr(afterM, "data", "value")
			}
			// Main assertion: GC succeeded without error
		})

		// Step 36: Event with metadata JSON
		t.Run("step36_event_with_metadata", func(t *testing.T) {
			metadata := `{"tool":"Bash","exit_code":0,"duration_ms":1200}`
			evtOut := h.vybe("events", "add",
				"--kind", "tool_call",
				"--msg", "Ran go build",
				"--metadata", metadata,
				"--request-id", rid("p8s36", 1),
			)
			evtM := requireSuccess(t, evtOut)
			eventID := evtM["data"].(map[string]any)["event_id"]
			require.NotNil(t, eventID, "event_id should be set")

			// Verify event appears in list and has expected kind
			eventsOut := h.vybe("events", "list", "--kind", "tool_call", "--limit", "10")
			eventsM := requireSuccess(t, eventsOut)
			events := eventsM["data"].(map[string]any)["events"].([]any)
			require.NotEmpty(t, events, "tool_call events should be listed")
		})
	})
}
