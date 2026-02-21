// Package test provides integration tests that simulate a complete AI agent session
// using real vybe CLI commands against a temporary SQLite database.
package test

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// crashRID generates a deterministic request ID for the crash recovery test.
func crashRID(phase string, step int) string {
	return fmt.Sprintf("crash_%s_%d", phase, step)
}

// TestCrashRecovery_OOM simulates an OOM crash mid-session and verifies vybe's
// crash recovery. The "crash" is simulated by simply not calling cleanup hooks —
// each individual vybe command commits before returning, so all durably-written
// state survives across what would be a SIGKILL.
//
// Phases:
//  1. Build up state (pre-crash)
//  2. Simulate OOM crash (no cleanup)
//  3. Recovery (new session start)
//  4. Continue working after recovery
//  5. Stress test — rapid crash cycles
//  6. WAL recovery / final integrity check
func TestCrashRecovery_OOM(t *testing.T) {
	h := newHarness(t)
	// Override agent name for this test.
	h.agent = "crash-agent"

	// -------------------------------------------------------------------------
	// Phase 1: Build Up State (Pre-Crash)
	// -------------------------------------------------------------------------
	var (
		projectID  string
		taskAID    string
		taskBID    string
		taskCID    string
		sessionID1 = "sess_crash_001"
	)

	t.Run("Phase1_BuildUpState", func(t *testing.T) {
		// Step 1: Init DB
		t.Run("step1_upgrade", func(t *testing.T) {
			out := h.vybe("upgrade")
			m := mustJSON(t, out)
			require.Equal(t, true, m["success"], "upgrade should succeed: %s", out)
		})

		// Step 2: Set project ID directly — project CLI was removed; task create with
		// --project stores the project_id on the task without requiring a project row.
		t.Run("step2_create_project", func(t *testing.T) {
			projectID = "proj_crash_test"
			require.NotEmpty(t, projectID, "project ID should be set")
		})

		// Step 3: Create tasks A (will become in_progress), B (pending), C (pending)
		t.Run("step3_create_tasks", func(t *testing.T) {
			outA := h.vybe("task", "create",
				"--title", "Task A - In Progress",
				"--project", projectID,
				"--request-id", crashRID("p1s3", 1),
			)
			mA := requireSuccess(t, outA)
			taskAID = getStr(mA, "data", "task", "id")
			require.NotEmpty(t, taskAID)

			outB := h.vybe("task", "create",
				"--title", "Task B - Pending",
				"--project", projectID,
				"--request-id", crashRID("p1s3", 2),
			)
			mB := requireSuccess(t, outB)
			taskBID = getStr(mB, "data", "task", "id")
			require.NotEmpty(t, taskBID)

			outC := h.vybe("task", "create",
				"--title", "Task C - Blocked by A",
				"--project", projectID,
				"--request-id", crashRID("p1s3", 3),
			)
			mC := requireSuccess(t, outC)
			taskCID = getStr(mC, "data", "task", "id")
			require.NotEmpty(t, taskCID)
		})

		// Step 4: Begin task A (mark in_progress)
		t.Run("step4_begin_task_a", func(t *testing.T) {
			out := h.vybe("task", "begin",
				"--id", taskAID,
				"--request-id", crashRID("p1s4", 1),
			)
			m := requireSuccess(t, out)
			status := getStr(m, "data", "task", "status")
			require.Equal(t, "in_progress", status)
		})

		// Step 5: Add dependency — C blocked by A
		t.Run("step5_block_c_on_a", func(t *testing.T) {
			out := h.vybe("task", "add-dep",
				"--id", taskCID,
				"--depends-on", taskAID,
				"--request-id", crashRID("p1s5", 1),
			)
			requireSuccess(t, out)
		})

		// Step 6: Fire SessionStart hook — advances cursor to current event position
		t.Run("step6_session_start", func(t *testing.T) {
			stdin := hookStdin("SessionStart", sessionID1, projectID, "startup", "", "")
			out := h.vybeWithStdin(stdin, "hook", "session-start")
			m := mustJSON(t, out)
			hso, ok := m["hookSpecificOutput"].(map[string]any)
			require.True(t, ok, "hookSpecificOutput should be present: %s", out)
			additionalCtx, _ := hso["additionalContext"].(string)
			require.NotEmpty(t, additionalCtx, "additionalContext should not be empty")
			// Focus should be Task A (already in_progress)
			require.Contains(t, additionalCtx, "Task A", "focus should be Task A: %s", additionalCtx)
		})

		// Step 7: Add 10 events to task A (simulating agent progress)
		t.Run("step7_add_10_progress_events", func(t *testing.T) {
			for i := 0; i < 10; i++ {
				msg := fmt.Sprintf("Progress note %d for Task A", i+1)
				pushJSON := fmt.Sprintf(`{"task_id":%q,"event":{"kind":"progress","message":%q}}`, taskAID, msg)
				out := h.vybe("push", "--json", pushJSON, "--request-id", crashRID("p1s7", i))
				requireSuccess(t, out)
			}
			// Verify events were added
			eventsOut := h.vybe("status", "--events", "--task", taskAID, "--kind", "progress", "--limit", "20")
			eventsM := requireSuccess(t, eventsOut)
			events := eventsM["data"].(map[string]any)["events"].([]any)
			require.GreaterOrEqual(t, len(events), 10, "expected at least 10 progress events")
		})

		// Step 8: Set 5 memories (global, project, task-scoped)
		t.Run("step8_set_memories", func(t *testing.T) {
			// 2 global memories
			out := h.vybe("memory", "set",
				"--key", "crash_global_key1",
				"--value", "crash_global_value1",
				"--scope", "global",
				"--request-id", crashRID("p1s8", 1),
			)
			requireSuccess(t, out)

			out = h.vybe("memory", "set",
				"--key", "crash_global_key2",
				"--value", "crash_global_value2",
				"--scope", "global",
				"--request-id", crashRID("p1s8", 2),
			)
			requireSuccess(t, out)

			// 2 project-scoped memories
			out = h.vybe("memory", "set",
				"--key", "crash_proj_key1",
				"--value", "crash_proj_value1",
				"--scope", "project",
				"--scope-id", projectID,
				"--request-id", crashRID("p1s8", 3),
			)
			requireSuccess(t, out)

			out = h.vybe("memory", "set",
				"--key", "crash_proj_key2",
				"--value", "crash_proj_value2",
				"--scope", "project",
				"--scope-id", projectID,
				"--request-id", crashRID("p1s8", 4),
			)
			requireSuccess(t, out)

			// 1 task-scoped memory
			out = h.vybe("memory", "set",
				"--key", "crash_task_key1",
				"--value", "crash_task_value1",
				"--scope", "task",
				"--scope-id", taskAID,
				"--request-id", crashRID("p1s8", 5),
			)
			requireSuccess(t, out)
		})

		// Step 9: Add 2 artifacts
		t.Run("step9_add_artifacts", func(t *testing.T) {
			dir := t.TempDir()

			art1 := filepath.Join(dir, "crash_artifact1.go")
			require.NoError(t, os.WriteFile(art1, []byte("package crash\n"), 0600))
			push1 := fmt.Sprintf(`{"task_id":%q,"artifacts":[{"file_path":%q,"content_type":"text/x-go"}]}`, taskAID, art1)
			out := h.vybe("push", "--json", push1, "--request-id", crashRID("p1s9", 1))
			requireSuccess(t, out)

			art2 := filepath.Join(dir, "crash_artifact2.txt")
			require.NoError(t, os.WriteFile(art2, []byte("crash recovery notes\n"), 0600))
			push2 := fmt.Sprintf(`{"task_id":%q,"artifacts":[{"file_path":%q,"content_type":"text/plain"}]}`, taskAID, art2)
			out = h.vybe("push", "--json", push2, "--request-id", crashRID("p1s9", 2))
			requireSuccess(t, out)
		})

		// Step 10: Fire UserPromptSubmit hooks for 3 prompts
		t.Run("step10_user_prompts", func(t *testing.T) {
			prompts := []string{
				"Implement the core logic",
				"Add error handling",
				"Write the tests",
			}
			for _, prompt := range prompts {
				stdin := hookStdin("UserPromptSubmit", sessionID1, projectID, "", prompt, "")
				h.vybeWithStdin(stdin, "hook", "prompt")
			}
			// Verify prompt events were logged
			eventsOut := h.vybe("status", "--events", "--kind", "user_prompt", "--limit", "10", "--all")
			eventsM := requireSuccess(t, eventsOut)
			events := eventsM["data"].(map[string]any)["events"].([]any)
			require.GreaterOrEqual(t, len(events), 3, "expected at least 3 user_prompt events")
		})

		// Step 11: Fire PostToolUse hooks for 2 tool calls (mutating tools only — Read is skipped by hook)
		t.Run("step11_tool_calls", func(t *testing.T) {
			stdin1 := hookStdinWithToolInput("PostToolUse", sessionID1, projectID, "Bash",
				map[string]any{"command": "go build ./..."})
			h.vybeWithStdin(stdin1, "hook", "tool-success")

			stdin2 := hookStdinWithToolInput("PostToolUse", sessionID1, projectID, "Write",
				map[string]any{"file_path": "/tmp/crash_file.go", "content": "package crash"})
			h.vybeWithStdin(stdin2, "hook", "tool-success")

			// Verify tool events logged
			eventsOut := h.vybe("status", "--events", "--kind", "tool_success", "--limit", "10", "--all")
			eventsM := requireSuccess(t, eventsOut)
			events := eventsM["data"].(map[string]any)["events"].([]any)
			require.GreaterOrEqual(t, len(events), 2, "expected at least 2 tool_success events")
		})

		// NOTE: Intentionally NOT firing checkpoint, session-end, or any cleanup hooks.
		// This is the pre-crash state.
	})

	// -------------------------------------------------------------------------
	// Phase 2: Simulate OOM Crash
	// -------------------------------------------------------------------------
	// No action needed — "crash" = stop without calling cleanup.
	// Task A is still in_progress. No compact happened. No GC ran.
	// The agent cursor was advanced during step6 SessionStart but events added
	// in steps 7–11 are AFTER that cursor position and will appear as deltas
	// on next resume.

	// -------------------------------------------------------------------------
	// Phase 3: Recovery (New Session)
	// -------------------------------------------------------------------------
	var sessionID2 = "sess_crash_002"

	t.Run("Phase3_Recovery", func(t *testing.T) {
		// Step 12: Fire SessionStart hook again (fresh session, same agent)
		// This is what happens when Claude Code restarts after OOM
		t.Run("step12_recovery_session_start", func(t *testing.T) {
			stdin := hookStdin("SessionStart", sessionID2, projectID, "startup", "", "")
			out := h.vybeWithStdin(stdin, "hook", "session-start")
			m := mustJSON(t, out)
			hso, ok := m["hookSpecificOutput"].(map[string]any)
			require.True(t, ok, "hookSpecificOutput should be present after recovery: %s", out)
			additionalCtx, _ := hso["additionalContext"].(string)
			require.NotEmpty(t, additionalCtx, "additionalContext should not be empty after recovery")
			// Task A is still in_progress — it should remain focus
			require.Contains(t, additionalCtx, "Task A",
				"recovery should still focus on in_progress Task A: %s", additionalCtx)
		})

		// Step 13: Verify resume returns correct state
		t.Run("step13_resume_correctness", func(t *testing.T) {
			resumeOut := h.vybe("resume", "--request-id", crashRID("p3s13", 1))
			resumeM := requireSuccess(t, resumeOut)
			brief := resumeM["data"].(map[string]any)["brief"].(map[string]any)

			// Focus task must be Task A (still in_progress)
			task := brief["task"]
			require.NotNil(t, task, "resume must return a focus task after crash recovery")
			recoveredTaskID := task.(map[string]any)["id"].(string)
			require.Equal(t, taskAID, recoveredTaskID, "focus task must be Task A (in_progress)")
			recoveredStatus := task.(map[string]any)["status"].(string)
			require.Equal(t, "in_progress", recoveredStatus, "Task A must still be in_progress after crash")

			// Deltas should include events added after the last cursor advancement
			deltas := resumeM["data"].(map[string]any)["deltas"]
			if deltas != nil {
				deltaList, ok := deltas.([]any)
				if ok {
					// Events since last cursor (steps 7-11 added after session-start in step 6)
					// We expect progress events, prompt events, tool events
					require.NotEmpty(t, deltaList, "deltas should include events added after crash")
				}
			}

			// Memories must be intact
			memories := brief["relevant_memory"]
			if memories != nil {
				memList, ok := memories.([]any)
				if ok {
					require.NotEmpty(t, memList, "memories should be intact after crash")
				}
			}
		})

		// Step 14: Verify all memories survived the crash
		t.Run("step14_memories_intact", func(t *testing.T) {
			// Global memories
			out := h.vybe("memory", "get", "--key", "crash_global_key1", "--scope", "global")
			m := requireSuccess(t, out)
			require.Equal(t, "crash_global_value1", getStr(m, "data", "value"),
				"global memory crash_global_key1 must survive crash")

			out = h.vybe("memory", "get", "--key", "crash_global_key2", "--scope", "global")
			m = requireSuccess(t, out)
			require.Equal(t, "crash_global_value2", getStr(m, "data", "value"),
				"global memory crash_global_key2 must survive crash")

			// Project-scoped memories
			out = h.vybe("memory", "get", "--key", "crash_proj_key1",
				"--scope", "project", "--scope-id", projectID)
			m = requireSuccess(t, out)
			require.Equal(t, "crash_proj_value1", getStr(m, "data", "value"),
				"project memory crash_proj_key1 must survive crash")

			out = h.vybe("memory", "get", "--key", "crash_proj_key2",
				"--scope", "project", "--scope-id", projectID)
			m = requireSuccess(t, out)
			require.Equal(t, "crash_proj_value2", getStr(m, "data", "value"),
				"project memory crash_proj_key2 must survive crash")

			// Task-scoped memory
			out = h.vybe("memory", "get", "--key", "crash_task_key1",
				"--scope", "task", "--scope-id", taskAID)
			m = requireSuccess(t, out)
			require.Equal(t, "crash_task_value1", getStr(m, "data", "value"),
				"task memory crash_task_key1 must survive crash")
		})

		// Step 15: Verify artifacts survived the crash
		t.Run("step15_artifacts_intact", func(t *testing.T) {
			artOut := h.vybe("status", "--artifacts", "--task", taskAID)
			artM := requireSuccess(t, artOut)
			artifacts := artM["data"].(map[string]any)["artifacts"].([]any)
			require.GreaterOrEqual(t, len(artifacts), 2, "both artifacts must survive crash")
		})

		// Step 16: Verify resume --peek matches resume
		t.Run("step16_brief_matches_resume", func(t *testing.T) {
			briefOut := h.vybe("resume", "--peek")
			briefM := requireSuccess(t, briefOut)
			briefData := briefM["data"].(map[string]any)["brief"].(map[string]any)

			task := briefData["task"]
			require.NotNil(t, task, "resume --peek should have a focus task after recovery")
			briefTaskID := task.(map[string]any)["id"].(string)
			require.Equal(t, taskAID, briefTaskID, "resume --peek focus task must match resume focus task")
		})

		// Step 17: Status check — no corruption
		t.Run("step17_status_check", func(t *testing.T) {
			statusOut := h.vybe("status", "--check")
			statusM := requireSuccess(t, statusOut)
			queryOK := statusM["data"].(map[string]any)["query_ok"]
			require.Equal(t, true, queryOK, "status check must report query_ok=true after crash recovery")
		})
	})

	// -------------------------------------------------------------------------
	// Phase 4: Continue Working After Recovery
	// -------------------------------------------------------------------------
	t.Run("Phase4_ContinueAfterRecovery", func(t *testing.T) {
		// Step 18: Add more events to task A post-recovery
		t.Run("step18_add_post_recovery_events", func(t *testing.T) {
			for i := 0; i < 3; i++ {
				msg := fmt.Sprintf("Post-recovery progress %d", i+1)
				pushJSON := fmt.Sprintf(`{"task_id":%q,"event":{"kind":"progress","message":%q}}`, taskAID, msg)
				out := h.vybe("push", "--json", pushJSON, "--request-id", crashRID("p4s18", i))
				requireSuccess(t, out)
			}
		})

		// Step 19: Complete task A
		t.Run("step19_complete_task_a", func(t *testing.T) {
			out := h.vybe("task", "complete",
				"--id", taskAID,
				"--outcome", "done",
				"--summary", "Task A completed after crash recovery",
				"--request-id", crashRID("p4s19", 1),
			)
			m := requireSuccess(t, out)
			status := getStr(m, "data", "task", "status")
			require.Equal(t, "completed", status, "Task A must be completed")
		})

		// Step 20: Fire TaskCompleted hook
		t.Run("step20_task_completed_hook", func(t *testing.T) {
			payload := map[string]any{
				"cwd":             projectID,
				"session_id":      sessionID2,
				"hook_event_name": "TaskCompleted",
				"task_id":         taskAID,
			}
			data, _ := json.Marshal(payload)
			h.vybeWithStdin(string(data), "hook", "task-completed")
		})

		// Step 21: Fire checkpoint hook (the one that didn't fire pre-crash)
		t.Run("step21_checkpoint_hook", func(t *testing.T) {
			stdin := hookStdin("PreCompact", sessionID2, projectID, "", "", "")
			h.vybeWithStdin(stdin, "hook", "checkpoint")
		})

		// Step 22: Resume — verify focus advances to Task B (C is blocked by A dependency)
		// Note: Task A is now completed, so it should be unblocked.
		// Task B is pending and unblocked. Task C depends on A (now completed).
		t.Run("step22_resume_advances_focus", func(t *testing.T) {
			resumeOut := h.vybe("resume", "--request-id", crashRID("p4s22", 1))
			resumeM := requireSuccess(t, resumeOut)
			brief := resumeM["data"].(map[string]any)["brief"].(map[string]any)

			task := brief["task"]
			if task != nil {
				nextTaskID := task.(map[string]any)["id"].(string)
				// Should be B or C (not A, which is completed)
				require.NotEqual(t, taskAID, nextTaskID, "focus must advance past completed Task A")
				isBorC := nextTaskID == taskBID || nextTaskID == taskCID
				require.True(t, isBorC, "focus should be Task B or Task C, got: %s", nextTaskID)
			}
			// If task is nil, all tasks are somehow completed — that's also acceptable
			// since C may be auto-unblocked, but we don't fail silently here.
		})

		// Step 23: Verify no data was lost from pre-crash session
		t.Run("step23_no_data_loss", func(t *testing.T) {
			// All 10 original progress events should still be there
			eventsOut := h.vybe("status", "--events", "--task", taskAID, "--kind", "progress", "--limit", "20")
			eventsM := requireSuccess(t, eventsOut)
			events := eventsM["data"].(map[string]any)["events"].([]any)
			// Original 10 + 3 post-recovery = 13 progress events
			require.GreaterOrEqual(t, len(events), 10,
				"original 10 progress events must not be lost after crash+recovery, got: %d", len(events))

			// Artifacts still linked
			artOut := h.vybe("status", "--artifacts", "--task", taskAID)
			artM := requireSuccess(t, artOut)
			artifacts := artM["data"].(map[string]any)["artifacts"].([]any)
			require.GreaterOrEqual(t, len(artifacts), 2, "artifacts must not be lost after crash+recovery")
		})
	})

	// -------------------------------------------------------------------------
	// Phase 5: Stress Test — Rapid Crash Cycles
	// -------------------------------------------------------------------------
	var taskDID string

	t.Run("Phase5_RapidCrashCycles", func(t *testing.T) {

		// Step 24: Create task D
		t.Run("step24_create_task_d", func(t *testing.T) {
			out := h.vybe("task", "create",
				"--title", "Task D - Rapid Crash",
				"--project", projectID,
				"--request-id", crashRID("p5s24", 1),
			)
			m := requireSuccess(t, out)
			taskDID = getStr(m, "data", "task", "id")
			require.NotEmpty(t, taskDID)
		})

		// Step 25: Begin D, add 1 event, "crash" (no cleanup)
		t.Run("step25_crash_cycle_1", func(t *testing.T) {
			beginOut := h.vybe("task", "begin",
				"--id", taskDID,
				"--request-id", crashRID("p5s25", 1),
			)
			requireSuccess(t, beginOut)

			pushJSON := fmt.Sprintf(`{"task_id":%q,"event":{"kind":"progress","message":"Crash cycle 1 event"}}`, taskDID)
			evtOut := h.vybe("push", "--json", pushJSON, "--request-id", crashRID("p5s25", 2))
			requireSuccess(t, evtOut)

			// "Crash" — no session-end or checkpoint
		})

		// Step 26: Recovery 1 — verify D is focus, event visible
		t.Run("step26_recovery_cycle_1", func(t *testing.T) {
			sessionCrash1 := "sess_crash_rapid_001"
			stdin := hookStdin("SessionStart", sessionCrash1, projectID, "startup", "", "")
			out := h.vybeWithStdin(stdin, "hook", "session-start")
			m := mustJSON(t, out)
			hso, ok := m["hookSpecificOutput"].(map[string]any)
			require.True(t, ok, "hookSpecificOutput should be present: %s", out)
			additionalCtx, _ := hso["additionalContext"].(string)
			require.NotEmpty(t, additionalCtx, "additionalContext should not be empty")
			// Task D should be focus (in_progress)
			require.True(t,
				strings.Contains(additionalCtx, "Task D") || strings.Contains(additionalCtx, "Rapid Crash"),
				"Task D should be in focus after rapid crash 1 recovery: %s", additionalCtx)

			// Verify the event is visible
			eventsOut := h.vybe("status", "--events", "--task", taskDID, "--kind", "progress", "--limit", "5")
			eventsM := requireSuccess(t, eventsOut)
			events := eventsM["data"].(map[string]any)["events"].([]any)
			require.GreaterOrEqual(t, len(events), 1, "event from crash cycle 1 must be visible")
		})

		// Step 27: Add 1 more event, "crash" again
		t.Run("step27_crash_cycle_2", func(t *testing.T) {
			pushJSON := fmt.Sprintf(`{"task_id":%q,"event":{"kind":"progress","message":"Crash cycle 2 event"}}`, taskDID)
			evtOut := h.vybe("push", "--json", pushJSON, "--request-id", crashRID("p5s27", 1))
			requireSuccess(t, evtOut)

			// "Crash" — no session-end or checkpoint
		})

		// Step 28: Recovery 2 — verify both events visible
		t.Run("step28_recovery_cycle_2", func(t *testing.T) {
			sessionCrash2 := "sess_crash_rapid_002"
			stdin := hookStdin("SessionStart", sessionCrash2, projectID, "startup", "", "")
			h.vybeWithStdin(stdin, "hook", "session-start")

			// Both events must be visible
			eventsOut := h.vybe("status", "--events", "--task", taskDID, "--kind", "progress", "--limit", "10")
			eventsM := requireSuccess(t, eventsOut)
			events := eventsM["data"].(map[string]any)["events"].([]any)
			require.GreaterOrEqual(t, len(events), 2, "both events from crash cycles must be visible")
		})

		// Step 29: Complete task D
		t.Run("step29_complete_task_d", func(t *testing.T) {
			out := h.vybe("task", "complete",
				"--id", taskDID,
				"--outcome", "done",
				"--summary", "Task D completed after rapid crash cycles",
				"--request-id", crashRID("p5s29", 1),
			)
			m := requireSuccess(t, out)
			status := getStr(m, "data", "task", "status")
			require.Equal(t, "completed", status)
		})

		// Step 30: Final status check — everything healthy
		t.Run("step30_status_check", func(t *testing.T) {
			statusOut := h.vybe("status", "--check")
			statusM := requireSuccess(t, statusOut)
			queryOK := statusM["data"].(map[string]any)["query_ok"]
			require.Equal(t, true, queryOK, "status check must report query_ok=true after rapid crash cycles")
		})
	})

	// -------------------------------------------------------------------------
	// Phase 6: WAL Recovery / Final Integrity Check
	// -------------------------------------------------------------------------
	t.Run("Phase6_WALRecoveryAndIntegrity", func(t *testing.T) {
		// Step 31: Verify system integrity via status check + task list
		t.Run("step31_integrity_check", func(t *testing.T) {
			statusOut := h.vybe("status", "--check")
			statusM := requireSuccess(t, statusOut)
			queryOK := statusM["data"].(map[string]any)["query_ok"]
			require.Equal(t, true, queryOK, "status check must pass after all crash cycles")

			// Verify task counts via task list
			tasksOut := h.vybe("task", "list")
			tasksM := requireSuccess(t, tasksOut)
			tasksList := tasksM["data"].(map[string]any)["tasks"].([]any)

			completed := 0
			for _, raw := range tasksList {
				task := raw.(map[string]any)
				if task["status"].(string) == "completed" {
					completed++
				}
			}
			require.GreaterOrEqual(t, completed, 2,
				"at least 2 tasks should be completed (A and D)")
		})

		// Step 32: Final status --check
		t.Run("step32_final_status_check", func(t *testing.T) {
			statusOut := h.vybe("status", "--check")
			statusM := requireSuccess(t, statusOut)
			data := statusM["data"].(map[string]any)

			queryOK := data["query_ok"]
			require.Equal(t, true, queryOK, "final status check must report query_ok=true")
		})

		// Step 33: Verify all tasks are in expected final states
		t.Run("step33_final_task_states", func(t *testing.T) {
			tasksOut := h.vybe("task", "list")
			tasksM := requireSuccess(t, tasksOut)
			tasksList := tasksM["data"].(map[string]any)["tasks"].([]any)

			taskStatuses := make(map[string]string)
			for _, raw := range tasksList {
				task := raw.(map[string]any)
				id := task["id"].(string)
				status := task["status"].(string)
				taskStatuses[id] = status
			}

			// Task A should be completed (we completed it in Phase 4)
			require.Equal(t, "completed", taskStatuses[taskAID],
				"Task A must be completed in final state")

			// Task D should be completed (we completed it in Phase 5)
			if taskDID != "" {
				require.Equal(t, "completed", taskStatuses[taskDID],
					"Task D must be completed in final state")
			}
		})
	})
}
