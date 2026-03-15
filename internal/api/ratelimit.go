package api

import (
	"net/http"
	"strings"
	"time"

	"github.com/ulule/limiter/v3"
	"github.com/ulule/limiter/v3/drivers/store/memory"
)

var limiterStore = memory.NewStore()

// RateLimitMiddleware creates a chi middleware for a specific requests-per-minute limit.
func RateLimitMiddleware(reqsPerMin int) func(http.Handler) http.Handler {
	rate := limiter.Rate{
		Period: 1 * time.Minute,
		Limit:  int64(reqsPerMin),
	}
	instance := limiter.New(limiterStore, rate)

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ip := GetClientIP(r)
			context, err := instance.Get(r.Context(), ip)
			if err != nil {
				http.Error(w, "Internal Server Error", http.StatusInternalServerError)
				return
			}
			if context.Reached {
				WriteJSON(w, http.StatusTooManyRequests, map[string]string{"error": "Rate limit exceeded. Please try again later."})
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// GetClientIP extracts the client IP from the request (for use by router and handlers).
func GetClientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		if idx := strings.Index(xff, ","); idx > 0 {
			return strings.TrimSpace(xff[:idx])
		}
		return strings.TrimSpace(xff)
	}
	if xri := r.Header.Get("X-Real-IP"); xri != "" {
		return strings.TrimSpace(xri)
	}
	return r.RemoteAddr
}

// RateLimitPath returns the path key for rate limit config (handles /entries/uuid etc.). Exported for tests.
func RateLimitPath(path string) string {
	if path == "" || path == "/" {
		return "/health"
	}
	if strings.HasPrefix(path, "/entries") {
		return "/entries"
	}
	if strings.HasPrefix(path, "/pending-questions") {
		return "/pending-questions"
	}
	return path
}
