# Brief: <Short Feature Name>

**Date:** YYYYMMDD
**Status:** `in-progress` | `done` | `abandoned`
**Branch:** `feature/<name>`
**Worktree:** `../<project>-<name>`

---

## Goal

_One paragraph. What does this accomplish and why does it matter?_

---

## Scope

**In:**
- ...

**Out:**
- ...

---

## Approach & Key Decisions

_Narrative of what we're doing and why. Update this as decisions are made — this is the primary context anchor for the LLM across sessions._

---

## Affected Areas

_Check all that apply and note specifics:_

- [ ] Agent / FOH loop — review `blueprint.md` before changing
- [ ] Tools — register via `tools.Register()` in `init()`, co-locate by domain
- [ ] Prompts / `app_capabilities.txt` — update if Jot's capabilities change
- [ ] Firestore schema or queries — update `firestore.indexes.json` if new composite indexes needed
- [ ] New dependencies / infra clients — pass via `*infra.App`, never hidden in context
- [ ] API routes or cron jobs
- [ ] Memory / journal behavior (Gold vs Gravel semantics)

---

## Open Questions

- [ ] ...

---

## Checklist

**Implementation**
- [ ] New code passes `*infra.App` explicitly — no `infra.GetApp(ctx)` in new code
- [ ] All logging uses `LoggerFrom(ctx)` — no `fmt.Print` or raw `slog`
- [ ] Debug logs pass full strings — no truncation at Debug level
- [ ] User-origin strings wrapped with `WrapAsUserData()` in any prompt
- [ ] LLM JSON parsed via `llmjson.RepairAndUnmarshal`
- [ ] Every significant agentic step has `StartSpan` / `defer span.End()`
- [ ] Errors wrapped with `%w`, not `%v`
- [ ] No file exceeds 400 lines

**Firestore (if applicable)**
- [ ] Composite indexes defined in `firestore.indexes.json`
- [ ] `firebase deploy --only firestore:indexes` run (or `./scripts/deploy.sh`)

**Wrap-up**
- [ ] `app_capabilities.txt` updated if capabilities changed
- [ ] `blueprint.md` consulted if core agentic loop was touched
- [ ] Tests added / updated
- [ ] Brief status set to `done` and file moved to `briefs/done/`

---

## Session Log

_The LLM appends a short bullet summary here at the end of each session. Most recent first._

<!-- YYYYMMDD -->
- ...
