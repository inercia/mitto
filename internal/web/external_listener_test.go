package web

import (
	"net"
	"net/http"
	"testing"
	"time"
)

func TestServer_ExternalListener(t *testing.T) {
	// Create a minimal server for testing
	s := &Server{
		httpServer: &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		})},
	}

	// Test initial state
	if s.IsExternalListenerRunning() {
		t.Error("External listener should not be running initially")
	}

	// Find an available port
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Failed to find available port: %v", err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	listener.Close()

	// Start external listener
	actualPort, err := s.StartExternalListener(port)
	if err != nil {
		t.Fatalf("Failed to start external listener: %v", err)
	}
	if actualPort != port {
		t.Errorf("StartExternalListener returned port %d, want %d", actualPort, port)
	}

	// Give the goroutine time to start
	time.Sleep(50 * time.Millisecond)

	// Verify it's running
	if !s.IsExternalListenerRunning() {
		t.Error("External listener should be running after start")
	}

	// Verify the port is set
	if s.GetExternalPort() != port {
		t.Errorf("External port = %d, want %d", s.GetExternalPort(), port)
	}

	// Starting again should be a no-op and return the existing port
	existingPort, err := s.StartExternalListener(port)
	if err != nil {
		t.Errorf("Starting already running listener should not error: %v", err)
	}
	if existingPort != port {
		t.Errorf("Second StartExternalListener returned port %d, want %d", existingPort, port)
	}

	// Stop external listener
	s.StopExternalListener()

	// Give time for cleanup
	time.Sleep(50 * time.Millisecond)

	// Verify it's stopped
	if s.IsExternalListenerRunning() {
		t.Error("External listener should not be running after stop")
	}

	// Stopping again should be a no-op
	s.StopExternalListener()
}

func TestServer_ExternalListener_PortInUse(t *testing.T) {
	// Create a minimal server for testing
	s := &Server{
		httpServer: &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		})},
	}

	// Occupy a port
	listener, err := net.Listen("tcp", "0.0.0.0:0")
	if err != nil {
		t.Fatalf("Failed to create listener: %v", err)
	}
	defer listener.Close()
	port := listener.Addr().(*net.TCPAddr).Port

	// Try to start external listener on the same port
	_, err = s.StartExternalListener(port)
	if err == nil {
		t.Error("Expected error when starting listener on occupied port")
		s.StopExternalListener()
	}
}

func TestServer_ExternalListener_RandomPort(t *testing.T) {
	// Create a minimal server for testing
	s := &Server{
		httpServer: &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		})},
	}

	// Start external listener with port 0 (random)
	actualPort, err := s.StartExternalListener(0)
	if err != nil {
		t.Fatalf("Failed to start external listener with random port: %v", err)
	}
	if actualPort == 0 {
		t.Error("StartExternalListener should return non-zero port when given port 0")
	}

	// Verify the port is set correctly
	if s.GetExternalPort() != actualPort {
		t.Errorf("GetExternalPort = %d, want %d", s.GetExternalPort(), actualPort)
	}

	// Verify it's running
	if !s.IsExternalListenerRunning() {
		t.Error("External listener should be running")
	}

	// Clean up
	s.StopExternalListener()
}

func TestServer_SetExternalPort(t *testing.T) {
	s := &Server{}

	s.SetExternalPort(9090)
	if s.GetExternalPort() != 9090 {
		t.Errorf("External port = %d, want 9090", s.GetExternalPort())
	}

	s.SetExternalPort(8080)
	if s.GetExternalPort() != 8080 {
		t.Errorf("External port = %d, want 8080", s.GetExternalPort())
	}
}
