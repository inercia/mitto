package web

import (
	"encoding/json"
	"net/http"
	"path/filepath"
	"strings"

	"github.com/inercia/mitto/internal/appdir"
	"github.com/inercia/mitto/internal/config"
	"github.com/inercia/mitto/internal/session"
	"github.com/inercia/mitto/internal/web/handlers"
)

// handleSessions handles GET and POST /api/sessions
func (s *Server) handleSessions(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		s.apiHandlers.HandleListSessions(w, r)
	case http.MethodPost:
		s.apiHandlers.HandleCreateSession(w, r)
	default:
		methodNotAllowed(w)
	}
}

// resolveOwningWorkspace is retained as an alias to the migrated
// handlers.ResolveOwningWorkspace so the existing web-package unit test keeps
// compiling. The create-session handler and its workspace-ownership helpers
// were moved to internal/web/handlers/session_create.go.
var resolveOwningWorkspace = handlers.ResolveOwningWorkspace

// SessionListResponse is an alias for the handlers-package type. The list
// handler was migrated to internal/web/handlers; the alias keeps existing
// references in the web package (e.g. tests) compiling.
type SessionListResponse = handlers.SessionListResponse

// handleSessionDetail handles GET, PATCH, DELETE {prefix}/api/sessions/{id}, GET {prefix}/api/sessions/{id}/events,
// WS {prefix}/api/sessions/{id}/ws, and image operations
func (s *Server) handleSessionDetail(w http.ResponseWriter, r *http.Request) {
	// Extract session ID from path: {prefix}/api/sessions/{id} or {prefix}/api/sessions/{id}/events etc.
	// First strip the API prefix, then strip /api/sessions/
	path := r.URL.Path
	path = strings.TrimPrefix(path, s.apiPrefix)
	path = strings.TrimPrefix(path, "/api/sessions/")
	parts := strings.Split(path, "/")
	if len(parts) == 0 || parts[0] == "" {
		http.Error(w, "Session ID required", http.StatusBadRequest)
		return
	}

	sessionID := parts[0]

	// Validate session ID format to prevent path traversal
	if !IsValidSessionID(sessionID) {
		http.Error(w, "Invalid session ID format", http.StatusBadRequest)
		return
	}

	isEventsRequest := len(parts) > 1 && parts[1] == "events"
	isWSRequest := len(parts) > 1 && parts[1] == "ws"
	isImagesRequest := len(parts) > 1 && parts[1] == "images"
	isFilesRequest := len(parts) > 1 && parts[1] == "files"
	isQueueRequest := len(parts) > 1 && parts[1] == "queue"
	isUserDataRequest := len(parts) > 1 && parts[1] == "user-data"
	isPeriodicRequest := len(parts) > 1 && parts[1] == "periodic"
	isCallbackRequest := len(parts) > 1 && parts[1] == "callback"
	isSettingsRequest := len(parts) > 1 && parts[1] == "settings"
	isPruneRequest := len(parts) > 1 && parts[1] == "prune"
	isChangesRequest := len(parts) > 1 && parts[1] == "changes"

	// Handle WebSocket upgrade for per-session connections
	if isWSRequest {
		s.handleSessionWS(w, r)
		return
	}

	// Handle image operations
	if isImagesRequest {
		// Extract image ID if present: /api/sessions/{id}/images/{imageId}
		imagePath := ""
		if len(parts) > 2 {
			imagePath = parts[2]
		}
		s.apiHandlers.HandleSessionImages(w, r, sessionID, imagePath)
		return
	}

	// Handle file operations
	if isFilesRequest {
		// Extract file ID if present: /api/sessions/{id}/files/{fileId}
		filePath := ""
		if len(parts) > 2 {
			filePath = parts[2]
		}
		s.apiHandlers.HandleSessionFiles(w, r, sessionID, filePath)
		return
	}

	// Handle queue operations
	if isQueueRequest {
		// Extract message ID if present: /api/sessions/{id}/queue/{msgId}
		queuePath := ""
		if len(parts) > 2 {
			queuePath = "/" + parts[2]
		}
		s.apiHandlers.HandleSessionQueue(w, r, sessionID, queuePath)
		return
	}

	// Handle user data operations
	if isUserDataRequest {
		s.apiHandlers.HandleSessionUserData(w, r, sessionID)
		return
	}

	// Handle periodic prompt operations
	if isPeriodicRequest {
		// Check for sub-paths like /periodic/run-now
		periodicSubPath := ""
		if len(parts) > 2 {
			periodicSubPath = parts[2]
		}
		s.apiHandlers.HandleSessionPeriodic(w, r, sessionID, periodicSubPath)
		return
	}

	// Handle callback token operations
	if isCallbackRequest {
		s.apiHandlers.HandleSessionCallback(w, r, sessionID)
		return
	}

	// Handle advanced settings operations
	if isSettingsRequest {
		s.apiHandlers.HandleSessionSettings(w, r, sessionID)
		return
	}

	// Handle prune operations
	if isPruneRequest {
		s.apiHandlers.HandleSessionPrune(w, r, sessionID)
		return
	}

	// Handle git changes operations
	if isChangesRequest {
		s.apiHandlers.HandleSessionChanges(w, r, sessionID)
		return
	}

	switch r.Method {
	case http.MethodGet:
		s.apiHandlers.HandleGetSession(w, r, sessionID, isEventsRequest)
	case http.MethodPatch:
		s.apiHandlers.HandleUpdateSession(w, r, sessionID)
	case http.MethodDelete:
		s.apiHandlers.HandleDeleteSession(w, sessionID)
	default:
		methodNotAllowed(w)
	}
}

// SessionUpdateRequest is an alias for the handlers-package type. The update
// handler was migrated to internal/web/handlers; the alias keeps existing
// references in the web package (e.g. tests) compiling.
type SessionUpdateRequest = handlers.SessionUpdateRequest

// handleWorkspaces handles /api/workspaces
// GET: List all workspaces
// POST: Add a new workspace
// DELETE: Remove a workspace (via query param ?dir=...)
// handleWorkspacePrompts handles GET/POST/DELETE /api/workspace-prompts
//
//   - GET ?dir=...                      Returns workspace prompts (backward-compat)
//   - GET ?dir=...&include_global=true  Returns builtin + workspace prompts merged, all sources
//   - POST                              Create or update a workspace prompt file
//   - DELETE ?dir=...&name=...          Delete a workspace prompt file by name
func (s *Server) handleWorkspacePrompts(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		s.apiHandlers.HandleWorkspacePromptsGET(w, r)
	case http.MethodPost:
		s.apiHandlers.HandleWorkspacePromptsPOST(w, r)
	case http.MethodDelete:
		s.apiHandlers.HandleWorkspacePromptsDELETE(w, r)
	default:
		methodNotAllowed(w)
	}
}

// migrateWorkspacePrompts migrates legacy .md prompt files to .prompt.yaml for
// the workspace's default prompts directory (.mitto/prompts) and any extra
// prompts_dirs declared in .mittorc. Migration is idempotent and serialized via
// promptMigrationMu so concurrent fetches don't race writing the same files;
// only the first caller observes a given migration (afterwards the .prompt.yaml
// already exists and nothing is reported). Returns the files migrated this call.
func (s *Server) migrateWorkspacePrompts(workingDir string) []config.MigratedPrompt {
	if workingDir == "" {
		return nil
	}

	dirs := []string{appdir.WorkspacePromptsDir(workingDir)}
	for _, dir := range s.sessionManager.GetWorkspacePromptsDirs(workingDir) {
		if !filepath.IsAbs(dir) {
			dir = filepath.Join(workingDir, dir)
		}
		dirs = append(dirs, dir)
	}

	s.promptMigrationMu.Lock()
	defer s.promptMigrationMu.Unlock()

	var migrated []config.MigratedPrompt
	seen := make(map[string]bool)
	for _, dir := range dirs {
		if seen[dir] {
			continue
		}
		seen[dir] = true

		m, err := config.MigrateMarkdownPromptsInDir(dir)
		if err != nil && s.logger != nil {
			s.logger.Warn("Failed to migrate legacy prompts", "dir", dir, "error", err)
		}
		if len(m) > 0 && s.logger != nil {
			s.logger.Info("Migrated legacy .md prompts to .prompt.yaml",
				"dir", dir, "count", len(m))
		}
		migrated = append(migrated, m...)
	}
	return migrated
}

// buildPromptEnabledContext creates a CEL evaluation context for the given session.
// Returns nil if session doesn't exist or context cannot be built.
func (s *Server) buildPromptEnabledContext(sessionID string) *config.PromptEnabledContext {
	store := s.Store()
	if store == nil || sessionID == "" {
		return nil
	}

	meta, err := store.GetMetadata(sessionID)
	if err != nil {
		return nil
	}

	ctx := &config.PromptEnabledContext{}

	// Session context
	ctx.Session.ID = meta.SessionID
	ctx.Session.Name = meta.Name
	ctx.Session.IsChild = meta.ParentSessionID != ""
	ctx.Session.IsAutoChild = meta.ChildOrigin == session.ChildOriginAuto
	ctx.Session.ParentID = meta.ParentSessionID
	ctx.Session.BeadsIssue = meta.BeadsIssue
	ctx.Session.HasBeadsIssue = meta.BeadsIssue != ""

	// Periodic conversation type: true when a periodic configuration exists for this
	// conversation (matches the PeriodicEnabled UI mode). Distinct from
	// session.isPeriodic, which marks a scheduler-triggered run.
	if periodic, err := store.Periodic(sessionID).Get(); err == nil && periodic != nil {
		ctx.Session.IsPeriodicConversation = true
	}

	// Parent context (if this is a child)
	if meta.ParentSessionID != "" {
		parentMeta, err := store.GetMetadata(meta.ParentSessionID)
		if err == nil {
			ctx.Parent.Exists = true
			ctx.Parent.Name = parentMeta.Name
			ctx.Parent.ACPServer = parentMeta.ACPServer
		}
	}

	// Children context
	children, err := store.ListChildSessions(sessionID)
	if err == nil {
		ctx.Children.Count = len(children)
		ctx.Children.Exists = len(children) > 0
		for _, child := range children {
			ctx.Children.Names = append(ctx.Children.Names, child.Name)
			ctx.Children.ACPServers = append(ctx.Children.ACPServers, child.ACPServer)
			// Check if child is currently prompting
			isPrompting := false
			if childBS := s.sessionManager.GetSession(child.SessionID); childBS != nil && childBS.IsPrompting() {
				ctx.Children.PromptingCount++
				isPrompting = true
			}
			// Populate structured child info for template funcs ({{ children }}, {{ mcpChildren }})
			childInfo := config.ChildInfo{
				ID:          child.SessionID,
				Name:        child.Name,
				ACPServer:   child.ACPServer,
				Origin:      string(child.ChildOrigin),
				IsPrompting: isPrompting,
			}
			ctx.Children.All = append(ctx.Children.All, childInfo)
			if child.ChildOrigin == session.ChildOriginMCP {
				ctx.Children.MCP = append(ctx.Children.MCP, childInfo)
				ctx.Children.MCPCount++
			}
		}
		ctx.Children.IdleCount = ctx.Children.Count - ctx.Children.PromptingCount
	}

	// ACP context from session metadata
	ctx.ACP.Name = meta.ACPServer
	if s.config.MittoConfig != nil {
		if srv, err := s.config.MittoConfig.GetServer(meta.ACPServer); err == nil {
			ctx.ACP.Type = srv.GetType()
			ctx.ACP.Tags = srv.Tags
			ctx.ACP.AutoApprove = srv.AutoApprove
		}
	}
	// ACP.Available: list of ACP servers with workspaces for this folder.
	// Replicates buildAvailableACPServers from internal/conversation/workspace_registry.go.
	if s.config.MittoConfig != nil && meta.WorkingDir != "" {
		folderWSs := s.sessionManager.GetWorkspacesForFolder(meta.WorkingDir)
		wsServerSet := make(map[string]bool, len(folderWSs))
		for _, ws := range folderWSs {
			wsServerSet[ws.ACPServer] = true
		}
		for _, srv := range s.config.MittoConfig.ACPServers {
			if wsServerSet[srv.Name] {
				ctx.ACP.Available = append(ctx.ACP.Available, config.ACPServerInfo{
					Name:    srv.Name,
					Type:    srv.GetType(),
					Tags:    srv.Tags,
					Current: srv.Name == meta.ACPServer,
				})
			}
		}
	}

	// Workspace context
	ctx.Workspace.Folder = meta.WorkingDir
	if ws := s.sessionManager.GetWorkspace(meta.WorkingDir); ws != nil {
		ctx.Workspace.UUID = ws.UUID
		ctx.Workspace.Name = ws.Name
	}
	// Check if workspace has user data schema; also marshal it for template rendering.
	if schema := s.sessionManager.GetUserDataSchema(meta.WorkingDir); schema != nil && len(schema.Fields) > 0 {
		ctx.Workspace.HasUserDataSchema = true
		if schemaBytes, merr := json.Marshal(schema.Fields); merr == nil {
			ctx.Workspace.UserDataSchemaJSON = string(schemaBytes)
		}
	}

	// Session user data JSON for template rendering ({{ .Session.UserDataJSON }}).
	if ud, uerr := store.GetUserData(sessionID); uerr == nil && ud != nil && len(ud.Attributes) > 0 {
		if udBytes, merr := json.Marshal(ud.Attributes); merr == nil {
			ctx.Session.UserDataJSON = string(udBytes)
		}
	}

	// Tools context - get from auxiliary manager if available
	// (This may be empty if tools haven't been fetched yet)
	if s.auxiliaryManager != nil && ctx.Workspace.UUID != "" {
		if tools, ok := s.auxiliaryManager.GetCachedMCPTools(ctx.Workspace.UUID); ok {
			ctx.Tools.Available = true
			for _, tool := range tools {
				ctx.Tools.Names = append(ctx.Tools.Names, tool.Name)
			}
		}
	}

	// Permissions context - resolve flags with defaults
	ctx.Permissions.CanDoIntrospection = session.GetFlagValue(meta.AdvancedSettings, session.FlagCanDoIntrospection)
	ctx.Permissions.CanSendPrompt = session.GetFlagValue(meta.AdvancedSettings, session.FlagCanSendPrompt)
	ctx.Permissions.CanPromptUser = session.GetFlagValue(meta.AdvancedSettings, session.FlagCanPromptUser)
	ctx.Permissions.CanStartConversation = session.GetFlagValue(meta.AdvancedSettings, session.FlagCanStartConversation)
	ctx.Permissions.CanInteractOtherWorkspaces = session.GetFlagValue(meta.AdvancedSettings, session.FlagCanInteractOtherWorkspaces)
	ctx.Permissions.AutoApprovePermissions = session.GetFlagValue(meta.AdvancedSettings, session.FlagAutoApprovePermissions)

	return ctx
}

// filterPromptsByEnabled filters prompts using enabledWhen CEL expressions.
// Prompts without an expression are always included.
// Fail-open behavior: prompts with invalid or unevaluable CEL expressions are included.
func (s *Server) filterPromptsByEnabled(prompts []config.WebPrompt, ctx *config.PromptEnabledContext) []config.WebPrompt {
	if ctx == nil {
		return prompts // No context, return all prompts
	}

	evaluator := config.GetCELEvaluator()
	if evaluator == nil {
		if s.logger != nil {
			s.logger.Warn("CEL evaluator not available, returning all prompts")
		}
		return prompts
	}

	var filtered []config.WebPrompt
	for _, p := range prompts {
		// --- enabledWhen CEL check ---
		if p.EnabledWhen == "" {
			// No expression — always include
			filtered = append(filtered, p)
			continue
		}

		// Compile the expression (cached)
		compiled, err := evaluator.Compile(p.EnabledWhen)
		if err != nil {
			// Invalid expression - include prompt (fail-open)
			if s.logger != nil {
				s.logger.Warn("Invalid enabledWhen expression",
					"prompt", p.Name,
					"expression", p.EnabledWhen,
					"error", err)
			}
			filtered = append(filtered, p)
			continue
		}

		// Evaluate the expression
		visible, err := evaluator.Evaluate(compiled, ctx)
		if err != nil {
			// Evaluation error - include prompt (fail-open)
			if s.logger != nil {
				s.logger.Warn("Failed to evaluate enabledWhen",
					"prompt", p.Name,
					"expression", p.EnabledWhen,
					"error", err)
			}
			filtered = append(filtered, p)
			continue
		}

		if visible {
			filtered = append(filtered, p)
		} else if s.logger != nil {
			s.logger.Debug("Prompt hidden by enabledWhen expression",
				"prompt", p.Name,
				"expression", p.EnabledWhen)
		}
	}

	return filtered
}

// applyWorkspaceNamespace populates the workspace/ACP/tools namespaces of ctx
// from workingDir, replacing any values already set. These namespaces describe
// the workspace the prompts being evaluated belong to (the `dir` query param),
// which drives dir-based gates (dirExists/fileExists via workspace.folder),
// tools.hasPattern, and acp.*. It is shared by buildWorkspacePromptEnabledContext
// and the session-aware path in handleWorkspacePromptsGET, where the requested
// dir — not the active session's folder — is authoritative for these gates.
func (s *Server) applyWorkspaceNamespace(ctx *config.PromptEnabledContext, workingDir string) {
	// Workspace context (reset so no session-derived values leak through).
	ctx.Workspace.Folder = workingDir
	ctx.Workspace.UUID = ""
	ctx.Workspace.Name = ""
	ctx.Workspace.HasUserDataSchema = false
	var acpServerName string
	if ws := s.sessionManager.GetWorkspace(workingDir); ws != nil {
		ctx.Workspace.UUID = ws.UUID
		ctx.Workspace.Name = ws.Name
		acpServerName = ws.ACPServer
	} else if defaultWs := s.sessionManager.GetDefaultWorkspace(); defaultWs != nil {
		acpServerName = defaultWs.ACPServer
	}
	if schema := s.sessionManager.GetUserDataSchema(workingDir); schema != nil && len(schema.Fields) > 0 {
		ctx.Workspace.HasUserDataSchema = true
	}

	// ACP context
	ctx.ACP.Name = acpServerName
	ctx.ACP.Type = ""
	ctx.ACP.Tags = nil
	ctx.ACP.AutoApprove = false
	if acpServerName != "" && s.config.MittoConfig != nil {
		if srv, err := s.config.MittoConfig.GetServer(acpServerName); err == nil {
			ctx.ACP.Type = srv.GetType()
			ctx.ACP.Tags = srv.Tags
			ctx.ACP.AutoApprove = srv.AutoApprove
		}
	}
	if ctx.ACP.Type == "" {
		ctx.ACP.Type = acpServerName
	}

	// Tools context (workspace-level cache; may be empty if not yet fetched)
	ctx.Tools.Available = false
	ctx.Tools.Names = nil
	if s.auxiliaryManager != nil && ctx.Workspace.UUID != "" {
		if tools, ok := s.auxiliaryManager.GetCachedMCPTools(ctx.Workspace.UUID); ok {
			ctx.Tools.Available = true
			for _, tool := range tools {
				ctx.Tools.Names = append(ctx.Tools.Names, tool.Name)
			}
		}
	}
}

// buildWorkspacePromptEnabledContext creates a session-less PromptEnabledContext
// from the cheaply-available workspace/ACP/tools namespaces and compile-time
// default permission flags. Used by the beads menu paths when no active session
// is available (see handleWorkspacePromptsGET, mitto-gns): it lets enabledWhen
// gates like commandExists("bd")/dirExists(".beads")/tools.hasPattern still be
// evaluated. Callers set ctx.Item afterwards for per-row item.* gating.
func (s *Server) buildWorkspacePromptEnabledContext(workingDir string) *config.PromptEnabledContext {
	ctx := &config.PromptEnabledContext{}

	// Workspace / ACP / tools namespaces describe the workspace these prompts
	// belong to (the requested dir).
	s.applyWorkspaceNamespace(ctx, workingDir)

	// Permissions: use compile-time defaults (no session to override from)
	ctx.Permissions.CanDoIntrospection = session.GetFlagDefault(session.FlagCanDoIntrospection)
	ctx.Permissions.CanSendPrompt = session.GetFlagDefault(session.FlagCanSendPrompt)
	ctx.Permissions.CanPromptUser = session.GetFlagDefault(session.FlagCanPromptUser)
	ctx.Permissions.CanStartConversation = session.GetFlagDefault(session.FlagCanStartConversation)
	ctx.Permissions.CanInteractOtherWorkspaces = session.GetFlagDefault(session.FlagCanInteractOtherWorkspaces)
	ctx.Permissions.AutoApprovePermissions = session.GetFlagDefault(session.FlagAutoApprovePermissions)

	return ctx
}

// loadPromptsFromDirs loads prompts from a list of directories.
// Relative paths are resolved against workspaceRoot.
// Non-existent directories are silently ignored.
// CEL filtering is handled later by filterPromptsByEnabled.
func (s *Server) loadPromptsFromDirs(workspaceRoot string, dirs []string) []config.WebPrompt {
	var allPrompts []config.WebPrompt

	for _, dir := range dirs {
		// Resolve relative paths
		absDir := dir
		if !filepath.IsAbs(dir) {
			absDir = filepath.Join(workspaceRoot, dir)
		}

		// Load prompts from this directory (silently ignore errors)
		prompts, err := config.LoadPromptsFromDir(absDir)
		if err != nil {
			if s.logger != nil {
				s.logger.Debug("Failed to load prompts from directory",
					"dir", absDir,
					"error", err)
			}
			continue
		}

		// Convert to WebPrompts and merge (later dirs override earlier)
		webPrompts := config.PromptsToWebPrompts(prompts)
		allPrompts = config.MergePrompts(nil, allPrompts, webPrompts)
	}

	return allPrompts
}

// getWorkspacePromptsAll returns the full merged prompt list for a working
// directory, using the same resolution pipeline as the workspace-prompts API
// endpoint (without ACP server-specific prompts). Used to validate prompt names
// when the beads upstream == "prompts".
func (s *Server) getWorkspacePromptsAll(workingDir string) []config.WebPrompt {
	// 1. Global file prompts
	var globalFilePrompts []config.WebPrompt
	if s.config.PromptsCache != nil {
		gfp, _ := s.config.PromptsCache.GetWebPrompts()
		globalFilePrompts = gfp
	}

	// 2. Settings file prompts
	var settingsPrompts []config.WebPrompt
	if s.config.MittoConfig != nil {
		settingsPrompts = s.config.MittoConfig.Prompts
	}

	// 3. Workspace directory prompts (.mitto/prompts/*.prompt.yaml)
	var workspacePromptsDirs []string
	workspacePromptsDirs = append(workspacePromptsDirs, appdir.WorkspacePromptsDir(workingDir))
	if s.sessionManager != nil {
		workspacePromptsDirs = append(workspacePromptsDirs, s.sessionManager.GetWorkspacePromptsDirs(workingDir)...)
	}
	dirPrompts := s.loadPromptsFromDirs(workingDir, workspacePromptsDirs)

	// 4. Workspace inline prompts (.mittorc)
	var inlinePrompts []config.WebPrompt
	if s.sessionManager != nil {
		inlinePrompts = s.sessionManager.GetWorkspacePrompts(workingDir)
	}

	return config.MergePrompts(
		config.MergePrompts(globalFilePrompts, settingsPrompts, dirPrompts),
		nil,
		inlinePrompts,
	)
}
