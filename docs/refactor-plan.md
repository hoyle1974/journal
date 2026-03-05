# Refactor Plan: File Moves with Per-Step Build + Commit

Complete the remaining file moves from the architecture refactor. After **each step**: run `go build ./... && go test ./...`, then `git add -A && git commit -m "refactor: <step description>"`.

**Dependency order:** infra → utils → memory → journal → agent → api (transport). Each step moves one layer and updates all references so the tree compiles.

---

## Step 1: pkg/infra – app, firestore, gemini, observability, tasks, sms

**Goal:** Create `pkg/infra` and move six files. Root `jot` and all callers import `github.com/jackstrohm/jot/pkg/infra` for app, Firestore, Gemini, logging, tasks, and Twilio.

**Moves:**

| From (root)     | To                    |
|-----------------|------------------------|
| app.go          | pkg/infra/app.go       |
| cloud_client.go | pkg/infra/firestore.go |
| gemini.go       | pkg/infra/gemini.go    |
| observability.go| pkg/infra/obs.go       |
| tasks.go        | pkg/infra/tasks.go     |
| twilio.go       | pkg/infra/sms.go       |

**Actions:**

1. Create `pkg/infra/` and add the six files with `package infra`.
2. In each file: replace `package jot` with `package infra`; add/update imports to use `internal/config`, `cloud.google.com/go/firestore`, `github.com/google/generative-ai-go/genai`, etc. Resolve internal references (e.g. `obs.go` may reference `Logger` → `infra.Logger` or a shared logger; `app.go` uses Firestore/Gemini from same package).
3. **Root/jot:** Keep a thin **facade**: e.g. `app_facade.go` that re-exports `infra.NewApp` as `jot.NewApp`, `infra.App` as `jot.App`, `infra.WithApp`/`infra.GetApp` so existing `jot` imports keep working. Alternatively, update every reference to `infra` in one pass (main.go, default_config, internal/api, cmd/server, cmd/admin, internal/tools/impl, handlers, etc.).
4. **internal/api:** `AppLike` stays; concrete type becomes `*infra.App`. Either api imports `pkg/infra` and root imports infra for `defaultApp`, or root still holds `*infra.App` and passes it to api (root imports infra).
5. Update `default_config.go` and init so `defaultApp` is created via `infra.NewApp(ctx, defaultConfig)` and typified as `*infra.App`; ensure `api.NewServer(defaultApp, ...)` still receives an type that implements `AppLike` (infra.App implements it).
6. Update all other files that reference `jot.NewApp`, `jot.GetApp`, `jot.Logger`, `StartSpan`, `LoggerFrom`, Firestore client, Gemini, tasks, Twilio to use `infra` (or jot re-exports).
7. **Verify:** `go build ./... && go test ./...`
8. **Commit:** `git add -A && git commit -m "refactor: add pkg/infra (app, firestore, gemini, obs, tasks, sms)"`

---

## Step 2: pkg/utils – convert, format, math, util, web

**Goal:** Create `pkg/utils` and move five tool files. Callers use `github.com/jackstrohm/jot/pkg/utils` for ConvertUnits, FormatEntriesForContext, EvaluateMathExpression, etc.

**Moves:**

| From (root)       | To                   |
|-------------------|----------------------|
| tools_convert.go  | pkg/utils/convert.go |
| tools_format.go   | pkg/utils/format.go  |
| tools_math.go     | pkg/utils/math.go    |
| tools_util.go     | pkg/utils/util.go    |
| tools_web.go      | pkg/utils/web.go     |

**Actions:**

1. Create `pkg/utils/` and add the five files with `package utils`.
2. **Dependencies:**  
   - `format.go` uses `Entry`, `QueryLog`, `SanitizePrompt`, `SafeTruncate`. Either move those types/helpers to a shared package (e.g. journal for Entry/QueryLog, infra or utils for SanitizePrompt/SafeTruncate) or have `format.go` accept interfaces and keep prompt helpers in jot/infra. Minimal change: `utils.FormatEntriesForContext(entries []journal.Entry, ...)` and `utils` imports `pkg/journal` once it exists; for now `utils` can import `jot` for `SanitizePrompt`/`SafeTruncate` and `journal` types if already moved, or keep format in jot until journal exists.  
   - `util.go` uses `UpsertKnowledge`, `GenerateEmbedding`, `QuerySimilarNodes` → from memory. So move `util.go` after **Step 4 (pkg/memory)** or have util import `pkg/memory` and call memory.UpsertKnowledge etc.  
   - `web.go` uses `truncateToMaxBytes` → define in utils (e.g. in util.go) or import from jot.
3. Order within Step 2: add `convert.go` and `math.go` first (no jot deps). Then add `util.go` with a local `truncateToMaxBytes` and imports of `pkg/memory` for HandleCountdown/HandleBookmark. Then `web.go` (import utils for truncate). Then `format.go` (import jot for SanitizePrompt/SafeTruncate and journal for Entry/QueryLog, or postpone format until after journal move).
4. Update all call sites (e.g. internal/tools/impl, agents, query_agent) to use `utils.ConvertUnits`, `utils.FormatEntriesForContext`, etc.
5. **Verify:** `go build ./... && go test ./...`
6. **Commit:** `git add -A && git commit -m "refactor: add pkg/utils (convert, format, math, util, web)"`

**Note:** If circular deps appear (utils → memory → infra, format → journal), do utils in two sub-steps: (2a) convert + math + web (and util.go only the parts that don’t need memory); (2b) after memory/journal exist, add format and the memory-dependent parts of util.

---

## Step 3: pkg/memory – schema, knowledge, migrate, context, rag

**Goal:** Create `pkg/memory` and move memory-related code. Everything that uses knowledge nodes, context nodes, RAG, or memory schema imports `pkg/memory` (and internal/memory disappears).

**Moves:**

| From                        | To                      |
|-----------------------------|-------------------------|
| internal/memory/schema.go   | pkg/memory/schema.go     |
| internal/memory/migrate.go  | pkg/memory/migrate.go    |
| cloud_knowledge.go          | pkg/memory/knowledge.go  |
| context.go                  | pkg/memory/context.go    |
| rag.go                      | pkg/memory/rag.go         |

**Actions:**

1. Create `pkg/memory/` and add the five files with `package memory`.
2. In each file: set `package memory`; fix imports to use `pkg/infra` (Firestore, Gemini, observability), `internal/config` if needed, and each other within pkg/memory. Resolve references to `SafeTruncate`, `StartSpan`, `LoggerFrom` (use infra or pass logger).
3. **internal/memory:** Remove or redirect. Prefer deleting `internal/memory/` after moving schema and migrate into `pkg/memory` and updating all imports (e.g. from `github.com/jackstrohm/jot/internal/memory` to `github.com/jackstrohm/jot/pkg/memory`).
4. Update root and all other references (handlers, agents, tools impl, cmd/admin, etc.) to import `pkg/memory` for knowledge, context, RAG, schema, migrate.
5. **Verify:** `go build ./... && go test ./...`
6. **Commit:** `git add -A && git commit -m "refactor: add pkg/memory (schema, knowledge, migrate, context, rag)"`

---

## Step 4: pkg/journal – entries, analysis, queries

**Goal:** Create `pkg/journal` and move journal/entries and queries code.

**Moves:**

| From (root)        | To                      |
|--------------------|-------------------------|
| cloud_entries.go   | pkg/journal/entries.go  |
| journal_analysis.go| pkg/journal/analysis.go |
| cloud_queries.go   | pkg/journal/queries.go  |

**Actions:**

1. Create `pkg/journal/` and add the three files with `package journal`.
2. In each file: set `package journal`; fix imports to use `pkg/infra`, `pkg/memory` if needed, and each other. Resolve references to jot-only types (e.g. Entry, QueryLog become journal.Entry, journal.QueryLog).
3. Update all call sites to use `journal.AddEntry`, `journal.GetEntriesAsc`, journal analysis, and query types/functions.
4. **Verify:** `go build ./... && go test ./...`
5. **Commit:** `git add -A && git commit -m "refactor: add pkg/journal (entries, analysis, queries)"`

---

## Step 5: pkg/agent – foh, specialists, planner, prompter, dreamer, rollup

**Done:** FOH, planner, prompter, specialists, rollup, and dreamer moved to `pkg/agent` with Env interfaces (FOHEnv, PlannerEnv, PrompterEnv, SpecialistsEnv, RollupEnv, DreamerEnv); jot implements all via `jotFOHEnv`. Dreamer: `pkg/agent/dreamer.go` has RunDreamer(ctx, env); jot keeps RunGapDetection, RunProfileSynthesis, RunEvolutionSynthesis, RunJanitor, RunPulseAudit and thin RunDreamer wrapper. Step 5 complete.

**Goal:** Create `pkg/agent` and move FOH, specialists, planner, prompter, dreamer, and rollup.

**Moves:**

| From (root)     | To                         |
|-----------------|----------------------------|
| query_agent.go  | pkg/agent/foh.go            |
| agents.go       | pkg/agent/specialists.go    |
| query_plan.go   | pkg/agent/planner.go       |
| query_prompt.go | pkg/agent/prompter.go      |
| cron.go         | pkg/agent/dreamer.go       |
| rollup.go       | pkg/agent/rollup.go        |

**Actions:**

1. Create `pkg/agent/` and add the six files with `package agent`.
2. In each file: set `package agent`; fix imports to use `pkg/infra`, `pkg/memory`, `pkg/journal`, `pkg/utils`, and each other. Resolve references to observability, Gemini, Firestore, types from journal/memory.
3. Update all call sites (handlers, internal/api router, internal/tools/impl) to use `agent.RunQuery`, `agent.RunDreamer`, etc.
4. **Verify:** `go build ./... && go test ./...`
5. **Commit:** `git add -A && git commit -m "refactor: add pkg/agent (foh, specialists, planner, prompter, dreamer, rollup)"`

---

## Step 6: internal/api – router, handlers, ratelimit, auth_test ✅ COMPLETE

**Done:** Backend interface added in `internal/api/backend.go`. Router and all HTTP handlers live in `internal/api` (handlers_health, handlers_legal, handlers_log, handlers_dream, handlers_entries, handlers_internal, handlers_sms, handlers_webhook, handlers_sync). Main uses `api.NewServer(..., JotBackend, api.Router)` and `api.StartRateLimitCleanup()`. Jot implements `api.Backend` in `api_backend.go` and keeps `process_entry.go`, `handlers_helpers.go`, and domain logic; duplicate jot handler files were removed. Auth and handler tests updated (auth_test and main_test in jot; EntryUUIDRegex test in internal/api).

**Goal:** Move HTTP routing and all handlers into `internal/api` so the transport layer lives in one package. Root `jot` no longer contains the router or handler implementations; it keeps config, ProcessEntry, handlers_helpers, api_backend, and domain code.

**Moves:**

| From (root)           | To                            |
|-----------------------|-------------------------------|
| main.go               | internal/api/router.go        |
| handlers_dream.go      | internal/api/handlers_dream.go |
| handlers_entries.go   | internal/api/handlers_entries.go |
| handlers_health.go    | internal/api/handlers_health.go  |
| handlers_helpers.go   | internal/api/handlers_helpers.go |
| handlers_internal.go  | internal/api/handlers_internal.go |
| handlers_legal.go     | internal/api/handlers_legal.go   |
| handlers_log.go       | internal/api/handlers_log.go    |
| handlers_sms.go       | internal/api/handlers_sms.go    |
| handlers_sync.go      | internal/api/handlers_sync.go   |
| handlers_webhook.go   | internal/api/handlers_webhook.go |
| ratelimit.go          | internal/api/ratelimit.go     |
| auth_test.go          | internal/api/auth_test.go    |

**Actions:**

1. Move router logic from root `main.go` into `internal/api/router.go`: package `api`, init that registers `JotAPI` and builds default server (using `infra.NewApp`, `config.Load`, etc.), `jotRouter`, `checkAuth`, `publicRoutes`, and `JotAPI(w, r)`.
2. Ensure the **Cloud Function entry point** remains: e.g. `functions.HTTP("JotAPI", JotAPI)` in api’s init, and the main package that runs the framework (e.g. cmd/server) imports `_ "github.com/jackstrohm/jot/internal/api"` so init runs.
3. Move all listed handler files and `ratelimit.go` into `internal/api` with `package api`. In each handler, use `s.App`, `s.Config`, `s.Logger`; call into `pkg/agent`, `pkg/journal`, `pkg/memory`, `pkg/infra` as needed (no imports of root jot for domain logic).
4. Move `auth_test.go` into `internal/api` and update tests to use api types and `testServer`/`checkAuth` from the api package.
5. **Root:** Remove moved files. Keep in root only: config.go (constants), default_config.go (if still used), tools_prompt.go, tool_impls.go, tools/types.go, handlers.go (if it only registers routes or can be folded into api), and any remaining jot-only code. Root may still have `func main()` for a default binary that just runs the framework, or that binary may live only in cmd/server.
6. Update cmd/server and cmd/jot to import api (and infra/config) so that init runs and JotAPI is available.
7. **Verify:** `go build ./... && go test ./...`
8. **Commit:** `git add -A && git commit -m "refactor: move router and handlers into internal/api"`

---

## Step 7: Root cleanup and entry-point wiring ✅ COMPLETE

**Goal:** Ensure a single clear entry point for the Cloud Function and for local/server; no dead code in root.

**Done:** Entry point confirmed: cmd/server imports the jot package; jot init registers JotAPI and builds the server with api.Router. Removed obsolete jot ratelimit wrapper (ratelimit.go); ratelimit_test.go now calls api.GetClientIP and api.CheckRateLimit directly. Build and tests pass for all packages and cmd/admin, cmd/server, cmd/jot.

**Actions (completed):**

1. Confirm **cmd/server** (and optionally cmd/jot) imports `_ "github.com/jackstrohm/jot/internal/api"` so that api’s init runs and registers `JotAPI`. If root used to have `func main()`, either remove it or leave a minimal main that runs the framework (if still building from root for some target).
2. Remove any duplicate or obsolete re-exports from root now that api and infra own routing and app.
3. Run `go build ./...` for all commands: `./cmd/admin`, `./cmd/server`, `./cmd/jot`.
4. Run `go test ./...`.
5. **Commit:** `git add -A && git commit -m "refactor: root cleanup and entry-point wiring"`

---

## Checklist summary

- [ ] **Step 1** – pkg/infra (app, firestore, gemini, obs, tasks, sms) → build + test + commit  
- [ ] **Step 2** – pkg/utils (convert, format, math, util, web) → build + test + commit  
- [ ] **Step 3** – pkg/memory (schema, knowledge, migrate, context, rag) → build + test + commit  
- [ ] **Step 4** – pkg/journal (entries, analysis, queries) → build + test + commit  
- [ ] **Step 5** – pkg/agent (foh, specialists, planner, prompter, dreamer, rollup) → build + test + commit  
- [x] **Step 6** – internal/api (router, handlers, ratelimit, Backend) → build + test + commit  
- [x] **Step 7** – Root cleanup and entry-point wiring → build + test + commit  

---

## Notes

- **internal/config** stays as-is; infra and api depend on it.
- **internal/tools/impl** and **tools/** (registry, params, etc.) stay; update their imports to pkg/infra, pkg/agent, pkg/journal, pkg/memory, pkg/utils as each step lands.
- **handlers_test.go** currently in root: move to `internal/api/handlers_test.go` in Step 6 so it lives with the handlers.
- If a step cannot be done in one go (e.g. circular deps), split into sub-steps (e.g. 2a/2b) and commit after each sub-step.
