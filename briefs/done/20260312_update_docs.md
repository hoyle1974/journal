# Brief: Synchronize Documentation with Codebase State

**Date:** 20260312
**Status:** `done`
**Branch:** `chore/update-docs`
**Worktree:** _(removed after merge)_

---

## Goal

Synchronize the project's documentation (`README.md`, `blueprint.md`, and `internal/prompts/app_capabilities.txt`) with the actual architectural and feature state of the codebase. Several API endpoints, `_system` state documents, and file paths have drifted from what is currently written in the docs. Updating these ensures the AI context and human developers have an accurate source of truth.

---

## Scope

**In:**
- **`README.md`**: Fix outdated file references in the "Project Structure" section.
- **`blueprint.md`**: 
  - Update the `_system` collection description in the Memory Hierarchy table to include sync-related documents and onboarding.
  - Expand the API entry points list to include all public and protected routes currently registered in the router.
- **`internal/prompts/app_capabilities.txt`**: 
  - Update the API entry points list to include recent additions (e.g., sync, entries, decay, pending questions).

**Out:**
- Code refactoring or logic changes.
- Adding new features or API endpoints.

---

## Approach & Key Decisions

A review of the codebase compared to the `.md` and `.txt` files reveals a few areas of drift:

1. **File Paths (`README.md`)**: The README references `agents.go`, `cron.go`, and `query_agent.go` in the root or unstructured paths. These now live in `pkg/agent/` and `internal/service/` (`pkg/agent/dreamer.go`, `pkg/agent/foh.go`, `internal/service/cron.go`).
2. **API Routes (`blueprint.md` & `app_capabilities.txt`)**: Both files list a subset of the API (`/query`, `/log`, `/dream`, `/rollup`, `/janitor`, `/plan`). The router (`internal/api/router.go`) now also handles `/entries`, `/sync`, `/pending-questions`, `/webhook`, `/sms`, `/decay-contexts`, and `/backfill-embeddings`. 
3. **System State (`blueprint.md`)**: The `_system` collection tracks more than just `dream_run` and `deploy_meta`. It also tracks `sync_lock`, `sync_state`, `sync_debounce`, and `onboarding`.

**Instructions for updates:**

* **In `README.md`:** * Change `* **agents.go** & **cron.go**: Logic for the Dreamer...` to `* **pkg/agent/** & **internal/service/cron.go**: Logic for the Dreamer...`.
    * Change `* **query_agent.go**: The main Front-of-House...` to `* **pkg/agent/foh.go**: The main Front-of-House...`.
* **In `blueprint.md`:** * *Section 2*: Update the `_system` row in the Memory Hierarchy table. Logic: "`dream_run`, `deploy_meta`, `sync_lock`, `sync_state`, `sync_debounce`, `onboarding`."
    * *Section 4*: Update the API entry points: `POST /query, /log, /dream, /rollup, /janitor, /plan, /sync, /decay-contexts, /backfill-embeddings, /webhook, /sms; GET /dream/latest, /dream/status, /metrics, /entries, /pending-questions; POST /pending-questions/:id/resolve`.
* **In `internal/prompts/app_capabilities.txt`:** * *Entry points*: Update the API bullet to match the expanded list above so the gap-detector and agents are aware of the full API surface, particularly for `/pending-questions` and `/sync`.

---

## Affected Areas

- [ ] Agent / FOH loop 
- [ ] Tools 
- [x] Prompts / `app_capabilities.txt` — LLM awareness of endpoints updated.
- [ ] Firestore schema or queries 
- [ ] New dependencies / infra clients 
- [ ] API routes or cron jobs 
- [x] Documentation (`README.md`, `blueprint.md`)

---

## Open Questions

- [ ] Do we need to document the internal cloud task routes (`/internal/process-entry`, `/internal/process-sms-query`, `/internal/save-query`, `/internal/dream-run`) in the standard API entry points lists, or keep them omitted since they are infra-only? *(Decision: Keep them omitted from general capabilities to avoid LLM confusion, but maybe note them as internal tasks in the blueprint).*

---

## Checklist

**Implementation**
- [x] `README.md` updated with correct paths.
- [x] `blueprint.md` API endpoints and `_system` collection updated.
- [x] `app_capabilities.txt` API endpoints updated.
- [x] Ensure no code logic was accidentally modified during text replacements.

**Wrap-up**
- [x] Changes reviewed for accuracy against `router.go` and `app.go`.
- [x] Brief status set to `done` and file moved to `briefs/done/`

---

## Key Files
- `README.md`
- `blueprint.md`
- `internal/prompts/app_capabilities.txt`

---

## Session Log

<!-- 20260312 -->
- Worktree `../jot-update-docs` created on branch `chore/update-docs`. Updated README.md (pkg/agent/, internal/service/cron.go, pkg/agent/foh.go), blueprint.md (_system docs: dream_run, deploy_meta, sync_lock, sync_state, sync_debounce, onboarding; API list extended with /sync, /entries, /pending-questions, /decay-contexts, /backfill-embeddings, /webhook, /sms, POST /pending-questions/:id/resolve), app_capabilities.txt (matching API list). All changes verified against internal/api/router.go. Checklist marked; ready for merge/closeout.
- Brief generated to capture documentation drift in API routes, system state, and file paths.
