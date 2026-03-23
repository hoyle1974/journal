# JOT Project Blueprint

## 1. Project Vision

JOT is a single-user "Agentic Second Brain." It creates a high-fidelity bridge between a raw chronological log (Episodic Memory) and a distilled, cross-linked Knowledge Graph (Semantic Memory).

### The "Gold vs. Gravel" Principle

- **Gravel:** Temporary logistics, one-off errands, and conversational filler. It stays in the raw logs.
- **Gold:** Relationship facts, project milestones, rigid preferences, and personal values. Captured synchronously at ingest through the Refinery pipeline in `ProcessEntry` (discovery -> extraction -> resolve/commit), not via FOH manual extraction or a nightly batch path.

**Capabilities:** The single source of truth for what Jot can do (tools, agents, API, cron, memory) is `internal/prompts/app_capabilities.txt`. Keep it updated when adding or changing behavior.

## 2. Memory Hierarchy (Firestore)

| Collection   | Purpose              | Logic                                                                 |
|--------------|----------------------|-----------------------------------------------------------------------|
| `journal`    | Episodic Memory      | Raw journal logs (`node_type: log`). Every user input is logged here first. Also stores task nodes (`node_type: task`), query/gap nodes (`node_type: query`), and pending-question nodes (`node_type: pending_question`). Unified collection via `github.com/hoyle1974/memory`. |
| `journal`    | Semantic Memory      | Distilled facts (`node_type: person|project|goal|preference|...`). Vector embeddings; reserved knowledge nodes (e.g. `user_profile`, `system_evolution`) may live here with significance_weight >= 0.7. |
| `_system`    | State                | `deploy_meta`, `onboarding`. |

## 3. Core Component Architecture

### A. The Front of House (FOH) — `internal/agent/foh.go`

The main query loop. Invoked via `internal/service` (`RunQuery` → `agent.RunQueryWithDebug`). User input is saved to the journal at the start of each request (before the LLM runs).

1. **Start:** Log user input as an entry (`AddEntryAndEnqueue`), build system prompt (identity, knowledge-gap block, open todos).
2. **Loop:** LLM either answers or issues tool calls. Tools run in parallel (worker pool); results are sent back to the LLM.
3. **Unified audit:** The system prompt requires the model to perform reflection, gap detection, and synthesis in its reasoning before giving the final answer. If the model outputs `MISSING_INFO: <list>`, that is parsed and the query is saved as a knowledge gap.
4. **Answer:** Save query (and optional knowledge-gap flag) via `EnqueueSaveQuery`.

Tools include journal, knowledge (semantic_search, upsert_knowledge, etc.), task, web, utility. `discovery_search` maps intent to tool schemas when the model is unsure which tool to use.

### B. The Evaluator — `internal/agent/evaluator.go` + `ProcessEntry`

On journal ingest (`ProcessEntry`), the evaluator assigns significance and can auto-create tasks from strong future commitments. Gold relationship extraction is handled by the synchronous Refinery pipeline. Proactive high-significance insights can still be stored for FOH.

## 4. Entry Points

- **CLI** (`cmd/jot`): log, query, edit, entries, help.
- **API:** POST /query, /log, /backfill-embeddings, /telegram; GET /metrics, /entries, /pending-questions; POST /pending-questions/:id/resolve. Cloud Tasks for async work (e.g. process-telegram-query, process-entry). Handler is wired in `function.go`: lazy init on first request; `InitDefaultApp` from `cmd/server` for explicit startup. Use `SetServer` to inject a server for tests.

## 5. Engineering Patterns (see also `.cursorrules`)

- **App / DI:** Prefer passing `*infra.App` (or env structs like `FOHEnv`) explicitly. Avoid hiding app in `context.Context` except at the outermost request boundary. Passing the entire application container through context (`infra.GetApp(ctx)`) is a **leaky abstraction**: it breaks module boundaries by allowing deeply nested functions to reach into the context and extract arbitrary dependencies they have not explicitly declared, bypassing proper module encapsulation. Legacy use of `infra.GetApp(ctx)` remains in some packages (e.g. tools) where refactoring would be large; new code must pass app or narrow interfaces explicitly.
- **Logging:** Use `LoggerFrom(ctx)` for all logs; no raw `slog` or `fmt.Print`. Debug logs must not truncate content.
- **Tools:** Register via `tools.Register` in `init()`; keep implementations in domain-specific files (e.g. `journal_tools.go`, `web_tools.go`).
- **Prompt safety:** Wrap user-origin strings in `<user_data>` via `WrapAsUserData()`. Parse LLM output as key/value lines (e.g. `pkg/utils.ParseKeyValueMap`); no JSON from LLM responses.
- **Observability:** Use `StartSpan(ctx, "operation_name")` for significant steps; set attributes and `defer span.End()`.
- **Feature work:** Track in `briefs/active/`; move to `briefs/done/` when merged or abandoned.
- **Firestore indexes:** All composite indexes in `firestore.indexes.json`; deploy with `./scripts/deploy.sh` or `firebase deploy --only firestore:indexes`.
