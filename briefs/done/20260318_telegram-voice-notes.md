# Brief: Telegram Voice Note Ingestion

**Date:** 20260318
**Status:** `done`
**Branch:** `feature/telegram-voice-notes`
**Worktree:** `../jot-telegram-voice-notes`

---

## Goal

Allow users to send Telegram voice notes to Jot. The audio is persisted to GCS (mirroring how images are stored), transcribed to text via Gemini's multimodal API, and then the transcription is processed exactly as if the user had typed that text — triggering the full FOH agent loop (task close-outs, journal entries, etc.).

---

## Scope

**In:**
- Detect `Voice` type messages in Telegram webhook parser
- Download OGG/Opus audio from Telegram and upload to GCS at `audio/<uuid>`
- Add `AudioURL` and `Transcription` fields to Entry struct
- `TranscribeAudio()` in `internal/infra/gemini.go` using Gemini multimodal
- Wire voice handling into `handler_telegram.go` and `handler_tasks.go`

**Out:**
- Audio file messages (non-voice type)
- Playback / retrieval of stored audio
- Speaker diarization or confidence scores

---

## Approach & Key Decisions

Mirror the image pipeline as closely as possible:

1. **Detection:** In `pkg/telegram/telegram.go` `ParseWebhook()`, check `msg.Voice != nil` and populate a new `VoiceFileID string` field on `IncomingMessage`. Voice notes from Telegram are always `audio/ogg` (Opus codec).

2. **Download & Storage:** Re-use `DownloadFile()` (same function used for images). Add `UploadAudio(ctx, data []byte) (string, error)` to `pkg/storage/gcs.go` that stores at `audio/<uuid>` with content-type `audio/ogg`. Returns GCS URI.

3. **Entry fields:** Add `AudioURL string` and `Transcription string` to `pkg/journal/entries.go` (and the extended struct). `AudioURL` is the GCS path; `Transcription` is the Gemini output.

4. **Transcription:** `TranscribeAudio(ctx, audioBytes []byte) (string, error)` in `internal/infra/gemini.go`. Sends audio as `genai.NewPartFromBytes(audioBytes, "audio/ogg")` to Gemini with a concise system prompt asking for verbatim transcription only.

5. **Handler flow (handler_telegram.go):** On `VoiceFileID` present → download audio → `UploadAudio` to GCS → create minimal entry with `AudioURL`. Enqueue process-telegram-query task (same as images).

6. **Task handler (handler_tasks.go):** On voice present → download audio again → `TranscribeAudio` → store `Transcription` on entry → pass transcription as the query body to `RunQuery()`. This is the key difference from images: the transcription becomes the FOH input directly (no "Voice note logged." short-circuit). FOH then acts on it as if typed.

7. **MIME type:** Telegram voice notes are `audio/ogg`. No magic-byte detection needed — Telegram guarantees this type for `Voice` messages.

---

## Edge Cases & Pre-Flight Checks

1. **Empty transcription:** Gemini may return empty string for silence or noise. Guard: if transcription is blank after trimming, reply "Could not transcribe voice note." and skip FOH.
2. **Download size:** 10 MiB cap already enforced by `DownloadFile`. Telegram's voice note limit is 1 minute / ~1 MB, well within bounds.
3. **GCS content-type for audio:** `UploadImage` uses magic bytes; audio won't have matching magic bytes, so a dedicated `UploadAudio` with explicit `audio/ogg` content-type is cleaner than patching the existing function.

---

## Affected Areas

- [ ] Agent / FOH loop — no changes to core loop
- [ ] Tools — no new tools
- [x] Prompts / `app_capabilities.txt` — update to mention voice note support
- [x] Firestore schema — add `audio_url` and `transcription` fields (no new indexes needed)
- [ ] New dependencies / infra clients — none
- [x] API routes — `handler_telegram.go`, `handler_tasks.go`
- [x] Memory / journal behavior — transcription treated as Gold user input

---

## Open Questions

- (none)

---

## Checklist

**Implementation**
- [ ] New code passes `*infra.App` explicitly — no `infra.GetApp(ctx)` in new code
- [ ] All logging uses `LoggerFrom(ctx)` — no `fmt.Print` or raw `slog`
- [ ] Debug logs pass full strings — no truncation at Debug level
- [ ] User-origin strings wrapped with `WrapAsUserData()` in any prompt
- [ ] Every significant agentic step has `StartSpan` / `defer span.End()`
- [ ] Errors wrapped with `%w`, not `%v`
- [ ] No file exceeds 400 lines

**Firestore (if applicable)**
- [ ] No new composite indexes needed

**Verification (Proof of Work)**
- [ ] `go build ./...` passes cleanly
- [ ] `go test ./...` passes
- [ ] `go vet ./...` passes

**Wrap-up**
- [ ] `app_capabilities.txt` updated
- [ ] Brief status set to `done` and file moved to `briefs/done/`

---

## Key Files

- `briefs/active/20260318_telegram-voice-notes.md` (this file)
- `pkg/telegram/telegram.go` — `ParseWebhook`, `IncomingMessage`, `DownloadFile`
- `pkg/storage/gcs.go` — add `UploadAudio`
- `pkg/journal/entries.go` — add `AudioURL`, `Transcription`
- `pkg/journal/entries_extended.go` — add fields to extended struct
- `internal/infra/gemini.go` — add `TranscribeAudio`
- `internal/api/handler_telegram.go` — wire voice detection
- `internal/api/handler_tasks.go` — wire transcription + FOH

---

## Session Log

<!-- 20260318 -->
- Brief created. Architecture explored: voice note pipeline mirrors image pipeline; transcription replaces caption and becomes the FOH query input directly.
