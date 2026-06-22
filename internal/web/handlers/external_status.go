package handlers

import (
	"net/http"
)

// ExternalStatusResponse represents the response for the external status endpoint.
type ExternalStatusResponse struct {
	Enabled bool `json:"enabled"`
	Port    int  `json:"port"`
}

// HandleExternalStatus handles GET /api/external-status.
// Returns the current status of the external listener.
//
// The external-listener lifecycle methods (start/stop/port) remain on
// *web.Server because they own server-internal state (listener, mutex,
// http.Server); this handler only reports that state via the Deps facade.
func (h *Handlers) HandleExternalStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	enabled := false
	if h.deps.IsExternalListenerRunning != nil {
		enabled = h.deps.IsExternalListenerRunning()
	}
	port := 0
	if h.deps.GetExternalPort != nil {
		port = h.deps.GetExternalPort()
	}

	writeJSONOK(w, ExternalStatusResponse{
		Enabled: enabled,
		Port:    port,
	})
}
