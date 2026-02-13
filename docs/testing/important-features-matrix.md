# Important Features Coverage Matrix

This matrix tracks end-to-end coverage for continuity-critical behavior.

Use this flow when reviewing test risk quickly:

1. Check `Core Workflows`
2. Check `Command Surface`
3. Add missing tests before changing behavior

## Core Workflows

| Feature | Primary APIs | Test Coverage | Status |
| --- | --- | --- | --- |
| Task lifecycle (create, status, start, claim, close, deps) | `actions.Task*`, `store.StartTaskAndFocus*`, `store.ClaimNextTaskTx`, `store.CloseTaskTx`, `store.UpdateTaskStatusWithEventTx` | `internal/actions/task_test.go`, `internal/store/important_features_test.go`, `internal/store/task_claim_next_test.go`, `internal/store/task_close_test.go` | Covered |
| Agent focus + cursor progression | `store.SetAgentFocus*`, `store.UpdateAgentStateAtomic*`, `store.LoadAgentCursorAndFocusTx` | `internal/store/important_features_test.go`, `internal/store/agent_state_test.go` | Covered |
| Resume + brief continuity packet | `actions.Resume*`, `store.BuildBrief`, `store.FetchEventsSince` | `internal/actions/resume_test.go`, `internal/actions/resume_integration_test.go`, `internal/store/resume_test.go` | Covered |
| Events append + archive + metadata variants | `store.AppendEvent*`, `store.ArchiveEventsRangeWithSummaryIdempotent`, `store.InsertEventWithProjectTx` | `internal/store/events_test.go`, `internal/store/important_features_test.go` | Covered |
| Memory lifecycle + compaction + GC | `actions.Memory*`, `store.UpsertMemoryWithEventIdempotent`, `store.CompactMemoryWithEventIdempotent`, `store.GCMemoryWithEventIdempotent` | `internal/actions/memory_test.go`, `internal/actions/important_features_test.go`, `internal/store/memory_test.go` | Covered |
| Project lifecycle + focus | `actions.Project*`, `store.CreateProject*`, `store.SetAgentFocusProject*` | `internal/actions/project_focus_test.go`, `internal/actions/important_features_test.go`, `internal/store/projects_test.go` | Covered |
| Artifacts lifecycle | `actions.Artifact*`, `store.AddArtifact*`, `store.ListArtifactsByTask` | `internal/actions/important_features_test.go`, `internal/store/artifacts_test.go` | Covered |

## Command Surface (Validation + Wiring)

| Command Group | Coverage Focus | Test Files | Status |
| --- | --- | --- | --- |
| `task` | Required flags, conflict args, dependency validation | `internal/commands/task_test.go` | Covered |
| `memory` | Required flags, duration parsing errors, identity requirements | `internal/commands/memory_test.go` | Covered |
| `events` | Agent/all rules, summarize required args | `internal/commands/events_test.go` | Covered |
| `project` | Required args + identity checks | `internal/commands/project_test.go` | Covered |
| `artifact` | Required args + identity checks | `internal/commands/artifact_test.go` | Covered |
| `agent` | Focus arg rules + identity checks | `internal/commands/agent_test.go` | Covered |

## Update Rule

If behavior changes in any row above, update this file and add/adjust tests
in the listed locations.
