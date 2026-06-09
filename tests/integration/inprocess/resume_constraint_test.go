//go:build integration

package inprocess

import (
	"context"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/inercia/mitto/internal/appdir"
	"github.com/inercia/mitto/internal/client"
	"github.com/inercia/mitto/internal/config"
	"github.com/inercia/mitto/internal/session"
	"github.com/inercia/mitto/internal/web"
)

// setupTestServerWithModelConstraint creates a test server whose mock-acp server
// is configured with a model constraint that auto-selects the "Opus" option.
// Mirrors SetupTestServer but injects Constraints into the MittoConfig.
func setupTestServerWithModelConstraint(t *testing.T, pattern string) *TestServer {
	t.Helper()

	tmpDir := t.TempDir()
	t.Setenv(appdir.MittoDirEnv, tmpDir)
	appdir.ResetCache()
	t.Cleanup(appdir.ResetCache)

	mockACPCmd := findMockACPServer(t)

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
			{
				Name:    "mock-acp",
				Command: mockACPCmd,
				Constraints: map[string]*config.ACPServerConstraint{
					"model": {MatchMode: "contains", Pattern: pattern},
				},
			},
		},
	}

	webConfig := web.Config{
		Workspaces: []config.WorkspaceSettings{
			{ACPServer: "mock-acp", WorkingDir: workspaceDir},
		},
		ACPCommand:              mockACPCmd,
		ACPServer:               "mock-acp",
		DefaultWorkingDir:       workspaceDir,
		AutoApprove:             true,
		Debug:                   true,
		FromCLI:                 true,
		MittoConfig:             mittoConfig,
		DisableAuxiliaryPrewarm: true,
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
		MockACPCmd: mockACPCmd,
	}
}

// TestResumeModelConstraint verifies that ACP server model-selection constraints
// are re-applied when a session is resumed (archive → unarchive cycle). This
// guards the bug where ResumeBackgroundSession's BackgroundSessionConfig literal
// omitted MittoConfig, leaving acpServerConstraints empty on resume.
func TestResumeModelConstraint(t *testing.T) {
	const (
		expectedModelID = "claude-opus-4-6"
		overrideModelID = "claude-sonnet-4-6"
	)

	ts := setupTestServerWithModelConstraint(t, "Opus")

	sess, err := ts.Client.CreateSession(client.CreateSessionRequest{
		Name: "Resume Constraint Test",
	})
	if err != nil {
		t.Fatalf("CreateSession failed: %v", err)
	}
	defer ts.Client.DeleteSession(sess.SessionID)

	// Connect and send a prompt so the deferred session/new handshake runs and
	// populates model info (lazy creation defers this to the first prompt).
	var promptComplete bool
	var promptMu sync.Mutex
	callbacks := client.SessionCallbacks{
		OnConnected: func(sid, cid, acp string) {
			t.Logf("Connected: session=%s", sid)
		},
		OnPromptComplete: func(_ int) {
			promptMu.Lock()
			promptComplete = true
			promptMu.Unlock()
		},
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	ws, err := ts.Client.Connect(ctx, sess.SessionID, callbacks)
	if err != nil {
		t.Fatalf("Connect failed: %v", err)
	}
	if err := ws.LoadEvents(50, 0, 0); err != nil {
		t.Fatalf("LoadEvents failed: %v", err)
	}
	time.Sleep(100 * time.Millisecond)
	if err := ws.SendPrompt("hello"); err != nil {
		t.Fatalf("SendPrompt failed: %v", err)
	}
	waitFor(t, 10*time.Second, func() bool {
		promptMu.Lock()
		defer promptMu.Unlock()
		return promptComplete
	}, "initial prompt to complete (triggers deferred session/new + model setup)")

	sm := ts.Server.GetSessionManager()
	bs := sm.GetSession(sess.SessionID)
	if bs == nil {
		t.Fatalf("GetSession returned nil after CreateSession")
	}

	// Initial constraint application: model should auto-select to Opus.
	// applyPendingSharedModes runs during the prompt goroutine above.
	waitFor(t, 5*time.Second, func() bool {
		return bs.GetConfigValue("model") == expectedModelID
	}, "initial model constraint to be applied (Opus)")
	t.Logf("Initial model auto-selected: %s", bs.GetConfigValue("model"))

	// Override the model to Sonnet so resume must re-apply the constraint
	// (otherwise the post-resume value would already match and we wouldn't
	// be testing the constraint re-application code path).
	overrideCtx, overrideCancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer overrideCancel()
	if err := bs.SetConfigOption(overrideCtx, "model", overrideModelID); err != nil {
		t.Fatalf("Override SetConfigOption failed: %v", err)
	}
	waitFor(t, 5*time.Second, func() bool {
		return bs.GetConfigValue("model") == overrideModelID
	}, "model override to Sonnet")

	ws.Close()

	// Archive → unarchive triggers the ResumeBackgroundSession code path.
	if err := ts.Client.ArchiveSession(sess.SessionID, true); err != nil {
		t.Fatalf("ArchiveSession failed: %v", err)
	}
	time.Sleep(500 * time.Millisecond)
	if err := ts.Client.ArchiveSession(sess.SessionID, false); err != nil {
		t.Fatalf("Unarchive failed: %v", err)
	}

	// After resume, GetSession may return a freshly-constructed BackgroundSession.
	waitFor(t, 10*time.Second, func() bool {
		return sm.GetSession(sess.SessionID) != nil
	}, "resumed BackgroundSession to be registered")
	resumedBS := sm.GetSession(sess.SessionID)

	// The key assertion: the constraint must have been re-applied on resume,
	// flipping the model back to Opus. Without the MittoConfig fix in
	// ResumeBackgroundSession, acpServerConstraints stays empty and the model
	// would remain at the overridden Sonnet value.
	waitFor(t, 10*time.Second, func() bool {
		return resumedBS.GetConfigValue("model") == expectedModelID
	}, "model constraint to be re-applied on resume (Opus)")
	t.Logf("Post-resume model: %s", resumedBS.GetConfigValue("model"))
}
