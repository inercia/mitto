package web

import (
	"log/slog"
	"sync"

	"github.com/inercia/mitto/internal/session"
)

// SessionManager manages background sessions that run independently of WebSocket connections.
// It is safe for concurrent use.
type SessionManager struct {
	mu       sync.RWMutex
	sessions map[string]*BackgroundSession // keyed by persisted session ID
	logger   *slog.Logger

	// Configuration for new sessions
	acpCommand  string
	acpServer   string
	autoApprove bool
	store       *session.Store
}

// NewSessionManager creates a new session manager.
func NewSessionManager(acpCommand, acpServer string, autoApprove bool, logger *slog.Logger) *SessionManager {
	return &SessionManager{
		sessions:    make(map[string]*BackgroundSession),
		logger:      logger,
		acpCommand:  acpCommand,
		acpServer:   acpServer,
		autoApprove: autoApprove,
	}
}

// SetStore sets the session store for persistence.
func (sm *SessionManager) SetStore(store *session.Store) {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	sm.store = store
}

// CreateSession creates a new background session and registers it.
func (sm *SessionManager) CreateSession(name, workingDir string) (*BackgroundSession, error) {
	sm.mu.Lock()
	store := sm.store
	sm.mu.Unlock()

	bs, err := NewBackgroundSession(BackgroundSessionConfig{
		ACPCommand:  sm.acpCommand,
		ACPServer:   sm.acpServer,
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
	sm.sessions[bs.GetSessionID()] = bs
	sm.mu.Unlock()

	if sm.logger != nil {
		sm.logger.Info("Created background session",
			"session_id", bs.GetSessionID(),
			"acp_id", bs.GetACPID())
	}

	return bs, nil
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
