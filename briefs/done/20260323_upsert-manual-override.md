# Brief: Upsert Manual Override Guard

**Date:** 20260323
**Status:** `done`
**Branch:** `feature/upsert-manual-override`
**Worktree:** `../jot-upsert-manual-override`

---

## Goal

Enforce Refinery-first extraction by preventing FOH from using `upsert_knowledge` during normal declarative conversation, while preserving explicit user-directed corrections and manual memory commands.

---

## Scope

**In:**
- Narrow `upsert_knowledge` tool description to explicit manual override intent.
- Add server-side guard in tool implementation that blocks non-explicit calls.
- Add tests for blocked declarative calls and allowed correction/remember calls.
- Update app capability text to document manual override behavior.

**Out:**
- Removing `upsert_knowledge` entirely.
- Changing Refinery extraction logic.

---

## Checklist

**Implementation**
- [x] Restrict `upsert_knowledge` description to manual override semantics.
- [x] Add implementation guard for explicit command/correction intent.
- [x] Add regression tests for blocked/allowed patterns.
- [x] Keep compatibility for explicit user correction flows.

**Verification (Proof of Work)**
- [x] `go build ./...`
- [x] `go test ./...`
- [ ] Manual query sanity check for blocked declarative upsert behavior.

---

## Session Log

<!-- 20260323 -->
- Merged `feature/upsert-manual-override` back to `main`, removed worktree, and moved this brief to done.
- Implemented manual-override-only guard in `upsert_knowledge`, added explicit intent matcher tests, and verified `go test ./...` + `go build ./...` pass.
