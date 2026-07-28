package handlers

import (
	"net/http"
	"strings"

	"github.com/inercia/mitto/internal/prompts"
)

// HandleRememberedArgsGET handles
// GET /api/workspace-prompts/remembered-args?working_dir=...&prompt=...
//
// It returns a JSON object of the form:
//
//	{"arguments": {"argName": "value", ...}}
//
// The returned map contains only arguments the current prompt definition
// declares with `remember: folder` (mitto-x8v) — stale keys are filtered out
// so a prompt that dropped or renamed a parameter does not carry ghosts. When
// the workspace cannot be resolved, the prompt is unknown, or the feature is
// disabled, an empty map is returned with HTTP 200 (fail-open).
func (h *Handlers) HandleRememberedArgsGET(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w)
		return
	}
	workingDir := r.URL.Query().Get("working_dir")
	promptName := r.URL.Query().Get("prompt")
	if workingDir == "" {
		writeErrorJSON(w, http.StatusBadRequest, "", "working_dir query parameter is required")
		return
	}
	if promptName == "" {
		writeErrorJSON(w, http.StatusBadRequest, "", "prompt query parameter is required")
		return
	}

	empty := map[string]interface{}{"arguments": map[string]string{}}

	// Resolve the workspace UUID from working_dir.
	if h.deps.SessionManager == nil {
		writeJSONOK(w, empty)
		return
	}
	ws := h.deps.SessionManager.GetWorkspace(workingDir)
	if ws == nil || ws.UUID == "" {
		writeJSONOK(w, empty)
		return
	}

	// Look up the prompt's parameter declarations so we can filter to
	// remember:folder args only.
	if h.deps.GetWorkspacePromptsAll == nil {
		writeJSONOK(w, empty)
		return
	}
	all := h.deps.GetWorkspacePromptsAll(workingDir)
	remember := map[string]struct{}{}
	found := false
	for _, p := range all {
		if !strings.EqualFold(p.Name, promptName) {
			continue
		}
		found = true
		for _, param := range p.Parameters {
			if param.Remember == prompts.RememberFolder {
				remember[param.Name] = struct{}{}
			}
		}
		break
	}
	if !found || len(remember) == 0 {
		writeJSONOK(w, empty)
		return
	}

	// Fetch the persisted snapshot and filter to only remember:folder args.
	if h.deps.GetRememberedArgs == nil {
		writeJSONOK(w, empty)
		return
	}
	stored, err := h.deps.GetRememberedArgs(ws.UUID, promptName)
	if err != nil {
		if h.deps.Logger != nil {
			h.deps.Logger.Warn("Failed to load remembered args",
				"workspace_uuid", ws.UUID,
				"prompt", promptName,
				"error", err)
		}
		writeJSONOK(w, empty)
		return
	}
	filtered := make(map[string]string, len(stored))
	for k, v := range stored {
		if _, ok := remember[k]; !ok {
			continue
		}
		filtered[k] = v
	}
	writeJSONOK(w, map[string]interface{}{"arguments": filtered})
}
