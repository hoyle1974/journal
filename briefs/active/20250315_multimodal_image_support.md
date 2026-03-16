# Brief: Multimodal image support

**Date:** 20250315
**Status:** in-progress
**Branch:** `feature/multimodal-image-support`
**Worktree:** `../jot-multimodal-image-support`

---

## Goal

Add image support to JOT so users can attach images to journal entries via CLI (`--attach`), API (multipart/form-data on `/log`), and Telegram (photo messages). Images are stored in Google Cloud Storage; Firestore entries get an optional `image_url`. Gemini is already multimodal, so FOH and Dreamer can later consume image data when present.

---

## Scope

**In:**
- GCS bucket for image blobs; `pkg/storage` with `UploadImage` returning URI.
- Journal entry schema: optional `image_url` (or `image_path`) on entries.
- API `/log`: accept multipart/form-data with `text` + optional `image` file; pass image bytes to Agent.AddEntry.
- CLI: `--attach <path>` for log command; send multipart when image present.
- Telegram: detect `message.photo`, download via getFile, add entry with caption + image bytes.
- Infra: add Storage interface to App; wire GCS client in NewApp.

**Out (this brief):**
- FOH/Dreamer actually passing image Parts to Gemini (follow-up brief).
- Image display in any UI.

---

## Approach & Key Decisions

- **Storage:** Images in GCS; Firestore stores only `image_url` (gs:// or signed URL). New `pkg/storage` with interface; infra holds GCS client, optional so deploy works without bucket.
- **Journal:** `journal.AddEntry` gains optional `imageURL string`; caller (AgentService) uploads via Storage then passes URL. Entry struct and Firestore doc get `image_url` when set.
- **API:** handleLog checks Content-Type: if `multipart/form-data`, parse form (10MB limit), read `text` and optional `image` file; call `Agent.AddEntry(ctx, content, source, timestamp, imageBytes)`. Else decode JSON as today.
- **CLI:** Flag `--attach <path>`; if set, build multipart body with `content` and file, POST to `/log`.
- **Telegram:** Extend `TgMsg` to parse `photo` array; `IncomingMessage` gets `ImageFileID`; add `DownloadFile(ctx, fileID)` in pkg/telegram using getFile API; handler downloads, then calls AddEntry with caption + imageBytes. Task payload already has body; we add entry first (with image), then enqueue process-telegram-query as today.

---

## Edge Cases & Pre-Flight Checks

1. **Upload size:** Limit multipart to 10MB to avoid abuse; reject oversized with 400.
2. **GCS not configured:** If bucket name empty or client nil, AddEntry with imageBytes should return clear error (e.g. "image upload not configured").
3. **Telegram photo-only:** If user sends photo with no caption, use empty string or placeholder ("Photo") for content so entry still has text field.

---

## Affected Areas

- [x] New dependencies / infra clients — GCS client in infra, Storage interface
- [x] API routes — `/log` multipart handling
- [x] Memory / journal behavior — entry schema `image_url`
- [ ] Agent / FOH loop — not in this brief (future: pass image to Gemini)
- [x] Prompts / `app_capabilities.txt` — updated with multimodal capabilities and has_image param
- [ ] Firestore schema or queries — no new indexes; optional field only

---

## Open Questions

- [ ] GCS bucket name: config field (e.g. `JOT_IMAGES_BUCKET`) or derived from project?

---

## Checklist

**Implementation**
- [ ] New code passes `*infra.App` explicitly
- [ ] All logging uses `LoggerFrom(ctx)`
- [ ] Errors wrapped with `%w`
- [ ] No file exceeds 400 lines

**Verification**
- [ ] `go build ./...` passes
- [ ] `go test ./...` passes
- [ ] Manual: `jot log --attach ./image.png "Caption"` and curl multipart to `/log`

**Wrap-up**
- [ ] `app_capabilities.txt` updated if capabilities changed
- [ ] Brief status set to `done` and moved to `briefs/done/`

---

Key Files

briefs/active/20250315_multimodal_image_support.md (this file)
internal/infra/app.go
pkg/storage/gcs.go (new)
pkg/journal/entries.go
internal/service/agent_service.go
internal/agent/foh_helpers.go
internal/api/handler_interact.go
internal/api/backend.go
cmd/jot/main.go
pkg/telegram/telegram.go
internal/service/telegram_service.go
internal/api/handler_telegram.go
internal/config/config.go

---

## Session Log

- 20250315: Implemented Phase 1–4: GCS storage (pkg/storage), infra.ImageStorage, journal image_url, Agent.AddEntry(imageBytes), handleLog multipart, CLI --attach, Telegram photo download + AddEntry. Build and tests pass. app_capabilities.txt updated. FOH/Dreamer consuming image Parts left for follow-up.
- 20250316: Multimodal agent awareness: (1) app_capabilities.txt — added "Multimodal capabilities" section and has_image param note on journal tools. (2) system_prompt.txt — identity updated to "multimodal Agentic Second Brain", guidance to use has_image when reasoning about photos. (3) Journal tools — get_recent_entries, get_entries_by_date_range, search_entries now accept optional has_image=true; filterEntriesWithImage in helpers.go. (4) pkg/journal — Entry.ParsedImageDescription added; FormatEntriesForContext appends "[Attached Image Content: ...]" or "[Attached image]" when entry has image so FOH tool results expose visual data to the LLM.
