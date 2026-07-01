package handlers

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/inercia/mitto/internal/session"
)

// HandleSessionCallback handles callback token management operations:
// GET    /api/sessions/{id}/callback - Get callback status
// POST   /api/sessions/{id}/callback - Generate/rotate token
// DELETE /api/sessions/{id}/callback - Revoke callback
func (h *Handlers) HandleSessionCallback(w http.ResponseWriter, r *http.Request, sessionID string) {
	store := h.deps.Store
	if store == nil {
		writeErrorJSON(w, http.StatusInternalServerError, "", "Session store not available")
		return
	}

	// Verify session exists
	if _, err := store.GetMetadata(sessionID); err != nil {
		if err == session.ErrSessionNotFound {
			writeErrorJSON(w, http.StatusNotFound, "", "Session not found")
			return
		}
		writeErrorJSON(w, http.StatusInternalServerError, "", "Failed to get session")
		return
	}

	cs := store.Callback(sessionID)

	switch r.Method {
	case http.MethodGet:
		h.handleGetCallback(w, cs)
	case http.MethodPost:
		h.handleGenerateCallback(w, cs, sessionID)
	case http.MethodDelete:
		h.handleRevokeCallback(w, cs, sessionID)
	default:
		methodNotAllowed(w)
	}
}

// handleGetCallback handles GET /api/sessions/{id}/callback
func (h *Handlers) handleGetCallback(w http.ResponseWriter, cs *session.CallbackStore) {
	cb, err := cs.Get()
	if err != nil {
		if err == session.ErrCallbackNotFound {
			writeErrorJSON(w, http.StatusNotFound, "", "No callback configured")
			return
		}
		if h.deps.Logger != nil {
			h.deps.Logger.Error("Failed to get callback", "error", err)
		}
		writeErrorJSON(w, http.StatusInternalServerError, "", "Failed to get callback")
		return
	}

	writeJSONOK(w, map[string]interface{}{
		"callback_url": h.buildCallbackURL(cb.Token),
		"created_at":   cb.CreatedAt,
	})
}

// handleGenerateCallback handles POST /api/sessions/{id}/callback
func (h *Handlers) handleGenerateCallback(w http.ResponseWriter, cs *session.CallbackStore, sessionID string) {
	// Get old token if it exists (for index cleanup)
	oldToken := ""
	if oldCB, err := cs.Get(); err == nil {
		oldToken = oldCB.Token
	}

	// Generate new token
	token, err := cs.GenerateToken()
	if err != nil {
		if h.deps.Logger != nil {
			h.deps.Logger.Error("Failed to generate callback token", "error", err, "session_id", sessionID)
		}
		writeErrorJSON(w, http.StatusInternalServerError, "", "Failed to generate callback token")
		return
	}

	// Update index: remove old token, register new one
	if oldToken != "" && h.deps.CallbackIndex != nil {
		h.deps.CallbackIndex.Remove(oldToken)
	}
	if h.deps.CallbackIndex != nil {
		h.deps.CallbackIndex.Register(token, sessionID)
	}

	writeJSONOK(w, map[string]interface{}{
		"callback_token":   token,
		"callback_url":     h.buildCallbackURL(token),
		"callback_enabled": true,
	})
}

// handleRevokeCallback handles DELETE /api/sessions/{id}/callback
func (h *Handlers) handleRevokeCallback(w http.ResponseWriter, cs *session.CallbackStore, sessionID string) {
	// Get token before revoking (for cleanup)
	var token string
	if cb, err := cs.Get(); err == nil {
		token = cb.Token
	}

	// Revoke in store
	if err := cs.Revoke(); err != nil {
		if err == session.ErrCallbackNotFound {
			writeErrorJSON(w, http.StatusNotFound, "", "No callback configured")
			return
		}
		if h.deps.Logger != nil {
			h.deps.Logger.Error("Failed to revoke callback", "error", err, "session_id", sessionID)
		}
		writeErrorJSON(w, http.StatusInternalServerError, "", "Failed to revoke callback")
		return
	}

	// Clean up index and rate limiter
	if token != "" {
		if h.deps.CallbackIndex != nil {
			h.deps.CallbackIndex.Remove(token)
		}
		if h.deps.CallbackRateLimiter != nil {
			h.deps.CallbackRateLimiter.Remove(token)
		}
	}

	writeNoContent(w)
}

// buildCallbackURL constructs the full callback URL for a token.
// Tries to use ExternalAddress from config first, falls back to localhost.
func (h *Handlers) buildCallbackURL(token string) string {
	// Try external address from config first
	if h.deps.MittoConfig != nil {
		if addr := h.deps.MittoConfig.Web.Hooks.ExternalAddress; addr != "" {
			// ExternalAddress is the base URL (e.g., "https://mitto.inerciatech.com")
			// without the API prefix. We must append apiPrefix + the callback path.
			return strings.TrimRight(addr, "/") + h.deps.APIPrefix + "/api/callback/" + token
		}
	}

	// Fall back to localhost with external port if configured
	port := 0
	if h.deps.GetExternalPort != nil {
		port = h.deps.GetExternalPort()
	}
	if port == 0 {
		return fmt.Sprintf("http://127.0.0.1%s/api/callback/%s", h.deps.APIPrefix, token)
	}
	return fmt.Sprintf("http://127.0.0.1:%d%s/api/callback/%s", port, h.deps.APIPrefix, token)
}
