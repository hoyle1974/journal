# Brief: Return Ingested Images to Telegram Users

**Date:** 20260316
**Status:** `done`
**Branch:** `feature/return-telegram-images`
**Worktree:** `../jot-return-telegram-images`

---

## Goal

When a user asks Jot via Telegram to show a previously ingested image (e.g. "show me the photo I sent last Tuesday"), the system should retrieve that image from GCS and send it back as an actual Telegram photo — not just a text description. Currently, images are stored in GCS and their captions surface via journal tools, but there is no path to send the raw image bytes back over Telegram's `sendPhoto` API.

---

## Scope

**In:**
- GCS download capability in `pkg/storage` (complement to `UploadImage`)
- `sendPhoto` method on the Telegram client in `pkg/telegram`
- A new `retrieve_image` tool that lets FOH fetch an image entry and trigger delivery
- FOH response path: when a tool result contains an image payload, the Telegram handler sends it via `sendPhoto` instead of (or in addition to) `sendMessage`

**Out:**
- Serving images over HTTP (no public URL / signed URL generation for now)
- Image retrieval from non-Telegram sources (web, interact endpoint)
- Modifying how images are ingested or captioned
- Returning multiple images in a single response (start with one)

---

## Approach & Key Decisions

**Storage layer:** `pkg/storage/gcs.go` has `UploadImage(ctx, bytes) (string, error)` but no download. Add `DownloadImage(ctx, gsURI string) ([]byte, string, error)` that parses the `gs://bucket/path` URI and fetches bytes + content-type via the GCS client.

**Telegram layer:** `pkg/telegram/telegram.go` has `SendMessage`. Add `SendPhoto(ctx, chatID int64, caption string, imageBytes []byte, mimeType string)` using Telegram's `sendPhoto` endpoint with multipart form upload.

**Tool:** Add `retrieve_image` in `internal/tools/impl/journal_tools.go` (or a new `image_tools.go`). It accepts an entry UUID or a natural-language date+description. It:
1. Looks up the journal entry (already have `GetEntryByUUID` or can search by date with `has_image=true`)
2. Calls `app.ImageStorage().DownloadImage(ctx, entry.ImageURL)`
3. Returns a structured tool result with `image_bytes` and `mime_type` fields that the response path can act on

**Response path:** The FOH query result already returns a string answer. We need a lightweight side-channel for binary payloads. Options:
- **Option A (preferred):** Tool result carries a special sentinel string like `[SEND_IMAGE:<entry_uuid>]`; the Telegram task handler detects this, fetches the image, and calls `SendPhoto` before or after `SendMessage`.
- **Option B:** Return image bytes through the tool result struct directly (requires a new `ToolResult` variant).

Option A is simpler and avoids changing the core FOH response contract. The task handler in `handler_tasks.go` already post-processes the FOH answer string — it can parse the sentinel and dispatch `SendPhoto`.

**Image lookup:** FOH will use the existing `search_entries` or `get_entries_by_date_range` tools (with `has_image=true`) to find the entry UUID, then call `retrieve_image` with that UUID. No new search mechanism needed.

---

## Edge Cases & Pre-Flight Checks

1. **GCS URI format:** `image_url` is stored as `gs://bucket/images/<uuid>`. The download function must parse this correctly and handle missing bucket env var gracefully (same no-op pattern as upload).
2. **Telegram file size limit:** `sendPhoto` accepts up to 10MB; `sendDocument` up to 50MB. Images ingested via Telegram are already capped at 10MB on download, but we should re-check on send and fall back to a caption-only response if the image is oversized or the GCS fetch fails.
3. **Sentinel collision:** The `[SEND_IMAGE:...]` pattern must not appear in normal LLM output. Use a UUID-keyed sentinel that is highly unlikely to occur naturally, and scope detection to only the Telegram handler path.

---

## Affected Areas

- [ ] Agent / FOH loop — review `blueprint.md` before changing
- [x] Tools — new `retrieve_image` tool, register via `tools.Register()` in `init()`
- [x] Prompts / `app_capabilities.txt` — update to describe image retrieval capability
- [ ] Firestore schema or queries — no new indexes needed (entry UUID lookup is by doc ID)
- [x] New dependencies / infra clients — `ImageStorage().DownloadImage()` passed via `*infra.App`
- [x] API routes or cron jobs — `handler_tasks.go` Telegram task path updated
- [ ] Memory / journal behavior (Gold vs Gravel semantics)

---

## Open Questions

- [ ] Should the Telegram response include both the image AND a text caption (the stored `parsed_image_description`), or just the image alone?
- [ ] If no image is found matching the user's query, what fallback message should FOH send?
- [ ] Do we need `sendDocument` as a fallback for non-JPEG/PNG types (e.g. WebP, GIF)?

---

## Checklist

**Implementation**
- [ ] New code passes `*infra.App` explicitly — no `infra.GetApp(ctx)` in new code
- [ ] All logging uses `LoggerFrom(ctx)` — no `fmt.Print` or raw `slog`
- [ ] Debug logs pass full strings — no truncation at Debug level
- [ ] User-origin strings wrapped with `WrapAsUserData()` in any prompt
- [ ] LLM output parsed as key/value lines via `pkg/utils.ParseKeyValueMap` (no JSON)
- [ ] Every significant agentic step has `StartSpan` / `defer span.End()`
- [ ] Errors wrapped with `%w`, not `%v`
- [ ] No file exceeds 400 lines

**Firestore (if applicable)**
- [ ] N/A — no new composite indexes

**Verification (Proof of Work)**
- [ ] **Compilation:** `go build ./...` passes cleanly.
- [ ] **Tests:** `go test ./...` passes.
- [ ] **Lint/Format:** Code is formatted and passes `go vet`.
- [ ] **Manual Smoke Test:** Send a photo to the Telegram bot, then ask "show me the photo I just sent" and confirm the bot replies with the actual image via `sendPhoto`.

**Wrap-up**
- [ ] `app_capabilities.txt` updated — describe `retrieve_image` tool
- [ ] `blueprint.md` consulted if core agentic loop was touched
- [ ] Tests added / updated
- [ ] Brief status set to `done` and file moved to `briefs/done/`

---

## Key Files

```
briefs/active/20260316_return-telegram-images.md   ← this file
pkg/storage/gcs.go                                 ← add DownloadImage()
pkg/telegram/telegram.go                           ← add SendPhoto()
internal/tools/impl/journal_tools.go               ← add retrieve_image tool (or new image_tools.go)
internal/api/handler_tasks.go                      ← detect [SEND_IMAGE:] sentinel, dispatch SendPhoto
internal/prompts/app_capabilities.txt              ← document new capability
```

---

## Session Log

_The LLM appends a short bullet summary here at the end of each session. Most recent first._

<!-- 20260316 -->
- Implementation complete. 9 files changed (257 insertions). `go build ./...` and `go test ./...` both pass. Committed on `feature/return-telegram-images` (5031ecb).
  - `pkg/storage`: Added `DownloadImage` to `ImageStorage` interface; implemented on `GCSImageStorage` (parses `gs://` URI, streams via GCS client) and `noopImageStorage`.
  - `pkg/telegram`: Added `SendPhoto` (multipart upload to `sendPhoto` Bot API endpoint, 60s timeout, graceful filename mapping per MIME type).
  - `internal/service`: `TelegramService.SendPhoto` wraps the above.
  - `internal/api/backend.go`: `TelegramService` interface extended with `SendPhoto`.
  - `internal/tools/impl/image_tools.go`: New `retrieve_image` tool — fetches entry by UUID, validates `image_url` exists, returns `[SEND_IMAGE:<uuid>]` sentinel + description for FOH to embed verbatim.
  - `internal/api/handler_tasks.go`: `sendTelegramResponse` helper parses sentinel via `parseSentinel`, downloads from GCS via `app.ImageStorage().DownloadImage`, calls `SendPhoto`; falls back to text on any failure.
  - `internal/prompts/app_capabilities.txt`: Documents `retrieve_image` and two-step usage pattern.

- Created brief. Explored ingestion path: images arrive via Telegram, stored in GCS (`gs://bucket/images/<uuid>`), entry has `image_url` + `parsed_image_description`. No existing retrieval/send-back path. Chose sentinel-string approach (`[SEND_IMAGE:<uuid>]`) to avoid changing FOH return contract.
