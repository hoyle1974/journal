package gemini

import (
	"context"
	"errors"
	"testing"

	"google.golang.org/genai"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/jackstrohm/jot/memory"
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

	ctxDeadline := context.DeadlineExceeded
	grpcDeadline := status.Error(codes.DeadlineExceeded, "deadline")
	grpcRateLimit := status.Error(codes.ResourceExhausted, "quota")
	grpcInternal := status.Error(codes.Internal, "boom")

	tests := []struct {
		name         string
		err          error
		wantCode     memory.ErrorCode // empty means expect plain error (no MemoryError)
		unwrapTarget error            // non-nil: verify errors.Is(result, unwrapTarget) is true
	}{
		{
			name:         "context deadline exceeded",
			err:          ctxDeadline,
			wantCode:     memory.CodeLLMTimeout,
			unwrapTarget: ctxDeadline,
		},
		{
			name:         "grpc deadline exceeded",
			err:          grpcDeadline,
			wantCode:     memory.CodeLLMTimeout,
			unwrapTarget: grpcDeadline,
		},
		{
			name:         "grpc resource exhausted (rate limit)",
			err:          grpcRateLimit,
			wantCode:     memory.CodeLLMRateLimit,
			unwrapTarget: grpcRateLimit,
		},
		{
			name:     "unknown grpc error passes through as plain error",
			err:      grpcInternal,
			wantCode: "", // no MemoryError wrapping expected
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := classifyAPIError(tc.err)
			if tc.wantCode == "" {
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
			if tc.unwrapTarget != nil && !errors.Is(got, tc.unwrapTarget) {
				t.Errorf("errors.Is(result, original err) = false; Unwrap chain broken for %v", tc.unwrapTarget)
			}
		})
	}
}
