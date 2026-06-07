//go:build integration

package inprocess

import (
	"context"
	"log/slog"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/inercia/mitto/internal/appdir"
	"github.com/inercia/mitto/internal/client"
	"github.com/inercia/mitto/internal/config"
	"github.com/inercia/mitto/internal/session"
	"github.com/inercia/mitto/internal/web"
)

// capturingHandler is a thread-safe slog.Handler that collects log records.
type capturingHandler struct {
	mu      sync.Mutex
	records []slog.Record
}

func (h *capturingHandler) Enabled(context.Context, slog.Level) bool { return true }

func (h *capturingHandler) Handle(_ context.Context, r slog.Record) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.records = append(h.records, r.Clone())
	return nil
}

func (h *capturingHandler) WithAttrs([]slog.Attr) slog.Handler { return h }
func (h *capturingHandler) WithGroup(string) slog.Handler      { return h }

func (h *capturingHandler) hasWarnContaining(substr string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, r := range h.records {
		if r.Level == slog.LevelWarn && strings.Contains(r.Message, substr) {
			return true
		}
	}
	return false
}

// setupWatchdogTestServer creates a test server that uses the given ACP command
// (intended to be a silent stub) and injects the provided logger into web.Config.
func setupWatchdogTestServer(t *testing.T, acpCommand string, logger *slog.Logger) *TestServer {
	t.Helper()

	tmpDir := t.TempDir()
	t.Setenv(appdir.MittoDirEnv, tmpDir)
	appdir.ResetCache()
	t.Cleanup(appdir.ResetCache)

	// Still need the mock binary present (harness expectation).
	findMockACPServer(t)

	store, err := session.NewStore(filepath.Join(tmpDir, "sessions"))
	if err != nil {
		t.Fatalf("Failed to create session store: %v", err)
	}
	t.Cleanup(func() { store.Close() })

	workspaceDir := filepath.Join(tmpDir, "workspace")
	if err := os.MkdirAll(workspaceDir, 0755); err != nil {
		t.Fatalf("Failed to create workspace dir: %v", err)
	}

	mittoConfig := &config.Config{
		ACPServers: []config.ACPServer{
			{Name: "stub-acp", Command: acpCommand},
		},
	}

	webConfig := web.Config{
		Workspaces: []config.WorkspaceSettings{
			{ACPServer: "stub-acp", WorkingDir: workspaceDir},
		},
		ACPCommand:              acpCommand,
		ACPServer:               "stub-acp",
		DefaultWorkingDir:       workspaceDir,
		AutoApprove:             true,
		Debug:                   true,
		FromCLI:                 true,
		MittoConfig:             mittoConfig,
		DisableAuxiliaryPrewarm: true,
		Logger:                  logger,
	}

	srv, err := web.NewServer(webConfig)
	if err != nil {
		t.Fatalf("Failed to create web server: %v", err)
	}

	httpServer := httptest.NewServer(srv.Handler())
	t.Cleanup(httpServer.Close)

	return &TestServer{
		Server:     srv,
		HTTPServer: httpServer,
		Store:      store,
		Client:     client.New(httpServer.URL),
		TempDir:    tmpDir,
	}
}

// TestACPStartupWatchdog_WarnsOnSilentStub verifies that the startup watchdog
// emits a WARN log when the ACP process produces no output and the Initialize
// handshake never completes within acpStartupWatchdogWarnDelay (10 s).
func TestACPStartupWatchdog_WarnsOnSilentStub(t *testing.T) {
	cap := &capturingHandler{}
	ts := setupWatchdogTestServer(t, "sleep 25", slog.New(cap))

	// CreateSession blocks until ACP Initialize completes; with a silent stub
	// it never completes, so run it in a goroutine and ignore the result.
	go func() { _, _ = ts.Client.CreateSession(client.CreateSessionRequest{}) }()

	// Watchdog WARN fires at acpStartupWatchdogWarnDelay (10 s). Allow generous margin.
	waitFor(t, 25*time.Second, func() bool {
		return cap.hasWarnContaining("unresponsive")
	}, "startup watchdog WARN for unresponsive ACP process")
}
