package handlers

import (
	"encoding/json"
	"net/http"

	"github.com/inercia/mitto/internal/appdir"
	"github.com/inercia/mitto/internal/config"
)

// globalShortcutsBody is the JSON envelope for GET and PUT
// /api/global/shortcuts. Sections maps section IDs (e.g. "conversations") to
// their ordered list of shortcut buttons. Prompts is populated on GET only: it
// carries the global (builtin + settings) prompts so the editor can offer them
// without a second request.
type globalShortcutsBody struct {
	Sections map[string][]config.ShortcutButton `json:"sections"`
	Prompts  []config.WebPrompt                 `json:"prompts,omitempty"`
}

// HandleGlobalShortcuts routes GET and PUT for /api/global/shortcuts.
// Global shortcut buttons are stored in settings.json and merged with
// folder-level shortcuts at render time (global entries first).
//
// Requires authentication via the standard auth middleware.
func (h *Handlers) HandleGlobalShortcuts(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		h.handleGlobalShortcutsGet(w, r)
	case http.MethodPut:
		h.handleGlobalShortcutsSet(w, r)
	default:
		methodNotAllowed(w)
	}
}

// globalPrompts returns the merged global prompt list (builtin overridden by
// settings prompts), keeping disabled entries so the editor can render them.
func (h *Handlers) globalPrompts() []config.WebPrompt {
	var builtinPrompts []config.WebPrompt
	if builtinDir, err := appdir.BuiltinPromptsDir(); err == nil {
		rawBuiltin, _ := config.LoadPromptsFromDir(builtinDir)
		for _, p := range rawBuiltin {
			wp := p.ToWebPrompt()
			wp.Source = config.PromptSourceBuiltin
			builtinPrompts = append(builtinPrompts, wp)
		}
	}
	var settingsPrompts []config.WebPrompt
	if h.deps.MittoConfig != nil {
		settingsPrompts = h.deps.MittoConfig.Prompts
	}
	return config.MergePromptsKeepDisabled(builtinPrompts, settingsPrompts, nil)
}

func (h *Handlers) handleGlobalShortcutsGet(w http.ResponseWriter, r *http.Request) {
	data := config.GlobalShortcuts()
	if data == nil {
		data = map[string][]config.ShortcutButton{}
	}
	writeJSONOK(w, globalShortcutsBody{Sections: data, Prompts: h.globalPrompts()})
}

func (h *Handlers) handleGlobalShortcutsSet(w http.ResponseWriter, r *http.Request) {
	var body globalShortcutsBody
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

	if err := config.SetGlobalShortcuts(sanitised); err != nil {
		writeErrorJSON(w, http.StatusInternalServerError, "", "Failed to save shortcuts: "+err.Error())
		return
	}

	// Keep the in-memory config in sync so an unrelated Settings save (which
	// preserves Shortcuts from MittoConfig) does not clobber this change.
	data := config.GlobalShortcuts()
	if data == nil {
		data = map[string][]config.ShortcutButton{}
	}
	if h.deps.MittoConfig != nil {
		h.deps.MittoConfig.Shortcuts = data
	}
	writeJSONOK(w, globalShortcutsBody{Sections: data, Prompts: h.globalPrompts()})
}
