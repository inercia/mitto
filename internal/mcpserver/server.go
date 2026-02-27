// Package mcpserver provides an MCP (Model Context Protocol) server for Mitto.
// The server exposes tools for inspecting conversations and configuration,
// as well as session-scoped tools for interacting with specific conversations.
// It binds only to 127.0.0.1 for security reasons.
package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/inercia/mitto/internal/auxiliary"
	"github.com/inercia/mitto/internal/config"
	"github.com/inercia/mitto/internal/logging"
	"github.com/inercia/mitto/internal/session"
)

const (
	// DefaultPort is the default port for the MCP server.
	DefaultPort = 5757
	// ServerName is the name of the MCP server.
	ServerName = "mitto"
	// ServerVersion is the version of the MCP server.
	ServerVersion = "1.0.0"
)

// TransportMode specifies the transport mode for the MCP server.
type TransportMode string

const (
	// TransportModeSSE uses Server-Sent Events over HTTP (default).
	// The server listens on a TCP port and clients connect via HTTP.
	TransportModeSSE TransportMode = "sse"

	// TransportModeSTDIO uses standard input/output for communication.
	// This is useful for running the MCP server as a subprocess.
	TransportModeSTDIO TransportMode = "stdio"
)

// Server is the MCP server for Mitto.
// It serves both global tools (always available) and session-scoped tools
// (which require a session_id parameter and route to specific conversations).
type Server struct {
	mcpServer *mcp.Server
	logger    *slog.Logger
	host      string
	port      int
	mode      TransportMode
	listener  net.Listener
	httpSrv   *http.Server

	// For STDIO mode
	stdioSession *mcp.ServerSession
	stdioDone    chan struct{}

	mu             sync.RWMutex
	store          *session.Store
	config         *config.Config
	sessionManager SessionManager
	running        bool
	shutdown       bool

	// Session registry for session-scoped tools.
	// Maps session_id -> registeredSession for routing UI prompts and checking permissions.
	sessionsMu sync.RWMutex
	sessions   map[string]*registeredSession

	// Pending request registry for correlating MCP requests with Mitto sessions.
	// When the ACP layer sees a tool_call for mitto_get_current_session, it registers
	// the request_id -> session_id mapping here. The MCP handler then looks it up.
	// Maps request_id -> pendingRequest
	pendingRequestsMu sync.RWMutex
	pendingRequests   map[string]*pendingRequest
}

// registeredSession holds information about a registered session.
// This is used to route UI prompts to the correct session and check permissions.
type registeredSession struct {
	sessionID  string
	uiPrompter UIPrompter
	logger     *slog.Logger
}

// pendingRequest holds information about a pending MCP request correlation.
type pendingRequest struct {
	sessionID    string
	registeredAt time.Time
}

// pendingRequestTimeout is how long we wait for a pending request to be registered.
const pendingRequestTimeout = 5 * time.Second

// pendingRequestPollInterval is how often we poll for a pending request.
const pendingRequestPollInterval = 50 * time.Millisecond

// pendingRequestExpiry is how long pending requests are kept before cleanup.
const pendingRequestExpiry = 30 * time.Second

// Dependencies holds the dependencies needed by the MCP server.
type Dependencies struct {
	Store  *session.Store
	Config *config.Config
	// SessionManager is optional - provides info about running sessions
	SessionManager SessionManager
}

// SessionManager interface for checking session status and managing sessions.
type SessionManager interface {
	GetSession(sessionID string) BackgroundSession
	ListRunningSessions() []string
	// CloseSessionGracefully waits for any active response to complete before closing.
	// Returns true if closed, false if timeout expired while waiting.
	CloseSessionGracefully(sessionID, reason string, timeout time.Duration) bool
	// CloseSession immediately closes a session.
	CloseSession(sessionID, reason string)
	// ResumeSession resumes an archived session by starting a new ACP connection.
	ResumeSession(sessionID, sessionName, workingDir string) (BackgroundSession, error)
}

// BackgroundSession interface for session info.
type BackgroundSession interface {
	IsPrompting() bool
	GetEventCount() int
	GetMaxAssignedSeq() int64
}

// Config holds the configuration for the MCP server.
type Config struct {
	// Host is the address to bind to (default: "127.0.0.1"). Only used in SSE mode.
	Host string

	// Port to listen on (default: 5757). Only used in SSE mode.
	Port int

	// Mode specifies the transport mode (sse or stdio). Default: sse.
	Mode TransportMode
}

// NewServer creates a new MCP server.
// If cfg.Port is -1, the default port (5757) is used.
// If cfg.Port is 0, a random available port is assigned when the server starts.
// If cfg.Host is empty, the default host (127.0.0.1) is used.
func NewServer(cfg Config, deps Dependencies) (*Server, error) {
	logger := logging.MCP()

	// Port -1 means use default, 0 means random available port
	if cfg.Port < 0 {
		cfg.Port = DefaultPort
	}

	// Host defaults to localhost only for security
	if cfg.Host == "" {
		cfg.Host = "127.0.0.1"
	}

	if cfg.Mode == "" {
		cfg.Mode = TransportModeSSE
	}

	s := &Server{
		logger:          logger,
		host:            cfg.Host,
		port:            cfg.Port,
		mode:            cfg.Mode,
		store:           deps.Store,
		config:          deps.Config,
		sessionManager:  deps.SessionManager,
		sessions:        make(map[string]*registeredSession),
		pendingRequests: make(map[string]*pendingRequest),
	}

	// Create MCP server
	mcpSrv := mcp.NewServer(&mcp.Implementation{
		Name:    ServerName,
		Version: ServerVersion,
	}, nil)

	// Register global tools (always available)
	s.registerGlobalTools(mcpSrv, deps)

	// Register session-scoped tools (require session_id parameter)
	s.registerSessionScopedTools(mcpSrv)

	s.mcpServer = mcpSrv
	return s, nil
}

// Start starts the MCP server.
// For SSE mode, it starts an HTTP server on 127.0.0.1.
// For STDIO mode, it starts reading from stdin and writing to stdout.
func (s *Server) Start(ctx context.Context) error {
	s.mu.Lock()
	if s.running {
		s.mu.Unlock()
		return fmt.Errorf("server already running")
	}
	s.mu.Unlock()

	switch s.mode {
	case TransportModeSTDIO:
		return s.startSTDIO(ctx)
	case TransportModeSSE:
		return s.startSSE(ctx)
	default:
		return fmt.Errorf("unknown transport mode: %s", s.mode)
	}
}

// startSSE starts the MCP server in HTTP mode on the configured host.
// Despite the name, this uses the Streamable HTTP transport (MCP spec 2025-03-26)
// which is different from the legacy SSE transport.
func (s *Server) startSSE(ctx context.Context) error {
	addr := fmt.Sprintf("%s:%d", s.host, s.port)
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("failed to listen on %s: %w", addr, err)
	}

	s.mu.Lock()
	s.listener = listener
	s.running = true
	actualPort := listener.Addr().(*net.TCPAddr).Port
	s.port = actualPort
	s.mu.Unlock()

	s.logger.Info("MCP server started",
		"mode", "http",
		"host", s.host,
		"port", actualPort,
	)

	// Create HTTP server using Streamable HTTP transport (MCP spec 2025-03-26).
	// This is the modern transport that Augment Agent and other clients use.
	mux := http.NewServeMux()

	// Create Streamable HTTP handler - this handles all MCP communication
	streamableHandler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server {
		return s.mcpServer
	}, nil) // nil options uses defaults

	// Mount on /mcp (standard endpoint for Streamable HTTP)
	mux.Handle("/mcp", streamableHandler)

	// Also mount on root for convenience
	mux.Handle("/", streamableHandler)

	s.httpSrv = &http.Server{Handler: mux}

	go func() {
		if err := s.httpSrv.Serve(listener); err != nil && err != http.ErrServerClosed {
			s.logger.Error("MCP server error", "error", err)
		}
	}()

	return nil
}

// startSTDIO starts the MCP server in STDIO mode.
// This is a non-blocking call that starts the server in a goroutine.
// Use Wait() to block until the server stops.
func (s *Server) startSTDIO(ctx context.Context) error {
	s.mu.Lock()
	s.running = true
	s.stdioDone = make(chan struct{})
	s.mu.Unlock()

	s.logger.Info("MCP server started", "mode", "stdio")

	// Start STDIO transport in a goroutine
	go func() {
		defer close(s.stdioDone)

		transport := &mcp.StdioTransport{}
		session, err := s.mcpServer.Connect(ctx, transport, nil)
		if err != nil {
			s.logger.Error("Failed to connect STDIO transport", "error", err)
			return
		}

		s.mu.Lock()
		s.stdioSession = session
		s.mu.Unlock()

		// Wait for the session to end
		if err := session.Wait(); err != nil {
			s.logger.Debug("STDIO session ended", "error", err)
		}

		s.mu.Lock()
		s.running = false
		s.stdioSession = nil
		s.mu.Unlock()

		s.logger.Info("MCP server stopped", "mode", "stdio")
	}()

	return nil
}

// Wait blocks until the server stops (STDIO mode only).
// For SSE mode, this returns immediately.
func (s *Server) Wait() error {
	s.mu.RLock()
	done := s.stdioDone
	s.mu.RUnlock()

	if done != nil {
		<-done
	}
	return nil
}

// Stop stops the MCP server gracefully.
func (s *Server) Stop() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.running || s.shutdown {
		return nil
	}

	s.shutdown = true
	s.running = false

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Stop SSE mode resources
	if s.httpSrv != nil {
		if err := s.httpSrv.Shutdown(ctx); err != nil {
			s.logger.Warn("Error shutting down MCP HTTP server", "error", err)
		}
	}

	if s.listener != nil {
		s.listener.Close()
	}

	// Stop STDIO mode resources
	if s.stdioSession != nil {
		if err := s.stdioSession.Close(); err != nil {
			s.logger.Warn("Error closing STDIO session", "error", err)
		}
	}

	s.logger.Info("MCP server stopped")
	return nil
}

// Port returns the actual port the server is listening on.
// Returns 0 for STDIO mode.
func (s *Server) Port() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.port
}

// Host returns the host address the server is bound to.
// Returns empty string for STDIO mode.
func (s *Server) Host() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.host
}

// Mode returns the transport mode of the server.
func (s *Server) Mode() TransportMode {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.mode
}

// IsRunning returns true if the server is running.
func (s *Server) IsRunning() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.running && !s.shutdown
}

// UpdateDependencies updates the server dependencies.
// This allows updating the store or config after server creation.
func (s *Server) UpdateDependencies(deps Dependencies) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if deps.Store != nil {
		s.store = deps.Store
	}
	if deps.Config != nil {
		s.config = deps.Config
	}
}

// RegisterSession registers a session with the MCP server.
// This enables session-scoped tools to route UI prompts to the correct session.
// The session must be registered before its tools can be used.
func (s *Server) RegisterSession(sessionID string, uiPrompter UIPrompter, logger *slog.Logger) error {
	s.sessionsMu.Lock()
	defer s.sessionsMu.Unlock()

	if _, exists := s.sessions[sessionID]; exists {
		return fmt.Errorf("session already registered: %s", sessionID)
	}

	s.sessions[sessionID] = &registeredSession{
		sessionID:  sessionID,
		uiPrompter: uiPrompter,
		logger:     logger,
	}

	s.logger.Info("Session registered with MCP server", "session_id", sessionID)
	return nil
}

// UnregisterSession removes a session from the MCP server.
// After unregistration, tools for this session will return "session not found" errors.
func (s *Server) UnregisterSession(sessionID string) {
	s.sessionsMu.Lock()
	if _, exists := s.sessions[sessionID]; !exists {
		s.sessionsMu.Unlock()
		return // Already unregistered
	}
	delete(s.sessions, sessionID)
	s.sessionsMu.Unlock()

	s.logger.Info("Session unregistered from MCP server", "session_id", sessionID)
}

// getSession returns the registered session for the given session ID.
// Returns nil if the session is not registered.
func (s *Server) getSession(sessionID string) *registeredSession {
	s.sessionsMu.RLock()
	defer s.sessionsMu.RUnlock()
	return s.sessions[sessionID]
}

// resolveSelfID resolves the provided session_id to a real session ID.
// It uses a two-phase lookup:
//  1. Direct lookup: If session_id matches a registered session, return it immediately.
//     This handles the case where the caller provides the actual session ID directly
//     (e.g., from mitto_conversation_get_current or external MCP clients like Auggie).
//  2. Correlation lookup: If not found directly, wait for the ACP layer to register
//     a correlation mapping (session_id -> real_session_id). This handles the case
//     where the caller provides a random identifier and the ACP layer intercepts
//     the tool call to register the mapping.
//
// Returns the resolved session ID, or empty string if resolution fails.
func (s *Server) resolveSelfID(inputSessionID string) string {
	if inputSessionID == "" {
		return ""
	}

	// Phase 1: Direct lookup - check if inputSessionID is already a registered session
	if reg := s.getSession(inputSessionID); reg != nil {
		s.logger.Debug("Session resolved via direct lookup",
			"input_session_id", inputSessionID,
			"resolved_session_id", inputSessionID)
		return inputSessionID
	}

	// Phase 2: Correlation lookup - wait for ACP layer to register the mapping
	// This is the original mechanism for agents that route through Mitto's ACP connection
	realSessionID := s.WaitForPendingRequest(inputSessionID)
	if realSessionID != "" {
		s.logger.Debug("Session resolved via correlation lookup",
			"input_session_id", inputSessionID,
			"resolved_session_id", realSessionID)
	}
	return realSessionID
}

// checkSessionFlag checks if a flag is enabled for the given session.
// Returns false if the session is not found or the flag is not enabled.
func (s *Server) checkSessionFlag(sessionID string, flagName string) bool {
	s.mu.RLock()
	store := s.store
	s.mu.RUnlock()

	if store == nil {
		return false
	}

	meta, err := store.GetMetadata(sessionID)
	if err != nil {
		return false
	}

	return session.GetFlagValue(meta.AdvancedSettings, flagName)
}

// RegisterPendingRequest registers a pending request for session correlation.
// This is called by the ACP/web layer when it sees a tool_call event for
// mitto_get_current_session. The MCP handler then uses WaitForPendingRequest
// to look up the session_id.
func (s *Server) RegisterPendingRequest(requestID, sessionID string) {
	if requestID == "" || sessionID == "" {
		return
	}

	s.pendingRequestsMu.Lock()
	defer s.pendingRequestsMu.Unlock()

	s.pendingRequests[requestID] = &pendingRequest{
		sessionID:    sessionID,
		registeredAt: time.Now(),
	}

	s.logger.Debug("Pending request registered",
		"request_id", requestID,
		"session_id", sessionID,
	)

	// Cleanup expired entries while we have the lock
	s.cleanupExpiredPendingRequestsLocked()
}

// WaitForPendingRequest waits for a pending request to be registered and returns the session ID.
// It polls until the request is found or the timeout expires.
// Returns empty string if the request is not found within the timeout.
func (s *Server) WaitForPendingRequest(requestID string) string {
	if requestID == "" {
		return ""
	}

	deadline := time.Now().Add(pendingRequestTimeout)

	for time.Now().Before(deadline) {
		s.pendingRequestsMu.RLock()
		req, exists := s.pendingRequests[requestID]
		s.pendingRequestsMu.RUnlock()

		if exists {
			s.logger.Debug("Pending request found",
				"request_id", requestID,
				"session_id", req.sessionID,
			)

			// Remove the pending request (one-time use)
			s.pendingRequestsMu.Lock()
			delete(s.pendingRequests, requestID)
			s.pendingRequestsMu.Unlock()

			return req.sessionID
		}

		time.Sleep(pendingRequestPollInterval)
	}

	s.logger.Warn("Pending request not found within timeout",
		"request_id", requestID,
		"timeout", pendingRequestTimeout,
	)
	return ""
}

// cleanupExpiredPendingRequestsLocked removes expired pending requests.
// Must be called with pendingRequestsMu held.
func (s *Server) cleanupExpiredPendingRequestsLocked() {
	now := time.Now()
	for reqID, req := range s.pendingRequests {
		if now.Sub(req.registeredAt) > pendingRequestExpiry {
			delete(s.pendingRequests, reqID)
			s.logger.Debug("Expired pending request removed", "request_id", reqID)
		}
	}
}

// permissionError returns a formatted error for tools that require a specific flag.
func permissionError(toolName, flagName, flagLabel string) error {
	return fmt.Errorf("tool '%s' requires the '%s' (%s) flag to be enabled in Advanced Settings", toolName, flagLabel, flagName)
}

// buildConversationDetails creates a ConversationDetails from session metadata and runtime info.
// This is the unified way to build conversation info for all conversation-related tools.
func (s *Server) buildConversationDetails(meta session.Metadata, sessionFolder string) ConversationDetails {
	details := ConversationDetails{
		SessionID:       meta.SessionID,
		Title:           meta.Name,
		Description:     meta.Description,
		ACPServer:       meta.ACPServer,
		WorkingDir:      meta.WorkingDir,
		MessageCount:    meta.EventCount,
		Status:          string(meta.Status),
		Archived:        meta.Archived,
		SessionFolder:   sessionFolder,
		ParentSessionID: meta.ParentSessionID,
	}

	// Format dates as ISO 8601 strings
	if !meta.CreatedAt.IsZero() {
		details.CreatedAt = meta.CreatedAt.Format("2006-01-02T15:04:05Z07:00")
	}
	if !meta.UpdatedAt.IsZero() {
		details.UpdatedAt = meta.UpdatedAt.Format("2006-01-02T15:04:05Z07:00")
	}
	if !meta.LastUserMessageAt.IsZero() {
		details.LastUserMessageAt = meta.LastUserMessageAt.Format("2006-01-02T15:04:05Z07:00")
	}

	// Add runtime status if available
	s.mu.RLock()
	store := s.store
	sm := s.sessionManager
	s.mu.RUnlock()

	// Check lock status
	if store != nil {
		if lockInfo, err := store.GetLockInfo(meta.SessionID); err == nil && lockInfo != nil {
			details.IsLocked = true
			details.LockStatus = string(lockInfo.Status)
			details.LockClientType = lockInfo.ClientType
			details.IsPrompting = lockInfo.Status == session.LockStatusProcessing
		}
	}

	// Get running session info if available (overrides lock-based IsPrompting)
	if sm != nil {
		if bs := sm.GetSession(meta.SessionID); bs != nil {
			details.IsRunning = true
			details.IsPrompting = bs.IsPrompting()
			details.LastSeq = bs.GetMaxAssignedSeq()
		}
	}

	return details
}

// registerGlobalTools registers global MCP tools (always available, no session context needed).
func (s *Server) registerGlobalTools(mcpSrv *mcp.Server, deps Dependencies) {
	// mitto_list_conversations tool - always available (no permission check)
	mcp.AddTool(mcpSrv, &mcp.Tool{
		Name: "mitto_conversation_list",
		Description: "List all existing Mitto conversations with metadata including title, dates, message count, prompting status, last sequence, and session folder. " +
			"Use this to find conversation IDs for other tools like 'mitto_conversation_get' or 'mitto_conversation_send_prompt'. " +
			"To CREATE a new conversation, use 'mitto_conversation_start' instead. Always available.",
	}, s.createListConversationsHandler(deps.SessionManager))

	// mitto_get_config tool - always available
	mcp.AddTool(mcpSrv, &mcp.Tool{
		Name:        "mitto_get_config",
		Description: "Get the current effective Mitto configuration",
	}, s.createGetConfigHandler())

	// mitto_get_runtime_info tool - always available
	mcp.AddTool(mcpSrv, &mcp.Tool{
		Name:        "mitto_get_runtime_info",
		Description: "Get runtime information including OS, architecture, log file paths, data directories, and process info",
	}, s.createGetRuntimeInfoHandler())
}

// selfIDNote is the standard note about self_id for tools that require session identification.
// For ACP-routed agents (like Auggie), the self_id is automatically correlated via the ACP layer,
// so any stable value works. For external MCP clients, the real session_id must be discovered first.
const selfIDNote = "The self_id parameter identifies YOUR current session (not the target conversation). " +
	"Call 'mitto_conversation_get_current' first to discover your real session_id, then use that value for all subsequent tool calls. " +
	"Your session_id is required to verify permissions for this tool."

// registerSessionScopedTools registers session-scoped MCP tools.
// These tools operate on specific conversations using automatic session detection via session_id correlation.
// Permission checks are done at execution time based on the session's flags.
func (s *Server) registerSessionScopedTools(mcpSrv *mcp.Server) {
	// mitto_get_current_session - Get info about the current session (auto-detected via session_id)
	mcp.AddTool(mcpSrv, &mcp.Tool{
		Name: "mitto_conversation_get_current",
		Description: "Get information about YOUR current conversation/session, including your real session ID, title, working directory, and message count. " +
			"CALL THIS FIRST to discover your session_id before using other Mitto tools that require permissions. " +
			"You can pass any value for self_id (e.g., 'init') - this tool auto-detects your session and returns the real session_id. " +
			// Note: selfIDNote is appended here for consistency, but for get_current specifically,
			// any self_id value works since the tool auto-detects the session via ACP correlation.
			selfIDNote,
	}, s.handleGetCurrentSession)

	// mitto_conversation_send_prompt - Send a prompt to another conversation
	mcp.AddTool(mcpSrv, &mcp.Tool{
		Name: "mitto_conversation_send_prompt",
		Description: "Send a message/prompt to an EXISTING conversation (identified by conversation_id). " +
			"The prompt is added to that conversation's queue and will be processed when the target agent becomes idle. " +
			"Use 'mitto_conversation_list' first to find existing conversation IDs, or use an ID returned by 'mitto_conversation_start'. " +
			"Requires 'Can Send Prompt' flag to be enabled. " +
			selfIDNote,
	}, s.handleSendPromptToConversation)

	// mitto_ui_ask_yes_no - Present a yes/no question
	mcp.AddTool(mcpSrv, &mcp.Tool{
		Name: "mitto_ui_ask_yes_no",
		Description: "Present a yes/no question to the user and wait for their response. " +
			"The tool blocks until the user clicks a button or the timeout expires. " +
			"Requires 'Can prompt user' flag to be enabled. " +
			selfIDNote,
	}, s.handleAskYesNo)

	// mitto_ui_options_buttons - Present multiple option buttons
	mcp.AddTool(mcpSrv, &mcp.Tool{
		Name: "mitto_ui_options_buttons",
		Description: "Present multiple options as buttons to the user and wait for their selection. " +
			"The tool blocks until the user clicks a button or the timeout expires. " +
			"Requires 'Can prompt user' flag to be enabled. " +
			selfIDNote,
	}, s.handleOptionsButtons)

	// mitto_ui_options_combo - Present a combo box
	mcp.AddTool(mcpSrv, &mcp.Tool{
		Name: "mitto_ui_options_combo",
		Description: "Present a dropdown/combo box with options to the user. " +
			"The user selects an option and clicks OK to confirm. " +
			"Requires 'Can prompt user' flag to be enabled. " +
			selfIDNote,
	}, s.handleOptionsCombo)

	// mitto_conversation_start - Start a new conversation
	mcp.AddTool(mcpSrv, &mcp.Tool{
		Name: "mitto_conversation_start",
		Description: "USE THIS TOOL TO CREATE A NEW CONVERSATION - no browser or UI interaction needed! " +
			"This tool programmatically creates and starts a NEW agent conversation that runs in parallel with your current session. " +
			"When a user asks you to 'create a new conversation', 'start a new session', or 'investigate something in a new conversation', " +
			"call this tool directly instead of trying to click buttons or navigate a UI. " +
			"This spawns a separate AI agent that can work independently on the task you specify. " +
			"Use this to delegate work, run background tasks, or parallelize complex work across multiple agents. " +
			"The new conversation inherits your workspace and ACP server configuration. " +
			"Optionally provide a 'title' for the conversation and an 'initial_prompt' to start the agent working immediately. " +
			"Requires 'Can start conversation' flag to be enabled in Advanced Settings (disabled by default for security). " +
			"Note: Conversations created by this tool cannot spawn further conversations (to prevent infinite recursion). " +
			selfIDNote,
	}, s.handleConversationStart)

	// mitto_conversation_get_summary - Get a summary of a conversation
	mcp.AddTool(mcpSrv, &mcp.Tool{
		Name: "mitto_conversation_get_summary",
		Description: "Generate a summary of a specific conversation (by conversation_id) using AI analysis. " +
			"The summary includes main topics discussed, key decisions, actions taken, and pending items. " +
			"Use 'mitto_conversation_list' first to find available conversation IDs. " +
			selfIDNote,
	}, s.handleGetConversationSummary)

	// mitto_conversation_get - Get properties of a specific conversation
	mcp.AddTool(mcpSrv, &mcp.Tool{
		Name: "mitto_conversation_get",
		Description: "Get detailed properties of a specific conversation by conversation_id. " +
			"Returns metadata, status, and runtime info including whether the agent is currently replying. " +
			"Use 'mitto_conversation_list' first to find available conversation IDs. " +
			selfIDNote,
	}, s.handleGetConversation)

	// mitto_conversation_set_periodic - Configure periodic prompts for a conversation
	mcp.AddTool(mcpSrv, &mcp.Tool{
		Name: "mitto_conversation_set_periodic",
		Description: "Configure a conversation to run periodically with a scheduled prompt. " +
			"This makes the conversation automatically receive the specified prompt at regular intervals. " +
			"Useful for setting up recurring tasks like daily reports, periodic checks, or scheduled automation. " +
			"Frequency can be specified in minutes, hours, or days. For days, you can optionally specify a time (HH:MM in UTC). " +
			"Examples: every 30 minutes, every 2 hours, every day at 09:00 UTC. " +
			"Set enabled=false to pause periodic execution without deleting the configuration. " +
			"Use 'mitto_conversation_list' first to find available conversation IDs. " +
			selfIDNote,
	}, s.handleSetPeriodic)

	// mitto_conversation_archive - Archive or unarchive a conversation
	mcp.AddTool(mcpSrv, &mcp.Tool{
		Name: "mitto_conversation_archive",
		Description: "Archive or unarchive a conversation. " +
			"Archiving a conversation gracefully stops any active agent response, closes the ACP connection, " +
			"and marks the conversation as archived. Archived conversations are read-only but can be unarchived later. " +
			"Set archived=false to unarchive a conversation and resume the ACP connection. " +
			"Use 'mitto_conversation_list' first to find available conversation IDs. " +
			selfIDNote,
	}, s.handleArchiveConversation)
}

// ListConversationsOutput wraps the list of conversations for MCP output schema compliance.
type ListConversationsOutput struct {
	Conversations []ConversationInfo `json:"conversations"`
}

// createListConversationsHandler creates the handler for list_conversations tool.
func (s *Server) createListConversationsHandler(sm SessionManager) mcp.ToolHandlerFor[struct{}, ListConversationsOutput] {
	return func(ctx context.Context, req *mcp.CallToolRequest, input struct{}) (*mcp.CallToolResult, ListConversationsOutput, error) {
		s.mu.RLock()
		store := s.store
		s.mu.RUnlock()

		if store == nil {
			return nil, ListConversationsOutput{}, fmt.Errorf("session store not available")
		}

		sessions, err := store.List()
		if err != nil {
			return nil, ListConversationsOutput{}, fmt.Errorf("failed to list sessions: %w", err)
		}

		conversations := make([]ConversationInfo, 0, len(sessions))
		for _, meta := range sessions {
			info := ConversationInfo{
				SessionID:         meta.SessionID,
				Title:             meta.Name,
				Description:       meta.Description,
				ACPServer:         meta.ACPServer,
				WorkingDir:        meta.WorkingDir,
				CreatedAt:         meta.CreatedAt,
				UpdatedAt:         meta.UpdatedAt,
				LastUserMessageAt: meta.LastUserMessageAt,
				MessageCount:      meta.EventCount,
				Status:            string(meta.Status),
				Archived:          meta.Archived,
				SessionFolder:     store.SessionDir(meta.SessionID),
			}

			// Check lock status
			lockInfo, err := store.GetLockInfo(meta.SessionID)
			if err == nil && lockInfo != nil {
				info.IsLocked = true
				info.LockStatus = string(lockInfo.Status)
				info.LockClientType = lockInfo.ClientType
				info.IsPrompting = lockInfo.Status == session.LockStatusProcessing
			}

			// Get running session info if available
			if sm != nil {
				if bs := sm.GetSession(meta.SessionID); bs != nil {
					info.IsRunning = true
					info.IsPrompting = bs.IsPrompting()
					info.LastSeq = bs.GetMaxAssignedSeq()
				}
			}

			conversations = append(conversations, info)
		}

		return nil, ListConversationsOutput{Conversations: conversations}, nil
	}
}

// createGetConfigHandler creates the handler for get_config tool.
func (s *Server) createGetConfigHandler() mcp.ToolHandlerFor[struct{}, ConfigInfo] {
	return func(ctx context.Context, req *mcp.CallToolRequest, input struct{}) (*mcp.CallToolResult, ConfigInfo, error) {
		s.mu.RLock()
		cfg := s.config
		s.mu.RUnlock()

		if cfg == nil {
			return nil, ConfigInfo{}, fmt.Errorf("configuration not available")
		}

		info := ConfigInfo{}

		// Marshal config to JSON for safe output
		data, err := json.Marshal(configToSafeOutput(cfg))
		if err != nil {
			return nil, ConfigInfo{}, fmt.Errorf("failed to marshal config: %w", err)
		}
		if err := json.Unmarshal(data, &info); err != nil {
			return nil, ConfigInfo{}, fmt.Errorf("failed to process config: %w", err)
		}

		return nil, info, nil
	}
}

// createGetRuntimeInfoHandler creates the handler for get_runtime_info tool.
func (s *Server) createGetRuntimeInfoHandler() mcp.ToolHandlerFor[struct{}, RuntimeInfo] {
	return func(ctx context.Context, req *mcp.CallToolRequest, input struct{}) (*mcp.CallToolResult, RuntimeInfo, error) {
		info := buildRuntimeInfo()
		return nil, *info, nil
	}
}

// =============================================================================
// Session-Scoped Tool Handlers
// These tools require a session_id parameter and operate on specific conversations.
// =============================================================================

// GetCurrentSessionInput is the input for mitto_get_current_session tool.
type GetCurrentSessionInput struct {
	// SelfID identifies YOUR current session (the caller), not a target conversation.
	// If the actual session ID is unknown, generate a random identifier (e.g., UUID, 'agent-task-1').
	// Reuse the same self_id for all calls within the same conversation.
	SelfID string `json:"self_id"`
}

// handleGetCurrentSession handles the mitto_get_current_session tool.
// The session is automatically detected using session_id correlation.
// The ACP layer registers the session_id -> real_session_id mapping when it sees the tool_call,
// and this handler waits for that mapping to become available.
func (s *Server) handleGetCurrentSession(ctx context.Context, req *mcp.CallToolRequest, input GetCurrentSessionInput) (*mcp.CallToolResult, CurrentSessionOutput, error) {
	s.logger.Debug("get_current_session called",
		"session_id", input.SelfID,
	)

	// Validate self_id
	if input.SelfID == "" {
		return nil, CurrentSessionOutput{}, fmt.Errorf(
			"self_id is required: please provide the session ID or a unique random identifier for this session",
		)
	}

	// Resolve the self_id to a real session ID using two-phase lookup:
	// 1. Direct lookup if session_id is already a registered session
	// 2. Correlation lookup via pending request registration (for ACP-routed calls)
	realSessionID := s.resolveSelfID(input.SelfID)
	if realSessionID == "" {
		return nil, CurrentSessionOutput{}, fmt.Errorf(
			"session not found: the self_id '%s' could not be resolved. "+
				"Provide the actual session ID from mitto_conversation_list, or ensure this tool is called from within a Mitto session",
			input.SelfID,
		)
	}

	// Check if session is registered (running)
	reg := s.getSession(realSessionID)
	if reg == nil {
		return nil, CurrentSessionOutput{}, fmt.Errorf("session not found or not running: %s", realSessionID)
	}

	s.mu.RLock()
	store := s.store
	s.mu.RUnlock()

	if store == nil {
		return nil, CurrentSessionOutput{}, fmt.Errorf("session store not available")
	}

	meta, err := store.GetMetadata(realSessionID)
	if err != nil {
		return nil, CurrentSessionOutput{}, fmt.Errorf("failed to get session: %w", err)
	}

	// Build unified conversation details
	output := s.buildConversationDetails(meta, store.SessionDir(meta.SessionID))

	return nil, output, nil
}

// SendPromptToConversationInput is the input for send_prompt_to_conversation tool.
type SendPromptToConversationInput struct {
	SelfID         string `json:"self_id"`         // YOUR session ID (the caller), not the target
	ConversationID string `json:"conversation_id"` // Target conversation ID to send prompt to
	Prompt         string `json:"prompt"`
}

func (s *Server) handleSendPromptToConversation(ctx context.Context, req *mcp.CallToolRequest, input SendPromptToConversationInput) (*mcp.CallToolResult, SendPromptOutput, error) {
	// Validate self_id
	if input.SelfID == "" {
		return nil, SendPromptOutput{Success: false, Error: "self_id is required"}, nil
	}

	// Resolve the self_id to a real session ID
	realSessionID := s.resolveSelfID(input.SelfID)
	if realSessionID == "" {
		return nil, SendPromptOutput{
			Success: false,
			Error: fmt.Sprintf("session not found: the self_id '%s' could not be resolved",
				input.SelfID),
		}, nil
	}

	// Check if source session is registered
	reg := s.getSession(realSessionID)
	if reg == nil {
		return nil, SendPromptOutput{Success: false, Error: fmt.Sprintf("session not found or not running: %s", realSessionID)}, nil
	}

	// Permission check: requires can_send_prompt on the SOURCE session
	if !s.checkSessionFlag(realSessionID, session.FlagCanSendPrompt) {
		return nil, SendPromptOutput{
			Success: false,
			Error:   fmt.Sprintf("tool 'mitto_send_prompt_to_conversation' requires the 'Can Send Prompt' (%s) flag to be enabled in Advanced Settings", session.FlagCanSendPrompt),
		}, nil
	}

	s.mu.RLock()
	store := s.store
	s.mu.RUnlock()

	if store == nil {
		return nil, SendPromptOutput{Success: false, Error: "session store not available"}, nil
	}

	// Validate input
	if input.ConversationID == "" {
		return nil, SendPromptOutput{Success: false, Error: "conversation_id is required"}, nil
	}
	if input.Prompt == "" {
		return nil, SendPromptOutput{Success: false, Error: "prompt is required"}, nil
	}

	// Check if target conversation exists
	_, err := store.GetMetadata(input.ConversationID)
	if err != nil {
		return nil, SendPromptOutput{
			Success: false,
			Error:   fmt.Sprintf("conversation not found: %s", input.ConversationID),
		}, nil
	}

	// Get the queue for the target conversation
	queue := store.Queue(input.ConversationID)

	// Add the prompt to the queue
	msg, err := queue.Add(input.Prompt, nil, nil, realSessionID, 0)
	if err != nil {
		return nil, SendPromptOutput{
			Success: false,
			Error:   fmt.Sprintf("failed to add prompt to queue: %v", err),
		}, nil
	}

	// Get queue length for position info
	queueLen, _ := queue.Len()

	s.logger.Info("Prompt sent to conversation queue",
		"source_session", realSessionID,
		"target_session", input.ConversationID,
		"message_id", msg.ID,
		"queue_position", queueLen)

	return nil, SendPromptOutput{
		Success:       true,
		MessageID:     msg.ID,
		QueuePosition: queueLen,
	}, nil
}

// AskYesNoInput is the input for the mitto_ui_ask_yes_no tool.
type AskYesNoInput struct {
	SelfID         string `json:"self_id"` // YOUR session ID (the caller)
	Question       string `json:"question"`
	YesLabel       string `json:"yes_label,omitempty"`
	NoLabel        string `json:"no_label,omitempty"`
	TimeoutSeconds int    `json:"timeout_seconds,omitempty"`
}

func (s *Server) handleAskYesNo(ctx context.Context, req *mcp.CallToolRequest, input AskYesNoInput) (*mcp.CallToolResult, AskYesNoOutput, error) {
	// Validate self_id
	if input.SelfID == "" {
		return nil, AskYesNoOutput{}, fmt.Errorf("self_id is required")
	}

	// Resolve the self_id to a real session ID
	realSessionID := s.resolveSelfID(input.SelfID)
	if realSessionID == "" {
		return nil, AskYesNoOutput{}, fmt.Errorf(
			"session not found: the self_id '%s' could not be resolved",
			input.SelfID,
		)
	}

	// Check if session is registered and get the UIPrompter
	reg := s.getSession(realSessionID)
	if reg == nil {
		return nil, AskYesNoOutput{}, fmt.Errorf("session not found or not running: %s", realSessionID)
	}

	// Permission check
	if !s.checkSessionFlag(realSessionID, session.FlagCanPromptUser) {
		return nil, AskYesNoOutput{}, permissionError("mitto_ui_ask_yes_no", session.FlagCanPromptUser, "Can prompt user")
	}

	// Check if UIPrompter is available
	if reg.uiPrompter == nil {
		return nil, AskYesNoOutput{}, fmt.Errorf("UI prompts are not available (no UI connected)")
	}

	// Apply defaults
	yesLabel := input.YesLabel
	if yesLabel == "" {
		yesLabel = "Yes"
	}
	noLabel := input.NoLabel
	if noLabel == "" {
		noLabel = "No"
	}
	timeout := input.TimeoutSeconds
	if timeout <= 0 {
		timeout = 300 // 5 minutes default
	}

	// Generate unique internal request ID for UI prompt
	uiRequestID := fmt.Sprintf("%s-%s", realSessionID[:8], uuid.New().String()[:8])

	// Build the prompt request
	promptReq := UIPromptRequest{
		RequestID: uiRequestID,
		Type:      UIPromptTypeYesNo,
		Question:  input.Question,
		Options: []UIPromptOption{
			{ID: "yes", Label: yesLabel},
			{ID: "no", Label: noLabel},
		},
		TimeoutSeconds: timeout,
	}

	s.logger.Debug("Sending UI yes/no prompt",
		"session_id", realSessionID,
		"input_session_id", input.SelfID,
		"ui_request_id", uiRequestID,
		"question", input.Question,
		"timeout", timeout)

	// Send prompt and wait for response
	resp, err := reg.uiPrompter.UIPrompt(ctx, promptReq)
	if err != nil {
		return nil, AskYesNoOutput{}, fmt.Errorf("failed to display UI prompt: %w", err)
	}

	if resp.TimedOut {
		s.logger.Debug("UI yes/no prompt timed out", "session_id", realSessionID)
		return nil, AskYesNoOutput{Response: "timeout"}, nil
	}

	s.logger.Debug("UI yes/no prompt answered",
		"session_id", realSessionID,
		"response", resp.OptionID)

	return nil, AskYesNoOutput{
		Response: resp.OptionID,
		Label:    resp.Label,
	}, nil
}

// OptionsButtonsInput is the input for the mitto_ui_options_buttons tool.
type OptionsButtonsInput struct {
	SelfID         string   `json:"self_id"` // YOUR session ID (the caller)
	Options        []string `json:"options"`
	Question       string   `json:"question,omitempty"`
	TimeoutSeconds int      `json:"timeout_seconds,omitempty"`
}

func (s *Server) handleOptionsButtons(ctx context.Context, req *mcp.CallToolRequest, input OptionsButtonsInput) (*mcp.CallToolResult, OptionsButtonsOutput, error) {
	// Validate self_id
	if input.SelfID == "" {
		return nil, OptionsButtonsOutput{Index: -1}, fmt.Errorf("self_id is required")
	}

	// Resolve the self_id to a real session ID
	realSessionID := s.resolveSelfID(input.SelfID)
	if realSessionID == "" {
		return nil, OptionsButtonsOutput{Index: -1}, fmt.Errorf(
			"session not found: the self_id '%s' could not be resolved",
			input.SelfID,
		)
	}

	// Check if session is registered and get the UIPrompter
	reg := s.getSession(realSessionID)
	if reg == nil {
		return nil, OptionsButtonsOutput{Index: -1}, fmt.Errorf("session not found or not running: %s", realSessionID)
	}

	// Permission check
	if !s.checkSessionFlag(realSessionID, session.FlagCanPromptUser) {
		return nil, OptionsButtonsOutput{Index: -1}, permissionError("mitto_ui_options_buttons", session.FlagCanPromptUser, "Can prompt user")
	}

	// Check if UIPrompter is available
	if reg.uiPrompter == nil {
		return nil, OptionsButtonsOutput{Index: -1}, fmt.Errorf("UI prompts are not available (no UI connected)")
	}

	// Validate input
	if len(input.Options) == 0 {
		return nil, OptionsButtonsOutput{Index: -1}, fmt.Errorf("at least one option is required")
	}
	if len(input.Options) > 4 {
		return nil, OptionsButtonsOutput{Index: -1}, fmt.Errorf("options_buttons supports at most 4 options (got %d); use options_combo for more options", len(input.Options))
	}

	// Apply defaults
	timeout := input.TimeoutSeconds
	if timeout <= 0 {
		timeout = 300
	}

	question := input.Question
	if question == "" {
		question = "Please select an option:"
	}

	// Generate unique internal request ID for UI prompt
	uiRequestID := fmt.Sprintf("%s-%s", realSessionID[:8], uuid.New().String()[:8])

	// Build options with IDs
	options := make([]UIPromptOption, len(input.Options))
	for i, label := range input.Options {
		options[i] = UIPromptOption{
			ID:    fmt.Sprintf("%d", i),
			Label: label,
		}
	}

	promptReq := UIPromptRequest{
		RequestID:      uiRequestID,
		Type:           UIPromptTypeOptions,
		Question:       question,
		Options:        options,
		TimeoutSeconds: timeout,
	}

	s.logger.Debug("Sending UI options buttons prompt",
		"session_id", realSessionID,
		"input_session_id", input.SelfID,
		"ui_request_id", uiRequestID,
		"option_count", len(input.Options),
		"timeout", timeout)

	resp, err := reg.uiPrompter.UIPrompt(ctx, promptReq)
	if err != nil {
		return nil, OptionsButtonsOutput{Index: -1}, fmt.Errorf("failed to display UI prompt: %w", err)
	}

	if resp.TimedOut {
		s.logger.Debug("UI options buttons prompt timed out", "session_id", realSessionID)
		return nil, OptionsButtonsOutput{Index: -1, TimedOut: true}, nil
	}

	var selectedIndex int
	if _, err := fmt.Sscanf(resp.OptionID, "%d", &selectedIndex); err != nil {
		selectedIndex = -1
	}

	s.logger.Debug("UI options buttons prompt answered",
		"session_id", realSessionID,
		"selected", resp.Label,
		"index", selectedIndex)

	return nil, OptionsButtonsOutput{
		Selected: resp.Label,
		Index:    selectedIndex,
	}, nil
}

// OptionsComboInput is the input for the mitto_ui_options_combo tool.
type OptionsComboInput struct {
	SelfID         string   `json:"self_id"` // YOUR session ID (the caller)
	Options        []string `json:"options"`
	Question       string   `json:"question,omitempty"`
	TimeoutSeconds int      `json:"timeout_seconds,omitempty"`
}

func (s *Server) handleOptionsCombo(ctx context.Context, req *mcp.CallToolRequest, input OptionsComboInput) (*mcp.CallToolResult, OptionsComboOutput, error) {
	// Validate self_id
	if input.SelfID == "" {
		return nil, OptionsComboOutput{Index: -1}, fmt.Errorf("self_id is required")
	}

	// Resolve the self_id to a real session ID
	realSessionID := s.resolveSelfID(input.SelfID)
	if realSessionID == "" {
		return nil, OptionsComboOutput{Index: -1}, fmt.Errorf(
			"session not found: the self_id '%s' could not be resolved",
			input.SelfID,
		)
	}

	// Check if session is registered and get the UIPrompter
	reg := s.getSession(realSessionID)
	if reg == nil {
		return nil, OptionsComboOutput{Index: -1}, fmt.Errorf("session not found or not running: %s", realSessionID)
	}

	// Permission check
	if !s.checkSessionFlag(realSessionID, session.FlagCanPromptUser) {
		return nil, OptionsComboOutput{Index: -1}, permissionError("mitto_ui_options_combo", session.FlagCanPromptUser, "Can prompt user")
	}

	// Check if UIPrompter is available
	if reg.uiPrompter == nil {
		return nil, OptionsComboOutput{Index: -1}, fmt.Errorf("UI prompts are not available (no UI connected)")
	}

	// Validate input
	if len(input.Options) == 0 {
		return nil, OptionsComboOutput{Index: -1}, fmt.Errorf("at least one option is required")
	}
	if len(input.Options) > 10 {
		return nil, OptionsComboOutput{Index: -1}, fmt.Errorf("options_combo supports at most 10 options (got %d)", len(input.Options))
	}

	// Apply defaults
	timeout := input.TimeoutSeconds
	if timeout <= 0 {
		timeout = 300
	}

	question := input.Question
	if question == "" {
		question = "Please select an option:"
	}

	// Generate unique internal request ID for UI prompt
	uiRequestID := fmt.Sprintf("%s-%s", realSessionID[:8], uuid.New().String()[:8])

	// Build options with IDs
	options := make([]UIPromptOption, len(input.Options))
	for i, label := range input.Options {
		options[i] = UIPromptOption{
			ID:    fmt.Sprintf("%d", i),
			Label: label,
		}
	}

	promptReq := UIPromptRequest{
		RequestID:      uiRequestID,
		Type:           UIPromptTypeSelect,
		Question:       question,
		Options:        options,
		TimeoutSeconds: timeout,
	}

	s.logger.Debug("Sending UI options combo prompt",
		"session_id", realSessionID,
		"input_session_id", input.SelfID,
		"ui_request_id", uiRequestID,
		"option_count", len(input.Options),
		"timeout", timeout)

	resp, err := reg.uiPrompter.UIPrompt(ctx, promptReq)
	if err != nil {
		return nil, OptionsComboOutput{Index: -1}, fmt.Errorf("failed to display UI prompt: %w", err)
	}

	if resp.TimedOut {
		s.logger.Debug("UI options combo prompt timed out", "session_id", realSessionID)
		return nil, OptionsComboOutput{Index: -1, TimedOut: true}, nil
	}

	var selectedIndex int
	if _, err := fmt.Sscanf(resp.OptionID, "%d", &selectedIndex); err != nil {
		selectedIndex = -1
	}

	s.logger.Debug("UI options combo prompt answered",
		"session_id", realSessionID,
		"selected", resp.Label,
		"index", selectedIndex)

	return nil, OptionsComboOutput{
		Selected: resp.Label,
		Index:    selectedIndex,
	}, nil
}

// ConversationStartInput is the input for mitto_conversation_start tool.
type ConversationStartInput struct {
	SelfID        string `json:"self_id"`                  // YOUR session ID (the caller)
	Title         string `json:"title,omitempty"`          // Optional title for the new conversation
	InitialPrompt string `json:"initial_prompt,omitempty"` // Optional initial message to queue
}

// ConversationStartOutput is the output for mitto_conversation_start tool.
// Embeds ConversationDetails for the newly created conversation.
type ConversationStartOutput struct {
	ConversationDetails        // Embedded conversation details
	QueuePosition       int    `json:"queue_position,omitempty"` // Queue position if initial prompt was provided
	Error               string `json:"error,omitempty"`
}

func (s *Server) handleConversationStart(ctx context.Context, req *mcp.CallToolRequest, input ConversationStartInput) (*mcp.CallToolResult, ConversationStartOutput, error) {
	// Validate self_id
	if input.SelfID == "" {
		return nil, ConversationStartOutput{}, fmt.Errorf("self_id is required")
	}

	// Resolve the self_id to a real session ID
	realSessionID := s.resolveSelfID(input.SelfID)
	if realSessionID == "" {
		return nil, ConversationStartOutput{}, fmt.Errorf(
			"session not found: the self_id '%s' could not be resolved",
			input.SelfID)
	}

	// Check if source session is registered
	reg := s.getSession(realSessionID)
	if reg == nil {
		return nil, ConversationStartOutput{}, fmt.Errorf("session not found or not running: %s", realSessionID)
	}

	s.mu.RLock()
	store := s.store
	s.mu.RUnlock()

	if store == nil {
		return nil, ConversationStartOutput{}, fmt.Errorf("session store not available")
	}

	// Get the source session's metadata
	sourceMeta, err := store.GetMetadata(realSessionID)
	if err != nil {
		return nil, ConversationStartOutput{}, fmt.Errorf("failed to get source session metadata: %v", err)
	}

	// Permission check: requires can_start_conversation flag
	// Note: This is checked first since it's the most common reason for failure
	// (flag defaults to false for security reasons)
	if !s.checkSessionFlag(realSessionID, session.FlagCanStartConversation) {
		return nil, ConversationStartOutput{}, fmt.Errorf(
			"the '%s' flag is not enabled for this session. Enable it in this session's Advanced Settings (gear icon) to allow creating new conversations",
			session.FlagCanStartConversation)
	}

	// Check if the source session has a parent - if so, it cannot create new sessions
	// This prevents infinite recursion where child sessions keep spawning more children
	if sourceMeta.ParentSessionID != "" {
		return nil, ConversationStartOutput{}, fmt.Errorf(
			"this session was created by another session (parent: %s) and cannot create new conversations to prevent infinite recursion",
			sourceMeta.ParentSessionID)
	}

	// Create new session ID using the standard timestamp format
	// This ensures compatibility with IsValidSessionID validation in the web layer
	newSessionID := session.GenerateSessionID()

	// Create the new session metadata
	// NOTE: Recursion is prevented by setting can_start_conversation=false for child sessions.
	// The parent check above (ParentSessionID != "") also blocks child sessions from creating new ones.
	// TODO: Consider adding a max recursion depth counter in metadata as a defensive measure,
	// though the current prevention logic should be sufficient.
	newMeta := session.Metadata{
		SessionID:       newSessionID,
		Name:            input.Title,
		ACPServer:       sourceMeta.ACPServer,
		WorkingDir:      sourceMeta.WorkingDir,
		ParentSessionID: realSessionID, // Mark this session as a child
		// Child sessions explicitly have can_start_conversation disabled to prevent infinite recursion
		AdvancedSettings: map[string]bool{
			session.FlagCanStartConversation: false,
		},
	}

	// Create the session
	if err := store.Create(newMeta); err != nil {
		return nil, ConversationStartOutput{}, fmt.Errorf("failed to create session: %v", err)
	}

	s.logger.Info("New conversation created via MCP",
		"new_session_id", newSessionID,
		"parent_session_id", realSessionID,
		"working_dir", sourceMeta.WorkingDir,
		"title", input.Title)

	// Re-fetch metadata to get timestamps set by Create()
	createdMeta, err := store.GetMetadata(newSessionID)
	if err != nil {
		// Use the newMeta we have if re-fetch fails
		createdMeta = newMeta
	}

	// Build unified conversation details
	output := ConversationStartOutput{
		ConversationDetails: s.buildConversationDetails(createdMeta, store.SessionDir(newSessionID)),
	}

	// If initial prompt provided, add it to the queue
	if input.InitialPrompt != "" {
		queue := store.Queue(newSessionID)
		_, err := queue.Add(input.InitialPrompt, nil, nil, realSessionID, 0)
		if err != nil {
			s.logger.Warn("Failed to queue initial prompt",
				"session_id", newSessionID,
				"error", err)
		} else {
			queueLen, _ := queue.Len()
			output.QueuePosition = queueLen
		}
	}

	return nil, output, nil
}

// GetConversationSummaryInput is the input for mitto_get_conversation_summary tool.
type GetConversationSummaryInput struct {
	SelfID         string `json:"self_id"`         // YOUR session ID (the caller)
	ConversationID string `json:"conversation_id"` // Target conversation ID to summarize
}

// GetConversationSummaryOutput is the output for mitto_get_conversation_summary tool.
type GetConversationSummaryOutput struct {
	Success      bool   `json:"success"`
	Summary      string `json:"summary,omitempty"`
	MessageCount int    `json:"message_count,omitempty"` // Number of messages analyzed
	Error        string `json:"error,omitempty"`
}

func (s *Server) handleGetConversationSummary(ctx context.Context, req *mcp.CallToolRequest, input GetConversationSummaryInput) (*mcp.CallToolResult, GetConversationSummaryOutput, error) {
	// Validate self_id
	if input.SelfID == "" {
		return nil, GetConversationSummaryOutput{Success: false, Error: "self_id is required"}, nil
	}

	// Validate conversation_id
	if input.ConversationID == "" {
		return nil, GetConversationSummaryOutput{Success: false, Error: "conversation_id is required"}, nil
	}

	// Resolve the self_id to a real session ID
	realSessionID := s.resolveSelfID(input.SelfID)
	if realSessionID == "" {
		return nil, GetConversationSummaryOutput{
			Success: false,
			Error: fmt.Sprintf("session not found: the self_id '%s' could not be resolved",
				input.SelfID),
		}, nil
	}

	// Check if source session is registered
	reg := s.getSession(realSessionID)
	if reg == nil {
		return nil, GetConversationSummaryOutput{Success: false, Error: fmt.Sprintf("session not found or not running: %s", realSessionID)}, nil
	}

	s.mu.RLock()
	store := s.store
	s.mu.RUnlock()

	if store == nil {
		return nil, GetConversationSummaryOutput{Success: false, Error: "session store not available"}, nil
	}

	// Check if the target conversation exists
	_, err := store.GetMetadata(input.ConversationID)
	if err != nil {
		return nil, GetConversationSummaryOutput{
			Success: false,
			Error:   fmt.Sprintf("conversation not found: %s", input.ConversationID),
		}, nil
	}

	// Read events from the conversation
	events, err := store.ReadEvents(input.ConversationID)
	if err != nil {
		return nil, GetConversationSummaryOutput{
			Success: false,
			Error:   fmt.Sprintf("failed to read conversation events: %v", err),
		}, nil
	}

	// Format conversation content for the summary
	conversationContent := formatConversationForSummary(events)

	// Count meaningful messages (user prompts + agent messages)
	messageCount := 0
	for _, e := range events {
		if e.Type == session.EventTypeUserPrompt || e.Type == session.EventTypeAgentMessage {
			messageCount++
		}
	}

	if messageCount == 0 {
		return nil, GetConversationSummaryOutput{
			Success:      true,
			Summary:      "This conversation has no messages yet.",
			MessageCount: 0,
		}, nil
	}

	// Generate summary using auxiliary session
	summary, err := auxiliary.GenerateConversationSummary(ctx, conversationContent)
	if err != nil {
		return nil, GetConversationSummaryOutput{
			Success: false,
			Error:   fmt.Sprintf("failed to generate summary: %v", err),
		}, nil
	}

	s.logger.Info("Generated conversation summary",
		"source_session", realSessionID,
		"target_conversation", input.ConversationID,
		"message_count", messageCount,
		"summary_length", len(summary))

	return nil, GetConversationSummaryOutput{
		Success:      true,
		Summary:      summary,
		MessageCount: messageCount,
	}, nil
}

// formatConversationForSummary formats conversation events into a readable format for summarization.
func formatConversationForSummary(events []session.Event) string {
	var sb strings.Builder

	for _, e := range events {
		switch e.Type {
		case session.EventTypeUserPrompt:
			if data, ok := e.Data.(map[string]interface{}); ok {
				if msg, ok := data["message"].(string); ok {
					sb.WriteString("USER: ")
					sb.WriteString(msg)
					sb.WriteString("\n\n")
				}
			}
		case session.EventTypeAgentMessage:
			if data, ok := e.Data.(map[string]interface{}); ok {
				// The field is "html" in JSON but contains the agent's message
				if html, ok := data["html"].(string); ok {
					sb.WriteString("ASSISTANT: ")
					sb.WriteString(html)
					sb.WriteString("\n\n")
				}
			}
		case session.EventTypeToolCall:
			if data, ok := e.Data.(map[string]interface{}); ok {
				if name, ok := data["name"].(string); ok {
					sb.WriteString("[Tool call: ")
					sb.WriteString(name)
					sb.WriteString("]\n\n")
				}
			}
		}
	}

	return sb.String()
}

// GetConversationInput is the input for mitto_get_conversation tool.
type GetConversationInput struct {
	SelfID         string `json:"self_id"`         // YOUR session ID (the caller)
	ConversationID string `json:"conversation_id"` // Target conversation ID to get properties for
}

// GetConversationOutput is the output for mitto_get_conversation tool.
// It returns the same ConversationDetails as other conversation tools.
type GetConversationOutput = ConversationDetails

func (s *Server) handleGetConversation(ctx context.Context, req *mcp.CallToolRequest, input GetConversationInput) (*mcp.CallToolResult, GetConversationOutput, error) {
	// Validate self_id
	if input.SelfID == "" {
		return nil, GetConversationOutput{}, fmt.Errorf("self_id is required")
	}

	// Validate conversation_id
	if input.ConversationID == "" {
		return nil, GetConversationOutput{}, fmt.Errorf("conversation_id is required")
	}

	// Resolve the self_id to a real session ID
	realSessionID := s.resolveSelfID(input.SelfID)
	if realSessionID == "" {
		return nil, GetConversationOutput{}, fmt.Errorf(
			"session not found: the self_id '%s' could not be resolved",
			input.SelfID)
	}

	// Check if source session is registered (must be running to use this tool)
	reg := s.getSession(realSessionID)
	if reg == nil {
		return nil, GetConversationOutput{}, fmt.Errorf("session not found or not running: %s", realSessionID)
	}

	s.mu.RLock()
	store := s.store
	s.mu.RUnlock()

	if store == nil {
		return nil, GetConversationOutput{}, fmt.Errorf("session store not available")
	}

	// Get metadata for the target conversation
	meta, err := store.GetMetadata(input.ConversationID)
	if err != nil {
		return nil, GetConversationOutput{}, fmt.Errorf("conversation not found: %s", input.ConversationID)
	}

	// Build unified conversation details
	output := s.buildConversationDetails(meta, store.SessionDir(meta.SessionID))

	s.logger.Debug("Get conversation properties",
		"source_session", realSessionID,
		"target_conversation", input.ConversationID,
		"is_running", output.IsRunning,
		"is_prompting", output.IsPrompting)

	return nil, output, nil
}

// SetPeriodicInput is the input for mitto_conversation_set_periodic tool.
type SetPeriodicInput struct {
	SelfID         string `json:"self_id"`                // YOUR session ID (the caller)
	ConversationID string `json:"conversation_id"`        // Target conversation to configure
	Prompt         string `json:"prompt"`                 // The prompt to send periodically
	FrequencyValue int    `json:"frequency_value"`        // Number of units between sends (e.g., 30 for "every 30 minutes")
	FrequencyUnit  string `json:"frequency_unit"`         // Time unit: "minutes", "hours", or "days"
	FrequencyAt    string `json:"frequency_at,omitempty"` // Time of day in HH:MM format (UTC), only for "days" unit
	Enabled        *bool  `json:"enabled,omitempty"`      // Whether periodic is active (defaults to true)
}

// SetPeriodicOutput is the output for mitto_conversation_set_periodic tool.
type SetPeriodicOutput struct {
	Success         bool   `json:"success"`
	ConversationID  string `json:"conversation_id,omitempty"`
	Prompt          string `json:"prompt,omitempty"`
	FrequencyValue  int    `json:"frequency_value,omitempty"`
	FrequencyUnit   string `json:"frequency_unit,omitempty"`
	FrequencyAt     string `json:"frequency_at,omitempty"`
	Enabled         bool   `json:"enabled,omitempty"`
	NextScheduledAt string `json:"next_scheduled_at,omitempty"` // RFC3339 format
	Error           string `json:"error,omitempty"`
}

func (s *Server) handleSetPeriodic(ctx context.Context, req *mcp.CallToolRequest, input SetPeriodicInput) (*mcp.CallToolResult, SetPeriodicOutput, error) {
	// Validate self_id
	if input.SelfID == "" {
		return nil, SetPeriodicOutput{Success: false, Error: "self_id is required"}, nil
	}

	// Validate conversation_id
	if input.ConversationID == "" {
		return nil, SetPeriodicOutput{Success: false, Error: "conversation_id is required"}, nil
	}

	// Validate prompt
	if input.Prompt == "" {
		return nil, SetPeriodicOutput{Success: false, Error: "prompt is required"}, nil
	}

	// Validate frequency_value
	if input.FrequencyValue < 1 {
		return nil, SetPeriodicOutput{Success: false, Error: "frequency_value must be >= 1"}, nil
	}

	// Validate frequency_unit
	var freqUnit session.FrequencyUnit
	switch input.FrequencyUnit {
	case "minutes":
		freqUnit = session.FrequencyMinutes
	case "hours":
		freqUnit = session.FrequencyHours
	case "days":
		freqUnit = session.FrequencyDays
	default:
		return nil, SetPeriodicOutput{Success: false, Error: "frequency_unit must be 'minutes', 'hours', or 'days'"}, nil
	}

	// Resolve the self_id to a real session ID
	realSessionID := s.resolveSelfID(input.SelfID)
	if realSessionID == "" {
		return nil, SetPeriodicOutput{
			Success: false,
			Error:   fmt.Sprintf("session not found: the self_id '%s' could not be resolved", input.SelfID),
		}, nil
	}

	// Check if source session is registered (must be running to use this tool)
	reg := s.getSession(realSessionID)
	if reg == nil {
		return nil, SetPeriodicOutput{Success: false, Error: fmt.Sprintf("session not found or not running: %s", realSessionID)}, nil
	}

	s.mu.RLock()
	store := s.store
	s.mu.RUnlock()

	if store == nil {
		return nil, SetPeriodicOutput{Success: false, Error: "session store not available"}, nil
	}

	// Verify target conversation exists
	if _, err := store.GetMetadata(input.ConversationID); err != nil {
		return nil, SetPeriodicOutput{
			Success: false,
			Error:   fmt.Sprintf("conversation not found: %s", input.ConversationID),
		}, nil
	}

	// Build frequency configuration
	freq := session.Frequency{
		Value: input.FrequencyValue,
		Unit:  freqUnit,
		At:    input.FrequencyAt,
	}

	// Validate frequency
	if err := freq.Validate(); err != nil {
		return nil, SetPeriodicOutput{Success: false, Error: err.Error()}, nil
	}

	// Determine enabled state (default to true)
	enabled := true
	if input.Enabled != nil {
		enabled = *input.Enabled
	}

	// Create periodic prompt configuration
	periodic := &session.PeriodicPrompt{
		Prompt:    input.Prompt,
		Frequency: freq,
		Enabled:   enabled,
	}

	// Get the periodic store for the target conversation
	periodicStore := store.Periodic(input.ConversationID)

	// Set the periodic configuration
	if err := periodicStore.Set(periodic); err != nil {
		return nil, SetPeriodicOutput{Success: false, Error: fmt.Sprintf("failed to set periodic: %v", err)}, nil
	}

	// Get the updated configuration to return
	updated, err := periodicStore.Get()
	if err != nil {
		return nil, SetPeriodicOutput{Success: false, Error: fmt.Sprintf("failed to read updated periodic: %v", err)}, nil
	}

	s.logger.Info("Periodic prompt configured via MCP",
		"source_session", realSessionID,
		"target_conversation", input.ConversationID,
		"frequency_value", input.FrequencyValue,
		"frequency_unit", input.FrequencyUnit,
		"enabled", enabled)

	output := SetPeriodicOutput{
		Success:        true,
		ConversationID: input.ConversationID,
		Prompt:         updated.Prompt,
		FrequencyValue: updated.Frequency.Value,
		FrequencyUnit:  string(updated.Frequency.Unit),
		FrequencyAt:    updated.Frequency.At,
		Enabled:        updated.Enabled,
	}

	if updated.NextScheduledAt != nil {
		output.NextScheduledAt = updated.NextScheduledAt.Format("2006-01-02T15:04:05Z07:00")
	}

	return nil, output, nil
}

// ArchiveConversationInput is the input for mitto_conversation_archive tool.
type ArchiveConversationInput struct {
	SelfID         string `json:"self_id"`            // YOUR session ID (the caller)
	ConversationID string `json:"conversation_id"`    // Target conversation to archive/unarchive
	Archived       *bool  `json:"archived,omitempty"` // true to archive, false to unarchive (defaults to true)
}

// ArchiveConversationOutput is the output for mitto_conversation_archive tool.
type ArchiveConversationOutput struct {
	Success        bool   `json:"success"`
	ConversationID string `json:"conversation_id,omitempty"`
	Archived       bool   `json:"archived,omitempty"`
	ArchivedAt     string `json:"archived_at,omitempty"` // RFC3339 format, only when archiving
	Error          string `json:"error,omitempty"`
}

// archiveWaitTimeout is the maximum time to wait for a response to complete when archiving.
const archiveWaitTimeout = 5 * time.Minute

func (s *Server) handleArchiveConversation(ctx context.Context, req *mcp.CallToolRequest, input ArchiveConversationInput) (*mcp.CallToolResult, ArchiveConversationOutput, error) {
	// Validate self_id
	if input.SelfID == "" {
		return nil, ArchiveConversationOutput{Success: false, Error: "self_id is required"}, nil
	}

	// Validate conversation_id
	if input.ConversationID == "" {
		return nil, ArchiveConversationOutput{Success: false, Error: "conversation_id is required"}, nil
	}

	// Default to archiving if not specified
	archived := true
	if input.Archived != nil {
		archived = *input.Archived
	}

	// Resolve the self_id to a real session ID
	realSessionID := s.resolveSelfID(input.SelfID)
	if realSessionID == "" {
		return nil, ArchiveConversationOutput{
			Success: false,
			Error:   fmt.Sprintf("session not found: the self_id '%s' could not be resolved", input.SelfID),
		}, nil
	}

	// Check if source session is registered (must be running to use this tool)
	reg := s.getSession(realSessionID)
	if reg == nil {
		return nil, ArchiveConversationOutput{Success: false, Error: fmt.Sprintf("session not found or not running: %s", realSessionID)}, nil
	}

	s.mu.RLock()
	store := s.store
	sessionManager := s.sessionManager
	s.mu.RUnlock()

	if store == nil {
		return nil, ArchiveConversationOutput{Success: false, Error: "session store not available"}, nil
	}

	// Verify target conversation exists
	meta, err := store.GetMetadata(input.ConversationID)
	if err != nil {
		return nil, ArchiveConversationOutput{
			Success: false,
			Error:   fmt.Sprintf("conversation not found: %s", input.ConversationID),
		}, nil
	}

	// Check if already in the desired state
	if meta.Archived == archived {
		state := "archived"
		if !archived {
			state = "unarchived"
		}
		return nil, ArchiveConversationOutput{
			Success:        true,
			ConversationID: input.ConversationID,
			Archived:       archived,
			Error:          fmt.Sprintf("conversation is already %s", state),
		}, nil
	}

	// Handle archive lifecycle
	if archived {
		if sessionManager != nil {
			// Wait for any active response to complete before archiving
			reason := "archived_via_mcp"
			if !sessionManager.CloseSessionGracefully(input.ConversationID, reason, archiveWaitTimeout) {
				// Timeout waiting for response - still proceed with archive but log warning
				s.logger.Warn("Timeout waiting for response before archiving via MCP, proceeding anyway",
					"session_id", input.ConversationID)
				// Force close the session
				reason = "archived_timeout_via_mcp"
				sessionManager.CloseSession(input.ConversationID, reason)
			}
		}
	}

	// Update metadata
	var archivedAt time.Time
	err = store.UpdateMetadata(input.ConversationID, func(m *session.Metadata) {
		m.Archived = archived
		if archived {
			archivedAt = time.Now()
			m.ArchivedAt = archivedAt
		} else {
			m.ArchivedAt = time.Time{}
		}
	})
	if err != nil {
		return nil, ArchiveConversationOutput{
			Success: false,
			Error:   fmt.Sprintf("failed to update metadata: %v", err),
		}, nil
	}

	// Handle unarchive lifecycle: restart ACP session
	if !archived && sessionManager != nil {
		_, err := sessionManager.ResumeSession(input.ConversationID, meta.Name, meta.WorkingDir)
		if err != nil {
			s.logger.Warn("Failed to resume ACP session after unarchive via MCP",
				"session_id", input.ConversationID,
				"error", err)
			// Don't fail the request - the session is unarchived, ACP will start when user sends a message
		} else {
			s.logger.Info("Resumed ACP session after unarchive via MCP",
				"session_id", input.ConversationID)
		}
	}

	action := "archived"
	if !archived {
		action = "unarchived"
	}
	s.logger.Info("Conversation "+action+" via MCP",
		"source_session", realSessionID,
		"target_conversation", input.ConversationID,
		"archived", archived)

	output := ArchiveConversationOutput{
		Success:        true,
		ConversationID: input.ConversationID,
		Archived:       archived,
	}

	if archived && !archivedAt.IsZero() {
		output.ArchivedAt = archivedAt.Format("2006-01-02T15:04:05Z07:00")
	}

	return nil, output, nil
}
