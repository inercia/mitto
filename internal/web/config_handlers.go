package web

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/inercia/mitto/internal/auxiliary"
	configPkg "github.com/inercia/mitto/internal/config"
	"github.com/inercia/mitto/internal/secrets"
)

// ConfigSaveRequest represents the request body for saving configuration.
type ConfigSaveRequest struct {
	Workspaces []configPkg.WorkspaceSettings `json:"workspaces"`
	ACPServers []struct {
		Name    string                `json:"name"`
		Command string                `json:"command"`
		Prompts []configPkg.WebPrompt `json:"prompts,omitempty"`
	} `json:"acp_servers"`
	// Prompts is the top-level list of global prompts
	Prompts []configPkg.WebPrompt `json:"prompts,omitempty"`
	Web     struct {
		Host         string `json:"host,omitempty"`
		ExternalPort int    `json:"external_port,omitempty"`
		Auth         *struct {
			Simple *struct {
				Username string `json:"username"`
				Password string `json:"password"`
			} `json:"simple,omitempty"`
		} `json:"auth,omitempty"`
		Hooks *configPkg.WebHooks `json:"hooks,omitempty"`
	} `json:"web"`
	UI            *configPkg.UIConfig            `json:"ui,omitempty"`
	Conversations *configPkg.ConversationsConfig `json:"conversations,omitempty"`
}

// handleConfig handles GET and POST /api/config.
func (s *Server) handleConfig(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		s.handleGetConfig(w, r)
	case http.MethodPost:
		s.handleSaveConfig(w, r)
	default:
		methodNotAllowed(w)
	}
}

// handleGetConfig handles GET {prefix}/api/config.
func (s *Server) handleGetConfig(w http.ResponseWriter, r *http.Request) {
	// Build complete config response including workspaces and ACP servers
	response := map[string]interface{}{
		"workspaces":      s.sessionManager.GetWorkspaces(),
		"acp_servers":     []map[string]string{},
		"web":             configPkg.WebConfig{},
		"config_readonly": s.config.ConfigReadOnly,
		"api_prefix":      s.apiPrefix, // Include API prefix for frontend to use
	}

	// Include RC file path if config is from an RC file
	if s.config.RCFilePath != "" {
		response["rc_file_path"] = s.config.RCFilePath
	}

	if s.config.MittoConfig != nil {
		response["web"] = s.config.MittoConfig.Web
		response["ui"] = s.config.MittoConfig.UI
		response["conversations"] = s.config.MittoConfig.Conversations

		// Merge prompts from global files and settings
		// Global file prompts (MITTO_DIR/prompts/*.md) have lower priority than settings prompts
		var globalFilePrompts []configPkg.WebPrompt
		if s.config.PromptsCache != nil {
			var err error
			globalFilePrompts, err = s.config.PromptsCache.GetWebPrompts()
			if err != nil && s.logger != nil {
				s.logger.Warn("Failed to load global file prompts", "error", err)
			}
		}
		// Merge: settings prompts override global file prompts by name
		// Note: workspace prompts are handled separately via /api/workspace-prompts
		mergedPrompts := configPkg.MergePrompts(globalFilePrompts, s.config.MittoConfig.Prompts, nil)
		response["prompts"] = mergedPrompts

		// Convert ACP servers to JSON-friendly format (including per-server prompts)
		acpServers := make([]map[string]interface{}, len(s.config.MittoConfig.ACPServers))
		for i, srv := range s.config.MittoConfig.ACPServers {
			acpServers[i] = map[string]interface{}{
				"name":    srv.Name,
				"command": srv.Command,
			}
			// Include prompts if present
			if len(srv.Prompts) > 0 {
				acpServers[i]["prompts"] = srv.Prompts
			}
		}
		response["acp_servers"] = acpServers
	}

	writeJSONOK(w, response)
}

// handleSaveConfig handles POST /api/config.
func (s *Server) handleSaveConfig(w http.ResponseWriter, r *http.Request) {
	// Reject saves when config is read-only (loaded from --config file)
	if s.config.ConfigReadOnly {
		http.Error(w, "Configuration is read-only (loaded from config file)", http.StatusForbidden)
		return
	}

	var req ConfigSaveRequest
	if !parseJSONBody(w, r, &req) {
		return
	}

	// Validate request structure
	if validationErr := s.validateConfigRequest(&req); validationErr != nil {
		s.writeConfigError(w, validationErr)
		return
	}

	// Check for workspace conflicts (workspaces being removed that have conversations)
	if conflictErr := s.checkWorkspaceConflicts(&req); conflictErr != nil {
		s.writeConfigError(w, conflictErr)
		return
	}

	// Build new settings (also stores password in Keychain on macOS)
	settings, err := s.buildNewSettings(&req)
	if err != nil {
		if s.logger != nil {
			s.logger.Error("Failed to build settings", "error", err)
		}
		http.Error(w, "Failed to build settings: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// Save settings to disk
	if err := configPkg.SaveSettings(settings); err != nil {
		if s.logger != nil {
			s.logger.Error("Failed to save settings", "error", err)
		}
		http.Error(w, "Failed to save settings: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// Apply changes to running server
	s.applyConfigChanges(&req, settings)

	// Build response with applied changes info
	writeJSONOK(w, map[string]interface{}{
		"success": true,
		"message": "Configuration saved successfully",
		"applied": map[string]interface{}{
			"external_access_enabled": s.IsExternalListenerRunning(),
			"external_port":           s.GetExternalPort(),
			"auth_enabled":            s.authManager != nil && s.authManager.IsEnabled(),
		},
	})
}

// handleImprovePrompt handles POST /api/aux/improve-prompt.
// It uses the auxiliary ACP session to improve a user's prompt.
func (s *Server) handleImprovePrompt(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w)
		return
	}

	// Parse request body
	var req struct {
		Prompt string `json:"prompt"`
	}
	if !parseJSONBody(w, r, &req) {
		return
	}

	if req.Prompt == "" {
		http.Error(w, "Prompt is required", http.StatusBadRequest)
		return
	}

	// Check if auxiliary manager is initialized
	if auxiliary.GetManager() == nil {
		s.logger.Error("Auxiliary manager not initialized")
		http.Error(w, "Service unavailable", http.StatusServiceUnavailable)
		return
	}

	// Create a context with timeout for the auxiliary request
	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()

	// Call the auxiliary package to improve the prompt
	improved, err := auxiliary.ImprovePrompt(ctx, req.Prompt)
	if err != nil {
		s.logger.Error("Failed to improve prompt", "error", err)
		http.Error(w, "Failed to improve prompt", http.StatusInternalServerError)
		return
	}

	// Return the improved prompt
	writeJSONOK(w, map[string]string{
		"improved_prompt": improved,
	})
}

// buildNewSettings builds the new settings from a ConfigSaveRequest.
// It also handles secure storage of the external access password on supported platforms.
// On macOS, the password is stored in the Keychain and omitted from settings.json.
// On other platforms, the password is stored in settings.json.
func (s *Server) buildNewSettings(req *ConfigSaveRequest) (*configPkg.Settings, error) {
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
		password := req.Web.Auth.Simple.Password

		// On platforms with secure storage, store password in Keychain
		// and omit it from settings.json
		if secrets.IsSupported() {
			if err := secrets.SetExternalAccessPassword(password); err != nil {
				return nil, fmt.Errorf("failed to store password in secure storage: %w", err)
			}
			// Omit password from settings.json when stored in Keychain
			password = ""
		}

		newWebConfig.Auth = &configPkg.WebAuth{
			Simple: &configPkg.SimpleAuth{
				Username: req.Web.Auth.Simple.Username,
				Password: password, // Empty when stored in Keychain
			},
		}
	} else {
		newWebConfig.Auth = nil
		// Clean up any stored password when auth is disabled
		if secrets.IsSupported() {
			_ = secrets.DeleteExternalAccessPassword() // Ignore errors
		}
	}

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

	// Preserve Session config (not exposed in web UI)
	var sessionConfig *configPkg.SessionConfig
	if s.config.MittoConfig != nil {
		sessionConfig = s.config.MittoConfig.Session
	}

	// Use Conversations from request if provided, otherwise preserve existing
	var conversationsConfig *configPkg.ConversationsConfig
	if req.Conversations != nil {
		conversationsConfig = req.Conversations
	} else if s.config.MittoConfig != nil {
		conversationsConfig = s.config.MittoConfig.Conversations
	}

	return &configPkg.Settings{
		ACPServers:    newACPServers,
		Prompts:       req.Prompts,
		Web:           newWebConfig,
		UI:            newUIConfig,
		Session:       sessionConfig,
		Conversations: conversationsConfig,
	}, nil
}

// applyConfigChanges applies the new configuration to the running server.
// Note: The settings parameter may have an empty password when Keychain is used,
// so we use the original password from req for runtime auth configuration.
func (s *Server) applyConfigChanges(req *ConfigSaveRequest, settings *configPkg.Settings) {
	// Build ACP server list for internal config (including per-server prompts)
	newACPServers := make([]configPkg.ACPServer, len(settings.ACPServers))
	for i, srv := range settings.ACPServers {
		newACPServers[i] = configPkg.ACPServer(srv)
	}

	// Determine if external access is being enabled or disabled
	oldAuthEnabled := s.config.MittoConfig != nil &&
		s.config.MittoConfig.Web.Auth != nil &&
		s.config.MittoConfig.Web.Auth.Simple != nil
	newAuthEnabled := settings.Web.Auth != nil && settings.Web.Auth.Simple != nil

	// Build runtime web config with the actual password (from request, not settings)
	// This is needed because settings may have an empty password when Keychain is used
	runtimeWebConfig := settings.Web
	if newAuthEnabled && req.Web.Auth != nil && req.Web.Auth.Simple != nil {
		runtimeWebConfig.Auth = &configPkg.WebAuth{
			Simple: &configPkg.SimpleAuth{
				Username: req.Web.Auth.Simple.Username,
				Password: req.Web.Auth.Simple.Password, // Use original password for runtime
			},
		}
	}

	// Update ACP servers, prompts, web config, UI config, and conversations config
	if s.config.MittoConfig != nil {
		s.config.MittoConfig.ACPServers = newACPServers
		s.config.MittoConfig.Prompts = settings.Prompts
		s.config.MittoConfig.Web = runtimeWebConfig
		s.config.MittoConfig.UI = settings.UI
		s.config.MittoConfig.Conversations = settings.Conversations

		// Update session manager's global conversations config so new sessions use the updated settings
		s.sessionManager.SetGlobalConversations(settings.Conversations)
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

	// Handle auth manager and external listener changes (use runtimeWebConfig with actual password)
	s.applyAuthChanges(oldAuthEnabled, newAuthEnabled, runtimeWebConfig.Auth)

	if s.logger != nil {
		s.logger.Info("Configuration saved",
			"workspaces", len(newWorkspaces),
			"acp_servers", len(newACPServers),
			"auth_enabled", newAuthEnabled,
			"external_listener", s.IsExternalListenerRunning())
	}
}

// hasValidCredentials checks if auth config has non-empty username and password.
func hasValidCredentials(authConfig *configPkg.WebAuth) bool {
	return authConfig != nil &&
		authConfig.Simple != nil &&
		authConfig.Simple.Username != "" &&
		authConfig.Simple.Password != ""
}

// ensureExternalListenerStarted starts the external listener if not already running.
func (s *Server) ensureExternalListenerStarted() {
	if s.IsExternalListenerRunning() {
		return
	}

	// Use configured external port: -1 = disabled, 0 = random, >0 = specific port
	port := s.GetExternalPort()
	if port == 0 && s.config.MittoConfig != nil && s.config.MittoConfig.Web.ExternalPort > 0 {
		port = s.config.MittoConfig.Web.ExternalPort
	}

	// Only start if port is >= 0 (port 0 = random, port > 0 = specific)
	// Port -1 means disabled
	if port < 0 {
		if s.logger != nil {
			s.logger.Debug("External listener disabled (port = -1)")
		}
		return
	}

	actualPort, err := s.StartExternalListener(port)
	if err != nil {
		if s.logger != nil {
			s.logger.Error("Failed to start external listener", "error", err)
		}
	} else if s.logger != nil {
		s.logger.Info("External listener started", "port", actualPort)
	}
}

// applyAuthChanges handles dynamic changes to authentication and external access.
// It validates that credentials are non-empty before enabling external access.
func (s *Server) applyAuthChanges(oldAuthEnabled, newAuthEnabled bool, newAuthConfig *configPkg.WebAuth) {
	// Case 1: Auth was disabled, now enabled -> create auth manager and start external listener
	if !oldAuthEnabled && newAuthEnabled {
		if !hasValidCredentials(newAuthConfig) {
			if s.logger != nil {
				s.logger.Error("Cannot enable external access: credentials are incomplete")
			}
			return
		}

		// Create new auth manager if it doesn't exist
		if s.authManager == nil {
			s.authManager = NewAuthManager(newAuthConfig)
			if s.logger != nil {
				s.logger.Info("Authentication enabled dynamically")
			}
		} else {
			s.authManager.UpdateConfig(newAuthConfig)
		}

		s.ensureExternalListenerStarted()
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
		if !hasValidCredentials(newAuthConfig) {
			if s.logger != nil {
				s.logger.Error("Cannot update external access: credentials are incomplete, stopping listener")
			}
			s.StopExternalListener()
			if s.authManager != nil {
				s.authManager.UpdateConfig(nil)
			}
			return
		}

		if s.authManager != nil {
			s.authManager.UpdateConfig(newAuthConfig)
			if s.logger != nil {
				s.logger.Info("Authentication credentials updated")
			}
		}

		s.ensureExternalListenerStarted()
		return
	}

	// Case 4: Auth was disabled and still disabled -> nothing to do
}
