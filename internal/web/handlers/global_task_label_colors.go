package handlers

import (
	"encoding/json"
	"io"
	"net/http"
	"regexp"
	"strings"

	"github.com/inercia/mitto/internal/config"
)

var taskLabelColorPattern = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)

type globalTaskLabelColorsBody struct {
	Entries []config.TaskLabelColor `json:"entries"`
}

// HandleGlobalTaskLabelColors routes GET and PUT for the ordered global task
// label-to-title-background mapping stored in settings.json.
func (h *Handlers) HandleGlobalTaskLabelColors(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		entries := config.GlobalTaskLabelColors()
		if entries == nil {
			entries = []config.TaskLabelColor{}
		}
		writeJSONOK(w, globalTaskLabelColorsBody{Entries: entries})
	case http.MethodPut:
		h.handleGlobalTaskLabelColorsSet(w, r)
	default:
		methodNotAllowed(w)
	}
}

func (h *Handlers) handleGlobalTaskLabelColorsSet(w http.ResponseWriter, r *http.Request) {
	var body globalTaskLabelColorsBody
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

	if err := config.SetGlobalTaskLabelColors(entries); err != nil {
		writeErrorJSON(w, http.StatusInternalServerError, "", "Failed to save task label colors: "+err.Error())
		return
	}
	if h.deps.MittoConfig != nil {
		h.deps.MittoConfig.TaskLabelColors = entries
	}
	if h.deps.BroadcastTaskLabelColorsUpdated != nil {
		h.deps.BroadcastTaskLabelColorsUpdated()
	}
	writeJSONOK(w, globalTaskLabelColorsBody{Entries: entries})
}
