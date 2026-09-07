//go:build integration

package inprocess

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/inercia/mitto/pkg/api"
)

// =============================================================================
// mitto-lrt.2 Group D — Content/tool-status semantics regression baseline.
//
// Existing tool-call coverage (tool-calls-interleaved.json + callback_test.go)
// only exercises the "completed" status. This test adds the "failed" status
// path — new tests/fixtures/responses/tool-call-failed.json — to pin that
// Mitto relays and persists a failed tool-call status verbatim, rather than
// silently coercing it into a success state. Add-only: no existing assertions
// are touched; the mock server already supports arbitrary status strings
// (tests/mocks/acp-server/handler.go executeAction "tool_update"), so no mock
// code changes were needed, only a new scenario fixture.
// =============================================================================

// TestSemanticsBaseline_ToolCallFailedStatusPreserved verifies that a tool
// call ending in status "failed" (as opposed to "completed") is: (1) relayed
// live over the WebSocket via OnToolUpdate with status=="failed", and (2)
// persisted so a later LoadEvents replay still shows status=="failed" rather
// than being normalized/dropped.
func TestSemanticsBaseline_ToolCallFailedStatusPreserved(t *testing.T) {
	ts := SetupTestServer(t)

	sess, err := ts.Client.CreateSession(api.CreateSessionRequest{Name: "tool-call-failed"})
	if err != nil {
		t.Fatalf("CreateSession failed: %v", err)
	}
	defer ts.Client.DeleteSession(sess.SessionID)

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	var (
		mu             sync.Mutex
		liveStatuses   []string
		promptComplete = make(chan struct{})
	)
	ws, err := ts.Client.Connect(ctx, sess.SessionID, api.SessionCallbacks{
		OnToolUpdate: func(id, status string) {
			mu.Lock()
			liveStatuses = append(liveStatuses, status)
			mu.Unlock()
		},
		OnPromptComplete: func(_ int) {
			close(promptComplete)
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

	// Matches tests/fixtures/responses/tool-call-failed.json.
	if err := ws.SendPrompt("TEST:tool-call-failed"); err != nil {
		t.Fatalf("SendPrompt failed: %v", err)
	}

	select {
	case <-promptComplete:
	case <-time.After(10 * time.Second):
		t.Fatal("Timeout waiting for prompt completion")
	}

	mu.Lock()
	gotFailed := false
	for _, s := range liveStatuses {
		if s == "failed" {
			gotFailed = true
		}
		if s == "completed" {
			t.Errorf("tool-call-failed fixture must never report status=completed live, got statuses=%v", liveStatuses)
		}
	}
	mu.Unlock()
	if !gotFailed {
		t.Fatalf("expected a live OnToolUpdate with status=failed, got statuses=%v", liveStatuses)
	}

	// Reconnect and replay from storage: the persisted event must still carry
	// status=="failed", proving it was not normalized/dropped on write.
	var (
		mu2    sync.Mutex
		events []api.SyncEvent
		loaded = make(chan struct{}, 1)
	)
	ws2, err := ts.Client.Connect(ctx, sess.SessionID, api.SessionCallbacks{
		OnEventsLoaded: func(evts []api.SyncEvent, hasMore bool, isPrompting bool) {
			mu2.Lock()
			events = append(events, evts...)
			mu2.Unlock()
			select {
			case loaded <- struct{}{}:
			default:
			}
		},
	})
	if err != nil {
		t.Fatalf("Second connect failed: %v", err)
	}
	defer ws2.Close()

	time.Sleep(150 * time.Millisecond)
	if err := ws2.LoadEvents(100, 0, 0); err != nil {
		t.Fatalf("LoadEvents (replay) failed: %v", err)
	}
	select {
	case <-loaded:
	case <-time.After(5 * time.Second):
		t.Fatal("Timeout waiting for events_loaded replay")
	}

	mu2.Lock()
	defer mu2.Unlock()
	foundPersistedFailed := false
	for _, e := range events {
		if e.Type != "tool_call_update" {
			continue
		}
		data, ok := e.Data.(map[string]interface{})
		if !ok {
			continue
		}
		if status, _ := data["status"].(string); status == "failed" {
			foundPersistedFailed = true
		}
	}
	if !foundPersistedFailed {
		t.Errorf("expected a persisted tool_call_update event with status=failed after replay, got %d events", len(events))
	}
	t.Logf("tool-call failed status relayed live and persisted correctly ✓ (live=%v)", liveStatuses)
}
