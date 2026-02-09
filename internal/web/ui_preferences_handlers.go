package web

import (
	"net/http"
	"os"

	"github.com/inercia/mitto/internal/appdir"
	"github.com/inercia/mitto/internal/fileutil"
)

// UIPreferences represents the client-side UI state that needs to persist
// across app launches. This is stored server-side because the macOS app
// uses random ports, which means localStorage is isolated per launch.
type UIPreferences struct {
	// GroupingMode is the conversation list grouping mode: "none", "server", or "folder"
	GroupingMode string `json:"grouping_mode,omitempty"`

	// ExpandedGroups maps group keys to their expanded state (true = expanded)
	// Group keys are server names (for server grouping) or folder paths (for folder grouping)
	ExpandedGroups map[string]bool `json:"expanded_groups,omitempty"`
}

// handleUIPreferences handles GET and PUT /api/ui-preferences.
// GET returns the current UI preferences.
// PUT saves new UI preferences.
func (s *Server) handleUIPreferences(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		s.handleGetUIPreferences(w, r)
	case http.MethodPut:
		s.handleSaveUIPreferences(w, r)
	default:
		methodNotAllowed(w)
	}
}

// handleGetUIPreferences handles GET /api/ui-preferences.
func (s *Server) handleGetUIPreferences(w http.ResponseWriter, r *http.Request) {
	prefs, err := loadUIPreferences()
	if err != nil {
		// If file doesn't exist, return empty preferences
		if os.IsNotExist(err) {
			writeJSONOK(w, UIPreferences{})
			return
		}
		if s.logger != nil {
			s.logger.Error("Failed to load UI preferences", "error", err)
		}
		http.Error(w, "Failed to load UI preferences", http.StatusInternalServerError)
		return
	}

	writeJSONOK(w, prefs)
}

// handleSaveUIPreferences handles PUT /api/ui-preferences.
func (s *Server) handleSaveUIPreferences(w http.ResponseWriter, r *http.Request) {
	var prefs UIPreferences
	if !parseJSONBody(w, r, &prefs) {
		return
	}

	// Validate grouping mode
	if prefs.GroupingMode != "" &&
		prefs.GroupingMode != "none" &&
		prefs.GroupingMode != "server" &&
		prefs.GroupingMode != "folder" {
		http.Error(w, "Invalid grouping_mode: must be 'none', 'server', or 'folder'", http.StatusBadRequest)
		return
	}

	if err := saveUIPreferences(&prefs); err != nil {
		if s.logger != nil {
			s.logger.Error("Failed to save UI preferences", "error", err)
		}
		http.Error(w, "Failed to save UI preferences", http.StatusInternalServerError)
		return
	}

	if s.logger != nil {
		s.logger.Debug("UI preferences saved",
			"grouping_mode", prefs.GroupingMode,
			"expanded_groups_count", len(prefs.ExpandedGroups))
	}

	writeJSONOK(w, map[string]interface{}{
		"success": true,
	})
}

// loadUIPreferences loads UI preferences from the file.
func loadUIPreferences() (*UIPreferences, error) {
	path, err := appdir.UIPreferencesPath()
	if err != nil {
		return nil, err
	}

	var prefs UIPreferences
	if err := fileutil.ReadJSON(path, &prefs); err != nil {
		return nil, err
	}

	return &prefs, nil
}

// saveUIPreferences saves UI preferences to the file atomically.
func saveUIPreferences(prefs *UIPreferences) error {
	path, err := appdir.UIPreferencesPath()
	if err != nil {
		return err
	}

	return fileutil.WriteJSONAtomic(path, prefs, 0644)
}
