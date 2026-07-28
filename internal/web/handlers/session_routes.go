package handlers

import "net/http"

// This file contains the thin route wrappers for session-scoped endpoints.
// Each method extracts the {id} path wildcard via sessionIDFromPath and
// delegates to the corresponding HandleSession* method. Registering these
// directly on *Handlers keeps the route table (see internal/web/routes.go)
// free of *Server thunks and consolidates the session-ID validation.

// HandleSessionsRoute dispatches GET/POST /api/sessions.
func (h *Handlers) HandleSessionsRoute(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		h.HandleListSessions(w, r)
	case http.MethodPost:
		h.HandleCreateSession(w, r)
	default:
		methodNotAllowed(w)
	}
}

// HandleSessionGetRoute handles GET /api/sessions/{id}.
func (h *Handlers) HandleSessionGetRoute(w http.ResponseWriter, r *http.Request) {
	if id, ok := sessionIDFromPath(w, r); ok {
		h.HandleGetSession(w, r, id, false)
	}
}

// HandleSessionEventsRoute handles GET /api/sessions/{id}/events.
func (h *Handlers) HandleSessionEventsRoute(w http.ResponseWriter, r *http.Request) {
	if id, ok := sessionIDFromPath(w, r); ok {
		h.HandleGetSession(w, r, id, true)
	}
}

// HandleSessionUpdateRoute handles PATCH /api/sessions/{id}.
func (h *Handlers) HandleSessionUpdateRoute(w http.ResponseWriter, r *http.Request) {
	if id, ok := sessionIDFromPath(w, r); ok {
		h.HandleUpdateSession(w, r, id)
	}
}

// HandleSessionDeleteRoute handles DELETE /api/sessions/{id}.
func (h *Handlers) HandleSessionDeleteRoute(w http.ResponseWriter, r *http.Request) {
	if id, ok := sessionIDFromPath(w, r); ok {
		h.HandleDeleteSession(w, id)
	}
}

// HandleSessionUserDataRoute handles /api/sessions/{id}/user-data.
func (h *Handlers) HandleSessionUserDataRoute(w http.ResponseWriter, r *http.Request) {
	if id, ok := sessionIDFromPath(w, r); ok {
		h.HandleSessionUserData(w, r, id)
	}
}

// HandleSessionCallbackRoute handles /api/sessions/{id}/callback.
func (h *Handlers) HandleSessionCallbackRoute(w http.ResponseWriter, r *http.Request) {
	if id, ok := sessionIDFromPath(w, r); ok {
		h.HandleSessionCallback(w, r, id)
	}
}

// HandleSessionSettingsRoute handles /api/sessions/{id}/settings.
func (h *Handlers) HandleSessionSettingsRoute(w http.ResponseWriter, r *http.Request) {
	if id, ok := sessionIDFromPath(w, r); ok {
		h.HandleSessionSettings(w, r, id)
	}
}

// HandleSessionPruneRoute handles /api/sessions/{id}/prune.
func (h *Handlers) HandleSessionPruneRoute(w http.ResponseWriter, r *http.Request) {
	if id, ok := sessionIDFromPath(w, r); ok {
		h.HandleSessionPrune(w, r, id)
	}
}

// HandleSessionChangesRoute handles /api/sessions/{id}/changes.
func (h *Handlers) HandleSessionChangesRoute(w http.ResponseWriter, r *http.Request) {
	if id, ok := sessionIDFromPath(w, r); ok {
		h.HandleSessionChanges(w, r, id)
	}
}

// HandleSessionImagesRoute handles /api/sessions/{id}/images and
// /api/sessions/{id}/images/{imageId}.
func (h *Handlers) HandleSessionImagesRoute(w http.ResponseWriter, r *http.Request) {
	if id, ok := sessionIDFromPath(w, r); ok {
		h.HandleSessionImages(w, r, id, r.PathValue("imageId"))
	}
}

// HandleSessionFilesRoute handles /api/sessions/{id}/files and
// /api/sessions/{id}/files/{fileId}.
func (h *Handlers) HandleSessionFilesRoute(w http.ResponseWriter, r *http.Request) {
	if id, ok := sessionIDFromPath(w, r); ok {
		h.HandleSessionFiles(w, r, id, r.PathValue("fileId"))
	}
}

// HandleSessionQueueRoute handles the queue endpoints, reconstructing the
// legacy queuePath ("/msgID/subAction") the underlying handler expects.
func (h *Handlers) HandleSessionQueueRoute(w http.ResponseWriter, r *http.Request) {
	if id, ok := sessionIDFromPath(w, r); ok {
		queuePath := ""
		if msgID := r.PathValue("msgId"); msgID != "" {
			queuePath = "/" + msgID
			if sub := r.PathValue("subAction"); sub != "" {
				queuePath += "/" + sub
			}
		}
		h.HandleSessionQueue(w, r, id, queuePath)
	}
}

// HandleSessionLoopRoute handles /api/sessions/{id}/loop and its subpaths.
func (h *Handlers) HandleSessionLoopRoute(w http.ResponseWriter, r *http.Request) {
	if id, ok := sessionIDFromPath(w, r); ok {
		h.HandleSessionLoop(w, r, id, r.PathValue("subPath"))
	}
}

// HandleSessionFlushRoute handles POST /api/sessions/{id}/flush.
func (h *Handlers) HandleSessionFlushRoute(w http.ResponseWriter, r *http.Request) {
	if id, ok := sessionIDFromPath(w, r); ok {
		h.HandleSessionFlush(w, r, id)
	}
}

// HandleSessionUIPromptAcknowledgeRoute handles POST /api/sessions/{id}/ui-prompt/acknowledge.
func (h *Handlers) HandleSessionUIPromptAcknowledgeRoute(w http.ResponseWriter, r *http.Request) {
	if id, ok := sessionIDFromPath(w, r); ok {
		h.HandleSessionUIPromptAcknowledge(w, r, id)
	}
}

// HandleSessionPromptArgCacheRoute handles GET /api/sessions/{id}/prompt-arg-cache.
func (h *Handlers) HandleSessionPromptArgCacheRoute(w http.ResponseWriter, r *http.Request) {
	if id, ok := sessionIDFromPath(w, r); ok {
		h.HandlePromptArgCache(w, r, id)
	}
}

// HandleWorkspacePromptsRoute dispatches GET/POST/DELETE /api/workspace-prompts.
func (h *Handlers) HandleWorkspacePromptsRoute(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		h.HandleWorkspacePromptsGET(w, r)
	case http.MethodPost:
		h.HandleWorkspacePromptsPOST(w, r)
	case http.MethodDelete:
		h.HandleWorkspacePromptsDELETE(w, r)
	default:
		methodNotAllowed(w)
	}
}
