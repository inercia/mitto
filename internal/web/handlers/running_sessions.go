package handlers

import (
	"net/http"
)

// RunningSessionInfo contains information about a running session.
type RunningSessionInfo struct {
	SessionID     string `json:"session_id"`
	Name          string `json:"name"`
	WorkingDir    string `json:"working_dir"`
	IsPrompting   bool   `json:"is_prompting"`
	PromptCount   int    `json:"prompt_count"`
	WorkspaceUUID string `json:"workspace_uuid"`
	ACPServer     string `json:"acp_server"`
}

// RunningSessionsResponse is the response for GET /api/sessions/running
type RunningSessionsResponse struct {
	TotalRunning int                  `json:"total_running"`
	Prompting    int                  `json:"prompting"`
	Sessions     []RunningSessionInfo `json:"sessions"`
}

// HandleRunningSessions handles GET /api/sessions/running
// Returns information about all running sessions, including which ones are actively prompting.
func (h *Handlers) HandleRunningSessions(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w)
		return
	}

	// Use the server's session store (owned by the server, not closed by this handler)
	store := h.deps.Store
	if store == nil {
		http.Error(w, "Session store not available", http.StatusInternalServerError)
		return
	}

	// Get list of running session IDs
	runningIDs := h.deps.SessionManager.ListRunningSessions()

	response := RunningSessionsResponse{
		TotalRunning: len(runningIDs),
		Sessions:     make([]RunningSessionInfo, 0, len(runningIDs)),
	}

	for _, sessionID := range runningIDs {
		bs := h.deps.SessionManager.GetSession(sessionID)
		if bs == nil {
			continue
		}

		info := RunningSessionInfo{
			SessionID:     sessionID,
			IsPrompting:   bs.IsPrompting(),
			PromptCount:   bs.GetPromptCount(),
			WorkspaceUUID: bs.GetWorkspaceUUID(),
		}

		// Get session metadata for name and working dir
		meta, err := store.GetMetadata(sessionID)
		if err == nil {
			info.Name = meta.Name
			info.WorkingDir = meta.WorkingDir
			info.ACPServer = meta.ACPServer
		}

		if info.IsPrompting {
			response.Prompting++
		}

		response.Sessions = append(response.Sessions, info)
	}

	writeJSONOK(w, response)
}
