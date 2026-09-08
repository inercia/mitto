package handlers

import (
	"net/http"

	"github.com/inercia/mitto/internal/config/configsvc"
)

// configSnapshotResponse is the JSON envelope for GET /api/config/snapshot.
type configSnapshotResponse struct {
	Exists   bool        `json:"exists"`
	Revision string      `json:"revision,omitempty"`
	Config   interface{} `json:"config,omitempty"`
}

// HandleConfigSnapshot handles GET /api/config/snapshot (mitto-4rz.3): an
// instance-bearer-gated (see Deps.ValidateInstanceBearer), read-only view of
// settings.json, fully redacted per the configsvc registry — secrets such as
// web.auth.simple.password, web.auth.shared_token, and the whole mcp
// subtree never cross this boundary (internal/config/configsvc/tree.go).
// Available even when Deps.ConfigReadOnly is true: that flag only gates
// writes (see HandleConfigPatch).
func (h *Handlers) HandleConfigSnapshot(w http.ResponseWriter, r *http.Request) {
	if !h.validateInstanceBearer(w, r) {
		return
	}

	snap, err := configsvc.ReadSnapshot()
	if err != nil {
		writeConfigsvcError(w, err)
		return
	}

	resp := configSnapshotResponse{Exists: snap.Exists, Revision: snap.Revision}
	if whole, ok := snap.GetWhole(); ok {
		resp.Config = whole
	}
	writeJSONOK(w, resp)
}
