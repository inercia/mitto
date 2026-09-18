//go:build integration

package inprocess

import (
	"context"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/inercia/mitto/internal/config"
	"github.com/inercia/mitto/internal/processors"
	"github.com/inercia/mitto/internal/session"
	"github.com/inercia/mitto/internal/web"
	"github.com/inercia/mitto/pkg/api"
)

// TestKnowledgeRouterClose_EndToEnd_DispatchIDRoundTrips is the mitto-3od.6
// end-to-end integration test for the close-phase knowledge-router pipeline:
// archive -> SessionManager.ApplyOnCloseProcessors -> Manager.ApplyOnClose ->
// SetPromptCompletionFunc -> WorkspaceAuxiliaryManager.PromptProcessorTracked
// -> auxiliary dispatch to the (mock) ACP agent -> parseProcessorCompletion
// -> close-router.json sidecar write.
//
// Before mitto-3od.6, mock ACP fixtures were static strings, so this path
// could never be exercised end-to-end: parseProcessorCompletion requires the
// terminal MITTO_PROCESSOR_COMPLETION line to echo back the EXACT
// runtime-generated dispatch_id embedded in the outbound prompt, which a
// static fixture cannot produce. tests/fixtures/responses/knowledge-router-close.json
// captures that dispatch_id via a regex group, and the mock now substitutes
// "${N}" placeholders in response chunks (tests/mocks/acp-server/handler.go
// expandCaptures) to echo it back — closing the gap without weakening
// parseProcessorCompletion's exact-match contract.
func TestKnowledgeRouterClose_EndToEnd_DispatchIDRoundTrips(t *testing.T) {
	// The close-phase knowledge-router dispatches through an auxiliary ACP
	// session (WorkspaceAuxiliaryManager.PromptProcessorTracked), which
	// SetupTestServer's default DisableAuxiliaryPrewarm=true fully disables
	// (ACPProcessManager.DisableAuxiliary gates ALL auxiliary features, not
	// just pre-warming) — re-enable it for this test only. Also disable the
	// unrelated follow-up-suggestions feature (action buttons), which
	// otherwise fires its own auxiliary session right after the seed prompt
	// and can win the mock ACP process's single-RPC-slot race against the
	// close-phase dispatch, shedding it to the durable spool nondeterministically.
	disabled := false
	ts := SetupTestServer(t, func(c *web.Config) {
		c.DisableAuxiliaryPrewarm = false
		c.MittoConfig.Conversations = &config.ConversationsConfig{
			ActionButtons: &config.ActionButtonsConfig{Enabled: &disabled},
		}
	})

	// Wire the real builtin knowledge-router processor. In-process server
	// construction (unlike the CLI's root command) never deploys builtin
	// processors into MITTO_DIR, so load the shipping YAML directly from the
	// repo. Loading the whole builtin dir mirrors production: every OTHER
	// close-phase processor's enabledWhen guards it out for this bare temp
	// workspace (no .beads dir; ACP server name is not augment/claude-code),
	// so knowledge-router is the only one that actually dispatches.
	builtinDir := findRepoFile(t, filepath.Join("config", "processors", "builtin"), "config/processors/builtin not found")
	procMgr := processors.NewManager(builtinDir, nil)
	if err := procMgr.Load(); err != nil {
		t.Fatalf("Load builtin processors: %v", err)
	}
	if procMgr.ProcessorCount() == 0 {
		t.Fatal("expected at least one builtin processor to load")
	}
	ts.Server.GetSessionManager().SetProcessorManager(procMgr)

	sess, err := ts.Client.CreateSession(api.CreateSessionRequest{Name: "kr-close-e2e"})
	if err != nil {
		t.Fatalf("CreateSession failed: %v", err)
	}
	defer ts.Client.DeleteSession(sess.SessionID)

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	var (
		mu             sync.Mutex
		closedOnce     bool
		promptComplete = make(chan struct{})
	)
	ws, err := ts.Client.Connect(ctx, sess.SessionID, api.SessionCallbacks{
		OnPromptComplete: func(_ int) {
			mu.Lock()
			defer mu.Unlock()
			if !closedOnce {
				closedOnce = true
				close(promptComplete)
			}
		},
	})
	if err != nil {
		t.Fatalf("Connect failed: %v", err)
	}
	defer ws.Close()

	if err := ws.LoadEvents(50, 0, 0); err != nil {
		t.Fatalf("LoadEvents failed: %v", err)
	}
	time.Sleep(150 * time.Millisecond)

	// Ordinary conversation turn — its content becomes part of the archived
	// history snapshot the knowledge-router classifies at close time. No
	// dedicated fixture is needed for this turn; the mock's default
	// unmatched-prompt echo response is fine.
	if err := ws.SendPrompt("mitto-3od.6: please remember that I always squash-merge PRs."); err != nil {
		t.Fatalf("SendPrompt failed: %v", err)
	}

	select {
	case <-promptComplete:
	case <-time.After(10 * time.Second):
		t.Fatal("timed out waiting for seed prompt completion")
	}

	// Archive triggers SessionManager.ApplyOnCloseProcessors.
	if err := ts.Client.ArchiveSession(sess.SessionID, true); err != nil {
		t.Fatalf("ArchiveSession(true) failed: %v", err)
	}

	// ApplyOnClose's knowledge-router dispatch is fire-and-forget from
	// ApplyOnClose's own point of view (dispatchPromptBatch spawns an
	// untracked goroutine), so WaitForCloseProcessors only bounds the
	// synchronous portion of the pipeline. Poll the sidecar for the async
	// completion instead (mirrors internal/processors/close_router_test.go's
	// waitForCloseRouterState helper).
	deadline := time.Now().Add(15 * time.Second)
	var state session.CloseRouterState
	for {
		state, err = session.ReadCloseRouterState(ts.Store, sess.SessionID)
		if err != nil {
			t.Fatalf("ReadCloseRouterState: %v", err)
		}
		if len(state.Runs) > 0 && !state.Runs[len(state.Runs)-1].CompletedAt.IsZero() {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for close-router state to complete; last state = %+v", state)
		}
		time.Sleep(50 * time.Millisecond)
	}

	run := state.Runs[len(state.Runs)-1]
	if len(run.Findings) != 1 {
		t.Fatalf("expected exactly 1 finding round-tripped from the mock, got %d: %+v", len(run.Findings), run.Findings)
	}
	if got, want := run.Findings[0].Destination, "none"; got != want {
		t.Fatalf("finding destination = %q, want %q", got, want)
	}
}
