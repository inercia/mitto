package handlers

import (
	"net/http"
	"strconv"

	"github.com/inercia/mitto/internal/session"
)

// HandleGetSession handles GET /api/sessions/{id} and GET /api/sessions/{id}/events
func (h *Handlers) HandleGetSession(w http.ResponseWriter, r *http.Request, sessionID string, isEventsRequest bool) {
	// Use the server's session store (owned by the server, not closed by this handler)
	store := h.deps.Store
	if store == nil {
		writeErrorJSON(w, http.StatusInternalServerError, "", "Session store not available")
		return
	}

	if isEventsRequest {
		// Parse query parameters for pagination
		query := r.URL.Query()
		var limit int
		var beforeSeq int64
		reverseOrder := query.Get("order") == "desc"

		if limitStr := query.Get("limit"); limitStr != "" {
			if l, err := strconv.Atoi(limitStr); err == nil && l > 0 {
				limit = l
			}
		}
		if beforeStr := query.Get("before"); beforeStr != "" {
			if b, err := strconv.ParseInt(beforeStr, 10, 64); err == nil && b > 0 {
				beforeSeq = b
			}
		}

		var events []session.Event
		var err error
		if limit > 0 {
			if reverseOrder {
				// Use reverse order read (newest first)
				events, err = store.ReadEventsLastReverse(sessionID, limit, beforeSeq)
			} else {
				// Use paginated read (oldest first)
				events, err = store.ReadEventsLast(sessionID, limit, beforeSeq)
			}
		} else {
			// Read all events (backward compatible)
			events, err = store.ReadEvents(sessionID)
			// If reverse order requested, reverse the result
			if reverseOrder && err == nil {
				for i, j := 0, len(events)-1; i < j; i, j = i+1, j-1 {
					events[i], events[j] = events[j], events[i]
				}
			}
		}

		if err != nil {
			if err == session.ErrSessionNotFound {
				writeErrorJSON(w, http.StatusNotFound, "", "Session not found")
				return
			}
			if h.deps.Logger != nil {
				h.deps.Logger.Error("Failed to read session events", "error", err, "session_id", sessionID)
			}
			writeErrorJSON(w, http.StatusInternalServerError, "", "Failed to read session events")
			return
		}

		writeJSONOK(w, events)
	} else {
		// Return session metadata
		meta, err := store.GetMetadata(sessionID)
		if err != nil {
			if err == session.ErrSessionNotFound {
				writeErrorJSON(w, http.StatusNotFound, "", "Session not found")
				return
			}
			if h.deps.Logger != nil {
				h.deps.Logger.Error("Failed to get session metadata", "error", err, "session_id", sessionID)
			}
			writeErrorJSON(w, http.StatusInternalServerError, "", "Failed to get session metadata")
			return
		}

		writeJSONOK(w, meta)
	}
}
