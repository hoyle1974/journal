# Brief: Unified Synchronous Pipeline

**Date:** 20260324
**Status:** `done`
**Branch:** `feature/unified-pipeline`
**Worktree:** `../jot-unified-pipeline`

---

## Goal

Replace the separate `jot log` and `jot query` commands with a single unified synchronous pipeline. All input (text, notes, questions) flows through the same path: save entry → Refinery (SPO graph extraction) → Task Worker → 2-hop Loom RAG → FOH with Gemini 2.5 native thinking. The agent's answer plus reasoning trace are returned synchronously to the caller.

---

## Scope

**In:**
- Refinery returns extracted node IDs
- `ProcessLogSequential` surfaces node IDs, drops async stage 4
- `BuildLoomRAGContext` seeded from refinery node IDs (2-hop)
- `ThinkingConfig` + `ExtractThinkingAndAnswer` in infra
- FOH uses Gemini 2.5 native thinking (removes `<thought>` block approach)
- Loom RAG context block injected into system prompt
- Fix silent model downgrade in `gemini.go`
- New `ProcessAndRespond` service method
- `POST /ingest` unified pipeline handler and route
- CLI `jot <text>` unified input (removes log/query split)
- `app_capabilities.txt` updated

**Out:**
- Async stage 4 (response worker) — removed in favour of sync pipeline
- `<thought>` / `REASONING PROTOCOL` pattern — replaced by native thinking

---

## Approach & Key Decisions

- Refinery pipeline now returns `[]string` node IDs alongside errors so downstream stages can build the RAG seed set without a second DB round-trip.
- `BuildLoomRAGContext` takes the seed IDs directly, performs 2-hop graph expansion via existing `graph_expand` logic, and returns a compact markdown block.
- FOH receives the Loom block via `prompter.go` system prompt injection.
- Gemini 2.5 native thinking replaces `<thought>` extraction; `ExtractThinkingAndAnswer` splits the response into `(reasoning_trace, answer)`.
- `ProcessAndRespond` in `agent_service.go` orchestrates the full synchronous pipeline and returns both fields to the caller.
- `POST /ingest` calls `ProcessAndRespond` and returns `{"answer": ..., "reasoning_trace": ...}`.
- CLI `jot <text>` sends to `/ingest` and prints reasoning trace then answer.

---

## Affected Areas

- [x] Agent / FOH loop
- [x] Prompts / `app_capabilities.txt`
- [x] API routes
- [x] Memory / journal behavior

---

## Checklist

**Verification (Proof of Work)**
- [x] **Compilation:** `go build ./...` passes cleanly.
- [x] **Tests:** `go test ./...` passes.
- [x] `app_capabilities.txt` updated

---

## Key Files

- `internal/service/refinery_pipeline.go`
- `internal/service/process_entry.go`
- `internal/service/loom_rag.go`
- `internal/service/loom_workers.go`
- `internal/foh/foh_thought.go`
- `internal/foh/foh.go`
- `internal/foh/chat.go`
- `internal/infra/gemini.go`
- `internal/foh/prompter.go`
- `internal/prompts/system_prompt.txt`
- `internal/service/agent_service.go`
- `cmd/backend/backend.go`
- `cmd/backend/handler_interact.go`
- `cmd/backend/router.go`
- `cmd/jot/main.go`
- `internal/prompts/app_capabilities.txt`

---

## Session Log

_Most recent first._

<!-- 20260324 -->
- **2026-03-24** — Task 12 (final): Updated `app_capabilities.txt` — removed `<thought>`/REASONING PROTOCOL references, removed log/query split, added Pipeline section describing the unified sync pipeline and `POST /ingest` endpoint, updated CLI entry-point description to `jot <anything>`. Created this brief. Ran build and test suite. All tasks 1–12 complete.
