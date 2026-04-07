package web

import (
	"fmt"
	"net/http"
)

// configValidationError represents a validation error with HTTP status code.
type configValidationError struct {
	StatusCode int
	Message    string
	Details    map[string]interface{}
}

func (e *configValidationError) Error() string {
	return e.Message
}

// writeConfigError writes a JSON error response for config validation errors.
func (s *Server) writeConfigError(w http.ResponseWriter, err *configValidationError) {
	if err.Details != nil {
		writeJSON(w, err.StatusCode, err.Details)
	} else {
		writeJSON(w, err.StatusCode, map[string]string{"error": err.Message})
	}
}

// hasExistingSimpleAuth returns true if the server already has simple auth configured
// with a non-empty password (either in config or in secure storage).
func (s *Server) hasExistingSimpleAuth() bool {
	if s.config.MittoConfig == nil || s.config.MittoConfig.Web.Auth == nil || s.config.MittoConfig.Web.Auth.Simple == nil {
		return false
	}
	return s.config.MittoConfig.Web.Auth.Simple.Username != ""
}

// validateConfigRequest validates the basic structure of a ConfigSaveRequest.
// Returns nil if valid, or a configValidationError if invalid.
func (s *Server) validateConfigRequest(req *ConfigSaveRequest) *configValidationError {
	// Validate: at least one workspace
	if len(req.Workspaces) == 0 {
		return &configValidationError{
			StatusCode: http.StatusBadRequest,
			Message:    "At least one workspace is required",
		}
	}

	// Validate: at least one ACP server
	if len(req.ACPServers) == 0 {
		return &configValidationError{
			StatusCode: http.StatusBadRequest,
			Message:    "At least one ACP server is required",
		}
	}

	// Validate ACP servers
	acpServerNames := make(map[string]bool)
	for _, srv := range req.ACPServers {
		if srv.Name == "" {
			return &configValidationError{
				StatusCode: http.StatusBadRequest,
				Message:    "ACP server name cannot be empty",
			}
		}
		if srv.Command == "" {
			return &configValidationError{
				StatusCode: http.StatusBadRequest,
				Message:    fmt.Sprintf("ACP server '%s' command cannot be empty", srv.Name),
			}
		}
		if acpServerNames[srv.Name] {
			return &configValidationError{
				StatusCode: http.StatusBadRequest,
				Message:    fmt.Sprintf("Duplicate ACP server name: %s", srv.Name),
			}
		}
		acpServerNames[srv.Name] = true
	}

	// Validate workspaces reference valid ACP servers
	for _, ws := range req.Workspaces {
		if ws.WorkingDir == "" {
			return &configValidationError{
				StatusCode: http.StatusBadRequest,
				Message:    "Workspace path cannot be empty",
			}
		}
		if !acpServerNames[ws.ACPServer] {
			return &configValidationError{
				StatusCode: http.StatusBadRequest,
				Message:    fmt.Sprintf("Workspace '%s' references unknown ACP server: %s", ws.WorkingDir, ws.ACPServer),
			}
		}
	}

	// Validate auth settings
	if req.Web.Auth != nil && req.Web.Auth.Simple != nil {
		if errMsg := ValidateUsername(req.Web.Auth.Simple.Username); errMsg != "" {
			return &configValidationError{
				StatusCode: http.StatusBadRequest,
				Message:    errMsg,
			}
		}
		// Skip password validation when the password is empty and auth is already configured.
		// The frontend sends an empty password when the user hasn't changed it (the backend
		// sanitizes the password before sending config to the client for security).
		// In this case, the existing password will be preserved in buildNewSettings.
		if req.Web.Auth.Simple.Password != "" || !s.hasExistingSimpleAuth() {
			if errMsg := ValidatePassword(req.Web.Auth.Simple.Password); errMsg != "" {
				return &configValidationError{
					StatusCode: http.StatusBadRequest,
					Message:    errMsg,
				}
			}
		}
	}

	return nil
}

// checkWorkspaceConflicts checks if any workspaces being removed have conversations.
// Returns nil if no conflicts, or a configValidationError if a workspace is in use.
func (s *Server) checkWorkspaceConflicts(req *ConfigSaveRequest) *configValidationError {
	currentWorkspaces := s.sessionManager.GetWorkspaces()

	// Build set of new workspace directories
	newWorkspaceDirs := make(map[string]bool)
	for _, ws := range req.Workspaces {
		newWorkspaceDirs[ws.WorkingDir] = true
	}

	// Find workspaces being removed
	var removedWorkspaces []string
	for _, ws := range currentWorkspaces {
		if !newWorkspaceDirs[ws.WorkingDir] {
			removedWorkspaces = append(removedWorkspaces, ws.WorkingDir)
		}
	}

	if len(removedWorkspaces) == 0 {
		return nil
	}

	// Check if any removed workspaces have conversations
	// Use the server's session store (owned by the server, not closed by this handler)
	store := s.Store()
	if store == nil {
		return &configValidationError{
			StatusCode: http.StatusInternalServerError,
			Message:    "Session store not available",
		}
	}

	sessions, err := store.List()
	if err != nil {
		if s.logger != nil {
			s.logger.Error("Failed to list sessions", "error", err)
		}
		return &configValidationError{
			StatusCode: http.StatusInternalServerError,
			Message:    "Failed to check workspace usage",
		}
	}

	// Check each removed workspace for conversations
	for _, removedDir := range removedWorkspaces {
		var conversationCount int
		for _, sess := range sessions {
			if sess.WorkingDir == removedDir {
				conversationCount++
			}
		}
		if conversationCount > 0 {
			return &configValidationError{
				StatusCode: http.StatusConflict,
				Message:    fmt.Sprintf("Cannot remove workspace '%s': %d conversation(s) are using it", removedDir, conversationCount),
				Details: map[string]interface{}{
					"error":              "workspace_in_use",
					"message":            fmt.Sprintf("Cannot remove workspace '%s': %d conversation(s) are using it", removedDir, conversationCount),
					"workspace":          removedDir,
					"conversation_count": conversationCount,
				},
			}
		}
	}

	return nil
}
