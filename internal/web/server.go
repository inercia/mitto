package web

import (
	"context"
	"fmt"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"os"
	"sync"

	"github.com/inercia/mitto/internal/appdir"
	configPkg "github.com/inercia/mitto/internal/config"
	"github.com/inercia/mitto/internal/logging"
	"github.com/inercia/mitto/internal/msghooks"
	"github.com/inercia/mitto/internal/session"
	mittoWeb "github.com/inercia/mitto/web"
)

// Config holds the web server configuration.
type Config struct {
	// Workspaces is the list of configured workspaces (ACP server + directory pairs).
	// If empty, a single workspace is created from ACPCommand/ACPServer/DefaultWorkingDir.
	Workspaces []configPkg.WorkspaceSettings

	// Legacy single-workspace fields (used if Workspaces is empty)
	ACPCommand        string
	ACPServer         string
	DefaultWorkingDir string

	AutoApprove bool
	Debug       bool
	// MittoConfig is the full Mitto configuration (used for /api/config endpoint)
	MittoConfig *configPkg.Config
	// StaticDir is an optional filesystem directory to serve static files from.
	// When set, files are served from this directory instead of the embedded assets.
	// This enables hot-reloading during development (just refresh the browser).
	StaticDir string

	// FromCLI indicates whether workspaces came from CLI flags.
	// When true, workspace changes are NOT persisted to disk.
	FromCLI bool
	// OnWorkspaceSave is called when workspaces are modified (only if FromCLI is false).
	OnWorkspaceSave WorkspaceSaveFunc
	// ConfigReadOnly indicates that configuration was loaded from a custom config file
	// (via --config flag or RC file). When true, the Settings dialog is disabled in the UI.
	ConfigReadOnly bool
	// RCFilePath is the path to the RC file if config was loaded from one.
	// This is used to show the user which file is being used when ConfigReadOnly is true.
	RCFilePath string
	// PromptsCache provides cached access to global prompts from MITTO_DIR/prompts/.
	// If nil, global file prompts are not loaded.
	PromptsCache *configPkg.PromptsCache
	// AccessLog is the configuration for security access logging.
	// If Path is empty, access logging is disabled.
	AccessLog AccessLogConfig
}

// GetWorkspaces returns the effective list of workspaces.
// If Workspaces is empty, creates a single workspace from legacy fields.
func (c *Config) GetWorkspaces() []configPkg.WorkspaceSettings {
	if len(c.Workspaces) > 0 {
		return c.Workspaces
	}
	// Create single workspace from legacy fields
	workDir := c.DefaultWorkingDir
	if workDir == "" {
		workDir, _ = os.Getwd()
	}
	return []configPkg.WorkspaceSettings{{
		ACPServer:  c.ACPServer,
		ACPCommand: c.ACPCommand,
		WorkingDir: workDir,
	}}
}

// GetDefaultWorkspace returns the first (default) workspace.
func (c *Config) GetDefaultWorkspace() *configPkg.WorkspaceSettings {
	workspaces := c.GetWorkspaces()
	if len(workspaces) == 0 {
		return nil
	}
	return &workspaces[0]
}

// GetWorkspaceByDir returns the workspace for a given directory, or nil if not found.
func (c *Config) GetWorkspaceByDir(dir string) *configPkg.WorkspaceSettings {
	for i := range c.Workspaces {
		if c.Workspaces[i].WorkingDir == dir {
			return &c.Workspaces[i]
		}
	}
	// Check legacy fields
	if len(c.Workspaces) == 0 && c.DefaultWorkingDir == dir {
		return &configPkg.WorkspaceSettings{
			ACPServer:  c.ACPServer,
			ACPCommand: c.ACPCommand,
			WorkingDir: dir,
		}
	}
	return nil
}

// Server is the web server for Mitto.
type Server struct {
	config     Config
	httpServer *http.Server
	logger     *slog.Logger
	mu         sync.Mutex
	shutdown   bool

	// apiPrefix is the URL prefix for all API and WebSocket endpoints.
	// Static assets and root landing page are not prefixed.
	apiPrefix string

	// Global events manager for broadcasting session lifecycle events
	eventsManager *GlobalEventsManager

	// Session manager for background sessions that persist across WebSocket disconnects
	sessionManager *SessionManager

	// Session store for persistence (owned by the server, shared across handlers)
	store *session.Store

	// Auth manager for handling authentication (nil if auth is disabled)
	authManager *AuthManager

	// CSRF manager for protecting state-changing requests
	csrfManager *CSRFManager

	// Security components
	rateLimiter       *GeneralRateLimiter
	connectionTracker *ConnectionTracker
	wsSecurityConfig  WebSocketSecurityConfig
	proxyChecker      *TrustedProxyChecker

	// External access listener management
	externalListener   net.Listener
	externalHTTPServer *http.Server // Separate server for external connections (marks them as external)
	externalMu         sync.Mutex
	externalPort       int // Port for external listener (same as main port by default)

	// Queue title worker for generating titles for queued messages
	queueTitleWorker *QueueTitleWorker

	// Access logger for security-relevant events (nil if disabled)
	accessLogger *AccessLogger
}

// APIPrefix returns the URL prefix for all API and WebSocket endpoints.
// This is used by the frontend to construct API URLs.
func (s *Server) APIPrefix() string {
	return s.apiPrefix
}

// NewServer creates a new web server.
func NewServer(config Config) (*Server, error) {
	// Use the global logger from the logging package
	logger := logging.Web()

	// Create session store for persistence
	store, err := session.DefaultStore()
	if err != nil {
		return nil, fmt.Errorf("failed to create session store: %w", err)
	}

	// Cleanup old images on startup
	if removed, err := store.CleanupOldImages(session.ImageCleanupAge, session.ImagePreserveRecent); err != nil {
		logger.Warn("Failed to cleanup old images", "error", err)
	} else if removed > 0 {
		logger.Info("Cleaned up old images on startup", "removed_count", removed)
	}

	// Get API prefix early (needed by session manager for HTTP file links)
	apiPrefix := configPkg.DefaultAPIPrefix
	if config.MittoConfig != nil && config.MittoConfig.Web.APIPrefix != "" {
		apiPrefix = config.MittoConfig.Web.APIPrefix
	}

	// Create session manager with workspace support
	var sessionMgr *SessionManager
	workspaces := config.Workspaces // Use direct field, not GetWorkspaces() which creates legacy workspace
	if len(workspaces) > 0 || !config.FromCLI {
		// Use new options-based constructor for workspace persistence support
		sessionMgr = NewSessionManagerWithOptions(SessionManagerOptions{
			Workspaces:      workspaces,
			AutoApprove:     config.AutoApprove,
			Logger:          logger,
			FromCLI:         config.FromCLI,
			OnWorkspaceSave: config.OnWorkspaceSave,
			APIPrefix:       apiPrefix,
		})
	} else {
		// Legacy single-workspace mode (CLI with no --dir flags and no saved workspaces)
		sessionMgr = NewSessionManager(config.ACPCommand, config.ACPServer, config.AutoApprove, logger)
		sessionMgr.SetAPIPrefix(apiPrefix)
	}
	sessionMgr.SetStore(store)

	// Set global conversations config for message processing
	if config.MittoConfig != nil {
		sessionMgr.SetGlobalConversations(config.MittoConfig.Conversations)
	}

	// Set full MittoConfig for agent-specific lookups
	if config.MittoConfig != nil {
		sessionMgr.SetMittoConfig(config.MittoConfig)
	}

	// Set global restricted runner config for sandboxed execution
	if config.MittoConfig != nil && config.MittoConfig.RestrictedRunners != nil {
		sessionMgr.SetGlobalRestrictedRunners(config.MittoConfig.RestrictedRunners)
		logger.Info("Global restricted runners configured",
			"runner_types", len(config.MittoConfig.RestrictedRunners))
	}

	// Load and set hooks from hooks directory
	if hooksDir, err := appdir.HooksDir(); err == nil {
		hookMgr := msghooks.NewManager(hooksDir, logger)
		if err := hookMgr.Load(); err != nil {
			logger.Warn("Failed to load hooks", "error", err)
		} else if len(hookMgr.Hooks()) > 0 {
			sessionMgr.SetHookManager(hookMgr)
			logger.Info("Loaded hooks", "count", len(hookMgr.Hooks()))
		}
	}

	// Initialize auth manager if auth is configured
	var authMgr *AuthManager
	if config.MittoConfig != nil && config.MittoConfig.Web.Auth != nil {
		authMgr = NewAuthManager(config.MittoConfig.Web.Auth)
		logger.Info("Authentication enabled", "type", "simple")
	}

	// Initialize security components from config
	var securityCfg *configPkg.WebSecurity
	if config.MittoConfig != nil {
		securityCfg = config.MittoConfig.Web.Security
	}

	// Initialize trusted proxy checker
	var proxyChecker *TrustedProxyChecker
	if securityCfg != nil && len(securityCfg.TrustedProxies) > 0 {
		proxyChecker = NewTrustedProxyChecker(securityCfg.TrustedProxies)
		SetDefaultProxyChecker(proxyChecker)
		logger.Info("Trusted proxies configured", "count", len(securityCfg.TrustedProxies))
	}

	// Initialize rate limiter
	rateLimitConfig := DefaultRateLimitConfig()
	if securityCfg != nil {
		if securityCfg.RateLimitRPS > 0 {
			rateLimitConfig.RequestsPerSecond = securityCfg.RateLimitRPS
		}
		if securityCfg.RateLimitBurst > 0 {
			rateLimitConfig.BurstSize = securityCfg.RateLimitBurst
		}
	}
	rateLimiter := NewGeneralRateLimiter(rateLimitConfig)

	// Initialize WebSocket security config
	wsSecurityConfig := DefaultWebSocketSecurityConfig()
	if securityCfg != nil {
		if len(securityCfg.AllowedOrigins) > 0 {
			wsSecurityConfig.AllowedOrigins = securityCfg.AllowedOrigins
		}
		if securityCfg.MaxWSConnectionsPerIP > 0 {
			wsSecurityConfig.MaxConnectionsPerIP = securityCfg.MaxWSConnectionsPerIP
		}
		if securityCfg.MaxWSMessageSize > 0 {
			wsSecurityConfig.MaxMessageSize = securityCfg.MaxWSMessageSize
		}
	}

	// Initialize connection tracker
	connectionTracker := NewConnectionTracker(wsSecurityConfig.MaxConnectionsPerIP)

	// Initialize CSRF manager
	csrfMgr := NewCSRFManager()

	// Set API prefix on auth manager for public path matching
	if authMgr != nil {
		authMgr.SetAPIPrefix(apiPrefix)
	}

	// Set API prefix on CSRF manager for exempt path matching
	csrfMgr.SetAPIPrefix(apiPrefix)

	// Initialize access logger (nil if disabled)
	var accessLogger *AccessLogger
	if config.AccessLog.Path != "" {
		accessLogger = NewAccessLogger(config.AccessLog)
		if accessLogger != nil {
			accessLogger.SetAuthManager(authMgr)
			accessLogger.SetAPIPrefix(apiPrefix)
			logger.Info("Access logging enabled", "path", config.AccessLog.Path)
		}
	}

	eventsManager := NewGlobalEventsManager()

	s := &Server{
		config:            config,
		logger:            logger,
		apiPrefix:         apiPrefix,
		eventsManager:     eventsManager,
		sessionManager:    sessionMgr,
		store:             store,
		authManager:       authMgr,
		csrfManager:       csrfMgr,
		rateLimiter:       rateLimiter,
		connectionTracker: connectionTracker,
		wsSecurityConfig:  wsSecurityConfig,
		proxyChecker:      proxyChecker,
		accessLogger:      accessLogger,
	}

	// Set events manager in session manager for broadcasting
	sessionMgr.SetEventsManager(eventsManager)

	// Initialize queue title worker
	s.queueTitleWorker = NewQueueTitleWorker(store, logger)
	s.queueTitleWorker.OnTitleGenerated = func(sessionID, messageID, title string) {
		// Broadcast title update to all connected clients
		s.eventsManager.Broadcast(WSMsgTypeQueueMessageTitled, map[string]string{
			"session_id": sessionID,
			"message_id": messageID,
			"title":      title,
		})
	}

	// Set up routes
	mux := http.NewServeMux()

	// Auth routes (always register, they handle their own enabled/disabled state)
	// These use the API prefix for security through obscurity
	if authMgr != nil {
		mux.HandleFunc(apiPrefix+"/api/login", authMgr.HandleLogin)
		mux.HandleFunc(apiPrefix+"/api/logout", authMgr.HandleLogout)
	}

	// CSRF token endpoint (always available for getting tokens)
	mux.HandleFunc(apiPrefix+"/api/csrf-token", csrfMgr.HandleCSRFToken)

	// API routes - all use the API prefix for security through obscurity
	mux.HandleFunc(apiPrefix+"/api/sessions", s.handleSessions)
	mux.HandleFunc(apiPrefix+"/api/sessions/running", s.handleRunningSessions)
	mux.HandleFunc(apiPrefix+"/api/sessions/", s.handleSessionDetail)
	mux.HandleFunc(apiPrefix+"/api/workspaces", s.handleWorkspaces)
	mux.HandleFunc(apiPrefix+"/api/workspace-prompts", s.handleWorkspacePrompts)
	mux.HandleFunc(apiPrefix+"/api/config", s.handleConfig)
	mux.HandleFunc(apiPrefix+"/api/supported-runners", s.handleSupportedRunners)
	mux.HandleFunc(apiPrefix+"/api/external-status", s.handleExternalStatus)
	mux.HandleFunc(apiPrefix+"/api/aux/improve-prompt", s.handleImprovePrompt)
	mux.HandleFunc(apiPrefix+"/api/badge-click", s.handleBadgeClick)

	// File server endpoint - serves files from workspace directories (for web browser access)
	fileServer := NewFileServer(sessionMgr, logger)
	mux.Handle(apiPrefix+"/api/files", fileServer)

	// WebSocket endpoints - also use the API prefix
	mux.HandleFunc(apiPrefix+"/api/events", s.handleGlobalEventsWS) // Global events (session lifecycle)

	// Static files: use filesystem directory if specified, otherwise use embedded assets
	var staticFS fs.FS
	if config.StaticDir != "" {
		// Use filesystem directory (enables hot-reloading during development)
		staticFS = os.DirFS(config.StaticDir)
		if logger != nil {
			logger.Info("Serving static files from filesystem", "dir", config.StaticDir)
		}
	} else {
		// Use embedded assets (default, for production)
		var err error
		staticFS, err = fs.Sub(mittoWeb.StaticFS, "static")
		if err != nil {
			return nil, err
		}
	}

	// Serve static files with proper content types and security
	// Mount at both root (/) and API prefix (/mitto/) to support both local and proxied access
	staticHandler := s.staticFileHandler(staticFS)
	mux.Handle("/", staticHandler)
	if apiPrefix != "" {
		// Also serve static files under the API prefix for proxied access
		// e.g., /mitto/viewer.html -> serves viewer.html from static files
		mux.Handle(apiPrefix+"/", http.StripPrefix(apiPrefix, staticHandler))
	}

	// Wrap with auth middleware if enabled
	var handler http.Handler = mux
	if authMgr != nil && authMgr.IsEnabled() {
		handler = authMgr.AuthMiddleware(mux)
	}

	// Wrap with CSRF middleware (applies after auth, before request processing)
	// This protects all state-changing API requests (POST, PUT, PATCH, DELETE)
	handler = csrfMgr.CSRFMiddleware(handler)

	// Wrap with security middlewares (applied in reverse order)
	// 1. Request size limit (1MB max for request bodies)
	handler = requestSizeLimitMiddleware(1 * 1024 * 1024)(handler)

	// 2. Rate limiting for API endpoints
	handler = rateLimiter.Middleware(handler)

	// 3. Request timeout (excludes WebSocket connections)
	handler = requestTimeoutMiddleware(DefaultRequestTimeout)(handler)

	// 4. Security headers (non-CSP headers)
	headerSecurityConfig := DefaultSecurityConfig()
	handler = securityHeadersMiddleware(headerSecurityConfig)(handler)

	// 5. CSP nonce injection for HTML responses
	// This must come after security headers but before hide server info
	// so that CSP headers are properly set with nonces for HTML responses.
	// Also injects the API prefix for frontend JavaScript to use.
	handler = cspNonceMiddlewareWithOptions(cspNonceMiddlewareOptions{
		config:    headerSecurityConfig,
		apiPrefix: apiPrefix,
	})(handler)

	// 6. Hide server info (outermost to catch all responses)
	handler = hideServerInfoMiddleware(handler)

	// Wrap with logging middleware
	handler = s.loggingMiddleware(handler)

	// 7. Access logging for security-relevant events (outermost to capture final status)
	if s.accessLogger != nil {
		handler = s.accessLogger.Middleware(handler)
	}

	s.httpServer = &http.Server{Handler: handler}

	logger.Info("Web server initialized", "acp_server", config.ACPServer, "api_prefix", apiPrefix)

	// Process pending queues from previous server run
	// Run in goroutine to not block server startup
	go func() {
		s.sessionManager.ProcessPendingQueues()
	}()

	return s, nil
}

// Serve starts the HTTP server on the given listener.
func (s *Server) Serve(listener net.Listener) error {
	return s.httpServer.Serve(listener)
}

// Handler returns the HTTP handler for the server.
// This is useful for testing with httptest.Server.
func (s *Server) Handler() http.Handler {
	return s.httpServer.Handler
}

// Shutdown gracefully shuts down the server.
func (s *Server) Shutdown() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.shutdown = true

	// Stop external listener if running
	s.stopExternalListenerLocked()

	// Close all background sessions
	if s.sessionManager != nil {
		s.sessionManager.CloseAll("server_shutdown")
	}

	// Close rate limiter
	if s.rateLimiter != nil {
		s.rateLimiter.Close()
	}

	// Close auth manager (stops rate limiter cleanup)
	if s.authManager != nil {
		s.authManager.Close()
	}

	// Close CSRF manager (stops token cleanup)
	if s.csrfManager != nil {
		s.csrfManager.Close()
	}

	// Close session store
	if s.store != nil {
		s.store.Close()
	}

	// Close queue title worker
	if s.queueTitleWorker != nil {
		s.queueTitleWorker.Close()
	}

	// Close access logger
	if s.accessLogger != nil {
		s.accessLogger.Close()
	}

	return s.httpServer.Shutdown(context.Background())
}

// IsShutdown returns whether the server has been shut down.
func (s *Server) IsShutdown() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.shutdown
}

// Logger returns the server's logger.
func (s *Server) Logger() *slog.Logger {
	return s.logger
}

// Store returns the server's session store.
// This store is owned by the server and should not be closed by callers.
func (s *Server) Store() *session.Store {
	return s.store
}

// loggingMiddleware logs HTTP requests.
func (s *Server) loggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		clientIP := getClientIPWithProxyCheck(r)

		// Log static assets at debug level, others at info level
		if isStaticAsset(path) {
			s.logger.Debug("HTTP request (static)",
				"method", r.Method,
				"path", path,
				"raw_uri", r.RequestURI,
				"client_ip", clientIP,
			)
		} else {
			s.logger.Debug("HTTP request",
				"method", r.Method,
				"path", path,
				"raw_uri", r.RequestURI,
				"client_ip", clientIP,
				"user_agent", r.UserAgent(),
			)
		}

		next.ServeHTTP(w, r)
	})
}

// BroadcastSessionRenamed notifies all connected clients that a session was renamed.
func (s *Server) BroadcastSessionRenamed(sessionID, newName string) {
	s.eventsManager.Broadcast(WSMsgTypeSessionRenamed, map[string]string{
		"session_id": sessionID,
		"name":       newName,
	})

	if s.logger != nil {
		s.logger.Debug("Broadcast session renamed", "session_id", sessionID, "name", newName,
			"clients", s.eventsManager.ClientCount())
	}
}

// BroadcastSessionPinned notifies all connected clients that a session's pinned state changed.
func (s *Server) BroadcastSessionPinned(sessionID string, pinned bool) {
	s.eventsManager.Broadcast(WSMsgTypeSessionPinned, map[string]interface{}{
		"session_id": sessionID,
		"pinned":     pinned,
	})

	if s.logger != nil {
		s.logger.Debug("Broadcast session pinned", "session_id", sessionID, "pinned", pinned,
			"clients", s.eventsManager.ClientCount())
	}
}

// BroadcastSessionDeleted notifies all connected clients that a session was deleted.
func (s *Server) BroadcastSessionDeleted(sessionID string) {
	s.eventsManager.Broadcast(WSMsgTypeSessionDeleted, map[string]string{
		"session_id": sessionID,
	})

	if s.logger != nil {
		s.logger.Debug("Broadcast session deleted", "session_id", sessionID,
			"clients", s.eventsManager.ClientCount())
	}
}
