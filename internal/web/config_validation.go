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
	body := errorBody{Code: defaultCodeForStatus(err.StatusCode), Message: err.Message}
	// Preserve domain-specific context (e.g. conflict workspace + conversation_count)
	// under the canonical details field, dropping the legacy flat error/message keys.
	if len(err.Details) > 0 {
		details := make(map[string]any, len(err.Details))
		for k, v := range err.Details {
			if k == "error" || k == "message" {
				continue
			}
			details[k] = v
		}
		if len(details) > 0 {
			body.Details = details
		}
	}
	writeJSON(w, err.StatusCode, errorEnvelope{Error: body})
}

// hasExistingSimpleAuth returns true if the server already has simple auth configured
// with a non-empty username AND a non-empty password.
//
// The password check uses the runtime config value, which is already populated from
// the system keychain at startup (via LoadSettings → loadKeychainPassword), so
// this correctly handles both in-file and keychain-stored passwords.
func (s *Server) hasExistingSimpleAuth() bool {
	if s.config.MittoConfig == nil || s.config.MittoConfig.Web.Auth == nil || s.config.MittoConfig.Web.Auth.Simple == nil {
		return false
	}
	simple := s.config.MittoConfig.Web.Auth.Simple
	return simple.Username != "" && simple.Password != ""
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

	// Validate workspaces
	for _, ws := range req.Workspaces {
		if ws.WorkingDir == "" {
			return &configValidationError{
				StatusCode: http.StatusBadRequest,
				Message:    "Workspace path cannot be empty",
			}
		}
		// Validate ACP server reference when the workspace specifies one.
		// A workspace with an empty ACPServer is valid (first-run, no servers configured yet).
		// A workspace that references a named server must reference one that exists.
		if ws.ACPServer != "" && !acpServerNames[ws.ACPServer] {
			return &configValidationError{
				StatusCode: http.StatusBadRequest,
				Message:    fmt.Sprintf("Workspace '%s' references unknown ACP server: %s", ws.WorkingDir, ws.ACPServer),
			}
		}
	}

	// Validate auth settings. When the web section is omitted entirely (req.Web == nil,
	// e.g. the Workspaces dialog), there is no auth to validate — the existing config is
	// preserved untouched in buildNewSettings.
	if req.Web != nil && req.Web.Auth != nil && req.Web.Auth.Simple != nil {
		if errMsg := ValidateUsername(req.Web.Auth.Simple.Username); errMsg != "" {
			return &configValidationError{
				StatusCode: http.StatusBadRequest,
				Message:    errMsg,
			}
		}
		// Skip password validation when the password is empty and a simple-auth block
		// already exists in the persisted config. The frontend always receives an empty
		// password (the backend redacts it before sending config to the client), so any
		// config save that round-trips the existing auth block — for example changing a
		// workspace's ACP server in the Workspaces dialog — sends back an empty password
		// it never intended to modify. Re-validating it here would reject those unrelated
		// saves with "Password is required" whenever the stored password lives only in the
		// keychain or is absent (a username-only/partial auth config). Skipping is safe:
		// buildNewSettings preserves any existing password, and applyAuthChanges refuses to
		// start a passwordless external listener (see hasValidCredentials). A brand-new auth
		// setup (no existing simple-auth block) is still validated and requires a password.
		existingSimpleAuth := s.config.MittoConfig != nil &&
			s.config.MittoConfig.Web.Auth != nil &&
			s.config.MittoConfig.Web.Auth.Simple != nil
		if req.Web.Auth.Simple.Password != "" || !existingSimpleAuth {
			if errMsg := ValidatePassword(req.Web.Auth.Simple.Password); errMsg != "" {
				return &configValidationError{
					StatusCode: http.StatusBadRequest,
					Message:    errMsg,
				}
			}
		}
	}

	// Validate MCP server port. A nil port means "use the default (5757)"; a
	// non-nil port must be a fixed, valid port. Port 0 (auto-assigned / random)
	// is rejected because the full MCP address must be known in advance so ACP
	// servers can be configured to connect to it.
	if req.MCP != nil && req.MCP.Port != nil {
		if p := *req.MCP.Port; p < 1 || p > 65535 {
			return &configValidationError{
				StatusCode: http.StatusBadRequest,
				Message:    "MCP server port must be a fixed port between 1 and 65535 (0 / auto-assigned is not allowed; the address must be known in advance for ACP servers to connect)",
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
