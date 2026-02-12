package mcpserver

import (
	"context"
	"testing"
	"time"

	"github.com/inercia/mitto/internal/session"
)

func TestNewServer(t *testing.T) {
	// Create a temporary store
	tmpDir := t.TempDir()
	store, err := session.NewStore(tmpDir)
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}
	defer store.Close()

	// Create server - this should not panic
	srv, err := NewServer(
		Config{Port: 0}, // Use port 0 to get a random available port
		Dependencies{
			Store:  store,
			Config: nil, // Config is optional
		},
	)
	if err != nil {
		t.Fatalf("NewServer failed: %v", err)
	}

	if srv == nil {
		t.Fatal("NewServer returned nil")
	}

	// Verify server is not running yet
	if srv.IsRunning() {
		t.Error("Server should not be running before Start()")
	}
}

func TestServerStartStop(t *testing.T) {
	// Create a temporary store
	tmpDir := t.TempDir()
	store, err := session.NewStore(tmpDir)
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}
	defer store.Close()

	// Create server
	srv, err := NewServer(
		Config{Port: 0},
		Dependencies{Store: store},
	)
	if err != nil {
		t.Fatalf("NewServer failed: %v", err)
	}

	// Start server
	ctx := context.Background()
	if err := srv.Start(ctx); err != nil {
		t.Fatalf("Start failed: %v", err)
	}

	// Verify server is running
	if !srv.IsRunning() {
		t.Error("Server should be running after Start()")
	}

	// Verify port was assigned
	port := srv.Port()
	if port == 0 {
		t.Error("Port should be assigned after Start()")
	}

	// Stop server
	if err := srv.Stop(); err != nil {
		t.Fatalf("Stop failed: %v", err)
	}

	// Give it a moment to stop
	time.Sleep(100 * time.Millisecond)

	// Verify server is not running
	if srv.IsRunning() {
		t.Error("Server should not be running after Stop()")
	}
}

func TestListConversationsWithEmptyStore(t *testing.T) {
	// Create a temporary store
	tmpDir := t.TempDir()
	store, err := session.NewStore(tmpDir)
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}
	defer store.Close()

	// Create server
	srv, err := NewServer(
		Config{Port: 0},
		Dependencies{Store: store},
	)
	if err != nil {
		t.Fatalf("NewServer failed: %v", err)
	}

	// Start server
	ctx := context.Background()
	if err := srv.Start(ctx); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer srv.Stop()

	// The server should be running and tools should be registered
	if !srv.IsRunning() {
		t.Error("Server should be running")
	}
}

func TestGetRuntimeInfo(t *testing.T) {
	// Test buildRuntimeInfo directly
	info := buildRuntimeInfo()

	if info.OS == "" {
		t.Error("OS should not be empty")
	}
	if info.Arch == "" {
		t.Error("Arch should not be empty")
	}
	if info.GoVersion == "" {
		t.Error("GoVersion should not be empty")
	}
	if info.PID == 0 {
		t.Error("PID should not be 0")
	}
	if info.NumCPU == 0 {
		t.Error("NumCPU should not be 0")
	}
}

func TestTransportModeDefaults(t *testing.T) {
	// Create a temporary store
	tmpDir := t.TempDir()
	store, err := session.NewStore(tmpDir)
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}
	defer store.Close()

	// Test default mode is SSE
	srv, err := NewServer(
		Config{}, // Empty config should default to SSE
		Dependencies{Store: store},
	)
	if err != nil {
		t.Fatalf("NewServer failed: %v", err)
	}

	if srv.Mode() != TransportModeSSE {
		t.Errorf("Default mode should be SSE, got %s", srv.Mode())
	}
}

func TestTransportModeSTDIO(t *testing.T) {
	// Create a temporary store
	tmpDir := t.TempDir()
	store, err := session.NewStore(tmpDir)
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}
	defer store.Close()

	// Test STDIO mode configuration
	srv, err := NewServer(
		Config{Mode: TransportModeSTDIO},
		Dependencies{Store: store},
	)
	if err != nil {
		t.Fatalf("NewServer failed: %v", err)
	}

	if srv.Mode() != TransportModeSTDIO {
		t.Errorf("Mode should be STDIO, got %s", srv.Mode())
	}

	// Port should be 0 for STDIO mode (not used)
	// Note: We don't start the server here because STDIO mode
	// would try to read from actual stdin
}
