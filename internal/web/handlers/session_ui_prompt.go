package handlers

import (
	"encoding/json"
	"net/http"
)

// uiPromptAcknowledgeRequest is the body for POST /api/sessions/{id}/ui-prompt/acknowledge.
type uiPromptAcknowledgeRequest struct {
	RequestID string `json:"request_id"`
}

// HandleSessionUIPromptAcknowledge handles POST /api/sessions/{id}/ui-prompt/acknowledge.
// Records that the user has dismissed the sidebar "?" indicator for the given
// active UI prompt, so the dismissal survives page reloads and syncs across
// every connected browser. No-op (200 OK, no broadcast) when the session is
// not currently waiting, when request_id does not match the active prompt,
// or when it was already acknowledged.
func (h *Handlers) HandleSessionUIPromptAcknowledge(w http.ResponseWriter, r *http.Request, sessionID string) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w)
		return
	}
	if h.deps.SessionManager == nil {
		writeErrorJSON(w, http.StatusInternalServerError, "", "Session manager not available")
		return
	}

	var req uiPromptAcknowledgeRequest
	if r.Body != nil {
		_ = json.NewDecoder(r.Body).Decode(&req)
	}
	if req.RequestID == "" {
		writeErrorJSON(w, http.StatusBadRequest, "", "request_id is required")
		return
	}

	if h.deps.SessionManager.AcknowledgeUIPrompt(sessionID, req.RequestID) {
		h.deps.SessionManager.BroadcastUIPromptAck(sessionID, req.RequestID)
	}

	writeJSONOK(w, map[string]interface{}{
		"acked_request_id": req.RequestID,
	})
}
