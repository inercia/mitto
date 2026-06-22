package handlers

import (
	"log/slog"
	"net/http"

	configPkg "github.com/inercia/mitto/internal/config"
)

// ConfigSaveRequest represents the request body for saving configuration.
type ConfigSaveRequest struct {
	Workspaces []configPkg.WorkspaceSettings `json:"workspaces"`
	ACPServers []struct {
		Name        string                                    `json:"name"`
		Command     string                                    `json:"command"`
		Type        string                                    `json:"type,omitempty"` // Optional type for prompt matching
		Env         map[string]string                         `json:"env,omitempty"`  // Environment variables
		Prompts     []configPkg.WebPrompt                     `json:"prompts,omitempty"`
		Source      configPkg.ConfigItemSource                `json:"source,omitempty"`       // Source of the server (rcfile, settings)
		AutoApprove bool                                      `json:"auto_approve,omitempty"` // Auto-approve permission requests
		Tags        []string                                  `json:"tags,omitempty"`         // Optional categorization tags
		Constraints map[string]*configPkg.ACPServerConstraint `json:"constraints,omitempty"`  // Config option auto-selection rules
	} `json:"acp_servers"`
	// Prompts is the top-level list of global prompts
	Prompts []configPkg.WebPrompt `json:"prompts,omitempty"`
	// Web is a pointer so the backend can distinguish "section omitted" (preserve the
	// existing web/auth/host/port config — e.g. the Workspaces dialog, which must never
	// touch external-access auth) from "section present" (apply it — the Settings dialog,
	// which always sends a complete web object).
	Web *struct {
		Host         string `json:"host,omitempty"`
		ExternalPort int    `json:"external_port,omitempty"`
		Auth         *struct {
			Simple *struct {
				Username string `json:"username"`
				Password string `json:"password"`
			} `json:"simple,omitempty"`
			Cloudflare *struct {
				TeamDomain string `json:"team_domain"`
				Audience   string `json:"audience"`
			} `json:"cloudflare,omitempty"`
		} `json:"auth,omitempty"`
		Hooks     *configPkg.WebHooks        `json:"hooks,omitempty"`
		AccessLog *configPkg.AccessLogConfig `json:"access_log,omitempty"`
	} `json:"web,omitempty"`
	UI            *configPkg.UIConfig            `json:"ui,omitempty"`
	Conversations *configPkg.ConversationsConfig `json:"conversations,omitempty"`
	Session       *configPkg.SessionConfig       `json:"session,omitempty"`
	Permissions   *configPkg.PermissionsConfig   `json:"permissions,omitempty"`
	// ServerRenames maps old ACP server names to their new names. The UI sends
	// this when a server is renamed in place so the backend can migrate the
	// stored ACPServer of existing conversations (otherwise they would be
	// orphaned and fail to resume with "empty command").
	ServerRenames map[string]string `json:"server_renames,omitempty"`
}

// HandleSaveConfig handles POST /api/config.
//
// The server-coupled operations (validation, settings construction, and
// applying changes to the running server) are delegated to the web package via
// Deps closures, since they mutate server lifecycle state (auth manager,
// external listener, in-memory config). This handler owns only the HTTP
// orchestration: read-only gate, body parsing, error/response formatting.
func (h *Handlers) HandleSaveConfig(w http.ResponseWriter, r *http.Request) {
	// Reject saves when config is read-only (loaded from --config file)
	if h.deps.ConfigReadOnly {
		http.Error(w, "Configuration is read-only (loaded from config file)", http.StatusForbidden)
		return
	}

	var req ConfigSaveRequest
	if !parseJSONBody(w, r, &req) {
		return
	}

	// DEBUG: Log UI config received (always log to slog for debugging)
	if req.UI != nil {
		slog.Info("Config save: UI config received",
			"ui", req.UI,
			"mac", req.UI.Mac,
		)
		if req.UI.Mac != nil && req.UI.Mac.Notifications != nil {
			slog.Info("Config save: Notifications config",
				"native_enabled", req.UI.Mac.Notifications.NativeEnabled,
				"sounds", req.UI.Mac.Notifications.Sounds,
			)
		}
	} else {
		slog.Info("Config save: UI config is nil")
	}

	// Validate request structure, check workspace conflicts, normalize default
	// workspaces, and validate restricted runners. The closure writes any error
	// response itself and returns false when the request must be rejected.
	if h.deps.ValidateAndPrepareConfig == nil || !h.deps.ValidateAndPrepareConfig(w, &req) {
		return
	}

	// Build new settings (also stores password in Keychain on macOS)
	settings, err := h.deps.BuildNewSettings(&req)
	if err != nil {
		if h.deps.Logger != nil {
			h.deps.Logger.Error("Failed to build settings", "error", err)
		}
		http.Error(w, "Failed to build settings: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// DEBUG: Log settings before save
	if h.deps.Logger != nil {
		h.deps.Logger.Info("Config save: Settings to save",
			"ui", settings.UI,
			"ui.mac", settings.UI.Mac,
		)
		if settings.UI.Mac != nil && settings.UI.Mac.Notifications != nil {
			h.deps.Logger.Info("Config save: Settings notifications",
				"native_enabled", settings.UI.Mac.Notifications.NativeEnabled,
			)
		}
	}

	// Save settings to disk
	if err := configPkg.SaveSettings(settings); err != nil {
		if h.deps.Logger != nil {
			h.deps.Logger.Error("Failed to save settings", "error", err)
		}
		http.Error(w, "Failed to save settings: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// Apply changes to running server
	h.deps.ApplyConfigChanges(&req, settings)

	// Build response with applied changes info
	authEnabled := false
	if h.deps.AuthEnabled != nil {
		authEnabled = h.deps.AuthEnabled()
	}
	externalRunning := false
	if h.deps.IsExternalListenerRunning != nil {
		externalRunning = h.deps.IsExternalListenerRunning()
	}
	externalPort := 0
	if h.deps.GetExternalPort != nil {
		externalPort = h.deps.GetExternalPort()
	}
	writeJSONOK(w, map[string]interface{}{
		"success": true,
		"message": "Configuration saved successfully",
		"applied": map[string]interface{}{
			"external_access_enabled": externalRunning,
			"external_port":           externalPort,
			"auth_enabled":            authEnabled,
		},
	})
}
