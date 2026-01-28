package web

import (
	"encoding/json"
	"fmt"
	"net/http"

	configPkg "github.com/inercia/mitto/internal/config"
	"github.com/inercia/mitto/internal/session"
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
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(err.StatusCode)
	if err.Details != nil {
		json.NewEncoder(w).Encode(err.Details)
	} else {
		json.NewEncoder(w).Encode(map[string]string{"error": err.Message})
	}
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
		if errMsg := ValidatePassword(req.Web.Auth.Simple.Password); errMsg != "" {
			return &configValidationError{
				StatusCode: http.StatusBadRequest,
				Message:    errMsg,
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
	store, err := session.DefaultStore()
	if err != nil {
		if s.logger != nil {
			s.logger.Error("Failed to create session store", "error", err)
		}
		return &configValidationError{
			StatusCode: http.StatusInternalServerError,
			Message:    "Failed to check workspace usage",
		}
	}
	defer store.Close()

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

// buildNewSettings builds the new settings from a ConfigSaveRequest.
func (s *Server) buildNewSettings(req *ConfigSaveRequest) *configPkg.Settings {
	// Build new ACP servers (including per-server prompts)
	newACPServers := make([]configPkg.ACPServerSettings, len(req.ACPServers))
	for i, srv := range req.ACPServers {
		newACPServers[i] = configPkg.ACPServerSettings{
			Name:    srv.Name,
			Command: srv.Command,
			Prompts: srv.Prompts,
		}
	}

	// Build new web config (preserve existing settings, update auth and host)
	newWebConfig := configPkg.WebConfig{}
	if s.config.MittoConfig != nil {
		newWebConfig = s.config.MittoConfig.Web
	}

	// Update host setting if provided
	if req.Web.Host != "" {
		newWebConfig.Host = req.Web.Host
	}

	// Update external port setting (0 means random)
	newWebConfig.ExternalPort = req.Web.ExternalPort

	// Update auth settings
	if req.Web.Auth != nil && req.Web.Auth.Simple != nil {
		newWebConfig.Auth = &configPkg.WebAuth{
			Simple: &configPkg.SimpleAuth{
				Username: req.Web.Auth.Simple.Username,
				Password: req.Web.Auth.Simple.Password,
			},
		}
	} else {
		newWebConfig.Auth = nil
	}

	// Update prompts
	newWebConfig.Prompts = req.Web.Prompts

	// Update hooks
	if req.Web.Hooks != nil {
		newWebConfig.Hooks = *req.Web.Hooks
	} else {
		// Clear hooks if not provided
		newWebConfig.Hooks = configPkg.WebHooks{}
	}

	// Build UI config - preserve existing settings, update from request if provided
	newUIConfig := configPkg.UIConfig{}
	if s.config.MittoConfig != nil {
		newUIConfig = s.config.MittoConfig.UI
	}
	// If UI config provided in request, use it (overrides existing)
	if req.UI != nil {
		newUIConfig = *req.UI
	}

	return &configPkg.Settings{
		ACPServers: newACPServers,
		Web:        newWebConfig,
		UI:         newUIConfig,
	}
}

// applyConfigChanges applies the new configuration to the running server.
func (s *Server) applyConfigChanges(req *ConfigSaveRequest, settings *configPkg.Settings) {
	// Build ACP server list for internal config (including per-server prompts)
	newACPServers := make([]configPkg.ACPServer, len(settings.ACPServers))
	for i, srv := range settings.ACPServers {
		newACPServers[i] = configPkg.ACPServer{
			Name:    srv.Name,
			Command: srv.Command,
			Prompts: srv.Prompts,
		}
	}

	// Determine if external access is being enabled or disabled
	oldAuthEnabled := s.config.MittoConfig != nil &&
		s.config.MittoConfig.Web.Auth != nil &&
		s.config.MittoConfig.Web.Auth.Simple != nil
	newAuthEnabled := settings.Web.Auth != nil && settings.Web.Auth.Simple != nil

	// Update ACP servers, web config, and UI config
	if s.config.MittoConfig != nil {
		s.config.MittoConfig.ACPServers = newACPServers
		s.config.MittoConfig.Web = settings.Web
		s.config.MittoConfig.UI = settings.UI
	}

	// Update workspaces - need to resolve ACP commands
	acpCommandMap := make(map[string]string)
	for _, srv := range newACPServers {
		acpCommandMap[srv.Name] = srv.Command
	}

	newWorkspaces := make([]configPkg.WorkspaceSettings, len(req.Workspaces))
	for i, ws := range req.Workspaces {
		newWorkspaces[i] = configPkg.WorkspaceSettings{
			ACPServer:  ws.ACPServer,
			ACPCommand: acpCommandMap[ws.ACPServer],
			WorkingDir: ws.WorkingDir,
			Color:      ws.Color,
		}
	}
	s.sessionManager.SetWorkspaces(newWorkspaces)
	s.config.Workspaces = newWorkspaces

	// Update external port setting on the server (before applying auth changes)
	// If the new setting is 0 (random) and we already have a running external listener,
	// preserve the current port to avoid generating a new random port on every save.
	newExternalPort := settings.Web.ExternalPort
	if newExternalPort == 0 && s.IsExternalListenerRunning() {
		// Keep the current port when re-saving with "random" port while already running
		newExternalPort = s.GetExternalPort()
	}
	s.SetExternalPort(newExternalPort)

	// Handle auth manager and external listener changes
	s.applyAuthChanges(oldAuthEnabled, newAuthEnabled, settings.Web.Auth)

	if s.logger != nil {
		s.logger.Info("Configuration saved",
			"workspaces", len(newWorkspaces),
			"acp_servers", len(newACPServers),
			"auth_enabled", newAuthEnabled,
			"external_listener", s.IsExternalListenerRunning())
	}
}

// applyAuthChanges handles dynamic changes to authentication and external access.
func (s *Server) applyAuthChanges(oldAuthEnabled, newAuthEnabled bool, newAuthConfig *configPkg.WebAuth) {
	// Case 1: Auth was disabled, now enabled -> create auth manager and start external listener
	if !oldAuthEnabled && newAuthEnabled {
		// Create new auth manager if it doesn't exist
		if s.authManager == nil {
			s.authManager = NewAuthManager(newAuthConfig)
			if s.logger != nil {
				s.logger.Info("Authentication enabled dynamically")
			}
		} else {
			s.authManager.UpdateConfig(newAuthConfig)
		}

		// Start external listener if not already running
		if !s.IsExternalListenerRunning() {
			// Use configured external port: -1 = disabled, 0 = random, >0 = specific port
			port := s.GetExternalPort()
			if port == 0 && s.config.MittoConfig != nil && s.config.MittoConfig.Web.ExternalPort > 0 {
				port = s.config.MittoConfig.Web.ExternalPort
			}
			// Only start if port is >= 0 (port 0 = random, port > 0 = specific)
			// Port -1 means disabled
			if port >= 0 {
				actualPort, err := s.StartExternalListener(port)
				if err != nil {
					if s.logger != nil {
						s.logger.Error("Failed to start external listener", "error", err)
					}
				} else if s.logger != nil {
					s.logger.Info("External listener started", "port", actualPort)
				}
			} else if s.logger != nil {
				s.logger.Debug("External listener disabled (port = -1)")
			}
		}
		return
	}

	// Case 2: Auth was enabled, now disabled -> stop external listener
	if oldAuthEnabled && !newAuthEnabled {
		s.StopExternalListener()

		// Note: We don't destroy the auth manager here because it might still be
		// needed for the middleware chain. Instead, we just update its config to nil
		// which effectively disables it.
		if s.authManager != nil {
			s.authManager.UpdateConfig(nil)
			if s.logger != nil {
				s.logger.Info("Authentication disabled dynamically")
			}
		}
		return
	}

	// Case 3: Auth was enabled and still enabled -> update credentials and ensure listener is running
	if oldAuthEnabled && newAuthEnabled {
		if s.authManager != nil {
			s.authManager.UpdateConfig(newAuthConfig)
			if s.logger != nil {
				s.logger.Info("Authentication credentials updated")
			}
		}

		// Ensure external listener is running (it might have been stopped or never started)
		if !s.IsExternalListenerRunning() {
			// Use configured external port: -1 = disabled, 0 = random, >0 = specific port
			port := s.GetExternalPort()
			if port == 0 && s.config.MittoConfig != nil && s.config.MittoConfig.Web.ExternalPort > 0 {
				port = s.config.MittoConfig.Web.ExternalPort
			}
			// Only start if port is >= 0 (port 0 = random, port > 0 = specific)
			// Port -1 means disabled
			if port >= 0 {
				actualPort, err := s.StartExternalListener(port)
				if err != nil {
					if s.logger != nil {
						s.logger.Error("Failed to start external listener", "error", err)
					}
				} else if s.logger != nil {
					s.logger.Info("External listener started", "port", actualPort)
				}
			} else if s.logger != nil {
				s.logger.Debug("External listener disabled (port = -1)")
			}
		}
		return
	}

	// Case 4: Auth was disabled and still disabled -> nothing to do
}

// ExternalStatusResponse represents the response from GET /api/external-status.
type ExternalStatusResponse struct {
	Enabled bool `json:"enabled"`
	Port    int  `json:"port"`
}

// handleExternalStatus handles GET /api/external-status.
// Returns the current status of the external listener.
func (s *Server) handleExternalStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(ExternalStatusResponse{
		Enabled: s.IsExternalListenerRunning(),
		Port:    s.GetExternalPort(),
	})
}
