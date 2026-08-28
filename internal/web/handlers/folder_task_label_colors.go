package handlers

import (
	"encoding/json"
	"io"
	"net/http"
	"path/filepath"
	"strings"

	"github.com/inercia/mitto/internal/config"
)

// folderTaskLabelColorsBody is the JSON envelope for GET and PUT
// /api/folders/task-label-colors. Entries is the ordered folder-level
// mapping from task labels to task-title background colors.
type folderTaskLabelColorsBody struct {
	Entries []config.TaskLabelColor `json:"entries"`
}

// HandleFolderTaskLabelColors handles:
//   - GET /api/folders/task-label-colors?working_dir=...  → folderTaskLabelColorsBody
//   - PUT /api/folders/task-label-colors?working_dir=...  → (body: folderTaskLabelColorsBody) → folderTaskLabelColorsBody
//
// Requires authentication via the standard auth middleware.
func (h *Handlers) HandleFolderTaskLabelColors(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		h.handleFolderTaskLabelColorsGet(w, r)
	case http.MethodPut:
		h.handleFolderTaskLabelColorsSet(w, r)
	default:
		methodNotAllowed(w)
	}
}

func (h *Handlers) handleFolderTaskLabelColorsGet(w http.ResponseWriter, r *http.Request) {
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

	entries := config.FolderTaskLabelColors(workingDir)
	if entries == nil {
		entries = []config.TaskLabelColor{}
	}
	writeJSONOK(w, folderTaskLabelColorsBody{Entries: entries})
}

func (h *Handlers) handleFolderTaskLabelColorsSet(w http.ResponseWriter, r *http.Request) {
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

	var body folderTaskLabelColorsBody
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&body); err != nil {
		writeErrorJSON(w, http.StatusBadRequest, "", "Invalid request body")
		return
	}
	if body.Entries == nil {
		writeErrorJSON(w, http.StatusBadRequest, "", "Task label color entries are required")
		return
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		writeErrorJSON(w, http.StatusBadRequest, "", "Invalid request body")
		return
	}

	entries := make([]config.TaskLabelColor, len(body.Entries))
	for i, entry := range body.Entries {
		label := strings.TrimSpace(entry.Label)
		color := strings.TrimSpace(entry.Color)
		if label == "" {
			writeErrorJSON(w, http.StatusBadRequest, "", "Task label must not be empty")
			return
		}
		if !taskLabelColorPattern.MatchString(color) {
			writeErrorJSON(w, http.StatusBadRequest, "", "Task label color must be a six-digit hex color")
			return
		}
		entries[i] = config.TaskLabelColor{Label: label, Color: strings.ToLower(color)}
	}

	if err := config.SetFolderTaskLabelColors(workingDir, entries); err != nil {
		writeErrorJSON(w, http.StatusInternalServerError, "", "Failed to save task label colors: "+err.Error())
		return
	}
	if h.deps.BroadcastFolderTaskLabelColorsUpdated != nil {
		h.deps.BroadcastFolderTaskLabelColorsUpdated(workingDir)
	}
	writeJSONOK(w, folderTaskLabelColorsBody{Entries: entries})
}
