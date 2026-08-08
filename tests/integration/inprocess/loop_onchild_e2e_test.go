//go:build integration

// Package inprocess contains in-process integration tests for Mitto.
package inprocess

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/inercia/mitto/internal/client"
	"github.com/inercia/mitto/internal/session"
)

// createOnChildParentSession creates a top-level session rooted at workingDir
// (created if missing). The onChild trigger has no client.SetLoopRequest
// field yet (mitto-987y.8), so the loop itself is configured directly through
// the shared session.Store by setOnChildLoop below, not via ts.Client.SetLoop.
func createOnChildParentSession(t *testing.T, ts *TestServer, workingDir, name string) *client.SessionInfo {
	t.Helper()
	if err := os.MkdirAll(workingDir, 0755); err != nil {
		t.Fatalf("MkdirAll(%s) error = %v", workingDir, err)
	}
	sess, err := ts.Client.CreateSession(client.CreateSessionRequest{Name: name, WorkingDir: workingDir})
	if err != nil {
		t.Fatalf("CreateSession failed: %v", err)
	}
	return sess
}

// setOnChildLoop (re)configures parentID's loop, always keeping schedule armed
// alongside onChild (onChild can never be the sole trigger) with a schedule
// frequency long enough that it never fires during the test — only onChild
// deliveries move IterationCount.
func setOnChildLoop(t *testing.T, ts *TestServer, parentID string, cooldownSeconds int) {
	t.Helper()
	if err := ts.Store.Loop(parentID).Set(&session.LoopPrompt{
		Prompt:          "iterate",
		Enabled:         true,
		Frequency:       session.Frequency{Value: 999, Unit: session.FrequencyDays},
		Triggers:        []session.LoopTrigger{session.TriggerSchedule, session.TriggerOnChild},
		CooldownSeconds: cooldownSeconds,
	}); err != nil {
		t.Fatalf("Loop(%s).Set() error = %v", parentID, err)
	}
}

// createOnChildChild registers child metadata under parentID directly in the
// store — no real ACP process is needed since only the child's lifecycle
// (idle notification / deletion) matters to the onChild leg.
func createOnChildChild(t *testing.T, ts *TestServer, parentID, workingDir string) string {
	t.Helper()
	childID := session.GenerateSessionID()
	if err := ts.Store.Create(session.Metadata{
		SessionID:       childID,
		ACPServer:       "test-server",
		WorkingDir:      workingDir,
		Name:            "OnChild Test Child",
		ParentSessionID: parentID,
	}); err != nil {
		t.Fatalf("Store.Create(child) error = %v", err)
	}
	return childID
}

func waitOnChildIterationCount(t *testing.T, ts *TestServer, sessionID string, want int) {
	t.Helper()
	waitFor(t, 10*time.Second, func() bool {
		got, err := ts.Client.GetLoop(sessionID)
		return err == nil && got.IterationCount == want
	}, fmt.Sprintf("iteration_count to reach %d for session %s", want, sessionID))
}

// TestLoopOnChildE2E verifies the onChild loop trigger end-to-end against the
// mock ACP server: a child finishing a response (OnChildEndResponse, wired
// from the SessionManager idle bridge) and a child being deleted (through the
// REAL session.Store.Delete, exercising the Store.SetDeleteObserver seam wired
// in internal/web/server.go to LoopRunner.OnChildDeleted — the one path no
// unit test covers) both fire the parent's loop, a cooldown floor collapses a
// burst of child-idle events into a single delivery, and the additive-only
// constraint (onChild may never be the sole armed trigger) is enforced by the
// real session.Store.Loop(...).Set path — not just the REST handler layer
// already covered by TestHandleSessionLoop_OnChildAloneRejected.
func TestLoopOnChildE2E(t *testing.T) {
	ts := SetupTestServer(t)
	runner := ts.Server.LoopRunner()
	// Global floor at 0 so per-conversation CooldownSeconds fully controls timing.
	runner.SetMinLoopTasksCooldownSeconds(0)

	dir := filepath.Join(ts.TempDir, "workspace", "onchild")
	parent := createOnChildParentSession(t, ts, dir, "onchild-parent")
	defer ts.Client.DeleteSession(parent.SessionID)
	setOnChildLoop(t, ts, parent.SessionID, 0)

	t.Run("child_end_response_fires_parent", func(t *testing.T) {
		// Intentionally not deleted: the default ChildEvents set also arms
		// anyDeleted, so deleting this child would itself fire the parent a
		// second time and corrupt the iteration_count expected by the
		// following subtests.
		childID := createOnChildChild(t, ts, parent.SessionID, dir)

		runner.OnChildEndResponse(childID)
		waitOnChildIterationCount(t, ts, parent.SessionID, 1)
		waitOnTasksSessionIdle(t, ts, parent.SessionID)
	})

	t.Run("child_deleted_fires_parent", func(t *testing.T) {
		childID := createOnChildChild(t, ts, parent.SessionID, dir)

		if err := ts.Store.Delete(childID); err != nil {
			t.Fatalf("Store.Delete(%s) error = %v", childID, err)
		}
		waitOnChildIterationCount(t, ts, parent.SessionID, 2)
		waitOnTasksSessionIdle(t, ts, parent.SessionID)
	})

	t.Run("onchild_alone_rejected_via_store", func(t *testing.T) {
		// Validate() runs (and can fail) before LoopStore.Set acquires its
		// lock or writes anything, so calling it with an invalid config on
		// the shared parent is side-effect-free — it does not disturb the
		// iteration_count the other subtests depend on.
		err := ts.Store.Loop(parent.SessionID).Set(&session.LoopPrompt{
			Prompt:    "iterate",
			Enabled:   true,
			Triggers:  []session.LoopTrigger{session.TriggerOnChild},
			Frequency: session.Frequency{Value: 999, Unit: session.FrequencyDays},
		})
		if !errors.Is(err, session.ErrOnChildAlone) {
			t.Fatalf("Loop(%s).Set({Triggers: [onChild]}) error = %v, want ErrOnChildAlone", parent.SessionID, err)
		}
		// Config from the previous subtests must be untouched.
		waitOnChildIterationCount(t, ts, parent.SessionID, 2)
	})

	t.Run("cooldown_collapses_child_idle_burst", func(t *testing.T) {
		// Reset counters (clearing LastSentAt) so this subtest's own first
		// fire is never blocked by a delivery from an earlier subtest, then
		// arm a cooldown far longer than this subtest can possibly take.
		// Only after that first delivery lands do we fire the burst —
		// isolating the cooldown guard from the separate single-dispatch-slot
		// coalescing already covered elsewhere.
		if err := ts.Store.Loop(parent.SessionID).ResetCounters(); err != nil {
			t.Fatalf("ResetCounters() error = %v", err)
		}
		setOnChildLoop(t, ts, parent.SessionID, 60)

		first := createOnChildChild(t, ts, parent.SessionID, dir)
		runner.OnChildEndResponse(first)
		waitOnChildIterationCount(t, ts, parent.SessionID, 1)
		waitOnTasksSessionIdle(t, ts, parent.SessionID)

		for i := 0; i < 4; i++ {
			childID := createOnChildChild(t, ts, parent.SessionID, dir)
			runner.OnChildEndResponse(childID)
		}
		time.Sleep(300 * time.Millisecond)
		if got, err := ts.Client.GetLoop(parent.SessionID); err != nil {
			t.Fatalf("GetLoop() error = %v", err)
		} else if got.IterationCount != 1 {
			t.Fatalf("iteration_count after burst = %d, want 1 (cooldown must collapse the rest)", got.IterationCount)
		}
	})
}
