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
	"github.com/inercia/mitto/internal/config"
	"github.com/inercia/mitto/internal/session"
	"github.com/inercia/mitto/internal/web"
	"github.com/inercia/mitto/pkg/api"
)

// TestServer wraps a web.Server with test utilities.
type TestServer struct {
	Server     *web.Server
	HTTPServer *httptest.Server
	Store      *session.Store
	Client     *api.Client
	TempDir    string
	MockACPCmd string
}

// SetupTestServer creates a new test server with mock ACP.
// The returned cleanup function must be called to release resources.
// Optional opts customize the web.Config before the server is created (e.g. to
// register workspaces with non-default settings).
func SetupTestServer(t *testing.T, opts ...func(*web.Config)) *TestServer {
	t.Helper()

	// Create temp directory for test data
	tmpDir := t.TempDir()
	t.Setenv(appdir.MittoDirEnv, tmpDir)
	appdir.ResetCache()
	t.Cleanup(appdir.ResetCache)

	// Find mock ACP server binary
	mockACPCmd := findMockACPServer(t)

	// Create workspace directory
	workspaceDir := filepath.Join(tmpDir, "workspace")
	if err := os.MkdirAll(workspaceDir, 0755); err != nil {
		t.Fatalf("Failed to create workspace dir: %v", err)
	}

	// Create minimal Mitto config for tests
	mittoConfig := &config.Config{
		ACPServers: []config.ACPServer{
			{
				Name:    "mock-acp",
				Command: mockACPCmd,
			},
		},
	}

	// Create web server config
	webConfig := web.Config{
		Workspaces: []config.WorkspaceSettings{
			{
				ACPServer:  "mock-acp",
				WorkingDir: workspaceDir,
			},
		},
		ACPCommand:        mockACPCmd,
		ACPServer:         "mock-acp",
		DefaultWorkingDir: workspaceDir,
		AutoApprove:       true, // Auto-approve for tests
		Debug:             true,
		FromCLI:           true, // Don't persist workspace changes
		MittoConfig:       mittoConfig,

		DisableAuxiliaryPrewarm: true, // Avoid interference with mock ACP server
	}

	// Apply optional config customizations.
	for _, opt := range opts {
		if opt != nil {
			opt(&webConfig)
		}
	}

	// Create web server
	srv, err := web.NewServer(webConfig)
	if err != nil {
		t.Fatalf("Failed to create web server: %v", err)
	}
	t.Cleanup(func() { _ = srv.Shutdown() })

	// Share the server's own session store. The server creates its store via
	// session.DefaultStore() over MITTO_DIR/sessions; constructing a second
	// session.Store over the same directory would give it an independent mutex,
	// so concurrent metadata writes (test event injection vs. the server's
	// background session) would race on metadata.json — corrupting EventCount
	// and producing duplicate sequence numbers. Sharing one Store instance
	// serializes all reads/writes through a single lock.
	store := srv.Store()

	// Create test HTTP server
	httpServer := httptest.NewServer(srv.Handler())
	t.Cleanup(httpServer.Close)

	// Create client
	mittoClient := api.New(httpServer.URL)

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
	return findRepoFile(t, filepath.Join("tests", "mocks", "acp-server", "mock-acp-server"),
		"mock-acp-server not found. Run 'make build-mock-acp' first")
}

// findRepoFile walks upward from the current working directory looking for
// relPath, returning its absolute path once found. Used to locate repo-root
// artifacts (built binaries, fixture scripts) regardless of which package's
// test binary is running (each go test invocation's cwd is that package's
// directory, arbitrarily deep under the repo root). Calls t.Skip with
// skipMsg if relPath is never found by the time the filesystem root is
// reached — mirroring the historical behavior of findMockACPServer, whose
// upward-walk this generalizes (mitto-7gta.25).
func findRepoFile(t *testing.T, relPath, skipMsg string) string {
	t.Helper()

	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("Failed to get working directory: %v", err)
	}

	for {
		candidate := filepath.Join(dir, relPath)
		if _, err := os.Stat(candidate); err == nil {
			return candidate
		}

		parent := filepath.Dir(dir)
		if parent == dir {
			t.Skip(skipMsg)
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
