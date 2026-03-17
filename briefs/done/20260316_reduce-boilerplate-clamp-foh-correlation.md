# Brief: Reduce Boilerplate — Clamp, FOH Error Results, Correlation

**Date:** 20260316
**Status:** `done`
**Branch:** `feature/reduce-boilerplate-clamp-foh-correlation`
**Worktree:** `../jot-reduce-boilerplate`

---

## Goal

Three mechanical, high-confidence code reduction passes that eliminate repeated identical code blocks without changing any behaviour.

---

## Scope

**In:**
1. **Clamp consolidation** — 7 private identical clamp functions in `internal/tools/impl/` → single `clampInt` in `helpers.go`
2. **FOH error result helper** — 5 repeated `&QueryResult{Error: true, ...}` blocks in `foh.go` → `errQueryResult(answer, iteration, debugLogs)` helper
3. **Correlation boilerplate** — 4 handlers in `handler_tasks.go` each embed `TaskID`/`ParentTraceID` fields + `infra.WithCorrelation` call → embedded `correlationFields` struct with `applyToCtx` method

**Out:**
- Any change to logic, behaviour, or error messages
- Changes to other files not mentioned above

---

## Approach & Key Decisions

### 1. Clamp
Add to `internal/tools/impl/helpers.go`:
```go
func clampInt(val, def, min, max int) int {
    if val == 0 { val = def }
    if val < min { return min }
    if val > max { return max }
    return val
}
```
Delete the 7 private versions. Rename all call sites to `clampInt(...)`.

### 2. FOH error helper
Add to `internal/agent/foh.go` (before `RunQueryWithDebug`):
```go
func errQueryResult(answer string, iteration int, debugLogs []string) *QueryResult {
    infra.ErrorsTotal.Inc()
    return &QueryResult{Answer: answer, Iterations: iteration, Error: true, DebugLogs: debugLogs}
}
```
The `span.RecordError` call must remain at each site (it needs the local span), so only the struct literal is collapsed. Each call site becomes:
```go
span.RecordError(err)
return errQueryResult(fmt.Sprintf("Error ...: %v", err), iteration, debugLogs)
```

### 3. Correlation
Add to `internal/api/handler_tasks.go` (or a new small file `internal/api/correlation.go`):
```go
type correlationFields struct {
    TaskID        string `json:"task_id"`
    ParentTraceID string `json:"parent_trace_id"`
}

func (c correlationFields) applyToCtx(ctx context.Context) context.Context {
    if c.TaskID != "" || c.ParentTraceID != "" {
        return infra.WithCorrelation(ctx, c.TaskID, c.ParentTraceID)
    }
    return ctx
}
```
Each handler's anonymous request struct embeds `correlationFields`. The `if data.TaskID != ""...` block becomes `ctx = data.correlationFields.applyToCtx(ctx)`.

---

## Edge Cases & Pre-Flight Checks

1. **Clamp rename**: confirm no other package imports any of the private clamp functions (they're all unexported, so this is safe by definition).
2. **FOH `app == nil` case**: that early return has no `span.RecordError` and `debugLogs` is nil — `errQueryResult` handles nil slice fine.
3. **Embedded struct JSON tags**: Go's `encoding/json` flattens embedded structs, so `correlationFields` fields appear at the top level of the JSON object as expected.

---

## Affected Areas

- [ ] Agent / FOH loop — `foh.go` touched but logic unchanged
- [ ] Tools — `internal/tools/impl/helpers.go` and 7 tool files touched
- [x] API routes — `handler_tasks.go` or new `correlation.go`

---

## Checklist

**Implementation**
- [ ] Errors wrapped with `%w`, not `%v`
- [ ] No file exceeds 400 lines

**Verification (Proof of Work)**
- [ ] `go build ./...` passes cleanly
- [ ] `go test ./...` passes
- [ ] `go vet ./...` passes

**Wrap-up**
- [ ] Brief status set to `done` and file moved to `briefs/done/`

---

## Key Files

- `briefs/active/20260316_reduce-boilerplate-clamp-foh-correlation.md`
- `internal/tools/impl/helpers.go`
- `internal/tools/impl/journal_tools.go`, `memory_tools.go`, `query_tools.go`, `web_tools.go`, `task_tools.go`, `github_tools.go`, `context_tools.go`
- `internal/agent/foh.go`
- `internal/api/handler_tasks.go`

---

## Session Log

<!-- 20260316 session 2 -->
- Implemented all three. `go build`, `go test`, `go vet` all pass. Net: -96 lines (82 insertions, 178 deletions).

<!-- 20260316 session 1 -->
- Brief created. Three targets: clamp consolidation (7 files → 1 helper), FOH error result helper (5 sites), correlation embedded struct (4 handlers).
