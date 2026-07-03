//go:build integration

package inprocess

import (
	"context"
	"fmt"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/inercia/mitto/internal/appdir"
	"github.com/inercia/mitto/internal/client"
	"github.com/inercia/mitto/internal/config"
	"github.com/inercia/mitto/internal/conversation"
	"github.com/inercia/mitto/internal/web"
)

// setupTestServerWithModelConstraintAndEnv is like setupTestServerWithModelConstraint
// but also allows injecting extra environment variables into the mock ACP server process.
// This is used by TestConcurrentModelSetBurst to exercise the retry path deterministically.
func setupTestServerWithModelConstraintAndEnv(t *testing.T, pattern string, acpEnv map[string]string) *TestServer {
	t.Helper()

	tmpDir := t.TempDir()
	t.Setenv(appdir.MittoDirEnv, tmpDir)
	appdir.ResetCache()
	t.Cleanup(appdir.ResetCache)

	mockACPCmd := findMockACPServer(t)

	workspaceDir := filepath.Join(tmpDir, "workspace")
	if err := os.MkdirAll(workspaceDir, 0755); err != nil {
		t.Fatalf("Failed to create workspace dir: %v", err)
	}

	mittoConfig := &config.Config{
		ACPServers: []config.ACPServer{
			{
				Name:    "mock-acp",
				Command: mockACPCmd,
				Env:     acpEnv,
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
	t.Cleanup(func() { _ = srv.Shutdown() })

	httpServer := httptest.NewServer(srv.Handler())
	t.Cleanup(httpServer.Close)

	return &TestServer{
		Server:     srv,
		HTTPServer: httpServer,
		Store:      srv.Store(),
		Client:     client.New(httpServer.URL),
		TempDir:    tmpDir,
		MockACPCmd: mockACPCmd,
	}
}

// TestConcurrentModelSetBurst verifies that N sessions starting simultaneously on the
// same shared ACP process all converge to the constrained model even when the mock
// server injects "timeout" failures for the first MOCK_SET_MODEL_FAIL_FIRST set_model
// requests (forcing the retry path in SharedACPProcess.SetSessionModel).
//
// Without the fix (serialisation semaphore + retry), concurrent callers race the
// serially-served agent subprocess and at least one call times out permanently.
// With the fix, the retried calls succeed and all sessions converge.
func TestConcurrentModelSetBurst(t *testing.T) {
	const (
		numSessions     = 3
		expectedModelID = "claude-opus-4-6"
		// MOCK_SET_MODEL_FAIL_FIRST=1: the first set_model RPC to reach the
		// (serially-served) agent returns a "timeout" error, forcing the retry path in
		// SharedACPProcess.SetSessionModel. The mock's fail counter is GLOBAL and the
		// serialisation semaphore makes callers run strictly one-at-a-time, so exactly
		// one session hits the injected failure and must recover via retry; the others
		// succeed on their first attempt. All three must still converge to Opus.
		//
		// NOTE: do NOT raise this to numSessions — with serialisation the first caller
		// would consume every injected failure across its own retry attempts and exhaust
		// its budget, which exercises a degenerate mock artefact rather than the fix.
		failFirst = "1"
	)

	ts := setupTestServerWithModelConstraintAndEnv(t, "Opus",
		map[string]string{"MOCK_SET_MODEL_FAIL_FIRST": failFirst})

	type sessionResult struct {
		id string
		bs *conversation.BackgroundSession
	}

	results := make([]sessionResult, numSessions)
	var wg sync.WaitGroup

	for i := 0; i < numSessions; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()

			sess, err := ts.Client.CreateSession(client.CreateSessionRequest{
				Name: fmt.Sprintf("concurrent-model-burst-%d", idx),
			})
			if err != nil {
				t.Errorf("session[%d] CreateSession failed: %v", idx, err)
				return
			}
			t.Cleanup(func() { ts.Client.DeleteSession(sess.SessionID) })

			var promptComplete bool
			var mu sync.Mutex
			callbacks := client.SessionCallbacks{
				OnPromptComplete: func(_ int) {
					mu.Lock()
					promptComplete = true
					mu.Unlock()
				},
			}

			ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
			defer cancel()

			ws, err := ts.Client.Connect(ctx, sess.SessionID, callbacks)
			if err != nil {
				t.Errorf("session[%d] Connect failed: %v", idx, err)
				return
			}
			defer ws.Close()

			if err := ws.LoadEvents(50, 0, 0); err != nil {
				t.Errorf("session[%d] LoadEvents failed: %v", idx, err)
				return
			}
			time.Sleep(100 * time.Millisecond)

			if err := ws.SendPrompt("hello"); err != nil {
				t.Errorf("session[%d] SendPrompt failed: %v", idx, err)
				return
			}

			// Wait for the prompt to complete (triggers deferred session/new + applyConfigConstraints).
			waitFor(t, 30*time.Second, func() bool {
				mu.Lock()
				defer mu.Unlock()
				return promptComplete
			}, fmt.Sprintf("session[%d] prompt complete", idx))

			sm := ts.Server.GetSessionManager()
			bs := sm.GetSession(sess.SessionID)
			if bs == nil {
				t.Errorf("session[%d] GetSession returned nil", idx)
				return
			}

			results[idx] = sessionResult{id: sess.SessionID, bs: bs}
		}(i)
	}

	wg.Wait()

	// Assert all sessions converged to the expected model within the deadline.
	//
	// CRITICAL: assert on the AGENT-CONFIRMED model (AgentModels().CurrentModelId),
	// NOT on GetConfigValue("model"). setAgentModels optimistically pre-applies the
	// constrained value to the local configOption.CurrentValue BEFORE the set_model
	// RPC runs, so GetConfigValue would return the desired model even if every RPC
	// failed — masking a broken retry path. AgentModels().CurrentModelId is only
	// updated by SetConfigOption AFTER a successful SetSessionModel RPC, so it is the
	// definitive signal that the retried call actually reached and was accepted by the
	// (serially-served) agent subprocess (mitto-3q9).
	sm := ts.Server.GetSessionManager()
	for i, r := range results {
		if r.bs == nil {
			continue // already reported error above
		}
		// The BackgroundSession reference may have been replaced; refresh it.
		bs := sm.GetSession(r.id)
		if bs == nil {
			t.Errorf("session[%d] GetSession returned nil after goroutines finished", i)
			continue
		}
		waitFor(t, 15*time.Second, func() bool {
			am := bs.AgentModels()
			return am != nil && string(am.CurrentModelId) == expectedModelID
		}, fmt.Sprintf("session[%d] agent-confirmed model (want %s)", i, expectedModelID))

		am := bs.AgentModels()
		gotAgent := ""
		if am != nil {
			gotAgent = string(am.CurrentModelId)
		}
		t.Logf("session[%d] agent-confirmed model: %s (local config value: %s)",
			i, gotAgent, bs.GetConfigValue("model"))
	}
}
