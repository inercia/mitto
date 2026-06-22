package handlers

import (
	"net/http"
	"time"
)

// HandleHealthCheck handles the health check endpoint for load balancer integration.
// M3: This endpoint returns server health status and basic metrics.
// It is intentionally NOT behind authentication to allow health checks from load balancers.
func (h *Handlers) HandleHealthCheck(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Check if server is shutting down
	if h.deps.IsShutdown != nil && h.deps.IsShutdown() {
		writeJSON(w, http.StatusServiceUnavailable, map[string]interface{}{
			"status":  "unhealthy",
			"reason":  "server_shutting_down",
			"message": "Server is shutting down",
		})
		return
	}

	// Gather health metrics
	response := map[string]interface{}{
		"status":    "healthy",
		"timestamp": time.Now().UTC().Format(time.RFC3339),
	}

	// Add session metrics if session manager is available
	if h.deps.SessionManager != nil {
		activeSessions := h.deps.SessionManager.ActiveSessionCount()
		promptingSessions := h.deps.SessionManager.PromptingSessionCount()
		response["sessions"] = map[string]interface{}{
			"active":    activeSessions,
			"prompting": promptingSessions,
		}
	}

	// Add store metrics if available
	if h.deps.Store != nil {
		storedCount, err := h.deps.Store.CountSessions()
		if err == nil {
			response["stored_sessions"] = storedCount
		}
	}

	writeJSONOK(w, response)
}

// HandleAuthInfo returns information about configured authentication methods.
// This is a public endpoint (no auth required) so the login page can adapt its UI.
func (h *Handlers) HandleAuthInfo(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w)
		return
	}

	info := map[string]bool{
		"simple":     false,
		"cloudflare": false,
	}

	if h.deps.AuthInfo != nil {
		simple, cloudflare := h.deps.AuthInfo()
		info["simple"] = simple
		info["cloudflare"] = cloudflare
	}

	writeJSONOK(w, info)
}
