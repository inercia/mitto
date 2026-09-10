package handlers

import "net/http"

// HandleSessionRetitle handles POST /api/sessions/{id}/retitle.
// It forces async title regeneration using extended conversation context
// (the last few user prompts plus the most recent agent response), bypassing
// the normal SessionNeedsTitle/NameExplicit gates — this is the explicit
// user-facing "Auto-rename" context-menu action (mitto-yv2). Works even when
// the conversation already has a title, including one set by an explicit
// rename. Unlike /flush, this is not rejected while a turn is in flight:
// title generation runs on the workspace's auxiliary session and already
// load-sheds on ErrProcessBusy.
func (h *Handlers) HandleSessionRetitle(w http.ResponseWriter, r *http.Request, sessionID string) {
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

	bs.ForceRegenerateTitle()

	writeJSONOK(w, map[string]interface{}{
		"status": "retitling",
	})
}
