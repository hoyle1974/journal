# Brief: Weather Tool

**Date:** 20260316
**Status:** `in-progress`
**Branch:** `feature/weather-tool`
**Worktree:** `../jot-weather-tool`

---

## Goal

Add a `get_weather` tool that fetches current conditions and a short forecast for a given location using the Open-Meteo API (no API key required). Also add "What city do you live in?" to the onboarding questions so the user's home location is stored as identity, enabling FOH to pass it as a default when weather is requested without a specific location.

---

## Scope

**In:**
- `internal/tools/impl/weather_tools.go` — new tool file with `get_weather`
- `internal/service/onboarding.go` — add location onboarding question
- `internal/prompts/app_capabilities.txt` — document new tool

**Out:**
- No new Firestore collections or indexes
- No API keys required

---

## Approach & Key Decisions

- Use Open-Meteo geocoding API (`https://geocoding-api.open-meteo.com/v1/search`) to convert city name → lat/lng
- Use Open-Meteo forecast API (`https://api.open-meteo.com/v1/forecast`) for current + daily forecast
- Return current temp, weather code description, wind speed, and a 3-day daily summary
- Tool args: `location` (required), `unit` (celsius/fahrenheit, default fahrenheit), `days` (1–7, default 3)
- FOH will look up user's stored location from identity nodes if user doesn't specify one

---

## Edge Cases & Pre-Flight Checks

1. Ambiguous city names (e.g., "Springfield") — Open-Meteo returns multiple results; use the first (highest population)
2. Unknown city — geocoding returns empty results; return a clear Fail message
3. Network timeout — 10s timeout on both HTTP calls, return Fail with error

---

## Affected Areas

- [x] Tools — register via `tools.Register()` in `init()`, co-locate by domain
- [x] Prompts / `app_capabilities.txt` — update if Jot's capabilities change

---

## Checklist

**Implementation**
- [ ] New code passes `*infra.App` explicitly — no `infra.GetApp(ctx)` in new code
- [ ] All logging uses `LoggerFrom(ctx)` — no `fmt.Print` or raw `slog`
- [ ] Errors wrapped with `%w`, not `%v`
- [ ] No file exceeds 400 lines

**Verification (Proof of Work)**
- [ ] **Compilation:** `go build ./...` passes cleanly.
- [ ] **Tests:** `go test ./...` passes.

**Wrap-up**
- [ ] `app_capabilities.txt` updated
- [ ] Brief status set to `done` and file moved to `briefs/done/`

---

## Key Files

briefs/active/20260316_weather-tool.md (this file)
internal/tools/impl/weather_tools.go
internal/service/onboarding.go
internal/prompts/app_capabilities.txt

---

## Session Log

<!-- 20260316 -->
- Created brief; implementing weather tool with Open-Meteo, adding location onboarding question
