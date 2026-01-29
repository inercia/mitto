package web

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/inercia/mitto/internal/config"
	"github.com/inercia/mitto/internal/logging"
)

const (
	// sessionCookieName is the name of the authentication cookie
	sessionCookieName = "mitto_session"

	// sessionTokenLength is the length of the session token in bytes
	sessionTokenLength = 32

	// sessionDuration is how long a session is valid
	sessionDuration = 24 * time.Hour

	// maxSessionsPerUser is the maximum number of concurrent sessions per user
	maxSessionsPerUser = 10

	// sessionCleanupInterval is how often to clean up expired sessions
	sessionCleanupInterval = 5 * time.Minute
)

// Credential validation errors
var (
	// ErrNoCredentials is returned when no credentials are configured.
	ErrNoCredentials = errors.New("no credentials configured for external access")
	// ErrEmptyUsername is returned when the username is empty.
	ErrEmptyUsername = errors.New("username cannot be empty for external access")
	// ErrEmptyPassword is returned when the password is empty.
	ErrEmptyPassword = errors.New("password cannot be empty for external access")
)

// AuthSession represents an authenticated user session.
type AuthSession struct {
	Token     string
	Username  string
	CreatedAt time.Time
	ExpiresAt time.Time
}

// AuthManager manages user authentication.
type AuthManager struct {
	config      *config.WebAuth
	sessions    map[string]*AuthSession
	mu          sync.RWMutex
	allowedNets []*net.IPNet     // Parsed CIDR networks from Allow list
	allowedIPs  []net.IP         // Individual IPs from Allow list
	rateLimiter *AuthRateLimiter // Rate limiter for failed login attempts

	// Cleanup goroutine control
	stopCleanup chan struct{}
	cleanupDone chan struct{}
}

// NewAuthManager creates a new auth manager.
func NewAuthManager(authConfig *config.WebAuth) *AuthManager {
	am := &AuthManager{
		config:      authConfig,
		sessions:    make(map[string]*AuthSession),
		rateLimiter: NewAuthRateLimiter(),
		stopCleanup: make(chan struct{}),
		cleanupDone: make(chan struct{}),
	}

	// Parse the allow list
	if authConfig != nil && authConfig.Allow != nil {
		for _, entry := range authConfig.Allow.IPs {
			entry = strings.TrimSpace(entry)
			if entry == "" {
				continue
			}

			// Try parsing as CIDR first
			if strings.Contains(entry, "/") {
				_, network, err := net.ParseCIDR(entry)
				if err == nil {
					am.allowedNets = append(am.allowedNets, network)
					continue
				}
			}

			// Try parsing as individual IP
			ip := net.ParseIP(entry)
			if ip != nil {
				am.allowedIPs = append(am.allowedIPs, ip)
			}
		}
	}

	// Start session cleanup goroutine
	go am.cleanupLoop()

	return am
}

// Close releases resources held by the auth manager.
func (a *AuthManager) Close() {
	// Stop cleanup goroutine
	close(a.stopCleanup)
	<-a.cleanupDone

	if a.rateLimiter != nil {
		a.rateLimiter.Close()
	}
}

// UpdateConfig updates the auth configuration dynamically.
// This allows changing auth settings without restarting the server.
func (a *AuthManager) UpdateConfig(authConfig *config.WebAuth) {
	a.mu.Lock()
	defer a.mu.Unlock()

	a.config = authConfig

	// Re-parse the allow list
	a.allowedNets = nil
	a.allowedIPs = nil

	if authConfig != nil && authConfig.Allow != nil {
		for _, entry := range authConfig.Allow.IPs {
			entry = strings.TrimSpace(entry)
			if entry == "" {
				continue
			}

			// Try parsing as CIDR first
			if strings.Contains(entry, "/") {
				_, network, err := net.ParseCIDR(entry)
				if err == nil {
					a.allowedNets = append(a.allowedNets, network)
					continue
				}
			}

			// Try parsing as individual IP
			ip := net.ParseIP(entry)
			if ip != nil {
				a.allowedIPs = append(a.allowedIPs, ip)
			}
		}
	}
}

// cleanupLoop periodically removes expired sessions.
func (a *AuthManager) cleanupLoop() {
	defer close(a.cleanupDone)

	ticker := time.NewTicker(sessionCleanupInterval)
	defer ticker.Stop()

	for {
		select {
		case <-a.stopCleanup:
			return
		case <-ticker.C:
			a.cleanupExpiredSessions()
		}
	}
}

// cleanupExpiredSessions removes all expired sessions.
func (a *AuthManager) cleanupExpiredSessions() {
	a.mu.Lock()
	defer a.mu.Unlock()

	now := time.Now()
	for token, session := range a.sessions {
		if now.After(session.ExpiresAt) {
			delete(a.sessions, token)
		}
	}
}

// IsEnabled returns true if authentication is enabled and credentials are configured.
// Returns false if username or password is empty, as external access must NEVER
// proceed with empty credentials.
func (a *AuthManager) IsEnabled() bool {
	return a.config != nil &&
		a.config.Simple != nil &&
		a.config.Simple.Username != "" &&
		a.config.Simple.Password != ""
}

// HasValidCredentials returns true if both username and password are non-empty.
// This is used to validate that credentials are properly configured before
// enabling external access.
func (a *AuthManager) HasValidCredentials() bool {
	if a.config == nil || a.config.Simple == nil {
		return false
	}
	return a.config.Simple.Username != "" && a.config.Simple.Password != ""
}

// CredentialError returns an error describing why credentials are invalid,
// or nil if credentials are valid.
func (a *AuthManager) CredentialError() error {
	if a.config == nil || a.config.Simple == nil {
		return ErrNoCredentials
	}
	if a.config.Simple.Username == "" {
		return ErrEmptyUsername
	}
	if a.config.Simple.Password == "" {
		return ErrEmptyPassword
	}
	return nil
}

// IsIPAllowed checks if the given IP address is in the allow list.
func (a *AuthManager) IsIPAllowed(ipStr string) bool {
	if len(a.allowedNets) == 0 && len(a.allowedIPs) == 0 {
		return false
	}

	// Parse the IP address (handle IPv6 zone identifiers and port)
	ip := parseClientIP(ipStr)
	if ip == nil {
		return false
	}

	// Check against individual IPs
	for _, allowedIP := range a.allowedIPs {
		if allowedIP.Equal(ip) {
			return true
		}
	}

	// Check against CIDR networks
	for _, network := range a.allowedNets {
		if network.Contains(ip) {
			return true
		}
	}

	return false
}

// parseClientIP extracts and parses an IP address from various formats.
// Handles: "192.168.1.1", "192.168.1.1:8080", "[::1]:8080", "::1"
func parseClientIP(addr string) net.IP {
	// Try to parse as host:port first
	host, _, err := net.SplitHostPort(addr)
	if err == nil {
		return net.ParseIP(host)
	}

	// Try parsing directly as IP
	return net.ParseIP(addr)
}

// getClientIP extracts the client IP from the request.
// It checks X-Forwarded-For and X-Real-IP headers first (for reverse proxies),
// then falls back to RemoteAddr.
func getClientIP(r *http.Request) string {
	// Check X-Forwarded-For header (may contain multiple IPs)
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		// Take the first IP (original client)
		parts := strings.Split(xff, ",")
		if len(parts) > 0 {
			return strings.TrimSpace(parts[0])
		}
	}

	// Check X-Real-IP header
	if xri := r.Header.Get("X-Real-IP"); xri != "" {
		return strings.TrimSpace(xri)
	}

	// Fall back to RemoteAddr
	return r.RemoteAddr
}

// generateToken creates a cryptographically secure random token.
func generateToken() (string, error) {
	b := make([]byte, sessionTokenLength)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// ValidateCredentials checks if the username and password match the config.
func (a *AuthManager) ValidateCredentials(username, password string) bool {
	if a.config == nil || a.config.Simple == nil {
		return false
	}
	// Use constant-time comparison to prevent timing attacks
	usernameMatch := subtle.ConstantTimeCompare([]byte(username), []byte(a.config.Simple.Username)) == 1
	passwordMatch := subtle.ConstantTimeCompare([]byte(password), []byte(a.config.Simple.Password)) == 1
	return usernameMatch && passwordMatch
}

// CreateSession creates a new authenticated session for the user.
// If the user has too many sessions, the oldest ones are evicted.
func (a *AuthManager) CreateSession(username string) (*AuthSession, error) {
	token, err := generateToken()
	if err != nil {
		return nil, err
	}

	now := time.Now()
	session := &AuthSession{
		Token:     token,
		Username:  username,
		CreatedAt: now,
		ExpiresAt: now.Add(sessionDuration),
	}

	a.mu.Lock()
	defer a.mu.Unlock()

	// Enforce session limit per user - evict oldest sessions if needed
	a.evictOldestSessionsForUser(username, maxSessionsPerUser-1)

	a.sessions[token] = session

	return session, nil
}

// evictOldestSessionsForUser removes the oldest sessions for a user until
// the count is at or below maxCount. Must be called with a.mu held.
func (a *AuthManager) evictOldestSessionsForUser(username string, maxCount int) {
	// Collect all sessions for this user
	var userSessions []*AuthSession
	for _, session := range a.sessions {
		if session.Username == username {
			userSessions = append(userSessions, session)
		}
	}

	// If under limit, nothing to do
	if len(userSessions) <= maxCount {
		return
	}

	// Sort by creation time (oldest first)
	for i := 0; i < len(userSessions)-1; i++ {
		for j := i + 1; j < len(userSessions); j++ {
			if userSessions[j].CreatedAt.Before(userSessions[i].CreatedAt) {
				userSessions[i], userSessions[j] = userSessions[j], userSessions[i]
			}
		}
	}

	// Remove oldest sessions until we're at the limit
	toRemove := len(userSessions) - maxCount
	for i := 0; i < toRemove; i++ {
		delete(a.sessions, userSessions[i].Token)
	}
}

// ValidateSession checks if a session token is valid.
func (a *AuthManager) ValidateSession(token string) (*AuthSession, bool) {
	a.mu.RLock()
	session, exists := a.sessions[token]
	a.mu.RUnlock()

	if !exists {
		return nil, false
	}

	if time.Now().After(session.ExpiresAt) {
		a.InvalidateSession(token)
		return nil, false
	}

	return session, true
}

// InvalidateSession removes a session.
func (a *AuthManager) InvalidateSession(token string) {
	a.mu.Lock()
	delete(a.sessions, token)
	a.mu.Unlock()
}

// SetSessionCookie sets the authentication cookie on the response.
func (a *AuthManager) SetSessionCookie(w http.ResponseWriter, session *AuthSession) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    session.Token,
		Path:     "/",
		HttpOnly: true,
		Secure:   true, // Always set Secure; browsers will handle appropriately
		SameSite: http.SameSiteStrictMode,
		Expires:  session.ExpiresAt,
	})
}

// ClearSessionCookie removes the authentication cookie.
func (a *AuthManager) ClearSessionCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteStrictMode,
		MaxAge:   -1,
	})
}

// GetSessionFromRequest retrieves the session from the request cookie.
func (a *AuthManager) GetSessionFromRequest(r *http.Request) (*AuthSession, bool) {
	cookie, err := r.Cookie(sessionCookieName)
	if err != nil {
		return nil, false
	}
	return a.ValidateSession(cookie.Value)
}

// publicPaths are paths that don't require authentication.
var publicPaths = map[string]bool{
	"/_auth.html":    true,
	"/api/login":     true,
	"/styles.css":    true,
	"/styles-v2.css": true,
	"/favicon.ico":   true,
}

// isPublicPath checks if a path is public (no auth required).
func isPublicPath(path string) bool {
	return publicPaths[path]
}

// isLoopbackIP checks if the given IP address is a loopback address.
// This includes 127.0.0.0/8 for IPv4 and ::1 for IPv6.
func isLoopbackIP(ipStr string) bool {
	ip := parseClientIP(ipStr)
	if ip == nil {
		return false
	}
	return ip.IsLoopback()
}

// AuthMiddleware returns a middleware that enforces authentication.
func (a *AuthManager) AuthMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// If auth is not enabled, pass through
		if !a.IsEnabled() {
			next.ServeHTTP(w, r)
			return
		}

		// Always allow localhost/loopback connections without authentication.
		// The localhost listener (127.0.0.1) is only accessible from the local machine,
		// so it's inherently trusted. Auth is only required for external connections.
		clientIP := getClientIP(r)
		if isLoopbackIP(clientIP) {
			next.ServeHTTP(w, r)
			return
		}

		// Check if client IP is in the allow list (bypass auth)
		if a.IsIPAllowed(clientIP) {
			next.ServeHTTP(w, r)
			return
		}

		// Allow public paths without authentication
		if isPublicPath(r.URL.Path) {
			next.ServeHTTP(w, r)
			return
		}

		// Check for valid session
		_, valid := a.GetSessionFromRequest(r)
		if !valid {
			// For API requests, return 401
			if len(r.URL.Path) >= 4 && r.URL.Path[:4] == "/api" {
				http.Error(w, "Unauthorized", http.StatusUnauthorized)
				return
			}
			// For WebSocket requests, return 401
			if r.URL.Path == "/ws" || (len(r.URL.Path) > 4 && r.URL.Path[len(r.URL.Path)-3:] == "/ws") {
				http.Error(w, "Unauthorized", http.StatusUnauthorized)
				return
			}
			// For page requests, redirect to login
			http.Redirect(w, r, "/_auth.html", http.StatusFound)
			return
		}

		next.ServeHTTP(w, r)
	})
}

// LoginRequest represents a login request.
type LoginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

// LoginResponse represents a login response.
type LoginResponse struct {
	Success       bool   `json:"success"`
	Error         string `json:"error,omitempty"`
	RetryAfterSec int    `json:"retry_after_sec,omitempty"` // Seconds until retry allowed (when rate limited)
}

// HandleLogin handles POST /api/login.
func (a *AuthManager) HandleLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Get client IP for rate limiting
	clientIP := getClientIP(r)
	parsedIP := parseClientIP(clientIP)
	ipKey := ""
	if parsedIP != nil {
		ipKey = parsedIP.String()
	} else {
		ipKey = clientIP // Fallback to raw string
	}

	logger := logging.Auth()

	// Check if IP is rate limited BEFORE processing the request
	// This prevents timing attacks and reduces server load
	if blocked, remaining := a.rateLimiter.IsBlocked(ipKey); blocked {
		retryAfter := int(remaining.Seconds()) + 1 // Round up
		logger.Warn("Login attempt from rate-limited IP",
			"client_ip", ipKey,
			"retry_after_sec", retryAfter,
		)
		w.Header().Set("Retry-After", fmt.Sprintf("%d", retryAfter))
		sendJSON(w, http.StatusTooManyRequests, LoginResponse{
			Success:       false,
			Error:         "Too many failed attempts. Please try again later.",
			RetryAfterSec: retryAfter,
		})
		return
	}

	var req LoginRequest
	if err := decodeJSON(r, &req); err != nil {
		sendJSON(w, http.StatusBadRequest, LoginResponse{
			Success: false,
			Error:   "Invalid request body",
		})
		return
	}

	if req.Username == "" || req.Password == "" {
		sendJSON(w, http.StatusBadRequest, LoginResponse{
			Success: false,
			Error:   "Username and password are required",
		})
		return
	}

	if !a.ValidateCredentials(req.Username, req.Password) {
		// Record the failure and check if now blocked
		nowBlocked, lockoutDuration := a.rateLimiter.RecordFailure(ipKey)

		if nowBlocked {
			retryAfter := int(lockoutDuration.Seconds()) + 1
			logger.Warn("Login failed - IP now rate limited",
				"client_ip", ipKey,
				"username", req.Username,
				"lockout_duration_sec", int(lockoutDuration.Seconds()),
			)
			w.Header().Set("Retry-After", fmt.Sprintf("%d", retryAfter))
			sendJSON(w, http.StatusTooManyRequests, LoginResponse{
				Success:       false,
				Error:         "Too many failed attempts. Please try again later.",
				RetryAfterSec: retryAfter,
			})
		} else {
			remaining := a.rateLimiter.RemainingAttempts(ipKey)
			logger.Warn("Login failed - invalid credentials",
				"client_ip", ipKey,
				"username", req.Username,
				"remaining_attempts", remaining,
			)
			// Use a generic error message to prevent username enumeration
			sendJSON(w, http.StatusUnauthorized, LoginResponse{
				Success: false,
				Error:   "Invalid username or password",
			})
		}
		return
	}

	// Successful login - clear any failure records for this IP
	a.rateLimiter.RecordSuccess(ipKey)

	session, err := a.CreateSession(req.Username)
	if err != nil {
		logger.Error("Failed to create session",
			"client_ip", ipKey,
			"username", req.Username,
			"error", err,
		)
		sendJSON(w, http.StatusInternalServerError, LoginResponse{
			Success: false,
			Error:   "Failed to create session",
		})
		return
	}

	logger.Info("Login successful",
		"client_ip", ipKey,
		"username", req.Username,
	)

	a.SetSessionCookie(w, session)
	sendJSON(w, http.StatusOK, LoginResponse{Success: true})
}

// HandleLogout handles POST /api/logout.
func (a *AuthManager) HandleLogout(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Get current session and invalidate it
	if session, valid := a.GetSessionFromRequest(r); valid {
		a.InvalidateSession(session.Token)
	}

	a.ClearSessionCookie(w)
	sendJSON(w, http.StatusOK, map[string]bool{"success": true})
}

// Helper functions for JSON encoding/decoding
func decodeJSON(r *http.Request, v interface{}) error {
	return json.NewDecoder(r.Body).Decode(v)
}

func sendJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}
