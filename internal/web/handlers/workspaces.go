package handlers

import (
	"fmt"
	"net/http"
	"os"
	"strings"

	configPkg "github.com/inercia/mitto/internal/config"
)

// HandleWorkspaces handles /api/workspaces (GET/POST/DELETE).
func (h *Handlers) HandleWorkspaces(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		h.handleGetWorkspaces(w, r)
	case http.MethodPost:
		h.handleAddWorkspace(w, r)
	case http.MethodDelete:
		h.handleRemoveWorkspace(w, r)
	default:
		methodNotAllowed(w)
	}
}

// handleGetWorkspaces returns the list of workspaces and available ACP servers.
// When the optional working_dir query parameter is provided, the acp_servers list
// is scoped to only the servers that have a workspace configured for that folder
// (the same set the MCP conversation-creation tools accept). When absent, all
// configured ACP servers are returned.
func (h *Handlers) handleGetWorkspaces(w http.ResponseWriter, r *http.Request) {
	workspaces := h.deps.SessionManager.GetWorkspaces()

	// Optional folder scoping for the ACP server list.
	workingDir := strings.TrimSpace(r.URL.Query().Get("working_dir"))
	var folderServerSet map[string]bool
	if workingDir != "" {
		folderWorkspaces := h.deps.SessionManager.GetWorkspacesForFolder(workingDir)
		folderServerSet = make(map[string]bool, len(folderWorkspaces))
		for _, ws := range folderWorkspaces {
			folderServerSet[ws.ACPServer] = true
		}
	}

	// Get available ACP servers from config, filtered to the folder when requested.
	var acpServers []map[string]string
	if h.deps.MittoConfig != nil {
		for _, srv := range h.deps.MittoConfig.ACPServers {
			if folderServerSet != nil && !folderServerSet[srv.Name] {
				continue
			}
			acpServers = append(acpServers, map[string]string{
				"name":    srv.Name,
				"command": srv.Command,
			})
		}
	}

	writeJSONOK(w, map[string]interface{}{
		"workspaces":  workspaces,
		"acp_servers": acpServers,
	})
}

// WorkspaceAddRequest represents a request to add a new workspace
type WorkspaceAddRequest struct {
	ACPServer  string `json:"acp_server"`
	WorkingDir string `json:"working_dir"`
	Name       string `json:"name,omitempty"`
	Color      string `json:"color,omitempty"`
	Code       string `json:"code,omitempty"`
}

// handleAddWorkspace adds a new workspace
func (h *Handlers) handleAddWorkspace(w http.ResponseWriter, r *http.Request) {
	var req WorkspaceAddRequest
	if !parseJSONBody(w, r, &req) {
		return
	}

	if req.WorkingDir == "" {
		http.Error(w, "working_dir is required", http.StatusBadRequest)
		return
	}

	if req.ACPServer == "" {
		http.Error(w, "acp_server is required", http.StatusBadRequest)
		return
	}

	// Validate the directory exists
	info, err := os.Stat(req.WorkingDir)
	if err != nil {
		http.Error(w, fmt.Sprintf("Directory does not exist: %s", req.WorkingDir), http.StatusBadRequest)
		return
	}
	if !info.IsDir() {
		http.Error(w, fmt.Sprintf("Path is not a directory: %s", req.WorkingDir), http.StatusBadRequest)
		return
	}

	// Validate the ACP server exists in global config.
	if h.deps.MittoConfig != nil {
		if _, err := h.deps.MittoConfig.GetServer(req.ACPServer); err != nil {
			http.Error(w, fmt.Sprintf("Unknown ACP server: %s", req.ACPServer), http.StatusBadRequest)
			return
		}
	}

	// Check if workspace already exists
	if ws := h.deps.SessionManager.GetWorkspace(req.WorkingDir); ws != nil {
		http.Error(w, fmt.Sprintf("Workspace already exists for directory: %s", req.WorkingDir), http.StatusConflict)
		return
	}

	// Add the workspace. ACP command/cwd/env are resolved from global config at runtime —
	// they are never stored on the workspace struct.
	newWorkspace := configPkg.WorkspaceSettings{
		ACPServer:  req.ACPServer,
		WorkingDir: req.WorkingDir,
		Name:       req.Name,
		Color:      req.Color,
		Code:       req.Code,
	}
	h.deps.SessionManager.AddWorkspace(newWorkspace)

	// Also update the server config
	if h.deps.SyncConfigWorkspaces != nil {
		h.deps.SyncConfigWorkspaces()
	}

	writeJSONCreated(w, newWorkspace)
}

// handleRemoveWorkspace removes a workspace by UUID.
// Supports both 'uuid' and legacy 'dir' query parameters for backwards compatibility.
func (h *Handlers) handleRemoveWorkspace(w http.ResponseWriter, r *http.Request) {
	uuid := r.URL.Query().Get("uuid")
	workingDir := r.URL.Query().Get("dir")

	// Find the workspace - prefer UUID, fall back to workingDir
	var ws *configPkg.WorkspaceSettings
	if uuid != "" {
		ws = h.deps.SessionManager.GetWorkspaceByUUID(uuid)
	} else if workingDir != "" {
		// Legacy support: find first workspace matching directory
		ws = h.deps.SessionManager.GetWorkspace(workingDir)
	} else {
		http.Error(w, "uuid or dir query parameter is required", http.StatusBadRequest)
		return
	}

	if ws == nil {
		http.Error(w, "Workspace not found", http.StatusNotFound)
		return
	}

	// Check if there are conversations using this specific workspace
	// Use the server's session store (owned by the server, not closed by this handler)
	store := h.deps.Store
	if store == nil {
		http.Error(w, "Session store not available", http.StatusInternalServerError)
		return
	}

	sessions, err := store.List()
	if err != nil {
		if h.deps.Logger != nil {
			h.deps.Logger.Error("Failed to list sessions", "error", err)
		}
		http.Error(w, "Failed to check workspace usage", http.StatusInternalServerError)
		return
	}

	// Count conversations using this specific workspace (same dir AND server)
	var conversationCount int
	for _, sess := range sessions {
		if sess.WorkingDir == ws.WorkingDir && sess.ACPServer == ws.ACPServer {
			conversationCount++
		}
	}

	if conversationCount > 0 {
		// Return error with count - don't allow deletion
		writeJSON(w, http.StatusConflict, map[string]interface{}{
			"error":              "workspace_in_use",
			"message":            fmt.Sprintf("Cannot delete workspace: %d conversation(s) are using it", conversationCount),
			"conversation_count": conversationCount,
		})
		return
	}

	// Remove the workspace by UUID
	h.deps.SessionManager.RemoveWorkspace(ws.UUID)

	// Also update the server config
	if h.deps.SyncConfigWorkspaces != nil {
		h.deps.SyncConfigWorkspaces()
	}

	writeNoContent(w)
}
