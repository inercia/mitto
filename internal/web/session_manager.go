package web

import (
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/inercia/mitto/internal/config"
	"github.com/inercia/mitto/internal/mcpserver"
	"github.com/inercia/mitto/internal/msghooks"
	"github.com/inercia/mitto/internal/runner"
	"github.com/inercia/mitto/internal/session"
)

// MaxSessions is the maximum number of concurrent sessions allowed.
const MaxSessions = 32

// ErrTooManySessions is returned when the session limit is reached.
var ErrTooManySessions = errors.New("maximum number of sessions reached")

// WorkspaceSaveFunc is a callback function called when workspaces are modified.
// It receives the current list of workspaces to be persisted.
type WorkspaceSaveFunc func(workspaces []config.WorkspaceSettings) error

// SessionManager manages background sessions that run independently of WebSocket connections.
// It is safe for concurrent use.
type SessionManager struct {
	mu       sync.RWMutex
	sessions map[string]*BackgroundSession // keyed by persisted session ID
	logger   *slog.Logger

	// Workspaces configuration - maps workspace UUID to workspace config.
	// Using UUID as key allows multiple workspaces to share the same working directory
	// (e.g., same project folder with different ACP servers like Claude vs Gemini).
	workspaces map[string]*config.WorkspaceSettings

	// Default workspace (used when no specific workspace is requested)
	defaultWorkspace *config.WorkspaceSettings

	// fromCLI indicates whether workspaces came from CLI flags.
	// When true, workspace changes are NOT persisted to disk.
	fromCLI bool

	// onWorkspaceSave is called when workspaces are modified (only if fromCLI is false).
	onWorkspaceSave WorkspaceSaveFunc

	// autoApprove enables automatic approval of permission requests.
	autoApprove bool

	// store is the session store for persistence.
	store *session.Store

	// workspaceRCCache provides cached access to workspace-specific .mittorc files.
	workspaceRCCache *config.WorkspaceRCCache

	// globalConversations contains global conversation processing configuration.
	globalConversations *config.ConversationsConfig

	// globalRestrictedRunners contains per-runner-type global configuration.
	globalRestrictedRunners map[string]*config.WorkspaceRunnerConfig

	// mittoConfig contains the full Mitto configuration (for looking up agent configs).
	mittoConfig *config.Config

	// hookManager manages external command hooks for message transformation.
	hookManager *msghooks.Manager

	// apiPrefix is the URL prefix for API endpoints (e.g., "/mitto").
	// Used to generate HTTP file links for web browser access.
	apiPrefix string

	// eventsManager is used to broadcast global events to all connected clients.
	eventsManager *GlobalEventsManager

	// planStateMu protects planState map.
	planStateMu sync.RWMutex
	// planState caches the last known agent plan entries per session.
	// This is in-memory only (not persisted to disk) and survives conversation switches
	// within the same server session. Automatically cleared on server restart.
	// Used to restore the agent plan panel when switching back to a conversation.
	planState map[string][]PlanEntry

	// mcpServer is the global MCP server for session registration.
	// Sessions register with this server to enable session-scoped MCP tools.
	mcpServer *mcpserver.Server
}

// NewSessionManager creates a new session manager with a single workspace configuration.
// This is used when running from CLI with explicit --acp-command and --acp-server flags.
func NewSessionManager(acpCommand, acpServer string, autoApprove bool, logger *slog.Logger) *SessionManager {
	// Create a default workspace from the provided configuration
	defaultWS := &config.WorkspaceSettings{
		ACPCommand: acpCommand,
		ACPServer:  acpServer,
		WorkingDir: "", // Will be set at session creation time
	}
	return &SessionManager{
		sessions:         make(map[string]*BackgroundSession),
		workspaces:       make(map[string]*config.WorkspaceSettings),
		logger:           logger,
		defaultWorkspace: defaultWS,
		autoApprove:      autoApprove,
		workspaceRCCache: config.NewWorkspaceRCCache(30 * time.Second),
		planState:        make(map[string][]PlanEntry),
	}
}

// SessionManagerOptions contains options for creating a SessionManager.
type SessionManagerOptions struct {
	// Workspaces is the initial list of workspaces.
	Workspaces []config.WorkspaceSettings
	// AutoApprove enables automatic approval of permission requests.
	AutoApprove bool
	// Logger is the logger to use.
	Logger *slog.Logger
	// FromCLI indicates whether workspaces came from CLI flags.
	// When true, workspace changes are NOT persisted to disk.
	FromCLI bool
	// OnWorkspaceSave is called when workspaces are modified (only if FromCLI is false).
	OnWorkspaceSave WorkspaceSaveFunc
	// APIPrefix is the URL prefix for API endpoints (e.g., "/mitto").
	// Used to generate HTTP file links for web browser access.
	APIPrefix string
}

// NewSessionManagerWithOptions creates a new session manager with the given options.
// Workspaces without UUIDs will have UUIDs generated automatically.
func NewSessionManagerWithOptions(opts SessionManagerOptions) *SessionManager {
	sm := &SessionManager{
		sessions:         make(map[string]*BackgroundSession),
		workspaces:       make(map[string]*config.WorkspaceSettings),
		logger:           opts.Logger,
		autoApprove:      opts.AutoApprove,
		fromCLI:          opts.FromCLI,
		onWorkspaceSave:  opts.OnWorkspaceSave,
		workspaceRCCache: config.NewWorkspaceRCCache(30 * time.Second),
		apiPrefix:        opts.APIPrefix,
		planState:        make(map[string][]PlanEntry),
	}

	for i := range opts.Workspaces {
		ws := &opts.Workspaces[i]
		ws.EnsureUUID() // Ensure workspace has a UUID
		sm.workspaces[ws.UUID] = ws
		if sm.defaultWorkspace == nil {
			sm.defaultWorkspace = ws
		}
	}

	return sm
}

// SetGlobalConversations sets the global conversation processing configuration.
// This is merged with workspace-specific configurations when creating sessions.
func (sm *SessionManager) SetGlobalConversations(conv *config.ConversationsConfig) {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	sm.globalConversations = conv
}

// SetHookManager sets the hook manager for external command msghooks.
func (sm *SessionManager) SetHookManager(hm *msghooks.Manager) {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	sm.hookManager = hm
}

// SetAPIPrefix sets the API prefix for HTTP file links.
func (sm *SessionManager) SetAPIPrefix(prefix string) {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	sm.apiPrefix = prefix
}

// SetWorkspaces sets the available workspaces.
// Workspaces without UUIDs will have UUIDs generated automatically.
func (sm *SessionManager) SetWorkspaces(workspaces []config.WorkspaceSettings) {
	sm.mu.Lock()

	sm.workspaces = make(map[string]*config.WorkspaceSettings)
	sm.defaultWorkspace = nil

	for i := range workspaces {
		ws := &workspaces[i]
		ws.EnsureUUID() // Ensure workspace has a UUID
		sm.workspaces[ws.UUID] = ws
		if sm.defaultWorkspace == nil {
			sm.defaultWorkspace = ws
		}
	}

	// Save workspaces if not from CLI and callback is set
	shouldSave := !sm.fromCLI && sm.onWorkspaceSave != nil
	var workspacesToSave []config.WorkspaceSettings
	if shouldSave {
		workspacesToSave = sm.getWorkspacesLocked()
	}
	sm.mu.Unlock()

	if shouldSave {
		if err := sm.onWorkspaceSave(workspacesToSave); err != nil && sm.logger != nil {
			sm.logger.Error("Failed to save workspaces", "error", err)
		}
	}
}

// GetWorkspaces returns all configured workspaces.
func (sm *SessionManager) GetWorkspaces() []config.WorkspaceSettings {
	sm.mu.RLock()
	defer sm.mu.RUnlock()

	if len(sm.workspaces) == 0 {
		// Return default workspace if it has valid configuration
		if sm.defaultWorkspace != nil && sm.defaultWorkspace.ACPCommand != "" {
			return []config.WorkspaceSettings{*sm.defaultWorkspace}
		}
		// No valid workspace configuration - return empty slice
		return []config.WorkspaceSettings{}
	}

	result := make([]config.WorkspaceSettings, 0, len(sm.workspaces))
	for _, ws := range sm.workspaces {
		result = append(result, *ws)
	}
	return result
}

// GetWorkspace returns the first workspace matching the given directory.
// If multiple workspaces share the same directory (with different ACP servers),
// this returns the first one found. Use GetWorkspaceByDirAndACP for a specific match.
func (sm *SessionManager) GetWorkspace(workingDir string) *config.WorkspaceSettings {
	sm.mu.RLock()
	defer sm.mu.RUnlock()

	for _, ws := range sm.workspaces {
		if ws.WorkingDir == workingDir {
			return ws
		}
	}
	return nil
}

// GetWorkspaceByDirAndACP returns the workspace matching both directory and ACP server.
// This is used when multiple workspaces share the same folder but use different ACP servers.
// If acpServer is empty, returns the first workspace matching the directory.
func (sm *SessionManager) GetWorkspaceByDirAndACP(workingDir, acpServer string) *config.WorkspaceSettings {
	sm.mu.RLock()
	defer sm.mu.RUnlock()

	for _, ws := range sm.workspaces {
		if ws.WorkingDir == workingDir {
			if acpServer == "" || ws.ACPServer == acpServer {
				return ws
			}
		}
	}
	return nil
}

// GetWorkspaceByUUID returns the workspace with the given UUID.
// Returns nil if no workspace with that UUID exists.
func (sm *SessionManager) GetWorkspaceByUUID(uuid string) *config.WorkspaceSettings {
	sm.mu.RLock()
	defer sm.mu.RUnlock()

	if ws, ok := sm.workspaces[uuid]; ok {
		return ws
	}
	// Also check default workspace
	if sm.defaultWorkspace != nil && sm.defaultWorkspace.UUID == uuid {
		return sm.defaultWorkspace
	}
	return nil
}

// ResolveWorkspaceIdentifier resolves a workspace UUID to its WorkingDir.
// Returns the working directory and true if found, empty string and false otherwise.
func (sm *SessionManager) ResolveWorkspaceIdentifier(uuid string) (string, bool) {
	sm.mu.RLock()
	defer sm.mu.RUnlock()

	// Find workspace by UUID - prefer workspaces with non-empty WorkingDir
	for _, ws := range sm.workspaces {
		if ws.UUID == uuid && ws.WorkingDir != "" {
			return ws.WorkingDir, true
		}
	}

	// Check default workspace if it has a non-empty WorkingDir
	if sm.defaultWorkspace != nil && sm.defaultWorkspace.UUID == uuid && sm.defaultWorkspace.WorkingDir != "" {
		return sm.defaultWorkspace.WorkingDir, true
	}

	// Fall back to active sessions - this handles the case where sessions are created
	// with a working directory that's not a registered workspace (e.g., CLI usage).
	// The session inherits the default workspace's UUID but has its own working directory.
	for _, bs := range sm.sessions {
		if bs.workspaceUUID == uuid && bs.workingDir != "" {
			return bs.workingDir, true
		}
	}

	// If we found the UUID but all working dirs are empty, still return success
	// to indicate the UUID is valid (even if we can't resolve to a directory)
	for _, ws := range sm.workspaces {
		if ws.UUID == uuid {
			return ws.WorkingDir, true
		}
	}
	if sm.defaultWorkspace != nil && sm.defaultWorkspace.UUID == uuid {
		return sm.defaultWorkspace.WorkingDir, true
	}

	return "", false
}

// GetDefaultWorkspace returns the default workspace.
// Returns nil if no default workspace is configured.
func (sm *SessionManager) GetDefaultWorkspace() *config.WorkspaceSettings {
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	return sm.defaultWorkspace
}

// GetWorkspacePrompts returns prompts defined in the workspace's .mittorc file.
// Returns nil if no .mittorc exists or if it has no prompts section.
func (sm *SessionManager) GetWorkspacePrompts(workingDir string) []config.WebPrompt {
	if sm.workspaceRCCache == nil || workingDir == "" {
		return nil
	}

	rc, err := sm.workspaceRCCache.Get(workingDir)
	if err != nil {
		if sm.logger != nil {
			sm.logger.Warn("Failed to load workspace .mittorc",
				"working_dir", workingDir,
				"error", err)
		}
		return nil
	}

	if rc == nil {
		return nil
	}

	return rc.Prompts
}

// GetWorkspacePromptsDirs returns the prompts_dirs defined in the workspace's .mittorc file.
// Returns nil if no .mittorc exists or if it has no prompts_dirs section.
func (sm *SessionManager) GetWorkspacePromptsDirs(workingDir string) []string {
	if sm.workspaceRCCache == nil || workingDir == "" {
		return nil
	}

	rc, err := sm.workspaceRCCache.Get(workingDir)
	if err != nil {
		return nil
	}

	if rc == nil {
		return nil
	}

	return rc.PromptsDirs
}

// GetWorkspaceRCLastModified returns the last modification time of the workspace's .mittorc file.
// Returns zero time if the file doesn't exist or the cache is not initialized.
func (sm *SessionManager) GetWorkspaceRCLastModified(workingDir string) time.Time {
	if sm.workspaceRCCache == nil || workingDir == "" {
		return time.Time{}
	}
	return sm.workspaceRCCache.GetLastModified(workingDir)
}

// GetUserDataSchema returns the user data schema defined in the workspace's .mittorc file.
// Returns nil if no .mittorc exists or if it has no user_data schema section.
// A nil schema means no custom user data attributes are allowed (validation will reject any).
func (sm *SessionManager) GetUserDataSchema(workingDir string) *config.UserDataSchema {
	if sm.workspaceRCCache == nil || workingDir == "" {
		return nil
	}

	rc, err := sm.workspaceRCCache.Get(workingDir)
	if err != nil {
		if sm.logger != nil {
			sm.logger.Warn("Failed to load workspace .mittorc for user data schema",
				"working_dir", workingDir,
				"error", err)
		}
		return nil
	}

	if rc == nil {
		return nil
	}

	return rc.UserDataSchema
}

// AddWorkspace adds a new workspace to the manager.
// If workspaces were not loaded from CLI flags and a save callback is set,
// the workspaces will be persisted to disk.
// A UUID will be automatically generated if the workspace doesn't have one.
func (sm *SessionManager) AddWorkspace(ws config.WorkspaceSettings) {
	sm.mu.Lock()

	// Initialize workspaces map if needed
	if sm.workspaces == nil {
		sm.workspaces = make(map[string]*config.WorkspaceSettings)
	}

	// Ensure the workspace has a UUID
	ws.EnsureUUID()

	// Add the workspace (keyed by UUID to allow multiple workspaces with same directory)
	sm.workspaces[ws.UUID] = &ws

	// Set as default if there's no default or if the current default has no WorkingDir
	// (which indicates it was created from CLI flags without a specific directory)
	if sm.defaultWorkspace == nil || sm.defaultWorkspace.WorkingDir == "" {
		sm.defaultWorkspace = &ws
	}

	if sm.logger != nil {
		sm.logger.Info("Added workspace",
			"uuid", ws.UUID,
			"working_dir", ws.WorkingDir,
			"acp_server", ws.ACPServer,
			"total_workspaces", len(sm.workspaces))
	}

	// Save workspaces if not from CLI and callback is set
	shouldSave := !sm.fromCLI && sm.onWorkspaceSave != nil
	var workspacesToSave []config.WorkspaceSettings
	if shouldSave {
		workspacesToSave = sm.getWorkspacesLocked()
	}
	sm.mu.Unlock()

	if shouldSave {
		if err := sm.onWorkspaceSave(workspacesToSave); err != nil && sm.logger != nil {
			sm.logger.Error("Failed to save workspaces", "error", err)
		}
	}
}

// getWorkspacesLocked returns all workspaces (must be called with lock held).
func (sm *SessionManager) getWorkspacesLocked() []config.WorkspaceSettings {
	result := make([]config.WorkspaceSettings, 0, len(sm.workspaces))
	for _, ws := range sm.workspaces {
		result = append(result, *ws)
	}
	return result
}

// RemoveWorkspace removes a workspace by UUID from the manager.
// If workspaces were not loaded from CLI flags and a save callback is set,
// the workspaces will be persisted to disk.
func (sm *SessionManager) RemoveWorkspace(uuid string) {
	sm.mu.Lock()

	if sm.workspaces == nil {
		sm.mu.Unlock()
		return
	}

	// Get the workspace info before deletion (for logging and default update)
	ws, exists := sm.workspaces[uuid]
	if !exists {
		sm.mu.Unlock()
		return
	}
	workingDir := ws.WorkingDir

	delete(sm.workspaces, uuid)

	// If we removed the default workspace, pick a new one
	if sm.defaultWorkspace != nil && sm.defaultWorkspace.UUID == uuid {
		sm.defaultWorkspace = nil
		for _, ws := range sm.workspaces {
			sm.defaultWorkspace = ws
			break
		}
	}

	if sm.logger != nil {
		sm.logger.Info("Removed workspace",
			"uuid", uuid,
			"working_dir", workingDir,
			"total_workspaces", len(sm.workspaces))
	}

	// Save workspaces if not from CLI and callback is set
	shouldSave := !sm.fromCLI && sm.onWorkspaceSave != nil
	var workspacesToSave []config.WorkspaceSettings
	if shouldSave {
		workspacesToSave = sm.getWorkspacesLocked()
	}
	sm.mu.Unlock()

	if shouldSave {
		if err := sm.onWorkspaceSave(workspacesToSave); err != nil && sm.logger != nil {
			sm.logger.Error("Failed to save workspaces", "error", err)
		}
	}
}

// HasWorkspaces returns true if there are any configured workspaces.
func (sm *SessionManager) HasWorkspaces() bool {
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	return len(sm.workspaces) > 0
}

// IsFromCLI returns true if workspaces were loaded from CLI flags.
func (sm *SessionManager) IsFromCLI() bool {
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	return sm.fromCLI
}

// SetStore sets the session store for persistence.
func (sm *SessionManager) SetStore(store *session.Store) {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	sm.store = store
}

// SetGlobalRestrictedRunners sets the per-runner-type global configuration.
func (sm *SessionManager) SetGlobalRestrictedRunners(runners map[string]*config.WorkspaceRunnerConfig) {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	sm.globalRestrictedRunners = runners
}

// SetEventsManager sets the global events manager for broadcasting events.
func (sm *SessionManager) SetEventsManager(eventsManager *GlobalEventsManager) {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	sm.eventsManager = eventsManager
}

// SetGlobalMCPServer sets the global MCP server for session registration.
// Sessions will register with this server to enable session-scoped MCP tools.
func (sm *SessionManager) SetGlobalMCPServer(srv *mcpserver.Server) {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	sm.mcpServer = srv
}

// SetMittoConfig sets the full Mitto configuration.
// This is used to look up agent-specific runner configurations.
func (sm *SessionManager) SetMittoConfig(cfg *config.Config) {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	sm.mittoConfig = cfg
}

// createRunner creates a restricted runner for the given workspace and agent.
// Returns nil if no runner configuration is found (direct execution).
func (sm *SessionManager) createRunner(workingDir, acpServer string) (*runner.Runner, error) {
	// Get workspace-specific runner configs from .mittorc (by runner type)
	var workspaceRunnerConfigByType map[string]*config.WorkspaceRunnerConfig
	if workingDir != "" && sm.workspaceRCCache != nil {
		if rc, err := sm.workspaceRCCache.Get(workingDir); err == nil && rc != nil {
			workspaceRunnerConfigByType = rc.RestrictedRunners
		}
	}

	// Get global and agent-specific configs
	sm.mu.RLock()
	globalRunnersByType := sm.globalRestrictedRunners
	mittoConfig := sm.mittoConfig
	sm.mu.RUnlock()

	// Get agent-specific runner config from MittoConfig
	var agentRunnersByType map[string]*config.WorkspaceRunnerConfig
	if mittoConfig != nil && acpServer != "" {
		if server, err := mittoConfig.GetServer(acpServer); err == nil && server != nil {
			agentRunnersByType = server.RestrictedRunners
		}
	}

	// If no configuration at any level, return nil (direct execution)
	if globalRunnersByType == nil && agentRunnersByType == nil && workspaceRunnerConfigByType == nil {
		return nil, nil
	}

	// Create runner with configuration hierarchy
	r, err := runner.NewRunner(
		globalRunnersByType,
		agentRunnersByType,
		workspaceRunnerConfigByType,
		workingDir,
		sm.logger,
	)
	if err != nil {
		return nil, err
	}

	return r, nil
}

// CreateSession creates a new background session and registers it.
// Returns ErrTooManySessions if the session limit is reached.
// Uses the workspace configuration for the given working directory, or the default if not found.
func (sm *SessionManager) CreateSession(name, workingDir string) (*BackgroundSession, error) {
	return sm.CreateSessionWithWorkspace(name, workingDir, nil)
}

// CreateSessionWithWorkspace creates a new session using the specified workspace configuration.
// If workspace is nil, looks up the workspace by workingDir or uses the default.
func (sm *SessionManager) CreateSessionWithWorkspace(name, workingDir string, workspace *config.WorkspaceSettings) (*BackgroundSession, error) {
	sm.mu.Lock()
	if len(sm.sessions) >= MaxSessions {
		sm.mu.Unlock()
		return nil, ErrTooManySessions
	}
	store := sm.store
	globalConv := sm.globalConversations
	hookMgr := sm.hookManager

	// Determine ACP command, cwd, server, and workspace UUID from workspace configuration
	var acpCommand, acpCwd, acpServer, workspaceUUID string
	var foundWs *config.WorkspaceSettings // Track which workspace is used for later auto-approve check

	if workspace != nil {
		acpCommand = workspace.ACPCommand
		acpCwd = workspace.ACPCwd
		acpServer = workspace.ACPServer
		workspaceUUID = workspace.UUID
		if workingDir == "" {
			workingDir = workspace.WorkingDir
		}
	} else {
		// Try to find a workspace by working directory (first match)
		for _, ws := range sm.workspaces {
			if ws.WorkingDir == workingDir {
				foundWs = ws
				break
			}
		}
		if foundWs != nil {
			acpCommand = foundWs.ACPCommand
			acpCwd = foundWs.ACPCwd
			acpServer = foundWs.ACPServer
			workspaceUUID = foundWs.UUID
		} else if sm.defaultWorkspace != nil {
			acpCommand = sm.defaultWorkspace.ACPCommand
			acpCwd = sm.defaultWorkspace.ACPCwd
			acpServer = sm.defaultWorkspace.ACPServer
			workspaceUUID = sm.defaultWorkspace.UUID
		}
	}
	sm.mu.Unlock()

	// Load workspace-specific conversation config and merge with global
	var workspaceConv *config.ConversationsConfig
	if workingDir != "" && sm.workspaceRCCache != nil {
		if rc, err := sm.workspaceRCCache.Get(workingDir); err == nil && rc != nil {
			workspaceConv = rc.Conversations
		}
	}
	processors := config.MergeProcessors(globalConv, workspaceConv)

	// Get queue config (prefer workspace config, fall back to global)
	var queueConfig *config.QueueConfig
	if workspaceConv != nil && workspaceConv.Queue != nil {
		queueConfig = workspaceConv.Queue
	} else if globalConv != nil {
		queueConfig = globalConv.Queue
	}

	// Get action buttons config (prefer workspace config, fall back to global)
	var actionButtonsConfig *config.ActionButtonsConfig
	if workspaceConv != nil && workspaceConv.ActionButtons != nil {
		actionButtonsConfig = workspaceConv.ActionButtons
	} else if globalConv != nil {
		actionButtonsConfig = globalConv.ActionButtons
	}

	// Get file links config (prefer workspace config, fall back to global)
	var fileLinksConfig *config.FileLinksConfig
	if workspaceConv != nil && workspaceConv.FileLinks != nil {
		fileLinksConfig = workspaceConv.FileLinks
	} else if globalConv != nil {
		fileLinksConfig = globalConv.FileLinks
	}

	// Create restricted runner if configured
	r, err := sm.createRunner(workingDir, acpServer)
	if err != nil {
		return nil, err
	}

	// Check if runner fallback occurred and notify clients
	var runnerFallbackInfo *runner.FallbackInfo
	if r != nil && r.FallbackInfo != nil {
		runnerFallbackInfo = r.FallbackInfo
	}

	// Determine auto-approve: global flag, per-server setting, or per-workspace setting
	autoApprove := sm.autoApprove
	if !autoApprove && sm.mittoConfig != nil && acpServer != "" {
		if serverConfig, err := sm.mittoConfig.GetServer(acpServer); err == nil {
			autoApprove = serverConfig.AutoApprove
			if autoApprove && sm.logger != nil {
				sm.logger.Debug("Using per-server auto_approve setting",
					"acp_server", acpServer,
					"auto_approve", autoApprove)
			}
		}
	}
	// Per-workspace auto_approve overrides global and per-server settings
	if !autoApprove {
		if workspace != nil && workspace.AutoApprove != nil && *workspace.AutoApprove {
			autoApprove = true
			if sm.logger != nil {
				sm.logger.Debug("Using per-workspace auto_approve setting",
					"workspace_uuid", workspace.UUID,
					"working_dir", workingDir,
					"auto_approve", autoApprove)
			}
		} else if workspace == nil && foundWs != nil && foundWs.AutoApprove != nil && *foundWs.AutoApprove {
			autoApprove = true
			if sm.logger != nil {
				sm.logger.Debug("Using per-workspace auto_approve setting",
					"workspace_uuid", foundWs.UUID,
					"working_dir", workingDir,
					"auto_approve", autoApprove)
			}
		}
	}

	bs, err := NewBackgroundSession(BackgroundSessionConfig{
		ACPCommand:          acpCommand,
		ACPCwd:              acpCwd,
		ACPServer:           acpServer,
		WorkingDir:          workingDir,
		AutoApprove:         autoApprove,
		Logger:              sm.logger,
		Store:               store,
		SessionName:         name,
		Processors:          processors,
		HookManager:         hookMgr,
		QueueConfig:         queueConfig,
		Runner:              r,
		ActionButtonsConfig: actionButtonsConfig,
		FileLinksConfig:     fileLinksConfig,
		APIPrefix:           sm.apiPrefix,
		WorkspaceUUID:       workspaceUUID,
		GlobalMCPServer:     sm.mcpServer,
		OnStreamingStateChanged: func(sessionID string, isStreaming bool) {
			if sm.eventsManager != nil {
				sm.eventsManager.Broadcast(WSMsgTypeSessionStreaming, map[string]interface{}{
					"session_id":   sessionID,
					"is_streaming": isStreaming,
				})
			}
		},
		OnPlanStateChanged: func(sessionID string, entries []PlanEntry) {
			sm.SetCachedPlanState(sessionID, entries)
		},
		OnConfigOptionChanged: func(sessionID string, configID, value string) {
			if sm.eventsManager != nil {
				sm.eventsManager.Broadcast(WSMsgTypeConfigOptionChanged, map[string]interface{}{
					"session_id": sessionID,
					"config_id":  configID,
					"value":      value,
				})
			}
		},
	})
	if err != nil {
		return nil, err
	}

	sm.mu.Lock()
	// Double-check after session creation
	if len(sm.sessions) >= MaxSessions {
		sm.mu.Unlock()
		bs.Close("session_limit_exceeded")
		return nil, ErrTooManySessions
	}
	// Check if a session with this ID somehow already exists (shouldn't happen for new sessions)
	if existing, ok := sm.sessions[bs.GetSessionID()]; ok {
		sm.mu.Unlock()
		bs.Close("duplicate_session")
		return existing, nil
	}
	sm.sessions[bs.GetSessionID()] = bs
	sm.mu.Unlock()

	if sm.logger != nil {
		sm.logger.Debug("Created background session",
			"session_id", bs.GetSessionID(),
			"acp_id", bs.GetACPID(),
			"acp_server", acpServer,
			"working_dir", workingDir,
			"total_sessions", len(sm.sessions))
	}

	// Notify about runner fallback if it occurred
	if runnerFallbackInfo != nil && sm.eventsManager != nil {
		sm.eventsManager.Broadcast(WSMsgTypeRunnerFallback, map[string]interface{}{
			"session_id":     bs.GetSessionID(),
			"requested_type": runnerFallbackInfo.RequestedType,
			"fallback_type":  runnerFallbackInfo.FallbackType,
			"reason":         runnerFallbackInfo.Reason,
		})
	}

	return bs, nil
}

// SessionCount returns the number of running sessions.
func (sm *SessionManager) SessionCount() int {
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	return len(sm.sessions)
}

// ActiveSessionCount returns the number of active (running) sessions.
// M3: This is used by the health check endpoint.
func (sm *SessionManager) ActiveSessionCount() int {
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	count := 0
	for _, bs := range sm.sessions {
		if !bs.IsClosed() {
			count++
		}
	}
	return count
}

// PromptingSessionCount returns the number of sessions currently processing prompts.
// M3: This is used by the health check endpoint.
func (sm *SessionManager) PromptingSessionCount() int {
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	count := 0
	for _, bs := range sm.sessions {
		if bs.IsPrompting() {
			count++
		}
	}
	return count
}

// GetSession returns a running session by ID, or nil if not found.
func (sm *SessionManager) GetSession(sessionID string) *BackgroundSession {
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	return sm.sessions[sessionID]
}

// GetActiveWorkingDirs returns all working directories from active sessions.
// This is used by the file server to validate workspace access.
func (sm *SessionManager) GetActiveWorkingDirs() []string {
	sm.mu.RLock()
	defer sm.mu.RUnlock()

	dirs := make([]string, 0, len(sm.sessions))
	seen := make(map[string]bool)
	for _, bs := range sm.sessions {
		dir := bs.GetWorkingDir()
		if dir != "" && !seen[dir] {
			dirs = append(dirs, dir)
			seen[dir] = true
		}
	}
	return dirs
}

// GetOrCreateSession returns an existing session or creates a new one.
// If the session exists in the store but isn't running, it starts a new ACP process.
func (sm *SessionManager) GetOrCreateSession(sessionID, workingDir string) (*BackgroundSession, bool, error) {
	// Check if already running
	if bs := sm.GetSession(sessionID); bs != nil {
		return bs, false, nil
	}

	// Not running - create new session
	// Note: We can't truly "resume" an ACP session, but we can start a new one
	// with the same persisted ID for continuity
	bs, err := sm.CreateSession("", workingDir)
	if err != nil {
		return nil, false, err
	}

	return bs, true, nil
}

// ResumeSession resumes an existing persisted session by creating a new ACP process.
// This is used when switching to an old conversation. If the agent supports session
// loading and we have a stored ACP session ID, we attempt to resume the ACP session
// on the server side as well. Otherwise, we create a new ACP connection and continue
// using the same persisted session ID for recording.
func (sm *SessionManager) ResumeSession(sessionID, sessionName, workingDir string) (*BackgroundSession, error) {
	// Check if already running
	if bs := sm.GetSession(sessionID); bs != nil {
		return bs, nil
	}

	sm.mu.Lock()
	if len(sm.sessions) >= MaxSessions {
		sm.mu.Unlock()
		return nil, ErrTooManySessions
	}
	store := sm.store
	globalConv := sm.globalConversations
	hookMgr := sm.hookManager

	// Determine ACP command, cwd, server, and workspace UUID from workspace configuration
	var acpCommand, acpCwd, acpServer, workspaceUUID string
	// Try to find a workspace by working directory (first match)
	var foundWs *config.WorkspaceSettings
	for _, ws := range sm.workspaces {
		if ws.WorkingDir == workingDir {
			foundWs = ws
			break
		}
	}
	if foundWs != nil {
		acpCommand = foundWs.ACPCommand
		acpCwd = foundWs.ACPCwd
		acpServer = foundWs.ACPServer
		workspaceUUID = foundWs.UUID
	} else if sm.defaultWorkspace != nil {
		acpCommand = sm.defaultWorkspace.ACPCommand
		acpCwd = sm.defaultWorkspace.ACPCwd
		acpServer = sm.defaultWorkspace.ACPServer
		workspaceUUID = sm.defaultWorkspace.UUID
	}

	// Get session metadata for ACP session ID and server name
	var acpSessionID string
	if store != nil {
		if meta, err := store.GetMetadata(sessionID); err == nil {
			// Get ACP session ID for potential resumption
			acpSessionID = meta.ACPSessionID

			// IMPORTANT: Use ACP server from session metadata, not workspace config.
			// The session was created with a specific ACP server, and resuming it
			// should use the same server regardless of current workspace defaults.
			// Only fall back to workspace config if metadata doesn't have the server.
			if meta.ACPServer != "" {
				acpServer = meta.ACPServer

				// IMPORTANT: Look up the correct command for the session's ACP server.
				// The workspace loop above may have found a different workspace (same dir,
				// different ACP server), so we must update the command to match.
				if sm.mittoConfig != nil {
					if server, err := sm.mittoConfig.GetServer(acpServer); err == nil {
						acpCommand = server.Command
						acpCwd = server.Cwd
						if sm.logger != nil {
							sm.logger.Debug("Using ACP command from session metadata server",
								"session_id", sessionID,
								"acp_server", acpServer,
								"acp_command", acpCommand)
						}
					}
				}
			}
		}
	}

	// If we still have an ACP server name but no command, look it up from global config
	// This handles cases where workspace config didn't provide a command
	if acpCommand == "" && acpServer != "" && sm.mittoConfig != nil {
		if server, err := sm.mittoConfig.GetServer(acpServer); err == nil {
			acpCommand = server.Command
			acpCwd = server.Cwd // Also get cwd from server config
			if sm.logger != nil {
				sm.logger.Debug("Using ACP command from global config",
					"session_id", sessionID,
					"acp_server", acpServer,
					"acp_command", acpCommand)
			}
		}
	}
	sm.mu.Unlock()

	// Load workspace-specific conversation config and merge with global
	// Note: For resumed sessions, isFirstPrompt is false, so "first" processors won't apply
	var workspaceConv *config.ConversationsConfig
	if workingDir != "" && sm.workspaceRCCache != nil {
		if rc, err := sm.workspaceRCCache.Get(workingDir); err == nil && rc != nil {
			workspaceConv = rc.Conversations
		}
	}
	processors := config.MergeProcessors(globalConv, workspaceConv)

	// Get queue config (prefer workspace config, fall back to global)
	var queueConfig *config.QueueConfig
	if workspaceConv != nil && workspaceConv.Queue != nil {
		queueConfig = workspaceConv.Queue
	} else if globalConv != nil {
		queueConfig = globalConv.Queue
	}

	// Get action buttons config (prefer workspace config, fall back to global)
	var actionButtonsConfig *config.ActionButtonsConfig
	if workspaceConv != nil && workspaceConv.ActionButtons != nil {
		actionButtonsConfig = workspaceConv.ActionButtons
	} else if globalConv != nil {
		actionButtonsConfig = globalConv.ActionButtons
	}

	// Get file links config (prefer workspace config, fall back to global)
	var fileLinksConfig *config.FileLinksConfig
	if workspaceConv != nil && workspaceConv.FileLinks != nil {
		fileLinksConfig = workspaceConv.FileLinks
	} else if globalConv != nil {
		fileLinksConfig = globalConv.FileLinks
	}

	// Create restricted runner if configured
	r, err := sm.createRunner(workingDir, acpServer)
	if err != nil {
		return nil, err
	}

	// Determine auto-approve: global flag, per-server setting, or per-workspace setting
	autoApprove := sm.autoApprove
	if !autoApprove && sm.mittoConfig != nil && acpServer != "" {
		if serverConfig, err := sm.mittoConfig.GetServer(acpServer); err == nil {
			autoApprove = serverConfig.AutoApprove
			if autoApprove && sm.logger != nil {
				sm.logger.Debug("Using per-server auto_approve setting for resumed session",
					"acp_server", acpServer,
					"auto_approve", autoApprove,
					"session_id", sessionID)
			}
		}
	}
	// Per-workspace auto_approve overrides global and per-server settings
	if !autoApprove && foundWs != nil && foundWs.AutoApprove != nil && *foundWs.AutoApprove {
		autoApprove = true
		if sm.logger != nil {
			sm.logger.Debug("Using per-workspace auto_approve setting for resumed session",
				"workspace_uuid", foundWs.UUID,
				"working_dir", workingDir,
				"auto_approve", autoApprove,
				"session_id", sessionID)
		}
	}

	// Create a background session with the existing persisted session ID
	// Pass the ACP session ID for potential server-side resumption
	bs, err := ResumeBackgroundSession(BackgroundSessionConfig{
		PersistedID:         sessionID,
		ACPCommand:          acpCommand,
		ACPCwd:              acpCwd,
		ACPServer:           acpServer,
		ACPSessionID:        acpSessionID,
		WorkingDir:          workingDir,
		AutoApprove:         autoApprove,
		Logger:              sm.logger,
		Store:               store,
		SessionName:         sessionName,
		Processors:          processors,
		HookManager:         hookMgr,
		QueueConfig:         queueConfig,
		Runner:              r,
		ActionButtonsConfig: actionButtonsConfig,
		FileLinksConfig:     fileLinksConfig,
		APIPrefix:           sm.apiPrefix,
		WorkspaceUUID:       workspaceUUID,
		GlobalMCPServer:     sm.mcpServer,
		OnStreamingStateChanged: func(sessionID string, isStreaming bool) {
			if sm.eventsManager != nil {
				sm.eventsManager.Broadcast(WSMsgTypeSessionStreaming, map[string]interface{}{
					"session_id":   sessionID,
					"is_streaming": isStreaming,
				})
			}
		},
		OnPlanStateChanged: func(sessionID string, entries []PlanEntry) {
			sm.SetCachedPlanState(sessionID, entries)
		},
		OnConfigOptionChanged: func(sessionID string, configID, value string) {
			if sm.eventsManager != nil {
				sm.eventsManager.Broadcast(WSMsgTypeConfigOptionChanged, map[string]interface{}{
					"session_id": sessionID,
					"config_id":  configID,
					"value":      value,
				})
			}
		},
	})
	if err != nil {
		return nil, err
	}

	sm.mu.Lock()
	// Double-check after session creation
	if len(sm.sessions) >= MaxSessions {
		sm.mu.Unlock()
		bs.Close("session_limit_exceeded")
		return nil, ErrTooManySessions
	}
	// Check if another goroutine already created this session while we were creating ours
	if existing, ok := sm.sessions[bs.GetSessionID()]; ok {
		sm.mu.Unlock()
		bs.Close("duplicate_session")
		return existing, nil
	}
	sm.sessions[bs.GetSessionID()] = bs
	sm.mu.Unlock()

	if sm.logger != nil {
		sm.logger.Debug("Resumed background session",
			"session_id", bs.GetSessionID(),
			"acp_id", bs.GetACPID(),
			"acp_server", acpServer,
			"working_dir", workingDir,
			"total_sessions", len(sm.sessions))
	}

	return bs, nil
}

// RemoveSession removes a session from the manager (does not close it).
func (sm *SessionManager) RemoveSession(sessionID string) {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	delete(sm.sessions, sessionID)
}

// CloseSession closes a session and removes it from the manager.
// Also clears any cached plan state for the session.
func (sm *SessionManager) CloseSession(sessionID, reason string) {
	sm.mu.Lock()
	bs := sm.sessions[sessionID]
	delete(sm.sessions, sessionID)
	sm.mu.Unlock()

	// Clear cached plan state when session is closed/deleted
	sm.ClearCachedPlanState(sessionID)

	if bs != nil {
		bs.Close(reason)
		if sm.logger != nil {
			sm.logger.Debug("Closed background session",
				"session_id", sessionID,
				"reason", reason)
		}
	}
}

// CloseSessionGracefully waits for any active response to complete before closing the session.
// This is used when archiving a conversation to avoid interrupting an in-progress response.
// Returns true if the session was closed, false if the timeout expired while waiting.
func (sm *SessionManager) CloseSessionGracefully(sessionID, reason string, timeout time.Duration) bool {
	sm.mu.Lock()
	bs := sm.sessions[sessionID]
	sm.mu.Unlock()

	if bs == nil {
		// Session not running, nothing to close
		return true
	}

	// Wait for any active response to complete
	if bs.IsPrompting() {
		if sm.logger != nil {
			sm.logger.Info("Waiting for response to complete before closing session",
				"session_id", sessionID,
				"timeout", timeout)
		}
		if !bs.WaitForResponseComplete(timeout) {
			if sm.logger != nil {
				sm.logger.Warn("Timeout waiting for response to complete",
					"session_id", sessionID,
					"timeout", timeout)
			}
			return false
		}
	}

	// Now close the session
	sm.CloseSession(sessionID, reason)
	return true
}

// ListRunningSessions returns the IDs of all running sessions.
func (sm *SessionManager) ListRunningSessions() []string {
	sm.mu.RLock()
	defer sm.mu.RUnlock()

	ids := make([]string, 0, len(sm.sessions))
	for id := range sm.sessions {
		ids = append(ids, id)
	}
	return ids
}

// CloseAll closes all running sessions.
func (sm *SessionManager) CloseAll(reason string) {
	sm.mu.Lock()
	sessions := make([]*BackgroundSession, 0, len(sm.sessions))
	for _, bs := range sm.sessions {
		sessions = append(sessions, bs)
	}
	sm.sessions = make(map[string]*BackgroundSession)
	sm.mu.Unlock()

	for _, bs := range sessions {
		bs.Close(reason)
	}

	if sm.logger != nil {
		sm.logger.Info("Closed all background sessions",
			"count", len(sessions),
			"reason", reason)
	}
}

// SetCachedPlanState stores the last known agent plan entries for a session.
// This is used to restore the agent plan panel when switching back to a conversation.
// The state is in-memory only and does not persist across server restarts.
func (sm *SessionManager) SetCachedPlanState(sessionID string, entries []PlanEntry) {
	sm.planStateMu.Lock()
	defer sm.planStateMu.Unlock()

	if sm.planState == nil {
		sm.planState = make(map[string][]PlanEntry)
	}

	if len(entries) == 0 {
		// Clear the entry if empty
		delete(sm.planState, sessionID)
		return
	}

	// Make a copy to avoid external modification
	entriesCopy := make([]PlanEntry, len(entries))
	copy(entriesCopy, entries)
	sm.planState[sessionID] = entriesCopy

	if sm.logger != nil {
		sm.logger.Debug("Cached plan state",
			"session_id", sessionID,
			"entry_count", len(entries))
	}
}

// GetCachedPlanState returns the cached agent plan entries for a session.
// Returns nil if no plan state is cached for the session.
// The returned slice is a copy, safe to modify.
func (sm *SessionManager) GetCachedPlanState(sessionID string) []PlanEntry {
	sm.planStateMu.RLock()
	defer sm.planStateMu.RUnlock()

	if sm.planState == nil {
		return nil
	}

	entries, ok := sm.planState[sessionID]
	if !ok || len(entries) == 0 {
		return nil
	}

	// Return a copy to prevent external modification
	result := make([]PlanEntry, len(entries))
	copy(result, entries)
	return result
}

// ClearCachedPlanState removes the cached plan state for a session.
// Called when a session is deleted or when a new prompt starts (plan becomes stale).
func (sm *SessionManager) ClearCachedPlanState(sessionID string) {
	sm.planStateMu.Lock()
	defer sm.planStateMu.Unlock()

	if sm.planState == nil {
		return
	}

	delete(sm.planState, sessionID)

	if sm.logger != nil {
		sm.logger.Debug("Cleared cached plan state", "session_id", sessionID)
	}
}

// ProcessPendingQueues checks all persisted sessions for queued messages and
// auto-resumes sessions that have pending queue items and meet the criteria
// for dequeuing (agent idle, delay elapsed). This is called on server startup.
func (sm *SessionManager) ProcessPendingQueues() {
	sm.mu.RLock()
	store := sm.store
	globalConv := sm.globalConversations
	sm.mu.RUnlock()

	if store == nil {
		return
	}

	// List all persisted sessions
	sessions, err := store.List()
	if err != nil {
		if sm.logger != nil {
			sm.logger.Warn("Failed to list sessions for queue processing", "error", err)
		}
		return
	}

	// Check each session for pending queue items
	for _, meta := range sessions {
		// Skip non-active sessions
		if meta.Status != session.SessionStatusActive && meta.Status != "" {
			continue
		}

		// Skip archived sessions - they should not have their ACP started automatically
		if meta.Archived {
			continue
		}

		// Check if this session has items in its queue
		queue := store.Queue(meta.SessionID)
		queueLen, err := queue.Len()
		if err != nil || queueLen == 0 {
			continue
		}

		// Session has queued messages - check if it's already running
		if sm.GetSession(meta.SessionID) != nil {
			// Session is already running, it will handle its own queue
			continue
		}

		// Get queue config to check delay
		var queueConfig *config.QueueConfig
		if meta.WorkingDir != "" && sm.workspaceRCCache != nil {
			if rc, err := sm.workspaceRCCache.Get(meta.WorkingDir); err == nil && rc != nil && rc.Conversations != nil {
				queueConfig = rc.Conversations.Queue
			}
		}
		if queueConfig == nil && globalConv != nil {
			queueConfig = globalConv.Queue
		}

		// Check if queue processing is enabled
		if queueConfig != nil && !queueConfig.IsEnabled() {
			continue
		}

		if sm.logger != nil {
			sm.logger.Info("Found session with pending queue items",
				"session_id", meta.SessionID,
				"queue_length", queueLen,
				"working_dir", meta.WorkingDir)
		}

		// Resume the session to process its queue
		// The session will check the delay before actually sending
		bs, err := sm.ResumeSession(meta.SessionID, meta.Name, meta.WorkingDir)
		if err != nil {
			if sm.logger != nil {
				sm.logger.Warn("Failed to resume session for queue processing",
					"session_id", meta.SessionID,
					"error", err)
			}
			continue
		}

		// Try to process the queued message immediately
		// Note: On startup, the delay is skipped because lastResponseComplete is zero
		// Run in a goroutine so we don't block startup
		go func(session *BackgroundSession, sessionID string) {
			if session.TryProcessQueuedMessage() {
				if sm.logger != nil {
					sm.logger.Info("Auto-dequeued message on startup",
						"session_id", sessionID)
				}
			}
		}(bs, meta.SessionID)
	}
}
