//go:build integration

package inprocess

import (
	"context"
	"fmt"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/inercia/mitto/internal/config"
	"github.com/inercia/mitto/internal/web"
	"github.com/inercia/mitto/pkg/api"
)

// =============================================================================
// mitto-lrt.2 Group B — Session lifecycle & recovery regression baseline.
//
// These tests pin two ACP-adjacent lifecycle behaviors identified as gaps by
// the mitto-lrt.2 coverage survey: an explicit archive->unarchive->re-archive
// cycle (existing tests only exercise a single archive/unarchive pass), and
// startup recovery replaying persisted history after a full server restart
// (existing tests never construct a second web.Server against the same
// on-disk session store). Add-only: no existing assertions are touched.
// =============================================================================

// TestLifecycleBaseline_ArchiveUnarchiveRearchive verifies that a session
// remains fully functional (able to send/receive prompts) across TWO
// consecutive archive -> unarchive cycles, not just one. This pins the
// explicit "re-archive" boundary called out in the mitto-lrt.2 plan.
func TestLifecycleBaseline_ArchiveUnarchiveRearchive(t *testing.T) {
	ts := SetupTestServer(t)

	sess, err := ts.Client.CreateSession(api.CreateSessionRequest{Name: "rearchive-baseline"})
	if err != nil {
		t.Fatalf("CreateSession failed: %v", err)
	}
	defer ts.Client.DeleteSession(sess.SessionID)

	runRound := func(round int) {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()

		var (
			mu       sync.Mutex
			complete bool
		)
		ws, err := ts.Client.Connect(ctx, sess.SessionID, api.SessionCallbacks{
			OnPromptComplete: func(_ int) {
				mu.Lock()
				complete = true
				mu.Unlock()
			},
		})
		if err != nil {
			t.Fatalf("round %d: Connect failed: %v", round, err)
		}
		defer ws.Close()

		if err := ws.LoadEvents(50, 0, 0); err != nil {
			t.Fatalf("round %d: LoadEvents failed: %v", round, err)
		}
		time.Sleep(150 * time.Millisecond)

		if err := ws.SendPrompt("hello"); err != nil {
			t.Fatalf("round %d: SendPrompt failed: %v", round, err)
		}
		waitFor(t, 15*time.Second, func() bool {
			mu.Lock()
			defer mu.Unlock()
			return complete
		}, fmt.Sprintf("round %d prompt to complete", round))
	}

	sm := ts.Server.GetSessionManager()
	cycle := func(round int) {
		runRound(round)

		if err := ts.Client.ArchiveSession(sess.SessionID, true); err != nil {
			t.Fatalf("round %d: ArchiveSession(true) failed: %v", round, err)
		}
		waitFor(t, 10*time.Second, func() bool {
			meta, err := ts.Store.GetMetadata(sess.SessionID)
			return err == nil && meta.Archived
		}, fmt.Sprintf("round %d archive to persist", round))

		if err := ts.Client.ArchiveSession(sess.SessionID, false); err != nil {
			t.Fatalf("round %d: ArchiveSession(false) failed: %v", round, err)
		}
		waitFor(t, 10*time.Second, func() bool {
			return sm.GetSession(sess.SessionID) != nil
		}, fmt.Sprintf("round %d resumed BackgroundSession to be registered", round))
	}

	// Round 1: baseline prompt, then archive -> unarchive.
	cycle(1)
	// Round 2: prompt again post-resume, then archive -> unarchive a SECOND
	// time (the explicit re-archive boundary).
	cycle(2)
	// Round 3: the session must still be fully functional after the second
	// unarchive — no lingering "archived" or half-resumed state.
	runRound(3)

	meta, err := ts.Store.GetMetadata(sess.SessionID)
	if err != nil {
		t.Fatalf("GetMetadata failed: %v", err)
	}
	if meta.Archived {
		t.Errorf("session unexpectedly archived after final round")
	}
	t.Logf("Session survived 2 archive<->unarchive cycles and 3 prompt rounds ✓ (event_count=%d)", meta.EventCount)
}

// TestLifecycleBaseline_StartupRecoveryReplaysEvents verifies that history
// persisted before a full server restart (a brand-new web.Server instance
// over the same on-disk session store, simulating a process restart) is
// replayed to a client that connects and calls LoadEvents afterwards. This
// was identified as the biggest gap in the mitto-lrt.2 coverage survey: no
// existing test constructs a second server against the same store directory.
func TestLifecycleBaseline_StartupRecoveryReplaysEvents(t *testing.T) {
	ts := SetupTestServer(t)

	sess, err := ts.Client.CreateSession(api.CreateSessionRequest{Name: "restart-recovery"})
	if err != nil {
		t.Fatalf("CreateSession failed: %v", err)
	}

	// Seed persisted history directly, as if it had accumulated before a
	// restart (independent of the in-memory BackgroundSession/ACP pipeline).
	inj := NewEventInjector(t, ts, sess.SessionID)
	_, lastSeq := inj.InjectMixed(3) // 6 events: 3x (user_prompt, agent_message)

	// Simulate a full server restart: shut down the first server instance so
	// it releases ownership, then construct a brand-new web.Server against the
	// SAME on-disk MITTO_DIR (the env var set by SetupTestServer via
	// t.Setenv remains in effect for the rest of this test).
	if err := ts.Server.Shutdown(); err != nil {
		t.Fatalf("Shutdown (pre-restart) failed: %v", err)
	}

	workspaceDir := filepath.Join(ts.TempDir, "workspace")
	mittoConfig := &config.Config{
		ACPServers: []config.ACPServer{{Name: "mock-acp", Command: ts.MockACPCmd}},
	}
	webConfig := web.Config{
		Workspaces:              []config.WorkspaceSettings{{ACPServer: "mock-acp", WorkingDir: workspaceDir}},
		ACPCommand:              ts.MockACPCmd,
		ACPServer:               "mock-acp",
		DefaultWorkingDir:       workspaceDir,
		AutoApprove:             true,
		Debug:                   true,
		FromCLI:                 true,
		MittoConfig:             mittoConfig,
		DisableAuxiliaryPrewarm: true,
	}
	srv2, err := web.NewServer(webConfig)
	if err != nil {
		t.Fatalf("Failed to create restarted web server: %v", err)
	}
	t.Cleanup(func() { _ = srv2.Shutdown() })

	httpServer2 := httptest.NewServer(srv2.Handler())
	t.Cleanup(httpServer2.Close)
	client2 := api.New(httpServer2.URL)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	var (
		mu     sync.Mutex
		events []api.SyncEvent
		loaded = make(chan struct{}, 1)
	)
	ws2, err := client2.Connect(ctx, sess.SessionID, api.SessionCallbacks{
		OnEventsLoaded: func(evts []api.SyncEvent, hasMore bool, isPrompting bool) {
			mu.Lock()
			events = append(events, evts...)
			mu.Unlock()
			select {
			case loaded <- struct{}{}:
			default:
			}
		},
	})
	if err != nil {
		t.Fatalf("Connect to restarted server failed: %v", err)
	}
	defer ws2.Close()

	time.Sleep(300 * time.Millisecond)
	if err := ws2.LoadEvents(50, 0, 0); err != nil {
		t.Fatalf("LoadEvents failed: %v", err)
	}

	select {
	case <-loaded:
	case <-time.After(10 * time.Second):
		t.Fatal("Timeout waiting for events_loaded from restarted server")
	}

	mu.Lock()
	defer mu.Unlock()
	if len(events) < 6 {
		t.Fatalf("Expected pre-restart history (>=6 events) to be replayed after restart, got %d: %v", len(events), events)
	}
	var maxSeen int64
	for _, e := range events {
		if e.Seq > maxSeen {
			maxSeen = e.Seq
		}
	}
	if maxSeen < lastSeq {
		t.Errorf("Replayed events max seq %d < injected lastSeq %d", maxSeen, lastSeq)
	}
	t.Logf("Startup recovery replayed %d pre-restart events (max seq %d) after simulated restart ✓", len(events), maxSeen)
}
