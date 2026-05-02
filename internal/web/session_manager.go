package web

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"sync"
	"time"

	"path/filepath"

	"github.com/inercia/mitto/internal/appdir"
	"github.com/inercia/mitto/internal/auxiliary"
	"github.com/inercia/mitto/internal/config"
	"github.com/inercia/mitto/internal/mcpserver"
	"github.com/inercia/mitto/internal/processors"
	"github.com/inercia/mitto/internal/runner"
	"github.com/inercia/mitto/internal/session"
)

// MaxSessions is the maximum number of concurrent sessions allowed.
// This limits running sessions (those with an active ACP process), not stored/archived sessions.
const MaxSessions = 64

// DefaultMaxMessagesPerSession is the default maximum number of messages to retain per session.
// When exceeded, the oldest messages are automatically pruned after each new event is recorded.
// This prevents unbounded session growth (especially for periodic sessions) which can cause
// OOM crashes when many large sessions share a single ACP process.
// Can be overridden via settings.json or .mitterc with "max_messages_per_session".
// Set to 0 in settings to disable automatic pruning.
const DefaultMaxMessagesPerSession = 2000

// ErrTooManySessions is returned when the session limit is reached.
var ErrTooManySessions = errors.New("maximum number of sessions reached")

// pendingResumeResult holds the outcome of an in-progress session resume operation.
// Goroutines that race to resume the same session ID wait on done, then read
// the result set by the first (primary) goroutine — preventing duplicate ACP launches.
type pendingResumeResult struct {
	done chan struct{}      // closed when the resume is complete
	bs   *BackgroundSession // result (valid after done is closed)
	err  error              // error  (valid after done is closed)
}

// ACPServerRenameResult summarizes persisted and restarted sessions after an ACP server rename/remap.
type ACPServerRenameResult struct {
	UpdatedSessionIDs   []string `json:"updated_session_ids,omitempty"`
	RestartedSessionIDs []string `json:"restarted_session_ids,omitempty"`
}

// WorkspaceSaveFunc is a callback function called when workspaces are modified.
// It receives the current list of workspaces to be persisted.
type WorkspaceSaveFunc func(workspaces []config.WorkspaceSettings) error

// SessionManager manages background sessions that run independently of WebSocket connections.
// It is safe for concurrent use.
type SessionManager struct {
	mu       sync.RWMutex
	sessions map[string]*BackgroundSession // keyed by persisted session ID

	// pendingResumes tracks in-progress session resume operations, keyed by session ID.
	// This prevents the TOCTOU race where two goroutines both observe no running session
	// and both launch a separate ACP subprocess for the same session ID.
	// Entries are added under sm.mu before the expensive ACP work begins, and removed
	// (also under sm.mu) by the primary goroutine after the work completes.
	pendingResumes map[string]*pendingResumeResult

	logger *slog.Logger

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

	// processorManager manages external command processors for message transformation.
	processorManager *processors.Manager

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

	// waitingForChildrenMu protects waitingForChildren map.
	waitingForChildrenMu sync.RWMutex
	// waitingForChildren tracks which sessions are currently blocked on mitto_children_tasks_wait.
	// This is in-memory only and is used to populate the session list API response so that
	// the frontend can show the hourglass icon even after fetchStoredSessions() overwrites storedSessions.
	waitingForChildren map[string]bool

	// mcpServer is the global MCP server for session registration.
	// Sessions register with this server to enable session-scoped MCP tools.
	mcpServer *mcpserver.Server

	// acpProcessManager manages shared ACP processes, one per workspace.
	// When set, new sessions use a shared process instead of starting their own.
	// When nil, legacy per-session process ownership is used.
	acpProcessManager *ACPProcessManager

	// auxiliaryManager provides workspace-scoped auxiliary tasks (title generation,
	// follow-up analysis, conversation summaries, etc.).
	auxiliaryManager *auxiliary.WorkspaceAuxiliaryManager

	// mcpCheckedWorkspaces tracks which workspaces have had MCP availability checked.
	mcpCheckedWorkspaces   map[string]bool
	mcpCheckedWorkspacesMu sync.RWMutex

	// mcpToolsFetchedWorkspaces tracks which workspaces have had MCP tools fetched.
	mcpToolsFetchedWorkspaces   map[string]bool
	mcpToolsFetchedWorkspacesMu sync.RWMutex
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
		sessions:                  make(map[string]*BackgroundSession),
		pendingResumes:            make(map[string]*pendingResumeResult),
		workspaces:                make(map[string]*config.WorkspaceSettings),
		logger:                    logger,
		defaultWorkspace:          defaultWS,
		autoApprove:               autoApprove,
		workspaceRCCache:          config.NewWorkspaceRCCache(30 * time.Second),
		planState:                 make(map[string][]PlanEntry),
		waitingForChildren:        make(map[string]bool),
		mcpCheckedWorkspaces:      make(map[string]bool),
		mcpToolsFetchedWorkspaces: make(map[string]bool),
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
		sessions:                  make(map[string]*BackgroundSession),
		pendingResumes:            make(map[string]*pendingResumeResult),
		workspaces:                make(map[string]*config.WorkspaceSettings),
		logger:                    opts.Logger,
		autoApprove:               opts.AutoApprove,
		fromCLI:                   opts.FromCLI,
		onWorkspaceSave:           opts.OnWorkspaceSave,
		workspaceRCCache:          config.NewWorkspaceRCCache(30 * time.Second),
		apiPrefix:                 opts.APIPrefix,
		planState:                 make(map[string][]PlanEntry),
		waitingForChildren:        make(map[string]bool),
		mcpCheckedWorkspaces:      make(map[string]bool),
		mcpToolsFetchedWorkspaces: make(map[string]bool),
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

// SetProcessorManager sets the processor manager for external command processors.
func (sm *SessionManager) SetProcessorManager(pm *processors.Manager) {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	sm.processorManager = pm
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

	return sm.getWorkspaceByDirAndACPLocked(workingDir, acpServer)
}

// getWorkspaceByDirAndACPLocked returns the workspace matching both directory and ACP server.
// Caller must hold sm.mu.
func (sm *SessionManager) getWorkspaceByDirAndACPLocked(workingDir, acpServer string) *config.WorkspaceSettings {
	for _, ws := range sm.workspaces {
		if ws.WorkingDir == workingDir {
			if acpServer == "" || ws.ACPServer == acpServer {
				return ws
			}
		}
	}
	return nil
}

// resolveWorkspaceForACPLocked returns the workspace to use for a given directory/server pair.
//
// Resolution rules:
//   - Prefer an exact workspace match for (workingDir, acpServer)
//   - If acpServer is empty, prefer the first workspace matching workingDir
//   - Fall back to the default workspace only when it is compatible with the ACP server
//     and working directory (or when those are unspecified in the default)
//
// Caller must hold sm.mu.
func (sm *SessionManager) resolveWorkspaceForACPLocked(workingDir, acpServer string) *config.WorkspaceSettings {
	if ws := sm.getWorkspaceByDirAndACPLocked(workingDir, acpServer); ws != nil {
		return ws
	}

	if sm.defaultWorkspace == nil {
		return nil
	}
	if acpServer != "" && sm.defaultWorkspace.ACPServer != acpServer {
		return nil
	}
	if workingDir != "" && sm.defaultWorkspace.WorkingDir != "" && sm.defaultWorkspace.WorkingDir != workingDir {
		return nil
	}
	return sm.defaultWorkspace
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

// createAutoChildren creates child sessions for a newly created parent session.
// Only called for top-level sessions (conversations created without a parent).
// Children are created asynchronously; failures are logged but don't fail parent creation.
func (sm *SessionManager) createAutoChildren(parentBS *BackgroundSession, workspace *config.WorkspaceSettings) {
	if workspace == nil || len(workspace.AutoChildren) == 0 {
		return
	}

	parentID := parentBS.GetSessionID()
	parentWorkingDir := parentBS.GetWorkingDir()

	if sm.logger != nil {
		sm.logger.Info("Creating auto-children for new session",
			"parent_session_id", parentID,
			"auto_children_count", len(workspace.AutoChildren))
	}

	store := sm.store
	if store == nil {
		if sm.logger != nil {
			sm.logger.Error("Cannot create auto-children: store not available",
				"parent_session_id", parentID)
		}
		return
	}

	for _, child := range workspace.AutoChildren {
		// Resolve target workspace
		targetWS := workspace // default: same workspace
		if child.TargetWorkspaceUUID != "" {
			targetWS = sm.GetWorkspaceByUUID(child.TargetWorkspaceUUID)
			if targetWS == nil {
				if sm.logger != nil {
					sm.logger.Warn("Auto-child target workspace not found",
						"parent_session_id", parentID,
						"child_title", child.Title,
						"target_uuid", child.TargetWorkspaceUUID)
				}
				continue
			}
		}

		// Generate new session ID
		childID := session.GenerateSessionID()

		// Create child session metadata
		childMeta := session.Metadata{
			SessionID:       childID,
			Name:            child.Title,
			ACPServer:       targetWS.ACPServer,
			WorkingDir:      parentWorkingDir,        // Inherit parent's working dir
			ParentSessionID: parentID,                // Mark as child
			IsAutoChild:     true,                    // Cascade delete with parent (backward compat)
			ChildOrigin:     session.ChildOriginAuto, // Created via auto_children config
		}

		// Create via store
		if err := store.Create(childMeta); err != nil {
			if sm.logger != nil {
				sm.logger.Error("Failed to create auto-child session",
					"parent_session_id", parentID,
					"child_title", child.Title,
					"error", err)
			}
			continue
		}

		// Resume the child session (start ACP process)
		childBS, err := sm.ResumeSession(childID, child.Title, parentWorkingDir)
		if err != nil {
			if sm.logger != nil {
				sm.logger.Error("Failed to start auto-child ACP process",
					"parent_session_id", parentID,
					"child_session_id", childID,
					"error", err)
			}
			// Session was created but ACP failed - it can be resumed later
			continue
		}

		// Broadcast creation to all connected clients
		sm.BroadcastSessionCreated(childID, child.Title, targetWS.ACPServer, parentWorkingDir, parentID, string(session.ChildOriginAuto))

		if sm.logger != nil {
			sm.logger.Info("Auto-created child conversation",
				"parent_session_id", parentID,
				"child_session_id", childID,
				"child_title", child.Title,
				"child_acp_server", targetWS.ACPServer,
				"child_is_running", childBS != nil)
		}
	}
}

// GetWorkspacesForFolder returns all workspace configurations for the given folder.
// Multiple workspaces may share the same folder with different ACP servers
// (e.g., same project folder with Claude Code and Auggie).
// Also includes the default workspace if its folder matches.
func (sm *SessionManager) GetWorkspacesForFolder(folder string) []config.WorkspaceSettings {
	sm.mu.RLock()
	defer sm.mu.RUnlock()

	var result []config.WorkspaceSettings
	seen := make(map[string]bool) // track by UUID to avoid duplicates

	for _, ws := range sm.workspaces {
		if ws.WorkingDir == folder {
			result = append(result, *ws)
			seen[ws.UUID] = true
		}
	}

	// Include default workspace if it matches and hasn't been included
	if sm.defaultWorkspace != nil && sm.defaultWorkspace.WorkingDir == folder {
		if !seen[sm.defaultWorkspace.UUID] {
			result = append(result, *sm.defaultWorkspace)
		}
	}

	return result
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

// buildAvailableACPServers returns the list of ACP servers that have workspaces
// configured for the given folder, using the same logic as the MCP tool

// buildPruneConfig builds a PruneConfig from the global settings.
// If no explicit max_messages_per_session is configured, it applies
// DefaultMaxMessagesPerSession to prevent unbounded session growth.
// Returns nil only if pruning is explicitly disabled (max_messages_per_session set to 0
// with no max_session_size_bytes).
func (sm *SessionManager) buildPruneConfig() *session.PruneConfig {
	maxMessages := DefaultMaxMessagesPerSession
	var maxSizeBytes int64

	if sm.mittoConfig != nil && sm.mittoConfig.Session != nil {
		sc := sm.mittoConfig.Session
		if sc.MaxMessagesPerSession > 0 {
			// Explicit positive value overrides default
			maxMessages = sc.MaxMessagesPerSession
		} else if sc.MaxMessagesPerSession < 0 {
			// Negative value disables message-count pruning
			maxMessages = 0
		}
		// 0 means "not set" — keep default
		maxSizeBytes = sc.MaxSessionSizeBytes
	}

	if maxMessages == 0 && maxSizeBytes <= 0 {
		return nil
	}

	return &session.PruneConfig{
		MaxMessages:  maxMessages,
		MaxSizeBytes: maxSizeBytes,
	}
}

// (mitto_conversation_get_current). Each entry includes the server name, type,
// and tags, plus whether it is the currently active server for the session.
//
// Returns nil when no config is available or no workspace is found for the folder.
func (sm *SessionManager) buildAvailableACPServers(folder, currentACPServer string) []processors.AvailableACPServer {
	if sm.mittoConfig == nil || len(sm.mittoConfig.ACPServers) == 0 {
		return nil
	}

	folderWorkspaces := sm.GetWorkspacesForFolder(folder)
	if len(folderWorkspaces) == 0 {
		return nil
	}

	wsServerSet := make(map[string]bool, len(folderWorkspaces))
	for _, ws := range folderWorkspaces {
		wsServerSet[ws.ACPServer] = true
	}

	servers := make([]processors.AvailableACPServer, 0, len(folderWorkspaces))
	for _, srv := range sm.mittoConfig.ACPServers {
		if wsServerSet[srv.Name] {
			servers = append(servers, processors.AvailableACPServer{
				Name:    srv.Name,
				Type:    srv.GetType(),
				Tags:    srv.Tags,
				Current: srv.Name == currentACPServer,
			})
		}
	}
	return servers
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

// GetWorkspaceProcessorsDirs returns the processors_dirs defined in the workspace's .mittorc file.
// Returns nil if no .mittorc exists or if it has no processors_dirs section.
func (sm *SessionManager) GetWorkspaceProcessorsDirs(workingDir string) []string {
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

	return rc.ProcessorsDirs
}

// GetProcessorManager returns the global processor manager.
func (sm *SessionManager) GetProcessorManager() *processors.Manager {
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	return sm.processorManager
}

// GetWorkspaceProcessorOverrides returns the processor enabled/disabled overrides from the
// workspace's .mittorc file. Returns nil if no .mittorc exists or if it has no overrides.
func (sm *SessionManager) GetWorkspaceProcessorOverrides(workingDir string) []config.ProcessorOverride {
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

	return rc.ProcessorOverrides
}

// GetWorkspaceProcessorManager returns the merged processor manager for a given workspace dir,
// combining global processors with workspace-specific ones from .mitto/processors/ and processors_dirs.
// This is used by the workspace processors API to list all applicable processors.
func (sm *SessionManager) GetWorkspaceProcessorManager(workingDir string) *processors.Manager {
	sm.mu.RLock()
	procMgr := sm.processorManager
	sm.mu.RUnlock()

	if procMgr == nil || workingDir == "" {
		return procMgr
	}

	return sm.loadWorkspaceProcessors(procMgr, workingDir)
}

// GetWorkspaceAllProcessorDirs returns all processor directories applicable to a workspace:
// the default .mitto/processors/ dir plus any extras from .mittorc processors_dirs.
func (sm *SessionManager) GetWorkspaceAllProcessorDirs(workingDir string) []string {
	if workingDir == "" {
		return nil
	}

	var dirs []string

	// 1. Default .mitto/processors/ directory
	defaultDir := appdir.WorkspaceProcessorsDir(workingDir)
	dirs = append(dirs, defaultDir)

	// 2. Additional processors_dirs from .mittorc
	if extraDirs := sm.GetWorkspaceProcessorsDirs(workingDir); len(extraDirs) > 0 {
		for _, dir := range extraDirs {
			if !filepath.IsAbs(dir) {
				dir = filepath.Join(workingDir, dir)
			}
			dirs = append(dirs, dir)
		}
	}

	return dirs
}

// loadWorkspaceProcessors clones the processor manager with workspace-specific
// processors loaded from .mitto/processors/ and any processors_dirs in .mittorc.
// Returns the original manager if no workspace processors are found.
func (sm *SessionManager) loadWorkspaceProcessors(procMgr *processors.Manager, workingDir string) *processors.Manager {
	if procMgr == nil || workingDir == "" {
		return procMgr
	}

	var dirs []string

	// 1. Default .mitto/processors/ directory (lowest priority among workspace dirs)
	defaultDir := appdir.WorkspaceProcessorsDir(workingDir)
	dirs = append(dirs, defaultDir)

	// 2. Additional processors_dirs from .mittorc (higher priority)
	if extraDirs := sm.GetWorkspaceProcessorsDirs(workingDir); len(extraDirs) > 0 {
		for _, dir := range extraDirs {
			if !filepath.IsAbs(dir) {
				dir = filepath.Join(workingDir, dir)
			}
			dirs = append(dirs, dir)
		}
	}

	return procMgr.CloneWithDirProcessors(dirs, sm.logger)
}

// GetWorkspaceRCLastModified returns the last modification time of the workspace's .mittorc file.
// Returns zero time if the file doesn't exist or the cache is not initialized.
func (sm *SessionManager) GetWorkspaceRCLastModified(workingDir string) time.Time {
	if sm.workspaceRCCache == nil || workingDir == "" {
		return time.Time{}
	}
	return sm.workspaceRCCache.GetLastModified(workingDir)
}

// InvalidateWorkspaceRC invalidates the cached workspace RC for the given directory,
// forcing a reload on the next access.
func (sm *SessionManager) InvalidateWorkspaceRC(workingDir string) {
	if sm.workspaceRCCache == nil || workingDir == "" {
		return
	}
	sm.workspaceRCCache.Invalidate(workingDir)
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

	if rc.Metadata == nil {
		return nil
	}
	return rc.Metadata.UserDataSchema
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

// SetACPProcessManager sets the shared ACP process manager.
// When set, new sessions use a shared ACP process per workspace instead of starting their own.
func (sm *SessionManager) SetACPProcessManager(pm *ACPProcessManager) {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	sm.acpProcessManager = pm
}

// SetAuxiliaryManager sets the workspace-scoped auxiliary manager for title generation,
// follow-up analysis, and other auxiliary tasks.
func (sm *SessionManager) SetAuxiliaryManager(am *auxiliary.WorkspaceAuxiliaryManager) {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	sm.auxiliaryManager = am
}

// resolveACPCommand resolves an ACP server name to its shell command.
// Returns empty string if the server name cannot be resolved.
func (sm *SessionManager) resolveACPCommand(serverName string) string {
	sm.mu.RLock()
	cfg := sm.mittoConfig
	sm.mu.RUnlock()

	if cfg == nil {
		return serverName // fallback: use name as command
	}
	srv, err := cfg.GetServer(serverName)
	if err != nil {
		if sm.logger != nil {
			sm.logger.Warn("Failed to resolve ACP server command",
				"server_name", serverName,
				"error", err)
		}
		return ""
	}
	return srv.Command
}

// EnsureWorkspaceProcess ensures the shared ACP process for the given workspace UUID is running,
// starting it on demand if necessary. This allows auxiliary features (e.g. "improve prompt") to
// work even when no user session is currently active for that workspace.
// Returns an error if the workspace is not found or the process cannot be started.
// The caller must NOT hold sm.mu when calling this method.
func (sm *SessionManager) EnsureWorkspaceProcess(workspaceUUID string) error {
	ws := sm.GetWorkspaceByUUID(workspaceUUID)
	if ws == nil {
		return fmt.Errorf("workspace %s not found", workspaceUUID)
	}

	r, err := sm.createRunner(ws.WorkingDir, ws.ACPServer, ws)
	if err != nil {
		// Runner creation failure is non-fatal — proceed without restriction.
		// The process will run unrestricted, which is acceptable for short-lived
		// auxiliary sessions that only do text processing.
		if sm.logger != nil {
			sm.logger.Warn("Failed to create runner for workspace auxiliary, proceeding without restriction",
				"workspace_uuid", workspaceUUID,
				"error", err)
		}
		r = nil
	}

	p := sm.getSharedProcess(ws, r)
	if p == nil {
		return fmt.Errorf("failed to start ACP process for workspace %s", workspaceUUID)
	}

	// If workspace has a dedicated auxiliary ACP server, ensure its process exists.
	if ws.HasDedicatedAuxiliary() && sm.acpProcessManager != nil {
		auxCommand := sm.resolveACPCommand(ws.AuxiliaryACPServer)
		if auxCommand != "" {
			auxRunner, auxRunnerErr := sm.createRunner(ws.WorkingDir, ws.AuxiliaryACPServer, ws)
			if auxRunnerErr != nil {
				if sm.logger != nil {
					sm.logger.Warn("Failed to create runner for dedicated auxiliary, proceeding without restriction",
						"workspace_uuid", workspaceUUID,
						"aux_acp_server", ws.AuxiliaryACPServer,
						"error", auxRunnerErr)
				}
				auxRunner = nil
			}
			if _, auxErr := sm.acpProcessManager.GetOrCreateAuxProcess(ws, auxCommand, auxRunner); auxErr != nil {
				if sm.logger != nil {
					sm.logger.Error("Failed to create dedicated auxiliary process",
						"workspace_uuid", workspaceUUID,
						"aux_acp_server", ws.AuxiliaryACPServer,
						"error", auxErr)
				}
				// Non-fatal: auxiliary will fall back to main process
			} else if sm.logger != nil {
				sm.logger.Info("Dedicated auxiliary process ready",
					"workspace_uuid", workspaceUUID,
					"aux_acp_server", ws.AuxiliaryACPServer)
			}
		}
	}

	return nil
}

// getSharedProcess returns the shared ACP process for the given workspace,
// or nil if shared process management is not enabled.
// The caller must NOT hold sm.mu when calling this method.
func (sm *SessionManager) getSharedProcess(workspace *config.WorkspaceSettings, r *runner.Runner) *SharedACPProcess {
	sm.mu.RLock()
	pm := sm.acpProcessManager
	sm.mu.RUnlock()

	if pm == nil || workspace == nil || workspace.UUID == "" {
		return nil
	}

	process, err := pm.GetOrCreateProcess(workspace, r, true)
	if err != nil {
		if sm.logger != nil {
			sm.logger.Warn("Failed to get shared ACP process, falling back to per-session",
				"workspace_uuid", workspace.UUID,
				"error", err)
		}
		return nil
	}
	return process
}

// BroadcastSessionCreated broadcasts a session_created event to all connected clients.
// This is called when a new session is created (via HTTP API or MCP tools).
func (sm *SessionManager) BroadcastSessionCreated(sessionID, name, acpServer, workingDir, parentSessionID, childOrigin string) {
	sm.mu.RLock()
	em := sm.eventsManager
	sm.mu.RUnlock()

	if em == nil {
		return
	}

	sessionData := map[string]interface{}{
		"session_id":  sessionID,
		"name":        name,
		"acp_server":  acpServer,
		"working_dir": workingDir,
		"status":      "active",
	}

	// Include parent_session_id if this is a child session
	if parentSessionID != "" {
		sessionData["parent_session_id"] = parentSessionID
	}

	// Include child_origin so frontend can show the correct icon (e.g., robot for MCP)
	if childOrigin != "" {
		sessionData["child_origin"] = childOrigin
	}

	em.Broadcast(WSMsgTypeSessionCreated, sessionData)

	if sm.logger != nil {
		sm.logger.Debug("Broadcast session created",
			"session_id", sessionID,
			"name", name,
			"parent_session_id", parentSessionID,
			"child_origin", childOrigin,
			"clients", em.ClientCount())
	}
}

// BroadcastSessionArchived broadcasts a session_archived event to all connected clients.
// This is called when a session is archived or unarchived (via HTTP API or MCP tools).
func (sm *SessionManager) BroadcastSessionArchived(sessionID string, archived bool) {
	sm.mu.RLock()
	em := sm.eventsManager
	sm.mu.RUnlock()

	if em == nil {
		return
	}

	em.Broadcast(WSMsgTypeSessionArchived, map[string]interface{}{
		"session_id": sessionID,
		"archived":   archived,
	})

	if sm.logger != nil {
		sm.logger.Debug("Broadcast session archived",
			"session_id", sessionID,
			"archived", archived,
			"clients", em.ClientCount())
	}
}

// BroadcastSessionDeleted broadcasts a session_deleted event to all connected clients.
// This is called when a session is permanently deleted.
func (sm *SessionManager) BroadcastSessionDeleted(sessionID string) {
	sm.mu.RLock()
	em := sm.eventsManager
	sm.mu.RUnlock()

	if em == nil {
		return
	}

	em.Broadcast(WSMsgTypeSessionDeleted, map[string]string{
		"session_id": sessionID,
	})

	if sm.logger != nil {
		sm.logger.Debug("Broadcast session deleted",
			"session_id", sessionID,
			"clients", em.ClientCount())
	}
}

// BroadcastSessionRenamed broadcasts a session_renamed event to all connected clients.
// This is called when a session is renamed (e.g., via MCP tools).
func (sm *SessionManager) BroadcastSessionRenamed(sessionID string, newName string) {
	sm.mu.RLock()
	em := sm.eventsManager
	sm.mu.RUnlock()

	if em == nil {
		return
	}

	em.Broadcast(WSMsgTypeSessionRenamed, map[string]string{
		"session_id": sessionID,
		"name":       newName,
	})

	if sm.logger != nil {
		sm.logger.Debug("Broadcast session renamed",
			"session_id", sessionID,
			"name", newName,
			"clients", em.ClientCount())
	}
}

// BroadcastWaitingForChildren broadcasts a session_waiting event to all connected clients.
// This is called when a parent session starts or stops blocking on mitto_children_tasks_wait.
func (sm *SessionManager) BroadcastWaitingForChildren(sessionID string, isWaiting bool) {
	// Track the state so it can be included in the session list API response.
	// This ensures fetchStoredSessions() returns accurate waiting state even after
	// a full session list refresh overwrites the frontend's storedSessions.
	sm.waitingForChildrenMu.Lock()
	if isWaiting {
		sm.waitingForChildren[sessionID] = true
	} else {
		delete(sm.waitingForChildren, sessionID)
	}
	sm.waitingForChildrenMu.Unlock()

	sm.mu.RLock()
	em := sm.eventsManager
	sm.mu.RUnlock()

	if em == nil {
		return
	}

	em.Broadcast(WSMsgTypeSessionWaiting, map[string]interface{}{
		"session_id": sessionID,
		"is_waiting": isWaiting,
	})

	if sm.logger != nil {
		sm.logger.Debug("Broadcast session waiting for children",
			"session_id", sessionID,
			"is_waiting", isWaiting,
			"clients", em.ClientCount())
	}
}

// IsWaitingForChildren returns whether a session is currently blocked on mitto_children_tasks_wait.
func (sm *SessionManager) IsWaitingForChildren(sessionID string) bool {
	sm.waitingForChildrenMu.RLock()
	defer sm.waitingForChildrenMu.RUnlock()
	return sm.waitingForChildren[sessionID]
}

// childArchiveTimeout is the timeout for gracefully closing child sessions when a parent is archived.
const childArchiveTimeout = 30 * time.Second

// DeleteChildSessions permanently deletes all child sessions when a parent is archived.
// Each child's ACP process is gracefully stopped, then the session data is removed from disk.
// Auto-grandchildren are cascade-deleted by store.Delete; MCP-grandchildren are orphaned.
func (sm *SessionManager) DeleteChildSessions(parentID string) {
	sm.mu.RLock()
	store := sm.store
	sm.mu.RUnlock()

	if store == nil {
		return
	}

	children, err := store.ListChildSessions(parentID)
	if err != nil {
		if sm.logger != nil {
			sm.logger.Error("Failed to list child sessions for deletion cascade",
				"parent_session_id", parentID,
				"error", err)
		}
		return
	}

	for _, child := range children {
		childID := child.SessionID

		// Find auto-grandchildren before deletion — store.Delete will cascade-delete them,
		// so we need their IDs now to close their ACP processes and broadcast deletions.
		autoGrandchildIDs, _ := store.FindAutoChildrenRecursive(childID)

		// Gracefully close ACP process for the child
		if !sm.CloseSessionGracefully(childID, "parent_archived", childArchiveTimeout) {
			sm.CloseSession(childID, "parent_archived_timeout")
		}

		// Close ACP processes for auto-grandchildren (they will be cascade-deleted)
		for _, gcID := range autoGrandchildIDs {
			sm.CloseSession(gcID, "ancestor_archived")
		}

		// Permanently delete the child (cascade-deletes auto-grandchildren, orphans MCP-grandchildren)
		if err := store.Delete(childID); err != nil {
			if sm.logger != nil {
				sm.logger.Error("Failed to delete child session",
					"parent_session_id", parentID,
					"child_session_id", childID,
					"error", err)
			}
			continue
		}

		// Broadcast deletion for child and any cascade-deleted auto-grandchildren
		sm.BroadcastSessionDeleted(childID)
		for _, gcID := range autoGrandchildIDs {
			sm.BroadcastSessionDeleted(gcID)
		}

		if sm.logger != nil {
			sm.logger.Info("Deleted child session (parent archived)",
				"parent_session_id", parentID,
				"child_session_id", childID,
				"child_name", child.Name,
				"auto_grandchildren_deleted", len(autoGrandchildIDs))
		}
	}
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
// workspace is optional — when provided, its RestrictedRunnerConfig (if set) overrides
// any .mittorc workspace-level configuration for the same runner type.
// Returns nil if no runner configuration is found (direct execution).
func (sm *SessionManager) createRunner(workingDir, acpServer string, workspace *config.WorkspaceSettings) (*runner.Runner, error) {
	// Get workspace-specific runner configs from .mittorc (by runner type)
	var workspaceRunnerConfigByType map[string]*config.WorkspaceRunnerConfig
	if workingDir != "" && sm.workspaceRCCache != nil {
		if rc, err := sm.workspaceRCCache.Get(workingDir); err == nil && rc != nil {
			workspaceRunnerConfigByType = rc.RestrictedRunners
		}
	}

	// Merge workspace settings UI config into workspace-level config.
	// WorkspaceSettings.RestrictedRunnerConfig (set via UI) takes precedence over .mittorc.
	if workspace != nil && workspace.RestrictedRunnerConfig != nil {
		runnerType := workspace.GetRestrictedRunner()
		if workspaceRunnerConfigByType == nil {
			workspaceRunnerConfigByType = make(map[string]*config.WorkspaceRunnerConfig)
		}
		// UI config overrides .mittorc for the same runner type
		workspaceRunnerConfigByType[runnerType] = workspace.RestrictedRunnerConfig
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
	createStart := time.Now()

	sm.mu.Lock()
	if len(sm.sessions) >= MaxSessions {
		sm.mu.Unlock()
		return nil, ErrTooManySessions
	}
	store := sm.store
	globalConv := sm.globalConversations
	procMgr := sm.processorManager

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

	// Debug logging for workspace UUID
	if sm.logger != nil {
		sm.logger.Debug("CreateSessionWithWorkspace",
			"working_dir", workingDir,
			"workspace_uuid", workspaceUUID,
			"acp_server", acpServer,
			"found_workspace", foundWs != nil,
			"using_default", foundWs == nil && sm.defaultWorkspace != nil)
	}

	// Load workspace-specific conversation config and merge with global
	var workspaceConv *config.ConversationsConfig
	if workingDir != "" && sm.workspaceRCCache != nil {
		if rc, err := sm.workspaceRCCache.Get(workingDir); err == nil && rc != nil {
			workspaceConv = rc.Conversations
		}
	}
	// Merge text-mode processors from config into the unified pipeline.
	// Text-mode processors use priority 0 so they run before command-mode processors (priority 100).
	// Use CloneWithTextProcessors to avoid mutating the shared Manager instance.
	if textProcs := config.MergeProcessors(globalConv, workspaceConv); len(textProcs) > 0 && procMgr != nil {
		procMgr = procMgr.CloneWithTextProcessors(textProcs, 0)
	}

	// Load workspace-local processors from .mitto/processors/ and processors_dirs.
	procMgr = sm.loadWorkspaceProcessors(procMgr, workingDir)

	// Apply workspace-level processor overrides from .mittorc processors section.
	if overrides := sm.GetWorkspaceProcessorOverrides(workingDir); len(overrides) > 0 {
		procMgr = procMgr.CloneWithEnabledOverrides(overrides)
	}

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

	// Determine which workspace settings to use for runner config:
	// - if workspace was passed in, use it directly
	// - otherwise use foundWs (matched by working dir)
	effectiveWorkspace := workspace
	if effectiveWorkspace == nil {
		effectiveWorkspace = foundWs
	}

	// Create restricted runner if configured
	r, err := sm.createRunner(workingDir, acpServer, effectiveWorkspace)
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

	// Resolve shared ACP process for this workspace (if shared mode is enabled)
	effectiveWs := workspace
	if effectiveWs == nil {
		effectiveWs = foundWs
	}
	if effectiveWs == nil {
		effectiveWs = sm.defaultWorkspace
	}
	sharedProcessStart := time.Now()
	sharedProcess := sm.getSharedProcess(effectiveWs, r)
	sharedProcessDuration := time.Since(sharedProcessStart)

	configDuration := time.Since(createStart)

	// Build pruning configuration from global settings (with default)
	pruneConfig := sm.buildPruneConfig()

	// Build available ACP servers list for this workspace folder (used in @mitto:variable substitution).
	availableServers := sm.buildAvailableACPServers(workingDir, acpServer)

	newBsStart := time.Now()
	bs, err := NewBackgroundSession(BackgroundSessionConfig{
		ACPCommand:          acpCommand,
		ACPCwd:              acpCwd,
		ACPServer:           acpServer,
		WorkingDir:          workingDir,
		AutoApprove:         autoApprove,
		Logger:              sm.logger,
		Store:               store,
		SessionName:         name,
		ProcessorManager:    procMgr,
		QueueConfig:         queueConfig,
		Runner:              r,
		ActionButtonsConfig: actionButtonsConfig,
		FileLinksConfig:     fileLinksConfig,
		APIPrefix:           sm.apiPrefix,
		WorkspaceUUID:       workspaceUUID,
		MittoConfig:         sm.mittoConfig,   // Pass config for default flags
		AvailableACPServers: availableServers, // Pre-computed workspace server list
		GlobalMCPServer:     sm.mcpServer,
		AuxiliaryManager:    sm.auxiliaryManager,
		SharedProcess:       sharedProcess,  // Shared ACP process (nil = legacy mode)
		PruneConfig:         pruneConfig,    // Auto-pruning configuration (nil = no auto-pruning)
		OnStreamingStateChanged: func(sessionID string, isStreaming bool) {
			if sm.eventsManager != nil {
				sm.eventsManager.Broadcast(WSMsgTypeSessionStreaming, map[string]interface{}{
					"session_id":   sessionID,
					"is_streaming": isStreaming,
				})
			}
		},
		OnUIPromptStateChanged: func(sessionID string, isWaiting bool) {
			if sm.eventsManager != nil {
				sm.eventsManager.Broadcast(WSMsgTypeSessionUIPrompt, map[string]interface{}{
					"session_id": sessionID,
					"is_waiting": isWaiting,
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
		OnTitleGenerated: func(sessionID, title string) {
			if sm.eventsManager != nil {
				sm.eventsManager.Broadcast(WSMsgTypeSessionRenamed, map[string]string{
					"session_id": sessionID,
					"name":       title,
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

	newBsDuration := time.Since(newBsStart)

	if sm.logger != nil {
		sm.logger.Info("CreateSessionWithWorkspace timing",
			"session_id", bs.GetSessionID(),
			"acp_id", bs.GetACPID(),
			"acp_server", acpServer,
			"working_dir", workingDir,
			"total_sessions", len(sm.sessions),
			"total_ms", time.Since(createStart).Milliseconds(),
			"config_and_shared_process_ms", configDuration.Milliseconds(),
			"shared_process_lookup_ms", sharedProcessDuration.Milliseconds(),
			"new_background_session_ms", newBsDuration.Milliseconds(),
			"shared_process", sharedProcess != nil)
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

	// Auto-create children for top-level sessions.
	// Run in goroutine to not block the parent session creation response.
	go sm.createAutoChildren(bs, effectiveWs)

	// Trigger early MCP tools fetch to warm the cache before the first message.
	sm.ensureMCPToolsFetch(workspaceUUID)

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

	// Re-check under write lock: another goroutine may have registered this session
	// between our read-locked GetSession check above and acquiring the write lock here.
	if bs, ok := sm.sessions[sessionID]; ok {
		sm.mu.Unlock()
		return bs, nil
	}

	// Check if another goroutine is already resuming this session. If so, release the
	// lock, wait for its result, and return it directly — preventing a second ACP launch.
	if pr, ok := sm.pendingResumes[sessionID]; ok {
		sm.mu.Unlock()
		if sm.logger != nil {
			sm.logger.Debug("Waiting for concurrent session resume",
				"session_id", sessionID)
		}
		<-pr.done
		if sm.logger != nil {
			sm.logger.Debug("Concurrent session resume completed, coalescing result",
				"session_id", sessionID,
				"success", pr.err == nil)
		}
		return pr.bs, pr.err
	}

	if len(sm.sessions) >= MaxSessions {
		sm.mu.Unlock()
		return nil, ErrTooManySessions
	}

	// Register our pending resume so any concurrent callers for this session ID
	// will wait for our result instead of launching their own ACP subprocess.
	// The channel is closed (with pr.bs/pr.err set) by signalDone below.
	pr := &pendingResumeResult{done: make(chan struct{})}
	sm.pendingResumes[sessionID] = pr

	store := sm.store
	globalConv := sm.globalConversations
	procMgr := sm.processorManager

	// Determine ACP command, cwd, server, and workspace UUID from workspace configuration
	var acpCommand, acpCwd, acpServer, workspaceUUID string
	// Try to find a workspace by working directory. If the session metadata later
	// identifies a specific ACP server, this provisional choice will be replaced
	// with the exact workspace for that server.
	var foundWs *config.WorkspaceSettings
	foundWs = sm.getWorkspaceByDirAndACPLocked(workingDir, "")
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

				// IMPORTANT: Re-resolve the workspace using BOTH working directory and
				// ACP server. The provisional workspace chosen above may point to the
				// same directory but a different ACP server, which would incorrectly
				// reuse the wrong shared ACP process.
				foundWs = sm.resolveWorkspaceForACPLocked(workingDir, acpServer)
				if foundWs != nil {
					workspaceUUID = foundWs.UUID
					if foundWs.ACPCommand != "" {
						acpCommand = foundWs.ACPCommand
					}
					if foundWs.ACPCwd != "" {
						acpCwd = foundWs.ACPCwd
					}
				} else {
					// No compatible workspace exists for this ACP server. Do NOT keep a
					// mismatched workspace from the same directory, otherwise shared ACP
					// process lookup can mix different agents.
					workspaceUUID = ""
					acpCommand = ""
					acpCwd = ""
					if sm.logger != nil {
						sm.logger.Warn("No matching workspace for resumed session ACP server; disabling shared workspace resolution",

							"session_id", sessionID,
							"working_dir", workingDir,
							"acp_server", acpServer)
					}
				}

				// IMPORTANT: Look up the correct command for the session's ACP server.
				// The workspace loop above may have found a different workspace (same dir,
				// different ACP server), so we must update the command to match.
				// However, if the workspace has a user-provided command override, prefer that.
				if foundWs == nil || foundWs.ACPCommandOverride == "" {
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
				} else if sm.logger != nil {
					sm.logger.Debug("Using workspace command override",
						"session_id", sessionID,
						"acp_server", acpServer,
						"acp_command", acpCommand,
						"acp_command_override", foundWs.ACPCommandOverride)
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

	// signalDone stores the resume result in pr and unblocks any goroutines that are
	// waiting on this session's pending resume channel. It must be called exactly once
	// on every code path below (success or failure).
	//
	// Order matters: store result fields first, then delete from pendingResumes
	// (under sm.mu), and only then close the channel.
	//
	// Deleting before closing ensures that by the time any waiting goroutine wakes
	// from <-pr.done, the pendingResumes entry is already gone:
	//
	//   Success path: the session is already in sm.sessions (registered before
	//   signalDone is called), so any new ResumeSession call that arrives after
	//   the delete will find it via GetSession and return early — no duplicate.
	//
	//   Error path: the session is NOT in sm.sessions.  Deleting before closing
	//   means a new goroutine can start a fresh resume immediately (rather than
	//   spinning on the already-closed channel until the delete races through),
	//   and callers that were waiting on pr.done will retry and coalesce onto
	//   that fresh pendingResumes entry.
	//
	// The previous "close then delete" ordering caused a spin-loop on the error
	// path: waiting goroutines woke, saw the error, their callers re-entered
	// ResumeSession, found the stale pendingResumes entry, read from the already-
	// closed channel, saw the error again, and kept retrying until the delete
	// finally raced through — creating a window for inconsistent state.
	signalDone := func(result *BackgroundSession, err error) {
		pr.bs = result
		pr.err = err
		sm.mu.Lock()
		delete(sm.pendingResumes, sessionID)
		sm.mu.Unlock()
		close(pr.done)
	}

	// Load workspace-specific conversation config and merge with global.
	// Note: For resumed sessions, isFirstPrompt is false, so "first" processors won't apply.
	var workspaceConv *config.ConversationsConfig
	if workingDir != "" && sm.workspaceRCCache != nil {
		if rc, err := sm.workspaceRCCache.Get(workingDir); err == nil && rc != nil {
			workspaceConv = rc.Conversations
		}
	}
	// Merge text-mode processors from config into the unified pipeline.
	// Text-mode processors use priority 0 so they run before command-mode processors (priority 100).
	// Use CloneWithTextProcessors to avoid mutating the shared Manager instance.
	if textProcs := config.MergeProcessors(globalConv, workspaceConv); len(textProcs) > 0 && procMgr != nil {
		procMgr = procMgr.CloneWithTextProcessors(textProcs, 0)
	}

	// Load workspace-local processors from .mitto/processors/ and processors_dirs.
	procMgr = sm.loadWorkspaceProcessors(procMgr, workingDir)

	// Apply workspace-level processor overrides from .mittorc processors section.
	if overrides := sm.GetWorkspaceProcessorOverrides(workingDir); len(overrides) > 0 {
		procMgr = procMgr.CloneWithEnabledOverrides(overrides)
	}

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

	// Create restricted runner if configured, passing foundWs for per-workspace restrictions
	r, err := sm.createRunner(workingDir, acpServer, foundWs)
	if err != nil {
		signalDone(nil, err)
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

	// Resolve shared ACP process for this workspace (if shared mode is enabled).
	// IMPORTANT: Do NOT fall back to an arbitrary default workspace here. For resumed
	// sessions, foundWs has already been resolved against the session's ACP server.
	// Falling back again would risk mixing different ACP servers on the same folder.
	sharedProcess := sm.getSharedProcess(foundWs, r)

	// Build pruning configuration from global settings (with default)
	pruneConfig := sm.buildPruneConfig()

	// Build available ACP servers list for this workspace folder (used in @mitto:variable substitution).
	resumeAvailableServers := sm.buildAvailableACPServers(workingDir, acpServer)

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
		ProcessorManager:    procMgr,
		QueueConfig:         queueConfig,
		Runner:              r,
		ActionButtonsConfig: actionButtonsConfig,
		FileLinksConfig:     fileLinksConfig,
		APIPrefix:           sm.apiPrefix,
		WorkspaceUUID:       workspaceUUID,
		AvailableACPServers: resumeAvailableServers, // Pre-computed workspace server list
		GlobalMCPServer:     sm.mcpServer,
		AuxiliaryManager:    sm.auxiliaryManager,
		SharedProcess:       sharedProcess,  // Shared ACP process (nil = legacy mode)
		PruneConfig:         pruneConfig,    // Auto-pruning configuration (nil = no auto-pruning)
		OnStreamingStateChanged: func(sessionID string, isStreaming bool) {
			if sm.eventsManager != nil {
				sm.eventsManager.Broadcast(WSMsgTypeSessionStreaming, map[string]interface{}{
					"session_id":   sessionID,
					"is_streaming": isStreaming,
				})
			}
		},
		OnUIPromptStateChanged: func(sessionID string, isWaiting bool) {
			if sm.eventsManager != nil {
				sm.eventsManager.Broadcast(WSMsgTypeSessionUIPrompt, map[string]interface{}{
					"session_id": sessionID,
					"is_waiting": isWaiting,
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
		OnTitleGenerated: func(sessionID, title string) {
			if sm.eventsManager != nil {
				sm.eventsManager.Broadcast(WSMsgTypeSessionRenamed, map[string]string{
					"session_id": sessionID,
					"name":       title,
				})
			}
		},
	})
	if err != nil {
		signalDone(nil, err)
		return nil, err
	}

	sm.mu.Lock()
	// Check session limit (another session may have been created concurrently).
	if len(sm.sessions) >= MaxSessions {
		sm.mu.Unlock()
		bs.Close("session_limit_exceeded")
		signalDone(nil, ErrTooManySessions)
		return nil, ErrTooManySessions
	}
	// Defensive check: pendingResumes should prevent this case, but handle it gracefully.
	if existing, ok := sm.sessions[bs.GetSessionID()]; ok {
		sm.mu.Unlock()
		bs.Close("duplicate_session")
		if sm.logger != nil {
			sm.logger.Warn("Unexpected duplicate session after pendingResumes guard",
				"session_id", sessionID)
		}
		signalDone(existing, nil)
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

	// Trigger early MCP tools fetch to warm the cache before the first message.
	sm.ensureMCPToolsFetch(workspaceUUID)

	signalDone(bs, nil)
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

// ApplyACPServerRenames updates persisted sessions that reference renamed/removed ACP servers.
// Running sessions are closed and resumed so they reconnect with the updated ACP server.
func (sm *SessionManager) ApplyACPServerRenames(renames map[string]string) (*ACPServerRenameResult, error) {
	if sm == nil || sm.store == nil || len(renames) == 0 {
		return nil, nil
	}

	normalized := make(map[string]string, len(renames))
	for oldName, newName := range renames {
		if oldName == "" || newName == "" || oldName == newName {
			continue
		}
		normalized[oldName] = newName
	}
	if len(normalized) == 0 {
		return nil, nil
	}

	metas, err := sm.store.List()
	if err != nil {
		return nil, err
	}

	type restartTarget struct {
		sessionID  string
		name       string
		workingDir string
	}

	result := &ACPServerRenameResult{}
	restartTargets := make([]restartTarget, 0)
	for _, meta := range metas {
		newServer, ok := normalized[meta.ACPServer]
		if !ok {
			continue
		}

		if err := sm.store.UpdateMetadata(meta.SessionID, func(m *session.Metadata) {
			m.ACPServer = newServer
		}); err != nil {
			return nil, err
		}

		result.UpdatedSessionIDs = append(result.UpdatedSessionIDs, meta.SessionID)
		if sm.GetSession(meta.SessionID) != nil {
			restartTargets = append(restartTargets, restartTarget{
				sessionID:  meta.SessionID,
				name:       meta.Name,
				workingDir: meta.WorkingDir,
			})
		}
	}

	for _, target := range restartTargets {
		sm.CloseSession(target.sessionID, "acp_server_reconfigured")
		if _, err := sm.ResumeSession(target.sessionID, target.name, target.workingDir); err != nil {
			if sm.logger != nil {
				sm.logger.Error("Failed to resume session after ACP server reconfiguration",
					"session_id", target.sessionID,
					"working_dir", target.workingDir,
					"error", err)
			}
			continue
		}
		result.RestartedSessionIDs = append(result.RestartedSessionIDs, target.sessionID)
	}

	if len(result.UpdatedSessionIDs) == 0 && len(result.RestartedSessionIDs) == 0 {
		return nil, nil
	}

	return result, nil
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
	pm := sm.acpProcessManager
	sm.mu.Unlock()

	for _, bs := range sessions {
		bs.Close(reason)
	}

	// Close the shared ACP process manager after all sessions are closed
	if pm != nil {
		pm.StopGC() // Stop GC before closing processes
		pm.Close()
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
//
// Sessions sharing the same ACP process (identified by working directory + ACP server)
// are staggered by a configurable delay (session.startup_stagger_ms, default 300 ms)
// to prevent overwhelming the ACP SDK's internal notification channel.
func (sm *SessionManager) ProcessPendingQueues() {
	sm.mu.RLock()
	store := sm.store
	globalConv := sm.globalConversations
	mittoConfig := sm.mittoConfig
	sm.mu.RUnlock()

	if store == nil {
		return
	}

	// Determine the stagger delay between resumes on the same shared ACP process.
	staggerMs := config.DefaultStartupStaggerMs
	if mittoConfig != nil && mittoConfig.Session != nil {
		staggerMs = mittoConfig.Session.GetStartupStaggerMs()
	}
	staggerDelay := time.Duration(staggerMs) * time.Millisecond

	// List all persisted sessions
	sessions, err := store.List()
	if err != nil {
		if sm.logger != nil {
			sm.logger.Warn("Failed to list sessions for queue processing", "error", err)
		}
		return
	}

	// Sort sessions by most recent activity first so that sessions the user interacted
	// with recently become available sooner after restart. Prefer LastUserMessageAt;
	// fall back to UpdatedAt when LastUserMessageAt is zero.
	sort.Slice(sessions, func(i, j int) bool {
		ti := sessions[i].LastUserMessageAt
		if ti.IsZero() {
			ti = sessions[i].UpdatedAt
		}
		tj := sessions[j].LastUserMessageAt
		if tj.IsZero() {
			tj = sessions[j].UpdatedAt
		}
		return ti.After(tj) // descending — newest first
	})

	// lastResumedForProcess tracks the last time a session was resumed for a given
	// (workingDir, acpServer) pair — i.e., the shared ACP process key.
	// Used to apply stagger delays only within the same ACP process.
	type acpProcessKey struct {
		workingDir string
		acpServer  string
	}
	lastResumedForProcess := make(map[acpProcessKey]time.Time)

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
			lastActivity := meta.LastUserMessageAt
			if lastActivity.IsZero() {
				lastActivity = meta.UpdatedAt
			}
			sm.logger.Info("Found session with pending queue items",
				"session_id", meta.SessionID,
				"queue_length", queueLen,
				"working_dir", meta.WorkingDir,
				"last_activity", lastActivity.Format(time.RFC3339))
		}

		// Apply stagger delay for sessions sharing the same ACP process.
		// Sessions on the same (workingDir, acpServer) use the same SharedACPProcess;
		// resuming them in rapid succession floods the ACP SDK notification channel.
		if staggerDelay > 0 {
			key := acpProcessKey{workingDir: meta.WorkingDir, acpServer: meta.ACPServer}
			if last, ok := lastResumedForProcess[key]; ok {
				elapsed := time.Since(last)
				if elapsed < staggerDelay {
					wait := staggerDelay - elapsed
					if sm.logger != nil {
						sm.logger.Debug("Staggering session resume to avoid ACP notification overflow",
							"session_id", meta.SessionID,
							"acp_server", meta.ACPServer,
							"working_dir", meta.WorkingDir,
							"wait_ms", wait.Milliseconds())
					}
					time.Sleep(wait)
				}
			}
		}

		// Resume the session to process its queue.
		// The session will check the delay before actually sending.
		bs, err := sm.ResumeSession(meta.SessionID, meta.Name, meta.WorkingDir)
		if err != nil {
			if sm.logger != nil {
				sm.logger.Warn("Failed to resume session for queue processing",
					"session_id", meta.SessionID,
					"error", err)
			}
			continue
		}

		// Record the resume time for this ACP process key so the next session on the
		// same process waits the full stagger interval.
		if staggerDelay > 0 {
			key := acpProcessKey{workingDir: meta.WorkingDir, acpServer: meta.ACPServer}
			lastResumedForProcess[key] = time.Now()
		}

		// Try to process the queued message immediately.
		// Note: On startup, the delay is skipped because lastResponseComplete is zero.
		// Run in a goroutine so we don't block the stagger loop for other sessions.
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

// GetWorkspaceUUIDForSession returns the workspace UUID for a given session ID.
// Returns empty string if the session is not found.
func (sm *SessionManager) GetWorkspaceUUIDForSession(sessionID string) string {
	sm.mu.RLock()
	defer sm.mu.RUnlock()

	if bs, ok := sm.sessions[sessionID]; ok {
		return bs.workspaceUUID
	}
	return ""
}

// GetSessionInfoByWorkspace returns session info grouped by workspace UUID.
// Used by the ACP process GC to determine which processes are still needed.
// The caller must NOT hold sm.mu when calling this method.
func (sm *SessionManager) GetSessionInfoByWorkspace() map[string][]SessionInfo {
	sm.mu.RLock()
	defer sm.mu.RUnlock()

	result := make(map[string][]SessionInfo)
	for _, bs := range sm.sessions {
		uuid := bs.GetWorkspaceUUID()
		if uuid == "" {
			continue
		}

		var nextPeriodic *time.Time
		if sm.store != nil {
			if p, err := sm.store.Periodic(bs.GetSessionID()).Get(); err == nil && p.Enabled {
				nextPeriodic = p.NextScheduledAt
			}
		}

		var queueLen int
		if sm.store != nil {
			queueLen, _ = sm.store.Queue(bs.GetSessionID()).Len()
		}

		result[uuid] = append(result[uuid], SessionInfo{
			SessionID:             bs.GetSessionID(),
			WorkspaceUUID:         uuid,
			IsPrompting:           bs.IsPrompting(),
			HasObservers:          bs.HasObservers(),
			HasConnectedClients:   bs.HasConnectedClients(),
			QueueLength:           queueLen,
			NextPeriodicAt:        nextPeriodic,
			ResumedAt:             bs.StartedAt(),
			LastObserverRemovedAt: bs.LastObserverRemovedAt(),
			LastActivityAt:        bs.LastActivityAt(),
		})
	}
	return result
}

// CloseIdleSession closes a session that the GC has determined is idle,
// removing it from the manager and releasing its resources.
// Safe to call concurrently; no-op if the session is not found.
// The caller must NOT hold sm.mu when calling this method.
func (sm *SessionManager) CloseIdleSession(sessionID string) {
	sm.mu.Lock()
	bs, exists := sm.sessions[sessionID]
	if !exists {
		sm.mu.Unlock()
		return
	}
	delete(sm.sessions, sessionID)
	sm.mu.Unlock()

	// Clear cached plan state when session is closed
	sm.ClearCachedPlanState(sessionID)

	if bs != nil {
		if sm.logger != nil {
			sm.logger.Info("Closing idle session (GC)",
				"session_id", sessionID,
				"workspace_uuid", bs.GetWorkspaceUUID())
		}
		bs.Close("gc_idle")
	}
}

// IsMCPChecked returns whether MCP availability has been checked for a workspace.
func (sm *SessionManager) IsMCPChecked(workspaceUUID string) bool {
	sm.mcpCheckedWorkspacesMu.RLock()
	defer sm.mcpCheckedWorkspacesMu.RUnlock()
	return sm.mcpCheckedWorkspaces[workspaceUUID]
}

// MarkMCPChecked marks a workspace as having had MCP availability checked.
func (sm *SessionManager) MarkMCPChecked(workspaceUUID string) {
	sm.mcpCheckedWorkspacesMu.Lock()
	sm.mcpCheckedWorkspaces[workspaceUUID] = true
	sm.mcpCheckedWorkspacesMu.Unlock()
}

// ClearMCPChecked clears the MCP checked flag for a workspace.
// This should be called after running the MCP installation command.
func (sm *SessionManager) ClearMCPChecked(workspaceUUID string) {
	sm.mcpCheckedWorkspacesMu.Lock()
	delete(sm.mcpCheckedWorkspaces, workspaceUUID)
	sm.mcpCheckedWorkspacesMu.Unlock()
}

// IsMCPToolsFetched returns whether MCP tools have been fetched for the given workspace.
func (sm *SessionManager) IsMCPToolsFetched(workspaceUUID string) bool {
	sm.mcpToolsFetchedWorkspacesMu.RLock()
	defer sm.mcpToolsFetchedWorkspacesMu.RUnlock()
	return sm.mcpToolsFetchedWorkspaces[workspaceUUID]
}

// MarkMCPToolsFetched marks that MCP tools have been fetched for the given workspace.
func (sm *SessionManager) MarkMCPToolsFetched(workspaceUUID string) {
	sm.mcpToolsFetchedWorkspacesMu.Lock()
	defer sm.mcpToolsFetchedWorkspacesMu.Unlock()
	sm.mcpToolsFetchedWorkspaces[workspaceUUID] = true
}

// ClearMCPToolsFetched clears the MCP tools fetched flag for the given workspace.
func (sm *SessionManager) ClearMCPToolsFetched(workspaceUUID string) {
	sm.mcpToolsFetchedWorkspacesMu.Lock()
	defer sm.mcpToolsFetchedWorkspacesMu.Unlock()
	delete(sm.mcpToolsFetchedWorkspaces, workspaceUUID)
}

// ensureMCPToolsFetch triggers an asynchronous MCP tools fetch for the given workspace
// if not already fetched. This warms the cache so that processor enabledWhenMCP checks
// have tool names available by the time the first message is processed.
// Safe to call multiple times — only the first call for a workspace triggers the fetch.
func (sm *SessionManager) ensureMCPToolsFetch(workspaceUUID string) {
	if workspaceUUID == "" {
		return
	}
	if sm.IsMCPToolsFetched(workspaceUUID) {
		return
	}
	sm.MarkMCPToolsFetched(workspaceUUID)

	// Read auxiliaryManager and eventsManager under lock.
	sm.mu.RLock()
	auxMgr := sm.auxiliaryManager
	evtMgr := sm.eventsManager
	sm.mu.RUnlock()

	if auxMgr == nil {
		return
	}

	go func() {
		// Use a long timeout — auxiliary operations can take several minutes.
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
		defer cancel()

		if sm.logger != nil {
			sm.logger.Debug("early MCP tools fetch: starting",
				"workspace_uuid", workspaceUUID)
		}

		tools, err := auxMgr.FetchMCPTools(ctx, workspaceUUID)
		if err != nil {
			if sm.logger != nil {
				sm.logger.Debug("early MCP tools fetch: failed",
					"workspace_uuid", workspaceUUID,
					"error", err)
			}
			// Clear the fetched flag so the WebSocket fallback can retry.
			sm.ClearMCPToolsFetched(workspaceUUID)
			return
		}

		if sm.logger != nil {
			sm.logger.Debug("early MCP tools fetch: completed",
				"workspace_uuid", workspaceUUID,
				"tool_count", len(tools))
		}

		// If empty result, clear flag so next connection retries.
		if len(tools) == 0 {
			sm.ClearMCPToolsFetched(workspaceUUID)
			return
		}

		// Broadcast to frontend (same as triggerMCPToolsFetch in session_ws.go).
		if evtMgr != nil {
			evtMgr.Broadcast(WSMsgTypeMCPToolsAvailable, map[string]interface{}{
				"workspace_uuid": workspaceUUID,
				"tools":          tools,
			})
		}
	}()
}
