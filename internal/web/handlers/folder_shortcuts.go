package handlers

import (
	"encoding/json"
	"net/http"
	"path/filepath"

	"github.com/inercia/mitto/internal/config"
)

// maxShortcutsPerSection is the server-side cap on buttons per section.
const maxShortcutsPerSection = 10

// folderShortcutsBody is the JSON envelope for GET and PUT
// /api/folders/shortcuts. Sections maps section IDs (e.g. "tasksList") to
// their ordered list of shortcut buttons.
type folderShortcutsBody struct {
	Sections map[string][]config.ShortcutButton `json:"sections"`
}

// HandleFolderShortcuts handles:
//   - GET /api/folders/shortcuts?working_dir=...  → folderShortcutsBody
//   - PUT /api/folders/shortcuts?working_dir=...  → (body: folderShortcutsBody) → folderShortcutsBody
//
// Requires authentication via the standard auth middleware.
func (h *Handlers) HandleFolderShortcuts(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		h.handleFolderShortcutsGet(w, r)
	case http.MethodPut:
		h.handleFolderShortcutsSet(w, r)
	default:
		methodNotAllowed(w)
	}
}

func (h *Handlers) handleFolderShortcutsGet(w http.ResponseWriter, r *http.Request) {
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

	data := config.FolderShortcuts(workingDir)
	if data == nil {
		data = map[string][]config.ShortcutButton{}
	}
	writeJSONOK(w, folderShortcutsBody{Sections: data})
}

func (h *Handlers) handleFolderShortcutsSet(w http.ResponseWriter, r *http.Request) {
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

	var body folderShortcutsBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErrorJSON(w, http.StatusBadRequest, "", "Invalid request body")
		return
	}
	if body.Sections == nil {
		body.Sections = map[string][]config.ShortcutButton{}
	}

	// Sanitise: drop entries with empty Prompt; cap each section to maxShortcutsPerSection.
	sanitised := make(map[string][]config.ShortcutButton, len(body.Sections))
	for section, buttons := range body.Sections {
		filtered := make([]config.ShortcutButton, 0, len(buttons))
		for _, b := range buttons {
			if b.Prompt == "" {
				continue
			}
			filtered = append(filtered, b)
		}
		if len(filtered) > maxShortcutsPerSection {
			filtered = filtered[:maxShortcutsPerSection]
		}
		sanitised[section] = filtered
	}

	if err := config.SetFolderShortcuts(workingDir, sanitised); err != nil {
		writeErrorJSON(w, http.StatusInternalServerError, "", "Failed to save shortcuts: "+err.Error())
		return
	}

	data := config.FolderShortcuts(workingDir)
	if data == nil {
		data = map[string][]config.ShortcutButton{}
	}
	writeJSONOK(w, folderShortcutsBody{Sections: data})
}
