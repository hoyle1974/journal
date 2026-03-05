package api

import (
	"net/http"
	"strings"
	"sync"
	"time"

	"golang.org/x/time/rate"
)

// Per-IP rate limits (requests per minute) for expensive endpoints.
var rateLimitConfig = map[string]int{
	"/query":              30,
	"/plan":               10,
	"/sync":               5,
	"/dream":              2,
	"/janitor":            1,
	"/rollup":             2,
	"/log":                60,
	"/entries":            60,
	"/webhook":            20,
	"/sms":                30,
	"/decay-contexts":      5,
	"/backfill-embeddings": 2,
	"/pending-questions":  60,
}

const defaultRatePerMin = 120

var (
	rateLimiters        sync.Map
	rateLimitCleanupOnce sync.Once
)

type rateLimiterEntry struct {
	limiter *rate.Limiter
	lastUse time.Time
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

// CheckRateLimit returns true if the request is allowed, false if rate limited.
func CheckRateLimit(r *http.Request, path string) bool {
	limitPath := RateLimitPath(path)
	perMin, ok := rateLimitConfig[limitPath]
	if !ok {
		perMin = defaultRatePerMin
	}
	ip := GetClientIP(r)
	key := ip + ":" + limitPath
	val, loaded := rateLimiters.Load(key)
	if loaded {
		entry := val.(*rateLimiterEntry)
		entry.lastUse = time.Now()
		return entry.limiter.Allow()
	}
	interval := time.Minute / time.Duration(perMin)
	lim := rate.NewLimiter(rate.Every(interval), perMin)
	rateLimiters.Store(key, &rateLimiterEntry{limiter: lim, lastUse: time.Now()})
	return lim.Allow()
}

// StartRateLimitCleanup periodically removes stale limiters. Call once from init.
func StartRateLimitCleanup() {
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
