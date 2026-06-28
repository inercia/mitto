package web

import (
	"fmt"
	"net/http"

	configPkg "github.com/inercia/mitto/internal/config"
	"github.com/inercia/mitto/internal/secrets"
	"github.com/inercia/mitto/internal/web/handlers"
	"github.com/inercia/mitto/internal/web/middleware"
)

// ConfigSaveRequest represents the request body for saving configuration.
//
// The type is defined in the handlers sub-package (handlers.ConfigSaveRequest,
// alongside the migrated HandleSaveConfig) and aliased here so the web-package
// config helpers (buildNewSettings, applyConfigChanges, validateConfigRequest,
// checkWorkspaceConflicts) and their tests keep referring to it unqualified.
type ConfigSaveRequest = handlers.ConfigSaveRequest

// ExternalAccessWarning is aliased from handlers.ExternalAccessWarning so the
// web-package helpers (applyAuthChanges, ensureExternalListenerStarted,
// applyConfigChanges) can return it without a package qualifier.
type ExternalAccessWarning = handlers.ExternalAccessWarning

// handleConfig handles GET and POST /api/config.
func (s *Server) handleConfig(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		s.apiHandlers.HandleGetConfig(w, r)
	case http.MethodPost:
		s.apiHandlers.HandleSaveConfig(w, r)
	default:
		methodNotAllowed(w)
	}
}

// validateAndPrepareSaveConfig runs the pre-save validation pipeline for a
// config save request: structural validation, workspace-removal conflict
// checks, default-workspace normalization, and per-workspace restricted-runner
// validation (with a non-fatal platform-support warning). It writes an error
// response and returns false when the request must be rejected; otherwise it
// returns true with req normalized in place. It is wired into handlers.Deps so
// the migrated HandleSaveConfig can delegate this server-coupled validation
// (which depends on the web package's private configValidationError type).
func (s *Server) validateAndPrepareSaveConfig(w http.ResponseWriter, req *ConfigSaveRequest) bool {
	// Validate request structure
	if validationErr := s.validateConfigRequest(req); validationErr != nil {
		s.writeConfigError(w, validationErr)
		return false
	}

	// Check for workspace conflicts (workspaces being removed that have conversations)
	if conflictErr := s.checkWorkspaceConflicts(req); conflictErr != nil {
		s.writeConfigError(w, conflictErr)
		return false
	}

	// Enforce a single default workspace per folder. The UI already clears
	// is_default on sibling workspaces when one is set, but normalize here as a
	// safety net so direct API callers (or pre-existing data) cannot persist
	// multiple defaults for the same folder.
	configPkg.NormalizeDefaultWorkspaces(req.Workspaces)

	// Validate restricted_runner field for all workspaces and check platform support
	for i, ws := range req.Workspaces {
		tempWs := configPkg.WorkspaceSettings{RestrictedRunner: ws.RestrictedRunner}
		if err := tempWs.ValidateRestrictedRunner(); err != nil {
			s.writeConfigError(w, &configValidationError{
				StatusCode: http.StatusBadRequest,
				Message:    fmt.Sprintf("workspaces[%d].restricted_runner: %s", i, err.Error()),
			})
			return false
		}

		// Check if runner is supported on this platform (pre-flight validation)
		if ws.RestrictedRunner != "" && ws.RestrictedRunner != "exec" {
			// Create a temporary runner to check platform support
			runnerType := ws.RestrictedRunner
			warning := handlers.CheckRunnerSupport(runnerType)
			if warning != "" {
				// Add warning to response (don't fail, just warn)
				// The warning will be shown in the UI
				if s.logger != nil {
					s.logger.Warn("Runner may not be supported on this platform",
						"workspace", ws.WorkingDir,
						"runner_type", runnerType,
						"warning", warning)
				}
			}
		}
	}

	return true
}

// handleImprovePrompt handles POST /api/aux/improve-prompt.
// It uses the auxiliary ACP session to improve a user's prompt.
// buildNewSettings builds the new settings from a ConfigSaveRequest.
// It also handles secure storage of the external access password on supported platforms.
// On macOS, the password is stored in the Keychain and omitted from settings.json.
// On other platforms, the password is stored in settings.json.
//
// IMPORTANT: Only servers with Source != SourceRCFile are saved to settings.json.
// RC file servers are preserved from the RC file and not duplicated in settings.
func (s *Server) buildNewSettings(req *ConfigSaveRequest) (*configPkg.Settings, error) {
	// Build a map of existing server settings for preserving fields not exposed in UI
	existingServers := make(map[string]configPkg.ACPServer)
	if s.config.MittoConfig != nil {
		for _, srv := range s.config.MittoConfig.ACPServers {
			existingServers[srv.Name] = srv
		}
	}

	// Build new ACP servers - ONLY include servers that are NOT from the RC file.
	// RC file servers are managed in .mittorc and should not be duplicated in settings.json.
	// This allows users to:
	// 1. Have a base set of servers in .mittorc (read-only, version controlled)
	// 2. Add custom servers via UI that are saved to settings.json
	var newACPServers []configPkg.ACPServerSettings
	for _, srv := range req.ACPServers {
		// Skip RC file servers - they are managed in .mittorc, not settings.json
		if srv.Source == configPkg.SourceRCFile {
			continue
		}

		newServer := configPkg.ACPServerSettings{
			Name:        srv.Name,
			Command:     srv.Command,
			Type:        srv.Type,                 // Optional type for prompt matching
			Env:         srv.Env,                  // Environment variables
			Source:      configPkg.SourceSettings, // Mark as settings-sourced
			AutoApprove: srv.AutoApprove,          // Auto-approve permission requests
			Tags:        srv.Tags,                 // Categorization tags
			Constraints: srv.Constraints,          // Config option auto-selection rules
			// ContextFlushCommand: agent-native context-flush slash command (e.g. "/clear")
			ContextFlushCommand: srv.ContextFlushCommand,
			// Per-server prompts are no longer saved to settings.json
			// They are managed via prompt files with acps: field
		}

		// Preserve Cwd and RestrictedRunners from existing server if present.
		// These fields are not exposed in the UI but should not be lost on save.
		//
		// Also restore any env var values that were masked ("***") in the GET /api/config
		// response — the client cannot know the real value so it echoes the mask back.
		// Replacing "***" with the original keeps the secret intact.
		if existing, ok := existingServers[srv.Name]; ok {
			newServer.Cwd = existing.Cwd
			newServer.RestrictedRunners = existing.RestrictedRunners
			if len(newServer.Env) > 0 && len(existing.Env) > 0 {
				for k, v := range newServer.Env {
					if v == "***" {
						if orig, found := existing.Env[k]; found {
							newServer.Env[k] = orig
						}
					}
				}
			}
		}

		newACPServers = append(newACPServers, newServer)
	}

	// Build new web config (preserve existing settings, update auth and host)
	newWebConfig := configPkg.WebConfig{}
	if s.config.MittoConfig != nil {
		newWebConfig = s.config.MittoConfig.Web
	}

	// When the request omits the web section entirely (req.Web == nil) — e.g. the
	// Workspaces dialog, which has no business touching external-access auth/host/port —
	// preserve the existing web config untouched. On secure-storage platforms the real
	// password lives in the runtime config (loaded from the keychain at startup), so
	// redact it before it is persisted to settings.json; the keychain copy is left intact
	// and the runtime auth (with the real password) is restored in applyConfigChanges.
	if req.Web == nil {
		if secrets.IsSupported() {
			newWebConfig = handlers.SanitizeWebConfig(newWebConfig)
		}
	} else {
		// Update host setting if provided
		if req.Web.Host != "" {
			newWebConfig.Host = req.Web.Host
		}

		// Update external port setting (0 means random)
		newWebConfig.ExternalPort = req.Web.ExternalPort

		// Update auth settings
		hasSimple := req.Web.Auth != nil && req.Web.Auth.Simple != nil
		hasCloudflare := req.Web.Auth != nil && req.Web.Auth.Cloudflare != nil

		if hasSimple || hasCloudflare {
			newWebConfig.Auth = &configPkg.WebAuth{}

			// Simple auth (username/password)
			if hasSimple {
				password := req.Web.Auth.Simple.Password

				// If the password is empty, preserve the existing password.
				// The frontend sends an empty password when the user hasn't changed it
				// (the backend sanitizes the password before sending config to the client).
				if password == "" && s.hasExistingSimpleAuth() {
					password = s.config.MittoConfig.Web.Auth.Simple.Password
				}

				// On platforms with secure storage, store password in Keychain
				// and omit it from settings.json
				if secrets.IsSupported() {
					if err := secrets.SetExternalAccessPassword(password); err != nil {
						return nil, fmt.Errorf("failed to store password in secure storage: %w", err)
					}
					// Omit password from settings.json when stored in Keychain
					password = ""
				}

				newWebConfig.Auth.Simple = &configPkg.SimpleAuth{
					Username: req.Web.Auth.Simple.Username,
					Password: password, // Empty when stored in Keychain
				}
			} else if secrets.IsSupported() {
				// Clean up stored password when simple auth is disabled
				_ = secrets.DeleteExternalAccessPassword()
			}

			// Cloudflare Access auth
			if hasCloudflare {
				newWebConfig.Auth.Cloudflare = &configPkg.CloudflareAuth{
					TeamDomain: req.Web.Auth.Cloudflare.TeamDomain,
					Audience:   req.Web.Auth.Cloudflare.Audience,
				}
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

		// Update access log settings
		if req.Web.AccessLog != nil {
			newWebConfig.AccessLog = req.Web.AccessLog
		}
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

	// Use Session from request if provided, otherwise preserve existing
	var sessionConfig *configPkg.SessionConfig
	if req.Session != nil {
		sessionConfig = req.Session
	} else if s.config.MittoConfig != nil {
		sessionConfig = s.config.MittoConfig.Session
	}

	// Use Conversations from request if provided, otherwise preserve existing
	var conversationsConfig *configPkg.ConversationsConfig
	if req.Conversations != nil {
		conversationsConfig = req.Conversations
	} else if s.config.MittoConfig != nil {
		conversationsConfig = s.config.MittoConfig.Conversations
	}

	// Use Permissions from request if provided, otherwise preserve existing
	var permissionsConfig *configPkg.PermissionsConfig
	if req.Permissions != nil {
		permissionsConfig = req.Permissions
	} else if s.config.MittoConfig != nil {
		permissionsConfig = s.config.MittoConfig.Permissions
	}

	// Use MCP from request if provided, otherwise preserve existing
	var mcpConfig *configPkg.MCPConfig
	if req.MCP != nil {
		mcpConfig = req.MCP
	} else if s.config.MittoConfig != nil {
		mcpConfig = s.config.MittoConfig.MCP
	}

	// Filter out file-sourced and builtin prompts — they should not be persisted to settings.json
	// since they're already loaded from MITTO_DIR/prompts/ files on startup.
	var settingsPrompts []configPkg.WebPrompt
	for _, p := range req.Prompts {
		if p.Source != configPkg.PromptSourceFile && p.Source != configPkg.PromptSourceBuiltin {
			settingsPrompts = append(settingsPrompts, p)
		}
	}

	// Use Models from request if provided, otherwise preserve existing profiles.
	// Pointer semantics: nil = section omitted (preserve); non-nil = authoritative list.
	var modelsConfig []configPkg.ModelProfile
	if req.Models != nil {
		modelsConfig = *req.Models
	} else if s.config.MittoConfig != nil {
		modelsConfig = s.config.MittoConfig.Models
	}

	return &configPkg.Settings{
		ACPServers:    newACPServers,
		Prompts:       settingsPrompts,
		Web:           newWebConfig,
		UI:            newUIConfig,
		Session:       sessionConfig,
		Conversations: conversationsConfig,
		Permissions:   permissionsConfig,
		MCP:           mcpConfig,
		Models:        modelsConfig,
	}, nil
}

// applyConfigChanges applies the new configuration to the running server.
// Note: The settings parameter may have an empty password when Keychain is used,
// so we use the original password from req for runtime auth configuration.
// Returns a non-nil *ExternalAccessWarning when the save results in the external
// listener not running even though external access was intended to be on.
func (s *Server) applyConfigChanges(req *ConfigSaveRequest, settings *configPkg.Settings) *ExternalAccessWarning {
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
	if req.Web == nil {
		// The web section was omitted (e.g. the Workspaces dialog). Restore the real
		// runtime auth — including the keychain-loaded password — so applyAuthChanges
		// does not tear down the external listener over the redacted/empty password that
		// buildNewSettings persisted to settings.json.
		if s.config.MittoConfig != nil {
			runtimeWebConfig.Auth = s.config.MittoConfig.Web.Auth
		}
	} else if newAuthEnabled && req.Web.Auth != nil && req.Web.Auth.Simple != nil {
		password := req.Web.Auth.Simple.Password
		// If request password is empty, preserve the existing runtime password
		if password == "" && oldAuthEnabled && s.config.MittoConfig.Web.Auth.Simple != nil {
			password = s.config.MittoConfig.Web.Auth.Simple.Password
		}
		runtimeWebConfig.Auth = &configPkg.WebAuth{
			Simple: &configPkg.SimpleAuth{
				Username: req.Web.Auth.Simple.Username,
				Password: password,
			},
		}
	}

	// Update ACP servers, prompts, web config, UI config, session config, and conversations config
	if s.config.MittoConfig != nil {
		s.config.MittoConfig.ACPServers = newACPServers
		s.config.MittoConfig.Prompts = settings.Prompts
		s.config.MittoConfig.Web = runtimeWebConfig
		s.config.MittoConfig.UI = settings.UI
		s.config.MittoConfig.Session = settings.Session
		s.config.MittoConfig.Conversations = settings.Conversations
		s.config.MittoConfig.MCP = settings.MCP
		s.config.MittoConfig.Models = settings.Models

		// Update session manager's global conversations config so new sessions use the updated settings
		s.sessionManager.SetGlobalConversations(settings.Conversations)

		// Update GC periodic suspend threshold at runtime if session config changed
		if settings.Session != nil && s.acpProcessManager != nil {
			if d, enabled := settings.Session.ParsePeriodicSuspendTimeout(); enabled {
				s.acpProcessManager.UpdatePeriodicSuspendThreshold(d)
			} else {
				s.acpProcessManager.UpdatePeriodicSuspendThreshold(0)
			}
			if bytes, enabled := settings.Session.ParseMemoryRecycleThreshold(); enabled {
				s.acpProcessManager.UpdateMemoryRecycleThreshold(bytes)
			} else {
				s.acpProcessManager.UpdateMemoryRecycleThreshold(0)
			}
		}
	}

	// ACP command/cwd/env are resolved from global config at runtime and are never
	// stored on the workspace. Just copy the workspace as-is from the request.
	newWorkspaces := make([]configPkg.WorkspaceSettings, len(req.Workspaces))
	copy(newWorkspaces, req.Workspaces)
	s.sessionManager.SetWorkspaces(newWorkspaces)
	s.config.Workspaces = newWorkspaces

	// Migrate conversations that reference renamed ACP servers. This runs after
	// the updated ACP servers and workspaces are applied above, so resumed
	// sessions resolve the new server. Without this, in-place renames would
	// orphan existing conversations (resume fails with "empty command").
	if len(req.ServerRenames) > 0 {
		if result, err := s.sessionManager.ApplyACPServerRenames(req.ServerRenames); err != nil {
			if s.logger != nil {
				s.logger.Error("Failed to apply ACP server renames to conversations",
					"error", err)
			}
		} else if result != nil && s.logger != nil {
			s.logger.Info("Applied ACP server renames to conversations",
				"updated", len(result.UpdatedSessionIDs),
				"restarted", len(result.RestartedSessionIDs))
		}
	}

	// Update external port setting on the server (before applying auth changes)
	// If the new setting is 0 (random) and we already have a running external listener,
	// preserve the current port to avoid generating a new random port on every save.
	newExternalPort := settings.Web.ExternalPort
	if newExternalPort == 0 && s.IsExternalListenerRunning() {
		// Keep the current port when re-saving with "random" port while already running
		newExternalPort = s.GetExternalPort()
	}
	s.SetExternalPort(newExternalPort)

	// Handle auth manager and external listener changes (use runtimeWebConfig with actual password).
	// Capture any warning so we can propagate it to the HTTP response.
	warning := s.applyAuthChanges(oldAuthEnabled, newAuthEnabled, runtimeWebConfig.Auth)

	// Reconcile the health monitor AFTER auth/listener changes have settled, so it
	// only starts when the external listener is actually up. Running it earlier (in
	// buildNewSettings) restarted the monitor before applyAuthChanges could tear down
	// the listener on incomplete credentials, causing a futile tunnel-restart storm.
	// Guarded by req.Web != nil to preserve prior behavior (only reconcile on web saves).
	if req.Web != nil {
		s.updateHealthMonitor(settings.Web.Hooks)
	}

	if s.logger != nil {
		s.logger.Info("Configuration saved",
			"workspaces", len(newWorkspaces),
			"acp_servers", len(newACPServers),
			"auth_enabled", newAuthEnabled,
			"external_listener", s.IsExternalListenerRunning())
	}

	return warning
}

// hasValidCredentials checks if auth config has non-empty username and password.
func hasValidCredentials(authConfig *configPkg.WebAuth) bool {
	return authConfig != nil &&
		authConfig.Simple != nil &&
		authConfig.Simple.Username != "" &&
		authConfig.Simple.Password != ""
}

// ensureExternalListenerStarted starts the external listener if not already running.
// Returns a non-nil *ExternalAccessWarning when StartExternalListener fails; returns
// nil on success or when the listener is intentionally disabled (port = -1).
func (s *Server) ensureExternalListenerStarted() *ExternalAccessWarning {
	if s.IsExternalListenerRunning() {
		return nil
	}

	// Use configured external port: -1 = disabled, 0 = random, >0 = specific port
	port := s.GetExternalPort()
	if port == 0 && s.config.MittoConfig != nil && s.config.MittoConfig.Web.ExternalPort > 0 {
		port = s.config.MittoConfig.Web.ExternalPort
	}

	// Only start if port is >= 0 (port 0 = random, port > 0 = specific)
	// Port -1 means disabled — no warning, this is intentional.
	if port < 0 {
		if s.logger != nil {
			s.logger.Debug("External listener disabled (port = -1)")
		}
		return nil
	}

	_, err := s.StartExternalListener(port)
	if err != nil {
		if s.logger != nil {
			s.logger.Error("Failed to start external listener", "error", err)
		}
		return &ExternalAccessWarning{
			Reason:  err.Error(),
			Port:    port,
			Message: fmt.Sprintf("External access failed to start on port %d: %s", port, err.Error()),
		}
	}
	// Note: StartExternalListener already logs success
	return nil
}

// applyAuthChanges handles dynamic changes to authentication and external access.
// It validates that credentials are non-empty before enabling external access.
// Returns a non-nil *ExternalAccessWarning when the save results in the external
// listener not running even though external access was intended to be on.
func (s *Server) applyAuthChanges(oldAuthEnabled, newAuthEnabled bool, newAuthConfig *configPkg.WebAuth) *ExternalAccessWarning {
	// Case 1: Auth was disabled, now enabled -> create auth manager and start external listener
	if !oldAuthEnabled && newAuthEnabled {
		if !hasValidCredentials(newAuthConfig) {
			if s.logger != nil {
				s.logger.Error("Cannot enable external access: credentials are incomplete")
			}
			s.externalDownForCredentials.Store(true)
			attemptedPort := s.GetExternalPort()
			if attemptedPort == 0 && s.config.MittoConfig != nil && s.config.MittoConfig.Web.ExternalPort > 0 {
				attemptedPort = s.config.MittoConfig.Web.ExternalPort
			}
			return &ExternalAccessWarning{
				Reason:  "authentication credentials are incomplete (no password)",
				Port:    attemptedPort,
				Message: fmt.Sprintf("External access is DOWN: authentication credentials are incomplete (no password). The external listener on port %d could not be started.", attemptedPort),
			}
		}

		// Create new auth manager if it doesn't exist
		if s.authManager == nil {
			s.authManager = middleware.NewAuthManager(newAuthConfig)
			if s.logger != nil {
				s.logger.Info("Authentication enabled dynamically")
			}
		} else {
			s.authManager.UpdateConfig(newAuthConfig)
		}

		warning := s.ensureExternalListenerStarted()
		if warning == nil && s.externalDownForCredentials.Swap(false) {
			if s.logger != nil {
				s.logger.Info("External access restored: credentials corrected, external listener back up",
					"port", s.GetExternalPort())
			}
		}
		return warning
	}

	// Case 2: Auth was enabled, now disabled -> stop external listener.
	// This is an intentional user action — no warning.
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
		return nil
	}

	// Case 3: Auth was enabled and still enabled -> update credentials and ensure listener is running
	if oldAuthEnabled && newAuthEnabled {
		if !hasValidCredentials(newAuthConfig) {
			if s.logger != nil {
				s.logger.Error("Cannot update external access: credentials are incomplete, stopping listener")
			}
			s.externalDownForCredentials.Store(true)
			// Capture the currently-running port BEFORE stopping the listener.
			attemptedPort := s.GetExternalPort()
			if attemptedPort == 0 && s.config.MittoConfig != nil && s.config.MittoConfig.Web.ExternalPort > 0 {
				attemptedPort = s.config.MittoConfig.Web.ExternalPort
			}
			s.StopExternalListener()
			if s.authManager != nil {
				s.authManager.UpdateConfig(nil)
			}
			return &ExternalAccessWarning{
				Reason:  "authentication credentials are incomplete (no password)",
				Port:    attemptedPort,
				Message: fmt.Sprintf("External access is DOWN: authentication credentials are incomplete (no password). The external listener on port %d was stopped.", attemptedPort),
			}
		}

		if s.authManager != nil {
			s.authManager.UpdateConfig(newAuthConfig)
			if s.logger != nil {
				s.logger.Info("Authentication credentials updated")
			}
		}

		warning := s.ensureExternalListenerStarted()
		if warning == nil && s.externalDownForCredentials.Swap(false) {
			if s.logger != nil {
				s.logger.Info("External access restored: credentials corrected, external listener back up",
					"port", s.GetExternalPort())
			}
		}
		return warning
	}

	// Case 4: Auth was disabled and still disabled -> nothing to do
	return nil
}
