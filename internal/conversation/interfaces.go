package conversation

import (
	"context"
	"log/slog"

	acp "github.com/coder/acp-go-sdk"
	"github.com/inercia/mitto/internal/config"
	"github.com/inercia/mitto/internal/runner"
)

// SharedProcess is the interface that a shared ACP OS process must satisfy.
// BackgroundSession uses this interface (rather than *SharedACPProcess directly)
// so that the domain layer does not depend on the web infrastructure package.
//
// The 13 methods below correspond exactly to the exported methods of
// *internal/web.SharedACPProcess that BackgroundSession calls.
type SharedProcess interface {
	// NewSession creates a new ACP session on this process.
	NewSession(ctx context.Context, cwd string, mcpServers []acp.McpServer) (*SessionHandle, error)
	// LoadSession loads (replays) an existing ACP session on this process.
	LoadSession(ctx context.Context, acpSessionID, cwd string, mcpServers []acp.McpServer) (*SessionHandle, error)
	// ResumeSession resumes a previously archived ACP session on this process.
	ResumeSession(ctx context.Context, acpSessionID, cwd string, mcpServers []acp.McpServer) (*SessionHandle, error)
	// RegisterSession wires per-session event callbacks into the multiplex layer.
	RegisterSession(sessionID acp.SessionId, callbacks *SessionCallbacks)
	// UnregisterSession removes a session's callbacks from the multiplex layer.
	UnregisterSession(sessionID acp.SessionId)
	// ProcessDone returns a channel closed when the OS process exits.
	ProcessDone() <-chan struct{}
	// Prompt sends a prompt to the agent for a specific session.
	Prompt(ctx context.Context, sessionID acp.SessionId, content []acp.ContentBlock) (acp.PromptResponse, error)
	// Cancel cancels the current in-progress prompt for a session.
	Cancel(ctx context.Context, sessionID acp.SessionId) error
	// SetSessionMode switches the session to a new mode (e.g. "code", "default").
	SetSessionMode(ctx context.Context, sessionID acp.SessionId, modeID string) error
	// SetSessionModel switches the session to a different model.
	SetSessionModel(ctx context.Context, sessionID acp.SessionId, modelID string) error
	// Done returns a channel closed when the process has fully shut down.
	Done() <-chan struct{}
	// Capabilities returns the agent's advertised capabilities.
	Capabilities() *acp.AgentCapabilities
	// Restart attempts to restart the underlying OS process.
	Restart() error
}

// PromptResolver resolves a prompt name to its full text for a given working directory.
// It is used by BackgroundSession, SessionManager, and PeriodicRunner to look up
// named workspace prompts at execution time.
type PromptResolver func(promptName string, workingDir string) (string, error)

// ProcessManager abstracts the shared ACP process manager (web.ACPProcessManager)
// so the domain layer does not depend on the web infrastructure package.
type ProcessManager interface {
	GetOrCreateProcess(workspace *config.WorkspaceSettings, acpCommand, acpCwd string, acpEnv map[string]string, r *runner.Runner, prewarm bool) (SharedProcess, error)
	EnsurePrewarmed(workspaceUUID string, logger *slog.Logger)
	ClearGCSuspended(sessionID string)
	IsGCSuspended(sessionID string) bool
	StopGC()
	Close()
	ProcessCount() int
}

// EventsBroadcaster abstracts the global events manager (web.GlobalEventsManager)
// for broadcasting WebSocket events to connected clients.
type EventsBroadcaster interface {
	Broadcast(msgType string, data interface{})
	ClientCount() int
}
