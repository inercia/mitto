package handlers

import "net/http"

// HandleSessionFlush handles POST /api/sessions/{id}/flush.
// It clears the agent's conversation context by sending the configured
// agent-native context-flush command (e.g. "/clear") through the normal prompt
// path. The command is configured per ACP server (acp_servers[].context_flush_command).
func (h *Handlers) HandleSessionFlush(w http.ResponseWriter, r *http.Request, sessionID string) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w)
		return
	}

	if h.deps.SessionManager == nil {
		writeErrorJSON(w, http.StatusInternalServerError, "", "Session manager not available")
		return
	}

	bs := h.deps.SessionManager.GetSession(sessionID)
	if bs == nil {
		writeErrorJSON(w, http.StatusNotFound, "", "Session not found or not running")
		return
	}

	cmd := bs.ContextFlushCommand()
	if cmd == "" {
		writeErrorJSON(w, http.StatusBadRequest, "", "Context flush is not configured for this server")
		return
	}

	// Reject while a turn is in flight so the flush command does not collide with
	// an active prompt; the client can retry once the agent is idle.
	if bs.IsPrompting() {
		writeErrorJSON(w, http.StatusConflict, "", "Session is currently processing a prompt")
		return
	}

	if err := bs.FlushContext(); err != nil {
		if h.deps.Logger != nil {
			h.deps.Logger.Error("Failed to flush context", "error", err, "session_id", sessionID)
		}
		writeErrorJSON(w, http.StatusInternalServerError, "", "Failed to flush context")
		return
	}

	writeJSONOK(w, map[string]interface{}{
		"status":  "flushing",
		"command": cmd,
	})
}
