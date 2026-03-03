# Jot Data Flow

This document describes how data flows through the Jot system: ingestion, storage, processing, and retrieval.

## Overview

```
┌─────────────────────────────────────────────────────────────────────────────────┐
│                           INGESTION SOURCES                                      │
├─────────────────────────────────────────────────────────────────────────────────┤
│  CLI (log)    SMS (no ?)    Google Doc Sync    /log API                         │
│      │             │                │               │                           │
│      └─────────────┴────────────────┴───────────────┘                           │
│                              │                                                  │
│                              ▼                                                  │
│                    ┌─────────────────┐                                          │
│                    │    AddEntry     │                                          │
│                    └────────┬────────┘                                          │
│                             │                                                   │
│         ┌───────────────────┼───────────────────┐                              │
│         │                   │                   │                              │
│         ▼                   ▼                   ▼                              │
│  ┌──────────────┐   ┌──────────────┐   ┌──────────────┐                       │
│  │   entries    │   │  Evaluator    │   │  Context     │                        │
│  │  (Firestore) │   │  (async)     │   │  Detection   │                       │
│  └──────┬───────┘   └──────┬───────┘   └──────┬───────┘                        │
│         │                  │                  │                                  │
│         │                  ▼                  ▼                                  │
│         │           ┌──────────────┐   ┌──────────────┐                         │
│         │           │knowledge_    │   │knowledge_    │                          │
│         │           │nodes         │   │nodes         │                          │
│         │           │(semantic     │   │(contexts)    │                          │
│         │           │ memory)      │   │              │                          │
│         │           └──────────────┘   └──────────────┘                         │
│         │                                                                        │
│         ▼                                                                        │
│  ┌──────────────┐                                                                │
│  │  embedding   │  (async, 768-dim vector for semantic search)                   │
│  │  on entry    │                                                                │
│  └──────────────┘                                                                │
└─────────────────────────────────────────────────────────────────────────────────┘
```

---

## Firestore Collections

| Collection       | Purpose                                      | Key Fields                                                                 |
|------------------|----------------------------------------------|----------------------------------------------------------------------------|
| `entries`        | Raw journal entries (episodic log)           | content, source, timestamp, embedding                                      |
| `queries`        | Question + answer pairs (conversation log)   | question, answer, source, timestamp                                        |
| `knowledge_nodes`| Semantic memory (facts, people, plans, etc.) | content, node_type, embedding, significance_weight, domain, last_recalled_at |
| `_system`        | Locks, debounce state                        | (internal)                                                                 |

---

## 1. Entry Ingestion

### Path A: Direct (no LLM)

- **POST /log** – API
- **SMS** (plain text, no `?` prefix) – Twilio webhook
- **CLI** `jot log <message>`

Flow: `handleLog` or `processEntrySMS` → `AddEntry` → Firestore `entries`

### Path B: Via Query Agent

- **POST /query** – API
- **SMS** with `?` prefix – question
- **Google Doc Sync** – plain lines (agent responds after input is logged)

Flow: `handleQuery` / `processQuerySMS` / `handleSync` → `RunQuery` → `AddEntry` (save input) → Gemini agent loop

### AddEntry Side Effects (async)

When `AddEntry` runs, it triggers three async jobs:

1. **Evaluator** – LLM scores significance (0–1), domain, and optional `fact_to_store`. If significance ≥ 0.5 and `fact_to_store` is set, writes to `knowledge_nodes` via `UpsertSemanticMemory`.
2. **Context detection** – Vector similarity to existing contexts; either touches a match or creates a new context node in `knowledge_nodes`.
3. **Embedding** – Generates 768-dim vector (Vertex AI text-embedding-005) and updates the entry doc with an `embedding` field.

---

## 2. Semantic Memory (knowledge_nodes)

### Write Paths

| Source        | Function               | When                                                                 |
|---------------|------------------------|----------------------------------------------------------------------|
| Evaluator     | `UpsertSemanticMemory` | New entry with high-significance fact (≥0.5)                         |
| Dreamer       | `UpsertSemanticMemory` | Daily cron: consolidates last 24h of entries into facts             |
| Agent tools   | `UpsertKnowledge`      | `upsert_knowledge`, `generate_plan`, bookmarks, countdowns, etc.     |
| Context system| `CreateContext`        | New project/plan/event detected from entry                           |

### Schema (extended)

- `content`, `node_type`, `metadata`, `embedding`
- `significance_weight` (0–1)
- `domain` (relationship, work, task, thought)
- `last_recalled_at` (RFC3339)
- `entity_links`, `journal_entry_ids` (optional)

### Dreamer (daily cron)

- Runs on last 24h of entries
- Four specialists: Anthropologist, Architect, Executive, Philosopher
- Extracts “gold” (facts) vs “gravel” (transient details)
- Writes facts to `knowledge_nodes` via `UpsertSemanticMemory` (weight 0.7)

### Janitor (weekly cron)

- Deletes nodes where `significance_weight < 0.2` and `last_recalled_at` older than 30 days
- Uses composite index on `(significance_weight, last_recalled_at)`

---

## 3. Query Flow

```
User question
     │
     ▼
┌─────────────────┐
│   RunQuery      │
│   (query_agent) │
└────────┬────────┘
         │
         │  Builds context: active contexts, recent entries, recent queries
         │
         ▼
┌─────────────────┐     tool calls      ┌─────────────────────────────┐
│  Gemini + tools │ ◄──────────────────►│ semantic_search, search_    │
│  (agent loop)   │                     │ entries, get_entries_by_    │
└────────┬────────┘                     │ date_range, consult_*      │
         │                              │ upsert_knowledge, etc.      │
         │                              └─────────────────────────────┘
         ▼
┌─────────────────┐
│  SaveQuery      │  →  queries collection (question, answer)
└─────────────────┘
```

### Tools that read data

- `semantic_search` – Vector search on `knowledge_nodes`
- `search_entries` – Vector search on `entries`
- `get_entries_by_date_range` – Timestamp range on `entries`
- `get_recent_queries`, `search_queries`, `get_queries_by_date` – `queries`
- `consult_anthropologist`, `consult_architect`, `consult_executive`, `consult_philosopher` – LLM specialists with journal context

---

## 4. Plan Flow

**POST /plan** or agent `generate_plan`:

1. Gemini produces a phased plan (JSON)
2. Goal saved as `knowledge_nodes` node (node_type `goal`)
3. Phases saved as child nodes (node_type `task`) with metadata linking to parent

---

## 5. Google Doc Sync

**POST /sync**:

1. Fetches Google Doc by `DOCUMENT_ID`
2. Finds unbolded `done.` line
3. Processes unbolded lines above it:
   - `?question` → RunQuery, insert answer, bold question
   - `!action` → RunQuery with action, insert confirmation, bold action
   - Plain text → RunQuery (input saved at start; agent responds)
4. Marks `done.` as bold and appends `[processed]`
5. Optionally enqueues Cloud Task for Drive Watch debounce

---

## 6. Summary Diagram

```
                    INGESTION
                         │
    ┌────────────────────┼────────────────────┐
    │                    │                    │
    ▼                    ▼                    ▼
 entries            Evaluator            Context
 (raw log)          → knowledge_nodes    → knowledge_nodes
    │               (high-sig facts)     (contexts)
    │
    └── embedding (async)
                         │
                    DREAMER (daily)
                         │
                    knowledge_nodes
                    (consolidated facts)
                         │
                    JANITOR (weekly)
                         │
                    Evict low-sig, stale nodes
                         │
                    QUERIES
                         │
    User question ──► RunQuery ──► tools ──► entries, knowledge_nodes, queries
                         │
                    SaveQuery ──► queries
```
