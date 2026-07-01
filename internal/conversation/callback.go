package conversation

import (
	"sync"
	"time"

	"golang.org/x/time/rate"
)

// CallbackIndex maintains an in-memory map of callback tokens to session IDs.
// This provides fast lookup without filesystem access on every callback request.
type CallbackIndex struct {
	mu     sync.RWMutex
	tokens map[string]string // token → sessionID
}

// NewCallbackIndex creates a new callback index.
func NewCallbackIndex() *CallbackIndex {
	return &CallbackIndex{
		tokens: make(map[string]string),
	}
}

// Lookup finds a session ID by callback token.
// Returns sessionID and true if found, empty string and false if not.
func (ci *CallbackIndex) Lookup(token string) (sessionID string, ok bool) {
	ci.mu.RLock()
	defer ci.mu.RUnlock()
	sessionID, ok = ci.tokens[token]
	return
}

// Register adds a token→sessionID mapping to the index.
func (ci *CallbackIndex) Register(token, sessionID string) {
	ci.mu.Lock()
	defer ci.mu.Unlock()
	ci.tokens[token] = sessionID
}

// Remove deletes a token from the index.
func (ci *CallbackIndex) Remove(token string) {
	ci.mu.Lock()
	defer ci.mu.Unlock()
	delete(ci.tokens, token)
}

// RemoveBySessionID removes all tokens for a given session ID.
// This is used during session deletion to clean up the index.
func (ci *CallbackIndex) RemoveBySessionID(sessionID string) {
	ci.mu.Lock()
	defer ci.mu.Unlock()
	for token, sid := range ci.tokens {
		if sid == sessionID {
			delete(ci.tokens, token)
		}
	}
}

// Count returns the total number of registered tokens.
func (ci *CallbackIndex) Count() int {
	ci.mu.RLock()
	defer ci.mu.RUnlock()
	return len(ci.tokens)
}

// CallbackRateLimiter provides per-token rate limiting for callback requests.
// This prevents abuse of the callback endpoint by a single token.
type CallbackRateLimiter struct {
	mu       sync.Mutex
	limiters map[string]*rate.Limiter
}

const (
	// callbackBurst is the burst size (allows up to 3 requests in quick succession).
	callbackBurst = 3
)

var (
	// callbackRateLimit is the per-token rate limit (1 request per 10 seconds).
	callbackRateLimit = rate.Every(10 * time.Second)
)

// NewCallbackRateLimiter creates a new callback rate limiter.
func NewCallbackRateLimiter() *CallbackRateLimiter {
	return &CallbackRateLimiter{
		limiters: make(map[string]*rate.Limiter),
	}
}

// Allow checks if a callback request is allowed for the given token.
// Returns true if allowed, false if rate limited.
func (crl *CallbackRateLimiter) Allow(token string) bool {
	crl.mu.Lock()
	defer crl.mu.Unlock()

	limiter, ok := crl.limiters[token]
	if !ok {
		limiter = rate.NewLimiter(callbackRateLimit, callbackBurst)
		crl.limiters[token] = limiter
	}

	return limiter.Allow()
}

// Remove deletes the rate limiter for a token.
// This is used during token revocation to clean up the limiter map.
func (crl *CallbackRateLimiter) Remove(token string) {
	crl.mu.Lock()
	defer crl.mu.Unlock()
	delete(crl.limiters, token)
}

// CallbackTriggerRequest is the optional request body for callback trigger requests.
// Clients can include arbitrary metadata that will be logged.
type CallbackTriggerRequest struct {
	Metadata map[string]interface{} `json:"metadata,omitempty"`
}
