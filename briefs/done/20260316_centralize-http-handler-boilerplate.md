# Brief: Centralize HTTP Handler Boilerplate

**Date:** 20260316
**Status:** `done`
**Branch:** `feature/centralize-http-handler-boilerplate`
**Worktree:** `../jot-centralize-http-handler-boilerplate`

---

## Goal

Every handler in `internal/api/handler_*.go` manually repeats the same setup: pulling the context, logging the request, checking the HTTP method, decoding/validating the body, executing business logic, and writing a JSON response. This creates inconsistency and makes every handler a potential place for divergence or bugs. The fix is a `wrapAPI` middleware that standardizes this lifecycle so handlers only express business logic.

---

## Scope

**In:**
- New `APIHandler` type and `wrapAPI` wrapper in `internal/api/`
- Refactor all `handler_*.go` files to use `wrapAPI`
- Centralize method enforcement, request logging, JSON encoding, and error responses inside the wrapper

**Out:**
- Changes to business logic in any handler
- Changes to routing, middleware chain, or `*infra.App` wiring
- `handler_sms.go` and `handler_telegram.go` if they have webhook-specific response semantics that can't fit the `(data any, err error)` pattern without extra care

---

## Approach & Key Decisions

Define the handler contract in `internal/api/api_handler.go`:

```go
type APIHandler func(w http.ResponseWriter, r *http.Request) (data any, err error)

func wrapAPI(method string, h APIHandler) http.HandlerFunc {
    return func(w http.ResponseWriter, r *http.Request) {
        ctx := r.Context()
        path := pathForLog(r.URL.Path)
        LogHandlerRequest(ctx, r.Method, path)

        if r.Method != method {
            http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
            return
        }

        data, err := h(w, r)
        if err != nil {
            LogHandlerResponse(ctx, r.Method, path, http.StatusInternalServerError)
            WriteJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
            return
        }
        LogHandlerResponse(ctx, r.Method, path, http.StatusOK)
        WriteJSON(w, http.StatusOK, data)
    }
}
```

Handlers that need `DecodeAndValidate` call it themselves at the top of the handler func (it's business-logic input parsing, not boilerplate). The wrapper only owns: context, logging, method check, and final write.

For handlers that must write a non-200 success (e.g., 202 Accepted), consider returning a typed response struct that carries the status code, or provide a `wrapAPIWithStatus` variant. Decide per-handler during implementation.

---

## Edge Cases & Pre-Flight Checks

1. **Webhook handlers (SMS, Telegram):** These may need to respond with 200 + empty body immediately regardless of processing outcome. They may not fit `wrapAPI` cleanly — evaluate whether to wrap or leave them as-is and document the exception.
2. **Non-JSON handlers (health check):** `handler_health.go` likely returns plain text. Either exclude it from `wrapAPI` or have the wrapper detect a `nil` data response and write no body.
3. **Partial error responses:** Some handlers may write a partial response before returning an error. The wrapper can't un-write headers. Ensure handlers do not write to `w` directly; all writes go through the wrapper's final step.

---

## Affected Areas

- [ ] Agent / FOH loop — review `blueprint.md` before changing
- [ ] Tools — register via `tools.Register()` in `init()`, co-locate by domain
- [ ] Prompts / `app_capabilities.txt` — update if Jot's capabilities change
- [ ] Firestore schema or queries — update `firestore.indexes.json` if new composite indexes needed
- [ ] New dependencies / infra clients — pass via `*infra.App`, never hidden in context
- [x] API routes or cron jobs — refactoring handler wiring
- [ ] Memory / journal behavior (Gold vs Gravel semantics)

---

## Open Questions

- [x] Do `handler_sms.go` and `handler_telegram.go` get wrapped or exempted? → **Exempted** (respond-before-async pattern incompatible with wrapAPI)
- [x] Is a typed `APIResponse{Status int; Data any}` return better than `(data any, err error)` for non-200 success cases? → **Used `withStatus(code, data) any`** — lightweight sentinel, no separate type needed

---

## Checklist

**Implementation**
- [x] New code passes `*infra.App` explicitly — no `infra.GetApp(ctx)` in new code
- [x] All logging uses `LoggerFrom(ctx)` — no `fmt.Print` or raw `slog`
- [x] Debug logs pass full strings — no truncation at Debug level
- [x] User-origin strings wrapped with `WrapAsUserData()` in any prompt
- [x] LLM output parsed as key/value lines via `pkg/utils.ParseKeyValueMap` (no JSON)
- [x] Every significant agentic step has `StartSpan` / `defer span.End()`
- [x] Errors wrapped with `%w`, not `%v`
- [x] No file exceeds 400 lines

**Firestore (if applicable)**
- [x] N/A — no Firestore changes

**Verification (Proof of Work)**
- [x] **Compilation:** `go build ./...` passes cleanly.
- [x] **Tests:** `go test ./...` passes — all packages green.
- [x] **Lint/Format:** `go vet ./...` passes.
- [x] **Manual Smoke Test:** N/A — pure refactor, no behaviour changes.

**Wrap-up**
- [x] `app_capabilities.txt` — no capability changes
- [x] `blueprint.md` — core agentic loop not touched
- [x] Tests unchanged and passing
- [x] Brief status set to `done` and file moved to `briefs/done/`

---

## Key Files

- `briefs/active/20260316_centralize-http-handler-boilerplate.md` (this file)
- `internal/api/handler_entries.go`
- `internal/api/handler_interact.go`
- `internal/api/handler_tasks.go`
- `internal/api/handler_pending.go`
- `internal/api/handler_gdoc.go`
- `internal/api/handler_cron.go`
- `internal/api/handler_legal.go`
- `internal/api/handler_health.go`
- `internal/api/handler_sms.go`
- `internal/api/handler_telegram.go`

---

## Session Log

_The LLM appends a short bullet summary here at the end of each session. Most recent first._

<!-- 20260316 session 2 -->
- Implemented `wrapAPI` in new `internal/api/api_handler.go` with `HandlerError` (typed non-500 errors), `withStatus` (non-200 success), and `APIHandler func(*Server, w, r) (any, error)`.
- Converted 19 handlers across 7 files to `APIHandler`; removed `wrapWithPath`.
- Exempted: `handleSMS`, `handleTelegram` (respond-before-async), `handleMetrics` (Prometheus), `handlePrivacyPolicy`, `handleTermsAndConditions` (HTML).
- Net result: -192 lines. `go build ./...`, `go test ./...`, `go vet ./...` all pass.

<!-- 20260316 session 1 -->
- Brief created. Scope defined: introduce `wrapAPI` wrapper in `internal/api/`, refactor all handler files. Key open questions: webhook handler compatibility and non-200 success response strategy.
