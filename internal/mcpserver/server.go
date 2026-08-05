// Package mcpserver provides an MCP (Model Context Protocol) server for Mitto.
// The server exposes tools for inspecting conversations and configuration,
// as well as session-scoped tools for interacting with specific conversations.
// It binds only to 127.0.0.1 for security reasons.
package mcpserver

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/inercia/mitto/internal/beads"
	beadswatcher "github.com/inercia/mitto/internal/beads/watcher"
	"github.com/inercia/mitto/internal/coldstart"
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

	// openSSEStreams counts currently open long-lived MCP Streamable-HTTP GET
	// (keepalive) requests, incremented/decremented around each GET request in
	// mcpRequestLoggingMiddleware. Exposed to the periodic goroutine gauge via
	// coldstart.SetOpenMCPStreamCounter in startSSE (mitto-x3x). Only used in
	// HTTP mode; always 0 in STDIO mode.
	openSSEStreams atomic.Int64

	mu             sync.RWMutex
	store          *session.Store
	config         *config.Config
	promptsCache   *config.PromptsCache
	sessionManager SessionManager
	loopRunner     LoopRunner // Optional — for triggering loop runs via MCP
	running        bool
	shutdown       bool

	// beadsCacheMetricsFn returns a snapshot of the beads read-cache counters.
	// Non-nil only when the --beads-cache flag is on; the mitto_beads_cache_metrics
	// tool is registered only when this is non-nil (mitto-is2.5).
	beadsCacheMetricsFn func() beads.CacheMetrics

	// beadsClient is the beads.Client used by the
	// mitto_conversation_wait beads_issues_reached_state branch to read
	// current statuses. Optional; wired via SetBeadsClient after NewServer.
	beadsClient beads.Client
	// beadsWatcher is the fsnotify-backed .beads/ watcher used by the
	// mitto_conversation_wait beads_issues_reached_state branch to re-evaluate
	// the predicate on debounced change events. Optional; wired via
	// SetBeadsWatcher after NewServer.
	beadsWatcher *beadswatcher.BeadsWatcher

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

	// reuseIssueLocksMu guards reuseIssueLocks (lazily-created keyed mutexes).
	reuseIssueLocksMu sync.Mutex
	// reuseIssueLocks holds one mutex per "workingDir\x00beadsIssue" key. It
	// serializes the reuseIssue find-or-route scan+create/seed sequence in
	// handleConversationStart so two concurrent MCP calls for the same key
	// cannot both miss the scan and create duplicate conversations. Mirrors
	// the HTTP handler's reuseIssueLocks (mitto-bx40). See lockReuseIssue.
	reuseIssueLocks map[string]*sync.Mutex

	// reuseTitleLocksMu guards reuseTitleLocks (lazily-created keyed mutexes).
	reuseTitleLocksMu sync.Mutex
	// reuseTitleLocks holds one mutex per "workingDir\x00title" key. It
	// serializes the reuseTitle find-or-route scan+create/seed sequence in
	// handleConversationStart so two concurrent MCP calls for the same key
	// cannot both miss the scan and create duplicate conversations. Mirrors
	// the HTTP handler's reuseTitleLocks. See lockReuseTitle.
	reuseTitleLocks map[string]*sync.Mutex
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
	// BeadsCacheMetrics, when non-nil, returns a snapshot of the beads read-cache
	// counters. Wired only when the --beads-cache flag is on. Nil means the
	// mitto_beads_cache_metrics tool is not registered (mitto-is2.5).
	BeadsCacheMetrics func() beads.CacheMetrics
	// BeadsClient, when non-nil, is used by the mitto_conversation_wait
	// beads_issues_reached_state branch to read current bd statuses. When nil,
	// that branch fails loudly with a "beads not available" error.
	BeadsClient beads.Client
	// BeadsWatcher, when non-nil, is used by the mitto_conversation_wait
	// beads_issues_reached_state branch to receive debounced fs-notify events
	// on .beads/ directories. When nil, that branch falls back to a periodic
	// polling loop.
	BeadsWatcher *beadswatcher.BeadsWatcher
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
	// BroadcastSessionBeadsIssueUpdated broadcasts a session_beads_issue_updated
	// event to all connected clients when a conversation's linked beads issue
	// ID changes via the mitto_conversation_update MCP tool.
	BroadcastSessionBeadsIssueUpdated(sessionID string, beadsIssue string)
	// BroadcastLoopUpdated broadcasts a loop_updated event to all connected clients.
	BroadcastLoopUpdated(sessionID string, loop *session.LoopPrompt)
	// BroadcastWorkspaceUINotify broadcasts a workspace-scoped notification to
	// all connected clients. Used by the mitto_workspace_ui_notify MCP tool
	// (mitto-6bn) so callers without a registered session — notably auxiliary
	// sessions running close-phase processors — can still surface toasts.
	// The frontend filters incoming notifications by workspace_uuid so users
	// only see toasts for the workspace they are currently viewing.
	BroadcastWorkspaceUINotify(workspaceUUID, workspaceName, workingDir string, req UINotifyRequest)
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
	// IsMCPInitTimeout reports whether err (possibly wrapped) is the transient
	// cold-start "MCP initialization timed out" signal. Auto-resume paths use it
	// to defer to a bounded retry instead of surfacing a hard failure (mitto-54k.6).
	IsMCPInitTimeout(err error) bool
}

// LoopRunner interface for triggering immediate loop prompt delivery.
type LoopRunner interface {
	TriggerNow(sessionID string, resetTimer bool) error
	// BootstrapOnCompletion delivers the very first run of a fresh onCompletion
	// loop conversation (IterationCount==0, LastSentAt==nil). No-op otherwise.
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
	// HasQueuedDeliveryInProgress returns true if a queued message has been popped and is
	// sleeping through a configured delay before dispatch. The session appears idle during
	// this window but will become prompting shortly — do not auto-complete.
	HasQueuedDeliveryInProgress() bool
	// GetQueueConfig returns the queue configuration for this session. May return nil.
	GetQueueConfig() *config.QueueConfig
	// WaitForResponseComplete waits for the current prompt to complete, if one is in progress.
	// Returns true if the prompt completed within the timeout, false if it timed out.
	// If no prompt is in progress, returns immediately with true.
	WaitForResponseComplete(timeout time.Duration) bool
	// TriggerTitleGeneration triggers async title generation if the session has no title yet.
	// Used by MCP tools and API handlers to generate titles for sessions that received
	// prompts via paths that don't normally trigger title generation (e.g., loop config).
	TriggerTitleGeneration(message string)
	// TriggerTitleGenerationFromLoop picks the best source text (prompt text or prompt
	// name) for title generation when a loop config is saved.
	TriggerTitleGenerationFromLoop(prompt, promptName string)
	// RequestSelfDestruct marks the conversation for deletion once the current turn
	// completes. Used by the mitto_conversation_delete tool when an agent requests
	// deletion of its own conversation.
	RequestSelfDestruct()
	// LastQueuedSendError returns the most recent queued-send failure message and its
	// timestamp. Used by the parent wait loop to surface dispatch failures as status=failed.
	LastQueuedSendError() (string, time.Time)
	// RecordChildWait accumulates a completed blocking wait duration for
	// mitto_children_tasks_wait calls made from this session. In-memory only.
	RecordChildWait(d time.Duration)
	// ApplyModelTag resolves a preferred-model tag (same tag-resolution semantics
	// as prompt-level preferredModels) against the agent's advertised model
	// catalog and switches the session's active model via the same SetConfigOption
	// path used by the user's manual model-dropdown click, so the change persists
	// as the new baseline. An empty tag clears any transient prompt-level model
	// override, restoring the caller-selected baseline. Returns the resolved
	// model id on success, "" when the tag was cleared, or an error when the
	// agent has not advertised a model catalog, the tag does not resolve to any
	// available model, or the underlying SetConfigOption call fails. Used by the
	// mitto_conversation_new / _update model_tag path.
	ApplyModelTag(ctx context.Context, tag string) (string, error)
	// ActivePromptDispatch returns the workspace-prompt name and Arguments of
	// the dispatch currently in flight (isPrompting == true). Consumed by the
	// target.reuseCoalesce check so a duplicate identical dispatch onto a busy
	// conversation can be a no-op (mitto-djs1). Returns ok=false when the
	// session is idle. When ok=true and name is empty, the in-flight dispatch
	// is a free-text prompt.
	ActivePromptDispatch() (name string, args map[string]string, ok bool)
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
		beadsCacheMetricsFn:   deps.BeadsCacheMetrics,
		beadsClient:           deps.BeadsClient,
		beadsWatcher:          deps.BeadsWatcher,
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

// lockReuseIssue locks (lazily creating if needed) the mutex for key and
// returns a function that unlocks it. Callers should `defer unlock()`.
// Key format is "workingDir\x00beadsIssue" — see handleConversationStart.
// Mirrors (*Handlers).lockReuseIssue in internal/web/handlers so MCP and
// HTTP paths serialize reuseIssue find-or-route with the same discipline.
func (s *Server) lockReuseIssue(key string) func() {
	s.reuseIssueLocksMu.Lock()
	if s.reuseIssueLocks == nil {
		s.reuseIssueLocks = make(map[string]*sync.Mutex)
	}
	mu, ok := s.reuseIssueLocks[key]
	if !ok {
		mu = &sync.Mutex{}
		s.reuseIssueLocks[key] = mu
	}
	s.reuseIssueLocksMu.Unlock()

	mu.Lock()
	return mu.Unlock
}

// lockReuseTitle locks (lazily creating if needed) the mutex for key and
// returns a function that unlocks it. Callers should `defer unlock()`.
// Key format is "workingDir\x00title" — see handleConversationStart.
// Mirrors (*Handlers).lockReuseTitle in internal/web/handlers so MCP and
// HTTP paths serialize reuseTitle find-or-route with the same discipline.
func (s *Server) lockReuseTitle(key string) func() {
	s.reuseTitleLocksMu.Lock()
	if s.reuseTitleLocks == nil {
		s.reuseTitleLocks = make(map[string]*sync.Mutex)
	}
	mu, ok := s.reuseTitleLocks[key]
	if !ok {
		mu = &sync.Mutex{}
		s.reuseTitleLocks[key] = mu
	}
	s.reuseTitleLocksMu.Unlock()

	mu.Lock()
	return mu.Unlock
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

	// Periodic goroutine gauge (mitto-x3x): expose the open-SSE-stream count
	// as the "open_mcp_sse_streams" contention counter.
	coldstart.SetOpenMCPStreamCounter(func() int { return int(s.openSSEStreams.Load()) })

	// Create HTTP server using Streamable HTTP transport (MCP spec 2025-03-26).
	// This is the modern transport that Augment Agent and other clients use.
	mux := http.NewServeMux()

	// Create Streamable HTTP handler - this handles all MCP communication.
	//
	// JSONResponse:true makes a POST's response (e.g. an agent's initialize /
	// tools/list on cold start) resolve inline as application/json (spec
	// §2.1.5) on the POST itself. The go-sdk default (nil opts => stateful
	// mode) instead lets that response ride the client's standalone SSE GET
	// stream; under concurrent MCP sessions that GET stream can stall, wedging
	// cold-start initialize for minutes (mitto-6hr). This does NOT affect
	// server->client interactions: Mitto's UIPrompter bridges to the UI over
	// WebSocket, not the MCP transport, so mitto_ui_* prompts are unaffected
	// (unlike Stateless:true, which would reject server->client requests).
	streamableHandler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server {
		return s.mcpServer
	}, &mcp.StreamableHTTPOptions{JSONResponse: true})

	// Wrap with request logging so inbound MCP requests (e.g. an agent's
	// initialize / tools/list during cold start) can be correlated with agent
	// stderr and the rest of mitto.log. Without this, a request that the agent
	// reports as "timed out" leaves no trace on the server side, making it
	// impossible to tell whether the request ever actually reached Mitto.
	loggedHandler := s.mcpRequestLoggingMiddleware(streamableHandler)

	// Mount on /mcp (standard endpoint for Streamable HTTP)
	mux.Handle("/mcp", loggedHandler)

	// Also mount on root for convenience
	mux.Handle("/", loggedHandler)

	s.httpSrv = &http.Server{Handler: mux}

	go func() {
		if err := s.httpSrv.Serve(listener); err != nil && err != http.ErrServerClosed {
			s.logger.Error("MCP server error", "error", err)
		}
	}()

	return nil
}

// mcpStatusRecorder wraps http.ResponseWriter to capture the status code
// written by the downstream handler for request logging. It defaults to 200
// when WriteHeader is never called explicitly (net/http's implicit 200).
type mcpStatusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *mcpStatusRecorder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}

func (r *mcpStatusRecorder) Write(b []byte) (int, error) {
	if r.status == 0 {
		r.status = http.StatusOK
	}
	return r.ResponseWriter.Write(b)
}

// Flush forwards to the underlying ResponseWriter's Flusher. This is REQUIRED:
// the streamable HTTP transport type-asserts the ResponseWriter to http.Flusher
// to push SSE events; if the wrapper does not expose Flush, the stream never
// flushes and MCP clients hang waiting for responses.
func (r *mcpStatusRecorder) Flush() {
	if f, ok := r.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// maxMCPBodyPeek caps how many bytes of the request body we buffer to extract
// the JSON-RPC method/id for logging. MCP handshake requests (initialize,
// tools/list) are tiny; this bound protects against a large tools/call payload.
const maxMCPBodyPeek = 8 * 1024

// mcpRequestLoggingMiddleware logs each inbound MCP HTTP request so that an
// agent's requests (initialize / tools/list / tools/call) can be correlated
// with agent stderr and the rest of mitto.log. This is critical for diagnosing
// cold-start hangs where an agent reports "mitto (timed out)": with this log we
// can tell whether the request ever reached Mitto at all, and if so how long it
// took to answer.
//
// The JSON-RPC method and id are peeked from the request body without consuming
// it — the body is buffered (bounded by maxMCPBodyPeek) and replaced so the
// downstream streamable handler sees the full, unmodified stream.
//
// Log level routing: real JSON-RPC calls (initialize, tools/list, tools/call,
// notifications/*) log at INFO because they are the ones that matter for
// cold-start diagnosis. Idle SSE keepalive GET streams (empty body,
// http_method=GET, ~300s duration when they close) and `ping` heartbeats log
// at DEBUG — they are high-volume, low-signal, and would otherwise dominate
// console output. They still appear in the file log when file level is debug
// (the macOS app's default; CLI users can opt in via --debug / --log-level=debug).
func (s *Server) mcpRequestLoggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reqID := uuid.NewString()[:8]
		mcpSessionID := r.Header.Get("Mcp-Session-Id")

		// Peek the JSON-RPC method/id from the body without consuming it.
		var rpcMethod, rpcID string
		var bodyLen int
		if r.Body != nil {
			limited := io.LimitReader(r.Body, maxMCPBodyPeek+1)
			peeked, _ := io.ReadAll(limited)
			bodyLen = len(peeked)
			// Reassemble the body: buffered prefix + any remaining unread bytes.
			if bodyLen > maxMCPBodyPeek {
				r.Body = struct {
					io.Reader
					io.Closer
				}{io.MultiReader(bytes.NewReader(peeked), r.Body), r.Body}
			} else {
				_ = r.Body.Close()
				r.Body = io.NopCloser(bytes.NewReader(peeked))
			}
			rpcMethod, rpcID = parseJSONRPCEnvelope(peeked)
		}

		level := slog.LevelInfo
		if r.Method == http.MethodGet || rpcMethod == "ping" {
			level = slog.LevelDebug
		}

		s.logger.Log(r.Context(), level, "MCP request received",
			"mcp_req_id", reqID,
			"http_method", r.Method,
			"path", r.URL.Path,
			"rpc_method", rpcMethod,
			"rpc_id", rpcID,
			"mcp_session_id", mcpSessionID,
			"remote_addr", r.RemoteAddr,
			"body_bytes", bodyLen,
		)

		// Track long-lived GET (idle SSE keepalive) streams for the periodic
		// goroutine gauge (mitto-x3x): a GET's next.ServeHTTP call blocks for
		// the lifetime of the stream, so bracketing it here counts exactly the
		// streams currently pinning a goroutine.
		if r.Method == http.MethodGet {
			s.openSSEStreams.Add(1)
			defer s.openSSEStreams.Add(-1)
		}

		rec := &mcpStatusRecorder{ResponseWriter: w}
		start := time.Now()
		next.ServeHTTP(rec, r)
		if rec.status == 0 {
			rec.status = http.StatusOK
		}

		s.logger.Log(r.Context(), level, "MCP request completed",
			"mcp_req_id", reqID,
			"rpc_method", rpcMethod,
			"rpc_id", rpcID,
			"status", rec.status,
			"duration_ms", time.Since(start).Milliseconds(),
		)
	})
}

// parseJSONRPCEnvelope extracts the "method" and "id" fields from a JSON-RPC
// request body for logging. It tolerates a batch (array) body by inspecting the
// first element. Returns empty strings when the body is not parseable JSON-RPC.
func parseJSONRPCEnvelope(body []byte) (method, id string) {
	trimmed := bytes.TrimSpace(body)
	if len(trimmed) == 0 {
		return "", ""
	}
	// Batch request: peek the first element.
	if trimmed[0] == '[' {
		var batch []json.RawMessage
		if err := json.Unmarshal(trimmed, &batch); err != nil || len(batch) == 0 {
			return "", ""
		}
		trimmed = batch[0]
	}
	var env struct {
		Method string          `json:"method"`
		ID     json.RawMessage `json:"id"`
	}
	if err := json.Unmarshal(trimmed, &env); err != nil {
		return "", ""
	}
	id = strings.Trim(string(env.ID), `"`)
	return env.Method, id
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

// loopDelayFloor returns the configured global floor for the on-completion loop
// delay. Falls back to the package default when no config is available.
func (s *Server) loopDelayFloor() int {
	if s.config != nil {
		return s.config.Conversations.GetMinLoopCompletionDelaySeconds()
	}
	return config.DefaultMinLoopCompletionDelaySeconds
}

// SetLoopRunner sets the loop runner for triggering loop runs via MCP tools.
// It may be called after NewServer since the loop runner is created after the MCP server.
func (s *Server) SetLoopRunner(runner LoopRunner) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.loopRunner = runner
}

// SetBeadsClient sets the beads.Client used by the
// mitto_conversation_wait beads_issues_reached_state branch. It may be called
// after NewServer since the beads client is created before the MCP server but
// wrapped in a CachingClient later. Passing nil disables the branch.
func (s *Server) SetBeadsClient(c beads.Client) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.beadsClient = c
}

// SetBeadsWatcher sets the BeadsWatcher used by the
// mitto_conversation_wait beads_issues_reached_state branch. It may be called
// after NewServer since the beads watcher is created later in web.Server.Start.
// Passing nil forces the branch to poll instead of subscribing.
func (s *Server) SetBeadsWatcher(w *beadswatcher.BeadsWatcher) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.beadsWatcher = w
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

		// Check if conversation has an active loop prompt
		if p, err := store.Loop(meta.SessionID).Get(); err == nil && p != nil {
			details.IsLoop = p.Enabled
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

// truncateForError truncates a string for inclusion in error messages.
func truncateForError(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}
