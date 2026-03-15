# Brief: Extract Twilio/SMS Logic from Infrastructure

**Date:** 20260314
**Status:** done
**Branch:** refactor/extract-twilio
**Worktree:** ../jot-extract-twilio (removed after merge)

---

## Goal

Remove all Twilio and SMS-specific structs, parsing logic, and network calls from the pkg/infra package. Create a dedicated pkg/sms package. This aligns with Clean Architecture principles, ensuring that the core infrastructure package handles generic application wiring (logging, telemetry, database connections) and does not leak details about specific external delivery providers like Twilio.

---

## Scope

**In:**
- Create pkg/sms/twilio.go (and any necessary test files).
- Move TwilioWebhookRequest struct from pkg/infra to pkg/sms.
- Move ValidateTwilioSignature from pkg/infra to pkg/sms.
- Move ParseTwilioWebhook from pkg/infra to pkg/sms.
- Move IsAllowedPhoneNumber from pkg/infra to pkg/sms.
- Move SendSMS from pkg/infra to pkg/sms.
- Update internal/service/sms_process.go and internal/service/sms_service.go to import and use the new pkg/sms package.
- Update internal/api/handler_sms.go and internal/api/handler_tasks.go to reference sms.TwilioWebhookRequest instead of infra.TwilioWebhookRequest.
- Update internal/api/backend.go's SMSService interface to use *sms.TwilioWebhookRequest.

**Out:**
- Changes to the underlying Twilio validation or sending logic.
- Changes to how the Agent processes the SMS query (the actual LLM invocation).

---

## Approach & Key Decisions

The pkg/infra package should only be responsible for cross-cutting technical concerns (Firestore client, Gemini client setup, OpenTelemetry tracing). By moving Twilio logic to pkg/sms, we isolate the third-party dependency. The internal/service/sms_service.go acts as a bridge between the API layer and the Twilio implementation; we repointed that bridge to pkg/sms. pkg/sms uses a Logger interface and accepts logger as a parameter (e.g. SendSMS(..., log Logger)) to avoid importing pkg/infra and prevent circular dependencies.

---

## Edge Cases & Pre-Flight Checks

1. Circular dependencies: pkg/infra does not import pkg/sms; pkg/sms does not import pkg/infra (uses Logger interface).
2. Environment variables: pkg/sms accesses Twilio credentials via *config.Config passed by callers.

---

## Affected Areas

- [ ] Agent / FOH loop
- [ ] Tools
- [ ] Prompts / app_capabilities.txt
- [ ] Firestore schema or queries
- [x] New dependencies / infra clients — Twilio logic relocated to pkg/sms.
- [x] API routes or cron jobs — handler_sms.go, handler_tasks.go.
- [ ] Memory / journal behavior

---

## Checklist

**Implementation**
- [x] pkg/sms/twilio.go created.
- [x] TwilioWebhookRequest and associated functions moved to pkg/sms.
- [x] pkg/infra cleaned of all Twilio references.
- [x] internal/api/backend.go interface updated.
- [x] internal/service/sms_service.go updated to use pkg/sms.
- [x] internal/service/sms_process.go updated to use pkg/sms.
- [x] internal/api/handler_sms.go (no code change; uses interface).
- [x] internal/api/handler_tasks.go updated to use pkg/sms.
- [x] New code passes *infra.App explicitly where needed; pkg/sms is infra-free.
- [x] Logging via Logger interface / LoggerFrom(ctx) in service layer.
- [x] Errors wrapped with %w.

**Verification**
- [x] go build ./... passes.
- [x] go test ./... passes.
- [x] go vet ./... passes.

**Wrap-up**
- [x] Brief status set to done and file in briefs/done/.

---

## Key Files

pkg/sms/twilio.go, pkg/sms/twilio_test.go, internal/api/backend.go, internal/api/handler_sms.go, internal/api/handler_tasks.go, internal/service/sms_process.go, internal/service/sms_service.go.

---

## Session Log

- 20260314: Implemented extraction in worktree: created pkg/sms/twilio.go (Logger interface, TwilioWebhookRequest, ValidateTwilioSignature, ParseTwilioWebhook, NormalizePhoneNumber, IsAllowedPhoneNumber, SendSMS), twilio_test.go; updated sms_service, sms_process, backend, handler_tasks; removed pkg/infra/sms.go and sms_test.go. Committed on refactor/extract-twilio, stashed main WIP, merged into main, removed worktree. Brief closed.
