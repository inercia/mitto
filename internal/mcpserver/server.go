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
	// Maps request_id -> FIFO queue of pendingRequests (handles concurrent calls with same key).
	pendingRequestsMu sync.RWMutex
	pendingRequests   map[string][]*pendingRequest

	// MCP session ID -> Mitto session ID cache.
	// After a successful get_current resolution, we cache the mapping from the MCP
	// protocol session ID (from Mcp-Session-Id header) to the Mitto session ID.
	// This provides a reliable Phase 3 fallback for subsequent tool calls from the
	// same MCP client, avoiding re-correlation.
	mcpSessionMapMu sync.RWMutex
	mcpSessionMap   map[string]string

	// Parent-child task coordination.
	// Maps parent_session_id -> *childReportCollector for collecting children's progress reports.
	// Collectors persist for the lifetime of the parent session (cleaned up in UnregisterSession).
	childReportCollectorsMu sync.Mutex
	childReportCollectors   map[string]*childReportCollector
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
	// GetWorkspacesForFolder returns all workspace configurations for the given folder.
	// Multiple workspaces may share the same folder with different ACP servers.
	GetWorkspacesForFolder(folder string) []config.WorkspaceSettings
	// BroadcastSessionCreated broadcasts a session_created event to all connected clients.
	BroadcastSessionCreated(sessionID, name, acpServer, workingDir, parentSessionID string)
	// BroadcastSessionArchived broadcasts a session_archived event to all connected clients.
	BroadcastSessionArchived(sessionID string, archived bool)
	// BroadcastSessionDeleted broadcasts a session_deleted event to all connected clients.
	BroadcastSessionDeleted(sessionID string)
	// BroadcastWaitingForChildren broadcasts a session_waiting event to all connected clients.
	BroadcastWaitingForChildren(sessionID string, isWaiting bool)
	// DeleteChildSessions permanently deletes all child sessions when a parent is archived.
	DeleteChildSessions(parentID string)
}

// BackgroundSession interface for session info.
type BackgroundSession interface {
	IsPrompting() bool
	GetEventCount() int
	GetMaxAssignedSeq() int64
	// TryProcessQueuedMessage attempts to process the next queued message if the session is idle.
	// Returns true if a message was sent.
	TryProcessQueuedMessage() bool
	// WaitForResponseComplete waits for the current prompt to complete, if one is in progress.
	// Returns true if the prompt completed within the timeout, false if it timed out.
	// If no prompt is in progress, returns immediately with true.
	WaitForResponseComplete(timeout time.Duration) bool
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
		logger:                logger,
		host:                  cfg.Host,
		port:                  cfg.Port,
		mode:                  cfg.Mode,
		store:                 deps.Store,
		config:                deps.Config,
		sessionManager:        deps.SessionManager,
		sessions:              make(map[string]*registeredSession),
		pendingRequests:       make(map[string][]*pendingRequest),
		mcpSessionMap:         make(map[string]string),
		childReportCollectors: make(map[string]*childReportCollector),
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
//
// This method is idempotent: if the session is already registered, the existing
// registration is updated in place (e.g., with a new UIPrompter after an ACP
// process restart). This prevents "session already registered" errors during
// automatic restarts where the old registration may not have been cleaned up.
func (s *Server) RegisterSession(sessionID string, uiPrompter UIPrompter, logger *slog.Logger) error {
	s.sessionsMu.Lock()
	defer s.sessionsMu.Unlock()

	if existing, exists := s.sessions[sessionID]; exists {
		// Update existing registration in place (idempotent restart).
		existing.uiPrompter = uiPrompter
		existing.logger = logger
		s.logger.Info("Session re-registered with MCP server (restart)", "session_id", sessionID)
		return nil
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

	// Clean up child report collector for this parent session
	s.childReportCollectorsMu.Lock()
	delete(s.childReportCollectors, sessionID)
	s.childReportCollectorsMu.Unlock()

	// Clean up MCP session cache entries pointing to this session
	s.mcpSessionMapMu.Lock()
	for mcpID, mittoID := range s.mcpSessionMap {
		if mittoID == sessionID {
			delete(s.mcpSessionMap, mcpID)
		}
	}
	s.mcpSessionMapMu.Unlock()

	s.logger.Info("Session unregistered from MCP server", "session_id", sessionID)
}

// getSession returns the registered session for the given session ID.
// Returns nil if the session is not registered.
func (s *Server) getSession(sessionID string) *registeredSession {
	s.sessionsMu.RLock()
	defer s.sessionsMu.RUnlock()
	return s.sessions[sessionID]
}

// getOrCreateCollector returns the existing child report collector for the given parent session ID,
// or creates a new one if it doesn't exist. The collector persists for the lifetime of the parent session.
func (s *Server) getOrCreateCollector(parentSessionID string) *childReportCollector {
	s.childReportCollectorsMu.Lock()
	defer s.childReportCollectorsMu.Unlock()

	collector := s.childReportCollectors[parentSessionID]
	if collector == nil {
		collector = &childReportCollector{
			parentSessionID: parentSessionID,
			reports:         make(map[string]*childReport),
		}
		s.childReportCollectors[parentSessionID] = collector
	}
	return collector
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
// Uses a FIFO queue per key to handle concurrent calls with the same key
// (e.g., multiple sessions calling get_current with self_id="init").
func (s *Server) RegisterPendingRequest(requestID, sessionID string) {
	if requestID == "" || sessionID == "" {
		return
	}

	s.pendingRequestsMu.Lock()
	defer s.pendingRequestsMu.Unlock()

	s.pendingRequests[requestID] = append(s.pendingRequests[requestID], &pendingRequest{
		sessionID:    sessionID,
		registeredAt: time.Now(),
	})

	s.logger.Debug("Pending request registered",
		"request_id", requestID,
		"session_id", sessionID,
		"queue_depth", len(s.pendingRequests[requestID]),
	)

	// Cleanup expired entries while we have the lock
	s.cleanupExpiredPendingRequestsLocked()
}

// WaitForPendingRequest waits for a pending request to be registered and returns the session ID.
// It polls until the request is found or the timeout expires.
// Uses FIFO ordering: when multiple sessions register the same key (e.g., "init"),
// the first registration is consumed first.
// Returns empty string if the request is not found within the timeout.
func (s *Server) WaitForPendingRequest(requestID string) string {
	if requestID == "" {
		return ""
	}

	deadline := time.Now().Add(pendingRequestTimeout)

	for time.Now().Before(deadline) {
		s.pendingRequestsMu.RLock()
		queue, exists := s.pendingRequests[requestID]
		hasEntries := exists && len(queue) > 0
		s.pendingRequestsMu.RUnlock()

		if hasEntries {
			// Pop the first entry (FIFO) under write lock
			s.pendingRequestsMu.Lock()
			queue = s.pendingRequests[requestID]
			if len(queue) == 0 {
				// Race: another goroutine consumed it between RLock and Lock
				s.pendingRequestsMu.Unlock()
				time.Sleep(pendingRequestPollInterval)
				continue
			}
			req := queue[0]
			if len(queue) == 1 {
				delete(s.pendingRequests, requestID)
			} else {
				s.pendingRequests[requestID] = queue[1:]
			}
			s.pendingRequestsMu.Unlock()

			s.logger.Debug("Pending request found",
				"request_id", requestID,
				"session_id", req.sessionID,
			)
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
	for reqID, queue := range s.pendingRequests {
		// Filter out expired entries from the queue
		n := 0
		for _, req := range queue {
			if now.Sub(req.registeredAt) <= pendingRequestExpiry {
				queue[n] = req
				n++
			}
		}
		if n == 0 {
			delete(s.pendingRequests, reqID)
			s.logger.Debug("Expired pending request queue removed", "request_id", reqID)
		} else {
			s.pendingRequests[reqID] = queue[:n]
		}
	}
}

// cacheMCPSession stores a mapping from MCP protocol session ID to Mitto session ID.
// This is called after a successful get_current resolution to enable Phase 3 lookups.
func (s *Server) cacheMCPSession(mcpSessionID, mittoSessionID string) {
	if mcpSessionID == "" || mittoSessionID == "" {
		return
	}
	s.mcpSessionMapMu.Lock()
	defer s.mcpSessionMapMu.Unlock()
	s.mcpSessionMap[mcpSessionID] = mittoSessionID
	s.logger.Debug("MCP session cached",
		"mcp_session_id", mcpSessionID,
		"mitto_session_id", mittoSessionID,
	)
}

// lookupMCPSession looks up a Mitto session ID by MCP protocol session ID.
func (s *Server) lookupMCPSession(mcpSessionID string) string {
	if mcpSessionID == "" {
		return ""
	}
	s.mcpSessionMapMu.RLock()
	defer s.mcpSessionMapMu.RUnlock()
	return s.mcpSessionMap[mcpSessionID]
}

// resolveSelfIDWithMCP resolves self_id with an additional Phase 3: MCP session ID lookup.
// This should be used by tool handlers that have access to the MCP request.
func (s *Server) resolveSelfIDWithMCP(inputSessionID string, req *mcp.CallToolRequest) string {
	// Phase 1 + Phase 2 (existing)
	result := s.resolveSelfID(inputSessionID)
	if result != "" {
		return result
	}

	// Phase 3: MCP session ID lookup
	// After a successful get_current call, the MCP session → Mitto session mapping
	// is cached. This handles subsequent calls from the same MCP client even if
	// self_id is wrong or the correlation mechanism fails.
	if req != nil && req.Session != nil {
		mcpSessionID := req.Session.ID()
		if cached := s.lookupMCPSession(mcpSessionID); cached != "" {
			s.logger.Debug("Session resolved via MCP session cache",
				"input_session_id", inputSessionID,
				"mcp_session_id", mcpSessionID,
				"resolved_session_id", cached,
			)
			return cached
		}
	}

	return ""
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

	// Populate available ACP servers — only those with workspaces for this folder
	s.mu.RLock()
	cfg := s.config
	sm2 := s.sessionManager
	s.mu.RUnlock()

	if cfg != nil && len(cfg.ACPServers) > 0 && sm2 != nil {
		// Filter to only servers that have a workspace defined for this session's folder
		folderWorkspaces := sm2.GetWorkspacesForFolder(meta.WorkingDir)
		wsServerSet := make(map[string]bool, len(folderWorkspaces))
		for _, ws := range folderWorkspaces {
			wsServerSet[ws.ACPServer] = true
		}

		servers := make([]AvailableACPServer, 0, len(folderWorkspaces))
		for _, srv := range cfg.ACPServers {
			if wsServerSet[srv.Name] {
				servers = append(servers, AvailableACPServer{
					Name:    srv.Name,
					Type:    srv.GetType(),
					Tags:    srv.Tags,
					Current: srv.Name == meta.ACPServer,
				})
			}
		}
		details.AvailableACPServers = servers
	} else if cfg != nil && len(cfg.ACPServers) > 0 {
		// Fallback if session manager not available: show all servers
		servers := make([]AvailableACPServer, 0, len(cfg.ACPServers))
		for _, srv := range cfg.ACPServers {
			servers = append(servers, AvailableACPServer{
				Name:    srv.Name,
				Type:    srv.GetType(),
				Tags:    srv.Tags,
				Current: srv.Name == meta.ACPServer,
			})
		}
		details.AvailableACPServers = servers
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
			"To CREATE a new conversation, use 'mitto_conversation_new' instead. Always available. " +
			"All parameters are optional filters — omit them to list all conversations.",
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
			"Use 'mitto_conversation_list' first to find existing conversation IDs, or use an ID returned by 'mitto_conversation_new'. " +
			"Requires 'Can Send Prompt' flag to be enabled. " +
			selfIDNote,
	}, s.handleSendPromptToConversation)

	// mitto_ui_options - Unified options menu (replaces ask_yes_no, options_buttons, options_combo)
	mcp.AddTool(mcpSrv, &mcp.Tool{
		Name: "mitto_ui_options",
		Description: "Present a list of options to the user as an expandable menu and wait for their selection. " +
			"Each option can have a short label and an optional longer description. " +
			"Optionally allows the user to type free text instead of selecting a predefined option. " +
			"Requires 'Can prompt user' flag to be enabled. " +
			selfIDNote,
	}, s.handleUIOptions)

	// mitto_ui_textbox - Present editable text to user
	mcp.AddTool(mcpSrv, &mcp.Tool{
		Name: "mitto_ui_textbox",
		Description: "Present a text editing dialog to the user and wait for their changes. " +
			"Shows a modal with a title and a large editable textarea pre-filled with the provided text. " +
			"The user can edit the text and submit, or abort if allowed. " +
			"Returns the edited text (or a unified diff of changes). " +
			"Text is limited to 16KB. For short-to-medium text snippets only, not full files. " +
			"Requires 'Can prompt user' flag to be enabled. " +
			selfIDNote,
	}, s.handleUITextbox)

	// mitto_ui_ask_yes_no - Present a yes/no question
	mcp.AddTool(mcpSrv, &mcp.Tool{
		Name: "mitto_ui_ask_yes_no",
		Description: "[Deprecated: use mitto_ui_options instead] " +
			"Present a yes/no question to the user and wait for their response. " +
			"The tool blocks until the user clicks a button or the timeout expires. " +
			"Requires 'Can prompt user' flag to be enabled. " +
			selfIDNote,
	}, s.handleAskYesNo)

	// mitto_ui_options_buttons - Present multiple option buttons
	mcp.AddTool(mcpSrv, &mcp.Tool{
		Name: "mitto_ui_options_buttons",
		Description: "[Deprecated: use mitto_ui_options instead] " +
			"Present multiple options as buttons to the user and wait for their selection. " +
			"The tool blocks until the user clicks a button or the timeout expires. " +
			"Requires 'Can prompt user' flag to be enabled. " +
			selfIDNote,
	}, s.handleOptionsButtons)

	// mitto_ui_options_combo - Present a combo box
	mcp.AddTool(mcpSrv, &mcp.Tool{
		Name: "mitto_ui_options_combo",
		Description: "[Deprecated: use mitto_ui_options instead] " +
			"Present a dropdown/combo box with options to the user. " +
			"The user selects an option and clicks OK to confirm. " +
			"Requires 'Can prompt user' flag to be enabled. " +
			selfIDNote,
	}, s.handleOptionsCombo)

	// mitto_conversation_new - Start a new conversation
	mcp.AddTool(mcpSrv, &mcp.Tool{
		Name: "mitto_conversation_new",
		Description: "USE THIS TOOL TO CREATE A NEW CONVERSATION - no browser or UI interaction needed! " +
			"This tool programmatically creates and starts a NEW agent conversation that runs in parallel with your current session. " +
			"When a user asks you to 'create a new conversation', 'start a new session', or 'investigate something in a new conversation', " +
			"call this tool directly instead of trying to click buttons or navigate a UI. " +
			"This spawns a separate AI agent that can work independently on the task you specify. " +
			"Use this to delegate work, run background tasks, or parallelize complex work across multiple agents. " +
			"The new conversation inherits your workspace configuration. By default it also inherits your ACP server, " +
			"but you can specify a different one via the optional 'acp_server' parameter (must have a workspace configured for the current folder) " +
			"(use 'mitto_conversation_get_current' to see available ACP servers in the 'available_acp_servers' field). " +
			"Optionally provide a 'title' for the conversation and an 'initial_prompt' to start the agent working immediately. " +
			"Requires 'Can start conversation' flag to be enabled in Advanced Settings (disabled by default for security). " +
			"Note: Conversations created by this tool cannot spawn further conversations (to prevent infinite recursion). " +
			selfIDNote,
	}, s.handleConversationStart)

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

	// mitto_conversation_delete - Delete (archive) a child conversation
	mcp.AddTool(mcpSrv, &mcp.Tool{
		Name: "mitto_conversation_delete",
		Description: "Delete a child conversation. " +
			"This archives the child conversation, gracefully stopping any active agent response and closing its ACP connection. " +
			"The caller MUST be the parent of the target conversation (verified via the parent-child relationship). " +
			"Deleted conversations become read-only and will no longer accept prompts. " +
			selfIDNote,
	}, s.handleDeleteConversation)

	// mitto_conversation_wait - Wait until something happens in a conversation
	mcp.AddTool(mcpSrv, &mcp.Tool{
		Name: "mitto_conversation_wait",
		Description: "Wait until something happens in a conversation. " +
			"Currently supports: 'agent_responded' — blocks until the agent finishes responding. " +
			"Returns immediately if the condition is already met (e.g., agent is not currently responding). " +
			selfIDNote,
	}, s.handleConversationWait)

	// mitto_children_tasks_wait - Wait for children to report progress
	mcp.AddTool(mcpSrv, &mcp.Tool{
		Name: "mitto_children_tasks_wait",
		Description: "Send a progress inquiry to multiple child conversations and BLOCK until all of them report back. " +
			"For each child, the provided prompt (plus reporting instructions) is enqueued. " +
			"If prompt is empty or omitted, no message is sent — the tool just waits for children to report " +
			"(useful for retrying after a timeout without re-enqueuing duplicate messages). " +
			"Duplicate messages are also prevented: if a child already has a pending message from this parent " +
			"in its queue, the prompt is skipped for that child. " +
			"This tool blocks until all children have reported or the timeout expires. " +
			"Returns a consolidated report from all children. " +
			"Requires 'Can Send Prompt' flag to be enabled. " +
			"Use task_id to scope reports: when retrying the same task after a timeout, pass the same task_id " +
			"so that reports already received are preserved. When starting a different task, use a new task_id " +
			"to clear stale reports from the previous task. " +
			selfIDNote,
	}, s.handleChildrenTasksWait)

	// mitto_children_tasks_report - Report task progress back to parent
	mcp.AddTool(mcpSrv, &mcp.Tool{
		Name: "mitto_children_tasks_report",
		Description: "Report task completion or progress back to a waiting parent conversation. " +
			"The parent must have previously called mitto_children_tasks_wait with this conversation's ID in the children_list. " +
			"Provide a status (e.g. 'completed', 'in_progress', 'failed'), a summary of your findings, " +
			"and optionally details with additional information. " +
			"Keep reports concise: summary is limited to ~8KB and details to ~16KB. " +
			"If the parent provided a task_id in the wait call, include the same task_id in your report. " +
			selfIDNote,
	}, s.handleChildrenTasksReport)
}

// ListConversationsOutput wraps the list of conversations for MCP output schema compliance.
type ListConversationsOutput struct {
	Conversations []ConversationInfo `json:"conversations"`
}

// createListConversationsHandler creates the handler for list_conversations tool.
func (s *Server) createListConversationsHandler(sm SessionManager) mcp.ToolHandlerFor[ListConversationsInput, ListConversationsOutput] {
	return func(ctx context.Context, req *mcp.CallToolRequest, input ListConversationsInput) (*mcp.CallToolResult, ListConversationsOutput, error) {
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
			// Apply filters before building full info
			if input.WorkingDir != nil && meta.WorkingDir != *input.WorkingDir {
				continue
			}
			if input.Archived != nil && meta.Archived != *input.Archived {
				continue
			}
			if input.ACPServer != nil && meta.ACPServer != *input.ACPServer {
				continue
			}
			if input.ExcludeSelf != nil && meta.SessionID == *input.ExcludeSelf {
				continue
			}

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

			// Apply is_running filter after runtime status is resolved
			if input.IsRunning != nil && info.IsRunning != *input.IsRunning {
				continue
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

	// Resolve the self_id to a real session ID using three-phase lookup:
	// 1. Direct lookup if session_id is already a registered session
	// 2. Correlation lookup via pending request registration (for ACP-routed calls)
	// 3. MCP session ID cache lookup (for subsequent calls from the same MCP client)
	realSessionID := s.resolveSelfIDWithMCP(input.SelfID, req)
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

	// Cache the MCP session → Mitto session mapping for Phase 3 lookups.
	// After this point, all subsequent calls from the same MCP client can be
	// resolved via the MCP session ID cache, even if self_id is wrong.
	if req != nil && req.Session != nil {
		if mcpSessionID := req.Session.ID(); mcpSessionID != "" {
			s.cacheMCPSession(mcpSessionID, realSessionID)
		}
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
	realSessionID := s.resolveSelfIDWithMCP(input.SelfID, req)
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
	targetMeta, err := store.GetMetadata(input.ConversationID)
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

	// Try to process the queued message immediately if agent is idle.
	// If the session is not running (stored), auto-resume it first.
	if s.sessionManager != nil {
		bs := s.sessionManager.GetSession(input.ConversationID)
		if bs == nil && !targetMeta.Archived {
			// Session is stored (not running) — try to resume it so the queue gets processed.
			s.logger.Info("Auto-resuming stored session to process queued prompt",
				"target_session", input.ConversationID,
				"source_session", realSessionID)
			resumed, resumeErr := s.sessionManager.ResumeSession(input.ConversationID, targetMeta.Name, targetMeta.WorkingDir)
			if resumeErr != nil {
				s.logger.Warn("Failed to auto-resume stored session",
					"target_session", input.ConversationID,
					"error", resumeErr)
			} else {
				bs = resumed
			}
		}
		if bs != nil {
			go bs.TryProcessQueuedMessage()
		}
	}

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
	realSessionID := s.resolveSelfIDWithMCP(input.SelfID, req)
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
	realSessionID := s.resolveSelfIDWithMCP(input.SelfID, req)
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
	realSessionID := s.resolveSelfIDWithMCP(input.SelfID, req)
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

// UIOptionsItem represents a single option in the unified options menu.
type UIOptionsItem struct {
	Label       string `json:"label"`
	Description string `json:"description,omitempty"`
}

// UIOptionsInput is the input for the mitto_ui_options tool.
type UIOptionsInput struct {
	SelfID              string          `json:"self_id"` // YOUR session ID (the caller)
	Question            string          `json:"question"`
	Options             []UIOptionsItem `json:"options"`
	AllowFreeText       bool            `json:"allow_free_text,omitempty"`
	FreeTextPlaceholder string          `json:"free_text_placeholder,omitempty"`
	TimeoutSeconds      int             `json:"timeout_seconds,omitempty"`
}

// UIOptionsOutput is the output for the mitto_ui_options tool.
type UIOptionsOutput struct {
	Selected string `json:"selected,omitempty"`
	Index    int    `json:"index"`
	FreeText string `json:"free_text,omitempty"`
	TimedOut bool   `json:"timed_out,omitempty"`
}

func (s *Server) handleUIOptions(ctx context.Context, req *mcp.CallToolRequest, input UIOptionsInput) (*mcp.CallToolResult, UIOptionsOutput, error) {
	// Validate self_id
	if input.SelfID == "" {
		return nil, UIOptionsOutput{Index: -1}, fmt.Errorf("self_id is required")
	}

	// Resolve the self_id to a real session ID
	realSessionID := s.resolveSelfIDWithMCP(input.SelfID, req)
	if realSessionID == "" {
		return nil, UIOptionsOutput{Index: -1}, fmt.Errorf(
			"session not found: the self_id '%s' could not be resolved",
			input.SelfID,
		)
	}

	// Check if session is registered and get the UIPrompter
	reg := s.getSession(realSessionID)
	if reg == nil {
		return nil, UIOptionsOutput{Index: -1}, fmt.Errorf("session not found or not running: %s", realSessionID)
	}

	// Permission check
	if !s.checkSessionFlag(realSessionID, session.FlagCanPromptUser) {
		return nil, UIOptionsOutput{Index: -1}, permissionError("mitto_ui_options", session.FlagCanPromptUser, "Can prompt user")
	}

	// Check if UIPrompter is available
	if reg.uiPrompter == nil {
		return nil, UIOptionsOutput{Index: -1}, fmt.Errorf("UI prompts are not available (no UI connected)")
	}

	// Validate input
	if len(input.Options) == 0 && !input.AllowFreeText {
		return nil, UIOptionsOutput{Index: -1}, fmt.Errorf("at least one option is required (or enable allow_free_text)")
	}
	if len(input.Options) > 10 {
		return nil, UIOptionsOutput{Index: -1}, fmt.Errorf("mitto_ui_options supports at most 10 options (got %d)", len(input.Options))
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

	// Build options with IDs and descriptions
	options := make([]UIPromptOption, len(input.Options))
	for i, item := range input.Options {
		options[i] = UIPromptOption{
			ID:          fmt.Sprintf("%d", i),
			Label:       item.Label,
			Description: item.Description,
		}
	}

	promptReq := UIPromptRequest{
		RequestID:           uiRequestID,
		Type:                UIPromptTypeOptions,
		Question:            question,
		Options:             options,
		TimeoutSeconds:      timeout,
		AllowFreeText:       input.AllowFreeText,
		FreeTextPlaceholder: input.FreeTextPlaceholder,
	}

	s.logger.Debug("Sending UI options prompt",
		"session_id", realSessionID,
		"input_session_id", input.SelfID,
		"ui_request_id", uiRequestID,
		"option_count", len(input.Options),
		"allow_free_text", input.AllowFreeText,
		"timeout", timeout)

	resp, err := reg.uiPrompter.UIPrompt(ctx, promptReq)
	if err != nil {
		return nil, UIOptionsOutput{Index: -1}, fmt.Errorf("failed to display UI prompt: %w", err)
	}

	if resp.TimedOut {
		s.logger.Debug("UI options prompt timed out", "session_id", realSessionID)
		return nil, UIOptionsOutput{Index: -1, TimedOut: true}, nil
	}

	// Handle free text response
	if resp.FreeText != "" {
		s.logger.Debug("UI options prompt answered with free text",
			"session_id", realSessionID,
			"free_text", resp.FreeText)
		return nil, UIOptionsOutput{
			Index:    -1,
			FreeText: resp.FreeText,
		}, nil
	}

	var selectedIndex int
	if _, err := fmt.Sscanf(resp.OptionID, "%d", &selectedIndex); err != nil {
		selectedIndex = -1
	}

	s.logger.Debug("UI options prompt answered",
		"session_id", realSessionID,
		"selected", resp.Label,
		"index", selectedIndex)

	return nil, UIOptionsOutput{
		Selected: resp.Label,
		Index:    selectedIndex,
	}, nil
}

func (s *Server) handleUITextbox(ctx context.Context, req *mcp.CallToolRequest, input UITextboxInput) (*mcp.CallToolResult, UITextboxOutput, error) {
	// Validate self_id
	if input.SelfID == "" {
		return nil, UITextboxOutput{}, fmt.Errorf("self_id is required")
	}

	// Resolve the self_id to a real session ID
	realSessionID := s.resolveSelfIDWithMCP(input.SelfID, req)
	if realSessionID == "" {
		return nil, UITextboxOutput{}, fmt.Errorf(
			"session not found: the self_id '%s' could not be resolved",
			input.SelfID,
		)
	}

	// Check if session is registered and get the UIPrompter
	reg := s.getSession(realSessionID)
	if reg == nil {
		return nil, UITextboxOutput{}, fmt.Errorf("session not found or not running: %s", realSessionID)
	}

	// Permission check
	if !s.checkSessionFlag(realSessionID, session.FlagCanPromptUser) {
		return nil, UITextboxOutput{}, permissionError("mitto_ui_textbox", session.FlagCanPromptUser, "Can prompt user")
	}

	// Check if UIPrompter is available
	if reg.uiPrompter == nil {
		return nil, UITextboxOutput{}, fmt.Errorf("UI prompts are not available (no UI connected)")
	}

	// Validate input
	if input.Title == "" {
		return nil, UITextboxOutput{}, fmt.Errorf("title is required")
	}
	if input.Text == "" {
		return nil, UITextboxOutput{}, fmt.Errorf("text is required")
	}
	const maxTextSize = 16 * 1024 // 16KB
	if len(input.Text) > maxTextSize {
		return nil, UITextboxOutput{}, fmt.Errorf("text exceeds maximum size of 16KB (got %d bytes)", len(input.Text))
	}

	// Validate and default result mode
	resultMode := input.ResultMode
	if resultMode == "" {
		resultMode = "text"
	}
	if resultMode != "text" && resultMode != "diff" {
		return nil, UITextboxOutput{}, fmt.Errorf("result must be 'text' or 'diff' (got '%s')", resultMode)
	}

	// Apply timeout default
	timeout := input.TimeoutSeconds
	if timeout <= 0 {
		timeout = 600 // 10 minutes default for text editing
	}

	// Generate unique internal request ID
	uiRequestID := fmt.Sprintf("%s-%s", realSessionID[:8], uuid.New().String()[:8])

	// Build the prompt request
	promptReq := UIPromptRequest{
		RequestID:      uiRequestID,
		Type:           UIPromptTypeTextbox,
		Title:          input.Title,
		Question:       input.Title, // Use title as question for consistency
		Text:           input.Text,
		ResultMode:     resultMode,
		AllowAbort:     true, // Always allow abort
		TimeoutSeconds: timeout,
		Blocking:       true,
	}

	s.logger.Debug("Sending UI textbox prompt",
		"session_id", realSessionID,
		"input_session_id", input.SelfID,
		"ui_request_id", uiRequestID,
		"title", input.Title,
		"text_length", len(input.Text),
		"result_mode", resultMode,
		"timeout", timeout)

	// Send prompt and wait for response (blocks until user responds or timeout)
	resp, err := reg.uiPrompter.UIPrompt(ctx, promptReq)
	if err != nil {
		return nil, UITextboxOutput{}, fmt.Errorf("failed to display UI textbox: %w", err)
	}

	// Handle timeout
	if resp.TimedOut {
		s.logger.Debug("UI textbox prompt timed out", "session_id", realSessionID)
		return nil, UITextboxOutput{TimedOut: true}, nil
	}

	// Handle abort
	if resp.Aborted || resp.OptionID == "abort" {
		s.logger.Debug("UI textbox prompt aborted", "session_id", realSessionID)
		return nil, UITextboxOutput{Aborted: true}, nil
	}

	// Get the edited text from the response
	editedText := resp.FreeText

	// Check if text was changed
	changed := editedText != input.Text

	if !changed {
		s.logger.Debug("UI textbox prompt submitted without changes", "session_id", realSessionID)
		return nil, UITextboxOutput{Changed: false}, nil
	}

	// Compute result based on mode
	var result string
	if resultMode == "diff" {
		result = computeUnifiedDiff(input.Text, editedText, "original", "edited")
	} else {
		result = editedText
	}

	s.logger.Debug("UI textbox prompt submitted with changes",
		"session_id", realSessionID,
		"result_mode", resultMode,
		"original_length", len(input.Text),
		"edited_length", len(editedText))

	return nil, UITextboxOutput{
		Changed: true,
		Result:  result,
	}, nil
}

// computeUnifiedDiff generates a simple unified diff between two texts.
func computeUnifiedDiff(original, edited, originalName, editedName string) string {
	originalLines := strings.Split(original, "\n")
	editedLines := strings.Split(edited, "\n")

	var result strings.Builder
	result.WriteString(fmt.Sprintf("--- %s\n", originalName))
	result.WriteString(fmt.Sprintf("+++ %s\n", editedName))

	m, n := len(originalLines), len(editedLines)

	// Build LCS table
	lcs := make([][]int, m+1)
	for i := range lcs {
		lcs[i] = make([]int, n+1)
	}
	for i := 1; i <= m; i++ {
		for j := 1; j <= n; j++ {
			if originalLines[i-1] == editedLines[j-1] {
				lcs[i][j] = lcs[i-1][j-1] + 1
			} else if lcs[i-1][j] >= lcs[i][j-1] {
				lcs[i][j] = lcs[i-1][j]
			} else {
				lcs[i][j] = lcs[i][j-1]
			}
		}
	}

	// Backtrack to find the diff operations
	type diffOp struct {
		op   byte // ' ' = context, '-' = remove, '+' = add
		line string
	}
	var ops []diffOp
	i, j := m, n
	for i > 0 || j > 0 {
		if i > 0 && j > 0 && originalLines[i-1] == editedLines[j-1] {
			ops = append(ops, diffOp{' ', originalLines[i-1]})
			i--
			j--
		} else if j > 0 && (i == 0 || lcs[i][j-1] >= lcs[i-1][j]) {
			ops = append(ops, diffOp{'+', editedLines[j-1]})
			j--
		} else if i > 0 {
			ops = append(ops, diffOp{'-', originalLines[i-1]})
			i--
		}
	}

	// Reverse ops (we built them backwards)
	for left, right := 0, len(ops)-1; left < right; left, right = left+1, right-1 {
		ops[left], ops[right] = ops[right], ops[left]
	}

	// Output all ops with unified diff markers
	for _, op := range ops {
		switch op.op {
		case ' ':
			result.WriteString(fmt.Sprintf(" %s\n", op.line))
		case '-':
			result.WriteString(fmt.Sprintf("-%s\n", op.line))
		case '+':
			result.WriteString(fmt.Sprintf("+%s\n", op.line))
		}
	}

	return result.String()
}

// ConversationStartInput is the input for mitto_conversation_new tool.
type ConversationStartInput struct {
	SelfID        string `json:"self_id"`                  // YOUR session ID (the caller)
	Title         string `json:"title,omitempty"`          // Optional title for the new conversation
	InitialPrompt string `json:"initial_prompt,omitempty"` // Optional initial message to queue
	ACPServer     string `json:"acp_server,omitempty"`     // Optional ACP server name (defaults to parent's server)
}

// ConversationStartOutput is the output for mitto_conversation_new tool.
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
	realSessionID := s.resolveSelfIDWithMCP(input.SelfID, req)
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
	// This allows users to disable conversation creation via the UI toggle
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

	// Check for duplicate title if title is provided
	if input.Title != "" {
		allSessions, err := store.List()
		if err != nil {
			return nil, ConversationStartOutput{}, fmt.Errorf("failed to check for duplicate titles: %v", err)
		}
		for _, existingMeta := range allSessions {
			if existingMeta.Name == input.Title {
				return nil, ConversationStartOutput{}, fmt.Errorf(
					"a conversation with the title '%s' already exists (session_id: %s). Please choose a different title",
					input.Title, existingMeta.SessionID)
			}
		}
	}

	// Determine which ACP server to use
	acpServerName := sourceMeta.ACPServer // Default: inherit from parent
	if input.ACPServer != "" {
		// Validate the requested ACP server exists in config
		s.mu.RLock()
		cfg := s.config
		s.mu.RUnlock()

		if cfg == nil {
			return nil, ConversationStartOutput{}, fmt.Errorf("server configuration not available")
		}
		if _, err := cfg.GetServer(input.ACPServer); err != nil {
			return nil, ConversationStartOutput{}, fmt.Errorf(
				"ACP server '%s' not found. Available servers: %v",
				input.ACPServer, cfg.ServerNames())
		}
		acpServerName = input.ACPServer
	}

	// Validate that a workspace exists for the folder + ACP server combination.
	// Conversations can only run in defined workspaces (folder + ACP server pairs).
	if s.sessionManager != nil {
		workspaces := s.sessionManager.GetWorkspacesForFolder(sourceMeta.WorkingDir)
		found := false
		for _, ws := range workspaces {
			if ws.ACPServer == acpServerName {
				found = true
				break
			}
		}
		if !found {
			availableServers := make([]string, 0, len(workspaces))
			for _, ws := range workspaces {
				availableServers = append(availableServers, ws.ACPServer)
			}
			return nil, ConversationStartOutput{}, fmt.Errorf(
				"no workspace configured for folder %q with ACP server %q. "+
					"Available ACP servers for this folder: %v. "+
					"Create a workspace for this folder+server pair in Settings first",
				sourceMeta.WorkingDir, acpServerName, availableServers)
		}
	}

	// Create new session ID using the standard timestamp format
	// This ensures compatibility with IsValidSessionID validation in the web layer
	newSessionID := session.GenerateSessionID()

	// Create the new session metadata
	// NOTE: Recursion is prevented by the ParentSessionID check above — children
	// with a parent cannot create new conversations. When the parent is deleted,
	// the child becomes an orphan (ParentSessionID is cleared) and can then create
	// new conversations since it inherits the parent's flags including can_start_conversation.

	// Inherit parent's advanced settings so orphaned children retain the same flags.
	childSettings := make(map[string]bool)
	for k, v := range sourceMeta.AdvancedSettings {
		childSettings[k] = v
	}

	newMeta := session.Metadata{
		SessionID:        newSessionID,
		Name:             input.Title,
		ACPServer:        acpServerName,
		WorkingDir:       sourceMeta.WorkingDir,
		ParentSessionID:  realSessionID,          // Mark this session as a child
		ChildOrigin:      session.ChildOriginMCP, // Created via MCP tool
		AdvancedSettings: childSettings,
	}

	// Create the session
	if err := store.Create(newMeta); err != nil {
		return nil, ConversationStartOutput{}, fmt.Errorf("failed to create session: %v", err)
	}

	s.logger.Info("New conversation created via MCP",
		"new_session_id", newSessionID,
		"parent_session_id", realSessionID,
		"acp_server", acpServerName,
		"working_dir", sourceMeta.WorkingDir,
		"title", input.Title)

	// Re-fetch metadata to get timestamps set by Create()
	createdMeta, err := store.GetMetadata(newSessionID)
	if err != nil {
		// Use the newMeta we have if re-fetch fails
		createdMeta = newMeta
	}

	// Start the ACP process for the new session.
	// store.Create() only writes metadata to disk - we need to start a BackgroundSession
	// with an actual ACP process so prompts can be executed.
	var bs BackgroundSession
	if s.sessionManager != nil {
		var resumeErr error
		bs, resumeErr = s.sessionManager.ResumeSession(newSessionID, input.Title, sourceMeta.WorkingDir)
		if resumeErr != nil {
			s.logger.Error("Failed to start ACP for new conversation",
				"session_id", newSessionID,
				"error", resumeErr)
			// Session was created but ACP failed to start - clean up isn't needed
			// since the session can be resumed later, but log the error
		}
	}

	// Broadcast session creation to all global events clients
	// This ensures the sidebar updates immediately when creating via MCP
	if s.sessionManager != nil {
		s.sessionManager.BroadcastSessionCreated(
			newSessionID,
			input.Title,
			acpServerName,
			sourceMeta.WorkingDir,
			realSessionID, // parent_session_id
		)
	}

	// Build unified conversation details
	output := ConversationStartOutput{
		ConversationDetails: s.buildConversationDetails(createdMeta, store.SessionDir(newSessionID)),
	}
	// Update runtime status to reflect the running ACP session
	if bs != nil {
		output.IsRunning = true
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

			// Try to process the queued message immediately if agent is idle
			if bs != nil {
				go bs.TryProcessQueuedMessage()
			}
		}
	}

	return nil, output, nil
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
	realSessionID := s.resolveSelfIDWithMCP(input.SelfID, req)
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
	realSessionID := s.resolveSelfIDWithMCP(input.SelfID, req)
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
	meta, err := store.GetMetadata(input.ConversationID)
	if err != nil {
		return nil, SetPeriodicOutput{
			Success: false,
			Error:   fmt.Sprintf("conversation not found: %s", input.ConversationID),
		}, nil
	}

	// Prevent setting periodic on child sessions
	if meta.ParentSessionID != "" {
		return nil, SetPeriodicOutput{
			Success: false,
			Error:   "cannot set periodic on a child conversation; only parent or top-level conversations can be periodic",
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
	realSessionID := s.resolveSelfIDWithMCP(input.SelfID, req)
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

	// When archiving a child session, delegate to handleDeleteConversation (which enforces parent-only permission)
	if archived && meta.ParentSessionID != "" {
		if meta.ParentSessionID != realSessionID {
			return nil, ArchiveConversationOutput{
				Success: false,
				Error:   "permission denied: only the parent can archive/delete a child conversation",
			}, nil
		}
		_, deleteOut, err := s.handleDeleteConversation(ctx, req, DeleteConversationInput{
			SelfID:         input.SelfID,
			ConversationID: input.ConversationID,
		})
		if err != nil {
			return nil, ArchiveConversationOutput{Success: false, Error: err.Error()}, nil
		}
		return nil, ArchiveConversationOutput{
			Success:        deleteOut.Success,
			ConversationID: deleteOut.ConversationID,
			Archived:       deleteOut.Success,
			Error:          deleteOut.Error,
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

	// Broadcast the archived state change to all connected WebSocket clients
	if s.sessionManager != nil {
		s.sessionManager.BroadcastSessionArchived(input.ConversationID, archived)
	}

	// Delete all child sessions when parent is archived
	if archived && s.sessionManager != nil {
		go s.sessionManager.DeleteChildSessions(input.ConversationID)
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

func (s *Server) handleDeleteConversation(ctx context.Context, req *mcp.CallToolRequest, input DeleteConversationInput) (*mcp.CallToolResult, DeleteConversationOutput, error) {
	// Validate self_id
	if input.SelfID == "" {
		return nil, DeleteConversationOutput{Success: false, Error: "self_id is required"}, nil
	}

	// Validate conversation_id
	if input.ConversationID == "" {
		return nil, DeleteConversationOutput{Success: false, Error: "conversation_id is required"}, nil
	}

	// Resolve the self_id to a real session ID
	realSessionID := s.resolveSelfIDWithMCP(input.SelfID, req)
	if realSessionID == "" {
		return nil, DeleteConversationOutput{
			Success: false,
			Error:   fmt.Sprintf("session not found: the self_id '%s' could not be resolved", input.SelfID),
		}, nil
	}

	// Check if source session is registered (must be running)
	reg := s.getSession(realSessionID)
	if reg == nil {
		return nil, DeleteConversationOutput{
			Success: false,
			Error:   fmt.Sprintf("session not found or not running: %s", realSessionID),
		}, nil
	}

	s.mu.RLock()
	store := s.store
	sessionManager := s.sessionManager
	s.mu.RUnlock()

	if store == nil {
		return nil, DeleteConversationOutput{Success: false, Error: "session store not available"}, nil
	}

	// Verify target conversation exists
	meta, err := store.GetMetadata(input.ConversationID)
	if err != nil {
		return nil, DeleteConversationOutput{
			Success: false,
			Error:   fmt.Sprintf("conversation not found: %s", input.ConversationID),
		}, nil
	}

	// Security check: caller must be the parent of the target conversation
	if meta.ParentSessionID != realSessionID {
		return nil, DeleteConversationOutput{
			Success: false,
			Error:   "permission denied: can only delete your own child conversations",
		}, nil
	}

	// Gracefully stop the child if it's running
	if sessionManager != nil {
		reason := "deleted_by_parent_via_mcp"
		if !sessionManager.CloseSessionGracefully(input.ConversationID, reason, archiveWaitTimeout) {
			s.logger.Warn("Timeout waiting for response before deleting child via MCP, proceeding anyway",
				"parent_session", realSessionID,
				"child_session", input.ConversationID)
			sessionManager.CloseSession(input.ConversationID, "deleted_by_parent_timeout_via_mcp")
		}
	}

	// Archive the conversation (mark as read-only rather than permanently deleting)
	err = store.UpdateMetadata(input.ConversationID, func(m *session.Metadata) {
		m.Archived = true
		m.ArchivedAt = time.Now()
	})
	if err != nil {
		return nil, DeleteConversationOutput{
			Success: false,
			Error:   fmt.Sprintf("failed to archive conversation: %v", err),
		}, nil
	}

	// Broadcast the archived state change to all connected WebSocket clients
	if s.sessionManager != nil {
		s.sessionManager.BroadcastSessionArchived(input.ConversationID, true)
	}

	s.logger.Info("Child conversation deleted (archived) by parent via MCP",
		"parent_session", realSessionID,
		"child_session", input.ConversationID)

	return nil, DeleteConversationOutput{
		Success:        true,
		ConversationID: input.ConversationID,
	}, nil
}

// =============================================================================
// Parent-Child Task Coordination Handlers
// =============================================================================

// =============================================================================
// Conversation Wait
// =============================================================================

// defaultConversationWaitTimeout is the default timeout for mitto_conversation_wait.
const defaultConversationWaitTimeout = 10 * time.Minute

// waitConditionAgentResponded is the "what" value for waiting until the agent finishes responding.
const waitConditionAgentResponded = "agent_responded"

func (s *Server) handleConversationWait(ctx context.Context, req *mcp.CallToolRequest, input ConversationWaitInput) (*mcp.CallToolResult, ConversationWaitOutput, error) {
	// Validate self_id
	if input.SelfID == "" {
		return nil, ConversationWaitOutput{Error: "self_id is required"}, nil
	}

	// Validate conversation_id
	if input.ConversationID == "" {
		return nil, ConversationWaitOutput{Error: "conversation_id is required"}, nil
	}

	// Validate "what" parameter
	if input.What == "" {
		return nil, ConversationWaitOutput{Error: "what is required"}, nil
	}
	if input.What != waitConditionAgentResponded {
		return nil, ConversationWaitOutput{
			Error: fmt.Sprintf("unsupported wait condition: %q (supported: %q)", input.What, waitConditionAgentResponded),
		}, nil
	}

	// Resolve the self_id to a real session ID
	realSessionID := s.resolveSelfIDWithMCP(input.SelfID, req)
	if realSessionID == "" {
		return nil, ConversationWaitOutput{
			Error: fmt.Sprintf("session not found: the self_id '%s' could not be resolved", input.SelfID),
		}, nil
	}

	// Check if source session is registered (must be running to use this tool)
	reg := s.getSession(realSessionID)
	if reg == nil {
		return nil, ConversationWaitOutput{
			Error: fmt.Sprintf("session not found or not running: %s", realSessionID),
		}, nil
	}

	// Get the target session via SessionManager
	if s.sessionManager == nil {
		return nil, ConversationWaitOutput{Error: "session manager not available"}, nil
	}
	targetBS := s.sessionManager.GetSession(input.ConversationID)
	if targetBS == nil {
		return nil, ConversationWaitOutput{
			Error: fmt.Sprintf("target conversation not running: %s", input.ConversationID),
		}, nil
	}

	// If the agent is not currently responding, return immediately
	if !targetBS.IsPrompting() {
		s.logger.Debug("Conversation wait: agent not prompting, returning immediately",
			"source_session", realSessionID,
			"target_conversation", input.ConversationID,
			"what", input.What)
		return nil, ConversationWaitOutput{
			Success: true,
			What:    input.What,
		}, nil
	}

	// Determine timeout
	timeout := time.Duration(input.TimeoutSeconds) * time.Second
	if timeout <= 0 {
		timeout = defaultConversationWaitTimeout
	}

	s.logger.Info("Waiting for conversation condition",
		"source_session", realSessionID,
		"target_conversation", input.ConversationID,
		"what", input.What,
		"timeout", timeout)

	// Wait for the agent to finish responding, respecting context cancellation.
	// WaitForResponseComplete blocks with its own timeout, but we also need to
	// handle ctx.Done() for MCP-level cancellation.
	done := make(chan bool, 1)
	go func() {
		done <- targetBS.WaitForResponseComplete(timeout)
	}()

	select {
	case completed := <-done:
		if completed {
			s.logger.Info("Conversation wait condition met",
				"source_session", realSessionID,
				"target_conversation", input.ConversationID,
				"what", input.What)
			return nil, ConversationWaitOutput{
				Success: true,
				What:    input.What,
			}, nil
		}
		// Timed out
		s.logger.Warn("Conversation wait timed out",
			"source_session", realSessionID,
			"target_conversation", input.ConversationID,
			"what", input.What,
			"timeout", timeout)
		return nil, ConversationWaitOutput{
			Success:  true,
			What:     input.What,
			TimedOut: true,
		}, nil
	case <-ctx.Done():
		return nil, ConversationWaitOutput{
			Error: "context cancelled while waiting",
		}, nil
	}
}

// =============================================================================
// Children Tasks Coordination
// =============================================================================

// defaultChildrenTasksTimeout is the default timeout for waiting for children to report.
const defaultChildrenTasksTimeout = 10 * time.Minute

// Report size limits. These prevent MCP protocol validation failures when the
// parent aggregates multiple children's reports into a single tool result.
// The MCP tool result must fit within the agent's protocol message size limits.
const (
	maxReportSummaryBytes = 8000  // ~8 KB for summary (concise findings)
	maxReportDetailsBytes = 16000 // ~16 KB for details (supporting information)
)

// childrenReportSuffix is appended to the prompt sent to each child,
// instructing them to call mitto_children_tasks_report.
// The %s placeholder is replaced with the task_id instruction (or empty string if no task_id).
const childrenReportSuffix = "\n\n" +
	"IMPORTANT: you must report your results when you are done. " + "\n" +
	"Report what you did and any recommended follow-ups (if any). " + "\n" +
	"Keep your report concise: focus on key conclusions and actions rather than exhaustive details. " + "\n" +
	"Use the `mitto_children_tasks_report` MCP tool with: " + "\n" +
	"1) your `self_id`, " + "\n" +
	"2) a `status` (e.g. \"completed\", \"in_progress\", \"failed\"), " + "\n" +
	"3) a `summary` of your findings/changes/conclusions (max ~8KB), " + "\n" +
	"4) OPTIONALLY some `details` with additional information (max ~16KB). " + "\n" +
	"%s " + "\n" +
	"NOTE: ignore these instructions if you have already sent the report."

func (s *Server) handleChildrenTasksWait(ctx context.Context, req *mcp.CallToolRequest, input ChildrenTasksWaitInput) (*mcp.CallToolResult, ChildrenTasksWaitOutput, error) {
	// Validate self_id
	if input.SelfID == "" {
		return nil, ChildrenTasksWaitOutput{Success: false, Error: "self_id is required"}, nil
	}

	// Validate children_list
	if len(input.ChildrenList) == 0 {
		return nil, ChildrenTasksWaitOutput{Success: false, Error: "children_list must contain at least one child conversation ID"}, nil
	}

	// Resolve the self_id to a real session ID (parent)
	realSessionID := s.resolveSelfIDWithMCP(input.SelfID, req)
	if realSessionID == "" {
		return nil, ChildrenTasksWaitOutput{
			Success: false,
			Error:   fmt.Sprintf("session not found: the self_id '%s' could not be resolved", input.SelfID),
		}, nil
	}

	// Check if source session is registered
	reg := s.getSession(realSessionID)
	if reg == nil {
		return nil, ChildrenTasksWaitOutput{
			Success: false,
			Error:   fmt.Sprintf("session not found or not running: %s", realSessionID),
		}, nil
	}

	// Permission check: requires can_send_prompt (we are sending prompts to children)
	if !s.checkSessionFlag(realSessionID, session.FlagCanSendPrompt) {
		return nil, ChildrenTasksWaitOutput{
			Success: false,
			Error:   fmt.Sprintf("tool 'mitto_children_tasks_wait' requires the 'Can Send Prompt' (%s) flag to be enabled in Advanced Settings", session.FlagCanSendPrompt),
		}, nil
	}

	s.mu.RLock()
	store := s.store
	s.mu.RUnlock()

	if store == nil {
		return nil, ChildrenTasksWaitOutput{Success: false, Error: "session store not available"}, nil
	}

	// Validate each child exists and is actually a child of this parent.
	// Also check if each child is currently running.
	validChildren := make([]string, 0, len(input.ChildrenList))
	runningChildren := make([]string, 0, len(input.ChildrenList))
	notRunningChildren := make([]string, 0)
	var warnings []string

	for _, childID := range input.ChildrenList {
		childMeta, err := store.GetMetadata(childID)
		if err != nil {
			s.logger.Warn("Child conversation not found, skipping",
				"parent_session", realSessionID,
				"child_session", childID,
				"error", err)
			continue
		}
		if childMeta.ParentSessionID != realSessionID {
			s.logger.Warn("Conversation is not a child of this parent, skipping",
				"parent_session", realSessionID,
				"child_session", childID,
				"actual_parent", childMeta.ParentSessionID)
			continue
		}
		validChildren = append(validChildren, childID)

		// Check if the child is currently running (registered with MCP server).
		// If not running and not archived, try to auto-resume it.
		childReg := s.getSession(childID)
		if childReg == nil && !childMeta.Archived && s.sessionManager != nil {
			// Session is stored (not running) — try to resume it.
			s.logger.Info("Auto-resuming stored child session",
				"parent_session", realSessionID,
				"child_session", childID)
			resumed, resumeErr := s.sessionManager.ResumeSession(childID, childMeta.Name, childMeta.WorkingDir)
			if resumeErr != nil {
				s.logger.Warn("Failed to auto-resume child session",
					"parent_session", realSessionID,
					"child_session", childID,
					"error", resumeErr)
			} else if resumed != nil {
				// Re-check registration after resume
				childReg = s.getSession(childID)
			}
		}
		if childReg == nil {
			notRunningChildren = append(notRunningChildren, childID)
			reason := "not running"
			if childMeta.Archived {
				reason = "archived"
			}
			warnings = append(warnings, fmt.Sprintf("child %s is %s and cannot process prompts", childID, reason))
			s.logger.Warn("Child conversation is not running",
				"parent_session", realSessionID,
				"child_session", childID,
				"archived", childMeta.Archived)
		} else {
			runningChildren = append(runningChildren, childID)
		}
	}

	if len(validChildren) == 0 {
		return nil, ChildrenTasksWaitOutput{
			Success: false,
			Error:   "none of the provided conversation IDs are valid children of this session",
		}, nil
	}

	// Get-or-create the persistent child report collector for this parent.
	collector := s.getOrCreateCollector(realSessionID)

	// Server-side safeguard: auto-report children that have been waited on for too long.
	// This prevents the AI agent from retrying indefinitely when a child is stuck.
	// We inject a synthetic "stuck" report so that startWait sees them as completed.
	stuckChildren := collector.getStuckChildren()
	for _, childID := range stuckChildren {
		s.logger.Warn("Child session considered stuck after prolonged cumulative wait — auto-reporting as stuck",
			"parent_session", realSessionID,
			"child_session", childID,
			"max_wait", maxChildWaitDuration)
		collector.addReport(childID, input.TaskID, json.RawMessage(`{"status":"stuck","summary":"Child session did not report after 30 minutes of cumulative waiting. The child may be unresponsive. Consider archiving this session."}`))
	}

	// If ALL valid children are not running, return immediately with not_running status.
	// We still register them in the collector for record-keeping.
	if len(runningChildren) == 0 {
		reports := make(map[string]ChildReportInfo, len(notRunningChildren))
		for _, childID := range notRunningChildren {
			reason := "session_closed"
			if childMeta, err := store.GetMetadata(childID); err == nil && childMeta.Archived {
				reason = "archived"
			}
			reports[childID] = ChildReportInfo{
				Completed: false,
				Status:    "not_running",
				Reason:    reason,
			}
		}
		return nil, ChildrenTasksWaitOutput{
			Success:  true,
			Reports:  reports,
			Warnings: warnings,
		}, nil
	}

	// Set up wait signaling. startWait only clears reports when the task_id
	// changes, preserving reports from the same task across retries.
	waitCh, _ := collector.startWait(input.TaskID, runningChildren)
	defer collector.clearWait()

	// Build the prompt to send to all running children.
	// If no prompt is provided, skip sending entirely (wait-only mode).
	// This allows callers to retry waits without re-enqueuing duplicate messages.
	promptText := input.Prompt
	sendPrompt := promptText != ""

	if sendPrompt {
		taskIDInstruction := ""
		if input.TaskID != "" {
			taskIDInstruction = fmt.Sprintf("5) the `task_id: \"%s\"` is mandatory", input.TaskID)
		}
		promptText += fmt.Sprintf(childrenReportSuffix, taskIDInstruction)
	}

	// Send prompt to running children (unless wait-only mode)
	if sendPrompt {
		for _, childID := range runningChildren {
			queue := store.Queue(childID)

			// Dedup: skip if there's already a pending message from this parent in the child's queue.
			// This prevents duplicate report-request messages from accumulating when the parent
			// retries after a timeout and the child hasn't consumed the previous message yet.
			existingMsgs, _ := queue.List()
			alreadyQueued := false
			for _, m := range existingMsgs {
				if m.ClientID == realSessionID {
					alreadyQueued = true
					break
				}
			}
			if alreadyQueued {
				s.logger.Debug("Skipping duplicate prompt — parent already has a pending message in child's queue",
					"parent_session", realSessionID,
					"child_session", childID)
				continue
			}

			msg, err := queue.Add(promptText, nil, nil, realSessionID, 0)
			if err != nil {
				s.logger.Warn("Failed to enqueue prompt to child",
					"parent_session", realSessionID,
					"child_session", childID,
					"error", err)
				continue
			}

			s.logger.Info("Progress inquiry sent to child",
				"parent_session", realSessionID,
				"child_session", childID,
				"message_id", msg.ID)

			// Try to process the queued message immediately if agent is idle
			if s.sessionManager != nil {
				if bs := s.sessionManager.GetSession(childID); bs != nil {
					go bs.TryProcessQueuedMessage()
				}
			}
		}
	}

	// Determine timeout
	timeout := time.Duration(input.TimeoutSeconds) * time.Second
	if timeout <= 0 {
		timeout = defaultChildrenTasksTimeout
	}

	// Broadcast that this parent is now waiting for children
	if s.sessionManager != nil {
		s.sessionManager.BroadcastWaitingForChildren(realSessionID, true)
		defer func() {
			s.sessionManager.BroadcastWaitingForChildren(realSessionID, false)
		}()
	}

	// Block until all running children report or timeout
	s.logger.Info("Waiting for children to report",
		"parent_session", realSessionID,
		"task_id", input.TaskID,
		"running_children", len(runningChildren),
		"not_running_children", len(notRunningChildren),
		"timeout", timeout)

	var timedOut bool

	// childIdlePollInterval is how often we check if pending children are still responsive.
	const childIdlePollInterval = 5 * time.Second
	// childIdleGracePeriod is how long a child must be idle (not prompting) before
	// we consider it done without response. This accounts for:
	// - Time for the child to pick up a queued message
	// - Time for the agent to process and call the report tool
	const childIdleGracePeriod = 15 * time.Second

	if s.sessionManager != nil {
		// Polling loop: check child agent status periodically
		pollTicker := time.NewTicker(childIdlePollInterval)
		defer pollTicker.Stop()

		timeoutTimer := time.NewTimer(timeout)
		defer timeoutTimer.Stop()

		// Track when each child was first seen idle (not prompting)
		childIdleSince := make(map[string]time.Time)

	waitLoop:
		for {
			select {
			case <-waitCh:
				// All children reported or auto-completed
				break waitLoop
			case <-timeoutTimer.C:
				timedOut = true
				pendingChildren, reportedChildren := collector.getPendingAndReported()
				s.logger.Warn("Timeout waiting for children to report",
					"parent_session", realSessionID,
					"pending_children", pendingChildren,
					"reported_children", reportedChildren,
					"total_running", len(runningChildren),
					"timeout", timeout)
				break waitLoop
			case <-ctx.Done():
				return nil, ChildrenTasksWaitOutput{
					Success: false,
					Error:   "context cancelled while waiting for children to report",
				}, nil
			case <-pollTicker.C:
				// Check status of pending children
				pending, _ := collector.getPendingAndReported()
				for _, childID := range pending {
					bs := s.sessionManager.GetSession(childID)
					if bs == nil {
						// Session is no longer running — auto-complete
						s.logger.Info("Child session stopped while waiting — auto-completing",
							"parent_session", realSessionID,
							"child_session", childID)
						collector.markChildAutoCompleted(childID, "session_stopped")
						delete(childIdleSince, childID)
						continue
					}

					if bs.IsPrompting() {
						// Child is actively processing — reset idle timer
						delete(childIdleSince, childID)
						continue
					}

					// Child is running but idle (not prompting)
					if idleSince, exists := childIdleSince[childID]; exists {
						if time.Since(idleSince) > childIdleGracePeriod {
							// Been idle too long without reporting — auto-complete
							s.logger.Info("Child agent idle without reporting — auto-completing",
								"parent_session", realSessionID,
								"child_session", childID,
								"idle_duration", time.Since(idleSince).Round(time.Second))
							collector.markChildAutoCompleted(childID, "agent_idle")
							delete(childIdleSince, childID)
						}
					} else {
						// First time seeing this child idle — start tracking
						childIdleSince[childID] = time.Now()
					}
				}
			}
		}
	} else {
		// No session manager available — fall back to simple wait (original behavior)
		select {
		case <-waitCh:
			// All running children reported
		case <-time.After(timeout):
			timedOut = true
			pendingChildren, reportedChildren := collector.getPendingAndReported()
			s.logger.Warn("Timeout waiting for children to report",
				"parent_session", realSessionID,
				"pending_children", pendingChildren,
				"reported_children", reportedChildren,
				"total_running", len(runningChildren),
				"timeout", timeout)
		case <-ctx.Done():
			return nil, ChildrenTasksWaitOutput{
				Success: false,
				Error:   "context cancelled while waiting for children to report",
			}, nil
		}
	}

	// Build the output with whatever reports we have
	reports := make(map[string]ChildReportInfo, len(validChildren))

	// Add reports from running children (from collector)
	collector.mu.Lock()
	for _, childID := range runningChildren {
		report := collector.reports[childID]
		info := ChildReportInfo{Completed: false, Status: "pending"}
		if report != nil && report.Completed {
			if report.AutoCompleted {
				// Auto-completed: agent went idle without reporting
				info.Completed = false
				info.Status = "agent_not_responding"
				info.Reason = report.AutoReason
				if !report.Timestamp.IsZero() {
					info.Timestamp = report.Timestamp.Format("2006-01-02T15:04:05Z07:00")
				}
			} else {
				info.Completed = true
				info.Status = "completed"
				// Unmarshal the raw JSON report into the typed struct for proper schema validation
				if len(report.Report) > 0 {
					var reportData ChildReportData
					if err := json.Unmarshal(report.Report, &reportData); err != nil {
						s.logger.Warn("Failed to unmarshal child report data",
							"child_session", childID,
							"error", err)
					} else {
						info.Report = &reportData
					}
				}
				if !report.Timestamp.IsZero() {
					info.Timestamp = report.Timestamp.Format("2006-01-02T15:04:05Z07:00")
				}
			}
		} else if timedOut {
			// Add diagnostic reason for timed-out children
			if childReg := s.getSession(childID); childReg == nil {
				info.Reason = "session_unregistered"
			} else if s.sessionManager != nil {
				if bs := s.sessionManager.GetSession(childID); bs != nil && bs.IsPrompting() {
					info.Reason = "still_processing"
				} else {
					info.Reason = "no_report_received"
				}
			} else {
				info.Reason = "no_report_received"
			}
		}
		reports[childID] = info
	}
	collector.mu.Unlock()

	// Add not-running children to reports with diagnostic reason
	for _, childID := range notRunningChildren {
		reason := "session_closed"
		if childMeta, err := store.GetMetadata(childID); err == nil && childMeta.Archived {
			reason = "archived"
		}
		reports[childID] = ChildReportInfo{
			Completed: false,
			Status:    "not_running",
			Reason:    reason,
		}
	}

	return nil, ChildrenTasksWaitOutput{
		Success:  true,
		Reports:  reports,
		TimedOut: timedOut,
		Warnings: warnings,
	}, nil
}

func (s *Server) handleChildrenTasksReport(ctx context.Context, req *mcp.CallToolRequest, input ChildrenTasksReportInput) (*mcp.CallToolResult, ChildrenTasksReportOutput, error) {
	// Validate self_id
	if input.SelfID == "" {
		return nil, ChildrenTasksReportOutput{Success: false, Error: "self_id is required"}, nil
	}

	// Validate report fields
	if input.Status == "" {
		return nil, ChildrenTasksReportOutput{Success: false, Error: "status is required"}, nil
	}
	if input.Summary == "" {
		return nil, ChildrenTasksReportOutput{Success: false, Error: "summary is required"}, nil
	}

	// Enforce size limits to prevent MCP protocol validation failures when the
	// parent aggregates multiple children's reports into a single tool result.
	if len(input.Summary) > maxReportSummaryBytes {
		return nil, ChildrenTasksReportOutput{
			Success: false,
			Error: fmt.Sprintf(
				"summary is too long (%d bytes, max %d). Please shorten your summary to the key findings and re-submit. "+
					"Focus on conclusions rather than exhaustive details — you can put extra information in the 'details' field.",
				len(input.Summary), maxReportSummaryBytes),
		}, nil
	}
	if len(input.Details) > maxReportDetailsBytes {
		return nil, ChildrenTasksReportOutput{
			Success: false,
			Error: fmt.Sprintf(
				"details is too long (%d bytes, max %d). Please condense your details and re-submit. "+
					"Keep only the most important information — the parent can always query you for more context later.",
				len(input.Details), maxReportDetailsBytes),
		}, nil
	}

	// Serialize the report fields into JSON for internal storage
	reportJSON, err := json.Marshal(map[string]string{
		"status":  input.Status,
		"summary": input.Summary,
		"details": input.Details,
	})
	if err != nil {
		return nil, ChildrenTasksReportOutput{Success: false, Error: fmt.Sprintf("failed to serialize report: %v", err)}, nil
	}

	// Resolve the self_id to a real session ID (child)
	realSessionID := s.resolveSelfIDWithMCP(input.SelfID, req)
	if realSessionID == "" {
		return nil, ChildrenTasksReportOutput{
			Success: false,
			Error:   fmt.Sprintf("session not found: the self_id '%s' could not be resolved", input.SelfID),
		}, nil
	}

	// Check if session is registered
	reg := s.getSession(realSessionID)
	if reg == nil {
		return nil, ChildrenTasksReportOutput{
			Success: false,
			Error:   fmt.Sprintf("session not found or not running: %s", realSessionID),
		}, nil
	}

	s.mu.RLock()
	store := s.store
	s.mu.RUnlock()

	if store == nil {
		return nil, ChildrenTasksReportOutput{Success: false, Error: "session store not available"}, nil
	}

	// Look up child's metadata to find parent
	childMeta, err := store.GetMetadata(realSessionID)
	if err != nil {
		return nil, ChildrenTasksReportOutput{
			Success: false,
			Error:   fmt.Sprintf("failed to get session metadata: %v", err),
		}, nil
	}

	parentSessionID := childMeta.ParentSessionID
	if parentSessionID == "" {
		return nil, ChildrenTasksReportOutput{
			Success: false,
			Error:   "this session has no parent session - only child conversations can report back",
		}, nil
	}

	// Get-or-create the persistent collector for the parent.
	// This ensures reports are stored even if the parent hasn't called _wait yet.
	collector := s.getOrCreateCollector(parentSessionID)

	// Store the report (may also signal a waiting parent)
	collector.addReport(realSessionID, input.TaskID, json.RawMessage(reportJSON))

	// Detect orphaned reports: parent unregistered or not actively waiting
	parentReg := s.getSession(parentSessionID)
	if parentReg == nil {
		s.logger.Warn("Child reported to unregistered parent session — report is orphaned",
			"child_session", realSessionID,
			"parent_session", parentSessionID)
	} else if !collector.isWaiting() {
		s.logger.Info("Child reported to parent (no active wait — report stored for next wait cycle)",
			"child_session", realSessionID,
			"parent_session", parentSessionID)
	} else {
		s.logger.Info("Child reported to parent",
			"child_session", realSessionID,
			"parent_session", parentSessionID)
	}

	return nil, ChildrenTasksReportOutput{
		Success:         true,
		ParentSessionID: parentSessionID,
	}, nil
}
