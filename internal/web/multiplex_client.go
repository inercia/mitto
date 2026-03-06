package web

import (
	"context"
	"sync"

	"github.com/coder/acp-go-sdk"

	mittoAcp "github.com/inercia/mitto/internal/acp"
)

// SessionCallbacks holds the per-session callback handlers that the MultiplexClient
// routes ACP events to. Each BackgroundSession registers its own set of callbacks.
type SessionCallbacks struct {
	// OnSessionUpdate handles streaming updates (agent messages, thoughts, tool calls, etc.)
	OnSessionUpdate func(ctx context.Context, params acp.SessionNotification) error
	// OnReadTextFile handles file read requests from the agent.
	OnReadTextFile func(ctx context.Context, params acp.ReadTextFileRequest) (acp.ReadTextFileResponse, error)
	// OnWriteTextFile handles file write requests from the agent.
	OnWriteTextFile func(ctx context.Context, params acp.WriteTextFileRequest) (acp.WriteTextFileResponse, error)
	// OnRequestPermission handles permission requests from the agent.
	OnRequestPermission func(ctx context.Context, params acp.RequestPermissionRequest) (acp.RequestPermissionResponse, error)
	// OnCreateTerminal handles terminal creation requests.
	OnCreateTerminal func(ctx context.Context, params acp.CreateTerminalRequest) (acp.CreateTerminalResponse, error)
	// OnTerminalOutput handles terminal output requests.
	OnTerminalOutput func(ctx context.Context, params acp.TerminalOutputRequest) (acp.TerminalOutputResponse, error)
	// OnReleaseTerminal handles terminal release requests.
	OnReleaseTerminal func(ctx context.Context, params acp.ReleaseTerminalRequest) (acp.ReleaseTerminalResponse, error)
	// OnWaitForTerminalExit handles terminal wait requests.
	OnWaitForTerminalExit func(ctx context.Context, params acp.WaitForTerminalExitRequest) (acp.WaitForTerminalExitResponse, error)
	// OnKillTerminalCommand handles terminal kill requests.
	OnKillTerminalCommand func(ctx context.Context, params acp.KillTerminalCommandRequest) (acp.KillTerminalCommandResponse, error)
}

// MultiplexClient implements acp.Client and routes all ACP callbacks to the
// correct BackgroundSession based on the SessionId included in each request.
//
// This enables multiple ACP sessions to share a single ACP server process,
// with each session receiving only its own events.
type MultiplexClient struct {
	mu       sync.RWMutex
	sessions map[acp.SessionId]*SessionCallbacks
}

// Ensure MultiplexClient implements acp.Client
var _ acp.Client = (*MultiplexClient)(nil)

// NewMultiplexClient creates a new MultiplexClient.
func NewMultiplexClient() *MultiplexClient {
	return &MultiplexClient{
		sessions: make(map[acp.SessionId]*SessionCallbacks),
	}
}

// RegisterSession registers callbacks for a session. The MultiplexClient will
// route ACP events with the given sessionID to these callbacks.
func (mc *MultiplexClient) RegisterSession(sessionID acp.SessionId, callbacks *SessionCallbacks) {
	mc.mu.Lock()
	defer mc.mu.Unlock()
	mc.sessions[sessionID] = callbacks
}

// UnregisterSession removes callbacks for a session.
func (mc *MultiplexClient) UnregisterSession(sessionID acp.SessionId) {
	mc.mu.Lock()
	defer mc.mu.Unlock()
	delete(mc.sessions, sessionID)
}

// getSession returns the callbacks for the given session, or nil if not found.
func (mc *MultiplexClient) getSession(sessionID acp.SessionId) *SessionCallbacks {
	mc.mu.RLock()
	defer mc.mu.RUnlock()
	return mc.sessions[sessionID]
}

// SessionUpdate routes streaming updates to the correct session.
func (mc *MultiplexClient) SessionUpdate(ctx context.Context, params acp.SessionNotification) error {
	cb := mc.getSession(params.SessionId)
	if cb == nil || cb.OnSessionUpdate == nil {
		return nil // Unknown session or no handler, ignore
	}
	return cb.OnSessionUpdate(ctx, params)
}

// ReadTextFile routes file read requests to the correct session.
func (mc *MultiplexClient) ReadTextFile(ctx context.Context, params acp.ReadTextFileRequest) (acp.ReadTextFileResponse, error) {
	cb := mc.getSession(params.SessionId)
	if cb == nil || cb.OnReadTextFile == nil {
		// Fallback: perform the read without session-specific tracking
		return defaultReadTextFile(params)
	}
	return cb.OnReadTextFile(ctx, params)
}

// WriteTextFile routes file write requests to the correct session.
func (mc *MultiplexClient) WriteTextFile(ctx context.Context, params acp.WriteTextFileRequest) (acp.WriteTextFileResponse, error) {
	cb := mc.getSession(params.SessionId)
	if cb == nil || cb.OnWriteTextFile == nil {
		// Fallback: perform the write without session-specific tracking
		return defaultWriteTextFile(params)
	}
	return cb.OnWriteTextFile(ctx, params)
}

// RequestPermission routes permission requests to the correct session.
func (mc *MultiplexClient) RequestPermission(ctx context.Context, params acp.RequestPermissionRequest) (acp.RequestPermissionResponse, error) {
	cb := mc.getSession(params.SessionId)
	if cb == nil || cb.OnRequestPermission == nil {
		// No handler - cancel the permission request
		return acp.RequestPermissionResponse{
			Outcome: acp.RequestPermissionOutcome{
				Cancelled: &acp.RequestPermissionOutcomeCancelled{},
			},
		}, nil
	}
	return cb.OnRequestPermission(ctx, params)
}

// CreateTerminal routes terminal creation requests to the correct session.
func (mc *MultiplexClient) CreateTerminal(ctx context.Context, params acp.CreateTerminalRequest) (acp.CreateTerminalResponse, error) {
	cb := mc.getSession(params.SessionId)
	if cb == nil || cb.OnCreateTerminal == nil {
		return defaultCreateTerminal(params)
	}
	return cb.OnCreateTerminal(ctx, params)
}

// KillTerminalCommand routes terminal kill requests to the correct session.
func (mc *MultiplexClient) KillTerminalCommand(ctx context.Context, params acp.KillTerminalCommandRequest) (acp.KillTerminalCommandResponse, error) {
	cb := mc.getSession(params.SessionId)
	if cb == nil || cb.OnKillTerminalCommand == nil {
		return defaultKillTerminalCommand(params)
	}
	return cb.OnKillTerminalCommand(ctx, params)
}

// TerminalOutput routes terminal output requests to the correct session.
func (mc *MultiplexClient) TerminalOutput(ctx context.Context, params acp.TerminalOutputRequest) (acp.TerminalOutputResponse, error) {
	cb := mc.getSession(params.SessionId)
	if cb == nil || cb.OnTerminalOutput == nil {
		return defaultTerminalOutput(params)
	}
	return cb.OnTerminalOutput(ctx, params)
}

// ReleaseTerminal routes terminal release requests to the correct session.
func (mc *MultiplexClient) ReleaseTerminal(ctx context.Context, params acp.ReleaseTerminalRequest) (acp.ReleaseTerminalResponse, error) {
	cb := mc.getSession(params.SessionId)
	if cb == nil || cb.OnReleaseTerminal == nil {
		return defaultReleaseTerminal(params)
	}
	return cb.OnReleaseTerminal(ctx, params)
}

// WaitForTerminalExit routes terminal wait requests to the correct session.
func (mc *MultiplexClient) WaitForTerminalExit(ctx context.Context, params acp.WaitForTerminalExitRequest) (acp.WaitForTerminalExitResponse, error) {
	cb := mc.getSession(params.SessionId)
	if cb == nil || cb.OnWaitForTerminalExit == nil {
		return defaultWaitForTerminalExit(params)
	}
	return cb.OnWaitForTerminalExit(ctx, params)
}

// Default fallback implementations for operations when no session is registered.

func defaultReadTextFile(params acp.ReadTextFileRequest) (acp.ReadTextFileResponse, error) {
	// Use the same filesystem handler as WebClient
	content, err := mittoAcp.DefaultFileSystem.ReadTextFile(params.Path, params.Line, params.Limit)
	if err != nil {
		return acp.ReadTextFileResponse{}, err
	}
	return acp.ReadTextFileResponse{Content: content}, nil
}

func defaultWriteTextFile(params acp.WriteTextFileRequest) (acp.WriteTextFileResponse, error) {
	if err := mittoAcp.DefaultFileSystem.WriteTextFile(params.Path, params.Content); err != nil {
		return acp.WriteTextFileResponse{}, err
	}
	return acp.WriteTextFileResponse{}, nil
}

func defaultCreateTerminal(params acp.CreateTerminalRequest) (acp.CreateTerminalResponse, error) {
	return webTerminalStub.CreateTerminal(context.Background(), params)
}

func defaultKillTerminalCommand(params acp.KillTerminalCommandRequest) (acp.KillTerminalCommandResponse, error) {
	return webTerminalStub.KillTerminalCommand(context.Background(), params)
}

func defaultTerminalOutput(params acp.TerminalOutputRequest) (acp.TerminalOutputResponse, error) {
	return webTerminalStub.TerminalOutput(context.Background(), params)
}

func defaultReleaseTerminal(params acp.ReleaseTerminalRequest) (acp.ReleaseTerminalResponse, error) {
	return webTerminalStub.ReleaseTerminal(context.Background(), params)
}

func defaultWaitForTerminalExit(params acp.WaitForTerminalExitRequest) (acp.WaitForTerminalExitResponse, error) {
	return webTerminalStub.WaitForTerminalExit(context.Background(), params)
}
