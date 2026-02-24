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
	"time"

	"github.com/inercia/mitto/internal/appdir"
	configPkg "github.com/inercia/mitto/internal/config"
	"github.com/inercia/mitto/internal/defense"
	"github.com/inercia/mitto/internal/logging"
	"github.com/inercia/mitto/internal/mcpserver"
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
	// (via --config flag). When true, the Settings dialog is disabled in the UI.
	// Note: RC file config is NOT fully read-only anymore - users can add servers via UI.
	ConfigReadOnly bool
	// RCFilePath is the path to the RC file if config was loaded from one.
	// This is used to show the user which file is being used.
	RCFilePath string
	// HasRCFileServers indicates whether any ACP servers came from the RC file.
	// When true, those servers are marked as read-only in the UI (cannot edit/delete).
	HasRCFileServers bool
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

	// Periodic runner for scheduled prompt delivery
	periodicRunner *PeriodicRunner

	// Access logger for security-relevant events (nil if disabled)
	accessLogger *AccessLogger

	// MCP debug server for exposing debugging tools
	mcpServer *mcpserver.Server

	// Scanner defense for blocking malicious IPs at the connection level
	defense *defense.ScannerDefense

	// Prompts watcher for monitoring prompt file changes
	promptsWatcher *configPkg.PromptsWatcher
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

	// Run data migrations before any other operations
	migrationCtx := buildMigrationContext(config.MittoConfig)
	if err := store.RunMigrations(migrationCtx); err != nil {
		logger.Warn("Failed to run migrations", "error", err)
		// Continue anyway - migrations are best-effort
	}

	// Cleanup old images on startup
	if removed, err := store.CleanupOldImages(session.ImageCleanupAge, session.ImagePreserveRecent); err != nil {
		logger.Warn("Failed to cleanup old images", "error", err)
	} else if removed > 0 {
		logger.Info("Cleaned up old images on startup", "removed_count", removed)
	}

	// Cleanup old files on startup
	if removed, err := store.CleanupOldFiles(session.FileCleanupAge, session.FilePreserveRecent); err != nil {
		logger.Warn("Failed to cleanup old files", "error", err)
	} else if removed > 0 {
		logger.Info("Cleaned up old files on startup", "removed_count", removed)
	}

	// Cleanup archived sessions on startup based on retention policy
	if config.MittoConfig != nil && config.MittoConfig.Session != nil {
		retentionPeriod := config.MittoConfig.Session.GetArchiveRetentionPeriod()
		if removed, err := store.CleanupArchivedSessions(retentionPeriod); err != nil {
			logger.Warn("Failed to cleanup archived sessions", "error", err)
		} else if removed > 0 {
			logger.Info("Cleaned up archived sessions on startup", "removed_count", removed, "retention_period", retentionPeriod)
		}
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

	// Initialize scanner defense
	// Enabled by default when external access is configured (ExternalPort >= 0)
	var scannerDefense *defense.ScannerDefense
	var webConfig *configPkg.WebConfig
	if config.MittoConfig != nil {
		webConfig = &config.MittoConfig.Web
	}
	if shouldEnableScannerDefense(webConfig) {
		defenseConfig := configToDefenseConfig(getScannerDefenseConfig(webConfig), true)
		var err error
		scannerDefense, err = defense.New(defenseConfig, logger)
		if err != nil {
			logger.Warn("Failed to initialize scanner defense", "error", err)
			// Continue without defense - graceful degradation
		} else {
			logger.Info("Scanner defense enabled",
				"component", "defense",
				"blocked_ips", scannerDefense.BlockedCount(),
				"external_port", config.MittoConfig.Web.ExternalPort,
			)
		}
	}

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
		defense:           scannerDefense,
	}

	// Set events manager in session manager for broadcasting
	sessionMgr.SetEventsManager(eventsManager)

	// Initialize MCP server.
	// This serves both global tools and session-scoped tools.
	// Check if MCP server is enabled (default: true)
	// Guard against nil config (can happen in tests)
	mcpEnabled := config.MittoConfig != nil && config.MittoConfig.MCP.IsEnabled()
	if mcpEnabled {
		// Get MCP host and port from config, or use defaults
		mcpHost := config.MittoConfig.MCP.GetHost()
		mcpPort := config.MittoConfig.MCP.GetPort()

		mcpSrv, err := mcpserver.NewServer(
			mcpserver.Config{Host: mcpHost, Port: mcpPort},
			mcpserver.Dependencies{
				Store:          store,
				Config:         config.MittoConfig,
				SessionManager: &sessionManagerAdapter{sm: sessionMgr},
			},
		)
		if err != nil {
			logger.Warn("Failed to create MCP server", "error", err)
		} else {
			s.mcpServer = mcpSrv
			if err := mcpSrv.Start(context.Background()); err != nil {
				logger.Warn("Failed to start MCP server", "error", err)
			} else {
				logger.Info("MCP server started", "port", mcpSrv.Port())
			}
			// Pass MCP server to session manager for session registration
			sessionMgr.SetGlobalMCPServer(mcpSrv)
		}
	} else {
		logger.Info("MCP server disabled by configuration")
	}

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

	// Initialize periodic runner for scheduled prompt delivery
	s.periodicRunner = NewPeriodicRunner(store, sessionMgr, logger)
	s.periodicRunner.SetOnPeriodicStarted(s.BroadcastPeriodicStarted)
	s.periodicRunner.Start()

	// Initialize prompts watcher for monitoring prompt file changes
	if promptsWatcher, err := configPkg.NewPromptsWatcher(logger); err != nil {
		logger.Warn("Failed to create prompts watcher", "error", err)
	} else {
		s.promptsWatcher = promptsWatcher
		// Subscribe the server to receive prompts change notifications
		// The server will broadcast these to all connected clients
		s.promptsWatcher.Subscribe(s, s.getPromptsWatchDirs())
		s.promptsWatcher.Start()
		logger.Info("Prompts watcher started", "dirs", s.getPromptsWatchDirs())
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
	mux.HandleFunc(apiPrefix+"/api/workspace/user-data-schema", s.handleWorkspaceUserDataSchema)
	mux.HandleFunc(apiPrefix+"/api/config", s.handleConfig)
	mux.HandleFunc(apiPrefix+"/api/supported-runners", s.handleSupportedRunners)
	mux.HandleFunc(apiPrefix+"/api/advanced-flags", s.handleAdvancedFlags)
	mux.HandleFunc(apiPrefix+"/api/external-status", s.handleExternalStatus)
	mux.HandleFunc(apiPrefix+"/api/aux/improve-prompt", s.handleImprovePrompt)
	mux.HandleFunc(apiPrefix+"/api/badge-click", s.handleBadgeClick)
	mux.HandleFunc(apiPrefix+"/api/ui-preferences", s.handleUIPreferences)

	// M3: Health check endpoint for load balancer integration and monitoring
	// This endpoint is intentionally NOT behind auth to allow health checks
	mux.HandleFunc(apiPrefix+"/api/health", s.handleHealthCheck)

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
	allowExternalImages := false
	if config.MittoConfig != nil && config.MittoConfig.Conversations != nil {
		allowExternalImages = config.MittoConfig.Conversations.AreExternalImagesEnabled()
	}
	handler = cspNonceMiddlewareWithOptions(cspNonceMiddlewareOptions{
		config:              headerSecurityConfig,
		apiPrefix:           apiPrefix,
		allowExternalImages: allowExternalImages,
	})(handler)

	// 6. Gzip compression for external connections only
	// Compresses text/html, text/css, application/javascript, application/json, etc.
	// Skips compression for localhost (no network benefit, just CPU overhead)
	// Skips WebSocket upgrades (they use permessage-deflate compression)
	handler = gzipMiddleware(handler)

	// 7. Hide server info (outermost to catch all responses)
	handler = hideServerInfoMiddleware(handler)

	// Wrap with logging middleware
	handler = s.loggingMiddleware(handler)

	// 8. Access logging for security-relevant events (outermost to capture final status)
	if s.accessLogger != nil {
		handler = s.accessLogger.Middleware(handler)
	}

	// 9. Defense recording middleware for request analysis
	if s.defense != nil {
		handler = s.defenseRecordingMiddleware(handler)
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
// Note: Scanner defense is only applied to the external listener, not this local listener.
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

	// Stop periodic runner
	if s.periodicRunner != nil {
		s.periodicRunner.Stop()
	}

	// Close access logger
	if s.accessLogger != nil {
		s.accessLogger.Close()
	}

	// Stop MCP debug server
	if s.mcpServer != nil {
		s.mcpServer.Stop()
	}

	// Close scanner defense (persists blocklist)
	if s.defense != nil {
		s.defense.Close()
	}

	// Close prompts watcher
	if s.promptsWatcher != nil {
		s.promptsWatcher.Close()
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

// handleHealthCheck handles the health check endpoint for load balancer integration.
// M3: This endpoint returns server health status and basic metrics.
// It is intentionally NOT behind authentication to allow health checks from load balancers.
func (s *Server) handleHealthCheck(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Check if server is shutting down
	if s.IsShutdown() {
		writeJSON(w, http.StatusServiceUnavailable, map[string]interface{}{
			"status":  "unhealthy",
			"reason":  "server_shutting_down",
			"message": "Server is shutting down",
		})
		return
	}

	// Gather health metrics
	response := map[string]interface{}{
		"status":    "healthy",
		"timestamp": time.Now().UTC().Format(time.RFC3339),
	}

	// Add session metrics if session manager is available
	if s.sessionManager != nil {
		activeSessions := s.sessionManager.ActiveSessionCount()
		promptingSessions := s.sessionManager.PromptingSessionCount()
		response["sessions"] = map[string]interface{}{
			"active":    activeSessions,
			"prompting": promptingSessions,
		}
	}

	// Add store metrics if available
	if s.store != nil {
		storedCount, err := s.store.CountSessions()
		if err == nil {
			response["stored_sessions"] = storedCount
		}
	}

	writeJSONOK(w, response)
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

// BroadcastSessionArchived notifies all connected clients that a session's archived state changed.
func (s *Server) BroadcastSessionArchived(sessionID string, archived bool) {
	s.eventsManager.Broadcast(WSMsgTypeSessionArchived, map[string]interface{}{
		"session_id": sessionID,
		"archived":   archived,
	})

	if s.logger != nil {
		s.logger.Debug("Broadcast session archived", "session_id", sessionID, "archived", archived,
			"clients", s.eventsManager.ClientCount())
	}
}

// BroadcastSessionSettingsUpdated notifies all connected clients that a session's advanced settings changed.
func (s *Server) BroadcastSessionSettingsUpdated(sessionID string, settings map[string]bool) {
	// Ensure we send an empty object instead of null for nil maps
	if settings == nil {
		settings = map[string]bool{}
	}
	s.eventsManager.Broadcast(WSMsgTypeSessionSettingsUpdated, map[string]interface{}{
		"session_id": sessionID,
		"settings":   settings,
	})

	if s.logger != nil {
		s.logger.Debug("Broadcast session settings updated", "session_id", sessionID,
			"settings_count", len(settings), "clients", s.eventsManager.ClientCount())
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

// BroadcastPeriodicUpdated notifies all connected clients that a session's periodic state changed.
// This includes the full periodic config so clients can update their frequency panels.
//
// The broadcast includes:
// - periodic_configured: true if a periodic config exists (controls UI mode)
// - periodic_enabled: true if periodic runs are active (controls lock state)
func (s *Server) BroadcastPeriodicUpdated(sessionID string, periodic *session.PeriodicPrompt) {
	data := map[string]interface{}{
		"session_id": sessionID,
	}

	if periodic != nil {
		// periodic_configured: true means the session is in periodic mode (shows periodic UI)
		data["periodic_configured"] = true
		// periodic_enabled: true means periodic runs are active (locked state)
		data["periodic_enabled"] = periodic.Enabled
		data["frequency"] = map[string]interface{}{
			"value": periodic.Frequency.Value,
			"unit":  periodic.Frequency.Unit,
		}
		if periodic.Frequency.At != "" {
			data["frequency"].(map[string]interface{})["at"] = periodic.Frequency.At
		}
		if periodic.NextScheduledAt != nil && !periodic.NextScheduledAt.IsZero() {
			data["next_scheduled_at"] = periodic.NextScheduledAt.Format(time.RFC3339)
		}
	} else {
		// No periodic config - session is not in periodic mode
		data["periodic_configured"] = false
		data["periodic_enabled"] = false
	}

	s.eventsManager.Broadcast(WSMsgTypePeriodicUpdated, data)

	if s.logger != nil {
		configured := periodic != nil
		enabled := periodic != nil && periodic.Enabled
		s.logger.Debug("Broadcast periodic updated", "session_id", sessionID,
			"periodic_configured", configured, "periodic_enabled", enabled,
			"clients", s.eventsManager.ClientCount())
	}
}

// BroadcastSessionStreaming notifies all connected clients that a session's streaming state changed.
// This is called when a session starts (user sends a prompt) or stops streaming (agent completes).
func (s *Server) BroadcastSessionStreaming(sessionID string, isStreaming bool) {
	s.eventsManager.Broadcast(WSMsgTypeSessionStreaming, map[string]interface{}{
		"session_id":   sessionID,
		"is_streaming": isStreaming,
	})

	if s.logger != nil {
		s.logger.Debug("Broadcast session streaming", "session_id", sessionID, "is_streaming", isStreaming,
			"clients", s.eventsManager.ClientCount())
	}
}

// BroadcastACPStopped notifies all connected clients that an ACP connection was stopped.
func (s *Server) BroadcastACPStopped(sessionID, reason string) {
	s.eventsManager.Broadcast(WSMsgTypeACPStopped, map[string]string{
		"session_id": sessionID,
		"reason":     reason,
	})

	if s.logger != nil {
		s.logger.Debug("Broadcast ACP stopped", "session_id", sessionID, "reason", reason,
			"clients", s.eventsManager.ClientCount())
	}
}

// BroadcastACPStarted notifies all connected clients that an ACP connection was started.
func (s *Server) BroadcastACPStarted(sessionID string) {
	s.eventsManager.Broadcast(WSMsgTypeACPStarted, map[string]string{
		"session_id": sessionID,
	})

	if s.logger != nil {
		s.logger.Debug("Broadcast ACP started", "session_id", sessionID,
			"clients", s.eventsManager.ClientCount())
	}
}

// BroadcastACPStartFailed notifies all connected clients that an ACP connection failed to start.
func (s *Server) BroadcastACPStartFailed(sessionID, errorMsg string) {
	if s.eventsManager == nil {
		return
	}

	s.eventsManager.Broadcast(WSMsgTypeACPStartFailed, map[string]string{
		"session_id": sessionID,
		"error":      errorMsg,
	})

	if s.logger != nil {
		s.logger.Warn("Broadcast ACP start failed", "session_id", sessionID, "error", errorMsg,
			"clients", s.eventsManager.ClientCount())
	}
}

// BroadcastPeriodicStarted notifies all connected clients that a periodic prompt was delivered.
// This allows the frontend to show a toast notification and native OS notification.
func (s *Server) BroadcastPeriodicStarted(sessionID, sessionName string) {
	s.eventsManager.Broadcast(WSMsgTypePeriodicStarted, map[string]string{
		"session_id":   sessionID,
		"session_name": sessionName,
	})

	if s.logger != nil {
		s.logger.Info("Broadcast periodic started", "session_id", sessionID, "session_name", sessionName,
			"clients", s.eventsManager.ClientCount())
	}
}

// BroadcastHookFailed notifies all connected clients that a lifecycle hook failed.
// This allows the frontend to show a toast notification about the hook failure.
func (s *Server) BroadcastHookFailed(name string, exitCode int, errorMsg string) {
	s.eventsManager.Broadcast(WSMsgTypeHookFailed, map[string]interface{}{
		"name":      name,
		"exit_code": exitCode,
		"error":     errorMsg,
	})

	if s.logger != nil {
		s.logger.Warn("Broadcast hook failed", "name", name, "exit_code", exitCode, "error", errorMsg,
			"clients", s.eventsManager.ClientCount())
	}
}

// sessionManagerAdapter adapts SessionManager to mcpserver.SessionManager interface.
type sessionManagerAdapter struct {
	sm *SessionManager
}

// GetSession returns a running session by ID.
func (a *sessionManagerAdapter) GetSession(sessionID string) mcpserver.BackgroundSession {
	bs := a.sm.GetSession(sessionID)
	if bs == nil {
		return nil
	}
	return bs
}

// ListRunningSessions returns the IDs of all running sessions.
func (a *sessionManagerAdapter) ListRunningSessions() []string {
	return a.sm.ListRunningSessions()
}

// =============================================================================
// PromptsSubscriber implementation
// =============================================================================

// OnPromptsChanged is called by the PromptsWatcher when prompt files change.
// It broadcasts the change to all connected clients via the global events WebSocket.
func (s *Server) OnPromptsChanged(event configPkg.PromptsChangeEvent) {
	if s.eventsManager == nil {
		return
	}

	// Force reload the prompts cache so next API call gets fresh data
	if s.config.PromptsCache != nil {
		if _, err := s.config.PromptsCache.ForceReload(); err != nil && s.logger != nil {
			s.logger.Warn("Failed to reload prompts cache after file change", "error", err)
		}
	}

	// Broadcast to all connected clients
	s.eventsManager.Broadcast(WSMsgTypePromptsChanged, map[string]interface{}{
		"changed_dirs": event.ChangedDirs,
		"timestamp":    event.Timestamp.Format("2006-01-02T15:04:05Z07:00"),
	})

	if s.logger != nil {
		s.logger.Debug("Broadcasted prompts_changed event",
			"changed_dirs", event.ChangedDirs,
			"client_count", s.eventsManager.ClientCount())
	}
}

// getPromptsWatchDirs returns the directories to watch for prompt file changes.
// This includes the default MITTO_DIR/prompts/ and any additional configured directories.
func (s *Server) getPromptsWatchDirs() []string {
	var dirs []string

	// Always watch the default prompts directory
	if promptsDir, err := appdir.PromptsDir(); err == nil {
		dirs = append(dirs, promptsDir)
	}

	// Add additional directories from global config
	if s.config.MittoConfig != nil && len(s.config.MittoConfig.PromptsDirs) > 0 {
		dirs = append(dirs, s.config.MittoConfig.PromptsDirs...)
	}

	return dirs
}

// buildMigrationContext creates a MigrationContext from the current configuration.
// This provides information needed by migrations to normalize data.
func buildMigrationContext(cfg *configPkg.Config) *session.MigrationContext {
	if cfg == nil || len(cfg.ACPServers) == 0 {
		return nil
	}

	// Extract server names and use the shared helper
	names := make([]string, len(cfg.ACPServers))
	for i, srv := range cfg.ACPServers {
		names[i] = srv.Name
	}
	return session.NewMigrationContext(names)
}
