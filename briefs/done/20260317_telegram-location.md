# Brief: Telegram Location Ingestion & Reverse Geocoding

**Date:** 20260317
**Status:** `done`
**Branch:** `feature/telegram-location`
**Worktree:** `../jot-telegram-location`

---

## Goal

Enable Jot to natively receive, parse, and log location pins sent via Telegram. The system will extract the coordinates, asynchronously reverse-geocode them into a human-readable city/neighborhood name, and append this spatial context directly to the journal entry text so the Front of House (FOH) agent can reason about the user's whereabouts.

---

## Scope

**In:**
- Update `pkg/telegram/telegram.go` structs (`TgMsg`, `IncomingMessage`) to capture Telegram's `location` object (`latitude`, `longitude`).
- Modify `ParseWebhook` to extract location data and apply a fallback text string ("Shared location pin") if the user sends a pin without a caption.
- Update `handleTelegram` in `internal/api/handler_telegram.go` to accept messages that contain a location (bypassing the "empty message" guardrail) and append the coordinates to the Cloud Tasks payload.
- Update `handleProcessTelegramQuery` in `internal/api/handler_tasks.go` to receive the coordinates.
- Implement a lightweight, context-aware reverse-geocoding function in `internal/api/handler_tasks.go` using BigDataCloud's free API to translate lat/lng into a city/state.
- Append the location data (e.g., `[Location: 37.77, -122.41 (San Francisco, CA)]`) to the `data.Body` before it is passed to `agent.AddEntry` and the FOH query loop.

**Out:**
- Continuous "Live Location" tracking (treat any location ping as a static, point-in-time check-in).
- Rendering maps in the CLI or API outputs.
- Modifying the Firestore schema (the location is injected purely as text into the entry `content`).

---

## Approach & Key Decisions

**1. Struct Updates (`pkg/telegram/telegram.go`)**
- Add `TgLocation { Longitude float64, Latitude float64 }`.
- Add `Location *TgLocation` to `TgMsg`.
- Add `HasLocation bool`, `Latitude float64`, and `Longitude float64` to `IncomingMessage`.

**2. Webhook Parsing**
- In `ParseWebhook`, if `msg.Location` is not nil, populate the `IncomingMessage` fields. If `Text` and `ImageFileID` are empty, set `Text = "Shared location pin"` so the entry has base content.

**3. The Cloud Task Handoff (`internal/api/handler_telegram.go`)**
- In `handleTelegram`, modify the empty-content check to allow through payloads where `incoming.HasLocation` is true.
- Inject `has_location`, `latitude`, and `longitude` into the `payload` map passed to `EnqueueTask`.

**4. Async Processing & Reverse Geocoding (`internal/api/handler_tasks.go`)**
- Update the `data` struct in `handleProcessTelegramQuery` to include `HasLocation`, `Latitude`, and `Longitude`.
- **API Note:** Open-Meteo only supports *forward* geocoding. We will use `https://api.bigdatacloud.net/data/reverse-geocode-client?latitude={lat}&longitude={lng}&localityLanguage=en`. It requires no API key and returns localized data.
- **Decision:** Perform the reverse-geocoding *synchronously* inside the task handler before calling `agent.AddEntry`.
- Format the string: `[Location: {lat}, {lng} ({City}, {PrincipalSubdivision})]`. If reverse-geocoding fails, fallback gracefully to `[Location: {lat}, {lng}]`.
- Append this string to `data.Body`.

---

## Edge Cases & Pre-Flight Checks

1. **Reverse-Geocoding API Failure:** External APIs rate-limit or go down. The HTTP call for reverse geocoding MUST have a strict, short timeout (e.g., 3 seconds). If it fails, times out, or returns a non-200 status, the code must gracefully swallow the error and fall back to appending the raw latitude/longitude so the journal entry is still saved.
2. **Cloud Tasks Payload Size:** Adding two floats and a boolean is negligible and safely under the 100KB Cloud Tasks limit.
3. **Data Overwrite:** Ensure that appending the location string to `data.Body` happens *before* the check `if data.Body == "Shared location pin"`.

---

## Affected Areas

- [x] Agent / FOH loop — No direct changes, but the agent receives richer text context.
- [ ] Tools
- [ ] Prompts / `app_capabilities.txt`
- [ ] Firestore schema or queries
- [ ] New dependencies / infra clients — Standard `net/http` client used for BigDataCloud call.
- [x] API routes or cron jobs — `handler_telegram.go` and `handler_tasks.go` handle the pipeline.
- [ ] Memory / journal behavior (Gold vs Gravel semantics)

---

## Open Questions

- [ ] Will we eventually want a `get_current_location` tool that caches the last received location pin? *(Decision for now: Keep it stateless. The FOH will rely on the semantic search of recent journal entries to find the user's location).*

---

## Checklist

**Implementation**
- [ ] Update `pkg/telegram/telegram.go` with location structs and extraction logic.
- [ ] Update `handleTelegram` in `internal/api/handler_telegram.go` to process location-only payloads and add lat/lng to the Cloud Tasks enqueue map.
- [ ] Update `handleProcessTelegramQuery` in `internal/api/handler_tasks.go` to receive the location fields.
- [ ] Write `reverseGeocode(lat, lng float64) string` helper function in `internal/api/handler_tasks.go` with a 3-second HTTP timeout.
- [ ] Append the formatted `[Location: ...]` string to `data.Body` before logging the entry.
- [ ] Ensure all new logging uses `LoggerFrom(ctx)`.

**Verification (Proof of Work)**
- [ ] **Compilation:** `go build ./...` passes cleanly.
- [ ] **Tests:** `go test ./...` passes.
- [ ] **Lint/Format:** Code is formatted and passes `go vet`.
- [ ] **Manual Smoke Test:** Send a location pin to the Telegram bot. Verify in Firestore that the created entry's content contains the reverse-geocoded city name.

**Wrap-up**
- [ ] Brief status set to `done` and file moved to `briefs/done/`.

---

## Key Files

`briefs/active/20260317_telegram-location.md`
`pkg/telegram/telegram.go`
`internal/api/handler_telegram.go`
`internal/api/handler_tasks.go`

---

## Session Log

- Brief created for Telegram location ingestion and BigDataCloud reverse geocoding.
- Worktree created at `../jot-telegram-location`.
