package memory

import (
	"errors"
	"fmt"
	"strings"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

var (
	// ErrNotFound is returned when a requested document does not exist in storage.
	ErrNotFound = errors.New("memory: document not found")
	// ErrLLMSafetyTrip is returned when the LLM refused to generate content due to a safety filter.
	ErrLLMSafetyTrip = errors.New("memory: llm safety filter triggered")
)

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

// MemoryError is the standard structured error type for the memory library.
// Use errors.As to extract it; use the boolean helpers below for common checks.
type MemoryError struct {
	Code    ErrorCode
	Message string
	Err     error
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
	if err == nil {
		return nil
	}
	if status.Code(err) == codes.NotFound {
		return fmt.Errorf("%s: %w", op, ErrNotFound)
	}
	return fmt.Errorf("%s: %w", op, err)
}
