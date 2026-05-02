package web

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/inercia/mitto/internal/appdir"
	"github.com/inercia/mitto/internal/config"
	"github.com/inercia/mitto/internal/processors"
	"github.com/inercia/mitto/internal/runner"
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
	// Use sessionManager.GetWorkspaces() as the source of truth - it maintains the live
	// workspace data that can be dynamically updated via the settings UI.
	// s.config.GetWorkspaces() may be stale if workspaces were added/removed at runtime.
	var workspace *config.WorkspaceSettings
	workspaces := s.sessionManager.GetWorkspaces()

	if req.WorkingDir != "" {
		// User specified a working directory - find matching workspace
		// If acp_server is also specified, match both (for duplicate workspaces with same dir)
		for i := range workspaces {
			if workspaces[i].WorkingDir == req.WorkingDir {
				// If ACP server is specified, only match if it also matches
				if req.ACPServer != "" && workspaces[i].ACPServer != req.ACPServer {
					continue
				}
				workspace = &workspaces[i]
				break
			}
		}
		// If not found in workspaces but working dir provided, create ad-hoc workspace
		if workspace == nil {
			// Use default workspace's ACP config with the requested directory
			defaultWs := s.sessionManager.GetDefaultWorkspace()
			if defaultWs != nil {
				workspace = &config.WorkspaceSettings{
					ACPServer:  defaultWs.ACPServer,
					ACPCommand: defaultWs.ACPCommand,
					WorkingDir: req.WorkingDir,
				}
				// Ensure the ad-hoc workspace has a UUID for auxiliary sessions
				workspace.EnsureUUID()
			}
		}
	} else if len(workspaces) == 1 {
		// Single workspace configured - use it
		workspace = &workspaces[0]
		req.WorkingDir = workspace.WorkingDir
	} else {
		// Multiple workspaces - use default
		workspace = s.sessionManager.GetDefaultWorkspace()
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
		// Broadcast ACP start failure to all clients (use empty session_id since session wasn't created)
		s.BroadcastACPStartFailed("", req.Name, err, workspace.ACPCommand)
		http.Error(w, "Failed to create session", http.StatusInternalServerError)
		return
	}

	// Invalidate negative session cache in case this session ID was previously cached as not found
	if s.negativeSessionCache != nil {
		s.negativeSessionCache.Remove(bs.GetSessionID())
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

// SessionListResponse extends session.Metadata with additional runtime fields.
type SessionListResponse struct {
	session.Metadata
	// PeriodicEnabled is true when a periodic config exists for this session.
	// This determines UI mode (shows frequency panel and lock/unlock buttons).
	// Note: This indicates config existence, not whether periodic runs are active.
	PeriodicEnabled bool `json:"periodic_enabled"`
	// NextScheduledAt is the next scheduled time for periodic sessions (nil if not periodic or not scheduled).
	NextScheduledAt *time.Time `json:"next_scheduled_at,omitempty"`
	// PeriodicFrequency is the frequency configuration for periodic sessions (nil if not periodic).
	PeriodicFrequency *session.Frequency `json:"periodic_frequency,omitempty"`
	// IsWaitingForChildren is true when the session is currently blocked on mitto_children_tasks_wait.
	// This is a runtime state (not persisted) tracked by the SessionManager.
	IsWaitingForChildren bool `json:"is_waiting_for_children,omitempty"`
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

	// Build response with periodic_enabled status and scheduling info
	response := make([]SessionListResponse, len(sessions))
	for i, meta := range sessions {
		response[i] = SessionListResponse{
			Metadata:        meta,
			PeriodicEnabled: false, // Default to false
		}
		// Check if a periodic config exists for this session
		// PeriodicEnabled = true means UI shows periodic mode (frequency panel, lock/unlock buttons)
		periodicStore := store.Periodic(meta.SessionID)
		if periodic, err := periodicStore.Get(); err == nil && periodic != nil {
			// Periodic config exists - session is in periodic mode
			response[i].PeriodicEnabled = true
			// Include scheduling info for progress indicator
			if periodic.NextScheduledAt != nil && !periodic.NextScheduledAt.IsZero() {
				response[i].NextScheduledAt = periodic.NextScheduledAt
			}
			response[i].PeriodicFrequency = &periodic.Frequency
		}
		// Check if session is currently waiting for children (runtime state from SessionManager)
		if s.sessionManager != nil {
			response[i].IsWaitingForChildren = s.sessionManager.IsWaitingForChildren(meta.SessionID)
		}
	}

	writeJSONOK(w, response)
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
	isFilesRequest := len(parts) > 1 && parts[1] == "files"
	isQueueRequest := len(parts) > 1 && parts[1] == "queue"
	isUserDataRequest := len(parts) > 1 && parts[1] == "user-data"
	isPeriodicRequest := len(parts) > 1 && parts[1] == "periodic"
	isCallbackRequest := len(parts) > 1 && parts[1] == "callback"
	isSettingsRequest := len(parts) > 1 && parts[1] == "settings"
	isPruneRequest := len(parts) > 1 && parts[1] == "prune"

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

	// Handle file operations
	if isFilesRequest {
		// Extract file ID if present: /api/sessions/{id}/files/{fileId}
		filePath := ""
		if len(parts) > 2 {
			filePath = parts[2]
		}
		s.handleSessionFiles(w, r, sessionID, filePath)
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

	// Handle user data operations
	if isUserDataRequest {
		s.handleSessionUserData(w, r, sessionID)
		return
	}

	// Handle periodic prompt operations
	if isPeriodicRequest {
		// Check for sub-paths like /periodic/run-now
		periodicSubPath := ""
		if len(parts) > 2 {
			periodicSubPath = parts[2]
		}
		s.handleSessionPeriodic(w, r, sessionID, periodicSubPath)
		return
	}

	// Handle callback token operations
	if isCallbackRequest {
		s.handleSessionCallback(w, r, sessionID)
		return
	}

	// Handle advanced settings operations
	if isSettingsRequest {
		s.handleSessionSettings(w, r, sessionID)
		return
	}

	// Handle prune operations
	if isPruneRequest {
		s.handleSessionPrune(w, r, sessionID)
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
	Pinned      *bool   `json:"pinned,omitempty"`   // Deprecated: use Archived instead
	Archived    *bool   `json:"archived,omitempty"` // If true, session is archived
}

// archiveWaitTimeout is the maximum time to wait for a response to complete when archiving.
const archiveWaitTimeout = 5 * time.Minute

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

	// When archiving a child session, delete it instead (children should never be archived)
	if req.Archived != nil && *req.Archived {
		meta, err := store.GetMetadata(sessionID)
		if err == nil && meta.ParentSessionID != "" {
			if s.logger != nil {
				s.logger.Info("Converting child archive to delete",
					"session_id", sessionID,
					"parent_session_id", meta.ParentSessionID)
			}
			s.handleDeleteSession(w, sessionID)
			return
		}
	}

	// Handle archive lifecycle: wait for response and stop ACP
	if req.Archived != nil && *req.Archived {
		if s.sessionManager != nil {
			// Wait for any active response to complete before archiving
			// This ensures we don't interrupt an in-progress agent response
			reason := "archived"
			if !s.sessionManager.CloseSessionGracefully(sessionID, reason, archiveWaitTimeout) {
				// Timeout waiting for response - still proceed with archive but log warning
				if s.logger != nil {
					s.logger.Warn("Timeout waiting for response before archiving, proceeding anyway",
						"session_id", sessionID)
				}
				// Force close the session
				reason = "archived_timeout"
				s.sessionManager.CloseSession(sessionID, reason)
			}
			// Broadcast that ACP was stopped
			s.BroadcastACPStopped(sessionID, reason)
		}
	}

	err := store.UpdateMetadata(sessionID, func(meta *session.Metadata) {
		if req.Name != nil {
			meta.Name = *req.Name
		}
		if req.Description != nil {
			meta.Description = *req.Description
		}
		if req.Pinned != nil {
			meta.Pinned = *req.Pinned
		}
		if req.Archived != nil {
			meta.Archived = *req.Archived
			if *req.Archived {
				// Set archived timestamp when archiving
				meta.ArchivedAt = time.Now()
			} else {
				// Clear archived timestamp when unarchiving
				meta.ArchivedAt = time.Time{}
			}
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

	// Broadcast the pinned state change to all connected WebSocket clients
	if req.Pinned != nil {
		s.BroadcastSessionPinned(sessionID, *req.Pinned)
	}

	// Broadcast the archived state change to all connected WebSocket clients
	if req.Archived != nil {
		s.BroadcastSessionArchived(sessionID, *req.Archived)
	}

	// Delete all child sessions when parent is archived
	if req.Archived != nil && *req.Archived {
		if s.sessionManager != nil {
			go s.sessionManager.DeleteChildSessions(sessionID)
		}
	}

	// Handle unarchive lifecycle: restart ACP session
	if req.Archived != nil && !*req.Archived {
		if s.sessionManager != nil {
			// Resume the session to restart the ACP connection
			_, err := s.sessionManager.ResumeSession(sessionID, meta.Name, meta.WorkingDir)
			if err != nil {
				// Log the error but don't fail the request - the session is unarchived
				// The ACP will be started when the user sends a message
				if s.logger != nil {
					s.logger.Warn("Failed to resume ACP session after unarchive",
						"session_id", sessionID,
						"error", err)
				}
				// Broadcast ACP start failure to all clients
				s.BroadcastACPStartFailed(sessionID, meta.Name, err, "")
			} else {
				if s.logger != nil {
					s.logger.Info("Resumed ACP session after unarchive",
						"session_id", sessionID)
				}
				// Broadcast that ACP was started
				s.BroadcastACPStarted(sessionID)
			}
		}
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
	// Use the server's session store (owned by the server, not closed by this handler)
	store := s.Store()
	if store == nil {
		http.Error(w, "Session store not available", http.StatusInternalServerError)
		return
	}

	// Find ALL children recursively BEFORE deletion (they will be cascade-deleted by store.Delete)
	// We need their IDs to close their ACP processes and broadcast deletions
	allChildIDs, err := store.FindAllChildrenRecursive(sessionID)
	if err != nil && s.logger != nil {
		s.logger.Warn("Failed to find children for deletion",
			"session_id", sessionID,
			"error", err)
	}

	// Clean up callback index entries for this session and all children
	if s.callbackIndex != nil {
		s.callbackIndex.RemoveBySessionID(sessionID)
		for _, childID := range allChildIDs {
			s.callbackIndex.RemoveBySessionID(childID)
		}
	}

	// Close ACP processes for parent and all children
	if s.sessionManager != nil {
		s.sessionManager.CloseSession(sessionID, "deleted")
		for _, childID := range allChildIDs {
			s.sessionManager.CloseSession(childID, "parent_deleted")
		}
	}

	// Delete from store (cascade-deletes all children recursively)
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

	// Broadcast deletions to all connected WebSocket clients
	s.BroadcastSessionDeleted(sessionID)
	for _, childID := range allChildIDs {
		s.BroadcastSessionDeleted(childID)
	}

	if s.logger != nil && len(allChildIDs) > 0 {
		s.logger.Info("Deleted session with children",
			"session_id", sessionID,
			"children_deleted", len(allChildIDs))
	}

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
	ACPServer          string `json:"acp_server"`
	WorkingDir         string `json:"working_dir"`
	Name               string `json:"name,omitempty"`
	Color              string `json:"color,omitempty"`
	Code               string `json:"code,omitempty"`
	AuxiliaryACPServer string `json:"auxiliary_acp_server,omitempty"`
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

	// Validate auxiliary ACP server if provided
	if req.AuxiliaryACPServer != "" && !strings.EqualFold(req.AuxiliaryACPServer, "none") {
		if s.config.MittoConfig != nil {
			if _, err := s.config.MittoConfig.GetServer(req.AuxiliaryACPServer); err != nil {
				http.Error(w, fmt.Sprintf("Unknown auxiliary ACP server: %s", req.AuxiliaryACPServer), http.StatusBadRequest)
				return
			}
		}
	}

	// Add the workspace
	newWorkspace := config.WorkspaceSettings{
		ACPServer:          req.ACPServer,
		ACPCommand:         acpCommand,
		WorkingDir:         req.WorkingDir,
		Name:               req.Name,
		Color:              req.Color,
		Code:               req.Code,
		AuxiliaryACPServer: req.AuxiliaryACPServer,
	}
	s.sessionManager.AddWorkspace(newWorkspace)

	// Also update the server config
	s.config.Workspaces = s.sessionManager.GetWorkspaces()

	writeJSONCreated(w, newWorkspace)
}

// handleRemoveWorkspace removes a workspace by UUID.
// Supports both 'uuid' and legacy 'dir' query parameters for backwards compatibility.
func (s *Server) handleRemoveWorkspace(w http.ResponseWriter, r *http.Request) {
	uuid := r.URL.Query().Get("uuid")
	workingDir := r.URL.Query().Get("dir")

	// Find the workspace - prefer UUID, fall back to workingDir
	var ws *config.WorkspaceSettings
	if uuid != "" {
		ws = s.sessionManager.GetWorkspaceByUUID(uuid)
	} else if workingDir != "" {
		// Legacy support: find first workspace matching directory
		ws = s.sessionManager.GetWorkspace(workingDir)
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
	s.sessionManager.RemoveWorkspace(ws.UUID)

	// Also update the server config
	s.config.Workspaces = s.sessionManager.GetWorkspaces()

	writeNoContent(w)
}

// slugifyPromptName converts a prompt name to a filesystem-safe slug.
// e.g., "Add tests" → "add-tests"
func slugifyPromptName(name string) string {
	slug := strings.ToLower(name)
	var result []byte
	lastHyphen := false
	for _, c := range slug {
		if (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') {
			result = append(result, byte(c))
			lastHyphen = false
		} else if !lastHyphen {
			result = append(result, '-')
			lastHyphen = true
		}
	}
	return strings.Trim(string(result), "-")
}

// handleWorkspacePrompts handles GET/POST/DELETE /api/workspace-prompts
//
//   - GET ?dir=...                      Returns workspace prompts (backward-compat)
//   - GET ?dir=...&include_global=true  Returns builtin + workspace prompts merged, all sources
//   - POST                              Create or update a workspace prompt file
//   - DELETE ?dir=...&name=...          Delete a workspace prompt file by name
func (s *Server) handleWorkspacePrompts(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		s.handleWorkspacePromptsGET(w, r)
	case http.MethodPost:
		s.handleWorkspacePromptsPOST(w, r)
	case http.MethodDelete:
		s.handleWorkspacePromptsDELETE(w, r)
	default:
		methodNotAllowed(w)
	}
}

// handleWorkspacePromptsGET handles GET /api/workspace-prompts?dir=...
// Returns the prompts from the workspace's .mittorc file and prompts_dirs.
// Prompts are filtered by the workspace's ACP server if specified in the prompt's acps field.
// Supports conditional requests via If-Modified-Since header.
// When include_global=true, also loads builtin prompts and returns all (including disabled).
func (s *Server) handleWorkspacePromptsGET(w http.ResponseWriter, r *http.Request) {

	workingDir := r.URL.Query().Get("dir")
	if workingDir == "" {
		http.Error(w, "dir query parameter is required", http.StatusBadRequest)
		return
	}

	// Get the ACP server type for this workspace (used for filtering prompts).
	// We use the server type (not name) because prompts target types,
	// and servers with the same type share prompts (e.g., auggie-fast and auggie-smart
	// can both have type "auggie" to share prompts with acps: auggie).
	var acpServerType string
	var acpServerName string
	if ws := s.sessionManager.GetWorkspace(workingDir); ws != nil {
		acpServerName = ws.ACPServer
	} else if defaultWs := s.sessionManager.GetDefaultWorkspace(); defaultWs != nil {
		acpServerName = defaultWs.ACPServer
	}
	// Look up the server type from config (falls back to name if type is not set)
	if acpServerName != "" && s.config.MittoConfig != nil {
		acpServerType = s.config.MittoConfig.GetServerType(acpServerName)
	}
	if acpServerType == "" {
		// Fallback: use name as type if server not found in config
		acpServerType = acpServerName
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

	// When include_global=true, load builtin + workspace prompts and return all (including disabled).
	// This is used by the WorkspacesDialog to show the full list with enable/disable controls.
	includeGlobal := r.URL.Query().Get("include_global")
	if includeGlobal == "true" || includeGlobal == "1" || includeGlobal == "t" {
		s.handleWorkspacePromptsGETIncludeGlobal(w, r, workingDir)
		return
	}

	// === Load prompts from ALL sources and merge into a single list ===
	// Priority (lowest to highest):
	// 1. Global file prompts (MITTO_DIR/prompts/*.md)
	// 2. Settings file prompts (config.Prompts)
	// 3. ACP server-specific prompts (prompts with acps: field targeting this server)
	// 4. Workspace directory prompts (.mitto/prompts/*.md)
	// 5. Workspace inline prompts (.mittorc prompts section) — highest priority

	// 1. Global file prompts
	var globalFilePrompts []config.WebPrompt
	if s.config.PromptsCache != nil {
		var err error
		globalFilePrompts, err = s.config.PromptsCache.GetWebPrompts()
		if err != nil && s.logger != nil {
			s.logger.Warn("Failed to load global file prompts", "error", err)
		}
	}

	// 2. Settings file prompts
	var settingsPrompts []config.WebPrompt
	if s.config.MittoConfig != nil {
		settingsPrompts = s.config.MittoConfig.Prompts
	}

	// 3. ACP server-specific file prompts (prompts with acps: field targeting this server)
	var serverPrompts []config.WebPrompt
	if acpServerType != "" && s.config.PromptsCache != nil {
		sp, err := s.config.PromptsCache.GetWebPromptsSpecificToACP(acpServerType)
		if err != nil && s.logger != nil {
			s.logger.Warn("Failed to load ACP-specific file prompts",
				"acp_server", acpServerName, "acp_type", acpServerType, "error", err)
		}
		serverPrompts = sp
	}

	// Also include inline per-server prompts from config
	if acpServerName != "" && s.config.MittoConfig != nil {
		for _, srv := range s.config.MittoConfig.ACPServers {
			if srv.Name == acpServerName {
				serverPrompts = append(serverPrompts, srv.Prompts...)
				break
			}
		}
	}

	// 4. Workspace directory prompts (.mitto/prompts/*.md)
	var workspacePromptsDirs []string
	defaultWorkspacePromptsDir := appdir.WorkspacePromptsDir(workingDir)
	workspacePromptsDirs = append(workspacePromptsDirs, defaultWorkspacePromptsDir)
	promptsDirs := s.sessionManager.GetWorkspacePromptsDirs(workingDir)
	workspacePromptsDirs = append(workspacePromptsDirs, promptsDirs...)
	dirPrompts := s.loadPromptsFromDirs(workingDir, workspacePromptsDirs)

	// 5. Workspace inline prompts (.mittorc)
	inlinePrompts := s.sessionManager.GetWorkspacePrompts(workingDir)

	// Merge all sources. MergePrompts takes (global, settings, workspace) and filters disabled.
	// We merge in two steps: first global+settings, then server+workspace on top.
	globalMerged := config.MergePromptsKeepDisabled(globalFilePrompts, settingsPrompts, nil)
	// Server prompts override global; workspace dir prompts override server; inline overrides all.
	allWorkspace := config.MergePromptsKeepDisabled(nil, dirPrompts, inlinePrompts)
	prompts := config.MergePromptsKeepDisabled(globalMerged, serverPrompts, allWorkspace)

	// Filter out disabled prompts (workspace enabled:false suppresses same-named global prompts)
	var filtered []config.WebPrompt
	for _, p := range prompts {
		if p.Enabled == nil || *p.Enabled {
			filtered = append(filtered, p)
		}
	}
	prompts = filtered

	// Filter by enabled expressions if session context is available
	sessionID := r.URL.Query().Get("session_id")
	if sessionID != "" {
		if visCtx := s.buildPromptEnabledContext(sessionID); visCtx != nil {
			prompts = s.filterPromptsByEnabled(prompts, visCtx)
		}
	}

	if s.logger != nil {
		s.logger.Debug("Returning workspace prompts (all sources merged)",
			"working_dir", workingDir,
			"acp_server", acpServerName,
			"acp_server_type", acpServerType,
			"prompt_count", len(prompts),
			"global_file_count", len(globalFilePrompts),
			"settings_count", len(settingsPrompts),
			"server_count", len(serverPrompts),
			"dir_prompt_count", len(dirPrompts),
			"inline_prompt_count", len(inlinePrompts),
			"prompts_dirs", workspacePromptsDirs,
			"last_modified", lastModified,
			"session_id", sessionID,
			"enabled_evaluated", sessionID != "")
	}

	writeJSONOK(w, map[string]interface{}{
		"prompts":           prompts,
		"working_dir":       workingDir,
		"enabled_evaluated": sessionID != "",
	})
}

// handleWorkspacePromptsGETIncludeGlobal handles the include_global=true variant of the GET endpoint.
// It loads builtin prompts and workspace prompts, merges them (workspace overrides builtin by name),
// and returns all prompts including disabled ones (so the UI can render enable/disable toggles).
func (s *Server) handleWorkspacePromptsGETIncludeGlobal(w http.ResponseWriter, r *http.Request, workingDir string) {
	// Load builtin prompts and tag them as source="builtin"
	var builtinPrompts []config.WebPrompt
	if builtinDir, err := appdir.BuiltinPromptsDir(); err == nil {
		rawBuiltin, _ := config.LoadPromptsFromDir(builtinDir)
		for _, p := range rawBuiltin {
			wp := p.ToWebPrompt()
			wp.Source = config.PromptSourceBuiltin
			builtinPrompts = append(builtinPrompts, wp)
		}
	}

	// Load workspace prompts from .mitto/prompts/ and tag them as source="workspace"
	var workspacePrompts []config.WebPrompt
	workspacePromptsDir := appdir.WorkspacePromptsDir(workingDir)
	rawWorkspace, _ := config.LoadPromptsFromDir(workspacePromptsDir)
	for _, p := range rawWorkspace {
		wp := p.ToWebPrompt()
		wp.Source = config.PromptSourceWorkspace
		workspacePrompts = append(workspacePrompts, wp)
	}

	// Load inline prompts from .mittorc. Separate them into:
	// - disable-only entries (no prompt text, enabled=false): applied as overrides on builtins
	// - full prompts with content: treated as workspace prompts
	disableOverrides := make(map[string]bool) // prompt name → disabled
	inlinePrompts := s.sessionManager.GetWorkspacePrompts(workingDir)
	for _, p := range inlinePrompts {
		isDisableOnly := p.Prompt == "" && p.Enabled != nil && !*p.Enabled
		if isDisableOnly {
			disableOverrides[p.Name] = true
		} else {
			p.Source = config.PromptSourceWorkspace
			workspacePrompts = append(workspacePrompts, p)
		}
	}

	// Merge: workspace overrides builtin by name.
	// Unlike MergePrompts, we do NOT filter out disabled prompts — the UI needs to see them.
	seen := make(map[string]bool)
	var merged []config.WebPrompt
	for _, p := range workspacePrompts {
		if p.Name != "" && !seen[p.Name] {
			merged = append(merged, p)
			seen[p.Name] = true
		}
	}
	for _, p := range builtinPrompts {
		if p.Name != "" && !seen[p.Name] {
			// Apply disable-only overrides from .mittorc: keep builtin source/content
			// but mark as disabled so the UI shows the toggle correctly.
			if disableOverrides[p.Name] {
				f := false
				p.Enabled = &f
			}
			merged = append(merged, p)
			seen[p.Name] = true
		}
	}

	if s.logger != nil {
		s.logger.Debug("Returning workspace prompts (include_global)",
			"working_dir", workingDir,
			"builtin_count", len(builtinPrompts),
			"workspace_count", len(workspacePrompts),
			"merged_count", len(merged))
	}

	writeJSONOK(w, map[string]interface{}{
		"prompts":     merged,
		"working_dir": workingDir,
	})
}

// handleWorkspacePromptsPOST handles POST /api/workspace-prompts
// Creates or updates a workspace prompt file in .mitto/prompts/<slug>.md.
func (s *Server) handleWorkspacePromptsPOST(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Dir             string `json:"dir"`
		Name            string `json:"name"`
		Prompt          string `json:"prompt"`
		Description     string `json:"description"`
		BackgroundColor string `json:"backgroundColor"`
		Group           string `json:"group"`
		Enabled         *bool  `json:"enabled"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid JSON body: "+err.Error(), http.StatusBadRequest)
		return
	}
	if req.Dir == "" {
		http.Error(w, "dir is required", http.StatusBadRequest)
		return
	}
	if req.Name == "" {
		http.Error(w, "name is required", http.StatusBadRequest)
		return
	}

	// Create the prompts directory if needed
	promptsDir := appdir.WorkspacePromptsDir(req.Dir)
	if err := os.MkdirAll(promptsDir, 0o755); err != nil {
		http.Error(w, "failed to create prompts directory: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// Build the YAML front-matter
	var frontMatter strings.Builder
	frontMatter.WriteString("---\n")
	frontMatter.WriteString(fmt.Sprintf("name: %q\n", req.Name))
	if req.Description != "" {
		frontMatter.WriteString(fmt.Sprintf("description: %q\n", req.Description))
	}
	if req.BackgroundColor != "" {
		frontMatter.WriteString(fmt.Sprintf("backgroundColor: %q\n", req.BackgroundColor))
	}
	if req.Group != "" {
		frontMatter.WriteString(fmt.Sprintf("group: %q\n", req.Group))
	}
	// Only include enabled in front-matter when explicitly false
	if req.Enabled != nil && !*req.Enabled {
		frontMatter.WriteString("enabled: false\n")
	}
	frontMatter.WriteString("---\n")

	content := frontMatter.String() + req.Prompt

	slug := slugifyPromptName(req.Name)
	if slug == "" {
		slug = "prompt"
	}
	filePath := filepath.Join(promptsDir, slug+".md")
	if err := os.WriteFile(filePath, []byte(content), 0o644); err != nil {
		http.Error(w, "failed to write prompt file: "+err.Error(), http.StatusInternalServerError)
		return
	}

	if s.logger != nil {
		s.logger.Debug("Created workspace prompt file", "path", filePath, "name", req.Name)
	}
	writeJSONOK(w, map[string]interface{}{"ok": true, "path": filePath})
}

// handleWorkspacePromptsDELETE handles DELETE /api/workspace-prompts?dir=...&name=...
// Finds and deletes a workspace prompt file by name from .mitto/prompts/.
func (s *Server) handleWorkspacePromptsDELETE(w http.ResponseWriter, r *http.Request) {
	workingDir := r.URL.Query().Get("dir")
	promptName := r.URL.Query().Get("name")
	if workingDir == "" {
		http.Error(w, "dir query parameter is required", http.StatusBadRequest)
		return
	}
	if promptName == "" {
		http.Error(w, "name query parameter is required", http.StatusBadRequest)
		return
	}

	promptsDir := appdir.WorkspacePromptsDir(workingDir)
	rawPrompts, err := config.LoadPromptsFromDir(promptsDir)
	if err != nil {
		http.Error(w, "failed to read prompts directory: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// Find the prompt by name
	var targetPath string
	for _, p := range rawPrompts {
		if strings.EqualFold(p.Name, promptName) {
			targetPath = filepath.Join(promptsDir, p.Path)
			break
		}
	}
	if targetPath == "" {
		http.Error(w, "prompt not found: "+promptName, http.StatusNotFound)
		return
	}

	if err := os.Remove(targetPath); err != nil {
		http.Error(w, "failed to delete prompt file: "+err.Error(), http.StatusInternalServerError)
		return
	}

	if s.logger != nil {
		s.logger.Debug("Deleted workspace prompt file", "path", targetPath, "name", promptName)
	}
	writeJSONOK(w, map[string]interface{}{"ok": true})
}

// handleWorkspacePromptsToggleEnabled handles PUT /api/workspace-prompts/toggle-enabled.
// If the prompt file exists in .mitto/prompts/, updates the enabled field in its frontmatter.
// Otherwise, records the enabled state in the workspace .mittorc file.
func (s *Server) handleWorkspacePromptsToggleEnabled(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPut {
		methodNotAllowed(w)
		return
	}

	var req struct {
		Dir     string `json:"dir"`
		Name    string `json:"name"`
		Enabled bool   `json:"enabled"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid JSON: "+err.Error(), http.StatusBadRequest)
		return
	}
	if req.Dir == "" {
		http.Error(w, "dir is required", http.StatusBadRequest)
		return
	}
	if req.Name == "" {
		http.Error(w, "name is required", http.StatusBadRequest)
		return
	}

	// Check if a dedicated prompt file exists in .mitto/prompts/
	slug := slugifyPromptName(req.Name)
	promptsDir := appdir.WorkspacePromptsDir(req.Dir)
	filePath := filepath.Join(promptsDir, slug+".md")

	if _, err := os.Stat(filePath); err == nil {
		// File exists — update its frontmatter
		if err := config.UpdatePromptFileEnabled(filePath, req.Enabled); err != nil {
			http.Error(w, "failed to update prompt file: "+err.Error(), http.StatusInternalServerError)
			return
		}
		if s.logger != nil {
			s.logger.Debug("Updated prompt file enabled state", "path", filePath, "enabled", req.Enabled)
		}
	} else {
		// File doesn't exist — record in .mittorc
		if err := config.SaveWorkspaceRCPromptEnabled(req.Dir, req.Name, req.Enabled); err != nil {
			http.Error(w, "failed to update workspace config: "+err.Error(), http.StatusInternalServerError)
			return
		}
		if s.logger != nil {
			s.logger.Debug("Updated .mittorc prompt enabled state", "dir", req.Dir, "name", req.Name, "enabled", req.Enabled)
		}
	}

	writeJSONOK(w, map[string]interface{}{"ok": true})
}

// buildPromptEnabledContext creates a CEL evaluation context for the given session.
// Returns nil if session doesn't exist or context cannot be built.
func (s *Server) buildPromptEnabledContext(sessionID string) *config.PromptEnabledContext {
	store := s.Store()
	if store == nil || sessionID == "" {
		return nil
	}

	meta, err := store.GetMetadata(sessionID)
	if err != nil {
		return nil
	}

	ctx := &config.PromptEnabledContext{}

	// Session context
	ctx.Session.ID = meta.SessionID
	ctx.Session.Name = meta.Name
	ctx.Session.IsChild = meta.ParentSessionID != ""
	ctx.Session.IsAutoChild = meta.IsAutoChild
	ctx.Session.ParentID = meta.ParentSessionID

	// Parent context (if this is a child)
	if meta.ParentSessionID != "" {
		parentMeta, err := store.GetMetadata(meta.ParentSessionID)
		if err == nil {
			ctx.Parent.Exists = true
			ctx.Parent.Name = parentMeta.Name
			ctx.Parent.ACPServer = parentMeta.ACPServer
		}
	}

	// Children context
	children, err := store.ListChildSessions(sessionID)
	if err == nil {
		ctx.Children.Count = len(children)
		ctx.Children.Exists = len(children) > 0
		for _, child := range children {
			ctx.Children.Names = append(ctx.Children.Names, child.Name)
			ctx.Children.ACPServers = append(ctx.Children.ACPServers, child.ACPServer)
		}
	}

	// ACP context from session metadata
	ctx.ACP.Name = meta.ACPServer
	if s.config.MittoConfig != nil {
		if srv, err := s.config.MittoConfig.GetServer(meta.ACPServer); err == nil {
			ctx.ACP.Type = srv.GetType()
			ctx.ACP.Tags = srv.Tags
			ctx.ACP.AutoApprove = srv.AutoApprove
		}
	}

	// Workspace context
	ctx.Workspace.Folder = meta.WorkingDir
	if ws := s.sessionManager.GetWorkspace(meta.WorkingDir); ws != nil {
		ctx.Workspace.UUID = ws.UUID
		ctx.Workspace.Name = ws.Name
	}
	// Check if workspace has user data schema
	if schema := s.sessionManager.GetUserDataSchema(meta.WorkingDir); schema != nil && len(schema.Fields) > 0 {
		ctx.Workspace.HasUserDataSchema = true
	}

	// Tools context - get from auxiliary manager if available
	// (This may be empty if tools haven't been fetched yet)
	if s.auxiliaryManager != nil && ctx.Workspace.UUID != "" {
		if tools, ok := s.auxiliaryManager.GetCachedMCPTools(ctx.Workspace.UUID); ok {
			ctx.Tools.Available = true
			for _, tool := range tools {
				ctx.Tools.Names = append(ctx.Tools.Names, tool.Name)
			}
		}
	}

	// Permissions context - resolve flags with defaults
	ctx.Permissions.CanDoIntrospection = session.GetFlagValue(meta.AdvancedSettings, session.FlagCanDoIntrospection)
	ctx.Permissions.CanSendPrompt = session.GetFlagValue(meta.AdvancedSettings, session.FlagCanSendPrompt)
	ctx.Permissions.CanPromptUser = session.GetFlagValue(meta.AdvancedSettings, session.FlagCanPromptUser)
	ctx.Permissions.CanStartConversation = session.GetFlagValue(meta.AdvancedSettings, session.FlagCanStartConversation)
	ctx.Permissions.CanInteractOtherWorkspaces = session.GetFlagValue(meta.AdvancedSettings, session.FlagCanInteractOtherWorkspaces)
	ctx.Permissions.AutoApprovePermissions = session.GetFlagValue(meta.AdvancedSettings, session.FlagAutoApprovePermissions)

	return ctx
}

// filterPromptsByEnabled filters prompts using enabledWhen CEL expressions.
// Prompts without an expression are always included.
// Fail-open behavior: prompts with invalid or unevaluable CEL expressions are included.
func (s *Server) filterPromptsByEnabled(prompts []config.WebPrompt, ctx *config.PromptEnabledContext) []config.WebPrompt {
	if ctx == nil {
		return prompts // No context, return all prompts
	}

	evaluator := config.GetCELEvaluator()
	if evaluator == nil {
		if s.logger != nil {
			s.logger.Warn("CEL evaluator not available, returning all prompts")
		}
		return prompts
	}

	var filtered []config.WebPrompt
	for _, p := range prompts {
		// --- enabledWhen CEL check ---
		if p.EnabledWhen == "" {
			// No expression — always include
			filtered = append(filtered, p)
			continue
		}

		// Compile the expression (cached)
		compiled, err := evaluator.Compile(p.EnabledWhen)
		if err != nil {
			// Invalid expression - include prompt (fail-open)
			if s.logger != nil {
				s.logger.Warn("Invalid enabledWhen expression",
					"prompt", p.Name,
					"expression", p.EnabledWhen,
					"error", err)
			}
			filtered = append(filtered, p)
			continue
		}

		// Evaluate the expression
		visible, err := evaluator.Evaluate(compiled, ctx)
		if err != nil {
			// Evaluation error - include prompt (fail-open)
			if s.logger != nil {
				s.logger.Warn("Failed to evaluate enabledWhen",
					"prompt", p.Name,
					"expression", p.EnabledWhen,
					"error", err)
			}
			filtered = append(filtered, p)
			continue
		}

		if visible {
			filtered = append(filtered, p)
		} else if s.logger != nil {
			s.logger.Debug("Prompt hidden by enabledWhen expression",
				"prompt", p.Name,
				"expression", p.EnabledWhen)
		}
	}

	return filtered
}

// loadPromptsFromDirs loads prompts from a list of directories.
// Relative paths are resolved against workspaceRoot.
// Non-existent directories are silently ignored.
// CEL filtering is handled later by filterPromptsByEnabled.
func (s *Server) loadPromptsFromDirs(workspaceRoot string, dirs []string) []config.WebPrompt {
	var allPrompts []config.WebPrompt

	for _, dir := range dirs {
		// Resolve relative paths
		absDir := dir
		if !filepath.IsAbs(dir) {
			absDir = filepath.Join(workspaceRoot, dir)
		}

		// Load prompts from this directory (silently ignore errors)
		prompts, err := config.LoadPromptsFromDir(absDir)
		if err != nil {
			if s.logger != nil {
				s.logger.Debug("Failed to load prompts from directory",
					"dir", absDir,
					"error", err)
			}
			continue
		}

		// Convert to WebPrompts and merge (later dirs override earlier)
		webPrompts := config.PromptsToWebPrompts(prompts)
		allPrompts = config.MergePrompts(nil, allPrompts, webPrompts)
	}

	return allPrompts
}

// handleWorkspaceDetail dispatches /api/workspaces/{uuid}/... sub-routes.
func (s *Server) handleWorkspaceDetail(w http.ResponseWriter, r *http.Request) {
	// Extract the path after "/api/workspaces/", stripping apiPrefix first (mirrors handleSessionDetail).
	path := r.URL.Path
	path = strings.TrimPrefix(path, s.apiPrefix)
	path = strings.TrimPrefix(path, "/api/workspaces/")

	parts := strings.SplitN(path, "/", 2)
	if len(parts) < 2 {
		http.NotFound(w, r)
		return
	}
	uuid := parts[0]
	subPath := parts[1]

	switch subPath {
	case "effective-runner-config":
		s.handleEffectiveRunnerConfig(w, r, uuid)
	default:
		http.NotFound(w, r)
	}
}

// EffectiveRunnerConfigResponse is the response for GET /api/workspaces/{uuid}/effective-runner-config.
// It returns the resolved runner config from global + agent levels (no workspace overrides),
// so the UI can show what restrictions a workspace would inherit.
type EffectiveRunnerConfigResponse struct {
	RunnerType   string                     `json:"runner_type"`
	Restrictions *config.RunnerRestrictions `json:"restrictions,omitempty"`
}

// handleEffectiveRunnerConfig handles GET /api/workspaces/{uuid}/effective-runner-config.
// Returns the effective runner config resolved from global and agent levels only.
func (s *Server) handleEffectiveRunnerConfig(w http.ResponseWriter, r *http.Request, uuid string) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w)
		return
	}

	ws := s.sessionManager.GetWorkspaceByUUID(uuid)
	if ws == nil {
		http.Error(w, "Workspace not found", http.StatusNotFound)
		return
	}

	// Get global runner configs
	sm := s.sessionManager
	sm.mu.RLock()
	globalRunnersByType := sm.globalRestrictedRunners
	mittoConfig := sm.mittoConfig
	sm.mu.RUnlock()

	// Get agent-specific runner configs
	var agentRunnersByType map[string]*config.WorkspaceRunnerConfig
	if mittoConfig != nil && ws.ACPServer != "" {
		if server, err := mittoConfig.GetServer(ws.ACPServer); err == nil && server != nil {
			agentRunnersByType = server.RestrictedRunners
		}
	}

	// Resolve global + agent levels only (no workspace level)
	resolved := runner.ResolveEffectiveConfig(globalRunnersByType, agentRunnersByType)

	resp := EffectiveRunnerConfigResponse{
		RunnerType:   resolved.Type,
		Restrictions: resolved.Restrictions,
	}

	writeJSONOK(w, resp)
}

// handleWorkspaceMetadata dispatches GET and PUT requests for workspace metadata.
func (s *Server) handleWorkspaceMetadata(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		s.handleWorkspaceMetadataGet(w, r)
	case http.MethodPut:
		s.handleWorkspaceMetadataPut(w, r)
	default:
		methodNotAllowed(w)
	}
}

// handleWorkspaceMetadataGet handles GET /api/workspace-metadata?working_dir=...
// Returns workspace metadata (description, URL) from the .mittorc file.
func (s *Server) handleWorkspaceMetadataGet(w http.ResponseWriter, r *http.Request) {
	workingDir := r.URL.Query().Get("working_dir")
	if workingDir == "" {
		http.Error(w, "working_dir query parameter is required", http.StatusBadRequest)
		return
	}

	workingDir = strings.TrimSpace(workingDir)

	// Validate that this is a known workspace
	workspace := s.sessionManager.GetWorkspace(workingDir)
	if workspace == nil {
		http.Error(w, "Unknown workspace", http.StatusNotFound)
		return
	}

	// Load workspace RC file
	rc, err := config.LoadWorkspaceRC(workingDir)
	if err != nil {
		// Log error but return empty metadata
		if s.logger != nil {
			s.logger.Warn("Failed to load workspace RC for metadata", "working_dir", workingDir, "error", err)
		}
		writeJSONOK(w, map[string]interface{}{})
		return
	}

	if rc == nil || rc.Metadata == nil {
		writeJSONOK(w, map[string]interface{}{})
		return
	}

	writeJSONOK(w, rc.Metadata)
}

// handleWorkspaceMetadataPut handles PUT /api/workspace-metadata.
// Saves description and URL to the workspace .mittorc file.
func (s *Server) handleWorkspaceMetadataPut(w http.ResponseWriter, r *http.Request) {
	var req struct {
		WorkingDir  string `json:"working_dir"`
		Description string `json:"description"`
		URL         string `json:"url"`
		Group       string `json:"group"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}
	if req.WorkingDir == "" {
		http.Error(w, "working_dir is required", http.StatusBadRequest)
		return
	}
	req.WorkingDir = strings.TrimSpace(req.WorkingDir)

	// Validate that this is a known workspace
	workspace := s.sessionManager.GetWorkspace(req.WorkingDir)
	if workspace == nil {
		http.Error(w, "Unknown workspace", http.StatusNotFound)
		return
	}

	if err := config.SaveWorkspaceMetadata(req.WorkingDir, req.Description, req.URL, req.Group); err != nil {
		if s.logger != nil {
			s.logger.Error("Failed to save workspace metadata", "working_dir", req.WorkingDir, "error", err)
		}
		http.Error(w, "Failed to save metadata: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// Invalidate the workspace RC cache so subsequent reads pick up the new data
	if s.sessionManager != nil {
		s.sessionManager.InvalidateWorkspaceRC(req.WorkingDir)
	}

	if s.logger != nil {
		s.logger.Info("Workspace metadata saved", "working_dir", req.WorkingDir)
	}

	writeJSONOK(w, map[string]string{"status": "ok"})
}

// WebProcessor represents a processor as returned by the workspace processors API.
type WebProcessor struct {
	Name        string                     `json:"name"`
	Description string                     `json:"description,omitempty"`
	Enabled     bool                       `json:"enabled"`
	Source      processors.ProcessorSource `json:"source"`
	When        string                     `json:"when,omitempty"`
	Priority    int                        `json:"priority,omitempty"`
	FilePath    string                     `json:"file_path,omitempty"`
	Mode        string                     `json:"mode,omitempty"` // "text", "command", or "prompt"
}

// handleWorkspaceProcessors handles GET /api/workspace-processors?dir=...
// Returns all processors applicable to the workspace (global + workspace-local),
// with enabled state reflecting any .mittorc overrides.
func (s *Server) handleWorkspaceProcessors(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w)
		return
	}

	workingDir := r.URL.Query().Get("dir")
	if workingDir == "" {
		http.Error(w, "dir query parameter is required", http.StatusBadRequest)
		return
	}

	// Get merged processor manager (global + workspace processors)
	procMgr := s.sessionManager.GetWorkspaceProcessorManager(workingDir)
	if procMgr == nil {
		writeJSONOK(w, map[string]interface{}{"processors": []WebProcessor{}, "working_dir": workingDir})
		return
	}

	// Build override map from workspace .mittorc processors section.
	// Mirrors the prompts pattern: [{name, enabled}] entries override processor defaults.
	overrides := make(map[string]bool) // name → enabled
	for _, o := range s.sessionManager.GetWorkspaceProcessorOverrides(workingDir) {
		if o.Enabled != nil {
			overrides[o.Name] = *o.Enabled
		}
	}

	// Build response list
	var result []WebProcessor
	for _, p := range procMgr.Processors() {
		// Skip config (text-mode) processors — they are not file-based and can't be toggled
		if p.Source == processors.ProcessorSourceConfig {
			continue
		}
		enabled := p.Enabled == nil || *p.Enabled
		// Apply workspace-level override from .mittorc processors section
		if override, ok := overrides[p.Name]; ok {
			enabled = override
		}
		mode := "command"
		if p.IsTextMode() {
			mode = "text"
		} else if p.IsPromptMode() {
			mode = "prompt"
		}
		result = append(result, WebProcessor{
			Name:        p.Name,
			Description: p.Description,
			Enabled:     enabled,
			Source:      p.Source,
			When:        string(p.When),
			Priority:    p.Priority,
			FilePath:    p.FilePath,
			Mode:        mode,
		})
	}

	// Sort: workspace processors first, then global, then by name within each group
	sort.Slice(result, func(i, j int) bool {
		si, sj := sourceOrder(result[i].Source), sourceOrder(result[j].Source)
		if si != sj {
			return si < sj
		}
		return result[i].Name < result[j].Name
	})

	if s.logger != nil {
		s.logger.Debug("Returning workspace processors",
			"working_dir", workingDir,
			"count", len(result))
	}

	writeJSONOK(w, map[string]interface{}{
		"processors":  result,
		"working_dir": workingDir,
	})
}

// sourceOrder returns a sort priority for processor sources (lower = shown first).
func sourceOrder(src processors.ProcessorSource) int {
	switch src {
	case processors.ProcessorSourceWorkspace:
		return 0
	case processors.ProcessorSourceGlobal:
		return 1
	case processors.ProcessorSourceBuiltin:
		return 2
	default:
		return 3
	}
}

// handleWorkspaceProcessorsToggleEnabled handles PUT /api/workspace-processors/toggle-enabled.
// If the processor YAML file is writable (workspace-local), updates enabled in-place.
// Otherwise, records the disabled state in the workspace .mittorc file.
func (s *Server) handleWorkspaceProcessorsToggleEnabled(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPut {
		methodNotAllowed(w)
		return
	}

	var req struct {
		Dir     string `json:"dir"`
		Name    string `json:"name"`
		Enabled bool   `json:"enabled"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid JSON: "+err.Error(), http.StatusBadRequest)
		return
	}
	if req.Dir == "" {
		http.Error(w, "dir is required", http.StatusBadRequest)
		return
	}
	if req.Name == "" {
		http.Error(w, "name is required", http.StatusBadRequest)
		return
	}

	// Try to find the processor YAML file in the workspace processor directories
	workspaceProcessorDirs := s.sessionManager.GetWorkspaceAllProcessorDirs(req.Dir)
	var filePath string
	for _, dir := range workspaceProcessorDirs {
		for _, ext := range []string{".yaml", ".yml"} {
			candidate := filepath.Join(dir, req.Name+ext)
			if _, err := os.Stat(candidate); err == nil {
				filePath = candidate
				break
			}
		}
		if filePath != "" {
			break
		}
	}

	if filePath != "" {
		// File exists in a workspace directory — update it in-place
		if err := processors.UpdateProcessorFileEnabled(filePath, req.Enabled); err != nil {
			http.Error(w, "failed to update processor file: "+err.Error(), http.StatusInternalServerError)
			return
		}
		if s.logger != nil {
			s.logger.Debug("Updated processor file enabled state", "path", filePath, "enabled", req.Enabled)
		}
	} else {
		// Global/builtin processor — record in .mittorc processors section
		if err := config.SaveWorkspaceRCProcessorEnabled(req.Dir, req.Name, req.Enabled); err != nil {
			http.Error(w, "failed to update workspace config: "+err.Error(), http.StatusInternalServerError)
			return
		}
		// Invalidate cache so the next read picks up the change
		if s.sessionManager != nil {
			s.sessionManager.InvalidateWorkspaceRC(req.Dir)
		}
		if s.logger != nil {
			s.logger.Debug("Updated .mittorc processor enabled state",
				"dir", req.Dir, "name", req.Name, "enabled", req.Enabled)
		}
	}

	writeJSONOK(w, map[string]interface{}{"ok": true})
}
