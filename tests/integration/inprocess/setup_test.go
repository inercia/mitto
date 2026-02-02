//go:build integration

// Package inprocess contains in-process integration tests for Mitto.
// These tests start the web server directly in the test process for faster execution.
package inprocess

import (
	"net"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/inercia/mitto/internal/appdir"
	"github.com/inercia/mitto/internal/client"
	"github.com/inercia/mitto/internal/config"
	"github.com/inercia/mitto/internal/session"
	"github.com/inercia/mitto/internal/web"
)

// TestServer wraps a web.Server with test utilities.
type TestServer struct {
	Server     *web.Server
	HTTPServer *httptest.Server
	Store      *session.Store
	Client     *client.Client
	TempDir    string
	MockACPCmd string
}

// SetupTestServer creates a new test server with mock ACP.
// The returned cleanup function must be called to release resources.
func SetupTestServer(t *testing.T) *TestServer {
	t.Helper()

	// Create temp directory for test data
	tmpDir := t.TempDir()
	t.Setenv(appdir.MittoDirEnv, tmpDir)
	appdir.ResetCache()
	t.Cleanup(appdir.ResetCache)

	// Find mock ACP server binary
	mockACPCmd := findMockACPServer(t)

	// Create session store
	store, err := session.NewStore(filepath.Join(tmpDir, "sessions"))
	if err != nil {
		t.Fatalf("Failed to create session store: %v", err)
	}
	t.Cleanup(func() { store.Close() })

	// Create workspace directory
	workspaceDir := filepath.Join(tmpDir, "workspace")
	if err := os.MkdirAll(workspaceDir, 0755); err != nil {
		t.Fatalf("Failed to create workspace dir: %v", err)
	}

	// Create web server config
	webConfig := web.Config{
		Workspaces: []config.WorkspaceSettings{
			{
				ACPServer:  "mock-acp",
				ACPCommand: mockACPCmd,
				WorkingDir: workspaceDir,
			},
		},
		ACPCommand:        mockACPCmd,
		ACPServer:         "mock-acp",
		DefaultWorkingDir: workspaceDir,
		AutoApprove:       true, // Auto-approve for tests
		Debug:             true,
		FromCLI:           true, // Don't persist workspace changes
	}

	// Create web server
	srv, err := web.NewServer(webConfig)
	if err != nil {
		t.Fatalf("Failed to create web server: %v", err)
	}

	// Create test HTTP server
	httpServer := httptest.NewServer(srv.Handler())
	t.Cleanup(httpServer.Close)

	// Create client
	mittoClient := client.New(httpServer.URL)

	return &TestServer{
		Server:     srv,
		HTTPServer: httpServer,
		Store:      store,
		Client:     mittoClient,
		TempDir:    tmpDir,
		MockACPCmd: mockACPCmd,
	}
}

// findMockACPServer locates the mock ACP server binary.
func findMockACPServer(t *testing.T) string {
	t.Helper()

	// Try to find project root
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("Failed to get working directory: %v", err)
	}

	for {
		mockPath := filepath.Join(dir, "tests", "mocks", "acp-server", "mock-acp-server")
		if _, err := os.Stat(mockPath); err == nil {
			return mockPath
		}

		parent := filepath.Dir(dir)
		if parent == dir {
			t.Skip("mock-acp-server not found. Run 'make build-mock-acp' first")
		}
		dir = parent
	}
}

// GetFreePort returns an available TCP port.
func GetFreePort(t *testing.T) int {
	t.Helper()

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Failed to get free port: %v", err)
	}
	defer listener.Close()

	return listener.Addr().(*net.TCPAddr).Port
}
