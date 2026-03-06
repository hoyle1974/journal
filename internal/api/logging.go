package api

import (
	"context"
	"strings"

	"github.com/jackstrohm/jot/pkg/infra"
)

// LogHandlerRequest logs HTTP request details for a handler. Call at handler entry with method, path, and any request-specific attrs (e.g. query params, body fields). Avoid logging secrets or huge payloads.
func LogHandlerRequest(ctx context.Context, method, path string, attrs ...any) {
	args := []any{"event", "http_request", "method", method, "path", path}
	args = append(args, attrs...)
	infra.LoggerFrom(ctx).Info("handler request", args...)
}

// LogHandlerResponse logs HTTP response details for a handler. Call before WriteJSON with status and any response summary attrs (e.g. success, uuid, count, error).
func LogHandlerResponse(ctx context.Context, method, path string, statusCode int, attrs ...any) {
	args := []any{"event", "http_response", "method", method, "path", path, "status", statusCode}
	args = append(args, attrs...)
	if statusCode >= 500 {
		infra.LoggerFrom(ctx).Error("handler response", args...)
	} else if statusCode >= 400 {
		infra.LoggerFrom(ctx).Warn("handler response", args...)
	} else {
		infra.LoggerFrom(ctx).Info("handler response", args...)
	}
}

// pathForLog returns the path used in router (trimmed, no trailing slash) for consistent logging.
func pathForLog(path string) string {
	return strings.TrimSuffix(path, "/")
}
