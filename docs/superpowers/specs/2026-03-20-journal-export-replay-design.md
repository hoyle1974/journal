# Journal Export & Replay Design

**Date:** 2026-03-20
**Status:** Approved

## Overview

A two-part system to export Firestore journal entries (raw inputs) to a portable local archive, and replay them through the HTTP processing pipeline on any environment. Useful for testing the processing pipeline on dev using real prod data.

---

## Archive Format

A self-contained directory with a flat media layout:

```
jot-export-2026-03-20/
  manifest.jsonl        # one JSON object per line, sorted ascending by timestamp
  images/
    <uuid>.<ext>        # downloaded from GCS (jpg, png, webp, gif)
  audio/
    <uuid>.ogg          # downloaded from GCS
```

Each line in `manifest.jsonl`:
```json
{
  "uuid": "abc-123",
  "content": "some text",
  "source": "telegram",
  "timestamp": "2026-01-15T10:30:00Z",
  "image_file": "images/abc-123.jpg",
  "audio_file": null
}
```

All paths in `image_file` and `audio_file` are relative to the archive root. The replayer resolves them as `filepath.Join(archiveDir, entry.ImageFile)`.

**Location notes:** Location pins are converted to text content by the Telegram handler before storage — no `lat`/`lon` fields exist on stored entries. Location replays naturally via `content`.

**Completeness signal:** `manifest.jsonl` is written last. A directory without a manifest indicates an incomplete/interrupted export.

---

## Export

### Admin CLI Subcommand

```
go run ./cmd/admin export-journal --output ./jot-export-2026-03-20
```

- Follows existing admin CLI pattern (shared infra setup, Firestore client)
- Queries `journal` collection for all `node_type: "log"` entries, sorted ascending by `timestamp`
- For each entry with `image_url` (`gs://` path): downloads via `app.ImageStorage().DownloadImage(ctx, uri)`, saves to `images/`
- For each entry with `audio_url` (`gs://` path): downloads via `app.ImageStorage().(*storage.GCSImageStorage).DownloadAudio(ctx, uri)` — requires adding a `DownloadAudio(ctx, uri)` method to `GCSImageStorage`. Both require `JOT_IMAGES_BUCKET` set in the sourced env file.
- If a GCS download fails (object deleted or permissions error): log a warning, set `image_file: null` / `audio_file: null` in the manifest for that entry, and continue — do not abort the export
- Writes `manifest.jsonl` last
- Prints progress: `exported 142/308 entries...`
- `--output` is required; fails if directory already exists (prevents accidental overwrites)

### Shell Script

```
./scripts/export-journal.sh <dev|prod>
```

- Follows exact pattern of `reset-firestore.sh`: uses `scripts/lib/env-confirm.sh`, sources env file, calls admin subcommand
- Auto-names output directory: `jot-export-YYYY-MM-DD` in repo root
- Shell script checks for directory existence before invoking Go binary and emits a clear error: `"Archive jot-export-YYYY-MM-DD already exists. Remove it or rename it before running again."`

---

## Playback

### Admin CLI Subcommand

```
go run ./cmd/admin replay-journal --archive ./jot-export-2026-03-20 --api-url http://localhost:8080 --api-key <key>
```

- Reads `manifest.jsonl` in order (already sorted by timestamp)
- For each entry, POSTs to `/internal/replay` with:
  - `content`, `source`, `timestamp` as form fields
  - image or audio file as multipart upload if present in archive
- `--api-url` defaults to `JOT_API_URL` env var; `--api-key` defaults to `JOT_API_KEY` — sourcing `.env` first means no explicit flags needed
- Prints progress: `replayed 12/308 entries...`

### Shell Script

```
./scripts/replay-journal.sh <dev|prod> <archive-dir>
```

- Same env-confirm pattern; passes `--archive` and API credentials from sourced env file

### New Endpoint: `POST /internal/replay`

A dedicated endpoint rather than reusing `/log` — this keeps the replay path isolated from production code paths (separate metrics, no risk of modifying prod behavior, clearly labelled test/dev route).

- Auth: `X-API-Key` header (same as other internal endpoints)
- Registered with `wrapAPI` wrapper (returns `(any, error)`) consistent with all other `/internal/*` handlers
- Rate limit: `RateLimitMiddleware(300)` — higher than normal internal tasks to accommodate bulk replay
- Accepts multipart form:
  - `content` (string)
  - `source` (string)
  - `timestamp` (RFC3339 string — overrides the default "now" behavior)
  - `image` (file, optional)
  - `audio` (file, optional)
- Uploads any provided media to GCS (same path as normal flow)
- Calls `AddEntry` with timestamp override
- Enqueues `ProcessEntry` exactly as normal flow
- No dev-only gating — API key is sufficient protection

---

## Files to Create / Modify

| Action | Path |
|--------|------|
| Create | `cmd/admin/export_journal.go` |
| Create | `cmd/admin/replay_journal.go` |
| Create | `scripts/export-journal.sh` |
| Create | `scripts/replay-journal.sh` |
| Modify | `cmd/admin/main.go` — register new subcommands |
| Modify | `internal/api/router.go` (or equivalent) — register `/internal/replay` |
| Create | `internal/api/handler_replay.go` |

---

## Out of Scope

- Incremental/delta exports (export since last run)
- Rate limiting / delay between replayed entries
- Filtering by date range or source
- Deduplication on replay (replaying twice will create duplicate entries)
