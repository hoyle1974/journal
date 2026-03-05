package jot

import (
	"net/http"

	"github.com/jackstrohm/jot/internal/api"
)

// getClientIP extracts the client IP from the request (delegates to api).
func getClientIP(r *http.Request) string {
	return api.GetClientIP(r)
}

// checkRateLimit returns true if the request is allowed, false if rate limited (delegates to api).
func checkRateLimit(r *http.Request, path string) bool {
	return api.CheckRateLimit(r, path)
}

// startRateLimitCleanup starts the rate limit cleanup goroutine (delegates to api).
func startRateLimitCleanup() {
	api.StartRateLimitCleanup()
}
