package handlers

import (
	"net/http"
	"strings"

	"github.com/inercia/mitto/internal/prompts"
)

// HandleRememberedArgsGET handles
// GET /api/workspace-prompts/remembered-args?working_dir=...&prompt=...[&session_id=...]
//
// It returns a JSON object of the form:
//
//	{"arguments": {"argName": "value", ...}}
//
// The returned map contains only arguments the current prompt definition
// declares with `remember: folder` or `remember: conversation` (mitto-x8v,
// mitto-47y.6.2) — stale keys are filtered out so a prompt that dropped or
// renamed a parameter does not carry ghosts. When session_id is provided, the
// conversation-scoped snapshot is merged on top of the folder-scoped snapshot
// (conversation values win on collision). When the workspace cannot be
// resolved, the prompt is unknown, or the feature is disabled, an empty map
// is returned with HTTP 200 (fail-open).
func (h *Handlers) HandleRememberedArgsGET(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w)
		return
	}
	workingDir := r.URL.Query().Get("working_dir")
	promptName := r.URL.Query().Get("prompt")
	sessionID := r.URL.Query().Get("session_id")
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
	// remember:{folder,conversation} args only, tracking the scope per name.
	if h.deps.GetWorkspacePromptsAll == nil {
		writeJSONOK(w, empty)
		return
	}
	all := h.deps.GetWorkspacePromptsAll(workingDir)
	rememberScope := map[string]string{} // arg name -> remember scope
	found := false
	for _, p := range all {
		if !strings.EqualFold(p.Name, promptName) {
			continue
		}
		found = true
		for _, param := range p.Parameters {
			switch param.Remember {
			case prompts.RememberFolder, prompts.RememberConversation:
				rememberScope[param.Name] = param.Remember
			}
		}
		break
	}
	if !found || len(rememberScope) == 0 {
		writeJSONOK(w, empty)
		return
	}

	// Fetch the folder-scope snapshot and filter to declared remember args.
	filtered := make(map[string]string, len(rememberScope))
	if h.deps.GetRememberedArgs != nil {
		stored, err := h.deps.GetRememberedArgs(ws.UUID, promptName)
		if err != nil {
			if h.deps.Logger != nil {
				h.deps.Logger.Warn("Failed to load remembered folder args",
					"workspace_uuid", ws.UUID,
					"prompt", promptName,
					"error", err)
			}
		} else {
			for k, v := range stored {
				if _, ok := rememberScope[k]; !ok {
					continue
				}
				filtered[k] = v
			}
		}
	}

	// Fetch the conversation-scope snapshot (when session_id is provided) and
	// overlay on top of folder-scope values. Conversation wins on collision
	// (mitto-47y.6.2).
	if sessionID != "" && h.deps.GetRememberedConversationArgs != nil {
		convStored, err := h.deps.GetRememberedConversationArgs(sessionID, promptName)
		if err != nil {
			if h.deps.Logger != nil {
				h.deps.Logger.Warn("Failed to load remembered conversation args",
					"session_id", sessionID,
					"prompt", promptName,
					"error", err)
			}
		} else {
			for k, v := range convStored {
				if _, ok := rememberScope[k]; !ok {
					continue
				}
				filtered[k] = v
			}
		}
	}

	writeJSONOK(w, map[string]interface{}{"arguments": filtered})
}
