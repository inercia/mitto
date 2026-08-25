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
			// Passkey (WebAuthn) registration (mitto-4mz.3). Handlers return 404
			// when passkeys are not enabled/derivable, and independently require
			// an authenticated mitto_session cookie (not just the shared token
			// or an IP-allowlist bypass) to enroll a credential.
			apiRoute{method: "POST", pattern: "/api/webauthn/register/begin", handler: http.HandlerFunc(authMgr.HandleRegisterBegin)},
			apiRoute{method: "POST", pattern: "/api/webauthn/register/finish", handler: http.HandlerFunc(authMgr.HandleRegisterFinish)},
		)
	}

	// CSRF token endpoint (always available for getting tokens).
	routes = append(routes,
		apiRoute{pattern: "/api/csrf-token", handler: http.HandlerFunc(csrfMgr.HandleCSRFToken)},
	)

	// Session endpoints.
	routes = append(routes,
		apiRoute{pattern: "/api/sessions", handler: http.HandlerFunc(s.apiHandlers.HandleSessionsRoute)},
		apiRoute{method: "GET", pattern: "/api/sessions/running", handler: http.HandlerFunc(s.apiHandlers.HandleRunningSessions)},
		apiRoute{method: "GET", pattern: "/api/sessions/{id}", handler: http.HandlerFunc(s.apiHandlers.HandleSessionGetRoute)},
		apiRoute{method: "PATCH", pattern: "/api/sessions/{id}", handler: http.HandlerFunc(s.apiHandlers.HandleSessionUpdateRoute)},
		apiRoute{method: "DELETE", pattern: "/api/sessions/{id}", handler: http.HandlerFunc(s.apiHandlers.HandleSessionDeleteRoute)},
		apiRoute{method: "GET", pattern: "/api/sessions/{id}/events", handler: http.HandlerFunc(s.apiHandlers.HandleSessionEventsRoute)},
		apiRoute{pattern: "/api/sessions/{id}/ws", handler: http.HandlerFunc(s.handleSessionWS)},
		// Specific sub-resource patterns registered alongside base /api/sessions/{id}.
		apiRoute{pattern: "/api/sessions/{id}/user-data", handler: http.HandlerFunc(s.apiHandlers.HandleSessionUserDataRoute)},
		apiRoute{pattern: "/api/sessions/{id}/callback", handler: http.HandlerFunc(s.apiHandlers.HandleSessionCallbackRoute)},
		apiRoute{pattern: "/api/sessions/{id}/settings", handler: http.HandlerFunc(s.apiHandlers.HandleSessionSettingsRoute)},
		apiRoute{pattern: "/api/sessions/{id}/prune", handler: http.HandlerFunc(s.apiHandlers.HandleSessionPruneRoute)},
		apiRoute{pattern: "/api/sessions/{id}/changes", handler: http.HandlerFunc(s.apiHandlers.HandleSessionChangesRoute)},
		apiRoute{method: "POST", pattern: "/api/sessions/{id}/flush", handler: http.HandlerFunc(s.apiHandlers.HandleSessionFlushRoute)},
		// Sub-resources with an optional trailing sub-ID; the same wrapper handles both.
		apiRoute{pattern: "/api/sessions/{id}/images", handler: http.HandlerFunc(s.apiHandlers.HandleSessionImagesRoute)},
		apiRoute{pattern: "/api/sessions/{id}/images/{imageId}", handler: http.HandlerFunc(s.apiHandlers.HandleSessionImagesRoute)},
		apiRoute{pattern: "/api/sessions/{id}/files", handler: http.HandlerFunc(s.apiHandlers.HandleSessionFilesRoute)},
		apiRoute{pattern: "/api/sessions/{id}/files/{fileId}", handler: http.HandlerFunc(s.apiHandlers.HandleSessionFilesRoute)},
		apiRoute{pattern: "/api/sessions/{id}/queue", handler: http.HandlerFunc(s.apiHandlers.HandleSessionQueueRoute)},
		apiRoute{pattern: "/api/sessions/{id}/queue/{msgId}", handler: http.HandlerFunc(s.apiHandlers.HandleSessionQueueRoute)},
		apiRoute{pattern: "/api/sessions/{id}/queue/{msgId}/{subAction}", handler: http.HandlerFunc(s.apiHandlers.HandleSessionQueueRoute)},
		apiRoute{pattern: "/api/sessions/{id}/loop", handler: http.HandlerFunc(s.apiHandlers.HandleSessionLoopRoute)},
		apiRoute{pattern: "/api/sessions/{id}/loop/{subPath}", handler: http.HandlerFunc(s.apiHandlers.HandleSessionLoopRoute)},
		apiRoute{method: "POST", pattern: "/api/sessions/{id}/ui-prompt/acknowledge", handler: http.HandlerFunc(s.apiHandlers.HandleSessionUIPromptAcknowledgeRoute)},
		apiRoute{method: "GET", pattern: "/api/sessions/{id}/prompt-arg-cache", handler: http.HandlerFunc(s.apiHandlers.HandleSessionPromptArgCacheRoute)},
	)

	// Workspace endpoints.
	routes = append(routes,
		apiRoute{pattern: "/api/workspaces", handler: http.HandlerFunc(s.apiHandlers.HandleWorkspaces)},
		apiRoute{method: "GET", pattern: "/api/workspaces/{uuid}/effective-runner-config", handler: http.HandlerFunc(s.apiHandlers.HandleWorkspaceEffectiveRunnerConfig)},
		apiRoute{method: "POST", pattern: "/api/workspaces/{uuid}/restart-acp", handler: http.HandlerFunc(s.apiHandlers.HandleWorkspaceRestartACP)},
		apiRoute{method: "GET", pattern: "/api/workspaces/{uuid}/acp-status", handler: http.HandlerFunc(s.apiHandlers.HandleWorkspaceACPStatus)},
		apiRoute{method: "GET", pattern: "/api/workspaces/{uuid}/metadata", handler: http.HandlerFunc(s.apiHandlers.HandleWorkspaceMetadata)},
		apiRoute{method: "PUT", pattern: "/api/workspaces/{uuid}/metadata", handler: http.HandlerFunc(s.apiHandlers.HandleWorkspaceMetadata)},
		apiRoute{method: "GET", pattern: "/api/workspaces/{uuid}/user-data-schema", handler: http.HandlerFunc(s.apiHandlers.HandleWorkspaceUserDataSchema)},
		apiRoute{method: "PUT", pattern: "/api/workspaces/{uuid}/user-data-schema", handler: http.HandlerFunc(s.apiHandlers.HandleWorkspaceUserDataSchema)},
		apiRoute{method: "GET", pattern: "/api/workspaces/{uuid}/processors", handler: http.HandlerFunc(s.apiHandlers.HandleWorkspaceProcessors)},
		apiRoute{method: "PATCH", pattern: "/api/workspaces/{uuid}/processors/{name}", handler: http.HandlerFunc(s.apiHandlers.HandleWorkspaceProcessorPatch)},
		apiRoute{method: "PUT", pattern: "/api/workspaces/{uuid}/processors/{name}/arguments", handler: http.HandlerFunc(s.apiHandlers.HandleWorkspaceProcessorArguments)},
		apiRoute{method: "GET", pattern: "/api/workspaces/{uuid}/mcp-tools", handler: http.HandlerFunc(s.apiHandlers.HandleWorkspaceMCPTools)},
		apiRoute{method: "POST", pattern: "/api/workspaces/{uuid}/mcp-tools/install", handler: http.HandlerFunc(s.apiHandlers.HandleWorkspaceMCPInstall)},
		apiRoute{method: "POST", pattern: "/api/workspaces/{uuid}/mcp-tools/remove", handler: http.HandlerFunc(s.apiHandlers.HandleWorkspaceMCPRemove)},
		apiRoute{method: "PUT", pattern: "/api/workspaces/{uuid}/folder-group", handler: http.HandlerFunc(s.apiHandlers.HandleFolderGroup)},
		apiRoute{pattern: "/api/workspace-prompts", handler: http.HandlerFunc(s.apiHandlers.HandleWorkspacePromptsRoute)},
		// Remembered per-argument values for prompt dialogs (mitto-x8v):
		// registered BEFORE the /{name} pattern so the mux doesn't route
		// "remembered-args" as a prompt name.
		apiRoute{method: "GET", pattern: "/api/workspace-prompts/remembered-args", handler: http.HandlerFunc(s.apiHandlers.HandleRememberedArgsGET)},
		apiRoute{method: "PATCH", pattern: "/api/workspace-prompts/{name}", handler: http.HandlerFunc(s.apiHandlers.HandleWorkspacePromptsToggleEnabled)},
		// Workspace file listing — feeds the "filename" prompt parameter type's
		// dropdown. Non-recursive; containment-checked. See mitto-vlg.
		apiRoute{method: "GET", pattern: "/api/workspace-files", handler: http.HandlerFunc(s.apiHandlers.HandleWorkspaceFiles)},
		// Workspace directory listing — feeds the "dirname" prompt parameter
		// type's dropdown. Non-recursive; containment-checked; hidden dirs
		// excluded by default. See mitto-2hw.
		apiRoute{method: "GET", pattern: "/api/workspace-dirs", handler: http.HandlerFunc(s.apiHandlers.HandleWorkspaceDirs)},
	)

	// Config and discovery endpoints.
	routes = append(routes,
		apiRoute{pattern: "/api/config", handler: http.HandlerFunc(s.apiHandlers.HandleConfigRoute)},
		apiRoute{pattern: "/api/agents/types", handler: http.HandlerFunc(s.apiHandlers.HandleAgentTypes)},
		apiRoute{pattern: "/api/agents/scan", handler: http.HandlerFunc(s.apiHandlers.HandleScanAgents)},
		apiRoute{pattern: "/api/agents/confirm", handler: http.HandlerFunc(s.apiHandlers.HandleConfirmAgents)},
		// Guided ACP-server deletion (bead mitto-pgt).
		apiRoute{method: "GET", pattern: "/api/acp-servers/{name}/prepare-delete", handler: http.HandlerFunc(s.apiHandlers.HandleACPServerPrepareDelete)},
		apiRoute{method: "POST", pattern: "/api/acp-servers/{name}/reassign-and-delete", handler: http.HandlerFunc(s.apiHandlers.HandleACPServerReassignAndDelete)},
		apiRoute{pattern: "/api/supported-runners", handler: http.HandlerFunc(s.apiHandlers.HandleSupportedRunners)},
		apiRoute{pattern: "/api/runner-defaults", handler: http.HandlerFunc(s.apiHandlers.HandleRunnerDefaults)},
		apiRoute{pattern: "/api/advanced-flags", handler: http.HandlerFunc(s.apiHandlers.HandleAdvancedFlags)},
		apiRoute{pattern: "/api/external-status", handler: http.HandlerFunc(s.apiHandlers.HandleExternalStatus)},
	)

	// Process-global Slack integration catalog. These paths are intentionally
	// absent from publicAPIPaths; mutation methods are protected by the global
	// authentication and CSRF middleware.
	routes = append(routes,
		apiRoute{method: "GET", pattern: "/api/slack/environment-import", handler: http.HandlerFunc(s.apiHandlers.HandleSlackEnvironmentStatus)},
		apiRoute{method: "POST", pattern: "/api/slack/environment-import", handler: http.HandlerFunc(s.apiHandlers.HandleSlackEnvironmentImport)},
		apiRoute{method: "GET", pattern: "/api/slack/connections", handler: http.HandlerFunc(s.apiHandlers.HandleSlackConnections)},
		apiRoute{method: "GET", pattern: "/api/slack/apps", handler: http.HandlerFunc(s.apiHandlers.HandleSlackAppsList)},
		apiRoute{method: "POST", pattern: "/api/slack/apps", handler: http.HandlerFunc(s.apiHandlers.HandleSlackAppCreate)},
		apiRoute{method: "GET", pattern: "/api/slack/apps/{appId}", handler: http.HandlerFunc(s.apiHandlers.HandleSlackAppGet)},
		apiRoute{method: "PATCH", pattern: "/api/slack/apps/{appId}", handler: http.HandlerFunc(s.apiHandlers.HandleSlackAppPatch)},
		apiRoute{method: "DELETE", pattern: "/api/slack/apps/{appId}", handler: http.HandlerFunc(s.apiHandlers.HandleSlackAppDelete)},
		apiRoute{method: "POST", pattern: "/api/slack/apps/{appId}/validate", handler: http.HandlerFunc(s.apiHandlers.HandleSlackAppValidate)},
		apiRoute{method: "PUT", pattern: "/api/slack/apps/{appId}/token", handler: http.HandlerFunc(s.apiHandlers.HandleSlackAppToken)},
		apiRoute{method: "PUT", pattern: "/api/slack/apps/{appId}/oauth-client", handler: http.HandlerFunc(s.apiHandlers.HandleSlackOAuthClient)},
		apiRoute{method: "POST", pattern: "/api/slack/apps/{appId}/oauth/start", handler: http.HandlerFunc(s.apiHandlers.HandleSlackOAuthCreateStart)},
		apiRoute{method: "GET", pattern: "/api/slack/apps/{appId}/prepare-delete", handler: http.HandlerFunc(s.apiHandlers.HandleSlackAppPrepareDelete)},
		apiRoute{method: "DELETE", pattern: "/api/slack/apps/{appId}/references", handler: http.HandlerFunc(s.apiHandlers.HandleSlackAppReferencesDelete)},
		apiRoute{method: "GET", pattern: "/api/slack/apps/{appId}/installations", handler: http.HandlerFunc(s.apiHandlers.HandleSlackInstallationsList)},
		apiRoute{method: "POST", pattern: "/api/slack/apps/{appId}/installations", handler: http.HandlerFunc(s.apiHandlers.HandleSlackInstallationCreate)},
		apiRoute{method: "GET", pattern: "/api/slack/installations/{installationId}", handler: http.HandlerFunc(s.apiHandlers.HandleSlackInstallationGet)},
		apiRoute{method: "PATCH", pattern: "/api/slack/installations/{installationId}", handler: http.HandlerFunc(s.apiHandlers.HandleSlackInstallationPatch)},
		apiRoute{method: "DELETE", pattern: "/api/slack/installations/{installationId}", handler: http.HandlerFunc(s.apiHandlers.HandleSlackInstallationDelete)},
		apiRoute{method: "POST", pattern: "/api/slack/installations/{installationId}/validate", handler: http.HandlerFunc(s.apiHandlers.HandleSlackInstallationValidate)},
		apiRoute{method: "PUT", pattern: "/api/slack/installations/{installationId}/token", handler: http.HandlerFunc(s.apiHandlers.HandleSlackInstallationToken)},
		apiRoute{method: "POST", pattern: "/api/slack/installations/{installationId}/oauth/start", handler: http.HandlerFunc(s.apiHandlers.HandleSlackOAuthReplaceStart)},
		apiRoute{method: "GET", pattern: "/api/slack/installations/{installationId}/prepare-delete", handler: http.HandlerFunc(s.apiHandlers.HandleSlackInstallationPrepareDelete)},
		apiRoute{method: "DELETE", pattern: "/api/slack/installations/{installationId}/references", handler: http.HandlerFunc(s.apiHandlers.HandleSlackInstallationReferencesDelete)},
		apiRoute{method: "GET", pattern: "/api/slack/installations/{installationId}/channels", handler: http.HandlerFunc(s.apiHandlers.HandleSlackInstallationChannels)},
		apiRoute{method: "GET", pattern: "/api/slack/oauth/config", handler: http.HandlerFunc(s.apiHandlers.HandleSlackOAuthConfig)},
		apiRoute{method: "GET", pattern: "/api/slack/oauth/flows/{flowId}", handler: http.HandlerFunc(s.apiHandlers.HandleSlackOAuthStatus)},
		apiRoute{method: "GET", pattern: "/api/slack/oauth/callback", handler: http.HandlerFunc(s.apiHandlers.HandleSlackOAuthCallback)},
	)

	// Auxiliary and notification endpoints.
	routes = append(routes,
		apiRoute{pattern: "/api/aux/improve-prompt", handler: http.HandlerFunc(s.apiHandlers.HandleImprovePrompt)},
		apiRoute{pattern: "/api/badge-click", handler: http.HandlerFunc(s.apiHandlers.HandleBadgeClick)},
	)

	// Beads (issue tracker) endpoints.
	// Read and core CRUD follow the RESTful /api/issues convention
	// (see docs/devel/rest-api-conventions.md §7.5/§8); the remaining
	// verb-style routes are migrated in later slices.
	routes = append(routes,
		apiRoute{method: "GET", pattern: "/api/issues", handler: http.HandlerFunc(s.apiHandlers.HandleBeadsList)},
		apiRoute{method: "GET", pattern: "/api/issues/stats", handler: http.HandlerFunc(s.apiHandlers.HandleBeadsStats)},
		apiRoute{method: "GET", pattern: "/api/issues/{id}", handler: http.HandlerFunc(s.apiHandlers.HandleBeadsShow)},
		apiRoute{method: "POST", pattern: "/api/issues", handler: http.HandlerFunc(s.apiHandlers.HandleBeadsCreate)},
		apiRoute{method: "PATCH", pattern: "/api/issues/{id}", handler: http.HandlerFunc(s.apiHandlers.HandleBeadsUpdate)},
		apiRoute{method: "DELETE", pattern: "/api/issues/{id}", handler: http.HandlerFunc(s.apiHandlers.HandleBeadsDelete)},
		apiRoute{method: "POST", pattern: "/api/issues/{id}/comments", handler: http.HandlerFunc(s.apiHandlers.HandleBeadsComment)},
		apiRoute{method: "POST", pattern: "/api/issues/{id}/dependencies", handler: http.HandlerFunc(s.apiHandlers.HandleBeadsDep)},
		apiRoute{method: "GET", pattern: "/api/issues/labels", handler: http.HandlerFunc(s.apiHandlers.HandleBeadsLabelsAll)},
		apiRoute{method: "POST", pattern: "/api/issues/{id}/labels", handler: http.HandlerFunc(s.apiHandlers.HandleBeadsLabel)},
		apiRoute{method: "GET", pattern: "/api/issues/config", handler: http.HandlerFunc(s.apiHandlers.HandleBeadsConfig)},
		apiRoute{method: "PUT", pattern: "/api/issues/config", handler: http.HandlerFunc(s.apiHandlers.HandleBeadsConfig)},
		apiRoute{method: "DELETE", pattern: "/api/issues/config", handler: http.HandlerFunc(s.apiHandlers.HandleBeadsConfig)},
		apiRoute{method: "GET", pattern: "/api/issues/upstream", handler: http.HandlerFunc(s.apiHandlers.HandleBeadsUpstream)},
		apiRoute{method: "PUT", pattern: "/api/issues/upstream", handler: http.HandlerFunc(s.apiHandlers.HandleBeadsUpstream)},
		apiRoute{method: "GET", pattern: "/api/issues/database-mode", handler: http.HandlerFunc(s.apiHandlers.HandleBeadsDatabaseMode)},
		apiRoute{method: "PUT", pattern: "/api/issues/database-mode", handler: http.HandlerFunc(s.apiHandlers.HandleBeadsDatabaseMode)},
		// Folder shortcut buttons (folder-native, stored in folders.json).
		apiRoute{method: "GET", pattern: "/api/folders/shortcuts", handler: http.HandlerFunc(s.apiHandlers.HandleFolderShortcuts)},
		apiRoute{method: "PUT", pattern: "/api/folders/shortcuts", handler: http.HandlerFunc(s.apiHandlers.HandleFolderShortcuts)},
		// Folder pin/visibility (folder-native, stored in folders.json).
		apiRoute{method: "GET", pattern: "/api/folders/pin", handler: http.HandlerFunc(s.apiHandlers.HandleFolderPin)},
		apiRoute{method: "PUT", pattern: "/api/folders/pin", handler: http.HandlerFunc(s.apiHandlers.HandleFolderPin)},
		// Global shortcut buttons (stored in settings.json, merged with folder shortcuts at render time).
		apiRoute{method: "GET", pattern: "/api/global/shortcuts", handler: http.HandlerFunc(s.apiHandlers.HandleGlobalShortcuts)},
		apiRoute{method: "PUT", pattern: "/api/global/shortcuts", handler: http.HandlerFunc(s.apiHandlers.HandleGlobalShortcuts)},
		apiRoute{method: "GET", pattern: "/api/global/task-label-colors", handler: http.HandlerFunc(s.apiHandlers.HandleGlobalTaskLabelColors)},
		apiRoute{method: "PUT", pattern: "/api/global/task-label-colors", handler: http.HandlerFunc(s.apiHandlers.HandleGlobalTaskLabelColors)},
		apiRoute{method: "POST", pattern: "/api/issues/cleanup", handler: http.HandlerFunc(s.apiHandlers.HandleBeadsCleanup)},
		apiRoute{method: "POST", pattern: "/api/issues/sync", handler: http.HandlerFunc(s.apiHandlers.HandleBeadsSync)},
		apiRoute{method: "POST", pattern: "/api/issues/{id}/status", handler: http.HandlerFunc(s.apiHandlers.HandleBeadsStatus)},
		// Schema migration — enabled by default; kill-switch via
		// web.beads.allow_migrate_from_ui: false. See mitto-ukl, mitto-erry.
		apiRoute{method: "POST", pattern: "/api/beads/migrate", handler: http.HandlerFunc(s.apiHandlers.HandleBeadsMigrate)},
	)

	// Global dashboard aggregation (epic mitto-aqo).
	routes = append(routes,
		apiRoute{method: "GET", pattern: "/api/dashboard", handler: http.HandlerFunc(s.apiHandlers.HandleDashboard)},
		// Dashboard time-series (epic mitto-a86b, ticket stats.7).
		apiRoute{method: "GET", pattern: "/api/dashboard/timeseries", handler: http.HandlerFunc(s.apiHandlers.HandleDashboardTimeseries)},
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

	// Shared-token rotation (mitto-pscc.9) — restricted to localhost only,
	// like the file-save endpoints above; surfaced via `mitto auth rotate`.
	routes = append(routes,
		apiRoute{method: "POST", pattern: "/api/auth/rotate-token", handler: http.HandlerFunc(s.apiHandlers.HandleRotateSharedToken)},
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
