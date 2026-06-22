package handlers

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"

	"github.com/inercia/mitto/internal/conversation"
	"github.com/inercia/mitto/internal/session"
)

// HandleCallbackTrigger handles POST /api/callback/{token}
// This is a PUBLIC endpoint (no auth required) that triggers a periodic prompt delivery.
func (h *Handlers) HandleCallbackTrigger(w http.ResponseWriter, r *http.Request) {
	// 1. Only accept POST requests
	if r.Method != http.MethodPost {
		writeErrorJSON(w, http.StatusMethodNotAllowed, "method_not_allowed", "Only POST is supported")
		return
	}

	// 2. Extract token from path
	path := strings.TrimPrefix(r.URL.Path, h.deps.APIPrefix+"/api/callback/")
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
	if h.deps.CallbackIndex == nil {
		writeErrorJSON(w, http.StatusInternalServerError, "internal", "Callback index not available")
		return
	}
	sessionID, ok := h.deps.CallbackIndex.Lookup(token)
	if !ok {
		writeErrorJSON(w, http.StatusNotFound, "not_found", "Callback not found")
		return
	}

	// 5. Check rate limit
	if h.deps.CallbackRateLimiter != nil && !h.deps.CallbackRateLimiter.Allow(token) {
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
	store := h.deps.Store
	if store == nil {
		writeErrorJSON(w, http.StatusInternalServerError, "internal", "Session store not available")
		return
	}

	cs := store.Callback(sessionID)
	if _, err := cs.Get(); err != nil {
		if err == session.ErrCallbackNotFound {
			// Clean up stale index entry
			h.deps.CallbackIndex.Remove(token)
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
	if h.deps.TriggerPeriodicNow == nil {
		writeErrorJSON(w, http.StatusInternalServerError, "internal", "Periodic runner not available")
		return
	}

	if err := h.deps.TriggerPeriodicNow(sessionID, true); err != nil {
		switch err {
		case h.deps.ErrSessionBusy:
			writeErrorJSON(w, http.StatusConflict, "session_busy", "Session is currently processing")
		case h.deps.ErrPeriodicNotEnabled:
			writeErrorJSON(w, http.StatusGone, "periodic_disabled", "Periodic is not enabled")
		case session.ErrPeriodicNotFound:
			writeErrorJSON(w, http.StatusGone, "periodic_disabled", "No periodic prompt configured")
		default:
			if h.deps.Logger != nil {
				h.deps.Logger.Error("Failed to trigger callback", "error", err, "session_id", sessionID)
			}
			writeErrorJSON(w, http.StatusInternalServerError, "internal", "Failed to trigger prompt")
		}
		return
	}

	// 10. Log successful trigger
	if h.deps.Logger != nil {
		tokenPrefix := token
		if len(tokenPrefix) > 10 {
			tokenPrefix = tokenPrefix[:10] + "..."
		}
		h.deps.Logger.Info("Callback triggered",
			"token_prefix", tokenPrefix,
			"session_id", sessionID,
			"client_ip", r.RemoteAddr,
			"metadata", req.Metadata)
	}

	// 11. Return success
	writeJSONOK(w, map[string]string{"status": "triggered"})
}
