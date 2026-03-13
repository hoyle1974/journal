# JOT Project Blueprint

## 1. Project Vision

JOT is a single-user "Agentic Second Brain." It creates a high-fidelity bridge between a raw chronological log (Episodic Memory) and a distilled, cross-linked Knowledge Graph (Semantic Memory).

### The "Gold vs. Gravel" Principle

- **Gravel:** Temporary logistics, one-off errands, and conversational filler. It stays in the raw logs but is ignored by long-term memory.
- **Gold:** Relationship facts, project milestones, rigid preferences, and personal values. This is extracted by the Dreamer and stored in the Knowledge Graph.

**Capabilities:** The single source of truth for what Jot can do (tools, agents, API, cron, memory) is `internal/prompts/app_capabilities.txt`. Keep it updated when adding or changing behavior.

## 2. Memory Hierarchy (Firestore)

| Collection         | Purpose              | Logic                                                                 |
|--------------------|----------------------|-----------------------------------------------------------------------|
| `entries`          | Episodic Memory      | Raw, immutable journal logs. Every user input is logged here first.   |
| `knowledge_nodes`  | Semantic Memory      | Distilled facts (people, projects, goals, preferences, etc.). Vector embeddings; context nodes (e.g. `user_profile`, `latest_dream`, `system_evolution`) live here. |
| `queries`          | System History       | Past Q&A. Used for context and Identifying Knowledge Gaps.            |
| `pending_questions`| Pending clarifications| Gaps/contradictions from Dreamer gap detection.                       |
| `_system`          | State                | `dream_run`, `deploy_meta`, `sync_lock`, `sync_state`, `sync_debounce`, `onboarding`.           |

## 3. Core Component Architecture

### A. The Front of House (FOH) — `pkg/agent/foh.go`

The main query loop. Invoked via `internal/service` (`RunQuery` → `agent.RunQueryWithDebug`). User input is saved to the journal at the start of each request (before the LLM runs).

1. **Start:** Log user input as an entry (`AddEntryAndEnqueue`), build system prompt (identity, contexts, knowledge-gap block, open todos).
2. **Loop:** LLM either answers or issues tool calls. Tools run in parallel (worker pool); results are sent back to the LLM.
3. **Reflect:** Before returning, a reflection check validates the draft answer against semantic memory to reduce hallucinations; revision may be applied.
4. **Synthesis pass:** When multiple search results were used, a refinement pass reduces repetition and dumping.
5. **Answer:** Save query (and optional knowledge-gap flag) via `EnqueueSaveQuery`, return a concise, CLI-friendly response.

Tools include journal, knowledge (semantic_search, upsert_knowledge, etc.), context, task, web, utility, and specialists. `discovery_search` maps intent to tool schemas when the model is unsure which tool to use.

### B. The Dreamer (Nightly) — `pkg/agent/dreamer.go` + `internal/service/cron.go`

Consolidates the last 24h of journal entries. Entry point: `service.RunDreamer` (cron or API).

1. **Fetch:** Load last 24h entries and journal context; load recent queries text.
2. **Colloquium:** "Committee" of specialists discuss the journal in a room (up to 2 passes); each can add or correct; they may reply "DONE" when satisfied.
3. **Extraction:** Run specialists (relationship, work, task, thought, selfmodel) for final fact extraction; run context extractor and query analyzer.
4. **Consolidation:** Merge facts by embedding similarity (`mergeDreamerFacts`), then write to semantic memory (`dreamerWriteMergedFacts`).
5. **Gap detection:** `RunGapDetection` — identify knowledge gaps/contradictions (uses `app_capabilities.txt`).
6. **Synthesis:** Re-synthesize impacted context nodes; merge persona facts into `user_profile` (`RunProfileSynthesis`); run Cognitive Engineer and write to `system_evolution` (`RunEvolutionSynthesis`).
7. **Incubation:** `PromoteIncubatingClusters` — promote recurring themes (tags/categories across days) to formal contexts.
8. **Narrative:** Write the morning readout to `_system/latest_dream`.
9. **Task phase:** Tool-calling phase to create or update tasks from the night's journal.

### C. The Specialist Agents — `pkg/agent/specialists.go`

Domains: **relationship** (Anthropologist), **work** (Architect), **task** (Executive), **thought** (Philosopher), **selfmodel**, **evolution** (Cognitive Engineer). Used in Dreamer for extraction and colloquium; consultable via tools (`consult_anthropologist`, `consult_architect`, etc.). `RunCommittee` runs selected specialists in parallel; `RunEvolutionAudit` is the Cognitive Engineer’s nightly analysis (friction, suggested changes).

### D. The Planner — `pkg/agent/planner.go`

`CreateAndSavePlan(goal)` uses the LLM to decompose a goal into phases, then stores the goal and phase nodes in the knowledge graph (goal + task nodes with `parent_goal`, `step_number`, `dependencies`). Exposed via CLI and `generate_plan` tool.

### E. Cron Jobs — `internal/service/cron.go`

- **Dreamer:** Daily consolidation (see above).
- **Janitor:** `RunJanitor` — evicts low-significance, rarely recalled nodes (composite index: `last_recalled_at`, `significance_weight`). Never deletes `identity_anchor` / `user_identity`.
- **Pulse audit:** `RunPulseAudit` — finds high-value nodes not recalled in 14 days and creates proactive "stale loop" signals for FOH.

## 4. Entry Points

- **CLI** (`cmd/jot`): log, query, sync (Google Doc), dream, janitor, plan, recall (dream narrative), edit, entries, etc.
- **API:** POST /query, /log, /dream, /rollup, /janitor, /plan, /sync, /decay-contexts, /backfill-embeddings, /webhook, /sms; GET /dream/latest, /dream/status, /metrics, /entries, /pending-questions; POST /pending-questions/:id/resolve. POST /dream returns 202 and runs the dream asynchronously (single run at a time; poll GET /dream/status). Cloud Tasks for async work (e.g. process-sms-query, dream-run).
- **Cron:** Dreamer (daily), Janitor (weekly).

## 5. Engineering Patterns (see also `.cursorrules`)

- **App / DI:** Prefer passing `*infra.App` (or env structs like `FOHEnv`) explicitly. Avoid hiding app in `context.Context` except at the outermost request boundary. Legacy use of `infra.GetApp(ctx)` is acceptable where refactoring would be large.
- **Logging:** Use `LoggerFrom(ctx)` for all logs; no raw `slog` or `fmt.Print`. Debug logs must not truncate content.
- **Tools:** Register via `tools.Register` in `init()`; keep implementations in domain-specific files (e.g. `journal_tools.go`, `web_tools.go`).
- **Prompt safety:** Wrap user-origin strings in `<user_data>` via `WrapAsUserData()`. Parse LLM output as key/value lines (e.g. `pkg/utils.ParseKeyValueMap`); no JSON from LLM responses.
- **Observability:** Use `StartSpan(ctx, "operation_name")` for significant steps; set attributes and `defer span.End()`.
- **Feature work:** Track in `briefs/active/`; move to `briefs/done/` when merged or abandoned.
- **Firestore indexes:** All composite indexes in `firestore.indexes.json`; deploy with `./scripts/deploy.sh` or `firebase deploy --only firestore:indexes`.
