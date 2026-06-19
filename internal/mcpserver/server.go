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
	"path/filepath"
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
	promptsCache   *config.PromptsCache
	sessionManager SessionManager
	periodicRunner PeriodicRunner // Optional — for triggering periodic runs via MCP
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
	// PromptsCache provides cached access to global prompts from MITTO_DIR/prompts/.
	// If nil, global file prompts are not loaded.
	PromptsCache *config.PromptsCache
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
	BroadcastSessionCreated(sessionID, name, acpServer, workingDir, parentSessionID, childOrigin string)
	// BroadcastSessionArchived broadcasts a session_archived event to all connected clients.
	BroadcastSessionArchived(sessionID string, archived bool, reason ...session.ArchiveReason)
	// BroadcastSessionDeleted broadcasts a session_deleted event to all connected clients.
	BroadcastSessionDeleted(sessionID string)
	// BroadcastWaitingForChildren broadcasts a session_waiting event to all connected clients.
	BroadcastWaitingForChildren(sessionID string, isWaiting bool)
	// DeleteChildSessions permanently deletes all child sessions when a parent is archived.
	DeleteChildSessions(parentID string)
	// GetWorkspaces returns all configured workspaces.
	GetWorkspaces() []config.WorkspaceSettings
	// GetWorkspaceByUUID returns the workspace with the given UUID.
	// Returns nil if no workspace with that UUID exists.
	GetWorkspaceByUUID(uuid string) *config.WorkspaceSettings
	// BroadcastSessionRenamed broadcasts a session_renamed event to all connected clients.
	BroadcastSessionRenamed(sessionID string, newName string)
	// BroadcastPeriodicUpdated broadcasts a periodic_updated event to all connected clients.
	BroadcastPeriodicUpdated(sessionID string, periodic *session.PeriodicPrompt)
	// GetUserDataSchema returns the user data schema for a workspace.
	GetUserDataSchema(workingDir string) *config.UserDataSchema
	// GetWorkspacePrompts returns prompts defined in the workspace's .mittorc file.
	GetWorkspacePrompts(workingDir string) []config.WebPrompt
	// GetWorkspacePromptsDirs returns the prompts_dirs defined in the workspace's .mittorc file.
	GetWorkspacePromptsDirs(workingDir string) []string
	// GetWorkspaceRCLastModified returns the last modification time of the workspace's .mittorc file.
	GetWorkspaceRCLastModified(workingDir string) time.Time
	// GetWorkspace returns the first workspace matching the working directory.
	GetWorkspace(workingDir string) *config.WorkspaceSettings
	// InvalidateWorkspaceRC clears the cached .mittorc for a workspace dir.
	InvalidateWorkspaceRC(workingDir string)
}

// PeriodicRunner interface for triggering immediate periodic prompt delivery.
type PeriodicRunner interface {
	TriggerNow(sessionID string, resetTimer bool) error
	// BootstrapOnCompletion delivers the very first run of a fresh onCompletion
	// periodic conversation (IterationCount==0, LastSentAt==nil). No-op otherwise.
	BootstrapOnCompletion(sessionID string)
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
	// TriggerTitleGeneration triggers async title generation if the session has no title yet.
	// Used by MCP tools and API handlers to generate titles for sessions that received
	// prompts via paths that don't normally trigger title generation (e.g., periodic config).
	TriggerTitleGeneration(message string)
	// TriggerTitleGenerationFromPeriodic picks the best source text (prompt text or prompt
	// name) for title generation when a periodic config is saved.
	TriggerTitleGenerationFromPeriodic(prompt, promptName string)
	// RequestSelfDestruct marks the conversation for deletion once the current turn
	// completes. Used by the mitto_conversation_delete tool when an agent requests
	// deletion of its own conversation.
	RequestSelfDestruct()
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
		promptsCache:          deps.PromptsCache,
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
	if deps.PromptsCache != nil {
		s.promptsCache = deps.PromptsCache
	}
}

// periodicDelayFloor returns the configured global floor for the on-completion periodic
// delay. Falls back to the package default when no config is available.
func (s *Server) periodicDelayFloor() int {
	if s.config != nil {
		return s.config.Conversations.GetMinPeriodicCompletionDelaySeconds()
	}
	return config.DefaultMinPeriodicCompletionDelaySeconds
}

// SetPeriodicRunner sets the periodic runner for triggering periodic runs via MCP tools.
// It may be called after NewServer since the periodic runner is created after the MCP server.
func (s *Server) SetPeriodicRunner(runner PeriodicRunner) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.periodicRunner = runner
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

// resolveSelfIDWithMCP resolves self_id using a three-phase lookup, in this order:
//  1. Direct lookup: If inputSessionID matches a registered session, return immediately.
//  2. MCP session cache: If req carries an MCP session ID cached from a prior get_current,
//     return the cached Mitto session immediately — avoids the 5s wait for repeat calls.
//  3. Correlation lookup: Wait up to pendingRequestTimeout for the ACP layer to register
//     a mapping. Needed for the genuine first get_current correlation race.
//
// Phase 2 (cache) is intentionally placed before Phase 3 (wait) so that repeat calls
// from the same MCP client resolve instantly instead of stalling for 5 seconds.
//
// Returns the resolved session ID, or empty string if resolution fails.
func (s *Server) resolveSelfIDWithMCP(inputSessionID string, req *mcp.CallToolRequest) string {
	if inputSessionID == "" {
		return ""
	}

	// Phase 1: Direct lookup - check if inputSessionID is already a registered session.
	if reg := s.getSession(inputSessionID); reg != nil {
		s.logger.Debug("Session resolved via direct lookup",
			"input_session_id", inputSessionID,
			"resolved_session_id", inputSessionID)
		return inputSessionID
	}

	// Phase 2 (before Phase 3): MCP session ID cache lookup.
	// After a successful get_current call, the MCP session → Mitto session mapping
	// is cached. Checking this before WaitForPendingRequest avoids the 5s stall
	// for repeat calls from the same MCP client.
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

	// Phase 3: Correlation lookup - wait for ACP layer to register the mapping.
	// This is needed for the genuine first get_current correlation race where the
	// ACP layer intercepts the tool call and registers the session ID mapping.
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

// confirmCrossWorkspaceOperation shows a blocking confirmation dialog to the user
// before allowing a cross-workspace operation. This is a security gate that cannot
// be bypassed — it does NOT require the "can_prompt_user" flag.
//
// Returns nil if the user approves, or an error if denied, timed out, or UI unavailable.
func (s *Server) confirmCrossWorkspaceOperation(
	ctx context.Context,
	callerSessionID string,
	operationDescription string, // e.g., "create a new conversation"
	targetWorkspace *config.WorkspaceSettings,
) error {
	// Get the caller's registered session
	reg := s.getSession(callerSessionID)
	if reg == nil {
		return fmt.Errorf("session not found or not running: %s", callerSessionID)
	}

	// UIPrompter must be available (requires connected UI)
	if reg.uiPrompter == nil {
		return fmt.Errorf("cross-workspace operations require a connected UI (no headless support)")
	}

	// Build human-readable workspace label
	workspaceLabel := targetWorkspace.Name
	if workspaceLabel == "" {
		workspaceLabel = filepath.Base(targetWorkspace.WorkingDir)
	}

	// Get caller's session name for the dialog
	callerName := callerSessionID
	s.mu.RLock()
	store := s.store
	s.mu.RUnlock()
	if store != nil {
		if meta, err := store.GetMetadata(callerSessionID); err == nil && meta.Name != "" {
			callerName = meta.Name
		}
	}

	question := fmt.Sprintf(
		"Conversation %q wants to %s in workspace %q (%s). Allow?",
		callerName,
		operationDescription,
		workspaceLabel,
		targetWorkspace.WorkingDir,
	)

	uiRequestID := uuid.New().String()
	promptReq := UIPromptRequest{
		RequestID: uiRequestID,
		Type:      UIPromptTypeOptions,
		Question:  question,
		Options: []UIPromptOption{
			{ID: "yes", Label: "Yes", Style: UIPromptOptionStyleSuccess},
			{ID: "no", Label: "No", Style: UIPromptOptionStyleDanger},
		},
		TimeoutSeconds: 60,
		Blocking:       true,
	}

	s.logger.Info("Cross-workspace confirmation requested",
		"caller_session", callerSessionID,
		"operation", operationDescription,
		"target_workspace", targetWorkspace.UUID,
		"target_path", targetWorkspace.WorkingDir)

	resp, err := reg.uiPrompter.UIPrompt(ctx, promptReq)
	if err != nil {
		return fmt.Errorf("failed to display confirmation dialog: %w", err)
	}

	if resp.TimedOut {
		return fmt.Errorf("cross-workspace operation timed out waiting for user confirmation")
	}

	if resp.OptionID != "yes" {
		return fmt.Errorf("cross-workspace operation denied by user")
	}

	s.logger.Info("Cross-workspace operation approved by user",
		"caller_session", callerSessionID,
		"operation", operationDescription,
		"target_workspace", targetWorkspace.UUID)

	return nil
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

	// Expected, recoverable fallback: resolution may still succeed via the MCP-session
	// cache (Phase 2 in resolveSelfIDWithMCP) or direct lookup. Do not pollute WARN logs.
	s.logger.Debug("Pending request not found within timeout",
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
		BeadsIssue:      meta.BeadsIssue,
		ACPServer:       meta.ACPServer,
		WorkingDir:      meta.WorkingDir,
		MessageCount:    meta.EventCount,
		Status:          string(meta.Status),
		Archived:        meta.Archived,
		ArchiveReason:   string(meta.ArchiveReason),
		SessionFolder:   sessionFolder,
		ParentSessionID: meta.ParentSessionID,
		ChildOrigin:     string(meta.ChildOrigin),
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

		// Check if conversation has an active periodic prompt
		if p, err := store.Periodic(meta.SessionID).Get(); err == nil && p != nil {
			details.IsPeriodic = p.Enabled
		}

		// Load message queue
		if msgs, err := store.Queue(meta.SessionID).List(); err == nil && len(msgs) > 0 {
			details.QueuedPrompts = make([]QueuedPrompt, 0, len(msgs))
			for _, msg := range msgs {
				qp := QueuedPrompt{
					ID:       msg.ID,
					Message:  truncateForError(msg.Message, 200),
					QueuedAt: msg.QueuedAt.Format("2006-01-02T15:04:05Z07:00"),
					ClientID: msg.ClientID,
					Title:    msg.Title,
				}
				if msg.ScheduledTime != nil {
					qp.ScheduledTime = msg.ScheduledTime.Format("2006-01-02T15:04:05Z07:00")
				}
				details.QueuedPrompts = append(details.QueuedPrompts, qp)
			}
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
			"All parameters are optional filters — omit them to list all conversations. " +
			"Optionally filter by workspace UUID using the 'workspace' parameter to list only conversations in a specific workspace. " +
			"Optionally provide 'self_id' for permission-aware listing: without it, all conversations are returned (backward compatible); " +
			"with 'self_id' but without the 'Can interact with other workspaces' flag, only the caller's own workspace conversations are returned. " +
			selfIDNote,
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

	// mitto_workspace_list tool - always available
	mcp.AddTool(mcpSrv, &mcp.Tool{
		Name: "mitto_workspace_list",
		Description: "List all configured workspaces with their settings and metadata. " +
			"Returns workspace UUID, display name, working directory, ACP server, " +
			"an is_default flag (the preferred workspace for its folder when several share the same directory), " +
			"and optional metadata from the workspace .mittorc file (description, URL, group, user data schema). " +
			"Optionally filter by activity: 'active' returns only workspaces with at least one non-archived conversation, " +
			"'archived' returns only workspaces where all conversations are archived (excludes workspaces with zero conversations). " +
			"Omit filter to return all workspaces. Always available.",
	}, s.createListWorkspacesHandler())

	// mitto_workspace_update tool - always available
	mcp.AddTool(mcpSrv, &mcp.Tool{
		Name: "mitto_workspace_update",
		Description: "Update a workspace's .mittorc configuration: the descriptive metadata (description, url, group) " +
			"and/or the user_data schema (the field definitions for per-conversation user data). " +
			"Supports partial updates — only provided fields change. For description/url/group, omit a field to leave it unchanged, " +
			"or pass an empty string to clear it. " +
			"user_data_schema is a list of {name, description, type} where type is one of 'string' (default), 'url', or 'filename'. " +
			"Set user_data_schema_merge to true (default) to merge fields by name with the existing schema, " +
			"or false to replace the whole schema (an empty list clears it). " +
			"By default targets the caller's own workspace; specify 'workspace' (a UUID from mitto_workspace_list) " +
			"to target another workspace (requires the 'Can interact with other workspaces' flag and user confirmation). " +
			"Note: .mittorc is a version-controlled file in the workspace root. " +
			selfIDNote,
	}, s.handleWorkspaceUpdate)
}

// selfIDNote is the standard note about self_id for tools that require session identification.
// For ACP-routed agents (like Auggie), the self_id is automatically correlated via the ACP layer,
// so any stable value works. For external MCP clients, the real session_id must be discovered first.
const selfIDNote = "The self_id parameter identifies YOUR current session (not the target conversation). " +
	"If your session_id was already provided in the conversation context (e.g., in a '[Session Context]' block), use that value directly — " +
	"do NOT call 'mitto_conversation_get_current' first. " +
	"Only call 'mitto_conversation_get_current' if you do not already know your session_id."

// registerSessionScopedTools registers session-scoped MCP tools.
// These tools operate on specific conversations using automatic session detection via session_id correlation.
// Permission checks are done at execution time based on the session's flags.
func (s *Server) registerSessionScopedTools(mcpSrv *mcp.Server) {
	// mitto_get_current_session - Get info about the current session (auto-detected via session_id)
	mcp.AddTool(mcpSrv, &mcp.Tool{
		Name: "mitto_conversation_get_current",
		Description: "Get information about YOUR current conversation/session, including your real session ID, title, working directory, and message count. " +
			"Only call this if you do NOT already know your session_id (e.g., it was not provided as part of the prompt). " +
			"You can pass any value for self_id (e.g., 'init') - this tool auto-detects your session and returns the real session_id. " +
			selfIDNote,
	}, s.handleGetCurrentSession)

	// mitto_conversation_send_prompt - Send a prompt to another conversation
	mcp.AddTool(mcpSrv, &mcp.Tool{
		Name: "mitto_conversation_send_prompt",
		Description: "Send a message/prompt to an EXISTING conversation (identified by conversation_id). " +
			"The prompt is added to that conversation's queue and will be processed when the target agent becomes idle. " +
			"Use 'mitto_conversation_list' first to find existing conversation IDs, or use an ID returned by 'mitto_conversation_new'. " +
			"Optionally specify a 'workspace' UUID when sending to a conversation in a different workspace (requires user confirmation). " +
			"Optionally provide a 'schedule_time' parameter (ISO 8601 / RFC 3339 timestamp) to schedule the message for future delivery instead of immediate processing. " +
			"Supports both absolute timestamps (e.g., '2024-01-15T10:30:00Z') and relative durations from now (e.g., '5m', '1h', '2h30m'). " +
			"Optionally provide an 'arguments' map (string keys to string values) to substitute bash-like placeholders in the prompt text when it is sent: '${VAR}' is replaced with the value (or empty string if absent), and '${VAR:-default}' uses the value when set and non-empty, otherwise 'default'. Escape with a backslash ('\\${VAR}') to emit a literal placeholder. " +
			"Optionally provide 'prompt_name' to enqueue a predefined workspace prompt by name instead of free text; the name is resolved to its full body at dispatch in the TARGET conversation's context. Provide either 'prompt' (free text) or 'prompt_name'. " +
			"Requires 'Can Send Prompt' flag to be enabled. " +
			selfIDNote,
	}, s.handleSendPromptToConversation)

	// mitto_ui_options - Unified options menu
	mcp.AddTool(mcpSrv, &mcp.Tool{
		Name: "mitto_ui_options",
		Description: "Present a list of options to the user as an expandable menu and wait for their selection. " +
			"Each option can have a short label and an optional longer description. " +
			"Option labels should be short (max 80 characters) and descriptions concise (max 200 characters); longer values will be truncated. " +
			"The question text must be concise (max 500 characters); if you need to provide detailed context, print it as a regular message first. " +
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

	// mitto_ui_form - Present a sanitized HTML form to the user
	mcp.AddTool(mcpSrv, &mcp.Tool{
		Name: "mitto_ui_form",
		Description: "Present an HTML form to the user and wait for their submission. " +
			"Provide simple HTML with form elements (input, select, textarea, checkbox, radio) and labels. " +
			"The HTML is strictly sanitized — only form-related elements are allowed (no scripts, styles, " +
			"images, links, or event handlers). Submit/cancel buttons are added automatically. " +
			"Returns the submitted form field values as key-value pairs (keyed by the 'name' attribute). " +
			"Requires 'Can prompt user' flag to be enabled. " +
			selfIDNote,
	}, s.handleUIForm)

	// mitto_ui_notify - Send a non-blocking notification to the user
	mcp.AddTool(mcpSrv, &mcp.Tool{
		Name: "mitto_ui_notify",
		Description: "Send a notification to the user. Unlike other UI tools, this is non-blocking — " +
			"it sends the notification and returns immediately without waiting for user interaction. " +
			"Useful for informing the user about progress, completion, errors, or other events. " +
			"style can be: 'info' (default, blue), 'success' (green), 'warning' (amber), 'error' (red). " +
			"native=true shows a native OS notification (macOS only) in addition to the in-app toast. " +
			"sound=true plays a notification sound. " +
			"sticky=true keeps the native notification in Notification Center until the user dismisses it (default: false, auto-removes after 5s). " +
			"Requires 'Can prompt user' flag to be enabled. " +
			selfIDNote,
	}, s.handleUINotify)

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
			"Instead of an inline 'initial_prompt', you may provide 'prompt_name' to use a predefined prompt by name (resolved the same way as 'mitto_prompt_get', case-insensitive) as the initial prompt — 'prompt_name' and 'initial_prompt' are mutually exclusive. " +
			"Optionally provide an 'arguments' map (string keys to string values) to substitute bash-like placeholders in the initial prompt when it is sent: '${VAR}' is replaced with the value (or empty string if absent), and '${VAR:-default}' uses the value when set and non-empty, otherwise 'default'. Escape with a backslash ('\\${VAR}') to emit a literal placeholder. This pairs with 'prompt_name' to fill a predefined prompt's parameters without fetching it first. " +
			"Optionally provide 'initial_prompt_delay' to delay the initial prompt delivery instead of sending it immediately. " +
			"Supports both absolute timestamps (e.g., '2024-01-15T10:30:00Z') and relative durations from now (e.g., '5m', '1h', '2h30m'). " +
			"Requires 'initial_prompt' or 'prompt_name' to be set. " +
			"Optionally specify a 'workspace' UUID to create the conversation in a different workspace (requires user confirmation). " +
			"Optionally provide 'beads_issue' to link the new conversation to a beads issue ID (e.g. 'mitto-123'). " +
			"Optionally configure the conversation as periodic by providing 'periodic_prompt', 'periodic_frequency_value', and 'periodic_frequency_unit'. " +
			"This is equivalent to configuring periodic via 'mitto_conversation_update' after creation, but done in one step. " +
			"For periodic with days, optionally specify 'periodic_frequency_at' (HH:MM in UTC). " +
			"Set 'periodic_enabled' to false to create the periodic configuration in a paused state. " +
			"Set 'periodic_fresh_context' to true to start each run with a clean agent context (no history injection, new ACP session). " +
			"Set 'periodic_max_iterations' to limit the number of scheduled runs (0 = unlimited). " +
			"Set 'periodic_trigger' to 'onCompletion' to fire the next run after the agent stops responding (event-driven) instead of on a fixed 'schedule'; onCompletion does not require a frequency. " +
			"For 'onCompletion', set 'periodic_completion_delay_seconds' to the wait after the agent stops (clamped to the global floor). " +
			"Set 'periodic_max_duration_seconds' to auto-stop the conversation after a wall-clock cap since iterating started (0 = unlimited). " +
			"Cannot be used together with 'acp_server'. " +
			"Requires 'Can start conversation' flag to be enabled in Advanced Settings (disabled by default for security). " +
			"Note: Conversations created by this tool cannot spawn further conversations (to prevent infinite recursion). " +
			selfIDNote,
	}, s.handleConversationStart)

	// mitto_conversation_get - Get properties of a specific conversation
	mcp.AddTool(mcpSrv, &mcp.Tool{
		Name: "mitto_conversation_get",
		Description: "Get detailed properties of a specific conversation by conversation_id. " +
			"Returns metadata, status, runtime info including whether the agent is currently replying, " +
			"and the list of queued prompts (with scheduled delivery times, if any). " +
			"Also returns parent-child relationship info (parent_session_id, child_origin). " +
			"Use 'mitto_conversation_list' first to find available conversation IDs. " +
			"Optionally specify a 'workspace' UUID to access a conversation in a different workspace (requires user confirmation). " +
			selfIDNote,
	}, s.handleGetConversation)

	// mitto_conversation_run_periodic_now - Trigger immediate periodic run
	mcp.AddTool(mcpSrv, &mcp.Tool{
		Name: "mitto_conversation_run_periodic_now",
		Description: "Trigger an immediate run of a periodic conversation's configured prompt, bypassing the normal schedule. " +
			"The conversation must have periodic prompts configured and enabled. " +
			"Use 'reset_timer' to control whether the countdown for the next scheduled run resets (default: true). " +
			"When reset_timer is true, the next run is scheduled from now (as if a normal run just occurred). " +
			"When reset_timer is false, the existing next-run schedule is preserved unchanged. " +
			"Use 'mitto_conversation_list' first to find available conversation IDs. " +
			selfIDNote,
	}, s.handleRunPeriodicNow)

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

	// mitto_conversation_delete - Permanently delete a child conversation
	mcp.AddTool(mcpSrv, &mcp.Tool{
		Name: "mitto_conversation_delete",
		Description: "Delete a conversation. " +
			"This permanently deletes the conversation, gracefully stopping any active agent response and closing its ACP connection. " +
			"To delete a CHILD conversation, pass its conversation_id; the caller MUST be the parent of the target conversation (verified via the parent-child relationship). " +
			"To delete YOUR OWN conversation (self-destruct), pass \"self\" (or your own conversation ID) as conversation_id; the deletion happens automatically once your current response finishes. " +
			"Deleted conversations are permanently removed and cannot be recovered. " +
			selfIDNote,
	}, s.handleDeleteConversation)

	// mitto_conversation_update - Update properties of a conversation
	mcp.AddTool(mcpSrv, &mcp.Tool{
		Name: "mitto_conversation_update",
		Description: "Update properties of a conversation. " +
			"Supports partial updates — only specified fields are changed, others are left untouched. " +
			"To update YOUR OWN conversation (e.g. a periodic conversation disabling its own periodicity), " +
			"pass \"self\" (or your own conversation ID) as conversation_id. " +
			"Updatable properties: 'name' (conversation title), 'user_data' (workspace-defined metadata attributes), " +
			"'beads_issue' (linked beads issue ID, e.g. \"mitto-123\"; empty string clears it), " +
			"'periodic' (periodic prompt configuration). " +
			"User data is validated against the workspace's schema defined in .mittorc. " +
			"Set 'user_data_merge' to true (default) to merge with existing attributes, or false to replace all. " +
			"Periodic configuration: provide 'periodic_prompt', 'periodic_frequency_value', and 'periodic_frequency_unit' " +
			"to configure or update periodic prompts. Use 'periodic_frequency_at' (HH:MM UTC) for daily schedules. " +
			"Set 'periodic_enabled' to false to pause periodic execution without deleting the configuration. " +
			"To disable periodic entirely, set 'periodic_enabled' to false. " +
			"Set 'periodic_fresh_context' to true to start each run with a clean agent context (no history injection, new ACP session). " +
			"Set 'periodic_max_iterations' to limit the number of scheduled runs (0 = unlimited). " +
			"Set 'periodic_trigger' to 'onCompletion' (event-driven: fire after the agent stops) or 'schedule' (frequency-based, default); onCompletion does not require a frequency. " +
			"For 'onCompletion', set 'periodic_completion_delay_seconds' to the wait after the agent stops (clamped to the global floor). " +
			"Set 'periodic_max_duration_seconds' to auto-stop the conversation after a wall-clock cap since iterating started (0 = unlimited). " +
			selfIDNote,
	}, s.handleConversationUpdate)

	// mitto_conversation_wait - Wait until something happens in a conversation
	mcp.AddTool(mcpSrv, &mcp.Tool{
		Name: "mitto_conversation_wait",
		Description: "Wait until something happens in a conversation. " +
			"Currently supports: 'agent_responded' — blocks until the agent finishes responding. " +
			"Returns immediately if the condition is already met (e.g., agent is not currently responding). " +
			"Optionally specify a 'workspace' UUID when waiting on a conversation in a different workspace (requires user confirmation). " +
			"If the wait times out, the result includes 'timed_out: true' and 'still_prompting' indicating whether the agent is still responding — you do NOT need to separately check the prompting status. " +
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

	// mitto_conversation_history - Search and retrieve conversation history
	mcp.AddTool(mcpSrv, &mcp.Tool{
		Name: "mitto_conversation_history",
		Description: "Get and search through the conversation history of a session. " +
			"Returns events (user prompts, agent messages, tool calls, etc.) with powerful filtering. " +
			"Useful for recalling past decisions, finding specific tool calls, searching for errors, " +
			"or reviewing what happened in a conversation. " +
			"Filter by time range with 'since' and 'until': both accept an absolute RFC 3339 timestamp " +
			"(e.g., '2024-01-15T10:30:00Z') or a relative duration meaning ago (e.g., '3m', '1h', '2h30m'). " +
			"Defaults to your own conversation if conversation_id is omitted. " +
			selfIDNote,
	}, s.handleConversationHistory)

	// mitto_prompt_list - List all prompts in a workspace
	mcp.AddTool(mcpSrv, &mcp.Tool{
		Name: "mitto_prompt_list",
		Description: "List all prompts available in a workspace, returning basic metadata for each (name, origin/source, enabled status, group, etc.) but NOT the full prompt text. " +
			"This reflects the merged/effective prompt list from all sources (global, settings, ACP-specific, workspace directory, workspace inline). " +
			"Defaults to the caller's workspace. " + selfIDNote,
	}, s.handlePromptList)

	// mitto_prompt_get - Get full details for a specific prompt
	mcp.AddTool(mcpSrv, &mcp.Tool{
		Name: "mitto_prompt_get",
		Description: "Get full details for a specific prompt in a workspace, including the complete prompt text and all metadata (origin, enabled status, group, etc.). " +
			"Prompt name matching is case-insensitive. " + selfIDNote,
	}, s.handlePromptGet)

	// mitto_prompt_update - Update a prompt's details
	mcp.AddTool(mcpSrv, &mcp.Tool{
		Name: "mitto_prompt_update",
		Description: "Update a prompt's details including its full text, description, group, color, and enabled status. " +
			"If the prompt originates from a global source, the update is saved to the workspace-local .mitto/prompts/ folder (creating a workspace-level override). " +
			"If only the enabled field is provided, uses the optimized toggle-enabled logic (updates frontmatter or .mittorc). " +
			"Can also create new prompts by specifying a name that doesn't exist yet. " + selfIDNote,
	}, s.handlePromptUpdate)
}

// ListConversationsOutput wraps the list of conversations for MCP output schema compliance.
type ListConversationsOutput struct {
	Conversations []ConversationInfo `json:"conversations"`
}

// WorkspaceInfo contains information about a single workspace for MCP tool output.
type WorkspaceInfo struct {
	UUID       string                    `json:"uuid"`
	Name       string                    `json:"name,omitempty"`
	WorkingDir string                    `json:"working_dir"`
	ACPServer  string                    `json:"acp_server"`
	IsDefault  bool                      `json:"is_default,omitempty"` // True if this is the default workspace for its folder
	Metadata   *config.WorkspaceMetadata `json:"metadata,omitempty"`
}

// WorkspaceListInput is the input for the mitto_workspace_list tool.
type WorkspaceListInput struct {
	Filter string `json:"filter,omitempty"` // Optional: "active", "archived", or empty for all
}

// WorkspaceListOutput is the output for the mitto_workspace_list tool.
type WorkspaceListOutput struct {
	Workspaces []WorkspaceInfo `json:"workspaces"`
}

// WorkspaceUserDataField is a single field definition for the user_data schema.
type WorkspaceUserDataField struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Type        string `json:"type,omitempty"` // "string" (default), "url", or "filename"
}

// WorkspaceUpdateInput is the input for the mitto_workspace_update tool.
// UserDataSchema uses a plain slice: nil means absent (leave schema untouched);
// a non-nil empty slice (JSON []) means "provided but empty" (clears schema when merge=false).
type WorkspaceUpdateInput struct {
	SelfID              string                   `json:"self_id"`
	Workspace           string                   `json:"workspace,omitempty"`
	Description         *string                  `json:"description,omitempty"`
	URL                 *string                  `json:"url,omitempty"`
	Group               *string                  `json:"group,omitempty"`
	UserDataSchema      []WorkspaceUserDataField `json:"user_data_schema,omitempty"`
	UserDataSchemaMerge *bool                    `json:"user_data_schema_merge,omitempty"`
}

// WorkspaceUpdateOutput is the output for the mitto_workspace_update tool.
type WorkspaceUpdateOutput struct {
	Success       bool                      `json:"success"`
	Error         string                    `json:"error,omitempty"`
	WorkspaceUUID string                    `json:"workspace_uuid,omitempty"`
	WorkingDir    string                    `json:"working_dir,omitempty"`
	Updated       []string                  `json:"updated,omitempty"`
	Metadata      *config.WorkspaceMetadata `json:"metadata,omitempty"`
}

// createListWorkspacesHandler creates the handler for mitto_workspace_list tool.
func (s *Server) createListWorkspacesHandler() mcp.ToolHandlerFor[WorkspaceListInput, WorkspaceListOutput] {
	return func(ctx context.Context, req *mcp.CallToolRequest, input WorkspaceListInput) (*mcp.CallToolResult, WorkspaceListOutput, error) {
		s.mu.RLock()
		sm := s.sessionManager
		s.mu.RUnlock()

		if sm == nil {
			return nil, WorkspaceListOutput{}, fmt.Errorf("session manager not available")
		}

		// Build workspace activity map if filtering is requested
		var wsHasNonArchived map[string]bool // workingDir → has at least one non-archived session
		var wsHasAnySessions map[string]bool // workingDir → has at least one session (any state)
		if input.Filter == "active" || input.Filter == "archived" {
			s.mu.RLock()
			store := s.store
			s.mu.RUnlock()

			if store == nil {
				return nil, WorkspaceListOutput{}, fmt.Errorf("session store not available (required for filtering)")
			}

			sessions, err := store.List()
			if err != nil {
				return nil, WorkspaceListOutput{}, fmt.Errorf("failed to list sessions for filtering: %w", err)
			}

			wsHasNonArchived = make(map[string]bool)
			wsHasAnySessions = make(map[string]bool)
			for _, meta := range sessions {
				if meta.WorkingDir == "" {
					continue
				}
				wsHasAnySessions[meta.WorkingDir] = true
				if !meta.Archived {
					wsHasNonArchived[meta.WorkingDir] = true
				}
			}
		} else if input.Filter != "" {
			return nil, WorkspaceListOutput{}, fmt.Errorf("invalid filter value %q: must be \"active\", \"archived\", or omitted", input.Filter)
		}

		workspaces := sm.GetWorkspaces()
		infos := make([]WorkspaceInfo, 0, len(workspaces))

		for _, ws := range workspaces {
			// Apply filter
			if input.Filter == "active" {
				if !wsHasNonArchived[ws.WorkingDir] {
					continue // skip: no non-archived sessions
				}
			} else if input.Filter == "archived" {
				if !wsHasAnySessions[ws.WorkingDir] || wsHasNonArchived[ws.WorkingDir] {
					continue // skip: no sessions at all OR has non-archived sessions
				}
			}

			info := WorkspaceInfo{
				UUID:       ws.UUID,
				Name:       ws.Name,
				WorkingDir: ws.WorkingDir,
				ACPServer:  ws.ACPServer,
				IsDefault:  ws.IsDefault,
			}

			// Load .mittorc metadata if workspace has a working directory
			if ws.WorkingDir != "" {
				rc, err := config.LoadWorkspaceRC(ws.WorkingDir)
				if err != nil {
					s.logger.Warn("Failed to load workspace .mittorc",
						"working_dir", ws.WorkingDir,
						"error", err)
				}
				if rc != nil && rc.Metadata != nil {
					info.Metadata = rc.Metadata
				}
			}

			infos = append(infos, info)
		}

		return nil, WorkspaceListOutput{Workspaces: infos}, nil
	}
}

func (s *Server) handleWorkspaceUpdate(ctx context.Context, req *mcp.CallToolRequest, input WorkspaceUpdateInput) (*mcp.CallToolResult, WorkspaceUpdateOutput, error) {
	if input.SelfID == "" {
		return nil, WorkspaceUpdateOutput{Success: false, Error: "self_id is required"}, nil
	}

	realSessionID := s.resolveSelfIDWithMCP(input.SelfID, req)
	if realSessionID == "" {
		return nil, WorkspaceUpdateOutput{
			Success: false,
			Error:   fmt.Sprintf("session not found: the self_id '%s' could not be resolved", input.SelfID),
		}, nil
	}

	reg := s.getSession(realSessionID)
	if reg == nil {
		return nil, WorkspaceUpdateOutput{
			Success: false,
			Error:   fmt.Sprintf("session not found or not running: %s", realSessionID),
		}, nil
	}

	s.mu.RLock()
	store := s.store
	sm := s.sessionManager
	s.mu.RUnlock()

	if store == nil {
		return nil, WorkspaceUpdateOutput{Success: false, Error: "session store not available"}, nil
	}
	if sm == nil {
		return nil, WorkspaceUpdateOutput{Success: false, Error: "session manager not available"}, nil
	}

	callerMeta, err := store.GetMetadata(realSessionID)
	if err != nil {
		return nil, WorkspaceUpdateOutput{
			Success: false,
			Error:   fmt.Sprintf("failed to get caller session metadata: %v", err),
		}, nil
	}

	// Resolve target workspace directory and UUID.
	var targetDir, targetUUID string
	if input.Workspace != "" {
		targetWS := sm.GetWorkspaceByUUID(input.Workspace)
		if targetWS == nil {
			return nil, WorkspaceUpdateOutput{
				Success: false,
				Error:   fmt.Sprintf("workspace not found: %s", input.Workspace),
			}, nil
		}
		targetDir = targetWS.WorkingDir
		targetUUID = targetWS.UUID
		if targetWS.WorkingDir != callerMeta.WorkingDir {
			if !s.checkSessionFlag(realSessionID, session.FlagCanInteractOtherWorkspaces) {
				return nil, WorkspaceUpdateOutput{
					Success: false,
					Error: fmt.Sprintf("cross-workspace operations require the 'Can interact with other workspaces' (%s) flag to be enabled in Advanced Settings",
						session.FlagCanInteractOtherWorkspaces),
				}, nil
			}
			if err := s.confirmCrossWorkspaceOperation(ctx, realSessionID, "modify workspace configuration", targetWS); err != nil {
				return nil, WorkspaceUpdateOutput{Success: false, Error: err.Error()}, nil
			}
		}
	} else {
		targetDir = callerMeta.WorkingDir
		for _, ws := range sm.GetWorkspaces() {
			if ws.WorkingDir == targetDir {
				targetUUID = ws.UUID
				break
			}
		}
	}

	if targetDir == "" {
		return nil, WorkspaceUpdateOutput{Success: false, Error: "target workspace has no working directory"}, nil
	}

	// Load current state.
	curRC, _ := config.LoadWorkspaceRC(targetDir)
	var curDesc, curURL, curGroup string
	var curFields []config.UserDataSchemaField
	if curRC != nil && curRC.Metadata != nil {
		curDesc = curRC.Metadata.Description
		curURL = curRC.Metadata.URL
		curGroup = curRC.Metadata.Group
		if curRC.Metadata.UserDataSchema != nil {
			curFields = curRC.Metadata.UserDataSchema.Fields
		}
	}

	var updated []string

	// Update metadata fields if any pointer is non-nil.
	if input.Description != nil || input.URL != nil || input.Group != nil {
		desc := curDesc
		if input.Description != nil {
			desc = *input.Description
		}
		u := curURL
		if input.URL != nil {
			u = *input.URL
		}
		grp := curGroup
		if input.Group != nil {
			grp = *input.Group
		}
		if err := config.SaveWorkspaceMetadata(targetDir, desc, u, grp); err != nil {
			return nil, WorkspaceUpdateOutput{Success: false, Error: fmt.Sprintf("failed to save metadata: %v", err)}, nil
		}
		if input.Description != nil {
			updated = append(updated, "description")
		}
		if input.URL != nil {
			updated = append(updated, "url")
		}
		if input.Group != nil {
			updated = append(updated, "group")
		}
	}

	// Update schema if user_data_schema was present in the input (nil = absent).
	if input.UserDataSchema != nil {
		merge := input.UserDataSchemaMerge == nil || *input.UserDataSchemaMerge

		// Convert and validate provided fields.
		newFields := make([]config.UserDataSchemaField, 0, len(input.UserDataSchema))
		for _, f := range input.UserDataSchema {
			t := config.UserDataAttributeType(f.Type).DefaultType()
			if !t.IsValid() {
				return nil, WorkspaceUpdateOutput{
					Success: false,
					Error:   fmt.Sprintf("invalid user_data field type %q for field %q (allowed: string, url, filename)", f.Type, f.Name),
				}, nil
			}
			newFields = append(newFields, config.UserDataSchemaField{
				Name:        f.Name,
				Description: f.Description,
				Type:        t,
			})
		}

		var finalFields []config.UserDataSchemaField
		if merge {
			// Start from existing fields; replace by name or append.
			finalFields = make([]config.UserDataSchemaField, len(curFields))
			copy(finalFields, curFields)
			for _, nf := range newFields {
				replaced := false
				for i, ef := range finalFields {
					if ef.Name == nf.Name {
						finalFields[i] = nf
						replaced = true
						break
					}
				}
				if !replaced {
					finalFields = append(finalFields, nf)
				}
			}
		} else {
			finalFields = newFields
		}

		if err := config.SaveWorkspaceUserDataSchema(targetDir, finalFields); err != nil {
			return nil, WorkspaceUpdateOutput{Success: false, Error: fmt.Sprintf("failed to save user_data schema: %v", err)}, nil
		}
		updated = append(updated, "user_data_schema")
	}

	if len(updated) == 0 {
		return nil, WorkspaceUpdateOutput{
			Success: false,
			Error:   "no properties to update: specify at least one of 'description', 'url', 'group', or 'user_data_schema'",
		}, nil
	}

	sm.InvalidateWorkspaceRC(targetDir)

	var outMeta *config.WorkspaceMetadata
	if newRC, _ := config.LoadWorkspaceRC(targetDir); newRC != nil {
		outMeta = newRC.Metadata
	}

	s.logger.Info("Workspace updated via MCP",
		"source_session", realSessionID,
		"target_dir", targetDir,
		"updated", updated)

	return nil, WorkspaceUpdateOutput{
		Success:       true,
		WorkspaceUUID: targetUUID,
		WorkingDir:    targetDir,
		Updated:       updated,
		Metadata:      outMeta,
	}, nil
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

		// A) Build workspace lookup maps for enriching results with workspace identity.
		// Composite key (workingDir+"|"+acpServer) → WorkspaceSettings for exact matching;
		// fallback key workingDir → WorkspaceSettings (first match) for partial matching.
		var wsCompositeMap map[string]config.WorkspaceSettings
		var wsFallbackMap map[string]config.WorkspaceSettings
		if sm != nil {
			workspaces := sm.GetWorkspaces()
			wsCompositeMap = make(map[string]config.WorkspaceSettings, len(workspaces))
			wsFallbackMap = make(map[string]config.WorkspaceSettings, len(workspaces))
			for _, ws := range workspaces {
				compositeKey := ws.WorkingDir + "|" + ws.ACPServer
				wsCompositeMap[compositeKey] = ws
				if _, exists := wsFallbackMap[ws.WorkingDir]; !exists {
					wsFallbackMap[ws.WorkingDir] = ws
				}
			}
		}

		// B) Permission-gated workspace filtering.
		// workingDirFilter, if non-empty, restricts results to a specific working directory
		// and takes precedence over input.WorkingDir.
		var workingDirFilter string

		if input.SelfID != "" {
			// Resolve caller's session ID.
			realSessionID := s.resolveSelfIDWithMCP(input.SelfID, req)
			if realSessionID == "" {
				return nil, ListConversationsOutput{}, fmt.Errorf(
					"session not found: the self_id '%s' could not be resolved", input.SelfID)
			}

			// Get caller's metadata to determine their workspace.
			callerMeta, err := store.GetMetadata(realSessionID)
			if err != nil {
				return nil, ListConversationsOutput{}, fmt.Errorf(
					"failed to get caller metadata: %w", err)
			}

			hasXWPermissions := s.checkSessionFlag(realSessionID, session.FlagCanInteractOtherWorkspaces)

			if input.Workspace != nil {
				// Explicit workspace requested — resolve and check permissions.
				if sm == nil {
					return nil, ListConversationsOutput{}, fmt.Errorf("session manager not available")
				}
				targetWS := sm.GetWorkspaceByUUID(*input.Workspace)
				if targetWS == nil {
					return nil, ListConversationsOutput{}, fmt.Errorf("workspace not found: %s", *input.Workspace)
				}
				if targetWS.WorkingDir != callerMeta.WorkingDir && !hasXWPermissions {
					return nil, ListConversationsOutput{}, fmt.Errorf(
						"cross-workspace operations require the 'Can interact with other workspaces' (%s) flag to be enabled in Advanced Settings",
						session.FlagCanInteractOtherWorkspaces)
				}
				workingDirFilter = targetWS.WorkingDir
			} else {
				// No explicit workspace: scope to caller's own workspace unless they have cross-workspace permissions.
				if !hasXWPermissions {
					workingDirFilter = callerMeta.WorkingDir
				}
				// If caller has cross-workspace permissions and no workspace filter, list all.
			}
		} else if input.Workspace != nil {
			// No self_id but workspace UUID provided — resolve without permission checks (backward compat).
			if sm != nil {
				targetWS := sm.GetWorkspaceByUUID(*input.Workspace)
				if targetWS == nil {
					return nil, ListConversationsOutput{}, fmt.Errorf("workspace not found: %s", *input.Workspace)
				}
				workingDirFilter = targetWS.WorkingDir
			}
		}

		sessions, err := store.List()
		if err != nil {
			return nil, ListConversationsOutput{}, fmt.Errorf("failed to list sessions: %w", err)
		}

		conversations := make([]ConversationInfo, 0, len(sessions))
		for _, meta := range sessions {
			// C) Apply filters.
			// workspace-derived filter takes precedence over explicit input.WorkingDir.
			if workingDirFilter != "" {
				if meta.WorkingDir != workingDirFilter {
					continue
				}
			} else if input.WorkingDir != nil && meta.WorkingDir != *input.WorkingDir {
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
				BeadsIssue:        meta.BeadsIssue,
				ACPServer:         meta.ACPServer,
				WorkingDir:        meta.WorkingDir,
				CreatedAt:         meta.CreatedAt,
				UpdatedAt:         meta.UpdatedAt,
				LastUserMessageAt: meta.LastUserMessageAt,
				MessageCount:      meta.EventCount,
				Status:            string(meta.Status),
				Archived:          meta.Archived,
				ArchiveReason:     string(meta.ArchiveReason),
				SessionFolder:     store.SessionDir(meta.SessionID),
				ChildOrigin:       string(meta.ChildOrigin),
			}

			// D) Enrich with workspace identity using composite key, falling back to working-dir-only lookup.
			if wsCompositeMap != nil {
				compositeKey := meta.WorkingDir + "|" + meta.ACPServer
				if ws, ok := wsCompositeMap[compositeKey]; ok {
					info.WorkspaceUUID = ws.UUID
					info.WorkspaceName = ws.Name
				} else if ws, ok := wsFallbackMap[meta.WorkingDir]; ok {
					info.WorkspaceUUID = ws.UUID
					info.WorkspaceName = ws.Name
				}
			}

			// Check lock status.
			lockInfo, err := store.GetLockInfo(meta.SessionID)
			if err == nil && lockInfo != nil {
				info.IsLocked = true
				info.LockStatus = string(lockInfo.Status)
				info.LockClientType = lockInfo.ClientType
				info.IsPrompting = lockInfo.Status == session.LockStatusProcessing
			}

			// Get running session info if available.
			if sm != nil {
				if bs := sm.GetSession(meta.SessionID); bs != nil {
					info.IsRunning = true
					info.IsPrompting = bs.IsPrompting()
					info.LastSeq = bs.GetMaxAssignedSeq()
				}
			}

			// Check if conversation has an active periodic prompt.
			if p, err := store.Periodic(meta.SessionID).Get(); err == nil && p != nil {
				info.IsPeriodic = p.Enabled
			}

			// Apply is_running filter after runtime status is resolved.
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
	SelfID         string            `json:"self_id"`         // YOUR session ID (the caller), not the target
	ConversationID string            `json:"conversation_id"` // Target conversation ID to send prompt to
	Prompt         string            `json:"prompt"`
	Workspace      string            `json:"workspace,omitempty"`     // Optional workspace UUID for cross-workspace operations
	ScheduleTime   string            `json:"schedule_time,omitempty"` // Optional: RFC 3339 timestamp or relative duration (e.g., "5m", "1h")
	Arguments      map[string]string `json:"arguments,omitempty"`     // Optional: ${VAR}/${VAR:-default} substitution values applied to the prompt text when sent
	PromptName     string            `json:"prompt_name,omitempty"`   // Optional: name of a workspace prompt to send by name (resolved at dispatch in the target conversation's context)
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
	if strings.TrimSpace(input.Prompt) == "" && strings.TrimSpace(input.PromptName) == "" {
		return nil, SendPromptOutput{Success: false, Error: "either 'prompt' or 'prompt_name' is required"}, nil
	}

	// Check if target conversation exists
	targetMeta, err := store.GetMetadata(input.ConversationID)
	if err != nil {
		return nil, SendPromptOutput{
			Success: false,
			Error:   fmt.Sprintf("conversation not found: %s", input.ConversationID),
		}, nil
	}

	// Cross-workspace support: if workspace UUID is provided, validate and confirm
	if input.Workspace != "" {
		if s.sessionManager == nil {
			return nil, SendPromptOutput{Success: false, Error: "session manager not available"}, nil
		}
		targetWS := s.sessionManager.GetWorkspaceByUUID(input.Workspace)
		if targetWS == nil {
			return nil, SendPromptOutput{
				Success: false,
				Error:   fmt.Sprintf("workspace not found: %s", input.Workspace),
			}, nil
		}

		// Validate conversation belongs to the specified workspace
		if targetMeta.WorkingDir != targetWS.WorkingDir {
			return nil, SendPromptOutput{
				Success: false,
				Error:   fmt.Sprintf("conversation %s does not belong to workspace %s", input.ConversationID, input.Workspace),
			}, nil
		}

		// Check if cross-workspace (caller's workspace differs from target)
		sourceMeta, err := store.GetMetadata(realSessionID)
		if err != nil {
			return nil, SendPromptOutput{
				Success: false,
				Error:   fmt.Sprintf("failed to get source session metadata: %v", err),
			}, nil
		}
		if sourceMeta.WorkingDir != targetWS.WorkingDir {
			// Permission check: requires can_interact_other_workspaces flag
			if !s.checkSessionFlag(realSessionID, session.FlagCanInteractOtherWorkspaces) {
				return nil, SendPromptOutput{
					Success: false,
					Error: fmt.Sprintf("cross-workspace operations require the 'Can interact with other workspaces' (%s) flag to be enabled in Advanced Settings",
						session.FlagCanInteractOtherWorkspaces),
				}, nil
			}
			if err := s.confirmCrossWorkspaceOperation(ctx, realSessionID, "send a prompt to a conversation", targetWS); err != nil {
				return nil, SendPromptOutput{Success: false, Error: err.Error()}, nil
			}
		}
	}

	// Parse optional scheduled time (supports RFC 3339 or relative duration like "5m", "1h")
	var scheduledTime *time.Time
	if input.ScheduleTime != "" {
		t, err := session.ParseScheduleTime(input.ScheduleTime)
		if err != nil {
			return nil, SendPromptOutput{
				Success: false,
				Error:   err.Error(),
			}, nil
		}
		scheduledTime = &t
	}

	// Get the queue for the target conversation
	queue := store.Queue(input.ConversationID)

	// Add the prompt to the queue
	msg, err := queue.Add(input.Prompt, nil, nil, realSessionID, scheduledTime, 0, input.Arguments, input.PromptName)
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
		"queue_position", queueLen,
		"scheduled", scheduledTime != nil)

	// Try to process the queued message immediately if agent is idle.
	// Skip for scheduled messages — the periodic runner will deliver them when due.
	if scheduledTime == nil {
		if s.sessionManager != nil {
			bs := s.sessionManager.GetSession(input.ConversationID)
			if bs == nil && !targetMeta.Archived {
				// Session is stored or completed (e.g., GC-closed) — try to resume it so the queue gets processed.
				s.logger.Info("Auto-resuming session to process queued prompt",
					"target_session", input.ConversationID,
					"source_session", realSessionID,
					"target_status", string(targetMeta.Status))
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
	}

	return nil, SendPromptOutput{
		Success:       true,
		MessageID:     msg.ID,
		QueuePosition: queueLen,
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
	const maxQuestionLen = 500
	if len([]rune(question)) > maxQuestionLen {
		return nil, UIOptionsOutput{Index: -1}, fmt.Errorf(
			"the question text is too long (%d characters, max %d). Print the detailed context to the user as a regular message first, then call mitto_ui_options with a concise question",
			len([]rune(question)), maxQuestionLen)
	}

	// Generate unique internal request ID for UI prompt
	uiRequestID := fmt.Sprintf("%s-%s", realSessionID[:8], uuid.New().String()[:8])

	// Build options with IDs and descriptions, truncating long text
	const maxLabelLen = 80
	const maxDescLen = 200
	options := make([]UIPromptOption, len(input.Options))
	for i, item := range input.Options {
		label := []rune(item.Label)
		if len(label) > maxLabelLen {
			label = append(label[:maxLabelLen-1], '…')
		}
		desc := []rune(item.Description)
		if len(desc) > maxDescLen {
			desc = append(desc[:maxDescLen-1], '…')
		}
		options[i] = UIPromptOption{
			ID:          fmt.Sprintf("%d", i),
			Label:       string(label),
			Description: string(desc),
		}
	}

	promptReq := UIPromptRequest{
		RequestID:           uiRequestID,
		Type:                UIPromptTypeOptions,
		Question:            question,
		Options:             options,
		TimeoutSeconds:      timeout,
		Blocking:            true,
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

func (s *Server) handleUIForm(ctx context.Context, req *mcp.CallToolRequest, input UIFormInput) (*mcp.CallToolResult, UIFormOutput, error) {
	// Validate self_id
	if input.SelfID == "" {
		return nil, UIFormOutput{}, fmt.Errorf("self_id is required")
	}

	// Resolve the self_id to a real session ID
	realSessionID := s.resolveSelfIDWithMCP(input.SelfID, req)
	if realSessionID == "" {
		return nil, UIFormOutput{}, fmt.Errorf(
			"session not found: the self_id '%s' could not be resolved",
			input.SelfID,
		)
	}

	// Check if session is registered and get the UIPrompter
	reg := s.getSession(realSessionID)
	if reg == nil {
		return nil, UIFormOutput{}, fmt.Errorf("session not found or not running: %s", realSessionID)
	}

	// Permission check
	if !s.checkSessionFlag(realSessionID, session.FlagCanPromptUser) {
		return nil, UIFormOutput{}, permissionError("mitto_ui_form", session.FlagCanPromptUser, "Can prompt user")
	}

	// Check if UIPrompter is available
	if reg.uiPrompter == nil {
		return nil, UIFormOutput{}, fmt.Errorf("UI prompts are not available (no UI connected)")
	}

	// Validate and sanitize HTML
	if input.Title == "" {
		return nil, UIFormOutput{}, fmt.Errorf("title is required")
	}
	sanitizedHTML, err := sanitizeFormHTML(input.HTML)
	if err != nil {
		return nil, UIFormOutput{}, fmt.Errorf("invalid form HTML: %w", err)
	}

	// Apply timeout default
	timeout := input.TimeoutSeconds
	if timeout <= 0 {
		timeout = 600 // 10 minutes default
	}

	// Generate unique internal request ID
	uiRequestID := fmt.Sprintf("%s-%s", realSessionID[:8], uuid.New().String()[:8])

	// Build the prompt request
	promptReq := UIPromptRequest{
		RequestID:      uiRequestID,
		Type:           UIPromptTypeForm,
		Title:          input.Title,
		Question:       input.Title,
		FormHTML:       sanitizedHTML,
		TimeoutSeconds: timeout,
		Blocking:       true,
	}

	s.logger.Debug("Sending UI form prompt",
		"session_id", realSessionID,
		"input_session_id", input.SelfID,
		"ui_request_id", uiRequestID,
		"title", input.Title,
		"html_length", len(sanitizedHTML),
		"timeout", timeout)

	// Send prompt and wait for response (blocks until user responds or timeout)
	resp, err := reg.uiPrompter.UIPrompt(ctx, promptReq)
	if err != nil {
		return nil, UIFormOutput{}, fmt.Errorf("failed to display UI form: %w", err)
	}

	// Handle timeout
	if resp.TimedOut {
		s.logger.Debug("UI form prompt timed out", "session_id", realSessionID)
		return nil, UIFormOutput{TimedOut: true}, nil
	}

	// Handle cancel
	if resp.Aborted || resp.OptionID == "cancel" {
		s.logger.Debug("UI form prompt cancelled", "session_id", realSessionID)
		return nil, UIFormOutput{Cancelled: true}, nil
	}

	// Parse form values from FreeText (JSON-encoded map[string]string)
	var values map[string]string
	if resp.FreeText != "" {
		if err := json.Unmarshal([]byte(resp.FreeText), &values); err != nil {
			s.logger.Error("Failed to parse form values", "session_id", realSessionID, "error", err)
			return nil, UIFormOutput{}, fmt.Errorf("failed to parse form values: %w", err)
		}
	}

	s.logger.Debug("UI form prompt submitted",
		"session_id", realSessionID,
		"field_count", len(values))

	return nil, UIFormOutput{
		Submitted: true,
		Values:    values,
	}, nil
}

func (s *Server) handleUINotify(_ context.Context, req *mcp.CallToolRequest, input UINotifyInput) (*mcp.CallToolResult, UINotifyOutput, error) {
	// Validate self_id
	if input.SelfID == "" {
		return nil, UINotifyOutput{}, fmt.Errorf("self_id is required")
	}

	// Resolve the self_id to a real session ID
	realSessionID := s.resolveSelfIDWithMCP(input.SelfID, req)
	if realSessionID == "" {
		return nil, UINotifyOutput{}, fmt.Errorf(
			"session not found: the self_id '%s' could not be resolved",
			input.SelfID,
		)
	}

	// Check if session is registered and get the UIPrompter
	reg := s.getSession(realSessionID)
	if reg == nil {
		return nil, UINotifyOutput{}, fmt.Errorf("session not found or not running: %s", realSessionID)
	}

	// Permission check
	if !s.checkSessionFlag(realSessionID, session.FlagCanPromptUser) {
		return nil, UINotifyOutput{}, permissionError("mitto_ui_notify", session.FlagCanPromptUser, "Can prompt user")
	}

	// Check if UIPrompter is available
	if reg.uiPrompter == nil {
		return nil, UINotifyOutput{}, fmt.Errorf("UI notifications are not available (no UI connected)")
	}

	// Validate title
	if input.Title == "" {
		return nil, UINotifyOutput{}, fmt.Errorf("title is required")
	}

	// Validate and default style
	style := input.Style
	switch style {
	case "info", "success", "warning", "error":
		// valid
	case "":
		style = "info"
	default:
		return nil, UINotifyOutput{}, fmt.Errorf("style must be one of: 'info', 'success', 'warning', 'error' (got '%s')", style)
	}

	// Truncate fields to reasonable limits
	const maxTitleLen = 200
	const maxMessageLen = 1000
	title := []rune(input.Title)
	if len(title) > maxTitleLen {
		title = append(title[:maxTitleLen-1], '…')
	}
	message := []rune(input.Message)
	if len(message) > maxMessageLen {
		message = append(message[:maxMessageLen-1], '…')
	}

	notifyReq := UINotifyRequest{
		Title:   string(title),
		Message: string(message),
		Style:   style,
		Sound:   input.Sound,
		Native:  input.Native,
		Sticky:  input.Sticky,
	}

	s.logger.Debug("UI notify dispatched",
		"session_id", realSessionID,
		"title", notifyReq.Title,
		"style", style)

	// Fire-and-forget — UINotify is non-blocking
	if err := reg.uiPrompter.UINotify(notifyReq); err != nil {
		return nil, UINotifyOutput{}, fmt.Errorf("failed to send notification: %w", err)
	}

	return nil, UINotifyOutput{Success: true}, nil
}

// computeUnifiedDiff generates a simple unified diff between two texts.
func computeUnifiedDiff(original, edited, originalName, editedName string) string {
	originalLines := strings.Split(original, "\n")
	editedLines := strings.Split(edited, "\n")

	var result strings.Builder
	fmt.Fprintf(&result, "--- %s\n", originalName)
	fmt.Fprintf(&result, "+++ %s\n", editedName)

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
			fmt.Fprintf(&result, " %s\n", op.line)
		case '-':
			fmt.Fprintf(&result, "-%s\n", op.line)
		case '+':
			fmt.Fprintf(&result, "+%s\n", op.line)
		}
	}

	return result.String()
}

// ConversationStartInput is the input for mitto_conversation_new tool.
type ConversationStartInput struct {
	SelfID             string            `json:"self_id"`                        // YOUR session ID (the caller)
	Title              string            `json:"title,omitempty"`                // Optional title for the new conversation
	InitialPrompt      string            `json:"initial_prompt,omitempty"`       // Optional initial message to queue
	PromptName         string            `json:"prompt_name,omitempty"`          // Optional: name of a predefined prompt to use as the initial prompt (mutually exclusive with initial_prompt)
	InitialPromptDelay string            `json:"initial_prompt_delay,omitempty"` // Optional: delay initial prompt delivery (RFC 3339 timestamp or relative duration like "5m", "1h")
	Arguments          map[string]string `json:"arguments,omitempty"`            // Optional: ${VAR}/${VAR:-default} substitution values applied to the initial prompt when sent
	ACPServer          string            `json:"acp_server,omitempty"`           // Optional ACP server name (defaults to parent's server)
	BeadsIssue         string            `json:"beads_issue,omitempty"`          // Optional: link the new conversation to a beads issue ID (e.g. "mitto-123")
	Workspace          string            `json:"workspace,omitempty"`            // Optional workspace UUID for cross-workspace operations
	// Periodic configuration (optional) - creates the conversation as periodic
	PeriodicPrompt         string `json:"periodic_prompt,omitempty"`          // The prompt to send periodically
	PeriodicFrequencyValue int    `json:"periodic_frequency_value,omitempty"` // Number of units between sends
	PeriodicFrequencyUnit  string `json:"periodic_frequency_unit,omitempty"`  // Time unit: "minutes", "hours", or "days"
	PeriodicFrequencyAt    string `json:"periodic_frequency_at,omitempty"`    // Time of day HH:MM (UTC), only for "days"
	PeriodicEnabled        *bool  `json:"periodic_enabled,omitempty"`         // Whether periodic is active (defaults to true)
	PeriodicFreshContext   *bool  `json:"periodic_fresh_context,omitempty"`   // Start each run with a fresh agent context (default false)
	PeriodicMaxIterations  *int   `json:"periodic_max_iterations,omitempty"`  // Maximum number of scheduled runs (0 = unlimited)
	// On-completion trigger configuration (optional)
	PeriodicTrigger                string `json:"periodic_trigger,omitempty"`                  // "schedule" (default) or "onCompletion"
	PeriodicCompletionDelaySeconds *int   `json:"periodic_completion_delay_seconds,omitempty"` // Wait (s) after agent stops, onCompletion only; clamped to floor
	PeriodicMaxDurationSeconds     *int   `json:"periodic_max_duration_seconds,omitempty"`     // Wall-clock cap (s) since iterating started (0 = unlimited)
}

// ConversationStartOutput is the output for mitto_conversation_new tool.
// Embeds ConversationDetails for the newly created conversation.
type ConversationStartOutput struct {
	ConversationDetails        // Embedded conversation details
	QueuePosition       int    `json:"queue_position,omitempty"`      // Queue position if initial prompt was provided
	PeriodicConfigured  bool   `json:"periodic_configured,omitempty"` // Whether periodic was configured
	PeriodicNextRun     string `json:"periodic_next_run,omitempty"`   // Next scheduled run (RFC3339)
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

	// Cross-workspace support: if workspace UUID is provided, resolve and potentially confirm
	var targetWorkspace *config.WorkspaceSettings
	if input.Workspace != "" {
		// Cannot specify both workspace and acp_server
		if input.ACPServer != "" {
			return nil, ConversationStartOutput{}, fmt.Errorf(
				"cannot specify both 'workspace' and 'acp_server' — workspace already determines the ACP server")
		}

		// Resolve workspace UUID
		if s.sessionManager == nil {
			return nil, ConversationStartOutput{}, fmt.Errorf("session manager not available")
		}
		targetWorkspace = s.sessionManager.GetWorkspaceByUUID(input.Workspace)
		if targetWorkspace == nil {
			return nil, ConversationStartOutput{}, fmt.Errorf("workspace not found: %s", input.Workspace)
		}

		// Check if this is a cross-workspace operation (different working directory)
		if targetWorkspace.WorkingDir != sourceMeta.WorkingDir {
			// Permission check: requires can_interact_other_workspaces flag
			if !s.checkSessionFlag(realSessionID, session.FlagCanInteractOtherWorkspaces) {
				return nil, ConversationStartOutput{}, fmt.Errorf(
					"cross-workspace operations require the 'Can interact with other workspaces' (%s) flag to be enabled in Advanced Settings",
					session.FlagCanInteractOtherWorkspaces)
			}
			if err := s.confirmCrossWorkspaceOperation(ctx, realSessionID, "create a new conversation", targetWorkspace); err != nil {
				return nil, ConversationStartOutput{}, err
			}
		}
	}

	// Resolve the effective initial prompt. A named prompt (prompt_name) is
	// mutually exclusive with an inline initial_prompt: when prompt_name is set,
	// its full text is looked up from the merged prompt list (same resolution as
	// mitto_prompt_get) and used as the initial prompt. Optional 'arguments' are
	// applied as ${VAR}/${VAR:-default} substitution when the prompt is sent.
	initialPromptText := input.InitialPrompt
	if input.PromptName != "" {
		if input.InitialPrompt != "" {
			return nil, ConversationStartOutput{}, fmt.Errorf(
				"cannot specify both 'prompt_name' and 'initial_prompt' — use one or the other")
		}
		promptWorkingDir, err := s.resolvePromptWorkingDir(realSessionID, input.Workspace)
		if err != nil {
			return nil, ConversationStartOutput{}, err
		}
		p, found := s.findPromptByName(promptWorkingDir, input.PromptName)
		if !found {
			return nil, ConversationStartOutput{}, fmt.Errorf(
				"prompt not found: no prompt named %q is available in this workspace", input.PromptName)
		}
		initialPromptText = p.Prompt
	}

	// Check max child conversations limit
	// This prevents a single session from spawning too many children and exhausting resources.
	// Auto-children (from workspace config) are excluded from the count.
	s.mu.RLock()
	cfg := s.config
	s.mu.RUnlock()

	maxChildren := config.DefaultMaxChildConversations
	if cfg != nil && cfg.Conversations != nil {
		maxChildren = cfg.Conversations.GetMaxChildConversations()
	}
	if maxChildren > 0 { // 0 means unlimited
		currentCount, err := store.CountMCPChildSessions(realSessionID)
		if err != nil {
			s.logger.Warn("Failed to count child sessions", "session_id", realSessionID, "error", err)
			// Don't block on count errors - just log and proceed
		} else if currentCount >= maxChildren {
			return nil, ConversationStartOutput{}, fmt.Errorf(
				"maximum number of child conversations reached (%d). "+
					"This limit can be changed in Settings → Conversations → Max Child Conversations",
				maxChildren)
		}
	}

	// Check for duplicate title if title is provided
	if input.Title != "" {
		allSessions, err := store.List()
		if err != nil {
			return nil, ConversationStartOutput{}, fmt.Errorf("failed to check for duplicate titles: %v", err)
		}
		for _, existingMeta := range allSessions {
			if existingMeta.Name == input.Title {
				errMsg := fmt.Sprintf(
					"a conversation with the title '%s' already exists (conversation_id: %s)",
					input.Title, existingMeta.SessionID)
				if initialPromptText != "" {
					errMsg += fmt.Sprintf(
						". To send a prompt to it, use 'mitto_conversation_send_prompt' with conversation_id='%s' and prompt='%s'",
						existingMeta.SessionID, truncateForError(initialPromptText, 200))
					if input.InitialPromptDelay != "" {
						errMsg += fmt.Sprintf(" and schedule_time='%s'", input.InitialPromptDelay)
					}
				}
				return nil, ConversationStartOutput{}, fmt.Errorf("%s", errMsg)
			}
		}
	}

	// Determine which ACP server and working directory to use.
	var acpServerName string
	var targetWorkingDir string
	if targetWorkspace != nil {
		// Cross-workspace: use the target workspace's server and directory.
		acpServerName = targetWorkspace.ACPServer
		targetWorkingDir = targetWorkspace.WorkingDir
	} else {
		acpServerName = sourceMeta.ACPServer // Default: inherit from parent
		targetWorkingDir = sourceMeta.WorkingDir
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
		WorkingDir:       targetWorkingDir,
		ParentSessionID:  realSessionID,          // Mark this session as a child
		ChildOrigin:      session.ChildOriginMCP, // Created via MCP tool
		AdvancedSettings: childSettings,
		BeadsIssue:       input.BeadsIssue,
	}

	// Create the session
	if err := store.Create(newMeta); err != nil {
		return nil, ConversationStartOutput{}, fmt.Errorf("failed to create session: %v", err)
	}

	s.logger.Info("New conversation created via MCP",
		"new_session_id", newSessionID,
		"parent_session_id", realSessionID,
		"acp_server", acpServerName,
		"working_dir", targetWorkingDir,
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
		bs, resumeErr = s.sessionManager.ResumeSession(newSessionID, input.Title, targetWorkingDir)
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
			targetWorkingDir,
			realSessionID,                  // parent_session_id
			string(session.ChildOriginMCP), // child_origin
		)
	}

	// If periodic configuration provided, set it up
	var periodicConfigured bool
	var periodicNextRun string
	if input.PeriodicPrompt != "" {
		// Resolve the trigger (default schedule). onCompletion is event-driven and does
		// not require a frequency.
		trigger := session.PeriodicTrigger(input.PeriodicTrigger)
		switch trigger {
		case "", session.TriggerSchedule, session.TriggerOnCompletion:
			// valid
		default:
			return nil, ConversationStartOutput{}, fmt.Errorf("periodic_trigger must be 'schedule' or 'onCompletion'")
		}
		isOnCompletion := trigger == session.TriggerOnCompletion

		var freq session.Frequency
		if !isOnCompletion {
			// Schedule trigger: frequency is required.
			if input.PeriodicFrequencyValue < 1 {
				return nil, ConversationStartOutput{}, fmt.Errorf("periodic_frequency_value must be >= 1 when periodic_prompt is provided")
			}

			var freqUnit session.FrequencyUnit
			switch input.PeriodicFrequencyUnit {
			case "minutes":
				freqUnit = session.FrequencyMinutes
			case "hours":
				freqUnit = session.FrequencyHours
			case "days":
				freqUnit = session.FrequencyDays
			default:
				return nil, ConversationStartOutput{}, fmt.Errorf("periodic_frequency_unit must be 'minutes', 'hours', or 'days'")
			}

			freq = session.Frequency{
				Value: input.PeriodicFrequencyValue,
				Unit:  freqUnit,
				At:    input.PeriodicFrequencyAt,
			}
			if err := freq.Validate(); err != nil {
				return nil, ConversationStartOutput{}, fmt.Errorf("invalid periodic frequency: %v", err)
			}
		}

		enabled := true
		if input.PeriodicEnabled != nil {
			enabled = *input.PeriodicEnabled
		}

		freshContext := false
		if input.PeriodicFreshContext != nil {
			freshContext = *input.PeriodicFreshContext
		}

		maxIterations := 0
		if input.PeriodicMaxIterations != nil {
			maxIterations = *input.PeriodicMaxIterations
		}

		delaySeconds := 0
		if input.PeriodicCompletionDelaySeconds != nil {
			delaySeconds = *input.PeriodicCompletionDelaySeconds
		}

		maxDurationSeconds := 0
		if input.PeriodicMaxDurationSeconds != nil {
			maxDurationSeconds = *input.PeriodicMaxDurationSeconds
		}

		periodic := &session.PeriodicPrompt{
			Prompt:             input.PeriodicPrompt,
			Frequency:          freq,
			Enabled:            enabled,
			FreshContext:       freshContext,
			MaxIterations:      maxIterations,
			Trigger:            trigger,
			DelaySeconds:       delaySeconds,
			MaxDurationSeconds: maxDurationSeconds,
		}
		// Clamp the on-completion delay to the global floor (no-op for schedule).
		periodic.ClampDelay(s.periodicDelayFloor())

		periodicStore := store.Periodic(newSessionID)
		if err := periodicStore.Set(periodic); err != nil {
			s.logger.Error("Failed to set periodic on new conversation",
				"session_id", newSessionID,
				"error", err)
			// Don't fail the whole creation - just log the error
		} else {
			periodicConfigured = true
			updated, err := periodicStore.Get()
			if err == nil && updated.NextScheduledAt != nil {
				periodicNextRun = updated.NextScheduledAt.Format("2006-01-02T15:04:05Z07:00")
			}
			s.logger.Info("Periodic prompt configured on new conversation",
				"session_id", newSessionID,
				"periodic_prompt", input.PeriodicPrompt,
				"frequency_value", input.PeriodicFrequencyValue,
				"frequency_unit", input.PeriodicFrequencyUnit,
				"enabled", enabled)

			// Kick off the very first run for a fresh onCompletion conversation.
			s.mu.RLock()
			runner := s.periodicRunner
			s.mu.RUnlock()
			if runner != nil {
				runner.BootstrapOnCompletion(newSessionID)
			}
		}
	}

	// If no explicit title was provided and periodic was configured, trigger title
	// generation from the periodic prompt text so the conversation has a name right away.
	// ConversationStartInput has no PeriodicPromptName field, so prompt name is passed as "".
	if input.Title == "" && periodicConfigured && bs != nil {
		bs.TriggerTitleGenerationFromPeriodic(input.PeriodicPrompt, "")
	}

	// Build unified conversation details
	output := ConversationStartOutput{
		ConversationDetails: s.buildConversationDetails(createdMeta, store.SessionDir(newSessionID)),
		PeriodicConfigured:  periodicConfigured,
		PeriodicNextRun:     periodicNextRun,
	}
	// Update runtime status to reflect the running ACP session
	if bs != nil {
		output.IsRunning = true
	}

	// Validate initial_prompt_delay requires an initial prompt (inline or named)
	if input.InitialPromptDelay != "" && initialPromptText == "" {
		return nil, ConversationStartOutput{}, fmt.Errorf("initial_prompt_delay requires initial_prompt or prompt_name to be set")
	}

	// If initial prompt provided, add it to the queue
	if initialPromptText != "" {
		// Parse optional initial prompt delay
		var scheduledTime *time.Time
		if input.InitialPromptDelay != "" {
			t, err := session.ParseScheduleTime(input.InitialPromptDelay)
			if err != nil {
				return nil, ConversationStartOutput{}, fmt.Errorf("invalid initial_prompt_delay: %v", err)
			}
			scheduledTime = &t
		}

		queue := store.Queue(newSessionID)
		_, err := queue.Add(initialPromptText, nil, nil, realSessionID, scheduledTime, 0, input.Arguments, "")
		if err != nil {
			s.logger.Warn("Failed to queue initial prompt",
				"session_id", newSessionID,
				"error", err)
		} else {
			queueLen, _ := queue.Len()
			output.QueuePosition = queueLen

			// Try to process the queued message immediately if agent is idle (skip if scheduled for later)
			if bs != nil && scheduledTime == nil {
				go bs.TryProcessQueuedMessage()
			}
		}
	}

	return nil, output, nil
}

// GetConversationInput is the input for mitto_get_conversation tool.
type GetConversationInput struct {
	SelfID         string `json:"self_id"`             // YOUR session ID (the caller)
	ConversationID string `json:"conversation_id"`     // Target conversation ID to get properties for
	Workspace      string `json:"workspace,omitempty"` // Optional workspace UUID for cross-workspace operations
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

	// Cross-workspace support: if workspace UUID is provided, validate and confirm
	if input.Workspace != "" {
		if s.sessionManager == nil {
			return nil, GetConversationOutput{}, fmt.Errorf("session manager not available")
		}
		targetWS := s.sessionManager.GetWorkspaceByUUID(input.Workspace)
		if targetWS == nil {
			return nil, GetConversationOutput{}, fmt.Errorf("workspace not found: %s", input.Workspace)
		}

		// Validate conversation belongs to the specified workspace
		if meta.WorkingDir != targetWS.WorkingDir {
			return nil, GetConversationOutput{}, fmt.Errorf(
				"conversation %s does not belong to workspace %s", input.ConversationID, input.Workspace)
		}

		// Check if cross-workspace (caller's workspace differs from target)
		sourceMeta, err := store.GetMetadata(realSessionID)
		if err != nil {
			return nil, GetConversationOutput{}, fmt.Errorf("failed to get source session metadata: %v", err)
		}
		if sourceMeta.WorkingDir != targetWS.WorkingDir {
			// Permission check: requires can_interact_other_workspaces flag
			if !s.checkSessionFlag(realSessionID, session.FlagCanInteractOtherWorkspaces) {
				return nil, GetConversationOutput{}, fmt.Errorf(
					"cross-workspace operations require the 'Can interact with other workspaces' (%s) flag to be enabled in Advanced Settings",
					session.FlagCanInteractOtherWorkspaces)
			}
			if err := s.confirmCrossWorkspaceOperation(ctx, realSessionID, "view a conversation", targetWS); err != nil {
				return nil, GetConversationOutput{}, err
			}
		}
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

// RunPeriodicNowInput is the input for mitto_conversation_run_periodic_now tool.
type RunPeriodicNowInput struct {
	SelfID         string `json:"self_id"`               // YOUR session ID (the caller)
	ConversationID string `json:"conversation_id"`       // Target conversation to trigger
	ResetTimer     *bool  `json:"reset_timer,omitempty"` // Whether to reset the countdown timer (default: true)
}

// RunPeriodicNowOutput is the output for mitto_conversation_run_periodic_now tool.
type RunPeriodicNowOutput struct {
	Success bool   `json:"success"`
	Message string `json:"message,omitempty"`
	Error   string `json:"error,omitempty"`
}

func (s *Server) handleRunPeriodicNow(ctx context.Context, req *mcp.CallToolRequest, input RunPeriodicNowInput) (*mcp.CallToolResult, RunPeriodicNowOutput, error) {
	// Validate self_id
	if input.SelfID == "" {
		return nil, RunPeriodicNowOutput{Error: "self_id is required"}, nil
	}

	// Validate conversation_id
	if input.ConversationID == "" {
		return nil, RunPeriodicNowOutput{Error: "conversation_id is required"}, nil
	}

	// Resolve the self_id to a real session ID
	realSessionID := s.resolveSelfIDWithMCP(input.SelfID, req)
	if realSessionID == "" {
		return nil, RunPeriodicNowOutput{
			Error: fmt.Sprintf("session not found: the self_id '%s' could not be resolved", input.SelfID),
		}, nil
	}

	// Check if source session is registered (must be running to use this tool)
	reg := s.getSession(realSessionID)
	if reg == nil {
		return nil, RunPeriodicNowOutput{Error: fmt.Sprintf("session not found or not running: %s", realSessionID)}, nil
	}

	// Check if periodic runner is available
	s.mu.RLock()
	runner := s.periodicRunner
	s.mu.RUnlock()

	if runner == nil {
		return nil, RunPeriodicNowOutput{Error: "periodic runner not available"}, nil
	}

	// Determine reset_timer (default: true — same as normal scheduled runs)
	resetTimer := true
	if input.ResetTimer != nil {
		resetTimer = *input.ResetTimer
	}

	// Trigger immediate delivery
	if err := runner.TriggerNow(input.ConversationID, resetTimer); err != nil {
		return nil, RunPeriodicNowOutput{Error: fmt.Sprintf("failed to trigger periodic run: %v", err)}, nil
	}

	msg := "Periodic prompt triggered successfully"
	if !resetTimer {
		msg += " (countdown timer preserved)"
	} else {
		msg += " (countdown timer reset)"
	}

	s.logger.Info("Periodic prompt triggered via MCP",
		"source_session", realSessionID,
		"target_conversation", input.ConversationID,
		"reset_timer", resetTimer)

	return nil, RunPeriodicNowOutput{Success: true, Message: msg}, nil
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
			m.ArchiveReason = session.ArchiveReasonManual
		} else {
			m.ArchivedAt = time.Time{}
			m.ArchiveReason = ""
		}
	})
	if err != nil {
		return nil, ArchiveConversationOutput{
			Success: false,
			Error:   fmt.Sprintf("failed to update metadata: %v", err),
		}, nil
	}

	// Broadcast the archived state change to all connected WebSocket clients.
	// For archive: broadcast immediately so clients know to disconnect.
	// For unarchive: broadcast AFTER ResumeSession so the session is already in
	// sm.sessions when clients reconnect (prevents pendingResumes race).
	if archived && s.sessionManager != nil {
		s.sessionManager.BroadcastSessionArchived(input.ConversationID, true, session.ArchiveReasonManual)
	}

	// Delete all child sessions when parent is archived
	if archived && s.sessionManager != nil {
		go s.sessionManager.DeleteChildSessions(input.ConversationID)
	}

	// Handle unarchive lifecycle: restart ACP session FIRST, then broadcast
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
		// Broadcast AFTER resume — session is now in sm.sessions
		if s.sessionManager != nil {
			s.sessionManager.BroadcastSessionArchived(input.ConversationID, false)
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

	// Self-deletion: the agent requests deletion of its OWN conversation by passing
	// "self" or its actual conversation ID. We cannot delete synchronously here —
	// the agent is mid-turn and the ACP connection is in use — and the parent-only
	// security check below would also reject it. Instead, set an in-memory flag on
	// the calling session; the backend deletes the conversation once the turn
	// completes (see BackgroundSession.PromptWithMeta).
	if input.ConversationID == "self" || input.ConversationID == realSessionID {
		if sessionManager == nil {
			return nil, DeleteConversationOutput{Success: false, Error: "session manager not available"}, nil
		}
		bs := sessionManager.GetSession(realSessionID)
		if bs == nil {
			return nil, DeleteConversationOutput{
				Success: false,
				Error:   fmt.Sprintf("session not found or not running: %s", realSessionID),
			}, nil
		}
		bs.RequestSelfDestruct()
		s.logger.Info("Conversation marked for self-destruction via MCP",
			"session_id", realSessionID)
		return nil, DeleteConversationOutput{
			Success:        true,
			ConversationID: realSessionID,
		}, nil
	}

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

	// Find ALL descendants recursively BEFORE deletion so we can close their ACP processes
	allDescendantIDs, findErr := store.FindAllChildrenRecursive(input.ConversationID)
	if findErr != nil {
		s.logger.Warn("Failed to find descendants for cascade deletion via MCP",
			"child_session", input.ConversationID,
			"error", findErr)
	}

	// Gracefully stop the child and all its descendants
	if sessionManager != nil {
		reason := "deleted_by_parent_via_mcp"
		if !sessionManager.CloseSessionGracefully(input.ConversationID, reason, archiveWaitTimeout) {
			s.logger.Warn("Timeout waiting for response before deleting child via MCP, proceeding anyway",
				"parent_session", realSessionID,
				"child_session", input.ConversationID)
			sessionManager.CloseSession(input.ConversationID, "deleted_by_parent_timeout_via_mcp")
		}
		// Close ACP for all descendants
		for _, descendantID := range allDescendantIDs {
			sessionManager.CloseSession(descendantID, "ancestor_deleted_via_mcp")
		}
	}

	// Permanently delete the child conversation from disk
	// store.Delete() will cascade-delete all descendants via handleChildSessionsOnParentDelete
	err = store.Delete(input.ConversationID)
	if err != nil {
		return nil, DeleteConversationOutput{
			Success: false,
			Error:   fmt.Sprintf("failed to delete conversation: %v", err),
		}, nil
	}

	// Broadcast deletion for the child and all descendants
	if s.sessionManager != nil {
		s.sessionManager.BroadcastSessionDeleted(input.ConversationID)
		for _, descendantID := range allDescendantIDs {
			s.sessionManager.BroadcastSessionDeleted(descendantID)
		}
	}

	s.logger.Info("Child conversation permanently deleted by parent via MCP",
		"parent_session", realSessionID,
		"child_session", input.ConversationID,
		"descendants_deleted", len(allDescendantIDs))

	return nil, DeleteConversationOutput{
		Success:        true,
		ConversationID: input.ConversationID,
	}, nil
}

func (s *Server) handleConversationUpdate(ctx context.Context, req *mcp.CallToolRequest, input ConversationUpdateInput) (*mcp.CallToolResult, ConversationUpdateOutput, error) {
	// Validate self_id
	if input.SelfID == "" {
		return nil, ConversationUpdateOutput{Success: false, Error: "self_id is required"}, nil
	}

	// Validate conversation_id
	if input.ConversationID == "" {
		return nil, ConversationUpdateOutput{Success: false, Error: "conversation_id is required"}, nil
	}

	// Resolve the self_id to a real session ID
	realSessionID := s.resolveSelfIDWithMCP(input.SelfID, req)
	if realSessionID == "" {
		return nil, ConversationUpdateOutput{
			Success: false,
			Error:   fmt.Sprintf("session not found: the self_id '%s' could not be resolved", input.SelfID),
		}, nil
	}

	// Check if source session is registered
	reg := s.getSession(realSessionID)
	if reg == nil {
		return nil, ConversationUpdateOutput{
			Success: false,
			Error:   fmt.Sprintf("session not found or not running: %s", realSessionID),
		}, nil
	}

	// Self-targeting: agents may pass "self" to update their OWN conversation
	// (e.g. a periodic conversation disabling its own periodicity). Unlike delete,
	// an update only touches metadata/periodic config and is safe to perform
	// synchronously, so we simply resolve the alias to the caller's real ID. This
	// keeps the tool consistent with mitto_conversation_delete, which also accepts "self".
	if input.ConversationID == "self" {
		input.ConversationID = realSessionID
	}

	s.mu.RLock()
	store := s.store
	sm := s.sessionManager
	s.mu.RUnlock()

	if store == nil {
		return nil, ConversationUpdateOutput{Success: false, Error: "session store not available"}, nil
	}

	// Verify target conversation exists
	meta, err := store.GetMetadata(input.ConversationID)
	if err != nil {
		return nil, ConversationUpdateOutput{
			Success: false,
			Error:   fmt.Sprintf("conversation not found: %s", input.ConversationID),
		}, nil
	}

	var updated []string

	// Update name if provided
	if input.Name != nil {
		if err := store.UpdateMetadata(input.ConversationID, func(m *session.Metadata) {
			m.Name = *input.Name
		}); err != nil {
			return nil, ConversationUpdateOutput{
				Success: false,
				Error:   fmt.Sprintf("failed to update name: %v", err),
			}, nil
		}
		updated = append(updated, "name")

		// Broadcast rename to all connected WebSocket clients
		if sm != nil {
			sm.BroadcastSessionRenamed(input.ConversationID, *input.Name)
		}

		s.logger.Info("Conversation renamed via MCP",
			"source_session", realSessionID,
			"target_conversation", input.ConversationID,
			"new_name", *input.Name)
	}

	// Update beads_issue if provided
	if input.BeadsIssue != nil {
		if err := store.UpdateMetadata(input.ConversationID, func(m *session.Metadata) {
			m.BeadsIssue = *input.BeadsIssue
		}); err != nil {
			return nil, ConversationUpdateOutput{
				Success: false,
				Error:   fmt.Sprintf("failed to update beads_issue: %v", err),
			}, nil
		}
		updated = append(updated, "beads_issue")
	}

	// Update user data if provided
	if len(input.UserData) > 0 {
		// Determine merge mode (default: true)
		merge := input.UserDataMerge == nil || *input.UserDataMerge

		var finalAttrs []session.UserDataAttribute
		if merge {
			// Load existing user data and merge
			existing, err := store.GetUserData(input.ConversationID)
			if err == nil && existing != nil {
				attrMap := make(map[string]string)
				var orderedNames []string
				seen := make(map[string]bool)
				for _, a := range existing.Attributes {
					attrMap[a.Name] = a.Value
					if !seen[a.Name] {
						orderedNames = append(orderedNames, a.Name)
						seen[a.Name] = true
					}
				}
				for _, a := range input.UserData {
					attrMap[a.Name] = a.Value
					if !seen[a.Name] {
						orderedNames = append(orderedNames, a.Name)
						seen[a.Name] = true
					}
				}
				for _, name := range orderedNames {
					finalAttrs = append(finalAttrs, session.UserDataAttribute{Name: name, Value: attrMap[name]})
				}
			} else {
				for _, a := range input.UserData {
					finalAttrs = append(finalAttrs, session.UserDataAttribute{Name: a.Name, Value: a.Value})
				}
			}
		} else {
			// Replace mode
			for _, a := range input.UserData {
				finalAttrs = append(finalAttrs, session.UserDataAttribute{Name: a.Name, Value: a.Value})
			}
		}

		userData := &session.UserData{Attributes: finalAttrs}

		// Validate against workspace schema. Relative filename paths are resolved
		// against the conversation's working directory.
		if sm != nil {
			schema := sm.GetUserDataSchema(meta.WorkingDir)
			if err := userData.Validate(schema, meta.WorkingDir); err != nil {
				return nil, ConversationUpdateOutput{
					Success: false,
					Error:   fmt.Sprintf("user_data validation error: %v", err),
				}, nil
			}
		}

		// Save user data
		if err := store.SetUserData(input.ConversationID, userData); err != nil {
			return nil, ConversationUpdateOutput{
				Success: false,
				Error:   fmt.Sprintf("failed to save user data: %v", err),
			}, nil
		}
		updated = append(updated, "user_data")

		s.logger.Info("User data updated via MCP",
			"source_session", realSessionID,
			"target_conversation", input.ConversationID,
			"attributes_count", len(finalAttrs),
			"merge", merge)
	}

	// Update periodic configuration if any periodic fields provided
	if input.PeriodicPrompt != nil || input.PeriodicFrequencyValue != nil || input.PeriodicFrequencyUnit != nil || input.PeriodicEnabled != nil || input.PeriodicFreshContext != nil || input.PeriodicMaxIterations != nil ||
		input.PeriodicTrigger != nil || input.PeriodicCompletionDelaySeconds != nil || input.PeriodicMaxDurationSeconds != nil {
		periodicStore := store.Periodic(input.ConversationID)

		// Check if this is an update to existing periodic config or a new setup
		existing, existErr := periodicStore.Get()
		isNew := existErr != nil || existing == nil

		if isNew {
			// Resolve the trigger (default schedule). onCompletion does not require a frequency.
			trigger := session.TriggerSchedule
			if input.PeriodicTrigger != nil {
				trigger = session.PeriodicTrigger(*input.PeriodicTrigger)
			}
			switch trigger {
			case "", session.TriggerSchedule, session.TriggerOnCompletion:
				// valid
			default:
				return nil, ConversationUpdateOutput{
					Success: false,
					Error:   "periodic_trigger must be 'schedule' or 'onCompletion'",
				}, nil
			}
			isOnCompletion := trigger == session.TriggerOnCompletion

			// Creating new periodic config — require the prompt always.
			if input.PeriodicPrompt == nil || *input.PeriodicPrompt == "" {
				return nil, ConversationUpdateOutput{
					Success: false,
					Error:   "periodic_prompt is required when creating new periodic configuration",
				}, nil
			}

			var freq session.Frequency
			if !isOnCompletion {
				// Schedule trigger: frequency is mandatory.
				if input.PeriodicFrequencyValue == nil || *input.PeriodicFrequencyValue < 1 {
					return nil, ConversationUpdateOutput{
						Success: false,
						Error:   "periodic_frequency_value (>= 1) is required when creating new periodic configuration",
					}, nil
				}
				if input.PeriodicFrequencyUnit == nil || *input.PeriodicFrequencyUnit == "" {
					return nil, ConversationUpdateOutput{
						Success: false,
						Error:   "periodic_frequency_unit is required when creating new periodic configuration",
					}, nil
				}

				var freqUnit session.FrequencyUnit
				switch *input.PeriodicFrequencyUnit {
				case "minutes":
					freqUnit = session.FrequencyMinutes
				case "hours":
					freqUnit = session.FrequencyHours
				case "days":
					freqUnit = session.FrequencyDays
				default:
					return nil, ConversationUpdateOutput{
						Success: false,
						Error:   "periodic_frequency_unit must be 'minutes', 'hours', or 'days'",
					}, nil
				}

				freq = session.Frequency{
					Value: *input.PeriodicFrequencyValue,
					Unit:  freqUnit,
				}
				if input.PeriodicFrequencyAt != nil {
					freq.At = *input.PeriodicFrequencyAt
				}
				if err := freq.Validate(); err != nil {
					return nil, ConversationUpdateOutput{
						Success: false,
						Error:   fmt.Sprintf("invalid periodic frequency: %v", err),
					}, nil
				}
			}

			enabled := true
			if input.PeriodicEnabled != nil {
				enabled = *input.PeriodicEnabled
			}

			freshContext := false
			if input.PeriodicFreshContext != nil {
				freshContext = *input.PeriodicFreshContext
			}

			maxIterations := 0
			if input.PeriodicMaxIterations != nil {
				maxIterations = *input.PeriodicMaxIterations
			}

			delaySeconds := 0
			if input.PeriodicCompletionDelaySeconds != nil {
				delaySeconds = *input.PeriodicCompletionDelaySeconds
			}

			maxDurationSeconds := 0
			if input.PeriodicMaxDurationSeconds != nil {
				maxDurationSeconds = *input.PeriodicMaxDurationSeconds
			}

			periodic := &session.PeriodicPrompt{
				Prompt:             *input.PeriodicPrompt,
				Frequency:          freq,
				Enabled:            enabled,
				FreshContext:       freshContext,
				MaxIterations:      maxIterations,
				Trigger:            trigger,
				DelaySeconds:       delaySeconds,
				MaxDurationSeconds: maxDurationSeconds,
			}
			// Clamp the on-completion delay to the global floor (no-op for schedule).
			periodic.ClampDelay(s.periodicDelayFloor())

			if err := periodicStore.Set(periodic); err != nil {
				return nil, ConversationUpdateOutput{
					Success: false,
					Error:   fmt.Sprintf("failed to set periodic: %v", err),
				}, nil
			}
		} else {
			// Updating existing periodic config — use partial update
			var prompt *string
			var freq *session.Frequency
			var enabled *bool

			if input.PeriodicPrompt != nil {
				prompt = input.PeriodicPrompt
			}

			if input.PeriodicFrequencyValue != nil || input.PeriodicFrequencyUnit != nil || input.PeriodicFrequencyAt != nil {
				// Build frequency from existing + overrides
				f := existing.Frequency
				if input.PeriodicFrequencyValue != nil {
					f.Value = *input.PeriodicFrequencyValue
				}
				if input.PeriodicFrequencyUnit != nil {
					switch *input.PeriodicFrequencyUnit {
					case "minutes":
						f.Unit = session.FrequencyMinutes
					case "hours":
						f.Unit = session.FrequencyHours
					case "days":
						f.Unit = session.FrequencyDays
					default:
						return nil, ConversationUpdateOutput{
							Success: false,
							Error:   "periodic_frequency_unit must be 'minutes', 'hours', or 'days'",
						}, nil
					}
				}
				if input.PeriodicFrequencyAt != nil {
					f.At = *input.PeriodicFrequencyAt
				}
				freq = &f
			}

			if input.PeriodicEnabled != nil {
				enabled = input.PeriodicEnabled
			}

			// On-completion fields (partial). Convert the trigger string to the typed pointer.
			var trigger *session.PeriodicTrigger
			if input.PeriodicTrigger != nil {
				t := session.PeriodicTrigger(*input.PeriodicTrigger)
				trigger = &t
			}
			delaySeconds := input.PeriodicCompletionDelaySeconds

			// Clamp the on-completion delay to the global floor on write. The effective
			// trigger is the patched value when provided, otherwise the stored one.
			if delaySeconds != nil {
				floor := s.periodicDelayFloor()
				if *delaySeconds < floor {
					effTrigger := existing.Trigger
					if trigger != nil {
						effTrigger = *trigger
					}
					if effTrigger == session.TriggerOnCompletion {
						clamped := floor
						delaySeconds = &clamped
					}
				}
			}

			if err := periodicStore.Update(prompt, nil, freq, enabled, input.PeriodicFreshContext, input.PeriodicMaxIterations, trigger, delaySeconds, input.PeriodicMaxDurationSeconds); err != nil {
				return nil, ConversationUpdateOutput{
					Success: false,
					Error:   fmt.Sprintf("failed to update periodic: %v", err),
				}, nil
			}
		}

		updated = append(updated, "periodic")

		// Broadcast the periodic state change so all clients refresh live (parity with REST paths).
		if sm != nil {
			if p, getErr := periodicStore.Get(); getErr == nil {
				sm.BroadcastPeriodicUpdated(input.ConversationID, p)
			}
		}

		// Kick off the very first run for a fresh onCompletion conversation.
		s.mu.RLock()
		runner := s.periodicRunner
		s.mu.RUnlock()
		if runner != nil {
			runner.BootstrapOnCompletion(input.ConversationID)
		}

		// If the session has no title and a periodic prompt was set, trigger title generation.
		if input.Name == nil && meta.Name == "" && sm != nil {
			if bs := sm.GetSession(input.ConversationID); bs != nil {
				var pPrompt, pName string
				if input.PeriodicPrompt != nil {
					pPrompt = *input.PeriodicPrompt
				}
				// ConversationUpdateInput has no PeriodicPromptName field; read prompt name
				// from the stored periodic config so the resolver can be used when inline
				// prompt is empty or the UI placeholder "(pending)".
				if p, getErr := periodicStore.Get(); getErr == nil && p != nil {
					if pPrompt == "" {
						pPrompt = p.Prompt
					}
					pName = p.PromptName
				}
				bs.TriggerTitleGenerationFromPeriodic(pPrompt, pName)
			}
		}

		s.logger.Info("Periodic configuration updated via MCP",
			"source_session", realSessionID,
			"target_conversation", input.ConversationID,
			"is_new", isNew)
	}

	// Check if anything was actually updated
	if len(updated) == 0 {
		return nil, ConversationUpdateOutput{
			Success: false,
			Error:   "no properties to update: specify at least one of 'name', 'beads_issue', 'user_data', or periodic fields",
		}, nil
	}

	// Build output with current state
	output := ConversationUpdateOutput{
		Success:        true,
		ConversationID: input.ConversationID,
		Updated:        updated,
	}

	// Read back current name and beads_issue
	if currentMeta, err := store.GetMetadata(input.ConversationID); err == nil {
		output.Name = currentMeta.Name
		output.BeadsIssue = currentMeta.BeadsIssue
	}

	// Read back current user data
	if currentData, err := store.GetUserData(input.ConversationID); err == nil && currentData != nil {
		for _, a := range currentData.Attributes {
			output.UserData = append(output.UserData, UserDataAttributeUpdate{Name: a.Name, Value: a.Value})
		}
	}

	// Read back current periodic config
	if p, err := store.Periodic(input.ConversationID).Get(); err == nil && p != nil {
		output.PeriodicPrompt = p.Prompt
		output.PeriodicFrequencyValue = p.Frequency.Value
		output.PeriodicFrequencyUnit = string(p.Frequency.Unit)
		output.PeriodicFrequencyAt = p.Frequency.At
		output.PeriodicEnabled = p.Enabled
		output.PeriodicFreshContext = p.FreshContext
		output.PeriodicMaxIterations = p.MaxIterations
		output.PeriodicIterationCount = p.IterationCount
		output.PeriodicTrigger = string(p.EffectiveTrigger())
		output.PeriodicCompletionDelaySeconds = p.DelaySeconds
		output.PeriodicMaxDurationSeconds = p.MaxDurationSeconds
		if p.NextScheduledAt != nil {
			output.PeriodicNextRun = p.NextScheduledAt.Format("2006-01-02T15:04:05Z07:00")
		}
	}

	return nil, output, nil
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

	// Cross-workspace support: if workspace UUID is provided, validate and confirm
	if input.Workspace != "" {
		targetWS := s.sessionManager.GetWorkspaceByUUID(input.Workspace)
		if targetWS == nil {
			return nil, ConversationWaitOutput{
				Error: fmt.Sprintf("workspace not found: %s", input.Workspace),
			}, nil
		}

		s.mu.RLock()
		store := s.store
		s.mu.RUnlock()

		if store != nil {
			// Validate the target conversation belongs to the workspace
			targetMeta, err := store.GetMetadata(input.ConversationID)
			if err == nil && targetMeta.WorkingDir != targetWS.WorkingDir {
				return nil, ConversationWaitOutput{
					Error: fmt.Sprintf("conversation %s does not belong to workspace %s", input.ConversationID, input.Workspace),
				}, nil
			}

			// Check if cross-workspace (caller's workspace differs from target)
			sourceMeta, err := store.GetMetadata(realSessionID)
			if err == nil && sourceMeta.WorkingDir != targetWS.WorkingDir {
				// Permission check: requires can_interact_other_workspaces flag
				if !s.checkSessionFlag(realSessionID, session.FlagCanInteractOtherWorkspaces) {
					return nil, ConversationWaitOutput{
						Error: fmt.Sprintf("cross-workspace operations require the 'Can interact with other workspaces' (%s) flag to be enabled in Advanced Settings",
							session.FlagCanInteractOtherWorkspaces),
					}, nil
				}
				if err := s.confirmCrossWorkspaceOperation(ctx, realSessionID, "wait on a conversation", targetWS); err != nil {
					return nil, ConversationWaitOutput{Error: err.Error()}, nil
				}
			}
		}
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

	// Broadcast that this session is now waiting (shows hourglass in sidebar)
	if s.sessionManager != nil {
		s.sessionManager.BroadcastWaitingForChildren(realSessionID, true)
		defer func() {
			s.sessionManager.BroadcastWaitingForChildren(realSessionID, false)
		}()
	}

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
		stillPrompting := targetBS.IsPrompting()
		var msg string
		if stillPrompting {
			msg = fmt.Sprintf("timed out after %s; the agent is still responding", timeout)
		} else {
			msg = fmt.Sprintf("timed out after %s; the agent has finished responding", timeout)
		}
		s.logger.Warn("Conversation wait timed out",
			"source_session", realSessionID,
			"target_conversation", input.ConversationID,
			"what", input.What,
			"timeout", timeout,
			"still_prompting", stillPrompting)
		return nil, ConversationWaitOutput{
			Success:        true,
			What:           input.What,
			TimedOut:       true,
			StillPrompting: stillPrompting,
			Message:        msg,
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
			// Session is stored or completed (e.g., GC-closed) — try to resume it.
			s.logger.Info("Auto-resuming child session",
				"parent_session", realSessionID,
				"child_session", childID,
				"child_status", string(childMeta.Status))
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

			msg, err := queue.Add(promptText, nil, nil, realSessionID, nil, 0, nil, "")
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

// =============================================================================
// Conversation History Handler
// =============================================================================

const (
	// historyDefaultLastN is the default number of events to return.
	historyDefaultLastN = 50
	// historyMaxLastN is the maximum number of events that can be requested.
	historyMaxLastN = 200
	// historyMaxDataStr is the maximum length of string fields in event data.
	historyMaxDataStr = 2048
)

// handleConversationHistory handles the mitto_conversation_history tool.
// It reads events from a session, applies filters, and returns a paginated result.
func (s *Server) handleConversationHistory(ctx context.Context, req *mcp.CallToolRequest, input ConversationHistoryInput) (*mcp.CallToolResult, ConversationHistoryOutput, error) {
	emptyOut := ConversationHistoryOutput{Events: []ConversationHistoryEvent{}}

	if input.SelfID == "" {
		emptyOut.Error = "self_id is required"
		return nil, emptyOut, nil
	}

	realSessionID := s.resolveSelfIDWithMCP(input.SelfID, req)
	if realSessionID == "" {
		emptyOut.Error = fmt.Sprintf("session not found: self_id '%s' could not be resolved", input.SelfID)
		return nil, emptyOut, nil
	}

	reg := s.getSession(realSessionID)
	if reg == nil {
		emptyOut.Error = fmt.Sprintf("session not found or not running: %s", realSessionID)
		return nil, emptyOut, nil
	}

	targetID := input.ConversationID
	if targetID == "" {
		targetID = realSessionID
	}

	s.mu.RLock()
	store := s.store
	s.mu.RUnlock()

	if store == nil {
		emptyOut.Error = "session store not available"
		return nil, emptyOut, nil
	}

	// Read all events from the target session.
	allEvents, err := store.ReadEvents(targetID)
	if err != nil {
		emptyOut.Error = fmt.Sprintf("failed to read events for session %s: %v", targetID, err)
		return nil, emptyOut, nil
	}

	totalEvents := len(allEvents)

	// Parse optional time-range filters (RFC 3339 absolute, or relative duration "ago").
	var since, until time.Time
	if input.Since != "" {
		t, err := session.ParseHistoryTime(input.Since)
		if err != nil {
			emptyOut.Error = fmt.Sprintf("invalid since: %v", err)
			return nil, emptyOut, nil
		}
		since = t
	}
	if input.Until != "" {
		t, err := session.ParseHistoryTime(input.Until)
		if err != nil {
			emptyOut.Error = fmt.Sprintf("invalid until: %v", err)
			return nil, emptyOut, nil
		}
		until = t
	}

	// Apply filters.
	filtered := historyFilterEvents(allEvents, input, since, until)

	// Apply pagination: last_n and offset.
	// "last_n" means we return the most recent N events.
	// "offset" pages backward: offset=0 returns the last N, offset=N returns the previous N.
	lastN := input.LastN
	if lastN <= 0 {
		lastN = historyDefaultLastN
	} else if lastN > historyMaxLastN {
		lastN = historyMaxLastN
	}

	offset := input.Offset
	if offset < 0 {
		offset = 0
	}

	totalFiltered := len(filtered)

	// Calculate window: from the end of filtered, skipping `offset` items.
	endIdx := totalFiltered - offset
	if endIdx < 0 {
		endIdx = 0
	}
	startIdx := endIdx - lastN
	if startIdx < 0 {
		startIdx = 0
	}
	hasMore := startIdx > 0

	paginated := filtered[startIdx:endIdx]

	// Determine whether to include full event data.
	includeData := input.IncludeData == nil || *input.IncludeData

	// Build response events.
	events := make([]ConversationHistoryEvent, 0, len(paginated))
	for _, e := range paginated {
		histEvent := historyBuildEvent(e, includeData)
		events = append(events, histEvent)
	}

	return nil, ConversationHistoryOutput{
		Success:        true,
		ConversationID: targetID,
		TotalEvents:    totalEvents,
		ReturnedEvents: len(events),
		Events:         events,
		HasMore:        hasMore,
	}, nil
}

// historyFilterEvents applies all configured filters to the event list.
// since and until are optional time-range bounds (zero value means unset):
// events before `since` or after `until` are excluded.
func historyFilterEvents(events []session.Event, input ConversationHistoryInput, since, until time.Time) []session.Event {
	// Build event type set for O(1) lookup.
	var typeSet map[string]bool
	if len(input.EventTypes) > 0 {
		typeSet = make(map[string]bool, len(input.EventTypes))
		for _, t := range input.EventTypes {
			typeSet[t] = true
		}
	}

	textContainsLower := strings.ToLower(input.TextContains)
	textExcludesLower := strings.ToLower(input.TextExcludes)
	toolNameLower := strings.ToLower(input.ToolName)

	var result []session.Event
	for _, e := range events {
		// Seq range filters.
		if input.AfterSeq > 0 && e.Seq <= input.AfterSeq {
			continue
		}
		if input.BeforeSeq > 0 && e.Seq >= input.BeforeSeq {
			continue
		}

		// Time range filters.
		if !since.IsZero() && e.Timestamp.Before(since) {
			continue
		}
		if !until.IsZero() && e.Timestamp.After(until) {
			continue
		}

		// Event type filter.
		if typeSet != nil && !typeSet[string(e.Type)] {
			continue
		}

		// Decode event data (needed for text and tool_name filters).
		data, _ := session.DecodeEventData(e)

		// Tool name filter (only applies to tool_call events).
		if toolNameLower != "" {
			if e.Type != session.EventTypeToolCall {
				continue
			}
			if d, ok := data.(session.ToolCallData); ok {
				if !strings.Contains(strings.ToLower(d.Title), toolNameLower) {
					continue
				}
			}
		}

		// Text content filters.
		if textContainsLower != "" || textExcludesLower != "" {
			text := historyExtractText(e.Type, data)
			lower := strings.ToLower(text)
			if textContainsLower != "" && !strings.Contains(lower, textContainsLower) {
				continue
			}
			if textExcludesLower != "" && strings.Contains(lower, textExcludesLower) {
				continue
			}
		}

		result = append(result, e)
	}

	return result
}

// historyExtractText extracts all searchable text from a decoded event data value.
func historyExtractText(eventType session.EventType, data interface{}) string {
	var parts []string
	switch eventType {
	case session.EventTypeUserPrompt:
		if d, ok := data.(session.UserPromptData); ok {
			parts = append(parts, d.Message)
		}
	case session.EventTypeAgentMessage:
		if d, ok := data.(session.AgentMessageData); ok {
			parts = append(parts, d.Text) // HTML as-is per spec
		}
	case session.EventTypeAgentThought:
		if d, ok := data.(session.AgentThoughtData); ok {
			parts = append(parts, d.Text)
		}
	case session.EventTypeToolCall:
		if d, ok := data.(session.ToolCallData); ok {
			parts = append(parts, d.Title)
			if d.RawInput != nil {
				if b, err := json.Marshal(d.RawInput); err == nil {
					parts = append(parts, string(b))
				}
			}
			if d.RawOutput != nil {
				if b, err := json.Marshal(d.RawOutput); err == nil {
					parts = append(parts, string(b))
				}
			}
		}
	case session.EventTypeToolCallUpdate:
		if d, ok := data.(session.ToolCallUpdateData); ok {
			if d.Title != nil {
				parts = append(parts, *d.Title)
			}
			if d.Status != nil {
				parts = append(parts, *d.Status)
			}
		}
	case session.EventTypePlan:
		if d, ok := data.(session.PlanData); ok {
			for _, entry := range d.Entries {
				parts = append(parts, entry.Content)
			}
		}
	case session.EventTypePermission:
		if d, ok := data.(session.PermissionData); ok {
			parts = append(parts, d.Title, d.SelectedOption, d.Outcome)
		}
	case session.EventTypeFileRead, session.EventTypeFileWrite:
		if d, ok := data.(session.FileOperationData); ok {
			parts = append(parts, d.Path, d.Content)
		}
	case session.EventTypeError:
		if d, ok := data.(session.ErrorData); ok {
			parts = append(parts, d.Message)
		}
	case session.EventTypeSessionStart:
		if d, ok := data.(session.SessionStartData); ok {
			parts = append(parts, d.SessionID)
		}
	case session.EventTypeSessionEnd:
		if d, ok := data.(session.SessionEndData); ok {
			parts = append(parts, d.Reason)
		}
	}
	return strings.Join(parts, " ")
}

// historyBuildEvent constructs a ConversationHistoryEvent from a raw session event.
func historyBuildEvent(e session.Event, includeData bool) ConversationHistoryEvent {
	data, _ := session.DecodeEventData(e)

	hist := ConversationHistoryEvent{
		Seq:       e.Seq,
		Type:      string(e.Type),
		Timestamp: e.Timestamp.Format("2006-01-02T15:04:05Z07:00"),
		Summary:   historyBuildSummary(e.Type, data),
	}

	if includeData {
		hist.Data = historyTruncateData(e.Type, data)
	}

	return hist
}

// historyBuildSummary generates a short human-readable one-liner for an event.
func historyBuildSummary(eventType session.EventType, data interface{}) string {
	switch eventType {
	case session.EventTypeUserPrompt:
		if d, ok := data.(session.UserPromptData); ok {
			return historyTruncateStr(d.Message, 150)
		}
	case session.EventTypeAgentMessage:
		if d, ok := data.(session.AgentMessageData); ok {
			return historyTruncateStr(session.StripHTML(d.Text), 150)
		}
	case session.EventTypeAgentThought:
		if d, ok := data.(session.AgentThoughtData); ok {
			return historyTruncateStr(d.Text, 150)
		}
	case session.EventTypeToolCall:
		if d, ok := data.(session.ToolCallData); ok {
			return fmt.Sprintf("%s [%s]", d.Title, d.Status)
		}
	case session.EventTypeToolCallUpdate:
		if d, ok := data.(session.ToolCallUpdateData); ok {
			title := ""
			if d.Title != nil {
				title = *d.Title
			}
			return fmt.Sprintf("Update: %s", title)
		}
	case session.EventTypePlan:
		if d, ok := data.(session.PlanData); ok {
			return fmt.Sprintf("Plan: %d entries", len(d.Entries))
		}
	case session.EventTypePermission:
		if d, ok := data.(session.PermissionData); ok {
			return fmt.Sprintf("%s: %s", d.Title, d.Outcome)
		}
	case session.EventTypeFileRead:
		if d, ok := data.(session.FileOperationData); ok {
			return fmt.Sprintf("Read: %s", d.Path)
		}
	case session.EventTypeFileWrite:
		if d, ok := data.(session.FileOperationData); ok {
			return fmt.Sprintf("Write: %s", d.Path)
		}
	case session.EventTypeError:
		if d, ok := data.(session.ErrorData); ok {
			return historyTruncateStr(d.Message, 150)
		}
	case session.EventTypeSessionStart:
		return "Session started"
	case session.EventTypeSessionEnd:
		if d, ok := data.(session.SessionEndData); ok {
			return fmt.Sprintf("Session ended: %s", d.Reason)
		}
	}
	return string(eventType)
}

// historyTruncateData returns a copy of event data with long string fields truncated to historyMaxDataStr.
func historyTruncateData(eventType session.EventType, data interface{}) interface{} {
	switch eventType {
	case session.EventTypeUserPrompt:
		if d, ok := data.(session.UserPromptData); ok {
			d.Message = historyTruncateDataStr(d.Message)
			return d
		}
	case session.EventTypeAgentMessage:
		if d, ok := data.(session.AgentMessageData); ok {
			d.Text = historyTruncateDataStr(d.Text)
			return d
		}
	case session.EventTypeError:
		if d, ok := data.(session.ErrorData); ok {
			d.Message = historyTruncateDataStr(d.Message)
			return d
		}
	}
	return data
}

// historyTruncateStr truncates a string to maxLen chars, appending "..." if truncated.
func historyTruncateStr(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen-3] + "..."
}

// historyTruncateDataStr truncates a string to historyMaxDataStr, appending "..." if truncated.
func historyTruncateDataStr(s string) string {
	if len(s) <= historyMaxDataStr {
		return s
	}
	return s[:historyMaxDataStr-3] + "..."
}

// truncateForError truncates a string for inclusion in error messages.
func truncateForError(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}
