# Brief: First-Run Onboarding via Pending Questions

**Date:** 20260311
**Status:** `in-progress`
**Branch:** `feature/first-run-onboarding`
**Worktree:** `../journal-first-run-onboarding`

---

## Goal

When the system starts fresh (empty Firestore, no prior state), inject a set of onboarding
questions into `pending_questions` so the user is prompted to answer them the next time they
run the CLI. This gives the Dreamer and FOH a baseline identity to work from immediately,
rather than building it up passively over the first several days of use.

---

## Scope

**In:**
- Detect first run by checking `_system/onboarding` in Firestore; create it (status `complete`)
  after injecting questions so the check is idempotent
- Inject the following questions into the `pending_questions` collection on first run:
  - "What is your name?"
  - "Describe your family."
  - "Who do you work for?"
  - "What is your job or role?"
  - (extensible — add more in `onboardingQuestions` slice)
- Each question uses `kind: "onboarding"` so they are visually distinct in the CLI prompt
- Trigger point: server startup (`InitDefaultApp`) or, if Firestore is not yet available there,
  lazily on the first authenticated API request via a one-time check
- The CLI already calls `maybePromptPendingQuestions()` before every `log` / `query`, so no
  CLI changes are needed for delivery
- Answers flow through the existing `ResolvePendingQuestion` path; the resolved answer should
  be saved as a `user_identity` knowledge node (same as any other pending question resolution,
  but we may want a small post-resolve hook — see Open Questions)

**Out:**
- No new API routes
- No changes to the CLI prompt flow (it already shows pending questions)
- No changes to the Dreamer pipeline (it already reads `user_profile`)
- No UI

---

## Approach & Key Decisions

### Detection

The `_system` collection already stores singleton documents (`dream_run`, `sync_lock`,
`deploy_meta`, `latest_dream`). Add `_system/onboarding`:

```
{
  "status": "complete",
  "seeded_at": <timestamp>,
  "version": 1
}
```

On startup (or first request), do a lightweight `Get` on this doc:
- **Not found / does not exist** → first run. Inject questions, then `Set` the doc.
- **Exists with `status: "complete"`** → skip entirely.

This is idempotent: if the server crashes after writing some but not all questions, re-running
will skip (doc exists). To handle partial seeds, write the `_system/onboarding` doc *last*,
after all questions are committed.

### Where to trigger

`InitDefaultApp` in `function.go` initializes config, observability, and the infra app. After
`infra.InitDefaultApp` succeeds, call `RunFirstRunOnboarding(ctx, app)`. This runs once per
cold start; Cloud Run may cold-start multiple instances but the Firestore `Set` with
`MergeAll` is idempotent and the question injection uses `Add` (unique UUIDs), so a race
between two cold starts at most injects duplicate questions — acceptable for a single-user
system. If it becomes an issue, wrap in a transaction.

```go
// function.go, in InitDefaultApp, after infra.InitDefaultApp succeeds:
if err := service.RunFirstRunOnboarding(ctx, app); err != nil {
    log.Printf("first-run onboarding skipped: %v", err)
    // non-fatal; never block startup
}
```

### Implementation location

New file: `internal/service/onboarding.go`

```go
package service

// onboardingQuestions is the seed set injected on first run.
// Extend this slice to add more onboarding prompts.
var onboardingQuestions = []struct {
    Question string
    Kind     string
}{
    {"What is your name?",        "onboarding"},
    {"Describe your family.",     "onboarding"},
    {"Who do you work for?",      "onboarding"},
    {"What is your job or role?", "onboarding"},
}

// RunFirstRunOnboarding checks _system/onboarding and seeds pending_questions if this
// is the first time the system has started. Safe to call on every cold start.
func RunFirstRunOnboarding(ctx context.Context, app *infra.App) error { ... }
```

Each question becomes a `pending_questions` document with:
- `uuid`: `uuid.New().String()`
- `question`: the question text
- `kind`: `"onboarding"`
- `context`: `"Initial setup — your answers will be stored as long-term identity facts."`
- `resolved`: `false`
- `created_at`: RFC3339 timestamp

### Answer handling

The existing `ResolvePendingQuestion` marks the question `resolved: true` and stores the
answer on the doc. That's enough for the CLI UX. However, the answer should also be persisted
as a `user_identity` knowledge node so the FOH and Dreamer immediately have it in semantic
memory.

Two options:
1. **Post-resolve hook in `memory.ResolvePendingQuestion`**: check `kind == "onboarding"` and
   call `UpsertSemanticMemory` with `node_type: user_identity`. Keeps it automatic.
2. **FOH-driven**: rely on the FOH to extract and store the fact from the answer when the user
   next queries. Lower implementation cost but slower — the fact doesn't land until the next
   active session.

**Decision: option 1.** The resolve path already has the answer text; upsert there with
`node_type: user_identity`, `significance_weight: 1.0`, `domain: "selfmodel"`. Wrap in a
goroutine so a slow embedding call doesn't block the resolve HTTP response.

---

## Affected Areas

- [x] API routes or cron jobs — `InitDefaultApp` in `function.go` gets one call added
- [x] Memory / journal behavior — `memory.ResolvePendingQuestion` gets an onboarding hook
- [ ] Agent / FOH loop — not touched
- [ ] Tools — not touched
- [x] Prompts / `app_capabilities.txt` — add one bullet under "Entry points" noting first-run
      onboarding seeds `pending_questions` on fresh install
- [ ] Firestore schema or queries — no new indexes; `pending_questions` collection already
      exists and is indexed
- [x] New dependencies / infra clients — none; reuses existing Firestore client via `app`

---

## Open Questions

- [ ] Should `kind: "onboarding"` questions be shown with a different header in the CLI
      (`maybePromptPendingQuestions`) to make it obvious this is a setup step and not a
      gap detected by the Dreamer? Currently all pending questions look the same. Low priority
      since `kind` is already printed in the list.
- [ ] Should we re-seed if the user clears Firestore via `admin reset-firestore`? The reset
      deletes `_system`, so yes — the next cold start will see no `_system/onboarding` doc and
      re-seed automatically. No extra work needed; document this behavior.
- [ ] Version field on `_system/onboarding`: if we add new questions later, bump `version` and
      check whether to inject only the new ones. Defer until we actually add questions.

---

## Checklist

**Implementation**
- [ ] `internal/service/onboarding.go` created; `RunFirstRunOnboarding(ctx, app)` implemented
- [ ] `function.go` calls `service.RunFirstRunOnboarding(ctx, app)` after `InitDefaultApp` —
      non-fatal (log + continue on error)
- [ ] `memory.ResolvePendingQuestion` upserts `user_identity` node for `kind == "onboarding"`
      answers (goroutine, non-blocking)
- [ ] `_system/onboarding` doc written *after* all questions committed (write-last for
      idempotency)
- [ ] New code passes `*infra.App` explicitly — no `infra.GetApp(ctx)`
- [ ] All logging uses `LoggerFrom(ctx)` — no `fmt.Print` or raw `slog`
- [ ] Errors wrapped with `%w`, not `%v`
- [ ] No file exceeds 400 lines

**Firestore (if applicable)**
- [ ] No new composite indexes needed — `pending_questions` collection already indexed
- [ ] Confirm `_system/onboarding` doc does not conflict with existing `_system` documents

**Wrap-up**
- [ ] `app_capabilities.txt` updated (one bullet: first-run onboarding)
- [ ] `blueprint.md` — not touched (no agentic loop change)
- [ ] Tests added for `RunFirstRunOnboarding` (mock Firestore: first-run path, already-run
      path, partial-seed recovery)
- [ ] Manual smoke test: `reset-firestore`, cold start, `jot log hello` → see 4 onboarding
      questions; answer them; confirm `user_identity` nodes written in Firestore
- [ ] Brief status set to `done` and file moved to `briefs/done/`

---

## Key Files

- `briefs/active/20260311_first-run-onboarding.md` (this file)
- `internal/service/onboarding.go` — new file; primary implementation
- `function.go` — add `service.RunFirstRunOnboarding(ctx, app)` call
- `pkg/memory/` (whichever file contains `ResolvePendingQuestion`) — add onboarding hook
- `internal/prompts/app_capabilities.txt` — one-line update

---

## Session Log

_The LLM appends a short bullet summary here at the end of each session. Most recent first._

<!-- 20260311 -->
- Brief created. Approach: detect via `_system/onboarding` Firestore doc; inject 4 questions
  into `pending_questions` on first cold start; hook `ResolvePendingQuestion` to upsert
  `user_identity` nodes for `kind == "onboarding"` answers. Trigger point: end of
  `InitDefaultApp` in `function.go`, non-fatal. No new routes, no CLI changes needed.
