package web

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/inercia/mitto/internal/conversation"
	"github.com/inercia/mitto/internal/session"
)

// handleCallbackTrigger handles POST /api/callback/{token}
// This is a PUBLIC endpoint (no auth required) that triggers a periodic prompt delivery.
func (s *Server) handleCallbackTrigger(w http.ResponseWriter, r *http.Request) {
	// 1. Only accept POST requests
	if r.Method != http.MethodPost {
		writeErrorJSON(w, http.StatusMethodNotAllowed, "method_not_allowed", "Only POST is supported")
		return
	}

	// 2. Extract token from path
	path := strings.TrimPrefix(r.URL.Path, s.apiPrefix+"/api/callback/")
	// Handle trailing slashes
	token := strings.TrimSuffix(path, "/")
	if token == "" {
		writeErrorJSON(w, http.StatusBadRequest, "missing_token", "Callback token is required")
		return
	}

	// 3. Validate token format
	if !session.ValidateCallbackToken(token) {
		writeErrorJSON(w, http.StatusBadRequest, "invalid_token", "Invalid callback token format")
		return
	}

	// 4. Lookup session ID from index
	sessionID, ok := s.callbackIndex.Lookup(token)
	if !ok {
		writeErrorJSON(w, http.StatusNotFound, "not_found", "Callback not found")
		return
	}

	// 5. Check rate limit
	if !s.callbackRateLimiter.Allow(token) {
		writeErrorJSON(w, http.StatusTooManyRequests, "rate_limited", "Too many requests")
		return
	}

	// 6. Parse optional metadata from request body (best-effort)
	var req conversation.CallbackTriggerRequest
	if r.Body != nil {
		bodyBytes, _ := io.ReadAll(r.Body)
		if len(bodyBytes) > 0 {
			_ = json.Unmarshal(bodyBytes, &req) // Ignore errors - metadata is optional
		}
	}

	// 7. Verify callback still exists in store (index could be stale)
	store := s.Store()
	if store == nil {
		writeErrorJSON(w, http.StatusInternalServerError, "internal", "Session store not available")
		return
	}

	cs := store.Callback(sessionID)
	if _, err := cs.Get(); err != nil {
		if err == session.ErrCallbackNotFound {
			// Clean up stale index entry
			s.callbackIndex.Remove(token)
			writeErrorJSON(w, http.StatusNotFound, "not_found", "Callback not found")
			return
		}
		writeErrorJSON(w, http.StatusInternalServerError, "internal", "Failed to get callback config")
		return
	}

	// 8. Check periodic config exists and is enabled
	periodicStore := store.Periodic(sessionID)
	periodic, err := periodicStore.Get()
	if err != nil {
		if err == session.ErrPeriodicNotFound {
			writeErrorJSON(w, http.StatusGone, "periodic_disabled", "No periodic prompt configured")
			return
		}
		writeErrorJSON(w, http.StatusInternalServerError, "internal", "Failed to get periodic config")
		return
	}

	if !periodic.Enabled {
		writeErrorJSON(w, http.StatusGone, "periodic_disabled", "Periodic is disabled")
		return
	}

	// 9. Trigger the periodic prompt via the runner
	if s.periodicRunner == nil {
		writeErrorJSON(w, http.StatusInternalServerError, "internal", "Periodic runner not available")
		return
	}

	if err := s.periodicRunner.TriggerNow(sessionID, true); err != nil {
		switch err {
		case ErrSessionBusy:
			writeErrorJSON(w, http.StatusConflict, "session_busy", "Session is currently processing")
		case ErrPeriodicNotEnabled:
			writeErrorJSON(w, http.StatusGone, "periodic_disabled", "Periodic is not enabled")
		case session.ErrPeriodicNotFound:
			writeErrorJSON(w, http.StatusGone, "periodic_disabled", "No periodic prompt configured")
		default:
			if s.logger != nil {
				s.logger.Error("Failed to trigger callback", "error", err, "session_id", sessionID)
			}
			writeErrorJSON(w, http.StatusInternalServerError, "internal", "Failed to trigger prompt")
		}
		return
	}

	// 10. Log successful trigger
	if s.logger != nil {
		tokenPrefix := token
		if len(tokenPrefix) > 10 {
			tokenPrefix = tokenPrefix[:10] + "..."
		}
		s.logger.Info("Callback triggered",
			"token_prefix", tokenPrefix,
			"session_id", sessionID,
			"client_ip", r.RemoteAddr,
			"metadata", req.Metadata)
	}

	// 11. Return success
	writeJSONOK(w, map[string]string{"status": "triggered"})
}

// handleSessionCallback handles callback token management operations:
// GET    /api/sessions/{id}/callback - Get callback status
// POST   /api/sessions/{id}/callback - Generate/rotate token
// DELETE /api/sessions/{id}/callback - Revoke callback
func (s *Server) handleSessionCallback(w http.ResponseWriter, r *http.Request, sessionID string) {
	store := s.Store()
	if store == nil {
		http.Error(w, "Session store not available", http.StatusInternalServerError)
		return
	}

	// Verify session exists
	if _, err := store.GetMetadata(sessionID); err != nil {
		if err == session.ErrSessionNotFound {
			http.Error(w, "Session not found", http.StatusNotFound)
			return
		}
		http.Error(w, "Failed to get session", http.StatusInternalServerError)
		return
	}

	cs := store.Callback(sessionID)

	switch r.Method {
	case http.MethodGet:
		s.handleGetCallback(w, cs)
	case http.MethodPost:
		s.handleGenerateCallback(w, cs, sessionID)
	case http.MethodDelete:
		s.handleRevokeCallback(w, cs, sessionID)
	default:
		methodNotAllowed(w)
	}
}

// handleGetCallback handles GET /api/sessions/{id}/callback
func (s *Server) handleGetCallback(w http.ResponseWriter, cs *session.CallbackStore) {
	cb, err := cs.Get()
	if err != nil {
		if err == session.ErrCallbackNotFound {
			http.Error(w, "No callback configured", http.StatusNotFound)
			return
		}
		if s.logger != nil {
			s.logger.Error("Failed to get callback", "error", err)
		}
		http.Error(w, "Failed to get callback", http.StatusInternalServerError)
		return
	}

	writeJSONOK(w, map[string]interface{}{
		"callback_url": s.buildCallbackURL(cb.Token),
		"created_at":   cb.CreatedAt,
	})
}

// handleGenerateCallback handles POST /api/sessions/{id}/callback
func (s *Server) handleGenerateCallback(w http.ResponseWriter, cs *session.CallbackStore, sessionID string) {
	// Get old token if it exists (for index cleanup)
	oldToken := ""
	if oldCB, err := cs.Get(); err == nil {
		oldToken = oldCB.Token
	}

	// Generate new token
	token, err := cs.GenerateToken()
	if err != nil {
		if s.logger != nil {
			s.logger.Error("Failed to generate callback token", "error", err, "session_id", sessionID)
		}
		http.Error(w, "Failed to generate callback token", http.StatusInternalServerError)
		return
	}

	// Update index: remove old token, register new one
	if oldToken != "" && s.callbackIndex != nil {
		s.callbackIndex.Remove(oldToken)
	}
	if s.callbackIndex != nil {
		s.callbackIndex.Register(token, sessionID)
	}

	writeJSONOK(w, map[string]interface{}{
		"callback_token":   token,
		"callback_url":     s.buildCallbackURL(token),
		"callback_enabled": true,
	})
}

// handleRevokeCallback handles DELETE /api/sessions/{id}/callback
func (s *Server) handleRevokeCallback(w http.ResponseWriter, cs *session.CallbackStore, sessionID string) {
	// Get token before revoking (for cleanup)
	var token string
	if cb, err := cs.Get(); err == nil {
		token = cb.Token
	}

	// Revoke in store
	if err := cs.Revoke(); err != nil {
		if err == session.ErrCallbackNotFound {
			http.Error(w, "No callback configured", http.StatusNotFound)
			return
		}
		if s.logger != nil {
			s.logger.Error("Failed to revoke callback", "error", err, "session_id", sessionID)
		}
		http.Error(w, "Failed to revoke callback", http.StatusInternalServerError)
		return
	}

	// Clean up index and rate limiter
	if token != "" {
		if s.callbackIndex != nil {
			s.callbackIndex.Remove(token)
		}
		if s.callbackRateLimiter != nil {
			s.callbackRateLimiter.Remove(token)
		}
	}

	writeNoContent(w)
}

// buildCallbackURL constructs the full callback URL for a token.
// Tries to use ExternalAddress from config first, falls back to localhost.
func (s *Server) buildCallbackURL(token string) string {
	// Try external address from config first
	if s.config.MittoConfig != nil {
		if addr := s.config.MittoConfig.Web.Hooks.ExternalAddress; addr != "" {
			// ExternalAddress is the base URL (e.g., "https://mitto.inerciatech.com")
			// without the API prefix. We must append apiPrefix + the callback path.
			return strings.TrimRight(addr, "/") + s.apiPrefix + "/api/callback/" + token
		}
	}

	// Fall back to localhost with external port if configured
	port := s.GetExternalPort()
	if port == 0 {
		return fmt.Sprintf("http://127.0.0.1%s/api/callback/%s", s.apiPrefix, token)
	}
	return fmt.Sprintf("http://127.0.0.1:%d%s/api/callback/%s", port, s.apiPrefix, token)
}

// buildCallbackIndex scans all sessions at startup and builds the in-memory token index.
// This is called once during server initialization.
func (s *Server) buildCallbackIndex() {
	store := s.Store()
	if store == nil {
		return
	}

	sessions, err := store.List()
	if err != nil {
		if s.logger != nil {
			s.logger.Error("Failed to list sessions for callback index", "error", err)
		}
		return
	}

	for _, meta := range sessions {
		cs := store.Callback(meta.SessionID)
		if cb, err := cs.Get(); err == nil {
			s.callbackIndex.Register(cb.Token, meta.SessionID)
		}
	}

	if s.logger != nil {
		s.logger.Info("Callback index built", "tokens", s.callbackIndex.Count())
	}
}
