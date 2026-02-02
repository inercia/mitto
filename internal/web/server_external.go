package web

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"time"
)

// SetExternalPort sets the port to use for external access.
// This should be called before starting the external listener.
func (s *Server) SetExternalPort(port int) {
	s.externalMu.Lock()
	defer s.externalMu.Unlock()
	s.externalPort = port
}

// externalConnectionMiddleware wraps requests to mark them as coming from the external listener.
// This ensures authentication is required for ALL external connections, even from localhost.
func externalConnectionMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Add context value indicating this is an external connection
		ctx := context.WithValue(r.Context(), ContextKeyExternalConnection, true)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// StartExternalListener starts a listener on 0.0.0.0 for external access.
// This allows external connections while keeping the main listener on 127.0.0.1.
// If port is 0, a random available port is selected.
// Returns the actual port used, or 0 and an error on failure.
// Returns 0 without error if external listener is already running.
//
// SECURITY: All connections through this listener are marked as "external" and
// require authentication even if they originate from localhost. This prevents
// authentication bypass by connecting to the external port from the local machine.
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

	// Create a separate HTTP server for external connections that marks all requests
	// as external. This ensures auth is required even for localhost connections.
	externalServer := &http.Server{
		Handler: externalConnectionMiddleware(s.httpServer.Handler),
	}
	s.externalHTTPServer = externalServer

	// Serve on the external listener in a goroutine
	// Capture externalServer locally to avoid race with stopExternalListenerLocked
	go func() {
		if err := externalServer.Serve(listener); err != nil {
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
		// Shutdown the external HTTP server gracefully
		if s.externalHTTPServer != nil {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if err := s.externalHTTPServer.Shutdown(ctx); err != nil {
				if s.logger != nil {
					s.logger.Debug("Error shutting down external HTTP server", "error", err)
				}
			}
			s.externalHTTPServer = nil
		}
		// Also close the listener (may already be closed by Shutdown)
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

// ExternalStatusResponse represents the response for the external status endpoint.
type ExternalStatusResponse struct {
	Enabled bool `json:"enabled"`
	Port    int  `json:"port"`
}

// handleExternalStatus handles GET /api/external-status.
// Returns the current status of the external listener.
func (s *Server) handleExternalStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	writeJSONOK(w, ExternalStatusResponse{
		Enabled: s.IsExternalListenerRunning(),
		Port:    s.GetExternalPort(),
	})
}
