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
	t.Run("IsValidationError true when wrapped in outer error", func(t *testing.T) {
		t.Parallel()
		inner := &MemoryError{Code: CodeValidation, Message: "bad"}
		wrapped := fmt.Errorf("outer: %w", inner)
		if !IsValidationError(wrapped) {
			t.Error("expected IsValidationError true through wrapping chain")
		}
	})
	t.Run("IsTransientLLMError true when wrapped in outer error", func(t *testing.T) {
		t.Parallel()
		inner := &MemoryError{Code: CodeLLMRateLimit, Message: "rate limited"}
		wrapped := fmt.Errorf("outer: %w", inner)
		if !IsTransientLLMError(wrapped) {
			t.Error("expected IsTransientLLMError true through wrapping chain")
		}
	})
}
