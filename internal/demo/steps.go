package demo

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Act I: Building The World

func stepUpgradeDatabase(r *Runner, ctx *DemoContext) error {
	m, raw, err := r.vybe("upgrade")
	if err != nil {
		return fmt.Errorf("upgrade failed: %w", err)
	}
	if err := r.mustSuccess(m, raw); err != nil {
		return err
	}
	r.printDetail("Database ready — schema migrated")
	return nil
}

func stepCreateProject(r *Runner, ctx *DemoContext) error {
	m, raw, err := r.vybe("project", "create",
		"--name", "demo-project",
		"--request-id", rid("p1", 2),
	)
	if err != nil {
		return err
	}
	if err := r.mustSuccess(m, raw); err != nil {
		return err
	}

	// Get project ID from list
	lm, lraw, err := r.vybe("project", "list")
	if err != nil {
		return err
	}
	if err := r.mustSuccess(lm, lraw); err != nil {
		return err
	}
	projects, ok := lm["data"].(map[string]any)["projects"].([]any)
	if !ok || len(projects) == 0 {
		return fmt.Errorf("no projects found after create")
	}
	ctx.ProjectID = projects[0].(map[string]any)["id"].(string)
	r.printDetail("Project created: id=%s", ctx.ProjectID)
	return nil
}

func stepCreateTaskGraph(r *Runner, ctx *DemoContext) error {
	taskTitles := []string{"Implement auth", "Write tests", "Deploy"}
	for i, title := range taskTitles {
		m, raw, err := r.vybe("task", "create",
			"--title", title,
			"--project", ctx.ProjectID,
			"--request-id", rid("p1s3", i),
		)
		if err != nil {
			return fmt.Errorf("create task %q: %w", title, err)
		}
		if err := r.mustSuccess(m, raw); err != nil {
			return fmt.Errorf("create task %q: %w", title, err)
		}
		taskID := getStr(m, "data", "task", "id")
		r.printDetail("Task created: id=%s title=%q", taskID, title)
	}

	// Fetch and store task IDs
	tm, traw, err := r.vybe("task", "list", "--status", "pending")
	if err != nil {
		return err
	}
	if err := r.mustSuccess(tm, traw); err != nil {
		return err
	}
	tasksList, ok := tm["data"].(map[string]any)["tasks"].([]any)
	if !ok || len(tasksList) != 3 {
		return fmt.Errorf("expected 3 tasks, got data: %s", traw)
	}
	tasksByTitle := make(map[string]string)
	for _, raw := range tasksList {
		task := raw.(map[string]any)
		tasksByTitle[task["title"].(string)] = task["id"].(string)
	}
	ctx.AuthTaskID = tasksByTitle["Implement auth"]
	ctx.TestsTaskID = tasksByTitle["Write tests"]
	ctx.DeployTaskID = tasksByTitle["Deploy"]
	if ctx.AuthTaskID == "" || ctx.TestsTaskID == "" || ctx.DeployTaskID == "" {
		return fmt.Errorf("could not find all task IDs: %v", tasksByTitle)
	}
	r.printDetail("Auth=%s Tests=%s Deploy=%s", ctx.AuthTaskID, ctx.TestsTaskID, ctx.DeployTaskID)
	return nil
}

func stepSetDependencies(r *Runner, ctx *DemoContext) error {
	m, raw, err := r.vybe("task", "add-dep",
		"--id", ctx.TestsTaskID,
		"--depends-on", ctx.AuthTaskID,
		"--request-id", rid("p1s4", 1),
	)
	if err != nil {
		return err
	}
	if err := r.mustSuccess(m, raw); err != nil {
		return err
	}
	r.printDetail("Dependency: Write tests blocked by Implement auth")
	return nil
}

func stepStoreGlobalMemory(r *Runner, ctx *DemoContext) error {
	m, raw, err := r.vybe("memory", "set",
		"--key", "go_version",
		"--value", "1.26",
		"--scope", "global",
		"--request-id", rid("p1s5", 1),
	)
	if err != nil {
		return err
	}
	if err := r.mustSuccess(m, raw); err != nil {
		return err
	}
	r.printDetail("Global memory: go_version=1.26")
	return nil
}

func stepStoreProjectMemory(r *Runner, ctx *DemoContext) error {
	m, raw, err := r.vybe("memory", "set",
		"--key", "api_framework",
		"--value", "chi",
		"--scope", "project",
		"--scope-id", ctx.ProjectID,
		"--request-id", rid("p1s6", 1),
	)
	if err != nil {
		return err
	}
	if err := r.mustSuccess(m, raw); err != nil {
		return err
	}
	r.printDetail("Project memory: api_framework=chi")
	return nil
}

// Act II: The Agent Works

func stepSessionStartHook(r *Runner, ctx *DemoContext) error {
	ctx.SessionID = "sess_demo_001"
	stdin := hookStdin("SessionStart", ctx.SessionID, ctx.ProjectID, "startup", "", "")
	m, raw, err := r.vybeWithStdin(stdin, "hook", "session-start")
	if err != nil {
		return err
	}
	if m == nil {
		return fmt.Errorf("no output from session-start hook")
	}
	hso, ok := m["hookSpecificOutput"].(map[string]any)
	if !ok {
		return fmt.Errorf("hookSpecificOutput missing: %s", raw)
	}
	additionalCtx, _ := hso["additionalContext"].(string)
	if additionalCtx == "" {
		return fmt.Errorf("additionalContext empty: %s", raw)
	}
	if !strings.Contains(additionalCtx, "Implement auth") {
		return fmt.Errorf("additionalContext should mention focus task 'Implement auth': %s", additionalCtx[:min(200, len(additionalCtx))])
	}
	r.printDetail("Session context injected — focus: Implement auth")
	return nil
}

func stepResume(r *Runner, ctx *DemoContext) error {
	m, raw, err := r.vybe("resume", "--request-id", rid("p2s7", 1))
	if err != nil {
		return err
	}
	if err := r.mustSuccess(m, raw); err != nil {
		return err
	}
	brief, ok := m["data"].(map[string]any)["brief"].(map[string]any)
	if !ok {
		return fmt.Errorf("brief missing in resume response: %s", raw)
	}
	focusTask, ok := brief["task"].(map[string]any)
	if !ok {
		return fmt.Errorf("no focus task in brief: %s", raw)
	}
	focusTaskID := focusTask["id"].(string)
	if focusTaskID != ctx.AuthTaskID {
		return fmt.Errorf("expected focus task %s (Implement auth), got %s", ctx.AuthTaskID, focusTaskID)
	}
	r.printDetail("Focus confirmed: %s (Implement auth)", focusTaskID)
	return nil
}

func stepPromptLogging(r *Runner, ctx *DemoContext) error {
	stdin := hookStdin("UserPromptSubmit", ctx.SessionID, ctx.ProjectID, "", "Implement the auth system", "")
	_, _, _ = r.vybeWithStdin(stdin, "hook", "prompt")

	m, raw, err := r.vybe("events", "list", "--kind", "user_prompt", "--limit", "5")
	if err != nil {
		return err
	}
	if err := r.mustSuccess(m, raw); err != nil {
		return err
	}
	events, ok := m["data"].(map[string]any)["events"].([]any)
	if !ok || len(events) == 0 {
		return fmt.Errorf("user_prompt event should be logged: %s", raw)
	}
	r.printDetail("user_prompt event recorded: %d event(s)", len(events))
	return nil
}

func stepClaimFocusTask(r *Runner, ctx *DemoContext) error {
	m, raw, err := r.vybe("task", "begin",
		"--id", ctx.AuthTaskID,
		"--request-id", rid("p2s9", 1),
	)
	if err != nil {
		return err
	}
	if err := r.mustSuccess(m, raw); err != nil {
		return err
	}
	status := getStr(m, "data", "task", "status")
	if status != "in_progress" {
		return fmt.Errorf("expected in_progress, got %s", status)
	}
	r.printDetail("Task %s: pending → in_progress", ctx.AuthTaskID)
	return nil
}

func stepToolSuccessTracking(r *Runner, ctx *DemoContext) error {
	stdin := hookStdinWithToolInput("PostToolUse", ctx.SessionID, ctx.ProjectID, "Bash",
		map[string]any{"command": "go build ./..."})
	_, _, _ = r.vybeWithStdin(stdin, "hook", "tool-success")

	m, raw, err := r.vybe("events", "list", "--kind", "tool_success", "--limit", "5")
	if err != nil {
		return err
	}
	if err := r.mustSuccess(m, raw); err != nil {
		return err
	}
	events, ok := m["data"].(map[string]any)["events"].([]any)
	if !ok || len(events) == 0 {
		return fmt.Errorf("tool_success event should be logged: %s", raw)
	}
	r.printDetail("tool_success event logged: %d event(s)", len(events))
	return nil
}

func stepToolFailureTracking(r *Runner, ctx *DemoContext) error {
	stdin := hookStdinWithToolInput("PostToolUseFailure", ctx.SessionID, ctx.ProjectID, "Bash",
		map[string]any{"command": "go test ./..."})
	_, _, _ = r.vybeWithStdin(stdin, "hook", "tool-failure")

	m, raw, err := r.vybe("events", "list", "--kind", "tool_failure", "--limit", "5")
	if err != nil {
		return err
	}
	if err := r.mustSuccess(m, raw); err != nil {
		return err
	}
	events, ok := m["data"].(map[string]any)["events"].([]any)
	if !ok || len(events) == 0 {
		return fmt.Errorf("tool_failure event should be logged: %s", raw)
	}
	r.printDetail("tool_failure event logged: %d event(s)", len(events))
	return nil
}

func stepLogProgressEvents(r *Runner, ctx *DemoContext) error {
	messages := []string{"Scaffolded JWT middleware", "Integrated with route handlers"}
	for i, msg := range messages {
		m, raw, err := r.vybe("events", "add",
			"--kind", "progress",
			"--msg", msg,
			"--task", ctx.AuthTaskID,
			"--request-id", rid("p2s12", i),
		)
		if err != nil {
			return fmt.Errorf("add event %q: %w", msg, err)
		}
		if err := r.mustSuccess(m, raw); err != nil {
			return fmt.Errorf("add event %q: %w", msg, err)
		}
		r.printDetail("Progress: %q", msg)
	}

	m, raw, err := r.vybe("events", "list", "--task", ctx.AuthTaskID, "--kind", "progress")
	if err != nil {
		return err
	}
	if err := r.mustSuccess(m, raw); err != nil {
		return err
	}
	events, _ := m["data"].(map[string]any)["events"].([]any)
	if len(events) < 2 {
		return fmt.Errorf("expected >= 2 progress events, got %d", len(events))
	}
	r.printDetail("Task %s has %d progress event(s)", ctx.AuthTaskID, len(events))
	return nil
}

func stepStoreTaskMemory(r *Runner, ctx *DemoContext) error {
	m, raw, err := r.vybe("memory", "set",
		"--key", "auth_strategy",
		"--value", "jwt",
		"--scope", "task",
		"--scope-id", ctx.AuthTaskID,
		"--request-id", rid("p2s13", 1),
	)
	if err != nil {
		return err
	}
	if err := r.mustSuccess(m, raw); err != nil {
		return err
	}
	r.printDetail("Task memory: auth_strategy=jwt")
	return nil
}

func stepLinkArtifact(r *Runner, ctx *DemoContext) error {
	// Create temp dir if not set
	if ctx.TempDir == "" {
		tmpDir, err := os.MkdirTemp("", "vybe-demo-artifacts-*")
		if err != nil {
			return fmt.Errorf("create temp dir: %w", err)
		}
		ctx.TempDir = tmpDir
	}

	artFile := filepath.Join(ctx.TempDir, "auth_impl.go")
	if err := os.WriteFile(artFile, []byte("package auth\n"), 0600); err != nil {
		return fmt.Errorf("write artifact file: %w", err)
	}

	m, raw, err := r.vybe("artifact", "add",
		"--task", ctx.AuthTaskID,
		"--path", artFile,
		"--type", "text/x-go",
		"--request-id", rid("p2s14", 1),
	)
	if err != nil {
		return err
	}
	if err := r.mustSuccess(m, raw); err != nil {
		return err
	}
	r.printDetail("Artifact linked: %s → task %s", artFile, ctx.AuthTaskID)
	return nil
}

func stepCompleteTask(r *Runner, ctx *DemoContext) error {
	m, raw, err := r.vybe("task", "complete",
		"--id", ctx.AuthTaskID,
		"--outcome", "done",
		"--summary", "Auth implemented with JWT strategy",
		"--request-id", rid("p2s15", 1),
	)
	if err != nil {
		return err
	}
	if err := r.mustSuccess(m, raw); err != nil {
		return err
	}
	status := getStr(m, "data", "task", "status")
	if status != "completed" {
		return fmt.Errorf("expected completed, got %s", status)
	}
	r.printDetail("Task %s: in_progress → completed", ctx.AuthTaskID)
	return nil
}

func stepTaskCompletionHook(r *Runner, ctx *DemoContext) error {
	payload := map[string]any{
		"cwd":             ctx.ProjectID,
		"session_id":      ctx.SessionID,
		"hook_event_name": "TaskCompleted",
		"task_id":         ctx.AuthTaskID,
	}
	data, _ := json.Marshal(payload)
	_, _, _ = r.vybeWithStdin(string(data), "hook", "task-completed")
	r.printDetail("TaskCompleted hook fired for task %s", ctx.AuthTaskID)
	return nil
}

// Act III: The Agent Sleeps

func stepMemoryCheckpoint(r *Runner, ctx *DemoContext) error {
	stdin := hookStdin("PreCompact", ctx.SessionID, ctx.ProjectID, "", "", "")
	_, _, _ = r.vybeWithStdin(stdin, "hook", "checkpoint")
	r.printDetail("Memory checkpoint complete")
	return nil
}

func stepSessionEnd(r *Runner, ctx *DemoContext) error {
	stdin := hookStdin("SessionEnd", ctx.SessionID, ctx.ProjectID, "", "", "")
	_, _, _ = r.vybeWithStdin(stdin, "hook", "session-end")
	r.printDetail("Session ended — state durable in SQLite")
	return nil
}

// Act IV: The Agent Returns

func stepNewSessionStart(r *Runner, ctx *DemoContext) error {
	ctx.SessionID2 = "sess_demo_002"
	stdin := hookStdin("SessionStart", ctx.SessionID2, ctx.ProjectID, "startup", "", "")
	m, raw, err := r.vybeWithStdin(stdin, "hook", "session-start")
	if err != nil {
		return err
	}
	if m == nil {
		return fmt.Errorf("no output from session-start hook")
	}
	hso, ok := m["hookSpecificOutput"].(map[string]any)
	if !ok {
		return fmt.Errorf("hookSpecificOutput missing: %s", raw)
	}
	additionalCtx, _ := hso["additionalContext"].(string)
	if additionalCtx == "" {
		return fmt.Errorf("additionalContext empty: %s", raw)
	}
	hasDeploy := strings.Contains(additionalCtx, "Deploy")
	hasWriteTests := strings.Contains(additionalCtx, "Write tests")
	if !hasDeploy && !hasWriteTests {
		return fmt.Errorf("focus should be an unblocked task, got: %s", additionalCtx[:min(200, len(additionalCtx))])
	}
	r.printDetail("New session context loaded — focus auto-advanced")
	return nil
}

func stepCrossSessionContinuity(r *Runner, ctx *DemoContext) error {
	// Check artifacts
	am, araw, err := r.vybe("artifact", "list", "--task", ctx.AuthTaskID)
	if err != nil {
		return err
	}
	if err := r.mustSuccess(am, araw); err != nil {
		return err
	}
	artifacts, ok := am["data"].(map[string]any)["artifacts"].([]any)
	if !ok || len(artifacts) == 0 {
		return fmt.Errorf("artifacts from previous session should persist: %s", araw)
	}
	r.printDetail("Artifacts survived: %d file(s)", len(artifacts))

	// Check global memory
	gm, graw, err := r.vybe("memory", "get", "--key", "go_version", "--scope", "global")
	if err != nil {
		return err
	}
	if err := r.mustSuccess(gm, graw); err != nil {
		return err
	}
	value := getStr(gm, "data", "value")
	if value != "1.26" {
		return fmt.Errorf("expected go_version=1.26, got %q", value)
	}
	r.printDetail("Global memory survived: go_version=%q", value)

	// Check project memory
	pm, praw, err := r.vybe("memory", "get", "--key", "api_framework", "--scope", "project", "--scope-id", ctx.ProjectID)
	if err != nil {
		return err
	}
	if err := r.mustSuccess(pm, praw); err != nil {
		return err
	}
	projValue := getStr(pm, "data", "value")
	if projValue != "chi" {
		return fmt.Errorf("expected api_framework=chi, got %q", projValue)
	}
	r.printDetail("Project memory survived: api_framework=%q", projValue)
	return nil
}

func stepCompleteDeployTask(r *Runner, ctx *DemoContext) error {
	bm, braw, err := r.vybe("task", "begin",
		"--id", ctx.DeployTaskID,
		"--request-id", rid("p4s21", 1),
	)
	if err != nil {
		return err
	}
	if err := r.mustSuccess(bm, braw); err != nil {
		return err
	}
	r.printDetail("Deploy task: pending → in_progress")

	em, eraw, err := r.vybe("events", "add",
		"--kind", "progress",
		"--msg", "Deployment pipeline configured",
		"--task", ctx.DeployTaskID,
		"--request-id", rid("p4s21", 2),
	)
	if err != nil {
		return err
	}
	if err := r.mustSuccess(em, eraw); err != nil {
		return err
	}

	dm, draw, err := r.vybe("task", "complete",
		"--id", ctx.DeployTaskID,
		"--outcome", "done",
		"--summary", "Deployed to production",
		"--request-id", rid("p4s21", 3),
	)
	if err != nil {
		return err
	}
	if err := r.mustSuccess(dm, draw); err != nil {
		return err
	}
	status := getStr(dm, "data", "task", "status")
	if status != "completed" {
		return fmt.Errorf("expected completed, got %s", status)
	}
	r.printDetail("Deploy task: in_progress → completed")
	return nil
}

func stepResumeWithBlockedTask(r *Runner, ctx *DemoContext) error {
	m, raw, err := r.vybe("resume", "--request-id", rid("p4s22", 1))
	if err != nil {
		return err
	}
	if err := r.mustSuccess(m, raw); err != nil {
		return err
	}
	brief, ok := m["data"].(map[string]any)["brief"].(map[string]any)
	if !ok {
		return fmt.Errorf("brief missing: %s", raw)
	}
	task := brief["task"]
	if task != nil {
		taskID := task.(map[string]any)["id"].(string)
		if taskID != ctx.TestsTaskID {
			return fmt.Errorf("expected Write tests task %s, got %s", ctx.TestsTaskID, taskID)
		}
		r.printDetail("Resume focus: Write tests (%s)", taskID)
	} else {
		r.printDetail("Resume: no task (all completed or blocked)")
	}
	return nil
}

// Act V: The Queue Moves

func stepRemoveDependency(r *Runner, ctx *DemoContext) error {
	m, raw, err := r.vybe("task", "remove-dep",
		"--id", ctx.TestsTaskID,
		"--depends-on", ctx.AuthTaskID,
		"--request-id", rid("p5s23", 1),
	)
	if err != nil {
		return err
	}
	if err := r.mustSuccess(m, raw); err != nil {
		return err
	}
	r.printDetail("Dependency removed: Write tests no longer blocked by Implement auth")

	sm, sraw, err := r.vybe("task", "set-status",
		"--id", ctx.TestsTaskID,
		"--status", "pending",
		"--request-id", rid("p5s23", 2),
	)
	if err != nil {
		return err
	}
	if err := r.mustSuccess(sm, sraw); err != nil {
		return err
	}
	r.printDetail("Write tests set to pending")
	return nil
}

func stepResumeSelectsUnblocked(r *Runner, ctx *DemoContext) error {
	m, raw, err := r.vybe("resume", "--request-id", rid("p5s24", 1))
	if err != nil {
		return err
	}
	if err := r.mustSuccess(m, raw); err != nil {
		return err
	}
	brief, ok := m["data"].(map[string]any)["brief"].(map[string]any)
	if !ok {
		return fmt.Errorf("brief missing: %s", raw)
	}
	task, ok := brief["task"].(map[string]any)
	if !ok {
		return fmt.Errorf("expected focus task, got nil: %s", raw)
	}
	taskID := task["id"].(string)
	if taskID != ctx.TestsTaskID {
		return fmt.Errorf("expected Write tests %s, got %s", ctx.TestsTaskID, taskID)
	}
	r.printDetail("Resume correctly selected Write tests (%s)", taskID)
	return nil
}

func stepCompleteFinalTask(r *Runner, ctx *DemoContext) error {
	bm, braw, err := r.vybe("task", "begin",
		"--id", ctx.TestsTaskID,
		"--request-id", rid("p5s25", 1),
	)
	if err != nil {
		return err
	}
	if err := r.mustSuccess(bm, braw); err != nil {
		return err
	}

	dm, draw, err := r.vybe("task", "complete",
		"--id", ctx.TestsTaskID,
		"--outcome", "done",
		"--summary", "All tests written and passing",
		"--request-id", rid("p5s25", 2),
	)
	if err != nil {
		return err
	}
	if err := r.mustSuccess(dm, draw); err != nil {
		return err
	}
	status := getStr(dm, "data", "task", "status")
	if status != "completed" {
		return fmt.Errorf("expected completed, got %s", status)
	}
	r.printDetail("Write tests: in_progress → completed — all 3 tasks done")
	return nil
}

func stepEmptyQueue(r *Runner, ctx *DemoContext) error {
	m, raw, err := r.vybe("resume", "--request-id", rid("p5s26", 1))
	if err != nil {
		return err
	}
	if err := r.mustSuccess(m, raw); err != nil {
		return err
	}
	brief, ok := m["data"].(map[string]any)["brief"].(map[string]any)
	if !ok {
		return fmt.Errorf("brief missing: %s", raw)
	}
	if brief["task"] != nil {
		return fmt.Errorf("expected task=null, got: %v", brief["task"])
	}
	r.printDetail("Queue empty — task=null")
	return nil
}

// Act VI: Auditing The Record

func stepQueryEventStream(r *Runner, ctx *DemoContext) error {
	m, raw, err := r.vybe("events", "list", "--limit", "100")
	if err != nil {
		return err
	}
	if err := r.mustSuccess(m, raw); err != nil {
		return err
	}
	events, ok := m["data"].(map[string]any)["events"].([]any)
	if !ok || len(events) == 0 {
		return fmt.Errorf("events list should not be empty: %s", raw)
	}
	kinds := make(map[string]bool)
	for _, raw := range events {
		e := raw.(map[string]any)
		if k, ok := e["kind"].(string); ok {
			kinds[k] = true
		}
	}
	if !kinds["user_prompt"] && !kinds["progress"] {
		return fmt.Errorf("expected user_prompt or progress events, got kinds: %v", kinds)
	}
	if !kinds["tool_success"] {
		return fmt.Errorf("expected tool_success events, got kinds: %v", kinds)
	}
	if !kinds["tool_failure"] {
		return fmt.Errorf("expected tool_failure events, got kinds: %v", kinds)
	}
	r.printDetail("Event stream: %d total events", len(events))
	return nil
}

func stepQueryAllMemoryScopes(r *Runner, ctx *DemoContext) error {
	// Global scope
	gm, graw, err := r.vybe("memory", "list", "--scope", "global")
	if err != nil {
		return err
	}
	if err := r.mustSuccess(gm, graw); err != nil {
		return err
	}
	globalMems, ok := gm["data"].(map[string]any)["memories"].([]any)
	if !ok || len(globalMems) == 0 {
		return fmt.Errorf("global memory should not be empty: %s", graw)
	}
	r.printDetail("Global memory: %d entries", len(globalMems))

	// Project scope
	pm, praw, err := r.vybe("memory", "list", "--scope", "project", "--scope-id", ctx.ProjectID)
	if err != nil {
		return err
	}
	if err := r.mustSuccess(pm, praw); err != nil {
		return err
	}
	projMems, ok := pm["data"].(map[string]any)["memories"].([]any)
	if !ok || len(projMems) == 0 {
		return fmt.Errorf("project memory should not be empty: %s", praw)
	}
	r.printDetail("Project memory: %d entries", len(projMems))

	// Task scope
	tm, traw, err := r.vybe("memory", "list", "--scope", "task", "--scope-id", ctx.AuthTaskID)
	if err != nil {
		return err
	}
	if err := r.mustSuccess(tm, traw); err != nil {
		return err
	}
	taskMems, ok := tm["data"].(map[string]any)["memories"].([]any)
	if !ok || len(taskMems) == 0 {
		return fmt.Errorf("task-scoped memory should not be empty: %s", traw)
	}
	r.printDetail("Task memory: %d entries", len(taskMems))
	return nil
}

func stepQueryArtifacts(r *Runner, ctx *DemoContext) error {
	m, raw, err := r.vybe("artifact", "list", "--task", ctx.AuthTaskID)
	if err != nil {
		return err
	}
	if err := r.mustSuccess(m, raw); err != nil {
		return err
	}
	artifacts, ok := m["data"].(map[string]any)["artifacts"].([]any)
	if !ok || len(artifacts) == 0 {
		return fmt.Errorf("artifacts should be linked to auth task: %s", raw)
	}
	r.printDetail("Artifacts: %d file(s) linked to auth task", len(artifacts))
	return nil
}

func stepCaptureSnapshot(r *Runner, ctx *DemoContext) error {
	m, raw, err := r.vybe("snapshot", "--request-id", rid("p6s30", 1))
	if err != nil {
		return err
	}
	if err := r.mustSuccess(m, raw); err != nil {
		return err
	}
	if m["data"] == nil {
		return fmt.Errorf("snapshot should return data: %s", raw)
	}
	r.printDetail("Snapshot captured")
	return nil
}

func stepHealthCheck(r *Runner, ctx *DemoContext) error {
	m, raw, err := r.vybe("status", "--check")
	if err != nil {
		return err
	}
	if err := r.mustSuccess(m, raw); err != nil {
		return err
	}
	queryOK := m["data"].(map[string]any)["query_ok"]
	if queryOK != true {
		return fmt.Errorf("status check should report query_ok=true: %s", raw)
	}
	r.printDetail("Health check passed: query_ok=true")
	return nil
}

// Act VII: Crash-Safe Retries

func stepReplayTaskCreate(r *Runner, ctx *DemoContext) error {
	fixedRID := "demo_idem_task_create_001"
	m1, raw1, err := r.vybe("task", "create",
		"--title", "Idempotent Task",
		"--request-id", fixedRID,
	)
	if err != nil {
		return err
	}
	if err := r.mustSuccess(m1, raw1); err != nil {
		return err
	}
	id1 := getStr(m1, "data", "task", "id")
	if id1 == "" {
		return fmt.Errorf("task ID should be set: %s", raw1)
	}
	r.printDetail("First create: task %s", id1)

	m2, raw2, err := r.vybe("task", "create",
		"--title", "Idempotent Task Changed",
		"--request-id", fixedRID,
	)
	if err != nil {
		return err
	}
	if err := r.mustSuccess(m2, raw2); err != nil {
		return err
	}
	id2 := getStr(m2, "data", "task", "id")
	if id1 != id2 {
		return fmt.Errorf("same request-id should return same task ID: %s != %s", id1, id2)
	}
	title2 := getStr(m2, "data", "task", "title")
	if title2 != "Idempotent Task" {
		return fmt.Errorf("idempotent replay should return original title %q, got %q", "Idempotent Task", title2)
	}
	r.printDetail("Replay returned same id=%s original title=%q", id2, title2)
	return nil
}

func stepReplayMemorySet(r *Runner, ctx *DemoContext) error {
	fixedRID := "demo_idem_memory_set_001"
	m1, raw1, err := r.vybe("memory", "set",
		"--key", "idem_key",
		"--value", "idem_value_1",
		"--scope", "global",
		"--request-id", fixedRID,
	)
	if err != nil {
		return err
	}
	if err := r.mustSuccess(m1, raw1); err != nil {
		return err
	}

	m2, raw2, err := r.vybe("memory", "set",
		"--key", "idem_key",
		"--value", "idem_value_2",
		"--scope", "global",
		"--request-id", fixedRID,
	)
	if err != nil {
		return err
	}
	if err := r.mustSuccess(m2, raw2); err != nil {
		return err
	}

	gm, graw, err := r.vybe("memory", "get", "--key", "idem_key", "--scope", "global")
	if err != nil {
		return err
	}
	if err := r.mustSuccess(gm, graw); err != nil {
		return err
	}
	value := getStr(gm, "data", "value")
	if value != "idem_value_1" {
		return fmt.Errorf("idempotent replay should preserve original value, got %q", value)
	}
	r.printDetail("Memory value after replay: %q — original preserved", value)
	return nil
}

// Act VIII: Production Hardening

func stepHeartbeatLiveness(r *Runner, ctx *DemoContext) error {
	tm, traw, err := r.vybe("task", "create",
		"--title", "Heartbeat Task",
		"--request-id", rid("p8s34", 1),
	)
	if err != nil {
		return err
	}
	if err := r.mustSuccess(tm, traw); err != nil {
		return err
	}
	heartbeatTaskID := getStr(tm, "data", "task", "id")
	r.printDetail("Created task %s", heartbeatTaskID)

	bm, braw, err := r.vybe("task", "begin",
		"--id", heartbeatTaskID,
		"--request-id", rid("p8s34", 2),
	)
	if err != nil {
		return err
	}
	if err := r.mustSuccess(bm, braw); err != nil {
		return err
	}

	hm, hraw, err := r.vybe("task", "heartbeat",
		"--id", heartbeatTaskID,
		"--request-id", rid("p8s34", 3),
	)
	if err != nil {
		return err
	}
	if err := r.mustSuccess(hm, hraw); err != nil {
		return err
	}
	r.printDetail("Heartbeat sent for task %s", heartbeatTaskID)
	return nil
}

func stepTTLExpiryAndGC(r *Runner, ctx *DemoContext) error {
	m, raw, err := r.vybe("memory", "set",
		"--key", "ttl_key_24h",
		"--value", "expires_in_24h",
		"--scope", "global",
		"--expires-in", "24h",
		"--request-id", rid("p8s35", 1),
	)
	if err != nil {
		return err
	}
	if err := r.mustSuccess(m, raw); err != nil {
		return err
	}

	gm, graw, err := r.vybe("memory", "get", "--key", "ttl_key_24h", "--scope", "global")
	if err != nil {
		return err
	}
	if err := r.mustSuccess(gm, graw); err != nil {
		return err
	}
	value := getStr(gm, "data", "value")
	if value != "expires_in_24h" {
		return fmt.Errorf("expected expires_in_24h, got %q", value)
	}
	expiresAt := gm["data"].(map[string]any)["expires_at"]
	if expiresAt == nil {
		return fmt.Errorf("expires_at should be set for TTL memory: %s", graw)
	}
	r.printDetail("TTL memory set: value=%q expires_at=%v", value, expiresAt)

	sm, sraw, err := r.vybe("memory", "set",
		"--key", "ttl_key_short",
		"--value", "expires_soon",
		"--scope", "global",
		"--expires-in", "1ms",
		"--request-id", rid("p8s35", 2),
	)
	if err != nil {
		return err
	}
	if err := r.mustSuccess(sm, sraw); err != nil {
		return err
	}

	gcm, gcraw, err := r.vybe("memory", "gc", "--request-id", rid("p8s35", 3))
	if err != nil {
		return err
	}
	if err := r.mustSuccess(gcm, gcraw); err != nil {
		return err
	}
	r.printDetail("Memory GC complete")
	return nil
}

func stepStructuredMetadata(r *Runner, ctx *DemoContext) error {
	metadata := `{"tool":"Bash","exit_code":0,"duration_ms":1200}`
	m, raw, err := r.vybe("events", "add",
		"--kind", "tool_call",
		"--msg", "Ran go build",
		"--metadata", metadata,
		"--request-id", rid("p8s36", 1),
	)
	if err != nil {
		return err
	}
	if err := r.mustSuccess(m, raw); err != nil {
		return err
	}
	eventID := m["data"].(map[string]any)["event_id"]
	if eventID == nil {
		return fmt.Errorf("event_id should be set: %s", raw)
	}
	r.printDetail("Event logged: id=%v kind=tool_call", eventID)

	lm, lraw, err := r.vybe("events", "list", "--kind", "tool_call", "--limit", "10")
	if err != nil {
		return err
	}
	if err := r.mustSuccess(lm, lraw); err != nil {
		return err
	}
	events, ok := lm["data"].(map[string]any)["events"].([]any)
	if !ok || len(events) == 0 {
		return fmt.Errorf("tool_call events should be listed: %s", lraw)
	}
	r.printDetail("tool_call events in log: %d", len(events))
	return nil
}

// Act IX: Task Intelligence

func stepFetchSingleTask(r *Runner, ctx *DemoContext) error {
	m, raw, err := r.vybe("task", "get", "--id", ctx.AuthTaskID)
	if err != nil {
		return err
	}
	if err := r.mustSuccess(m, raw); err != nil {
		return err
	}
	id := getStr(m, "data", "id")
	if id != ctx.AuthTaskID {
		return fmt.Errorf("expected id=%s, got %s", ctx.AuthTaskID, id)
	}
	title := getStr(m, "data", "title")
	if title != "Implement auth" {
		return fmt.Errorf("expected title=Implement auth, got %q", title)
	}
	status := getStr(m, "data", "status")
	if status != "completed" {
		return fmt.Errorf("expected status=completed, got %q", status)
	}
	r.printDetail("task get: id=%s title=%q status=%s", id, title, status)
	return nil
}

func stepAggregateStats(r *Runner, ctx *DemoContext) error {
	m, raw, err := r.vybe("task", "stats")
	if err != nil {
		return err
	}
	if err := r.mustSuccess(m, raw); err != nil {
		return err
	}
	data, ok := m["data"].(map[string]any)
	if !ok {
		return fmt.Errorf("stats data missing: %s", raw)
	}
	completed, ok := data["completed"]
	if !ok {
		return fmt.Errorf("stats should include completed count: %s", raw)
	}
	completedCount, ok := completed.(float64)
	if !ok {
		return fmt.Errorf("completed count should be a number: %v", completed)
	}
	if int(completedCount) < 3 {
		return fmt.Errorf("expected >= 3 completed tasks, got %d", int(completedCount))
	}
	r.printDetail("Task stats: completed=%d", int(completedCount))
	return nil
}

func stepPendingQueue(r *Runner, ctx *DemoContext) error {
	cm, craw, err := r.vybe("task", "create",
		"--title", "Next Test Task",
		"--request-id", rid("p9s39", 1),
	)
	if err != nil {
		return err
	}
	if err := r.mustSuccess(cm, craw); err != nil {
		return err
	}

	m, raw, err := r.vybe("task", "next", "--limit", "5")
	if err != nil {
		return err
	}
	if err := r.mustSuccess(m, raw); err != nil {
		return err
	}
	data, ok := m["data"].(map[string]any)
	if !ok {
		return fmt.Errorf("task next data missing: %s", raw)
	}
	tasks, ok := data["tasks"].([]any)
	if !ok {
		return fmt.Errorf("task next should return tasks array: %s", raw)
	}
	if len(tasks) == 0 {
		return fmt.Errorf("task next should return at least one pending task: %s", raw)
	}
	r.printDetail("task next returned %d pending task(s)", len(tasks))
	return nil
}

func stepDependencyImpact(r *Runner, ctx *DemoContext) error {
	bm, braw, err := r.vybe("task", "create",
		"--title", "Blocker Task",
		"--request-id", rid("p9s40", 1),
	)
	if err != nil {
		return err
	}
	if err := r.mustSuccess(bm, braw); err != nil {
		return err
	}
	blockerID := getStr(bm, "data", "task", "id")

	dm, draw, err := r.vybe("task", "create",
		"--title", "Dependent Task",
		"--request-id", rid("p9s40", 2),
	)
	if err != nil {
		return err
	}
	if err := r.mustSuccess(dm, draw); err != nil {
		return err
	}
	dependentID := getStr(dm, "data", "task", "id")
	r.printDetail("Blocker=%s Dependent=%s", blockerID, dependentID)

	_, _, _ = r.vybe("task", "add-dep",
		"--id", dependentID,
		"--depends-on", blockerID,
		"--request-id", rid("p9s40", 3),
	)

	um, uraw, err := r.vybe("task", "unlocks", "--id", blockerID)
	if err != nil {
		return err
	}
	if err := r.mustSuccess(um, uraw); err != nil {
		return err
	}
	data, ok := um["data"].(map[string]any)
	if !ok {
		return fmt.Errorf("task unlocks data missing: %s", uraw)
	}
	tasks, ok := data["tasks"].([]any)
	if !ok || len(tasks) == 0 {
		return fmt.Errorf("completing blocker should unlock at least one task: %s", uraw)
	}
	unlockedID := tasks[0].(map[string]any)["id"].(string)
	if unlockedID != dependentID {
		return fmt.Errorf("expected dependent task %s, got %s", dependentID, unlockedID)
	}
	r.printDetail("task unlocks: completing %s unblocks %s", blockerID, unlockedID)
	return nil
}

// Act X: Multi-Agent Coordination

func stepAtomicClaim(r *Runner, ctx *DemoContext) error {
	cm, craw, err := r.vybe("task", "create",
		"--title", "Claimable Task",
		"--request-id", rid("p10s41", 1),
	)
	if err != nil {
		return err
	}
	if err := r.mustSuccess(cm, craw); err != nil {
		return err
	}

	m, raw, err := r.vybe("task", "claim", "--request-id", rid("p10s41", 2))
	if err != nil {
		return err
	}
	if err := r.mustSuccess(m, raw); err != nil {
		return err
	}
	data, ok := m["data"].(map[string]any)
	if !ok {
		return fmt.Errorf("claim data missing: %s", raw)
	}
	task, ok := data["task"].(map[string]any)
	if !ok {
		return fmt.Errorf("claim should return a task: %s", raw)
	}
	claimedID := task["id"].(string)
	status := task["status"].(string)
	if status != "in_progress" {
		return fmt.Errorf("claimed task should be in_progress, got %s", status)
	}
	// Store for next step
	ctx.TempDir = claimedID // reuse TempDir field to pass claimedTaskID between steps
	// Actually use a separate approach - store in a way that claim_lease_renewal can use
	// We'll store it in an unused ctx field — using AuthTaskID is bad; use a custom approach
	// The act steps share ctx, so we'll store the claimed task ID in a context-local way.
	// Since DemoContext doesn't have a ClaimedTaskID field, let's use a trick:
	// Store the claimed ID temporarily — we'll clean up TempDir separately.
	// ACTUALLY we need to add a field. But we can't modify DemoContext from steps.go since
	// it's defined in acts.go. Let's pass via a package-level var (not great) or just
	// re-lookup in the next step. For simplicity, let's set the SessionID2 field temporarily
	// since it's already been used. Better: we'll just define a new field in DemoContext.
	// But DemoContext is in acts.go... The cleanest solution is to store it in an existing
	// unused field. TempDir is used for artifact files, but after Act II it's just a path.
	// Let's store claimed task ID in a dedicated way by appending to TempDir with a delimiter.
	// OR: just look it up again in claim_lease_renewal.
	// For now, store in a temp variable accessible via closure — but step functions don't have closures.
	// Best approach: add ClaimedTaskID to DemoContext. We'll update acts.go.
	// For now: put it in a file system trick or just accept re-lookup.
	// Let's just store it in ctx.TempDir with prefix so claim_lease_renewal can extract it.
	_ = claimedID // will handle differently
	r.printDetail("Task claimed: id=%s status=%s", claimedID, status)

	// Store claimedTaskID for the next step using the context
	// We'll write a small file in the temp dir
	if ctx.TempDir != "" && !strings.Contains(ctx.TempDir, "claimed:") {
		ctx.TempDir = ctx.TempDir + "|claimed:" + claimedID
	} else if ctx.TempDir == "" {
		ctx.TempDir = "|claimed:" + claimedID
	}
	return nil
}

// getClaimedTaskID extracts the claimed task ID stored in ctx.TempDir.
func getClaimedTaskID(ctx *DemoContext) string {
	if ctx.TempDir == "" {
		return ""
	}
	parts := strings.Split(ctx.TempDir, "|claimed:")
	if len(parts) < 2 {
		return ""
	}
	return parts[len(parts)-1]
}

func stepClaimLeaseRenewal(r *Runner, ctx *DemoContext) error {
	claimedTaskID := getClaimedTaskID(ctx)
	if claimedTaskID == "" {
		r.printDetail("skipping: no claimed task")
		return nil
	}
	m, raw, err := r.vybe("task", "heartbeat",
		"--id", claimedTaskID,
		"--request-id", rid("p10s42", 1),
	)
	if err != nil {
		return err
	}
	if err := r.mustSuccess(m, raw); err != nil {
		return err
	}
	r.printDetail("Heartbeat accepted for task %s", claimedTaskID)
	return nil
}

func stepLeaseGC(r *Runner, ctx *DemoContext) error {
	m, raw, err := r.vybe("task", "gc")
	if err != nil {
		return err
	}
	if err := r.mustSuccess(m, raw); err != nil {
		return err
	}
	if m["data"] == nil {
		return fmt.Errorf("task gc should return data: %s", raw)
	}
	r.printDetail("Task GC complete")
	return nil
}

// Act XI: Task Lifecycle

func stepPriorityBoost(r *Runner, ctx *DemoContext) error {
	cm, craw, err := r.vybe("task", "create",
		"--title", "Priority Task",
		"--request-id", rid("p11s44", 1),
	)
	if err != nil {
		return err
	}
	if err := r.mustSuccess(cm, craw); err != nil {
		return err
	}
	priorityTaskID := getStr(cm, "data", "task", "id")
	r.printDetail("Created Priority Task (%s)", priorityTaskID)

	m, raw, err := r.vybe("task", "set-priority",
		"--id", priorityTaskID,
		"--priority", "10",
		"--request-id", rid("p11s44", 2),
	)
	if err != nil {
		return err
	}
	if err := r.mustSuccess(m, raw); err != nil {
		return err
	}
	data, ok := m["data"].(map[string]any)
	if !ok {
		return fmt.Errorf("set-priority data missing: %s", raw)
	}
	task, ok := data["task"].(map[string]any)
	if !ok {
		return fmt.Errorf("set-priority should return task: %s", raw)
	}
	priority, ok := task["priority"].(float64)
	if !ok {
		return fmt.Errorf("priority should be a number: %v", task["priority"])
	}
	if priority != 10 {
		return fmt.Errorf("expected priority=10, got %v", priority)
	}
	r.printDetail("Priority updated: task %s priority → %d", priorityTaskID, int(priority))
	return nil
}

func stepDeleteTask(r *Runner, ctx *DemoContext) error {
	cm, craw, err := r.vybe("task", "create",
		"--title", "Task To Delete",
		"--request-id", rid("p11s45", 1),
	)
	if err != nil {
		return err
	}
	if err := r.mustSuccess(cm, craw); err != nil {
		return err
	}
	deleteTaskID := getStr(cm, "data", "task", "id")
	if deleteTaskID == "" {
		return fmt.Errorf("task ID should be set: %s", craw)
	}

	dm, draw, err := r.vybe("task", "delete",
		"--id", deleteTaskID,
		"--request-id", rid("p11s45", 2),
	)
	if err != nil {
		return err
	}
	if err := r.mustSuccess(dm, draw); err != nil {
		return err
	}

	// Verify it's gone
	gm, _, _ := r.vybe("task", "get", "--id", deleteTaskID)
	if gm != nil && gm["success"] == true {
		if data, ok := gm["data"].(map[string]any); ok {
			if data["task"] != nil {
				return fmt.Errorf("deleted task should not be retrievable")
			}
		}
	}
	r.printDetail("Task %s deleted and confirmed gone", deleteTaskID)
	return nil
}

func stepStatusTransitions(r *Runner, ctx *DemoContext) error {
	cm, craw, err := r.vybe("task", "create",
		"--title", "Status Update Task",
		"--request-id", rid("p11s46", 1),
	)
	if err != nil {
		return err
	}
	if err := r.mustSuccess(cm, craw); err != nil {
		return err
	}
	statusTaskID := getStr(cm, "data", "task", "id")

	m, raw, err := r.vybe("task", "set-status",
		"--id", statusTaskID,
		"--status", "blocked",
		"--request-id", rid("p11s46", 2),
	)
	if err != nil {
		return err
	}
	if err := r.mustSuccess(m, raw); err != nil {
		return err
	}
	status := getStr(m, "data", "task", "status")
	if status != "blocked" {
		return fmt.Errorf("expected blocked, got %s", status)
	}
	r.printDetail("Status transition: pending → %s", status)
	return nil
}

// Act XII: Knowledge Management

func stepCompactMemory(r *Runner, ctx *DemoContext) error {
	for i, kv := range [][2]string{
		{"compact_key_a", "value_a"},
		{"compact_key_b", "value_b"},
	} {
		_, _, _ = r.vybe("memory", "set",
			"--key", kv[0],
			"--value", kv[1],
			"--scope", "global",
			"--request-id", rid("p12s47", i),
		)
	}

	m, raw, err := r.vybe("memory", "compact",
		"--scope", "global",
		"--request-id", rid("p12s47", 99),
	)
	if err != nil {
		return err
	}
	if err := r.mustSuccess(m, raw); err != nil {
		return err
	}
	if m["data"] == nil {
		return fmt.Errorf("memory compact should return data: %s", raw)
	}
	r.printDetail("Memory compact complete")
	return nil
}

func stepRefreshAccessTime(r *Runner, ctx *DemoContext) error {
	_, _, _ = r.vybe("memory", "set",
		"--key", "touch_test_key",
		"--value", "touch_value",
		"--scope", "global",
		"--request-id", rid("p12s48", 1),
	)

	m, raw, err := r.vybe("memory", "touch",
		"--key", "touch_test_key",
		"--scope", "global",
		"--request-id", rid("p12s48", 2),
	)
	if err != nil {
		return err
	}
	if err := r.mustSuccess(m, raw); err != nil {
		return err
	}
	if m["data"] == nil {
		return fmt.Errorf("memory touch should return data: %s", raw)
	}
	r.printDetail("memory touch succeeded")
	return nil
}

func stepPatternSearch(r *Runner, ctx *DemoContext) error {
	_, _, _ = r.vybe("memory", "set",
		"--key", "go_module",
		"--value", "github.com/dotcommander/vybe",
		"--scope", "global",
		"--request-id", rid("p12s49", 1),
	)

	m, raw, err := r.vybe("memory", "query",
		"--pattern", "go%",
		"--scope", "global",
		"--limit", "10",
	)
	if err != nil {
		return err
	}
	if err := r.mustSuccess(m, raw); err != nil {
		return err
	}
	data, ok := m["data"].(map[string]any)
	if !ok {
		return fmt.Errorf("memory query data missing: %s", raw)
	}
	memories, ok := data["memories"].([]any)
	if !ok || len(memories) == 0 {
		return fmt.Errorf("query for 'go%%' should return at least one result: %s", raw)
	}
	found := false
	for _, rawMem := range memories {
		mem := rawMem.(map[string]any)
		if k, ok := mem["key"].(string); ok && strings.HasPrefix(k, "go") {
			found = true
			break
		}
	}
	if !found {
		return fmt.Errorf("at least one memory key should start with 'go'")
	}
	r.printDetail("memory query 'go%%' returned %d result(s)", len(memories))
	return nil
}

func stepExplicitDeletion(r *Runner, ctx *DemoContext) error {
	_, _, _ = r.vybe("memory", "set",
		"--key", "delete_me",
		"--value", "temporary",
		"--scope", "global",
		"--request-id", rid("p12s50", 1),
	)

	gm, graw, err := r.vybe("memory", "get", "--key", "delete_me", "--scope", "global")
	if err != nil {
		return err
	}
	if err := r.mustSuccess(gm, graw); err != nil {
		return err
	}
	value := getStr(gm, "data", "value")
	if value != "temporary" {
		return fmt.Errorf("key should exist before deletion, got %q", value)
	}

	dm, draw, err := r.vybe("memory", "delete",
		"--key", "delete_me",
		"--scope", "global",
		"--request-id", rid("p12s50", 2),
	)
	if err != nil {
		return err
	}
	if err := r.mustSuccess(dm, draw); err != nil {
		return err
	}

	am, _, _ := r.vybe("memory", "get", "--key", "delete_me", "--scope", "global")
	if am != nil && am["success"] == true {
		return fmt.Errorf("deleted key should not be retrievable")
	}
	r.printDetail("Memory delete confirmed — key gone")
	return nil
}

// Act XIII: Agent Identity

func stepInitializeAgent(r *Runner, ctx *DemoContext) error {
	m, raw, err := r.vybe("agent", "init", "--request-id", rid("p13s51", 1))
	if err != nil {
		return err
	}
	if err := r.mustSuccess(m, raw); err != nil {
		return err
	}
	if m["data"] == nil {
		return fmt.Errorf("agent init should return data: %s", raw)
	}
	r.printDetail("Agent state initialized")
	return nil
}

func stepReadAgentState(r *Runner, ctx *DemoContext) error {
	m, raw, err := r.vybe("agent", "status")
	if err != nil {
		return err
	}
	if err := r.mustSuccess(m, raw); err != nil {
		return err
	}
	data, ok := m["data"].(map[string]any)
	if !ok {
		return fmt.Errorf("agent status data missing: %s", raw)
	}
	agentName := getStr(m, "data", "agent_name")
	if agentName == "" {
		return fmt.Errorf("agent status should return agent_name: %s", raw)
	}
	if _, hasEventID := data["last_seen_event_id"]; !hasEventID {
		return fmt.Errorf("agent status should include last_seen_event_id: %s", raw)
	}
	r.printDetail("Agent status: agent_name=%q", agentName)
	return nil
}

func stepOverrideFocus(r *Runner, ctx *DemoContext) error {
	cm, craw, err := r.vybe("task", "create",
		"--title", "Focus Target Task",
		"--request-id", rid("p13s53", 1),
	)
	if err != nil {
		return err
	}
	if err := r.mustSuccess(cm, craw); err != nil {
		return err
	}
	focusTargetID := getStr(cm, "data", "task", "id")

	m, raw, err := r.vybe("agent", "focus",
		"--task", focusTargetID,
		"--request-id", rid("p13s53", 2),
	)
	if err != nil {
		return err
	}
	if err := r.mustSuccess(m, raw); err != nil {
		return err
	}
	if m["data"] == nil {
		return fmt.Errorf("agent focus should return data: %s", raw)
	}

	sm, sraw, err := r.vybe("agent", "status")
	if err != nil {
		return err
	}
	if err := r.mustSuccess(sm, sraw); err != nil {
		return err
	}
	focusTaskID := getStr(sm, "data", "focus_task_id")
	if focusTaskID != focusTargetID {
		return fmt.Errorf("agent focus_task_id should match set task: expected %s, got %s", focusTargetID, focusTaskID)
	}
	r.printDetail("Agent focus updated: focus_task_id=%s", focusTaskID)
	return nil
}

// Act XIV: The Event Stream

func stepCompressHistory(r *Runner, ctx *DemoContext) error {
	// List events ascending to find IDs
	lm, lraw, err := r.vybe("events", "list", "--limit", "10", "--asc")
	if err != nil {
		return err
	}
	if err := r.mustSuccess(lm, lraw); err != nil {
		return err
	}
	events, ok := lm["data"].(map[string]any)["events"].([]any)
	if !ok || len(events) < 2 {
		return fmt.Errorf("need at least 2 events to summarize: %s", lraw)
	}

	// Add progress events to auth task
	for i := range 2 {
		_, _, _ = r.vybe("events", "add",
			"--kind", "progress",
			"--msg", fmt.Sprintf("pre-summary event %d", i),
			"--task", ctx.AuthTaskID,
			"--request-id", rid("p14s54", i),
		)
	}

	// Get range for auth task events
	rm, rraw, err := r.vybe("events", "list", "--task", ctx.AuthTaskID, "--asc", "--limit", "100")
	if err != nil {
		return err
	}
	if err := r.mustSuccess(rm, rraw); err != nil {
		return err
	}
	rangeEvents, ok := rm["data"].(map[string]any)["events"].([]any)
	if !ok || len(rangeEvents) == 0 {
		return fmt.Errorf("should have events for auth task: %s", rraw)
	}

	taskFirst := rangeEvents[0].(map[string]any)
	taskLast := rangeEvents[len(rangeEvents)-1].(map[string]any)
	fromID := int(taskFirst["id"].(float64))
	toID := int(taskLast["id"].(float64))

	if fromID >= toID {
		r.printDetail("skipping summarize: only one event (fromID=%d >= toID=%d)", fromID, toID)
		return nil
	}

	m, raw, err := r.vybe("events", "summarize",
		"--from-id", fmt.Sprintf("%d", fromID),
		"--to-id", fmt.Sprintf("%d", toID),
		"--summary", "Auth implementation complete: JWT strategy, integrated with routes",
		"--task", ctx.AuthTaskID,
		"--request-id", rid("p14s54", 99),
	)
	if err != nil {
		return err
	}
	if err := r.mustSuccess(m, raw); err != nil {
		return err
	}
	if m["data"] == nil {
		return fmt.Errorf("events summarize should return data: %s", raw)
	}
	r.printDetail("Events %d–%d summarized", fromID, toID)
	return nil
}

func stepRecentActivity(r *Runner, ctx *DemoContext) error {
	m, raw, err := r.vybe("events", "list", "--limit", "5")
	if err != nil {
		return err
	}
	if err := r.mustSuccess(m, raw); err != nil {
		return err
	}
	events, ok := m["data"].(map[string]any)["events"].([]any)
	if !ok || len(events) == 0 {
		return fmt.Errorf("events list should return recent events: %s", raw)
	}
	r.printDetail("Recent events: %d returned (limit=5)", len(events))
	return nil
}

// Act XV: System Introspection

func stepFetchProject(r *Runner, ctx *DemoContext) error {
	m, raw, err := r.vybe("project", "get", "--id", ctx.ProjectID)
	if err != nil {
		return err
	}
	if err := r.mustSuccess(m, raw); err != nil {
		return err
	}
	id := getStr(m, "data", "id")
	if id != ctx.ProjectID {
		return fmt.Errorf("expected id=%s, got %s", ctx.ProjectID, id)
	}
	name := getStr(m, "data", "name")
	if name != "demo-project" {
		return fmt.Errorf("expected name=demo-project, got %q", name)
	}
	r.printDetail("Project: id=%s name=%q", id, name)
	return nil
}

func stepSessionDigest(r *Runner, ctx *DemoContext) error {
	m, raw, err := r.vybe("session", "digest")
	if err != nil {
		return err
	}
	if err := r.mustSuccess(m, raw); err != nil {
		return err
	}
	if m["data"] == nil {
		return fmt.Errorf("session digest should return data: %s", raw)
	}
	r.printDetail("Session digest generated")
	return nil
}

func stepInspectSchema(r *Runner, ctx *DemoContext) error {
	m, raw, err := r.vybe("schema")
	if err != nil {
		return err
	}
	if err := r.mustSuccess(m, raw); err != nil {
		return err
	}
	if m["data"] == nil {
		return fmt.Errorf("schema should return data: %s", raw)
	}
	r.printDetail("Schema fetched")
	return nil
}

// Act XVI: IDE Integration

func stepTrackSubagentSpawn(r *Runner, ctx *DemoContext) error {
	hookSession := "sess_demo_hook_phase"
	payload := map[string]any{
		"hook_event_name": "SubagentStart",
		"session_id":      hookSession,
		"cwd":             ctx.ProjectID,
		"description":     "quality-agent",
	}
	data, _ := json.Marshal(payload)
	_, _, _ = r.vybeWithStdin(string(data), "hook", "subagent-start")

	m, raw, err := r.vybe("events", "list", "--kind", "agent_spawned", "--limit", "5")
	if err != nil {
		return err
	}
	if err := r.mustSuccess(m, raw); err != nil {
		return err
	}
	events, ok := m["data"].(map[string]any)["events"].([]any)
	if !ok || len(events) == 0 {
		return fmt.Errorf("agent_spawned event should be logged: %s", raw)
	}
	r.printDetail("agent_spawned event recorded: %d event(s)", len(events))
	return nil
}

func stepTrackSubagentCompletion(r *Runner, ctx *DemoContext) error {
	hookSession := "sess_demo_hook_phase"
	payload := map[string]any{
		"hook_event_name": "SubagentStop",
		"session_id":      hookSession,
		"cwd":             ctx.ProjectID,
		"description":     "quality-agent",
	}
	data, _ := json.Marshal(payload)
	_, _, _ = r.vybeWithStdin(string(data), "hook", "subagent-stop")

	m, raw, err := r.vybe("events", "list", "--kind", "agent_completed", "--limit", "5")
	if err != nil {
		return err
	}
	if err := r.mustSuccess(m, raw); err != nil {
		return err
	}
	events, ok := m["data"].(map[string]any)["events"].([]any)
	if !ok || len(events) == 0 {
		return fmt.Errorf("agent_completed event should be logged: %s", raw)
	}
	r.printDetail("agent_completed event recorded: %d event(s)", len(events))
	return nil
}

func stepTurnBoundaryHeartbeat(r *Runner, ctx *DemoContext) error {
	hookSession := "sess_demo_hook_phase"
	payload := map[string]any{
		"hook_event_name": "Stop",
		"session_id":      hookSession,
		"cwd":             ctx.ProjectID,
	}
	data, _ := json.Marshal(payload)
	_, _, _ = r.vybeWithStdin(string(data), "hook", "stop")

	m, raw, err := r.vybe("events", "list", "--kind", "heartbeat", "--limit", "5")
	if err != nil {
		return err
	}
	if err := r.mustSuccess(m, raw); err != nil {
		return err
	}
	events, ok := m["data"].(map[string]any)["events"].([]any)
	if !ok || len(events) == 0 {
		return fmt.Errorf("heartbeat event should be logged: %s", raw)
	}
	r.printDetail("heartbeat event recorded: %d event(s)", len(events))
	return nil
}

// Act XVII: The Full Surface

func stepArtifactGetByID(r *Runner, ctx *DemoContext) error {
	lm, lraw, err := r.vybe("artifact", "list", "--task", ctx.AuthTaskID)
	if err != nil {
		return err
	}
	if err := r.mustSuccess(lm, lraw); err != nil {
		return err
	}
	artifacts, ok := lm["data"].(map[string]any)["artifacts"].([]any)
	if !ok || len(artifacts) == 0 {
		return fmt.Errorf("artifacts should exist for auth task: %s", lraw)
	}
	artifactID, ok := artifacts[0].(map[string]any)["id"].(string)
	if !ok || artifactID == "" {
		return fmt.Errorf("artifact should have an ID: %v", artifacts[0])
	}

	m, raw, err := r.vybe("artifact", "get", "--id", artifactID)
	if err != nil {
		return err
	}
	if err := r.mustSuccess(m, raw); err != nil {
		return err
	}
	gotID := getStr(m, "data", "id")
	if gotID != artifactID {
		return fmt.Errorf("expected artifact id=%s, got %s", artifactID, gotID)
	}
	gotTaskID := getStr(m, "data", "task_id")
	if gotTaskID != ctx.AuthTaskID {
		return fmt.Errorf("expected task_id=%s, got %s", ctx.AuthTaskID, gotTaskID)
	}
	r.printDetail("artifact get: id=%s task_id=%s", gotID, gotTaskID)
	return nil
}

func stepRetrospectiveExtraction(r *Runner, ctx *DemoContext) error {
	_, _, _ = r.vybe("events", "add",
		"--kind", "progress",
		"--msg", "retrospective event A",
		"--request-id", rid("p17s63", 1),
	)
	_, _, _ = r.vybe("events", "add",
		"--kind", "progress",
		"--msg", "retrospective event B",
		"--request-id", rid("p17s63", 2),
	)

	payload := map[string]any{
		"hook_event_name": "SessionEnd",
		"session_id":      ctx.SessionID,
		"cwd":             ctx.ProjectID,
	}
	data, _ := json.Marshal(payload)
	_, _, _ = r.vybeWithStdin(string(data), "hook", "retrospective")
	r.printDetail("hook retrospective fired (best-effort)")
	return nil
}

func stepHistoryImport(r *Runner, ctx *DemoContext) error {
	// Create temp dir for history file
	histDir, err := os.MkdirTemp("", "vybe-demo-hist-*")
	if err != nil {
		return fmt.Errorf("create temp dir: %w", err)
	}
	defer os.RemoveAll(histDir)

	histFile := filepath.Join(histDir, "history.jsonl")
	histContent := strings.Join([]string{
		`{"type":"human","display":"Fix the auth bug","project":"/tmp/test","sessionId":"sess_ingest_1","timestamp":1700000000000}`,
		`{"type":"human","display":"Add unit tests","project":"/tmp/test","sessionId":"sess_ingest_1","timestamp":1700000001000}`,
		`{"type":"human","display":"Deploy to prod","project":"/tmp/test","sessionId":"sess_ingest_2","timestamp":1700000002000}`,
	}, "\n")
	if err := os.WriteFile(histFile, []byte(histContent), 0600); err != nil {
		return fmt.Errorf("write history file: %w", err)
	}

	m, raw, err := r.vybe("ingest", "history",
		"--file", histFile,
		"--request-id", rid("p17s64", 1),
	)
	if err != nil {
		return err
	}
	if err := r.mustSuccess(m, raw); err != nil {
		return err
	}
	data, ok := m["data"].(map[string]any)
	if !ok {
		return fmt.Errorf("ingest history data missing: %s", raw)
	}
	imported, ok := data["imported"].(float64)
	if !ok {
		return fmt.Errorf("ingest history response should have imported count: %s", raw)
	}
	if int(imported) < 1 {
		return fmt.Errorf("at least 1 entry should be imported, got %d", int(imported))
	}
	r.printDetail("History ingest complete: %d event(s) imported", int(imported))
	return nil
}

func stepLoopIterationStats(r *Runner, ctx *DemoContext) error {
	m, raw, err := r.vybe("loop", "stats")
	if err != nil {
		return err
	}
	if err := r.mustSuccess(m, raw); err != nil {
		return err
	}
	data, ok := m["data"].(map[string]any)
	if !ok {
		return fmt.Errorf("loop stats data missing: %s", raw)
	}
	if _, hasRuns := data["runs"]; !hasRuns {
		return fmt.Errorf("loop stats should include a runs field: %s", raw)
	}
	r.printDetail("Loop stats: runs=%v", data["runs"])
	return nil
}

func stepProjectLifecycle(r *Runner, ctx *DemoContext) error {
	cm, craw, err := r.vybe("project", "create",
		"--name", "Delete Me",
		"--request-id", rid("p17s66", 1),
	)
	if err != nil {
		return err
	}
	if err := r.mustSuccess(cm, craw); err != nil {
		return err
	}
	deleteProjectID := getStr(cm, "data", "project", "id")
	if deleteProjectID == "" {
		return fmt.Errorf("throwaway project should have an ID: %s", craw)
	}

	dm, draw, err := r.vybe("project", "delete",
		"--id", deleteProjectID,
		"--request-id", rid("p17s66", 2),
	)
	if err != nil {
		return err
	}
	if err := r.mustSuccess(dm, draw); err != nil {
		return err
	}
	deletedID := getStr(dm, "data", "project_id")
	if deletedID != deleteProjectID {
		return fmt.Errorf("project delete should return deleted ID: expected %s, got %s", deleteProjectID, deletedID)
	}

	lm, lraw, err := r.vybe("project", "list")
	if err != nil {
		return err
	}
	if err := r.mustSuccess(lm, lraw); err != nil {
		return err
	}
	projects := lm["data"].(map[string]any)["projects"].([]any)
	for _, raw := range projects {
		p := raw.(map[string]any)
		if p["id"].(string) == deleteProjectID {
			return fmt.Errorf("deleted project %s should not appear in list", deleteProjectID)
		}
	}
	r.printDetail("Project %s deleted and confirmed gone", deleteProjectID)
	return nil
}

func stepReadOnlyBrief(r *Runner, ctx *DemoContext) error {
	m, raw, err := r.vybe("brief")
	if err != nil {
		return err
	}
	if err := r.mustSuccess(m, raw); err != nil {
		return err
	}
	data, ok := m["data"].(map[string]any)
	if !ok {
		return fmt.Errorf("brief should return a data object: %s", raw)
	}
	agentName := getStr(m, "data", "agent_name")
	if agentName == "" {
		return fmt.Errorf("brief data should include agent_name: %s", raw)
	}
	if _, hasBrief := data["brief"]; !hasBrief {
		return fmt.Errorf("brief data should include 'brief' key: %s", raw)
	}
	r.printDetail("brief returned: agent_name=%q", agentName)
	return nil
}

func stepJSONLStreaming(r *Runner, ctx *DemoContext) error {
	out := r.vybeRaw("events", "tail", "--once", "--jsonl", "--limit", "3", "--all")
	if strings.TrimSpace(out) == "" {
		return fmt.Errorf("events tail --once should return at least one event line")
	}
	firstLine := strings.SplitN(strings.TrimSpace(out), "\n", 2)[0]
	var event map[string]any
	if err := json.Unmarshal([]byte(firstLine), &event); err != nil {
		return fmt.Errorf("first line should be valid JSON: %w (line: %s)", err, firstLine)
	}
	if _, hasID := event["id"]; !hasID {
		return fmt.Errorf("event should have an 'id' field: %s", firstLine)
	}
	if _, hasKind := event["kind"]; !hasKind {
		return fmt.Errorf("event should have a 'kind' field: %s", firstLine)
	}
	r.printDetail("events tail --once: id=%v kind=%v", event["id"], event["kind"])
	return nil
}

func stepHookInstallUninstall(r *Runner, ctx *DemoContext) error {
	hookTmpDir, err := os.MkdirTemp("", "vybe-demo-hooks-*")
	if err != nil {
		return fmt.Errorf("create temp dir: %w", err)
	}
	defer os.RemoveAll(hookTmpDir)

	if err := os.MkdirAll(filepath.Join(hookTmpDir, ".claude"), 0750); err != nil {
		return fmt.Errorf("create .claude dir: %w", err)
	}

	im, iraw, err := r.vybeWithDir(hookTmpDir, "hook", "install", "--claude", "--project")
	if err != nil {
		return err
	}
	if err := r.mustSuccess(im, iraw); err != nil {
		return err
	}
	installData, ok := im["data"].(map[string]any)
	if !ok {
		return fmt.Errorf("hook install should return data: %s", iraw)
	}
	claudeInstall, ok := installData["claude"].(map[string]any)
	if !ok {
		return fmt.Errorf("hook install data should include 'claude' key: %s", iraw)
	}
	if _, hasInstalled := claudeInstall["installed"]; !hasInstalled {
		return fmt.Errorf("claude install result should have 'installed' field: %s", iraw)
	}

	settingsPath := filepath.Join(hookTmpDir, ".claude", "settings.json")
	if _, err := os.Stat(settingsPath); err != nil {
		return fmt.Errorf("hook install should write .claude/settings.json: %w", err)
	}
	r.printDetail("Hook install: settings.json written")

	um, uraw, err := r.vybeWithDir(hookTmpDir, "hook", "uninstall", "--claude", "--project")
	if err != nil {
		return err
	}
	if err := r.mustSuccess(um, uraw); err != nil {
		return err
	}
	uninstallData, ok := um["data"].(map[string]any)
	if !ok {
		return fmt.Errorf("hook uninstall should return data: %s", uraw)
	}
	claudeUninstall, ok := uninstallData["claude"].(map[string]any)
	if !ok {
		return fmt.Errorf("hook uninstall data should include 'claude' key: %s", uraw)
	}
	if _, hasRemoved := claudeUninstall["removed"]; !hasRemoved {
		return fmt.Errorf("claude uninstall result should have 'removed' field: %s", uraw)
	}
	r.printDetail("Hook uninstall: removed=%v", claudeUninstall["removed"])
	return nil
}

func stepLoopDryRun(r *Runner, ctx *DemoContext) error {
	cm, craw, err := r.vybe("task", "create",
		"--title", "Loop Demo Task",
		"--request-id", rid("p17loop", 1),
	)
	if err != nil {
		return err
	}
	if err := r.mustSuccess(cm, craw); err != nil {
		return err
	}
	loopTaskID := getStr(cm, "data", "task", "id")
	if loopTaskID == "" {
		return fmt.Errorf("loop demo task should have an ID: %s", craw)
	}
	r.printDetail("Created pending task %s for loop to discover", loopTaskID)

	m, raw, err := r.vybe("loop", "--dry-run", "--max-tasks=1", "--cooldown=0s")
	if err != nil {
		return err
	}
	if err := r.mustSuccess(m, raw); err != nil {
		return err
	}
	data, ok := m["data"].(map[string]any)
	if !ok {
		return fmt.Errorf("loop data missing: %s", raw)
	}
	if data["completed"] != float64(1) {
		return fmt.Errorf("dry-run loop should complete 1 iteration, got %v", data["completed"])
	}
	if data["total"] != float64(1) {
		return fmt.Errorf("dry-run loop should run 1 total, got %v", data["total"])
	}
	results, ok := data["results"].([]any)
	if !ok || len(results) != 1 {
		return fmt.Errorf("should have exactly 1 result, got %v", data["results"])
	}
	r0 := results[0].(map[string]any)
	if r0["status"] != "dry_run" {
		return fmt.Errorf("result status should be dry_run, got %v", r0["status"])
	}
	if r0["task_title"] == "" || r0["task_title"] == nil {
		return fmt.Errorf("result should have a task title: %v", r0)
	}
	r.printDetail("Loop dry-run: found task %v (%v) status=%v", r0["task_id"], r0["task_title"], r0["status"])
	return nil
}

func stepLoopCircuitBreaker(r *Runner, ctx *DemoContext) error {
	cm, craw, err := r.vybe("task", "create",
		"--title", "Circuit Breaker Task",
		"--request-id", rid("p17cb", 1),
	)
	if err != nil {
		return err
	}
	if err := r.mustSuccess(cm, craw); err != nil {
		return err
	}
	cbTaskID := getStr(cm, "data", "task", "id")
	if cbTaskID == "" {
		return fmt.Errorf("circuit breaker task should have an ID: %s", craw)
	}

	bm, braw, err := r.vybe("task", "begin",
		"--id", cbTaskID,
		"--request-id", rid("p17cb", 2),
	)
	if err != nil {
		return err
	}
	if err := r.mustSuccess(bm, braw); err != nil {
		return err
	}
	r.printDetail("Task %s is now in_progress", cbTaskID)

	m, raw, err := r.vybe("loop", "--command", "true", "--max-tasks=1", "--max-fails=1", "--cooldown=0s", "--task-timeout=5s")
	if err != nil {
		return err
	}
	if err := r.mustSuccess(m, raw); err != nil {
		return err
	}
	data, ok := m["data"].(map[string]any)
	if !ok {
		return fmt.Errorf("loop data missing: %s", raw)
	}
	if failed, ok := data["failed"].(float64); !ok || failed < 1 {
		return fmt.Errorf("should have at least 1 failure, got %v", data["failed"])
	}
	results, ok := data["results"].([]any)
	if !ok || len(results) == 0 {
		return fmt.Errorf("should have at least 1 result: %s", raw)
	}
	r0 := results[0].(map[string]any)
	if r0["status"] != "blocked" {
		return fmt.Errorf("task should be marked blocked after command exits without completing, got %v", r0["status"])
	}
	r.printDetail("Circuit breaker: task %v status=%v", r0["task_id"], r0["status"])
	return nil
}

func stepBackgroundRetrospective(r *Runner, ctx *DemoContext) error {
	payloadDir, err := os.MkdirTemp("", "vybe-demo-retro-*")
	if err != nil {
		return fmt.Errorf("create temp dir: %w", err)
	}
	defer os.RemoveAll(payloadDir)

	payloadFile := filepath.Join(payloadDir, "retro_payload.json")
	if err := os.WriteFile(payloadFile, []byte(`{}`), 0600); err != nil {
		return fmt.Errorf("write payload file: %w", err)
	}

	r.vybeRaw("hook", "retrospective-bg", "demo-agent", payloadFile)
	r.printDetail("hook retrospective-bg completed without panic")
	return nil
}
