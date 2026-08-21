package handlers

import (
	"encoding/json"
	"net/http"
	"path/filepath"
	"time"

	"github.com/inercia/mitto/internal/config"
)

// folderPinBody is the JSON envelope for GET and PUT /api/folders/pin. Pinned
// reflects the folder-level visibility flag persisted in folders.json.
type folderPinBody struct {
	Pinned bool `json:"pinned"`
}

// HandleFolderPin handles:
//   - GET /api/folders/pin?working_dir=...  → folderPinBody
//   - PUT /api/folders/pin?working_dir=...  → (body: folderPinBody) → folderPinBody
//
// Requires authentication via the standard auth middleware.
func (h *Handlers) HandleFolderPin(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		h.handleFolderPinGet(w, r)
	case http.MethodPut:
		h.handleFolderPinSet(w, r)
	default:
		methodNotAllowed(w)
	}
}

func (h *Handlers) handleFolderPinGet(w http.ResponseWriter, r *http.Request) {
	workingDir := r.URL.Query().Get("working_dir")
	if workingDir == "" {
		writeErrorJSON(w, http.StatusBadRequest, "", "working_dir is required")
		return
	}
	if !filepath.IsAbs(workingDir) {
		writeErrorJSON(w, http.StatusBadRequest, "", "working_dir must be an absolute path")
		return
	}
	if !h.isKnownWorkspaceDir(workingDir) {
		writeErrorJSON(w, http.StatusBadRequest, "", "working_dir does not match any known workspace")
		return
	}

	writeJSONOK(w, folderPinBody{Pinned: config.FolderPinned(workingDir)})
}

func (h *Handlers) handleFolderPinSet(w http.ResponseWriter, r *http.Request) {
	workingDir := r.URL.Query().Get("working_dir")
	if workingDir == "" {
		writeErrorJSON(w, http.StatusBadRequest, "", "working_dir is required")
		return
	}
	if !filepath.IsAbs(workingDir) {
		writeErrorJSON(w, http.StatusBadRequest, "", "working_dir must be an absolute path")
		return
	}
	if !h.isKnownWorkspaceDir(workingDir) {
		writeErrorJSON(w, http.StatusBadRequest, "", "working_dir does not match any known workspace")
		return
	}

	var body folderPinBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErrorJSON(w, http.StatusBadRequest, "", "Invalid request body")
		return
	}

	if err := config.SetFolderPinned(workingDir, body.Pinned); err != nil {
		if h.deps.Logger != nil {
			h.deps.Logger.Error("failed to update folder pin state",
				"working_dir", workingDir,
				"pinned", body.Pinned,
				"error", err)
		}
		writeErrorJSON(w, http.StatusInternalServerError, "", "failed to update folder pin state")
		return
	}

	// Stamp the folder's MRU timestamp on pin (so the "Add folder" dialog ranks
	// recently-pinned folders first when they later re-enter the hidden list).
	if body.Pinned {
		if err := config.SetFolderLastOpenedAt(workingDir, time.Now()); err != nil && h.deps.Logger != nil {
			h.deps.Logger.Warn("failed to stamp folder last_opened_at",
				"working_dir", workingDir, "error", err)
		}
	}

	// Propagate the new folder-native flag into the in-memory config so the next
	// /api/workspaces GET reflects the projected Pinned field on every workspace
	// in this folder.
	if h.deps.SyncConfigWorkspaces != nil {
		h.deps.SyncConfigWorkspaces()
	}

	writeJSONOK(w, folderPinBody{Pinned: config.FolderPinned(workingDir)})
}
