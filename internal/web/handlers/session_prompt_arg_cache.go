package handlers

import (
	"net/http"

	"github.com/inercia/mitto/internal/session"
)

// promptArgCacheResponse is the JSON response for the prompt-arg-cache endpoint.
// It reports which parameter names are currently cached (fresh) for a given prompt.
// Values are NEVER included — names only.
type promptArgCacheResponse struct {
	Prompt string   `json:"prompt"`
	Cached []string `json:"cached"`
}

// HandlePromptArgCache handles:
//
//	GET /api/sessions/{id}/prompt-arg-cache?prompt=<promptName>
//
// Returns the non-expired cached parameter names for the prompt in the session.
// 400 if the `prompt` query parameter is missing.
// 404 if the session does not exist in the store.
// 200 with cached:[] (empty array, never null) when the session exists but has
// no running BackgroundSession (e.g. archived/suspended).
func (h *Handlers) HandlePromptArgCache(w http.ResponseWriter, r *http.Request, sessionID string) {
	promptName := r.URL.Query().Get("prompt")
	if promptName == "" {
		writeErrorJSON(w, http.StatusBadRequest, "", "prompt query parameter is required")
		return
	}

	store := h.deps.Store
	if store == nil {
		writeErrorJSON(w, http.StatusInternalServerError, "", "Session store not available")
		return
	}

	if _, err := store.GetMetadata(sessionID); err != nil {
		if err == session.ErrSessionNotFound {
			writeErrorJSON(w, http.StatusNotFound, "", "Session not found")
			return
		}
		writeErrorJSON(w, http.StatusInternalServerError, "", "Failed to get session")
		return
	}

	// Must be empty array, not nil — ACP validates this
	cached := []string{}
	if h.deps.SessionManager != nil {
		if bs := h.deps.SessionManager.GetSession(sessionID); bs != nil {
			if names := bs.FreshCachedArgNames(promptName); names != nil {
				cached = names
			}
		}
	}

	writeJSONOK(w, promptArgCacheResponse{Prompt: promptName, Cached: cached})
}
