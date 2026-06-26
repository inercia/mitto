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
	method  string       // optional HTTP method qualifier (e.g. "GET", "POST"); empty = any method
}

// apiRoutes returns the declarative route table for all API and WebSocket
// endpoints. Login/logout are included only when authMgr is non-nil.
// Patterns do NOT include the apiPrefix; the caller prepends it.
func (s *Server) apiRoutes(authMgr *middleware.AuthManager, csrfMgr *middleware.CSRFManager, fileServer http.Handler) []apiRoute {
	routes := []apiRoute{}

	// Auth routes — only when authentication is configured.
	if authMgr != nil {
		routes = append(routes,
			apiRoute{pattern: "/api/login", handler: http.HandlerFunc(authMgr.HandleLogin)},
			apiRoute{pattern: "/api/logout", handler: http.HandlerFunc(authMgr.HandleLogout)},
		)
	}

	// CSRF token endpoint (always available for getting tokens).
	routes = append(routes,
		apiRoute{pattern: "/api/csrf-token", handler: http.HandlerFunc(csrfMgr.HandleCSRFToken)},
	)

	// Session endpoints.
	routes = append(routes,
		apiRoute{pattern: "/api/sessions", handler: http.HandlerFunc(s.handleSessions)},
		apiRoute{method: "GET", pattern: "/api/sessions/running", handler: http.HandlerFunc(s.apiHandlers.HandleRunningSessions)},
		apiRoute{method: "GET", pattern: "/api/sessions/{id}", handler: http.HandlerFunc(s.handleSessionGet)},
		apiRoute{method: "PATCH", pattern: "/api/sessions/{id}", handler: http.HandlerFunc(s.handleSessionUpdate)},
		apiRoute{method: "DELETE", pattern: "/api/sessions/{id}", handler: http.HandlerFunc(s.handleSessionDelete)},
		apiRoute{method: "GET", pattern: "/api/sessions/{id}/events", handler: http.HandlerFunc(s.handleSessionEvents)},
		apiRoute{pattern: "/api/sessions/{id}/ws", handler: http.HandlerFunc(s.handleSessionWS)},
		// Specific sub-resource patterns registered alongside base /api/sessions/{id}.
		apiRoute{pattern: "/api/sessions/{id}/user-data", handler: http.HandlerFunc(s.handleSessionUserData)},
		apiRoute{pattern: "/api/sessions/{id}/callback", handler: http.HandlerFunc(s.handleSessionCallbackRoute)},
		apiRoute{pattern: "/api/sessions/{id}/settings", handler: http.HandlerFunc(s.handleSessionSettings)},
		apiRoute{pattern: "/api/sessions/{id}/prune", handler: http.HandlerFunc(s.handleSessionPrune)},
		apiRoute{pattern: "/api/sessions/{id}/changes", handler: http.HandlerFunc(s.handleSessionChanges)},
		// Sub-resources with an optional trailing sub-ID; the same wrapper handles both.
		apiRoute{pattern: "/api/sessions/{id}/images", handler: http.HandlerFunc(s.handleSessionImages)},
		apiRoute{pattern: "/api/sessions/{id}/images/{imageId}", handler: http.HandlerFunc(s.handleSessionImages)},
		apiRoute{pattern: "/api/sessions/{id}/files", handler: http.HandlerFunc(s.handleSessionFiles)},
		apiRoute{pattern: "/api/sessions/{id}/files/{fileId}", handler: http.HandlerFunc(s.handleSessionFiles)},
		apiRoute{pattern: "/api/sessions/{id}/queue", handler: http.HandlerFunc(s.handleSessionQueue)},
		apiRoute{pattern: "/api/sessions/{id}/queue/{msgId}", handler: http.HandlerFunc(s.handleSessionQueue)},
		apiRoute{pattern: "/api/sessions/{id}/periodic", handler: http.HandlerFunc(s.handleSessionPeriodic)},
		apiRoute{pattern: "/api/sessions/{id}/periodic/{subPath}", handler: http.HandlerFunc(s.handleSessionPeriodic)},
	)

	// Workspace endpoints.
	routes = append(routes,
		apiRoute{pattern: "/api/workspaces", handler: http.HandlerFunc(s.apiHandlers.HandleWorkspaces)},
		apiRoute{method: "GET", pattern: "/api/workspaces/{uuid}/effective-runner-config", handler: http.HandlerFunc(s.apiHandlers.HandleWorkspaceEffectiveRunnerConfig)},
		apiRoute{method: "POST", pattern: "/api/workspaces/{uuid}/restart-acp", handler: http.HandlerFunc(s.apiHandlers.HandleWorkspaceRestartACP)},
		apiRoute{method: "GET", pattern: "/api/workspaces/{uuid}/metadata", handler: http.HandlerFunc(s.apiHandlers.HandleWorkspaceMetadata)},
		apiRoute{method: "PUT", pattern: "/api/workspaces/{uuid}/metadata", handler: http.HandlerFunc(s.apiHandlers.HandleWorkspaceMetadata)},
		apiRoute{method: "GET", pattern: "/api/workspaces/{uuid}/user-data-schema", handler: http.HandlerFunc(s.apiHandlers.HandleWorkspaceUserDataSchema)},
		apiRoute{method: "PUT", pattern: "/api/workspaces/{uuid}/user-data-schema", handler: http.HandlerFunc(s.apiHandlers.HandleWorkspaceUserDataSchema)},
		apiRoute{method: "GET", pattern: "/api/workspaces/{uuid}/processors", handler: http.HandlerFunc(s.apiHandlers.HandleWorkspaceProcessors)},
		apiRoute{method: "PATCH", pattern: "/api/workspaces/{uuid}/processors/{name}", handler: http.HandlerFunc(s.apiHandlers.HandleWorkspaceProcessorPatch)},
		apiRoute{method: "GET", pattern: "/api/workspaces/{uuid}/mcp-tools", handler: http.HandlerFunc(s.apiHandlers.HandleWorkspaceMCPTools)},
		apiRoute{method: "POST", pattern: "/api/workspaces/{uuid}/mcp-tools/install", handler: http.HandlerFunc(s.apiHandlers.HandleWorkspaceMCPInstall)},
		apiRoute{method: "POST", pattern: "/api/workspaces/{uuid}/mcp-tools/remove", handler: http.HandlerFunc(s.apiHandlers.HandleWorkspaceMCPRemove)},
		apiRoute{method: "PUT", pattern: "/api/workspaces/{uuid}/folder-group", handler: http.HandlerFunc(s.apiHandlers.HandleFolderGroup)},
		apiRoute{pattern: "/api/workspace-prompts", handler: http.HandlerFunc(s.handleWorkspacePrompts)},
		apiRoute{pattern: "/api/workspace-prompts/toggle-enabled", handler: http.HandlerFunc(s.apiHandlers.HandleWorkspacePromptsToggleEnabled)},
	)

	// Config and discovery endpoints.
	routes = append(routes,
		apiRoute{pattern: "/api/config", handler: http.HandlerFunc(s.handleConfig)},
		apiRoute{pattern: "/api/agent-types", handler: http.HandlerFunc(s.apiHandlers.HandleAgentTypes)},
		apiRoute{pattern: "/api/agents/scan", handler: http.HandlerFunc(s.apiHandlers.HandleScanAgents)},
		apiRoute{pattern: "/api/agents/confirm", handler: http.HandlerFunc(s.apiHandlers.HandleConfirmAgents)},
		apiRoute{pattern: "/api/supported-runners", handler: http.HandlerFunc(s.apiHandlers.HandleSupportedRunners)},
		apiRoute{pattern: "/api/runner-defaults", handler: http.HandlerFunc(s.apiHandlers.HandleRunnerDefaults)},
		apiRoute{pattern: "/api/advanced-flags", handler: http.HandlerFunc(s.apiHandlers.HandleAdvancedFlags)},
		apiRoute{pattern: "/api/external-status", handler: http.HandlerFunc(s.apiHandlers.HandleExternalStatus)},
	)

	// Auxiliary and notification endpoints.
	routes = append(routes,
		apiRoute{pattern: "/api/aux/improve-prompt", handler: http.HandlerFunc(s.apiHandlers.HandleImprovePrompt)},
		apiRoute{pattern: "/api/badge-click", handler: http.HandlerFunc(s.apiHandlers.HandleBadgeClick)},
	)

	// Beads (issue tracker) endpoints.
	routes = append(routes,
		apiRoute{pattern: "/api/beads/list", handler: http.HandlerFunc(s.apiHandlers.HandleBeadsList)},
		apiRoute{pattern: "/api/beads/stats", handler: http.HandlerFunc(s.apiHandlers.HandleBeadsStats)},
		apiRoute{pattern: "/api/beads/show", handler: http.HandlerFunc(s.apiHandlers.HandleBeadsShow)},
		apiRoute{pattern: "/api/beads/create", handler: http.HandlerFunc(s.apiHandlers.HandleBeadsCreate)},
		apiRoute{pattern: "/api/beads/cleanup", handler: http.HandlerFunc(s.apiHandlers.HandleBeadsCleanup)},
		apiRoute{pattern: "/api/beads/delete", handler: http.HandlerFunc(s.apiHandlers.HandleBeadsDelete)},
		apiRoute{pattern: "/api/beads/status", handler: http.HandlerFunc(s.apiHandlers.HandleBeadsStatus)},
		apiRoute{pattern: "/api/beads/update", handler: http.HandlerFunc(s.apiHandlers.HandleBeadsUpdate)},
		apiRoute{pattern: "/api/beads/comment", handler: http.HandlerFunc(s.apiHandlers.HandleBeadsComment)},
		apiRoute{pattern: "/api/beads/dep", handler: http.HandlerFunc(s.apiHandlers.HandleBeadsDep)},
		apiRoute{pattern: "/api/beads/config", handler: http.HandlerFunc(s.apiHandlers.HandleBeadsConfig)},
		apiRoute{pattern: "/api/beads/upstream", handler: http.HandlerFunc(s.apiHandlers.HandleBeadsUpstream)},
		apiRoute{pattern: "/api/beads/sync", handler: http.HandlerFunc(s.apiHandlers.HandleBeadsSync)},
	)

	// UI preferences.
	routes = append(routes,
		apiRoute{pattern: "/api/ui-preferences", handler: http.HandlerFunc(s.apiHandlers.HandleUIPreferences)},
	)

	// File save endpoints — restricted to localhost only (used by native macOS app).
	routes = append(routes,
		apiRoute{pattern: "/api/save-file-to-path", handler: http.HandlerFunc(s.apiHandlers.HandleSaveFileToPath)},
		apiRoute{pattern: "/api/check-file-exists", handler: http.HandlerFunc(s.apiHandlers.HandleCheckFileExists)},
	)

	// Auth info endpoint (public, used by login page to adapt its UI).
	routes = append(routes,
		apiRoute{pattern: "/api/auth-info", handler: http.HandlerFunc(s.apiHandlers.HandleAuthInfo)},
	)

	// Health check endpoint — intentionally NOT behind auth.
	routes = append(routes,
		apiRoute{pattern: "/api/health", handler: http.HandlerFunc(s.apiHandlers.HandleHealthCheck)},
	)

	// Callback trigger endpoint (public, no auth required).
	routes = append(routes,
		apiRoute{pattern: "/api/callback/", handler: http.HandlerFunc(s.apiHandlers.HandleCallbackTrigger)},
	)

	// File server endpoint — serves files from workspace directories.
	routes = append(routes,
		apiRoute{pattern: "/api/files", handler: fileServer},
	)

	// WebSocket endpoints.
	routes = append(routes,
		apiRoute{pattern: "/api/events", handler: http.HandlerFunc(s.handleGlobalEventsWS)}, // Global events (session lifecycle)
	)

	return routes
}
