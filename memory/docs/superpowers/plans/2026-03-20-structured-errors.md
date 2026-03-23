# Structured Error Handling Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace opaque string errors in the memory library with a typed error taxonomy so callers (jot) can branch logic programmatically — retry on timeout, skip on safety block, prompt the user on validation failure.

**Architecture:** Expand `errors.go` with sentinel vars, a `MemoryError` struct, `ErrorCode` constants, and boolean inspection helpers. Refactor three callsites to return typed errors: `ValidateMetadata` in `schema.go`, the Gemini dispatcher in `gemini/dispatcher.go`, and "not found" paths in `knowledge.go` and `context.go`. No new files; all changes are additive to existing files.

**Tech Stack:** Go standard library (`errors`, `fmt`, `context`), `google.golang.org/grpc/codes` and `status` (already imported), `google.golang.org/genai` (already used in the dispatcher).

---

## File Map

| File | Change |
|------|--------|
| `errors.go` | Add `ErrorCode`, `MemoryError`, sentinel vars, `wrapFirestoreNotFound`, and inspection helpers |
| `errors_test.go` | New — unit tests for `MemoryError`, sentinels, and helpers |
| `schema.go` | `ValidateMetadata` wraps its return in `MemoryError{Code: CodeValidation, ...}` |
| `schema_test.go` | Add cases asserting returned errors satisfy `IsValidationError` |
| `knowledge.go` | `UpdateProjectStatus` nil-node case returns `ErrNotFound` |
| `context.go` | Three `.Get()` failure sites use `wrapFirestoreNotFound` |
| `gemini/dispatcher.go` | Add `classifyAPIError` and `isSafetyResponse` helpers; update `Dispatch` to use them |
| `gemini/dispatcher_test.go` | New — unit tests for the two new helpers |

---

## Task 1: Define the error taxonomy in `errors.go`

**Files:**
- Modify: `errors.go`
- Create: `errors_test.go`

### Step 1.1: Write the failing tests

Create `errors_test.go`:

```go
package memory

import (
	"errors"
	"fmt"
	"testing"
)

func TestMemoryErrorMessage(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		err     *MemoryError
		wantMsg string
	}{
		{
			name:    "with wrapped error",
			err:     &MemoryError{Code: CodeValidation, Message: "bad field", Err: errors.New("missing")},
			wantMsg: "VALIDATION_FAILED: bad field (missing)",
		},
		{
			name:    "without wrapped error",
			err:     &MemoryError{Code: CodeLLMTimeout, Message: "api timeout"},
			wantMsg: "LLM_TIMEOUT: api timeout",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := tc.err.Error(); got != tc.wantMsg {
				t.Errorf("Error() = %q, want %q", got, tc.wantMsg)
			}
		})
	}
}

func TestMemoryErrorUnwrap(t *testing.T) {
	t.Parallel()
	inner := errors.New("inner")
	memErr := &MemoryError{Code: CodeValidation, Message: "m", Err: inner}
	if !errors.Is(memErr, inner) {
		t.Error("errors.Is should find inner error via Unwrap")
	}
}

func TestSentinelErrors(t *testing.T) {
	t.Parallel()

	notFound := fmt.Errorf("outer: %w", ErrNotFound)
	if !errors.Is(notFound, ErrNotFound) {
		t.Error("ErrNotFound sentinel not detected through wrapping")
	}

	safetyTrip := fmt.Errorf("outer: %w", ErrLLMSafetyTrip)
	if !errors.Is(safetyTrip, ErrLLMSafetyTrip) {
		t.Error("ErrLLMSafetyTrip sentinel not detected through wrapping")
	}
}

func TestInspectionHelpers(t *testing.T) {
	t.Parallel()

	t.Run("IsNotFound true", func(t *testing.T) {
		t.Parallel()
		if !IsNotFound(fmt.Errorf("wrap: %w", ErrNotFound)) {
			t.Error("expected IsNotFound true")
		}
	})
	t.Run("IsNotFound false", func(t *testing.T) {
		t.Parallel()
		if IsNotFound(errors.New("some other error")) {
			t.Error("expected IsNotFound false")
		}
	})
	t.Run("IsSafetyBlock true", func(t *testing.T) {
		t.Parallel()
		if !IsSafetyBlock(fmt.Errorf("wrap: %w", ErrLLMSafetyTrip)) {
			t.Error("expected IsSafetyBlock true")
		}
	})
	t.Run("IsSafetyBlock false", func(t *testing.T) {
		t.Parallel()
		if IsSafetyBlock(errors.New("timeout")) {
			t.Error("expected IsSafetyBlock false for non-safety error")
		}
	})
	t.Run("IsValidationError true", func(t *testing.T) {
		t.Parallel()
		err := &MemoryError{Code: CodeValidation, Message: "bad"}
		if !IsValidationError(err) {
			t.Error("expected IsValidationError true")
		}
	})
	t.Run("IsValidationError false for non-MemoryError", func(t *testing.T) {
		t.Parallel()
		if IsValidationError(errors.New("something")) {
			t.Error("expected IsValidationError false")
		}
	})
	t.Run("IsValidationError false for other code", func(t *testing.T) {
		t.Parallel()
		err := &MemoryError{Code: CodeLLMTimeout, Message: "timeout"}
		if IsValidationError(err) {
			t.Error("expected IsValidationError false for LLM_TIMEOUT code")
		}
	})
	t.Run("IsTransientLLMError true for rate limit", func(t *testing.T) {
		t.Parallel()
		err := &MemoryError{Code: CodeLLMRateLimit, Message: "rate limited"}
		if !IsTransientLLMError(err) {
			t.Error("expected IsTransientLLMError true for rate limit")
		}
	})
	t.Run("IsTransientLLMError true for timeout", func(t *testing.T) {
		t.Parallel()
		err := &MemoryError{Code: CodeLLMTimeout, Message: "timed out"}
		if !IsTransientLLMError(err) {
			t.Error("expected IsTransientLLMError true for timeout")
		}
	})
	t.Run("IsTransientLLMError false for validation", func(t *testing.T) {
		t.Parallel()
		err := &MemoryError{Code: CodeValidation, Message: "bad field"}
		if IsTransientLLMError(err) {
			t.Error("expected IsTransientLLMError false for validation error")
		}
	})
}
```

- [ ] **Step 1.2: Run tests to verify they fail**

```bash
cd /Users/jstrohm/code/memory && go test ./... -run TestMemoryError -v
```
Expected: compile error — `MemoryError`, `CodeValidation`, etc. not defined.

- [ ] **Step 1.3: Implement the error taxonomy in `errors.go`**

Replace the entire `errors.go` with:

```go
package memory

import (
	"errors"
	"fmt"
	"strings"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// --- Sentinel Errors ---
// Use with errors.Is().

var (
	// ErrNotFound is returned when a requested document does not exist in storage.
	ErrNotFound = errors.New("memory: document not found")
	// ErrAlreadyExists is returned when a create operation finds the document already present.
	ErrAlreadyExists = errors.New("memory: document already exists")
	// ErrLLMSafetyTrip is returned when the LLM refused to generate content due to a safety filter.
	ErrLLMSafetyTrip = errors.New("memory: llm safety filter triggered")
)

// --- Error Codes ---

// ErrorCode classifies a MemoryError for programmatic branching.
type ErrorCode string

const (
	// CodeValidation indicates a schema or metadata validation failure.
	CodeValidation ErrorCode = "VALIDATION_FAILED"
	// CodeLLMTimeout indicates an LLM call that exceeded its deadline.
	CodeLLMTimeout ErrorCode = "LLM_TIMEOUT"
	// CodeLLMRateLimit indicates the LLM API rejected the request due to quota exhaustion.
	CodeLLMRateLimit ErrorCode = "LLM_RATE_LIMIT"
)

// --- MemoryError ---

// MemoryError is the standard structured error type for the memory library.
// Use errors.As to extract it; use the boolean helpers below for common checks.
type MemoryError struct {
	Code    ErrorCode
	Message string
	Err     error // underlying cause; surfaced by Unwrap for errors.Is/As chains
}

func (e *MemoryError) Error() string {
	if e.Err != nil {
		return fmt.Sprintf("%s: %s (%v)", e.Code, e.Message, e.Err)
	}
	return fmt.Sprintf("%s: %s", e.Code, e.Message)
}

func (e *MemoryError) Unwrap() error {
	return e.Err
}

// --- Inspection Helpers ---

// IsNotFound returns true if err represents a missing document.
func IsNotFound(err error) bool {
	return errors.Is(err, ErrNotFound)
}

// IsSafetyBlock returns true if the LLM refused to generate content.
func IsSafetyBlock(err error) bool {
	return errors.Is(err, ErrLLMSafetyTrip)
}

// IsValidationError returns true if err is a schema or metadata validation failure.
func IsValidationError(err error) bool {
	var memErr *MemoryError
	if errors.As(err, &memErr) {
		return memErr.Code == CodeValidation
	}
	return false
}

// IsTransientLLMError returns true if the error is a retryable LLM failure
// (timeout or rate limit). Safety blocks are intentional and not retryable.
func IsTransientLLMError(err error) bool {
	var memErr *MemoryError
	if errors.As(err, &memErr) {
		return memErr.Code == CodeLLMTimeout || memErr.Code == CodeLLMRateLimit
	}
	return false
}

// --- Internal Helpers ---

// wrapFirestoreIndexError wraps "query requires an index" errors with a user-facing message.
func wrapFirestoreIndexError(err error) error {
	if err == nil {
		return nil
	}
	if status.Code(err) != codes.FailedPrecondition {
		return err
	}
	if !strings.Contains(err.Error(), "index") {
		return err
	}
	return fmt.Errorf("Firestore query requires a composite index. Add the needed index to firestore.indexes.json and run: firebase deploy --only firestore:indexes — %w", err)
}

// wrapFirestoreNotFound converts a Firestore "not found" gRPC error into ErrNotFound.
// op is the operation name for the wrapping message (e.g. "get context").
// Non-NotFound errors are wrapped normally.
func wrapFirestoreNotFound(op string, err error) error {
	if status.Code(err) == codes.NotFound {
		return fmt.Errorf("%s: %w", op, ErrNotFound)
	}
	return fmt.Errorf("%s: %w", op, err)
}
```

- [ ] **Step 1.4: Run tests to verify they pass**

```bash
cd /Users/jstrohm/code/memory && go test ./... -run "TestMemoryError|TestSentinel|TestInspection" -v
```
Expected: all new tests PASS; full suite still compiles.

- [ ] **Step 1.5: Commit**

```bash
cd /Users/jstrohm/code/memory
git add errors.go errors_test.go
git commit -m "feat(errors): add MemoryError taxonomy, sentinels, and inspection helpers"
```

---

## Task 2: Wrap `ValidateMetadata` return in `MemoryError`

**Files:**
- Modify: `schema.go` (function `ValidateMetadata`, lines 213–222)
- Modify: `schema_test.go` (add assertions to existing `TestValidateMetadata` table)

- [ ] **Step 2.1: Add typed-error assertions to `schema_test.go`**

Append these cases inside the `tests` slice in `TestValidateMetadata` (after the existing last entry):

```go
// Typed error assertions — validate that validation failures are MemoryErrors.
{
    name:      "preference invalid category returns MemoryError",
    nodeType:  "preference",
    meta:      map[string]any{"category": "music"},
    wantErr:   true,
    errSubstr: "VALIDATION_FAILED",
},
{
    name:      "project invalid status returns MemoryError",
    nodeType:  "project",
    meta:      map[string]any{"status": "wip"},
    wantErr:   true,
    errSubstr: "VALIDATION_FAILED",
},
```

Also add a new test function after `TestValidateMetadata`:

```go
func TestValidateMetadataReturnsMemoryError(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		nodeType string
		meta     map[string]any
	}{
		{"preference bad category", "preference", map[string]any{"category": "music"}},
		{"project bad status", "project", map[string]any{"status": "wip"}},
		{"event bad type", "event", map[string]any{"type": "party"}},
		{"place bad category", "place", map[string]any{"category": "museum"}},
		{"user_identity bad category", "user_identity", map[string]any{"category": "hobby"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := ValidateMetadata(tc.nodeType, tc.meta)
			if err == nil {
				t.Fatal("expected error, got nil")
			}
			if !IsValidationError(err) {
				t.Errorf("IsValidationError = false, want true; err = %v", err)
			}
		})
	}
}
```

- [ ] **Step 2.2: Run tests to verify they fail**

```bash
cd /Users/jstrohm/code/memory && go test ./... -run "TestValidateMetadata" -v
```
Expected: new `TestValidateMetadataReturnsMemoryError` cases FAIL — `IsValidationError` returns false because `ValidateMetadata` still returns raw errors. The `errSubstr: "VALIDATION_FAILED"` cases in the table also fail.

- [ ] **Step 2.3: Update `ValidateMetadata` in `schema.go`**

Replace the `ValidateMetadata` function body (lines 213–222):

```go
// ValidateMetadata validates m against the schema for nodeType.
// On failure it returns a *MemoryError with Code: CodeValidation, making
// the error classifiable via IsValidationError.
func ValidateMetadata(nodeType string, m map[string]any) error {
	if m == nil {
		return errors.New("metadata map is nil")
	}
	entry, ok := registry[nodeType]
	if !ok {
		return nil
	}
	if err := entry.validate(m); err != nil {
		return &MemoryError{
			Code:    CodeValidation,
			Message: fmt.Sprintf("invalid metadata for %q", nodeType),
			Err:     err,
		}
	}
	return nil
}
```

- [ ] **Step 2.4: Run tests to verify they pass**

```bash
cd /Users/jstrohm/code/memory && go test ./... -v 2>&1 | tail -20
```
Expected: all tests PASS (no regressions; existing `errSubstr` checks still match because the error message now includes both the code and the original validator message).

- [ ] **Step 2.5: Commit**

```bash
cd /Users/jstrohm/code/memory
git add schema.go schema_test.go
git commit -m "feat(schema): ValidateMetadata returns typed MemoryError on validation failure"
```

---

## Task 3: Return `ErrNotFound` from storage "not found" paths

**Files:**
- Modify: `knowledge.go` (line 636)
- Modify: `context.go` (lines 84, 156, 591)

There are no existing unit tests for these paths because they require a live Firestore. Instead, verify by building and running the existing test suite (which tests adjacent logic).

- [ ] **Step 3.1: Update `knowledge.go` line 636**

The nil-node check in `UpdateProjectStatus` is not a Firestore error; the node was simply absent from the query results. Change it to wrap `ErrNotFound`:

Old:
```go
return fmt.Errorf("knowledge node %q not found", nodeID)
```

New:
```go
return fmt.Errorf("update project status %q: %w", nodeID, ErrNotFound)
```

- [ ] **Step 3.2: Update the three "context not found" sites in `context.go`**

Each of the three `.Get(ctx)` failure sites currently returns a generic message that does not distinguish a missing document from a network error. Apply `wrapFirestoreNotFound` to each:

**Line 84** (`TouchContext`):
```go
// old
return fmt.Errorf("context not found: %w", err)
// new
return wrapFirestoreNotFound("touch context", err)
```

**Line 156** (`TouchContextBatch` — the next `.Get()` call):
```go
// old
return fmt.Errorf("context not found: %w", err)
// new
return wrapFirestoreNotFound("touch context batch", err)
```
Note: `context.go` has a fourth `.Get()` call around line 352 inside `GetContextMetadata` — it does **not** use the "context not found" pattern and must not be changed.

**Line 591** (`SynthesizeContext`):
```go
// old
return fmt.Errorf("context not found: %w", err)
// new
return wrapFirestoreNotFound("synthesize context", err)
```

- [ ] **Step 3.3: Build and run tests**

```bash
cd /Users/jstrohm/code/memory && go build ./... && go test ./... -v 2>&1 | tail -20
```
Expected: clean build, all tests PASS.

- [ ] **Step 3.4: Commit**

```bash
cd /Users/jstrohm/code/memory
git add knowledge.go context.go
git commit -m "feat(storage): not-found paths return ErrNotFound for programmatic inspection"
```

---

## Task 4: Map Gemini API errors to typed errors in the dispatcher

**Files:**
- Modify: `gemini/dispatcher.go`
- Create: `gemini/dispatcher_test.go`

The `genai.Client` is a concrete type with no interface, so we test the two new internal helpers directly rather than the full `Dispatch` call. `Dispatch` is updated to call these helpers.

- [ ] **Step 4.1: Write the failing tests in `gemini/dispatcher_test.go`**

```go
package gemini

import (
	"context"
	"errors"
	"testing"

	"google.golang.org/genai"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/hoyle1974/memory"
)

func TestIsSafetyResponse(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		resp *genai.GenerateContentResponse
		want bool
	}{
		{name: "nil response", resp: nil, want: false},
		{name: "empty candidates", resp: &genai.GenerateContentResponse{}, want: false},
		{
			name: "finish reason STOP",
			resp: &genai.GenerateContentResponse{
				Candidates: []*genai.Candidate{
					{FinishReason: genai.FinishReasonStop},
				},
			},
			want: false,
		},
		{
			name: "finish reason SAFETY",
			resp: &genai.GenerateContentResponse{
				Candidates: []*genai.Candidate{
					{FinishReason: genai.FinishReasonSafety},
				},
			},
			want: true,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := isSafetyResponse(tc.resp); got != tc.want {
				t.Errorf("isSafetyResponse() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestClassifyAPIError(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		err         error
		wantCode    memory.ErrorCode
		wantSentinel error // when non-nil, check errors.Is
	}{
		{
			name:        "context deadline exceeded",
			err:         context.DeadlineExceeded,
			wantCode:    memory.CodeLLMTimeout,
		},
		{
			name:        "grpc deadline exceeded",
			err:         status.Error(codes.DeadlineExceeded, "deadline"),
			wantCode:    memory.CodeLLMTimeout,
		},
		{
			name:        "grpc resource exhausted (rate limit)",
			err:         status.Error(codes.ResourceExhausted, "quota"),
			wantCode:    memory.CodeLLMRateLimit,
		},
		{
			name:        "unknown grpc error passes through",
			err:         status.Error(codes.Internal, "boom"),
			wantCode:    "",  // no MemoryError wrapping expected
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := classifyAPIError(tc.err)
			if tc.wantCode == "" {
				// Should NOT be a MemoryError — just a plain wrapped error.
				var memErr *memory.MemoryError
				if errors.As(got, &memErr) {
					t.Errorf("expected plain error, got MemoryError with code %q", memErr.Code)
				}
				return
			}
			var memErr *memory.MemoryError
			if !errors.As(got, &memErr) {
				t.Fatalf("errors.As(*MemoryError) = false; got: %v", got)
			}
			if memErr.Code != tc.wantCode {
				t.Errorf("Code = %q, want %q", memErr.Code, tc.wantCode)
			}
		})
	}
}
```

- [ ] **Step 4.2: Run tests to verify they fail**

```bash
cd /Users/jstrohm/code/memory && go test ./gemini/... -run "TestIsSafety|TestClassify" -v
```
Expected: compile error — `isSafetyResponse` and `classifyAPIError` not defined.

- [ ] **Step 4.3: Add helpers and update `Dispatch` in `gemini/dispatcher.go`**

Add imports (add `"context"`, `"errors"` to existing imports, and `"google.golang.org/grpc/codes"` / `"google.golang.org/grpc/status"`):

```go
import (
	"context"
	"errors"
	"fmt"
	"strings"

	"google.golang.org/genai"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/hoyle1974/memory"
)
```

Add the two helpers after the `dispatcher` struct definition (before `NewDispatcher`):

```go
// isSafetyResponse returns true if the Gemini API blocked generation for safety reasons.
// This happens when any candidate has FinishReason == FinishReasonSafety.
func isSafetyResponse(resp *genai.GenerateContentResponse) bool {
	if resp == nil {
		return false
	}
	for _, c := range resp.Candidates {
		if c.FinishReason == genai.FinishReasonSafety {
			return true
		}
	}
	return false
}

// classifyAPIError maps known Gemini/gRPC transient errors to typed MemoryErrors.
// Unrecognised errors are wrapped with the "gemini dispatch" prefix and passed through.
func classifyAPIError(err error) error {
	if errors.Is(err, context.DeadlineExceeded) || status.Code(err) == codes.DeadlineExceeded {
		return &memory.MemoryError{Code: memory.CodeLLMTimeout, Message: "gemini API timeout", Err: err}
	}
	if status.Code(err) == codes.ResourceExhausted {
		return &memory.MemoryError{Code: memory.CodeLLMRateLimit, Message: "gemini rate limit exceeded", Err: err}
	}
	return fmt.Errorf("gemini dispatch: %w", err)
}
```

Update the `Dispatch` method to use the helpers:

```go
func (d *dispatcher) Dispatch(ctx context.Context, req memory.LLMRequest) (string, error) {
	maxOut := req.MaxTokens
	if maxOut <= 0 {
		maxOut = defaultMaxOutputTokens
	}
	if maxOut < minMaxOutputTokens {
		maxOut = minMaxOutputTokens
	}

	cfg := &genai.GenerateContentConfig{
		MaxOutputTokens: int32(maxOut),
		SafetySettings: []*genai.SafetySetting{
			{Category: genai.HarmCategoryHarassment, Threshold: genai.HarmBlockThresholdBlockNone},
			{Category: genai.HarmCategoryHateSpeech, Threshold: genai.HarmBlockThresholdBlockNone},
			{Category: genai.HarmCategorySexuallyExplicit, Threshold: genai.HarmBlockThresholdBlockNone},
			{Category: genai.HarmCategoryDangerousContent, Threshold: genai.HarmBlockThresholdBlockNone},
		},
	}
	if req.JSONMode {
		cfg.ResponseMIMEType = "application/json"
	}
	if req.SystemPrompt != "" {
		cfg.SystemInstruction = &genai.Content{
			Role:  genai.RoleUser,
			Parts: []*genai.Part{{Text: req.SystemPrompt}},
		}
	}

	contents := []*genai.Content{{Role: genai.RoleUser, Parts: []*genai.Part{{Text: req.UserPrompt}}}}
	resp, err := d.client.Models.GenerateContent(ctx, d.model, contents, cfg)
	if err != nil {
		return "", classifyAPIError(err)
	}
	if isSafetyResponse(resp) {
		return "", fmt.Errorf("gemini dispatch: %w", memory.ErrLLMSafetyTrip)
	}
	text := strings.TrimSpace(resp.Text())
	if text == "" {
		return "", fmt.Errorf("gemini returned empty response")
	}
	return text, nil
}
```

- [ ] **Step 4.4: Run tests to verify they pass**

```bash
cd /Users/jstrohm/code/memory && go test ./gemini/... -run "TestIsSafety|TestClassify" -v
```
Expected: both test functions PASS.

- [ ] **Step 4.5: Build the full module**

```bash
cd /Users/jstrohm/code/memory && go build ./...
```
Expected: clean build, no errors.

- [ ] **Step 4.6: Run the full test suite**

```bash
cd /Users/jstrohm/code/memory && go test ./... -v 2>&1 | tail -30
```
Expected: all tests PASS.

- [ ] **Step 4.7: Commit**

```bash
cd /Users/jstrohm/code/memory
git add gemini/dispatcher.go gemini/dispatcher_test.go
git commit -m "feat(gemini): map safety blocks and transient API errors to typed MemoryErrors"
```

---

## Post-Implementation: Usage pattern in jot

> **IMPORTANT — DO NOT ADD THIS CODE TO THE MEMORY LIBRARY.** The snippet below belongs in the `jot` application, not here. It is documentation only, showing how a caller benefits from the typed errors. No file in `memory` should be created or modified as part of this section.

After the memory library is updated, the jot Dreamer loop can branch like this (no new imports needed beyond `"github.com/hoyle1974/memory"`):

```go
result, err := app.Memory.SomeOperation(ctx, ...)
if err != nil {
    switch {
    case memory.IsSafetyBlock(err):
        s.log.Warn("LLM safety block, skipping task", "err", err)
        continue
    case memory.IsTransientLLMError(err):
        s.log.Warn("transient LLM error, will retry", "err", err)
        return err // bubble up for caller to retry
    case memory.IsNotFound(err):
        s.log.Info("resource missing, skipping", "err", err)
        continue
    case memory.IsValidationError(err):
        s.log.Error("validation failure, operator action required", "err", err)
        notifyUser(ctx, err)
    default:
        return err
    }
}
```
