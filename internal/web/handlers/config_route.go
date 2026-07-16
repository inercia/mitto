package handlers

import "net/http"

// HandleConfigRoute dispatches /api/config: GET → HandleGetConfig, POST → HandleSaveConfig.
func (h *Handlers) HandleConfigRoute(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		h.HandleGetConfig(w, r)
	case http.MethodPost:
		h.HandleSaveConfig(w, r)
	default:
		methodNotAllowed(w)
	}
}
