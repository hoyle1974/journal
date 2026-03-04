package jot

import (
	"net/http"
	"strings"
	"sync"
	"time"

	"golang.org/x/time/rate"
)

// Per-IP rate limits (requests per minute) for expensive endpoints.
var rateLimitConfig = map[string]int{
	"/query":       30,
	"/plan":        10,
	"/sync":        5,
	"/dream":       2,
	"/janitor":     1,
	"/rollup":      2,
	"/log":         60,
	"/entries":     60,
	"/webhook":     20,
	"/sms":         30,
	"/decay-contexts":    5,
	"/backfill-embeddings": 2,
	"/pending-questions": 60,
}

// defaultRate is used for unlisted paths (health, metrics, etc.)
const defaultRatePerMin = 120

var (
	rateLimiters   sync.Map // map[string]*rateLimiterEntry
	rateLimitCleanupOnce sync.Once
)

type rateLimiterEntry struct {
	limiter *rate.Limiter
	lastUse time.Time
}

// getClientIP extracts the client IP from the request.
// Cloud Run / load balancers set X-Forwarded-For: client, proxy1, proxy2
func getClientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		// First element is the client IP
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

// getLimiter returns or creates a rate limiter for the given key.
func getLimiter(key string, perMin int) *rate.Limiter {
	// rate.Limiter uses tokens: rate.Every(1/min) with burst = perMin
	// So we get perMin requests per minute with burst of perMin
	interval := time.Minute / time.Duration(perMin)
	lim := rate.NewLimiter(rate.Every(interval), perMin)

	// Store for next time (we always create new - see cleanup below)
	// Actually we want to reuse limiters per IP+path. Let me fix.
	// The key should be IP+path so each endpoint has its own limit per IP.
	val, _ := rateLimiters.LoadOrStore(key, &rateLimiterEntry{
		limiter: lim,
		lastUse: time.Now(),
	})
	entry := val.(*rateLimiterEntry)
	entry.lastUse = time.Now()
	return entry.limiter
}

// rateLimitPath returns the path key for rate limit config (handles /entries/uuid etc.)
func rateLimitPath(path string) string {
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

// checkRateLimit returns true if the request is allowed, false if rate limited.
// If false, the caller should respond with 429.
func checkRateLimit(r *http.Request, path string) bool {
	limitPath := rateLimitPath(path)
	perMin, ok := rateLimitConfig[limitPath]
	if !ok {
		perMin = defaultRatePerMin
	}

	ip := getClientIP(r)
	key := ip + ":" + limitPath

	// Get or create limiter - we need to sync on the key to avoid races
	// sync.Map LoadOrStore is atomic, but we need to create limiter with correct rate
	val, loaded := rateLimiters.Load(key)
	if loaded {
		entry := val.(*rateLimiterEntry)
		entry.lastUse = time.Now()
		return entry.limiter.Allow()
	}

	interval := time.Minute / time.Duration(perMin)
	lim := rate.NewLimiter(rate.Every(interval), perMin)
	rateLimiters.Store(key, &rateLimiterEntry{limiter: lim, lastUse: time.Now()})

	// Allow first request
	return lim.Allow()
}

// startRateLimitCleanup periodically removes stale limiters to prevent memory leak.
func startRateLimitCleanup() {
	rateLimitCleanupOnce.Do(func() {
		go func() {
			ticker := time.NewTicker(15 * time.Minute)
			defer ticker.Stop()
			for range ticker.C {
				now := time.Now()
				rateLimiters.Range(func(key, value interface{}) bool {
					entry := value.(*rateLimiterEntry)
					if now.Sub(entry.lastUse) > 30*time.Minute {
						rateLimiters.Delete(key)
					}
					return true
				})
			}
		}()
	})
}
