package web

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/inercia/mitto/internal/auxiliary"
	configPkg "github.com/inercia/mitto/internal/config"
	"github.com/inercia/mitto/internal/logging"
	"github.com/inercia/mitto/internal/session"
	mittoWeb "github.com/inercia/mitto/web"
)

// Config holds the web server configuration.
type Config struct {
	// Workspaces is the list of configured workspaces (ACP server + directory pairs).
	// If empty, a single workspace is created from ACPCommand/ACPServer/DefaultWorkingDir.
	Workspaces []WorkspaceConfig

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
	ConfigReadOnly bool
}

// GetWorkspaces returns the effective list of workspaces.
// If Workspaces is empty, creates a single workspace from legacy fields.
func (c *Config) GetWorkspaces() []WorkspaceConfig {
	if len(c.Workspaces) > 0 {
		return c.Workspaces
	}
	// Create single workspace from legacy fields
	workDir := c.DefaultWorkingDir
	if workDir == "" {
		workDir, _ = os.Getwd()
	}
	return []WorkspaceConfig{{
		ACPServer:  c.ACPServer,
		ACPCommand: c.ACPCommand,
		WorkingDir: workDir,
	}}
}

// GetDefaultWorkspace returns the first (default) workspace.
func (c *Config) GetDefaultWorkspace() *WorkspaceConfig {
	workspaces := c.GetWorkspaces()
	if len(workspaces) == 0 {
		return nil
	}
	return &workspaces[0]
}

// GetWorkspaceByDir returns the workspace for a given directory, or nil if not found.
func (c *Config) GetWorkspaceByDir(dir string) *WorkspaceConfig {
	for i := range c.Workspaces {
		if c.Workspaces[i].WorkingDir == dir {
			return &c.Workspaces[i]
		}
	}
	// Check legacy fields
	if len(c.Workspaces) == 0 && c.DefaultWorkingDir == dir {
		return &WorkspaceConfig{
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

	// Global events manager for broadcasting session lifecycle events
	eventsManager *GlobalEventsManager

	// WebSocket client registry for broadcasting messages (legacy)
	clientsMu sync.RWMutex
	clients   map[*WSClient]struct{}

	// Session manager for background sessions that persist across WebSocket disconnects
	sessionManager *SessionManager

	// Auth manager for handling authentication (nil if auth is disabled)
	authManager *AuthManager

	// Security components
	rateLimiter       *GeneralRateLimiter
	connectionTracker *ConnectionTracker
	wsSecurityConfig  WebSocketSecurityConfig
	proxyChecker      *TrustedProxyChecker

	// External access listener management
	externalListener net.Listener
	externalMu       sync.Mutex
	externalPort     int // Port for external listener (same as main port by default)
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
		})
	} else {
		// Legacy single-workspace mode (CLI with no --dir flags and no saved workspaces)
		sessionMgr = NewSessionManager(config.ACPCommand, config.ACPServer, config.AutoApprove, logger)
	}
	sessionMgr.SetStore(store)

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

	s := &Server{
		config:            config,
		logger:            logger,
		clients:           make(map[*WSClient]struct{}),
		eventsManager:     NewGlobalEventsManager(),
		sessionManager:    sessionMgr,
		authManager:       authMgr,
		rateLimiter:       rateLimiter,
		connectionTracker: connectionTracker,
		wsSecurityConfig:  wsSecurityConfig,
		proxyChecker:      proxyChecker,
	}

	// Set up routes
	mux := http.NewServeMux()

	// Auth routes (always register, they handle their own enabled/disabled state)
	if authMgr != nil {
		mux.HandleFunc("/api/login", authMgr.HandleLogin)
		mux.HandleFunc("/api/logout", authMgr.HandleLogout)
	}

	// API routes
	mux.HandleFunc("/api/sessions", s.handleSessions)
	mux.HandleFunc("/api/sessions/running", s.handleRunningSessions)
	mux.HandleFunc("/api/sessions/", s.handleSessionDetail)
	mux.HandleFunc("/api/workspaces", s.handleWorkspaces)
	mux.HandleFunc("/api/config", s.handleConfig)
	mux.HandleFunc("/api/external-status", s.handleExternalStatus)
	mux.HandleFunc("/api/aux/improve-prompt", s.handleImprovePrompt)

	// WebSocket endpoints
	mux.HandleFunc("/api/events", s.handleGlobalEventsWS) // Global events (session lifecycle)
	mux.HandleFunc("/ws", s.handleWebSocket)              // Legacy endpoint for backward compatibility

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
	mux.Handle("/", s.staticFileHandler(staticFS))

	// Wrap with auth middleware if enabled
	var handler http.Handler = mux
	if authMgr != nil && authMgr.IsEnabled() {
		handler = authMgr.AuthMiddleware(mux)
	}

	// Wrap with security middlewares (applied in reverse order)
	// 1. Request size limit (1MB max for request bodies)
	handler = requestSizeLimitMiddleware(1 * 1024 * 1024)(handler)

	// 2. Rate limiting for API endpoints
	handler = rateLimiter.Middleware(handler)

	// 3. Request timeout (excludes WebSocket connections)
	handler = requestTimeoutMiddleware(DefaultRequestTimeout)(handler)

	// 4. Security headers
	headerSecurityConfig := DefaultSecurityConfig()
	handler = securityHeadersMiddleware(headerSecurityConfig)(handler)

	// 5. Hide server info (outermost to catch all responses)
	handler = hideServerInfoMiddleware(handler)

	// Wrap with logging middleware
	handler = s.loggingMiddleware(handler)

	s.httpServer = &http.Server{Handler: handler}

	logger.Info("Web server initialized", "acp_server", config.ACPServer)

	return s, nil
}

// staticFileHandler wraps the file server to handle content types and security.
// It returns a minimal 404 for unknown files to avoid leaking information.
func (s *Server) staticFileHandler(staticFS fs.FS) http.Handler {
	fileServer := http.FileServer(http.FS(staticFS))

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Clean the path
		path := r.URL.Path
		if path == "/" {
			path = "/index.html"
		}

		// Remove leading slash for fs.Open
		fsPath := path
		if len(fsPath) > 0 && fsPath[0] == '/' {
			fsPath = fsPath[1:]
		}

		// Check if file exists before serving
		f, err := staticFS.Open(fsPath)
		if err != nil {
			// Return minimal 404 - don't reveal application type
			http.Error(w, "Not Found", http.StatusNotFound)
			return
		}
		f.Close()

		// Set cache headers for static assets
		// When using StaticDir (development mode), disable caching to enable hot-reloading
		if s.config.StaticDir != "" {
			w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
			w.Header().Set("Pragma", "no-cache")
			w.Header().Set("Expires", "0")
		} else if r.URL.Path != "/" && r.URL.Path != "/index.html" {
			w.Header().Set("Cache-Control", "public, max-age=31536000")
		}

		fileServer.ServeHTTP(w, r)
	})
}

// Serve starts the HTTP server on the given listener.
func (s *Server) Serve(listener net.Listener) error {
	return s.httpServer.Serve(listener)
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

// SetExternalPort sets the port to use for external access.
// This should be called before starting the external listener.
func (s *Server) SetExternalPort(port int) {
	s.externalMu.Lock()
	defer s.externalMu.Unlock()
	s.externalPort = port
}

// StartExternalListener starts a listener on 0.0.0.0 for external access.
// This allows external connections while keeping the main listener on 127.0.0.1.
// If port is 0, a random available port is selected.
// Returns the actual port used, or 0 and an error on failure.
// Returns 0 without error if external listener is already running.
func (s *Server) StartExternalListener(port int) (int, error) {
	s.externalMu.Lock()
	defer s.externalMu.Unlock()

	// Already running
	if s.externalListener != nil {
		return s.externalPort, nil
	}

	addr := fmt.Sprintf("0.0.0.0:%d", port)
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return 0, fmt.Errorf("failed to start external listener on %s: %w", addr, err)
	}

	// Get actual port (may differ if port was 0 for random selection)
	actualPort := listener.Addr().(*net.TCPAddr).Port

	s.externalListener = listener
	s.externalPort = actualPort

	// Serve on the external listener in a goroutine
	go func() {
		if err := s.httpServer.Serve(listener); err != nil {
			// Ignore errors if we're shutting down or the listener was closed
			s.externalMu.Lock()
			isShuttingDown := s.externalListener == nil
			s.externalMu.Unlock()

			if !isShuttingDown && err != http.ErrServerClosed {
				if s.logger != nil {
					s.logger.Error("External listener error", "error", err)
				}
			}
		}
	}()

	if s.logger != nil {
		s.logger.Info("External access enabled", "address", fmt.Sprintf("0.0.0.0:%d", actualPort))
	}

	return actualPort, nil
}

// StopExternalListener stops the external listener if running.
func (s *Server) StopExternalListener() {
	s.externalMu.Lock()
	defer s.externalMu.Unlock()
	s.stopExternalListenerLocked()
}

// stopExternalListenerLocked stops the external listener (must hold externalMu).
func (s *Server) stopExternalListenerLocked() {
	if s.externalListener != nil {
		if err := s.externalListener.Close(); err != nil {
			if s.logger != nil {
				s.logger.Debug("Error closing external listener", "error", err)
			}
		}
		if s.logger != nil {
			s.logger.Info("External access disabled")
		}
		s.externalListener = nil
	}
}

// IsExternalListenerRunning returns whether the external listener is currently running.
func (s *Server) IsExternalListenerRunning() bool {
	s.externalMu.Lock()
	defer s.externalMu.Unlock()
	return s.externalListener != nil
}

// GetExternalPort returns the port used for external access.
func (s *Server) GetExternalPort() int {
	s.externalMu.Lock()
	defer s.externalMu.Unlock()
	return s.externalPort
}

// loggingMiddleware logs HTTP requests.
func (s *Server) loggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Skip logging for static assets to reduce noise
		path := r.URL.Path
		if isStaticAsset(path) {
			next.ServeHTTP(w, r)
			return
		}

		// Log the request
		clientIP := getClientIP(r)
		s.logger.Info("HTTP request",
			"method", r.Method,
			"path", path,
			"client_ip", clientIP,
			"user_agent", r.UserAgent(),
		)

		next.ServeHTTP(w, r)
	})
}

// isStaticAsset returns true if the path is a static asset.
func isStaticAsset(path string) bool {
	staticExtensions := []string{".js", ".css", ".png", ".jpg", ".jpeg", ".gif", ".ico", ".svg", ".woff", ".woff2", ".ttf"}
	for _, ext := range staticExtensions {
		if len(path) > len(ext) && path[len(path)-len(ext):] == ext {
			return true
		}
	}
	return false
}

// registerClient adds a WebSocket client to the registry.
func (s *Server) registerClient(client *WSClient) {
	s.clientsMu.Lock()
	defer s.clientsMu.Unlock()
	s.clients[client] = struct{}{}
}

// unregisterClient removes a WebSocket client from the registry.
func (s *Server) unregisterClient(client *WSClient) {
	s.clientsMu.Lock()
	defer s.clientsMu.Unlock()
	delete(s.clients, client)
}

// BroadcastSessionRenamed notifies all connected clients that a session was renamed.
func (s *Server) BroadcastSessionRenamed(sessionID, newName string) {
	// Broadcast via global events manager (new architecture)
	s.eventsManager.Broadcast(WSMsgTypeSessionRenamed, map[string]string{
		"session_id": sessionID,
		"name":       newName,
	})

	// Also broadcast to legacy clients
	s.clientsMu.RLock()
	defer s.clientsMu.RUnlock()

	for client := range s.clients {
		client.sendMessage(WSMsgTypeSessionRenamed, map[string]string{
			"session_id": sessionID,
			"name":       newName,
		})
	}

	if s.logger != nil {
		s.logger.Debug("Broadcast session renamed", "session_id", sessionID, "name", newName,
			"events_clients", s.eventsManager.ClientCount(), "legacy_clients", len(s.clients))
	}
}

// BroadcastSessionDeleted notifies all connected clients that a session was deleted.
func (s *Server) BroadcastSessionDeleted(sessionID string) {
	// Broadcast via global events manager (new architecture)
	s.eventsManager.Broadcast(WSMsgTypeSessionDeleted, map[string]string{
		"session_id": sessionID,
	})

	// Also broadcast to legacy clients
	s.clientsMu.RLock()
	defer s.clientsMu.RUnlock()

	for client := range s.clients {
		client.sendMessage(WSMsgTypeSessionDeleted, map[string]string{
			"session_id": sessionID,
		})
	}

	if s.logger != nil {
		s.logger.Debug("Broadcast session deleted", "session_id", sessionID,
			"events_clients", s.eventsManager.ClientCount(), "legacy_clients", len(s.clients))
	}
}

// handleConfig handles GET and POST /api/config.
func (s *Server) handleConfig(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		s.handleGetConfig(w, r)
	case http.MethodPost:
		s.handleSaveConfig(w, r)
	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

// handleGetConfig handles GET /api/config.
func (s *Server) handleGetConfig(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	// Build complete config response including workspaces and ACP servers
	response := map[string]interface{}{
		"workspaces":      s.sessionManager.GetWorkspaces(),
		"acp_servers":     []map[string]string{},
		"web":             configPkg.WebConfig{},
		"config_readonly": s.config.ConfigReadOnly,
	}

	if s.config.MittoConfig != nil {
		response["web"] = s.config.MittoConfig.Web
		response["ui"] = s.config.MittoConfig.UI

		// Convert ACP servers to JSON-friendly format (including per-server prompts)
		acpServers := make([]map[string]interface{}, len(s.config.MittoConfig.ACPServers))
		for i, srv := range s.config.MittoConfig.ACPServers {
			acpServers[i] = map[string]interface{}{
				"name":    srv.Name,
				"command": srv.Command,
			}
			// Include prompts if present
			if len(srv.Prompts) > 0 {
				acpServers[i]["prompts"] = srv.Prompts
			}
		}
		response["acp_servers"] = acpServers
	}

	json.NewEncoder(w).Encode(response)
}

// ConfigSaveRequest represents the request body for saving configuration.
type ConfigSaveRequest struct {
	Workspaces []WorkspaceConfig `json:"workspaces"`
	ACPServers []struct {
		Name    string                `json:"name"`
		Command string                `json:"command"`
		Prompts []configPkg.WebPrompt `json:"prompts,omitempty"`
	} `json:"acp_servers"`
	Web struct {
		Host         string `json:"host,omitempty"`
		ExternalPort int    `json:"external_port,omitempty"`
		Auth         *struct {
			Simple *struct {
				Username string `json:"username"`
				Password string `json:"password"`
			} `json:"simple,omitempty"`
		} `json:"auth,omitempty"`
		Prompts []configPkg.WebPrompt `json:"prompts,omitempty"`
	} `json:"web"`
	UI *configPkg.UIConfig `json:"ui,omitempty"`
}

// handleSaveConfig handles POST /api/config.
func (s *Server) handleSaveConfig(w http.ResponseWriter, r *http.Request) {
	// Reject saves when config is read-only (loaded from --config file)
	if s.config.ConfigReadOnly {
		http.Error(w, "Configuration is read-only (loaded from config file)", http.StatusForbidden)
		return
	}

	var req ConfigSaveRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body: "+err.Error(), http.StatusBadRequest)
		return
	}

	// Validate request structure
	if validationErr := s.validateConfigRequest(&req); validationErr != nil {
		s.writeConfigError(w, validationErr)
		return
	}

	// Check for workspace conflicts (workspaces being removed that have conversations)
	if conflictErr := s.checkWorkspaceConflicts(&req); conflictErr != nil {
		s.writeConfigError(w, conflictErr)
		return
	}

	// Build new settings
	settings := s.buildNewSettings(&req)

	// Save settings to disk
	if err := configPkg.SaveSettings(settings); err != nil {
		if s.logger != nil {
			s.logger.Error("Failed to save settings", "error", err)
		}
		http.Error(w, "Failed to save settings: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// Apply changes to running server
	s.applyConfigChanges(&req, settings)

	// Build response with applied changes info
	response := map[string]interface{}{
		"success": true,
		"message": "Configuration saved successfully",
		"applied": map[string]interface{}{
			"external_access_enabled": s.IsExternalListenerRunning(),
			"external_port":           s.GetExternalPort(),
			"auth_enabled":            s.authManager != nil && s.authManager.IsEnabled(),
		},
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

// handleImprovePrompt handles POST /api/aux/improve-prompt.
// It uses the auxiliary ACP session to improve a user's prompt.
func (s *Server) handleImprovePrompt(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Parse request body
	var req struct {
		Prompt string `json:"prompt"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.logger.Error("Failed to decode improve-prompt request", "error", err)
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	if req.Prompt == "" {
		http.Error(w, "Prompt is required", http.StatusBadRequest)
		return
	}

	// Check if auxiliary manager is initialized
	if auxiliary.GetManager() == nil {
		s.logger.Error("Auxiliary manager not initialized")
		http.Error(w, "Service unavailable", http.StatusServiceUnavailable)
		return
	}

	// Create a context with timeout for the auxiliary request
	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()

	// Call the auxiliary package to improve the prompt
	improved, err := auxiliary.ImprovePrompt(ctx, req.Prompt)
	if err != nil {
		s.logger.Error("Failed to improve prompt", "error", err)
		http.Error(w, "Failed to improve prompt", http.StatusInternalServerError)
		return
	}

	// Return the improved prompt
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{
		"improved_prompt": improved,
	})
}
