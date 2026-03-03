# JOT Project Blueprint

## 1. Project Vision

JOT is a single-user "Agentic Second Brain." It creates a high-fidelity bridge between a raw chronological log (Episodic Memory) and a distilled, cross-linked Knowledge Graph (Semantic Memory).

### The "Gold vs. Gravel" Principle

- **Gravel:** Temporary logistics, one-off errands, and conversational filler. It stays in the raw logs but is ignored by long-term memory.
- **Gold:** Relationship facts, project milestones, rigid preferences, and personal values. This is extracted by the Dreamer and stored in the Knowledge Graph.

## 2. Memory Hierarchy (Firestore)

| Collection       | Purpose          | Logic                                                                 |
|------------------|------------------|-----------------------------------------------------------------------|
| `entries`        | Episodic Memory  | Raw, immutable logs. Every input is logged here first.               |
| `knowledge_nodes`| Semantic Memory  | Distilled facts (People, Projects, Facts). Backed by Vector Embeddings. |
| `queries`        | System History   | Past Q&A. Used for context and Identifying Knowledge Gaps.            |
| `_system`        | State            | Distributed locks, sync tokens, and debounce timers.                   |

## 3. Core Component Architecture

### A. The Front of House (FOH) - `query_agent.go`

The main entry point for user interaction. The system saves the user's input to the journal at the start of each request (before the LLM runs). Loop:

1. **Decompose:** Decide which domains (Relationship, Work, etc.) are relevant.
2. **Execute:** Run tools (search, utility, specialists) in parallel via worker pools.
3. **Reflect:** Check the draft answer against semantic memory to prevent hallucinations.
4. **Answer:** Return a concise, CLI-friendly response.

### B. The Dreamer (Nightly) - `cron.go`

Consolidates the last 24h of logs.

- **Consolidation:** Uses the "Committee of Minds" to extract Gold.
- **Synthesis:** Updates context nodes (e.g., `user_profile`, `active_plans`) with high-density briefings.
- **Evolution Audit:** The "Cognitive Engineer" analyzes system friction and suggests code/tool improvements.

### C. The Specialist Agents - `agents.go`

- **Anthropologist:** Relationships & Social Debt.
- **Architect:** Projects & Technical Logic.
- **Executive:** Tasks & Planning.
- **Philosopher:** Reflection & Growth.
- **Cognitive Engineer:** System Architecture & Friction.

## 4. Engineering Patterns (Rules for Cursor)

- **The App Pattern:** Every function requiring Firestore, Gemini, or Loggers must use the App attached to the `context.Context`. Never initialize clients locally.

- **Tool Registration:** Tools are registered via `tools.Register` in `init()` functions. Implementations should be domain-specific (e.g., `web_tools.go`, `journal_tools.go`).

- **Prompt Safety:** Use `llmjson` for resilient parsing. Never inject raw user strings into prompts; always wrap them using `WrapAsUserData()`.

- **Observability:** Use `StartSpan` for all significant operations. Attach metadata to spans to make the agent's "reasoning" visible in traces.
