package web

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/inercia/mitto/internal/config"
	"github.com/inercia/mitto/internal/session"
)

// handleSessions handles GET and POST /api/sessions
func (s *Server) handleSessions(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		s.handleListSessions(w, r)
	case http.MethodPost:
		s.handleCreateSession(w, r)
	default:
		methodNotAllowed(w)
	}
}

// SessionCreateRequest represents a request to create a new session.
type SessionCreateRequest struct {
	Name       string `json:"name,omitempty"`
	WorkingDir string `json:"working_dir,omitempty"`
	ACPServer  string `json:"acp_server,omitempty"` // Optional: specify ACP server for the session
}

// handleCreateSession handles POST /api/sessions
func (s *Server) handleCreateSession(w http.ResponseWriter, r *http.Request) {
	var req SessionCreateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		// Allow empty body for default session creation
		req = SessionCreateRequest{}
	}

	// Note: Empty names are allowed - they will be auto-generated after first message
	// The frontend displays "New Conversation" as a placeholder for empty names

	// Determine workspace to use
	var workspace *config.WorkspaceSettings
	workspaces := s.config.GetWorkspaces()

	if req.WorkingDir != "" {
		// User specified a working directory - find matching workspace
		for i := range workspaces {
			if workspaces[i].WorkingDir == req.WorkingDir {
				workspace = &workspaces[i]
				break
			}
		}
		// If not found in workspaces but working dir provided, create ad-hoc workspace
		if workspace == nil {
			// Use default workspace's ACP config with the requested directory
			defaultWs := s.config.GetDefaultWorkspace()
			if defaultWs != nil {
				workspace = &config.WorkspaceSettings{
					ACPServer:  defaultWs.ACPServer,
					ACPCommand: defaultWs.ACPCommand,
					WorkingDir: req.WorkingDir,
				}
			}
		}
	} else if len(workspaces) == 1 {
		// Single workspace configured - use it
		workspace = &workspaces[0]
		req.WorkingDir = workspace.WorkingDir
	} else {
		// Multiple workspaces - use default
		workspace = s.config.GetDefaultWorkspace()
		if workspace != nil {
			req.WorkingDir = workspace.WorkingDir
		}
	}

	// Fall back to current directory if still no working dir
	if req.WorkingDir == "" {
		req.WorkingDir, _ = os.Getwd()
	}

	// Validate that we have a valid ACP configuration
	if workspace == nil || workspace.ACPCommand == "" {
		writeErrorJSON(w, http.StatusBadRequest, "no_workspace_configured",
			"No workspace configured. Please configure a workspace in Settings first.")
		return
	}

	// Note: The session manager already has the store set by the server at startup.
	// No need to create a new store here.

	// Create the background session with workspace configuration
	bs, err := s.sessionManager.CreateSessionWithWorkspace(req.Name, req.WorkingDir, workspace)
	if err != nil {
		if err == ErrTooManySessions {
			http.Error(w, "Maximum number of sessions reached (32)", http.StatusServiceUnavailable)
			return
		}
		if s.logger != nil {
			s.logger.Error("Failed to create session", "error", err)
		}
		http.Error(w, "Failed to create session", http.StatusInternalServerError)
		return
	}

	// Determine the ACP server name for the response
	acpServerName := s.config.ACPServer
	if workspace != nil && workspace.ACPServer != "" {
		acpServerName = workspace.ACPServer
	}

	// Broadcast session creation to all global events clients
	sessionData := map[string]interface{}{
		"session_id":     bs.GetSessionID(),
		"acp_session_id": bs.GetACPID(),
		"name":           req.Name,
		"acp_server":     acpServerName,
		"working_dir":    req.WorkingDir,
		"status":         "active",
	}
	s.eventsManager.Broadcast(WSMsgTypeSessionCreated, sessionData)

	// Return session info
	writeJSONCreated(w, sessionData)
}

// handleListSessions handles GET /api/sessions
func (s *Server) handleListSessions(w http.ResponseWriter, r *http.Request) {
	// Use the server's session store (owned by the server, not closed by this handler)
	store := s.Store()
	if store == nil {
		http.Error(w, "Session store not available", http.StatusInternalServerError)
		return
	}

	sessions, err := store.List()
	if err != nil {
		if s.logger != nil {
			s.logger.Error("Failed to list sessions", "error", err)
		}
		http.Error(w, "Failed to list sessions", http.StatusInternalServerError)
		return
	}

	// Sort by update time, most recently used first
	sort.Slice(sessions, func(i, j int) bool {
		return sessions[i].UpdatedAt.After(sessions[j].UpdatedAt)
	})

	writeJSONOK(w, sessions)
}

// handleSessionDetail handles GET, PATCH, DELETE {prefix}/api/sessions/{id}, GET {prefix}/api/sessions/{id}/events,
// WS {prefix}/api/sessions/{id}/ws, and image operations
func (s *Server) handleSessionDetail(w http.ResponseWriter, r *http.Request) {
	// Extract session ID from path: {prefix}/api/sessions/{id} or {prefix}/api/sessions/{id}/events etc.
	// First strip the API prefix, then strip /api/sessions/
	path := r.URL.Path
	path = strings.TrimPrefix(path, s.apiPrefix)
	path = strings.TrimPrefix(path, "/api/sessions/")
	parts := strings.Split(path, "/")
	if len(parts) == 0 || parts[0] == "" {
		http.Error(w, "Session ID required", http.StatusBadRequest)
		return
	}

	sessionID := parts[0]

	// Validate session ID format to prevent path traversal
	if !IsValidSessionID(sessionID) {
		http.Error(w, "Invalid session ID format", http.StatusBadRequest)
		return
	}

	isEventsRequest := len(parts) > 1 && parts[1] == "events"
	isWSRequest := len(parts) > 1 && parts[1] == "ws"
	isImagesRequest := len(parts) > 1 && parts[1] == "images"
	isQueueRequest := len(parts) > 1 && parts[1] == "queue"

	// Handle WebSocket upgrade for per-session connections
	if isWSRequest {
		s.handleSessionWS(w, r)
		return
	}

	// Handle image operations
	if isImagesRequest {
		// Extract image ID if present: /api/sessions/{id}/images/{imageId}
		imagePath := ""
		if len(parts) > 2 {
			imagePath = parts[2]
		}
		s.handleSessionImages(w, r, sessionID, imagePath)
		return
	}

	// Handle queue operations
	if isQueueRequest {
		// Extract message ID if present: /api/sessions/{id}/queue/{msgId}
		queuePath := ""
		if len(parts) > 2 {
			queuePath = "/" + parts[2]
		}
		s.handleSessionQueue(w, r, sessionID, queuePath)
		return
	}

	switch r.Method {
	case http.MethodGet:
		s.handleGetSession(w, r, sessionID, isEventsRequest)
	case http.MethodPatch:
		s.handleUpdateSession(w, r, sessionID)
	case http.MethodDelete:
		s.handleDeleteSession(w, sessionID)
	default:
		methodNotAllowed(w)
	}
}

// handleGetSession handles GET /api/sessions/{id} and GET /api/sessions/{id}/events
// For events, supports query parameters:
//   - limit: maximum number of events to return (returns last N events)
//   - before: only return events with seq < before (for pagination)
//   - order: "asc" (default, oldest first) or "desc" (newest first)
func (s *Server) handleGetSession(w http.ResponseWriter, r *http.Request, sessionID string, isEventsRequest bool) {
	// Use the server's session store (owned by the server, not closed by this handler)
	store := s.Store()
	if store == nil {
		http.Error(w, "Session store not available", http.StatusInternalServerError)
		return
	}

	if isEventsRequest {
		// Parse query parameters for pagination
		query := r.URL.Query()
		var limit int
		var beforeSeq int64
		reverseOrder := query.Get("order") == "desc"

		if limitStr := query.Get("limit"); limitStr != "" {
			if l, err := strconv.Atoi(limitStr); err == nil && l > 0 {
				limit = l
			}
		}
		if beforeStr := query.Get("before"); beforeStr != "" {
			if b, err := strconv.ParseInt(beforeStr, 10, 64); err == nil && b > 0 {
				beforeSeq = b
			}
		}

		var events []session.Event
		var err error
		if limit > 0 {
			if reverseOrder {
				// Use reverse order read (newest first)
				events, err = store.ReadEventsLastReverse(sessionID, limit, beforeSeq)
			} else {
				// Use paginated read (oldest first)
				events, err = store.ReadEventsLast(sessionID, limit, beforeSeq)
			}
		} else {
			// Read all events (backward compatible)
			events, err = store.ReadEvents(sessionID)
			// If reverse order requested, reverse the result
			if reverseOrder && err == nil {
				for i, j := 0, len(events)-1; i < j; i, j = i+1, j-1 {
					events[i], events[j] = events[j], events[i]
				}
			}
		}

		if err != nil {
			if err == session.ErrSessionNotFound {
				http.Error(w, "Session not found", http.StatusNotFound)
				return
			}
			if s.logger != nil {
				s.logger.Error("Failed to read session events", "error", err, "session_id", sessionID)
			}
			http.Error(w, "Failed to read session events", http.StatusInternalServerError)
			return
		}

		writeJSONOK(w, events)
	} else {
		// Return session metadata
		meta, err := store.GetMetadata(sessionID)
		if err != nil {
			if err == session.ErrSessionNotFound {
				http.Error(w, "Session not found", http.StatusNotFound)
				return
			}
			if s.logger != nil {
				s.logger.Error("Failed to get session metadata", "error", err, "session_id", sessionID)
			}
			http.Error(w, "Failed to get session metadata", http.StatusInternalServerError)
			return
		}

		writeJSONOK(w, meta)
	}
}

// SessionUpdateRequest represents a request to update session metadata.
type SessionUpdateRequest struct {
	Name        *string `json:"name,omitempty"`
	Description *string `json:"description,omitempty"`
}

// handleUpdateSession handles PATCH /api/sessions/{id}
func (s *Server) handleUpdateSession(w http.ResponseWriter, r *http.Request, sessionID string) {
	var req SessionUpdateRequest
	if !parseJSONBody(w, r, &req) {
		return
	}

	// Use the server's session store (owned by the server, not closed by this handler)
	store := s.Store()
	if store == nil {
		http.Error(w, "Session store not available", http.StatusInternalServerError)
		return
	}

	err := store.UpdateMetadata(sessionID, func(meta *session.Metadata) {
		if req.Name != nil {
			meta.Name = *req.Name
		}
		if req.Description != nil {
			meta.Description = *req.Description
		}
	})
	if err != nil {
		if err == session.ErrSessionNotFound {
			http.Error(w, "Session not found", http.StatusNotFound)
			return
		}
		if s.logger != nil {
			s.logger.Error("Failed to update session", "error", err, "session_id", sessionID)
		}
		http.Error(w, "Failed to update session", http.StatusInternalServerError)
		return
	}

	// Return updated metadata
	meta, err := store.GetMetadata(sessionID)
	if err != nil {
		http.Error(w, "Failed to get updated metadata", http.StatusInternalServerError)
		return
	}

	// Broadcast the rename to all connected WebSocket clients
	if req.Name != nil {
		s.BroadcastSessionRenamed(sessionID, *req.Name)
	}

	writeJSONOK(w, meta)
}

// RunningSessionInfo contains information about a running session.
type RunningSessionInfo struct {
	SessionID   string `json:"session_id"`
	Name        string `json:"name"`
	WorkingDir  string `json:"working_dir"`
	IsPrompting bool   `json:"is_prompting"`
	PromptCount int    `json:"prompt_count"`
}

// RunningSessionsResponse is the response for GET /api/sessions/running
type RunningSessionsResponse struct {
	TotalRunning int                  `json:"total_running"`
	Prompting    int                  `json:"prompting"`
	Sessions     []RunningSessionInfo `json:"sessions"`
}

// handleRunningSessions handles GET /api/sessions/running
// Returns information about all running sessions, including which ones are actively prompting.
func (s *Server) handleRunningSessions(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w)
		return
	}

	// Use the server's session store (owned by the server, not closed by this handler)
	store := s.Store()
	if store == nil {
		http.Error(w, "Session store not available", http.StatusInternalServerError)
		return
	}

	// Get list of running session IDs
	runningIDs := s.sessionManager.ListRunningSessions()

	response := RunningSessionsResponse{
		TotalRunning: len(runningIDs),
		Sessions:     make([]RunningSessionInfo, 0, len(runningIDs)),
	}

	for _, sessionID := range runningIDs {
		bs := s.sessionManager.GetSession(sessionID)
		if bs == nil {
			continue
		}

		info := RunningSessionInfo{
			SessionID:   sessionID,
			IsPrompting: bs.IsPrompting(),
			PromptCount: bs.GetPromptCount(),
		}

		// Get session metadata for name and working dir
		meta, err := store.GetMetadata(sessionID)
		if err == nil {
			info.Name = meta.Name
			info.WorkingDir = meta.WorkingDir
		}

		if info.IsPrompting {
			response.Prompting++
		}

		response.Sessions = append(response.Sessions, info)
	}

	writeJSONOK(w, response)
}

// handleDeleteSession handles DELETE /api/sessions/{id}
func (s *Server) handleDeleteSession(w http.ResponseWriter, sessionID string) {
	// First, close any running background session for this ID
	// This stops the ACP process and cleans up resources
	if s.sessionManager != nil {
		s.sessionManager.CloseSession(sessionID, "deleted")
	}

	// Use the server's session store (owned by the server, not closed by this handler)
	store := s.Store()
	if store == nil {
		http.Error(w, "Session store not available", http.StatusInternalServerError)
		return
	}

	if err := store.Delete(sessionID); err != nil {
		if err == session.ErrSessionNotFound {
			http.Error(w, "Session not found", http.StatusNotFound)
			return
		}
		if s.logger != nil {
			s.logger.Error("Failed to delete session", "error", err, "session_id", sessionID)
		}
		http.Error(w, "Failed to delete session", http.StatusInternalServerError)
		return
	}

	// Broadcast the deletion to all connected WebSocket clients
	s.BroadcastSessionDeleted(sessionID)

	writeNoContent(w)
}

// handleWorkspaces handles /api/workspaces
// GET: List all workspaces
// POST: Add a new workspace
// DELETE: Remove a workspace (via query param ?dir=...)
func (s *Server) handleWorkspaces(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		s.handleGetWorkspaces(w, r)
	case http.MethodPost:
		s.handleAddWorkspace(w, r)
	case http.MethodDelete:
		s.handleRemoveWorkspace(w, r)
	default:
		methodNotAllowed(w)
	}
}

// handleGetWorkspaces returns the list of workspaces and available ACP servers
func (s *Server) handleGetWorkspaces(w http.ResponseWriter, r *http.Request) {
	workspaces := s.sessionManager.GetWorkspaces()

	// Get available ACP servers from config
	var acpServers []map[string]string
	if s.config.MittoConfig != nil {
		for _, srv := range s.config.MittoConfig.ACPServers {
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
	Color      string `json:"color,omitempty"`
}

// handleAddWorkspace adds a new workspace
func (s *Server) handleAddWorkspace(w http.ResponseWriter, r *http.Request) {
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

	// Get the ACP server command from config
	var acpCommand string
	if s.config.MittoConfig != nil {
		srv, err := s.config.MittoConfig.GetServer(req.ACPServer)
		if err != nil {
			http.Error(w, fmt.Sprintf("Unknown ACP server: %s", req.ACPServer), http.StatusBadRequest)
			return
		}
		acpCommand = srv.Command
	} else {
		// Fallback: use server name as command
		acpCommand = req.ACPServer
	}

	// Check if workspace already exists
	if ws := s.sessionManager.GetWorkspace(req.WorkingDir); ws != nil {
		http.Error(w, fmt.Sprintf("Workspace already exists for directory: %s", req.WorkingDir), http.StatusConflict)
		return
	}

	// Add the workspace
	newWorkspace := config.WorkspaceSettings{
		ACPServer:  req.ACPServer,
		ACPCommand: acpCommand,
		WorkingDir: req.WorkingDir,
		Color:      req.Color,
	}
	s.sessionManager.AddWorkspace(newWorkspace)

	// Also update the server config
	s.config.Workspaces = s.sessionManager.GetWorkspaces()

	writeJSONCreated(w, newWorkspace)
}

// handleRemoveWorkspace removes a workspace
func (s *Server) handleRemoveWorkspace(w http.ResponseWriter, r *http.Request) {
	workingDir := r.URL.Query().Get("dir")
	if workingDir == "" {
		http.Error(w, "dir query parameter is required", http.StatusBadRequest)
		return
	}

	// Check if workspace exists
	if ws := s.sessionManager.GetWorkspace(workingDir); ws == nil {
		http.Error(w, fmt.Sprintf("Workspace not found for directory: %s", workingDir), http.StatusNotFound)
		return
	}

	// Check if there are conversations using this workspace
	// Use the server's session store (owned by the server, not closed by this handler)
	store := s.Store()
	if store == nil {
		http.Error(w, "Session store not available", http.StatusInternalServerError)
		return
	}

	sessions, err := store.List()
	if err != nil {
		if s.logger != nil {
			s.logger.Error("Failed to list sessions", "error", err)
		}
		http.Error(w, "Failed to check workspace usage", http.StatusInternalServerError)
		return
	}

	// Count conversations using this workspace
	var conversationCount int
	for _, sess := range sessions {
		if sess.WorkingDir == workingDir {
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

	// Remove the workspace
	s.sessionManager.RemoveWorkspace(workingDir)

	// Also update the server config
	s.config.Workspaces = s.sessionManager.GetWorkspaces()

	writeNoContent(w)
}

// handleWorkspacePrompts handles GET /api/workspace-prompts?dir=...
// Returns the prompts from the workspace's .mittorc file.
// Supports conditional requests via If-Modified-Since header.
func (s *Server) handleWorkspacePrompts(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w)
		return
	}

	workingDir := r.URL.Query().Get("dir")
	if workingDir == "" {
		http.Error(w, "dir query parameter is required", http.StatusBadRequest)
		return
	}

	// Get the file's last modification time for conditional requests
	lastModified := s.sessionManager.GetWorkspaceRCLastModified(workingDir)

	// Check If-Modified-Since header for conditional request
	if !lastModified.IsZero() {
		// Set Last-Modified header
		w.Header().Set("Last-Modified", lastModified.UTC().Format(http.TimeFormat))

		// Check if client has fresh data
		if ifModifiedSince := r.Header.Get("If-Modified-Since"); ifModifiedSince != "" {
			if t, err := time.Parse(http.TimeFormat, ifModifiedSince); err == nil {
				// HTTP time has second precision, so truncate for comparison
				if !lastModified.Truncate(time.Second).After(t) {
					w.WriteHeader(http.StatusNotModified)
					return
				}
			}
		}
	}

	prompts := s.sessionManager.GetWorkspacePrompts(workingDir)

	if s.logger != nil {
		s.logger.Debug("Returning workspace prompts",
			"working_dir", workingDir,
			"prompt_count", len(prompts),
			"last_modified", lastModified)
	}

	writeJSONOK(w, map[string]interface{}{
		"prompts":     prompts,
		"working_dir": workingDir,
	})
}
