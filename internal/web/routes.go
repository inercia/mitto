package web

import (
	"net/http"

	"github.com/inercia/mitto/internal/web/middleware"
)

// apiRoute describes one server-registered route. Pattern is relative to
// the API prefix (which is prepended at registration time).
type apiRoute struct {
	pattern string       // e.g. "/api/sessions" (NO apiPrefix)
	handler http.Handler // HandlerFunc values wrapped via http.HandlerFunc
}

// apiRoutes returns the declarative route table for all API and WebSocket
// endpoints. Login/logout are included only when authMgr is non-nil.
// Patterns do NOT include the apiPrefix; the caller prepends it.
func (s *Server) apiRoutes(authMgr *middleware.AuthManager, csrfMgr *middleware.CSRFManager, fileServer http.Handler) []apiRoute {
	routes := []apiRoute{}

	// Auth routes — only when authentication is configured.
	if authMgr != nil {
		routes = append(routes,
			apiRoute{"/api/login", http.HandlerFunc(authMgr.HandleLogin)},
			apiRoute{"/api/logout", http.HandlerFunc(authMgr.HandleLogout)},
		)
	}

	// CSRF token endpoint (always available for getting tokens).
	routes = append(routes,
		apiRoute{"/api/csrf-token", http.HandlerFunc(csrfMgr.HandleCSRFToken)},
	)

	// Session endpoints.
	routes = append(routes,
		apiRoute{"/api/sessions", http.HandlerFunc(s.handleSessions)},
		apiRoute{"/api/sessions/running", http.HandlerFunc(s.apiHandlers.HandleRunningSessions)},
		apiRoute{"/api/sessions/", http.HandlerFunc(s.handleSessionDetail)},
	)

	// Workspace endpoints.
	routes = append(routes,
		apiRoute{"/api/workspaces", http.HandlerFunc(s.apiHandlers.HandleWorkspaces)},
		apiRoute{"/api/workspaces/", http.HandlerFunc(s.apiHandlers.HandleWorkspaceDetail)},
		apiRoute{"/api/workspace-prompts", http.HandlerFunc(s.handleWorkspacePrompts)},
		apiRoute{"/api/workspace-prompts/toggle-enabled", http.HandlerFunc(s.apiHandlers.HandleWorkspacePromptsToggleEnabled)},
		apiRoute{"/api/workspace-processors", http.HandlerFunc(s.apiHandlers.HandleWorkspaceProcessors)},
		apiRoute{"/api/workspace-processors/toggle-enabled", http.HandlerFunc(s.apiHandlers.HandleWorkspaceProcessorsToggleEnabled)},
		apiRoute{"/api/workspace-mcp-tools", http.HandlerFunc(s.apiHandlers.HandleWorkspaceMCPTools)},
		apiRoute{"/api/workspace-mcp-install", http.HandlerFunc(s.apiHandlers.HandleWorkspaceMCPInstall)},
		apiRoute{"/api/workspace-mcp-remove", http.HandlerFunc(s.apiHandlers.HandleWorkspaceMCPRemove)},
		apiRoute{"/api/workspace-metadata", http.HandlerFunc(s.apiHandlers.HandleWorkspaceMetadata)},
		apiRoute{"/api/folder-group", http.HandlerFunc(s.apiHandlers.HandleFolderGroup)},
		apiRoute{"/api/workspace/user-data-schema", http.HandlerFunc(s.apiHandlers.HandleWorkspaceUserDataSchema)},
	)

	// Config and discovery endpoints.
	routes = append(routes,
		apiRoute{"/api/config", http.HandlerFunc(s.handleConfig)},
		apiRoute{"/api/agent-types", http.HandlerFunc(s.apiHandlers.HandleAgentTypes)},
		apiRoute{"/api/agents/scan", http.HandlerFunc(s.apiHandlers.HandleScanAgents)},
		apiRoute{"/api/agents/confirm", http.HandlerFunc(s.apiHandlers.HandleConfirmAgents)},
		apiRoute{"/api/supported-runners", http.HandlerFunc(s.apiHandlers.HandleSupportedRunners)},
		apiRoute{"/api/runner-defaults", http.HandlerFunc(s.apiHandlers.HandleRunnerDefaults)},
		apiRoute{"/api/advanced-flags", http.HandlerFunc(s.apiHandlers.HandleAdvancedFlags)},
		apiRoute{"/api/external-status", http.HandlerFunc(s.apiHandlers.HandleExternalStatus)},
	)

	// Auxiliary and notification endpoints.
	routes = append(routes,
		apiRoute{"/api/aux/improve-prompt", http.HandlerFunc(s.apiHandlers.HandleImprovePrompt)},
		apiRoute{"/api/badge-click", http.HandlerFunc(s.apiHandlers.HandleBadgeClick)},
	)

	// Beads (issue tracker) endpoints.
	routes = append(routes,
		apiRoute{"/api/beads/list", http.HandlerFunc(s.apiHandlers.HandleBeadsList)},
		apiRoute{"/api/beads/stats", http.HandlerFunc(s.apiHandlers.HandleBeadsStats)},
		apiRoute{"/api/beads/show", http.HandlerFunc(s.apiHandlers.HandleBeadsShow)},
		apiRoute{"/api/beads/create", http.HandlerFunc(s.apiHandlers.HandleBeadsCreate)},
		apiRoute{"/api/beads/cleanup", http.HandlerFunc(s.apiHandlers.HandleBeadsCleanup)},
		apiRoute{"/api/beads/delete", http.HandlerFunc(s.apiHandlers.HandleBeadsDelete)},
		apiRoute{"/api/beads/status", http.HandlerFunc(s.apiHandlers.HandleBeadsStatus)},
		apiRoute{"/api/beads/update", http.HandlerFunc(s.apiHandlers.HandleBeadsUpdate)},
		apiRoute{"/api/beads/comment", http.HandlerFunc(s.apiHandlers.HandleBeadsComment)},
		apiRoute{"/api/beads/dep", http.HandlerFunc(s.apiHandlers.HandleBeadsDep)},
		apiRoute{"/api/beads/config", http.HandlerFunc(s.apiHandlers.HandleBeadsConfig)},
		apiRoute{"/api/beads/upstream", http.HandlerFunc(s.apiHandlers.HandleBeadsUpstream)},
		apiRoute{"/api/beads/sync", http.HandlerFunc(s.apiHandlers.HandleBeadsSync)},
	)

	// UI preferences.
	routes = append(routes,
		apiRoute{"/api/ui-preferences", http.HandlerFunc(s.apiHandlers.HandleUIPreferences)},
	)

	// File save endpoints — restricted to localhost only (used by native macOS app).
	routes = append(routes,
		apiRoute{"/api/save-file-to-path", http.HandlerFunc(s.apiHandlers.HandleSaveFileToPath)},
		apiRoute{"/api/check-file-exists", http.HandlerFunc(s.apiHandlers.HandleCheckFileExists)},
	)

	// Auth info endpoint (public, used by login page to adapt its UI).
	routes = append(routes,
		apiRoute{"/api/auth-info", http.HandlerFunc(s.apiHandlers.HandleAuthInfo)},
	)

	// Health check endpoint — intentionally NOT behind auth.
	routes = append(routes,
		apiRoute{"/api/health", http.HandlerFunc(s.apiHandlers.HandleHealthCheck)},
	)

	// Callback trigger endpoint (public, no auth required).
	routes = append(routes,
		apiRoute{"/api/callback/", http.HandlerFunc(s.apiHandlers.HandleCallbackTrigger)},
	)

	// File server endpoint — serves files from workspace directories.
	routes = append(routes,
		apiRoute{"/api/files", fileServer},
	)

	// WebSocket endpoints.
	routes = append(routes,
		apiRoute{"/api/events", http.HandlerFunc(s.handleGlobalEventsWS)}, // Global events (session lifecycle)
	)

	return routes
}
