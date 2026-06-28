package handlers

import (
	"net/http"
	"strings"

	configPkg "github.com/inercia/mitto/internal/config"
)

// sensitiveEnvKeyPatterns contains lowercase substrings that flag an env var key as sensitive.
var sensitiveEnvKeyPatterns = []string{
	"secret", "password", "passwd", "token", "api_key", "apikey",
	"private_key", "credentials", "access_key", "auth_key",
}

// isSensitiveEnvKey returns true when the env var key name suggests it holds a secret.
func isSensitiveEnvKey(key string) bool {
	lower := strings.ToLower(key)
	for _, pat := range sensitiveEnvKeyPatterns {
		if strings.Contains(lower, pat) {
			return true
		}
	}
	return false
}

// SanitizeEnvVars returns a shallow copy of env with sensitive values replaced by "***".
// This prevents API keys and tokens from leaking through the config endpoint.
func SanitizeEnvVars(env map[string]string) map[string]string {
	if env == nil {
		return nil
	}
	out := make(map[string]string, len(env))
	for k, v := range env {
		if isSensitiveEnvKey(k) {
			out[k] = "***"
		} else {
			out[k] = v
		}
	}
	return out
}

// SanitizeWebConfig returns a deep copy of WebConfig with the auth password redacted.
// The password must never be sent to the client — not even to an authenticated user —
// because it could be exfiltrated via XSS, screen-sharing, or developer tools.
func SanitizeWebConfig(cfg configPkg.WebConfig) configPkg.WebConfig {
	sanitized := cfg
	if cfg.Auth != nil {
		authCopy := *cfg.Auth
		if cfg.Auth.Simple != nil {
			simpleCopy := *cfg.Auth.Simple
			simpleCopy.Password = "" // Never return the password to the client
			authCopy.Simple = &simpleCopy
		}
		sanitized.Auth = &authCopy
	}
	return sanitized
}

// HandleGetConfig handles GET {prefix}/api/config.
// Supports optional query parameters:
//   - session_id: If specified, merged prompts are further filtered using
//     enabledWhen CEL expressions with the context of the given session.
func (h *Handlers) HandleGetConfig(w http.ResponseWriter, r *http.Request) {
	// Build complete config response including workspaces and ACP servers
	response := map[string]interface{}{
		"workspaces":      h.deps.SessionManager.GetWorkspaces(),
		"acp_servers":     []map[string]string{},
		"web":             configPkg.WebConfig{},
		"config_readonly": h.deps.ConfigReadOnly,
		"api_prefix":      h.deps.APIPrefix, // Include API prefix for frontend to use
	}

	// Include RC file path if config is from an RC file
	if h.deps.RCFilePath != "" {
		response["rc_file_path"] = h.deps.RCFilePath
	}

	if h.deps.MittoConfig != nil {
		// SECURITY: Sanitize web config to remove sensitive fields (auth password) before
		// sending to the client.  Even authenticated users must not receive the password
		// because it could be exfiltrated through XSS, dev-tools inspection, or screen-sharing.
		response["web"] = SanitizeWebConfig(h.deps.MittoConfig.Web)
		// Indicate to the frontend whether a password already exists (in keychain or settings).
		// The frontend uses this to distinguish "user left the field empty intentionally"
		// from "field is empty because there was never a password" — without exposing the password itself.
		if h.deps.HasExistingSimpleAuth != nil {
			response["has_auth_password"] = h.deps.HasExistingSimpleAuth()
		}
		response["ui"] = h.deps.MittoConfig.UI
		response["session"] = h.deps.MittoConfig.Session
		response["conversations"] = h.deps.MittoConfig.Conversations
		response["permissions"] = h.deps.MittoConfig.Permissions

		// MCP server config — send effective values (getters are nil-safe).
		// GetPort() returns -1 when unset; surface the default 5757 for display.
		mcpPort := h.deps.MittoConfig.MCP.GetPort()
		if mcpPort < 0 {
			mcpPort = 5757
		}
		response["mcp"] = map[string]interface{}{
			"enabled": h.deps.MittoConfig.MCP.IsEnabled(),
			"host":    h.deps.MittoConfig.MCP.GetHost(),
			"port":    mcpPort,
		}

		// Merge prompts from global files and settings
		// Global file prompts (MITTO_DIR/prompts/*.prompt.yaml) have lower priority than settings prompts
		var globalFilePrompts []configPkg.WebPrompt
		if h.deps.PromptsCache != nil {
			var err error
			globalFilePrompts, err = h.deps.PromptsCache.GetWebPrompts()
			if err != nil && h.deps.Logger != nil {
				h.deps.Logger.Warn("Failed to load global file prompts", "error", err)
			}
		}
		// Merge: settings prompts override global file prompts by name
		// Note: workspace prompts are handled separately via /api/workspace-prompts
		mergedPrompts := configPkg.MergePrompts(globalFilePrompts, h.deps.MittoConfig.Prompts, nil)

		// Filter by session context if session_id is provided
		sessionID := r.URL.Query().Get("session_id")
		if sessionID != "" && h.deps.FilterPromptsForSession != nil {
			mergedPrompts = h.deps.FilterPromptsForSession(mergedPrompts, sessionID)
		}

		response["prompts"] = mergedPrompts

		// Convert ACP servers to JSON-friendly format
		// Include source field so frontend knows which servers are from RC file (read-only)
		// Only include file-based prompts that explicitly list this ACP server in their acps: field
		acpServers := make([]map[string]interface{}, len(h.deps.MittoConfig.ACPServers))
		for i, srv := range h.deps.MittoConfig.ACPServers {
			acpServers[i] = map[string]interface{}{
				"name":         srv.Name,
				"command":      srv.Command,
				"source":       string(srv.Source), // Include source for frontend read-only indication
				"auto_approve": srv.AutoApprove,    // Include auto-approve setting for permissions
				// SECURITY: mask values of keys that look like API keys / tokens / secrets.
				"env":  SanitizeEnvVars(srv.Env),
				"tags": srv.Tags, // Include categorization tags
			}

			// Include constraints if present
			if srv.Constraints != nil {
				acpServers[i]["constraints"] = srv.Constraints
			}

			// Include type if specified (for prompt matching)
			if srv.Type != "" {
				acpServers[i]["type"] = srv.Type
			}

			// Include context-flush command if specified
			if srv.ContextFlushCommand != "" {
				acpServers[i]["context_flush_command"] = srv.ContextFlushCommand
			}

			// Get file-based prompts that explicitly target this ACP server type
			// Only prompts with acps: field containing this server's type are included.
			// If type is not set, the server name is used as the type.
			var filePrompts []configPkg.WebPrompt
			if h.deps.PromptsCache != nil {
				var err error
				acpType := srv.GetType() // Use type (falls back to name)
				filePrompts, err = h.deps.PromptsCache.GetWebPromptsSpecificToACP(acpType)
				if err != nil && h.deps.Logger != nil {
					h.deps.Logger.Warn("Failed to load ACP-specific file prompts",
						"acp_server", srv.Name, "acp_type", acpType, "error", err)
				}
			}

			if len(filePrompts) > 0 {
				acpServers[i]["prompts"] = filePrompts
			}
		}
		response["acp_servers"] = acpServers

		// Include flag indicating if any servers came from RC file
		response["has_rcfile_servers"] = h.deps.HasRCFileServers
	}

	writeJSONWithETag(w, r, response)
}
