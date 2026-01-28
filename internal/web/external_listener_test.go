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

func TestServer_SetExternalPort_DisabledValue(t *testing.T) {
	// Test that -1 (disabled) is properly stored and retrieved
	s := &Server{}

	// Set to disabled (-1)
	s.SetExternalPort(-1)
	if s.GetExternalPort() != -1 {
		t.Errorf("External port = %d, want -1 (disabled)", s.GetExternalPort())
	}

	// Set to random (0)
	s.SetExternalPort(0)
	if s.GetExternalPort() != 0 {
		t.Errorf("External port = %d, want 0 (random)", s.GetExternalPort())
	}

	// Set to specific port
	s.SetExternalPort(8443)
	if s.GetExternalPort() != 8443 {
		t.Errorf("External port = %d, want 8443", s.GetExternalPort())
	}
}

func TestExternalPortSemantics(t *testing.T) {
	// Document and test the external port semantics:
	// -1 = disabled (no external listener)
	//  0 = random port (OS chooses)
	// >0 = specific port number

	tests := []struct {
		name        string
		port        int
		isDisabled  bool
		isRandom    bool
		isSpecific  bool
		description string
	}{
		{
			name:        "disabled",
			port:        -1,
			isDisabled:  true,
			isRandom:    false,
			isSpecific:  false,
			description: "Port -1 means external listener is disabled",
		},
		{
			name:        "random",
			port:        0,
			isDisabled:  false,
			isRandom:    true,
			isSpecific:  false,
			description: "Port 0 means OS chooses a random available port",
		},
		{
			name:        "specific",
			port:        8443,
			isDisabled:  false,
			isRandom:    false,
			isSpecific:  true,
			description: "Port > 0 means use that specific port",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Test the semantic interpretation
			isDisabled := tt.port < 0
			isRandom := tt.port == 0
			isSpecific := tt.port > 0

			if isDisabled != tt.isDisabled {
				t.Errorf("port %d: isDisabled = %v, want %v", tt.port, isDisabled, tt.isDisabled)
			}
			if isRandom != tt.isRandom {
				t.Errorf("port %d: isRandom = %v, want %v", tt.port, isRandom, tt.isRandom)
			}
			if isSpecific != tt.isSpecific {
				t.Errorf("port %d: isSpecific = %v, want %v", tt.port, isSpecific, tt.isSpecific)
			}
		})
	}
}
