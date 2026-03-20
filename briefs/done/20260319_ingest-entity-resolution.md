# Brief: Ingest-Time Entity Resolution

**Date:** 20260319
**Status:** `in-progress`
**Branch:** `feature/ingest-entity-resolution`
**Worktree:** `../jot-ingest-entity-resolution`

---

## Goal

Extend `ProcessEntry` to resolve entity mentions in new journal entries against existing knowledge nodes at write time. "Gloria" in a new note gets immediately linked to her person node; "Gloria is my wife" creates an SPO relationship node — without waiting for the nightly Dreamer.

---

## Scope

**In:**
- `internal/prompts/relationship_extractor.txt` — new SPO extraction prompt
- `internal/prompts/prompts.go` — embed var + template var + `RelationshipExtractorData` + `BuildRelationshipExtractor`
- `internal/agent/graph_builder.go` — replace `LinkEntryToPeople` with `ResolveAndLinkEntities` + `ExtractAndStoreRelationships` + `parseSPOLines`
- `internal/agent/graph_builder_test.go` — tests for `parseSPOLines`
- `internal/agent/process_entry.go` — wire new functions, remove old goroutine
- `internal/prompts/app_capabilities.txt` — document new ingest behavior

**Out:**
- No Dreamer changes
- No new Firestore indexes (uses existing fields)
- No new entity node creation at ingest (only links to existing nodes; creation stays with Dreamer/upsert_knowledge)

---

## Approach & Key Decisions

- `ResolveAndLinkEntities`: synchronous, 8s internal timeout, all entity types (not just persons)
- `ExtractAndStoreRelationships`: background goroutine (LLM call ~1-2s would hurt ingest P99), 8s internal timeout
- Prompt pattern: `//go:embed` individual var + `template.Must(template.New(...).Parse(var))` — matches exact pattern in `prompts.go`
- `GenerateContentSimple` call: rendered prompt goes as `systemPrompt` (3rd arg), empty string as `userPrompt` (4th arg) — matches evaluator pattern in `specialists.go`
- `LinkEntryToPeople` removed after `process_entry.go` is updated (confirmed single caller)

## Edge Cases & Pre-Flight Checks

1. **Ingest latency:** `ResolveAndLinkEntities` runs synchronously and does a vector search per entity. With 5 entities at ~500ms each = 2.5s added to ingest. Internal timeout at 8s provides a ceiling. Monitor P99 after deploy.
2. **LLM hallucination in SPO extraction:** Prompt instructs "clearly stated facts only" and outputs `NONE` for ambiguous entries. `parseSPOLines` skips malformed lines. Worst case: a false SPO node is stored as `generic` type with low significance — Janitor can clean it up.
3. **`prompts.go` embed pattern:** Must use `//go:embed` + individual `var` — NOT `ParseFS`. The existing file uses `template.Must(template.New("name").Parse(varName))` — must match exactly or `go build` will fail.

---

## Affected Areas

- [ ] Agent / FOH loop — not touched
- [ ] Tools — not touched
- [x] Prompts / `app_capabilities.txt` — new prompt template + capabilities update
- [ ] Firestore schema or queries — no new indexes
- [ ] New dependencies / infra clients — none
- [ ] API routes or cron jobs — not touched
- [x] Memory / journal behavior — new SPO nodes written at ingest (Gold)

---

## Open Questions

- [ ] Should `ResolveAndLinkEntities` be moved to a goroutine if P99 ingest latency increases noticeably in production?

---

## Checklist

**Implementation**
- [ ] New code passes `*infra.App` explicitly — no `infra.GetApp(ctx)` in new code
- [ ] All logging uses `LoggerFrom(ctx)` — no `fmt.Print` or raw `slog`
- [ ] Debug logs pass full strings — no truncation at Debug level
- [ ] User-origin strings wrapped with `WrapAsUserData()` in prompt template (done via `<user_data>` tags in .txt)
- [ ] Every significant agentic step has `StartSpan` / `defer span.End()`
- [ ] Errors wrapped with `%w`, not `%v`
- [ ] No file exceeds 400 lines

**Verification (Proof of Work)**
- [ ] `go build ./...` passes cleanly
- [ ] `go test ./...` passes
- [ ] `go vet ./...` clean
- [ ] Smoke test: log entry with relationship phrase, check for `relationship stored` in logs

**Wrap-up**
- [ ] `app_capabilities.txt` updated
- [ ] `LinkEntryToPeople` removed (confirmed zero callers after update)
- [ ] Brief moved to `briefs/done/`

---

## Key Files

- `briefs/active/20260319_ingest-entity-resolution.md` (this file)
- `docs/superpowers/plans/2026-03-19-ingest-entity-resolution.md` (full plan)
- `internal/agent/process_entry.go` (line ~112: goroutine to replace)
- `internal/agent/graph_builder.go` (LinkEntryToPeople to replace)
- `internal/prompts/prompts.go` (exact embed pattern to follow)
- `pkg/memory/knowledge.go` (FindEntityNodeByName, UpsertKnowledge, AddEntityLink)
- `pkg/memory/schema.go` (ParseSPOTriple, NormalizedPredicate, NodeTypeGeneric)

---

## Session Log

<!-- 20260319 -->
- Plan written and reviewed; code-reviewer agent identified and fixed: prompts.go embed pattern (use //go:embed + Parse not ParseFS), GenerateContentSimple arg order clarified, latency warning added, LinkEntryToPeople removal step added. Worktree created. Agent dispatched.

<!-- 20260319 session 2 -->
- Implemented all 4 tasks via TDD. Created `internal/prompts/relationship_extractor.txt` (SPO extraction prompt with `<user_data>` wrapping). Added `RelationshipExtractorData` struct and `BuildRelationshipExtractor` to `prompts.go` following the exact `//go:embed` + `template.Must(template.New(...).Parse(...))` pattern. Replaced `graph_builder.go` entirely: `LinkEntryToPeople` removed, replaced with `parseSPOLines` (unexported, tested), `ResolveAndLinkEntities` (synchronous, all entity types, 8s timeout, StartSpan), and `ExtractAndStoreRelationships` (background goroutine, LLM call, SPO nodes stored). Updated `process_entry.go` to call `ResolveAndLinkEntities` synchronously and `ExtractAndStoreRelationships` in a goroutine. Updated `app_capabilities.txt` with ingest-time entity resolution and SPO extraction bullets. All 7 new tests pass (`TestBuildRelationshipExtractor_*` x2, `TestParseSPOLines_*` x5). `go build ./...`, `go vet ./...`, and `go test ./...` all clean.
