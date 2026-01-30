package web

import (
	"net/http"
	"sync"
	"time"

	"golang.org/x/time/rate"
)

// RateLimitConfig holds configuration for the general rate limiter.
type RateLimitConfig struct {
	// RequestsPerSecond is the rate limit for requests per second per IP
	RequestsPerSecond float64
	// BurstSize is the maximum burst size allowed
	BurstSize int
	// CleanupInterval is how often to clean up old entries
	CleanupInterval time.Duration
	// EntryTTL is how long to keep entries after last access
	EntryTTL time.Duration
}

// DefaultRateLimitConfig returns sensible defaults for rate limiting.
func DefaultRateLimitConfig() RateLimitConfig {
	return RateLimitConfig{
		RequestsPerSecond: 20,               // 20 requests per second
		BurstSize:         50,               // Allow bursts up to 50 (for page loads with many API calls)
		CleanupInterval:   5 * time.Minute,  // Clean up every 5 minutes
		EntryTTL:          10 * time.Minute, // Keep entries for 10 minutes
	}
}

// rateLimitEntry tracks rate limiting state for a single IP.
type rateLimitEntry struct {
	limiter    *rate.Limiter
	lastAccess time.Time
}

// GeneralRateLimiter provides rate limiting for API endpoints.
// It is safe for concurrent use.
type GeneralRateLimiter struct {
	mu       sync.RWMutex
	limiters map[string]*rateLimitEntry
	config   RateLimitConfig

	stopCleanup chan struct{}
	cleanupDone chan struct{}
}

// NewGeneralRateLimiter creates a new rate limiter with the given configuration.
func NewGeneralRateLimiter(config RateLimitConfig) *GeneralRateLimiter {
	rl := &GeneralRateLimiter{
		limiters:    make(map[string]*rateLimitEntry),
		config:      config,
		stopCleanup: make(chan struct{}),
		cleanupDone: make(chan struct{}),
	}

	go rl.cleanupLoop()

	return rl
}

// Close stops the cleanup goroutine and releases resources.
func (rl *GeneralRateLimiter) Close() {
	close(rl.stopCleanup)
	<-rl.cleanupDone
}

// Allow checks if a request from the given IP should be allowed.
func (rl *GeneralRateLimiter) Allow(ip string) bool {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	entry, exists := rl.limiters[ip]
	if !exists {
		entry = &rateLimitEntry{
			limiter: rate.NewLimiter(rate.Limit(rl.config.RequestsPerSecond), rl.config.BurstSize),
		}
		rl.limiters[ip] = entry
	}
	entry.lastAccess = time.Now()

	return entry.limiter.Allow()
}

// Middleware returns an HTTP middleware that enforces rate limiting.
func (rl *GeneralRateLimiter) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Skip rate limiting for static files - they are cacheable and browsers
		// fetch many of them simultaneously during page load. Rate limiting
		// should focus on API endpoints where abuse is more likely.
		if rl.isStaticPath(r.URL.Path) {
			next.ServeHTTP(w, r)
			return
		}

		// Use getClientIPWithProxyCheck to only trust X-Forwarded-For headers
		// from configured trusted proxies. This prevents IP spoofing attacks
		// where attackers set fake X-Forwarded-For headers to bypass rate limiting.
		clientIP := getClientIPWithProxyCheck(r)

		if !rl.Allow(clientIP) {
			w.Header().Set("Retry-After", "1")
			http.Error(w, "Too Many Requests", http.StatusTooManyRequests)
			return
		}

		next.ServeHTTP(w, r)
	})
}

// isStaticPath returns true if the path is for a static file that should
// be exempt from rate limiting.
func (rl *GeneralRateLimiter) isStaticPath(path string) bool {
	// Static file extensions that should be exempt from rate limiting
	staticExtensions := []string{
		".js", ".css", ".html", ".htm",
		".png", ".jpg", ".jpeg", ".gif", ".svg", ".ico", ".webp",
		".woff", ".woff2", ".ttf", ".eot",
		".json", ".map",
	}

	for _, ext := range staticExtensions {
		if len(path) > len(ext) && path[len(path)-len(ext):] == ext {
			return true
		}
	}

	return false
}

// cleanupLoop periodically removes stale entries.
func (rl *GeneralRateLimiter) cleanupLoop() {
	defer close(rl.cleanupDone)

	ticker := time.NewTicker(rl.config.CleanupInterval)
	defer ticker.Stop()

	for {
		select {
		case <-rl.stopCleanup:
			return
		case <-ticker.C:
			rl.cleanup()
		}
	}
}

// cleanup removes entries that haven't been accessed recently.
func (rl *GeneralRateLimiter) cleanup() {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	cutoff := time.Now().Add(-rl.config.EntryTTL)
	for ip, entry := range rl.limiters {
		if entry.lastAccess.Before(cutoff) {
			delete(rl.limiters, ip)
		}
	}
}

// Stats returns current statistics for monitoring.
func (rl *GeneralRateLimiter) Stats() (totalEntries int) {
	rl.mu.RLock()
	defer rl.mu.RUnlock()
	return len(rl.limiters)
}
