# Brief: Unified Task & Project Engine Integration

**Date:** 20260318
**Status:** `done`
**Branch:** `feature/unified-task-engine`
**Worktree:** `../jot-unified-task-engine`

---

## Goal

Deprecate the legacy one-shot `planner.go` and merge its logic into `pkg/task`. Transform the Task system into a "Project Engine" that actively guides the user through complex multi-step goals. A "project" is just a task with subtasks (parent_id == "").

---

## Scope

**In:**
- Add `Dependencies []string` and `IsSequential bool` fields to `pkg/task/schema.go`
- Create `pkg/task/engine.go` with `BrainstormSubtasks` function
- Add `decompose_task` tool in `internal/tools/impl/task_tools.go`
- Inject active project subtask tree into system prompt in `internal/agent/prompter.go`
- Delete `internal/agent/planner.go`
- Remove `CreateAndSavePlan` wrapper from `internal/service/query_agent.go`
- Remove `generate_plan` tool from `internal/tools/impl/memory_tools.go`
- Remove `POST /plan` route from `internal/api/router.go` and `handlePlan` from handler
- Remove `CreateAndSavePlan` from `AgentService` interface and implementation
- Update `app_capabilities.txt` and `.cursorrules`

**Out:**
- No changes to Firestore index config (no new composite indexes needed)
- No changes to dream/memory pipeline

---

## Approach & Key Decisions

1. Schema gets `dependencies` ([]string) and `is_sequential` (bool) fields — Firestore tags
2. `engine.go` implements `BrainstormSubtasks`: fetches parent task, calls Gemini with K/V output format, creates child tasks
3. `decompose_task` tool delegates to `BrainstormSubtasks`
4. Prompter: find the most recently touched task that has children → inject subtree block
5. Remove all planner code and /plan route

---

## Edge Cases & Pre-Flight Checks

1. GetChildTasks needs to iterate tasks by parent_id — no composite index needed since we just filter in-memory after a small ordered query
2. Active project injection in system prompt must be bounded in token size
3. Removing /plan route is a breaking API change — noted in brief (acceptable per brief spec)

---

## Affected Areas

- [x] Agent / FOH loop — adding project context injection to system prompt
- [x] Tools — new `decompose_task` tool, removing `generate_plan`
- [x] Prompts / `app_capabilities.txt` — updating capabilities
- [ ] Firestore schema or queries — schema fields added but no new indexes
- [x] API routes or cron jobs — removing POST /plan

---

## Checklist

**Implementation**
- [ ] New code passes `*infra.App` explicitly — no `infra.GetApp(ctx)` in new code
- [ ] All logging uses `LoggerFrom(ctx)` — no `fmt.Print` or raw `slog`
- [ ] Debug logs pass full strings — no truncation at Debug level
- [ ] User-origin strings wrapped with `WrapAsUserData()` in any prompt
- [ ] LLM output parsed as key/value lines via `pkg/utils.ParseKeyValueMap` (no JSON)
- [ ] Every significant agentic step has `StartSpan` / `defer span.End()`
- [ ] Errors wrapped with `%w`, not `%v`
- [ ] No file exceeds 400 lines

**Verification (Proof of Work)**
- [ ] **Compilation:** `go build ./...` passes cleanly.
- [ ] **Tests:** `go test ./...` passes.
- [ ] **Lint/Format:** Code formatted and passes `go vet`.

**Wrap-up**
- [ ] `app_capabilities.txt` updated
- [ ] Tests added / updated
- [ ] Brief status set to `done` and file moved to `briefs/done/`

---

## Key Files

- `briefs/active/20260318_unified-task-engine.md` (this file)
- `pkg/task/schema.go`
- `pkg/task/engine.go` (new)
- `pkg/task/tasks.go`
- `internal/agent/planner.go` (delete)
- `internal/agent/prompter.go`
- `internal/tools/impl/task_tools.go`
- `internal/tools/impl/memory_tools.go`
- `internal/service/query_agent.go`
- `internal/service/agent_service.go`
- `internal/api/router.go`
- `internal/api/handler_interact.go`
- `internal/api/backend.go`
- `internal/prompts/app_capabilities.txt`

---

## Session Log

<!-- 20260318 -->
- Created brief and worktree; beginning implementation
