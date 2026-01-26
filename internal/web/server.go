package web

import (
	"context"
	"encoding/json"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"os"
	"sync"

	"github.com/inercia/mitto/internal/config"
	mittoWeb "github.com/inercia/mitto/web"
)

// Config holds the web server configuration.
type Config struct {
	ACPCommand  string
	ACPServer   string
	AutoApprove bool
	Debug       bool
	// MittoConfig is the full Mitto configuration (used for /api/config endpoint)
	MittoConfig *config.Config
	// StaticDir is an optional filesystem directory to serve static files from.
	// When set, files are served from this directory instead of the embedded assets.
	// This enables hot-reloading during development (just refresh the browser).
	StaticDir string
}

// Server is the web server for Mitto.
type Server struct {
	config     Config
	httpServer *http.Server
	logger     *slog.Logger
	mu         sync.Mutex
	shutdown   bool

	// WebSocket client registry for broadcasting messages
	clientsMu sync.RWMutex
	clients   map[*WSClient]struct{}

	// Session manager for background sessions that persist across WebSocket disconnects
	sessionManager *SessionManager
}

// NewServer creates a new web server.
func NewServer(config Config) (*Server, error) {
	var logger *slog.Logger
	if config.Debug {
		logger = slog.Default()
	}

	s := &Server{
		config:         config,
		logger:         logger,
		clients:        make(map[*WSClient]struct{}),
		sessionManager: NewSessionManager(config.ACPCommand, config.ACPServer, config.AutoApprove, logger),
	}

	// Set up routes
	mux := http.NewServeMux()

	// API routes
	mux.HandleFunc("/api/sessions", s.handleListSessions)
	mux.HandleFunc("/api/sessions/", s.handleSessionDetail)
	mux.HandleFunc("/api/config", s.handleGetConfig)

	// WebSocket endpoint
	mux.HandleFunc("/ws", s.handleWebSocket)

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

	// Serve static files with proper content types
	fileServer := http.FileServer(http.FS(staticFS))
	mux.Handle("/", s.staticFileHandler(fileServer))

	s.httpServer = &http.Server{Handler: mux}

	return s, nil
}

// staticFileHandler wraps the file server to handle SPA routing and content types.
func (s *Server) staticFileHandler(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Set cache headers for static assets
		// When using StaticDir (development mode), disable caching to enable hot-reloading
		if s.config.StaticDir != "" {
			w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
			w.Header().Set("Pragma", "no-cache")
			w.Header().Set("Expires", "0")
		} else if r.URL.Path != "/" && r.URL.Path != "/index.html" {
			w.Header().Set("Cache-Control", "public, max-age=31536000")
		}
		next.ServeHTTP(w, r)
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

	// Close all background sessions
	if s.sessionManager != nil {
		s.sessionManager.CloseAll("server_shutdown")
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
	s.clientsMu.RLock()
	defer s.clientsMu.RUnlock()

	for client := range s.clients {
		client.sendMessage(WSMsgTypeSessionRenamed, map[string]string{
			"session_id": sessionID,
			"name":       newName,
		})
	}

	if s.logger != nil {
		s.logger.Debug("Broadcast session renamed", "session_id", sessionID, "name", newName, "clients", len(s.clients))
	}
}

// BroadcastSessionDeleted notifies all connected clients that a session was deleted.
func (s *Server) BroadcastSessionDeleted(sessionID string) {
	s.clientsMu.RLock()
	defer s.clientsMu.RUnlock()

	for client := range s.clients {
		client.sendMessage(WSMsgTypeSessionDeleted, map[string]string{
			"session_id": sessionID,
		})
	}

	if s.logger != nil {
		s.logger.Debug("Broadcast session deleted", "session_id", sessionID, "clients", len(s.clients))
	}
}

// handleGetConfig handles GET /api/config.
func (s *Server) handleGetConfig(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	w.Header().Set("Content-Type", "application/json")

	// Return the web config portion (prompts) from the Mitto config
	if s.config.MittoConfig == nil {
		// Return empty config if not set
		json.NewEncoder(w).Encode(map[string]interface{}{
			"web": config.WebConfig{},
		})
		return
	}

	response := map[string]interface{}{
		"web": s.config.MittoConfig.Web,
	}
	json.NewEncoder(w).Encode(response)
}
