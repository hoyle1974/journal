# Context sent to Gemini: what changes vs what doesn’t

This doc maps the **LLM_CONTEXT_SENT** payload (system + history + current turn) to what stays the same between runs and what changes, for context-caching decisions.

## Structure of the context

1. **=== system ===** — One big system instruction (built by `BuildSystemPrompt` in `pkg/agent/prompter.go`). It is split into:
   - **Preamble** (before `=======`): intended to be cacheable — static instructions plus SOURCE CODE block.
   - **After `=======`**: dynamic per-request data (date, active contexts, recent conversation, proactive alerts, knowledge gap).
2. **=== history ===** — Multi-turn conversation so far (user/assistant + tool results) in this chat session.
3. **=== current turn ===** — The message being sent right now (user text or tool results).

Tool **definitions** are sent with the request but are not included in the logged context; they are fixed until you change tools.

---

## System section: preamble vs dynamic

The system block is built from `internal/prompts/system_prompt.txt`. It is organized so **cacheable content comes first**, then a literal **=======** separator, then **dynamic** content. Placeholder breakdown:

### Preamble (before `=======`) — cacheable

| Part | Source | Notes |
|------|--------|--------|
| Boilerplate text | Template literal | Safety rules, TIME AND ORDER SEMANTICS, YOUR PRIMARY ROLE (1–5), IMPORTANT GUIDELINES. Same until you edit the template. |
| SOURCE CODE block | `prompts.SourceCodeBlock()` | Repo URL and instructions; effectively static. |
| Tool definitions | `tools.GetDefinitions()` | Sent with the model; static until you add/change tools. |

Future option: longer “ancient” history/conversation could be appended to the preamble and included in the cache; only “recent” would go below `=======`.

### After `=======` — dynamic

### Changes daily (date rollover)

| Part | Source | In template as |
|------|--------|-----------------|
| Today’s date | `time.Now()` | `today` |
| Current week | e.g. `2026-W10` | `currentWeek` |
| Last week | e.g. `2026-W09` | `lastWeekStr` |
| Current month | e.g. `2026-03` | `currentMonth` |

So “Current time context” changes at most once per day (or per first request after midnight).

### Changes every request (or very often) — all below `=======`

These are filled per call to `BuildSystemPrompt` and can change on every FOH request:

| Part | Source | When it changes |
|------|--------|------------------|
| ACTIVE CONTEXTS | `memory.GetActiveContexts(ctx, 5)` | When linked contexts / relevance change. |
| RECENT CONVERSATION (last 5 Q&A pairs) | `journal.GetRecentQueries(ctx, 5)` | When there’s a new Q&A (or you change the limit). |
| PROACTIVE ALERTS | `memory.GetActiveSignals(ctx, 3)` | When signals are added or rotated. |
| KNOWLEDGE GAP block | `journal.GetRecentGapQueries(ctx, 5)` | When gap queries are logged or rotated. |

So within a single day, the **variable** part of the system prompt is: date block + active contexts + recent conversation + proactive alerts + knowledge gap block.

---

## History and current turn

| Part | When it changes |
|------|------------------|
| **=== history ===** | Grows every turn: each user message, each assistant reply, and each tool round-trip add more content. |
| **=== current turn ===** | Different on every request; it’s the current user message (or tool results) being sent. |

So **history + current turn** are the most dynamic: different on every `SendMessage`.

---

## Summary for caching

- **Preamble (before `=======`):** Template boilerplate + SOURCE CODE + tools. This is the intended cacheable prefix. You can later add “ancient” history/conversation here and cache it; only the tail would be dynamic.
- **After `=======`:** Current time context (daily), ACTIVE CONTEXTS, RECENT CONVERSATION (5 Q&A pairs), PROACTIVE ALERTS, KNOWLEDGE GAP block — all refreshed per `BuildSystemPrompt`. Plus **=== history ===** and **=== current turn ===** in the chat (new every turn).

The prompt is laid out so the **preamble** can be cached as one blob; everything after **=======** is sent as the dynamic part each request.

Use **LLM_CONTEXT_AUDIT** token counts (`system_tokens`, `tool_tokens`, `archive_tokens`, `recent_tokens`, `is_cacheable_size`) to see how much of each run is static vs dynamic and whether the static portion is large enough to justify caching.
