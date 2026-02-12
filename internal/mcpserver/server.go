// Package mcpserver provides an MCP (Model Context Protocol) server for debugging Mitto.
// The server exposes tools for inspecting conversations and configuration.
// It binds only to 127.0.0.1 for security reasons.
package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/inercia/mitto/internal/config"
	"github.com/inercia/mitto/internal/logging"
	"github.com/inercia/mitto/internal/session"
)

const (
	// DefaultPort is the default port for the MCP server.
	DefaultPort = 5757
	// ServerName is the name of the MCP server.
	ServerName = "mitto-debug"
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

// Server is the MCP debug server for Mitto.
type Server struct {
	mcpServer *mcp.Server
	logger    *slog.Logger
	port      int
	mode      TransportMode
	listener  net.Listener
	httpSrv   *http.Server

	// For STDIO mode
	stdioSession *mcp.ServerSession
	stdioDone    chan struct{}

	mu       sync.RWMutex
	store    *session.Store
	config   *config.Config
	running  bool
	shutdown bool
}

// Dependencies holds the dependencies needed by the MCP server.
type Dependencies struct {
	Store  *session.Store
	Config *config.Config
	// SessionManager is optional - provides info about running sessions
	SessionManager SessionManager
}

// SessionManager interface for checking session status.
type SessionManager interface {
	GetSession(sessionID string) BackgroundSession
	ListRunningSessions() []string
}

// BackgroundSession interface for session info.
type BackgroundSession interface {
	IsPrompting() bool
	GetEventCount() int
	GetMaxAssignedSeq() int64
}

// Config holds the configuration for the MCP server.
type Config struct {
	// Port to listen on (default: 5757). Only used in SSE mode.
	Port int

	// Mode specifies the transport mode (sse or stdio). Default: sse.
	Mode TransportMode
}

// NewServer creates a new MCP debug server.
// If cfg.Port is -1, the default port (5757) is used.
// If cfg.Port is 0, a random available port is assigned when the server starts.
func NewServer(cfg Config, deps Dependencies) (*Server, error) {
	logger := logging.MCP()

	// Port -1 means use default, 0 means random available port
	if cfg.Port < 0 {
		cfg.Port = DefaultPort
	}

	if cfg.Mode == "" {
		cfg.Mode = TransportModeSSE
	}

	s := &Server{
		logger: logger,
		port:   cfg.Port,
		mode:   cfg.Mode,
		store:  deps.Store,
		config: deps.Config,
	}

	// Create MCP server
	mcpSrv := mcp.NewServer(&mcp.Implementation{
		Name:    ServerName,
		Version: ServerVersion,
	}, nil)

	// Register tools
	s.registerTools(mcpSrv, deps)

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

// startSSE starts the MCP server in HTTP mode on 127.0.0.1.
// Despite the name, this uses the Streamable HTTP transport (MCP spec 2025-03-26)
// which is different from the legacy SSE transport.
func (s *Server) startSSE(ctx context.Context) error {
	addr := fmt.Sprintf("127.0.0.1:%d", s.port)
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

	s.logger.Info("MCP debug server started",
		"mode", "http",
		"address", addr,
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

	s.logger.Info("MCP debug server started", "mode", "stdio")

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

		s.logger.Info("MCP debug server stopped", "mode", "stdio")
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

	s.logger.Info("MCP debug server stopped")
	return nil
}

// Port returns the actual port the server is listening on.
// Returns 0 for STDIO mode.
func (s *Server) Port() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.port
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

// registerTools registers all MCP tools with the server.
func (s *Server) registerTools(mcpSrv *mcp.Server, deps Dependencies) {
	// list_conversations tool
	mcp.AddTool(mcpSrv, &mcp.Tool{
		Name:        "list_conversations",
		Description: "List all conversations with metadata including title, dates, message count, prompting status, last sequence, and session folder",
	}, s.createListConversationsHandler(deps.SessionManager))

	// get_config tool
	mcp.AddTool(mcpSrv, &mcp.Tool{
		Name:        "get_config",
		Description: "Get the current effective Mitto configuration",
	}, s.createGetConfigHandler())

	// get_runtime_info tool
	mcp.AddTool(mcpSrv, &mcp.Tool{
		Name:        "get_runtime_info",
		Description: "Get runtime information including OS, architecture, log file paths, data directories, and process info",
	}, s.createGetRuntimeInfoHandler())
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
