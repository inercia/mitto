package web

import (
	"errors"
	"log/slog"
	"sync"

	"github.com/inercia/mitto/internal/session"
)

// MaxSessions is the maximum number of concurrent sessions allowed.
const MaxSessions = 32

// ErrTooManySessions is returned when the session limit is reached.
var ErrTooManySessions = errors.New("maximum number of sessions reached")

// WorkspaceSaveFunc is a callback function called when workspaces are modified.
// It receives the current list of workspaces to be persisted.
type WorkspaceSaveFunc func(workspaces []WorkspaceConfig) error

// SessionManager manages background sessions that run independently of WebSocket connections.
// It is safe for concurrent use.
type SessionManager struct {
	mu       sync.RWMutex
	sessions map[string]*BackgroundSession // keyed by persisted session ID
	logger   *slog.Logger

	// Workspaces configuration - maps directory to workspace config
	workspaces map[string]*WorkspaceConfig

	// Default workspace (used when no specific workspace is requested)
	defaultWorkspace *WorkspaceConfig

	// fromCLI indicates whether workspaces came from CLI flags.
	// When true, workspace changes are NOT persisted to disk.
	fromCLI bool

	// onWorkspaceSave is called when workspaces are modified (only if fromCLI is false).
	onWorkspaceSave WorkspaceSaveFunc

	// Legacy single-workspace fields (used if no workspaces configured)
	acpCommand  string
	acpServer   string
	autoApprove bool
	store       *session.Store
}

// NewSessionManager creates a new session manager with legacy single-workspace configuration.
func NewSessionManager(acpCommand, acpServer string, autoApprove bool, logger *slog.Logger) *SessionManager {
	return &SessionManager{
		sessions:    make(map[string]*BackgroundSession),
		workspaces:  make(map[string]*WorkspaceConfig),
		logger:      logger,
		acpCommand:  acpCommand,
		acpServer:   acpServer,
		autoApprove: autoApprove,
	}
}

// SessionManagerOptions contains options for creating a SessionManager.
type SessionManagerOptions struct {
	// Workspaces is the initial list of workspaces.
	Workspaces []WorkspaceConfig
	// AutoApprove enables automatic approval of permission requests.
	AutoApprove bool
	// Logger is the logger to use.
	Logger *slog.Logger
	// FromCLI indicates whether workspaces came from CLI flags.
	// When true, workspace changes are NOT persisted to disk.
	FromCLI bool
	// OnWorkspaceSave is called when workspaces are modified (only if FromCLI is false).
	OnWorkspaceSave WorkspaceSaveFunc
}

// NewSessionManagerWithWorkspaces creates a new session manager with multiple workspaces.
// Deprecated: Use NewSessionManagerWithOptions instead.
func NewSessionManagerWithWorkspaces(workspaces []WorkspaceConfig, autoApprove bool, logger *slog.Logger) *SessionManager {
	return NewSessionManagerWithOptions(SessionManagerOptions{
		Workspaces:  workspaces,
		AutoApprove: autoApprove,
		Logger:      logger,
		FromCLI:     true, // Default to CLI behavior for backward compatibility
	})
}

// NewSessionManagerWithOptions creates a new session manager with the given options.
func NewSessionManagerWithOptions(opts SessionManagerOptions) *SessionManager {
	sm := &SessionManager{
		sessions:        make(map[string]*BackgroundSession),
		workspaces:      make(map[string]*WorkspaceConfig),
		logger:          opts.Logger,
		autoApprove:     opts.AutoApprove,
		fromCLI:         opts.FromCLI,
		onWorkspaceSave: opts.OnWorkspaceSave,
	}

	for i := range opts.Workspaces {
		ws := &opts.Workspaces[i]
		sm.workspaces[ws.WorkingDir] = ws
		if sm.defaultWorkspace == nil {
			sm.defaultWorkspace = ws
			// Also set legacy fields for backward compatibility
			sm.acpCommand = ws.ACPCommand
			sm.acpServer = ws.ACPServer
		}
	}

	return sm
}

// SetWorkspaces sets the available workspaces.
func (sm *SessionManager) SetWorkspaces(workspaces []WorkspaceConfig) {
	sm.mu.Lock()

	sm.workspaces = make(map[string]*WorkspaceConfig)
	sm.defaultWorkspace = nil

	for i := range workspaces {
		ws := &workspaces[i]
		sm.workspaces[ws.WorkingDir] = ws
		if sm.defaultWorkspace == nil {
			sm.defaultWorkspace = ws
			sm.acpCommand = ws.ACPCommand
			sm.acpServer = ws.ACPServer
		}
	}

	// Save workspaces if not from CLI and callback is set
	shouldSave := !sm.fromCLI && sm.onWorkspaceSave != nil
	var workspacesToSave []WorkspaceConfig
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
func (sm *SessionManager) GetWorkspaces() []WorkspaceConfig {
	sm.mu.RLock()
	defer sm.mu.RUnlock()

	if len(sm.workspaces) == 0 {
		// Only return legacy single workspace if it has valid configuration
		if sm.acpCommand != "" {
			return []WorkspaceConfig{{
				ACPServer:  sm.acpServer,
				ACPCommand: sm.acpCommand,
				WorkingDir: "", // Will be set at session creation time
			}}
		}
		// No valid workspace configuration - return empty slice
		return []WorkspaceConfig{}
	}

	result := make([]WorkspaceConfig, 0, len(sm.workspaces))
	for _, ws := range sm.workspaces {
		result = append(result, *ws)
	}
	return result
}

// GetWorkspace returns the workspace for a given directory.
func (sm *SessionManager) GetWorkspace(workingDir string) *WorkspaceConfig {
	sm.mu.RLock()
	defer sm.mu.RUnlock()

	if ws, ok := sm.workspaces[workingDir]; ok {
		return ws
	}
	return nil
}

// GetDefaultWorkspace returns the default workspace.
func (sm *SessionManager) GetDefaultWorkspace() *WorkspaceConfig {
	sm.mu.RLock()
	defer sm.mu.RUnlock()

	if sm.defaultWorkspace != nil {
		return sm.defaultWorkspace
	}
	// Return legacy config as workspace
	return &WorkspaceConfig{
		ACPServer:  sm.acpServer,
		ACPCommand: sm.acpCommand,
		WorkingDir: "",
	}
}

// AddWorkspace adds a new workspace to the manager.
// If workspaces were not loaded from CLI flags and a save callback is set,
// the workspaces will be persisted to disk.
func (sm *SessionManager) AddWorkspace(ws WorkspaceConfig) {
	sm.mu.Lock()

	// Initialize workspaces map if needed
	if sm.workspaces == nil {
		sm.workspaces = make(map[string]*WorkspaceConfig)
	}

	// Add the workspace
	sm.workspaces[ws.WorkingDir] = &ws

	// Set as default if it's the first one
	if sm.defaultWorkspace == nil {
		sm.defaultWorkspace = &ws
		sm.acpCommand = ws.ACPCommand
		sm.acpServer = ws.ACPServer
	}

	if sm.logger != nil {
		sm.logger.Info("Added workspace",
			"working_dir", ws.WorkingDir,
			"acp_server", ws.ACPServer,
			"total_workspaces", len(sm.workspaces))
	}

	// Save workspaces if not from CLI and callback is set
	shouldSave := !sm.fromCLI && sm.onWorkspaceSave != nil
	var workspacesToSave []WorkspaceConfig
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
func (sm *SessionManager) getWorkspacesLocked() []WorkspaceConfig {
	result := make([]WorkspaceConfig, 0, len(sm.workspaces))
	for _, ws := range sm.workspaces {
		result = append(result, *ws)
	}
	return result
}

// RemoveWorkspace removes a workspace from the manager.
// If workspaces were not loaded from CLI flags and a save callback is set,
// the workspaces will be persisted to disk.
func (sm *SessionManager) RemoveWorkspace(workingDir string) {
	sm.mu.Lock()

	if sm.workspaces == nil {
		sm.mu.Unlock()
		return
	}

	delete(sm.workspaces, workingDir)

	// If we removed the default workspace, pick a new one
	if sm.defaultWorkspace != nil && sm.defaultWorkspace.WorkingDir == workingDir {
		sm.defaultWorkspace = nil
		for _, ws := range sm.workspaces {
			sm.defaultWorkspace = ws
			sm.acpCommand = ws.ACPCommand
			sm.acpServer = ws.ACPServer
			break
		}
	}

	if sm.logger != nil {
		sm.logger.Info("Removed workspace",
			"working_dir", workingDir,
			"total_workspaces", len(sm.workspaces))
	}

	// Save workspaces if not from CLI and callback is set
	shouldSave := !sm.fromCLI && sm.onWorkspaceSave != nil
	var workspacesToSave []WorkspaceConfig
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

// CreateSession creates a new background session and registers it.
// Returns ErrTooManySessions if the session limit is reached.
// Uses the workspace configuration for the given working directory, or the default if not found.
func (sm *SessionManager) CreateSession(name, workingDir string) (*BackgroundSession, error) {
	return sm.CreateSessionWithWorkspace(name, workingDir, nil)
}

// CreateSessionWithWorkspace creates a new session using the specified workspace configuration.
// If workspace is nil, looks up the workspace by workingDir or uses the default.
func (sm *SessionManager) CreateSessionWithWorkspace(name, workingDir string, workspace *WorkspaceConfig) (*BackgroundSession, error) {
	sm.mu.Lock()
	if len(sm.sessions) >= MaxSessions {
		sm.mu.Unlock()
		return nil, ErrTooManySessions
	}
	store := sm.store

	// Determine ACP command and server
	acpCommand := sm.acpCommand
	acpServer := sm.acpServer

	if workspace != nil {
		acpCommand = workspace.ACPCommand
		acpServer = workspace.ACPServer
		if workingDir == "" {
			workingDir = workspace.WorkingDir
		}
	} else if ws, ok := sm.workspaces[workingDir]; ok {
		acpCommand = ws.ACPCommand
		acpServer = ws.ACPServer
	}
	sm.mu.Unlock()

	bs, err := NewBackgroundSession(BackgroundSessionConfig{
		ACPCommand:  acpCommand,
		ACPServer:   acpServer,
		WorkingDir:  workingDir,
		AutoApprove: sm.autoApprove,
		Logger:      sm.logger,
		Store:       store,
		SessionName: name,
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
	sm.sessions[bs.GetSessionID()] = bs
	sm.mu.Unlock()

	if sm.logger != nil {
		sm.logger.Info("Created background session",
			"session_id", bs.GetSessionID(),
			"acp_id", bs.GetACPID(),
			"acp_server", acpServer,
			"working_dir", workingDir,
			"total_sessions", len(sm.sessions))
	}

	return bs, nil
}

// SessionCount returns the number of running sessions.
func (sm *SessionManager) SessionCount() int {
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	return len(sm.sessions)
}

// GetSession returns a running session by ID, or nil if not found.
func (sm *SessionManager) GetSession(sessionID string) *BackgroundSession {
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	return sm.sessions[sessionID]
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
// This is used when switching to an old conversation - we can't truly resume the ACP
// session, but we can create a new ACP connection and continue using the same
// persisted session ID for recording.
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

	// Determine ACP command and server from workspace configuration
	acpCommand := sm.acpCommand
	acpServer := sm.acpServer
	if ws, ok := sm.workspaces[workingDir]; ok {
		acpCommand = ws.ACPCommand
		acpServer = ws.ACPServer
	}

	// If still no ACP command, try to get it from session metadata
	// This handles the case where the workspace configuration is not available
	// (e.g., server restarted without the same --dir flags)
	if acpCommand == "" && store != nil {
		if meta, err := store.GetMetadata(sessionID); err == nil {
			if meta.ACPCommand != "" {
				acpCommand = meta.ACPCommand
				if sm.logger != nil {
					sm.logger.Info("Using ACP command from session metadata",
						"session_id", sessionID,
						"acp_command", acpCommand)
				}
			}
			if acpServer == "" && meta.ACPServer != "" {
				acpServer = meta.ACPServer
			}
		}
	}
	sm.mu.Unlock()

	// Create a background session with the existing persisted session ID
	bs, err := ResumeBackgroundSession(BackgroundSessionConfig{
		PersistedID: sessionID,
		ACPCommand:  acpCommand,
		ACPServer:   acpServer,
		WorkingDir:  workingDir,
		AutoApprove: sm.autoApprove,
		Logger:      sm.logger,
		Store:       store,
		SessionName: sessionName,
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
	sm.sessions[bs.GetSessionID()] = bs
	sm.mu.Unlock()

	if sm.logger != nil {
		sm.logger.Info("Resumed background session",
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
func (sm *SessionManager) CloseSession(sessionID, reason string) {
	sm.mu.Lock()
	bs := sm.sessions[sessionID]
	delete(sm.sessions, sessionID)
	sm.mu.Unlock()

	if bs != nil {
		bs.Close(reason)
		if sm.logger != nil {
			sm.logger.Info("Closed background session",
				"session_id", sessionID,
				"reason", reason)
		}
	}
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
