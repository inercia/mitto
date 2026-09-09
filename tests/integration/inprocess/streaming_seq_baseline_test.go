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
// mitto-lrt.2 Group C — Streaming & sequence-boundary regression baseline.
//
// Existing sequence_contract_test.go / sequence_isolation_test.go cover
// monotonicity, persistence and reconnect sync, but never (a) cancel a prompt
// WHILE it is actively streaming (only preparation-time cancellation is
// covered, in websocket_prompt_preparation_test.go), nor (b) the
// after_seq == current-max-seq boundary (a client fully caught up asking for
// "anything newer" should get zero events, not an error or a wrapped page of
// stale events). Add-only: no existing assertions are touched.
// =============================================================================

// TestStreamingBaseline_CancelDuringActiveStreaming pins the behavior of
// Session.Cancel() called mid-stream. The mock ACP server's read loop is
// single-threaded (see tests/mocks/acp-server/main.go), so it cannot react to
// the session/cancel notification until it finishes synchronously streaming
// the slow-response fixture's 8 chunks (~4.5s total with delays). Cancellation
// therefore takes effect on MITTO'S side by cancelling the in-flight RPC's
// context (BackgroundSession.cancelPromptTurn), independent of whether the
// agent process itself ever sees or honors the cancel. This test pins that:
// the turn ends (IsPrompting()==false) promptly after Cancel(), well before
// the fixture's natural ~4.5s completion time — the caller does not have to
// wait for the agent to finish streaming.
func TestStreamingBaseline_CancelDuringActiveStreaming(t *testing.T) {
	ts := SetupTestServer(t)

	sess, err := ts.Client.CreateSession(api.CreateSessionRequest{Name: "cancel-mid-stream"})
	if err != nil {
		t.Fatalf("CreateSession failed: %v", err)
	}
	defer ts.Client.DeleteSession(sess.SessionID)

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	var (
		mu         sync.Mutex
		chunksSeen int
		firstChunk = make(chan struct{}, 1)
	)
	ws, err := ts.Client.Connect(ctx, sess.SessionID, api.SessionCallbacks{
		OnAgentMessage: func(html string) {
			mu.Lock()
			chunksSeen++
			n := chunksSeen
			mu.Unlock()
			if n == 1 {
				select {
				case firstChunk <- struct{}{}:
				default:
				}
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

	// Matches tests/fixtures/responses/slow-response.json: 500ms initial delay,
	// then 8 chunks each with a 500ms delay (~4.5s total to stream naturally).
	if err := ws.SendPrompt("Please give me a slow response"); err != nil {
		t.Fatalf("SendPrompt failed: %v", err)
	}

	select {
	case <-firstChunk:
	case <-time.After(5 * time.Second):
		t.Fatal("Timeout waiting for the first streamed chunk before cancelling")
	}

	cancelStart := time.Now()
	if err := ws.Cancel(); err != nil {
		t.Fatalf("Cancel failed: %v", err)
	}

	sm := ts.Server.GetSessionManager()
	waitFor(t, 3*time.Second, func() bool {
		bs := sm.GetSession(sess.SessionID)
		return bs != nil && !bs.IsPrompting()
	}, "turn to end promptly after Cancel()")
	elapsed := time.Since(cancelStart)

	// The fixture takes ~4.5s to complete naturally; the turn must end in a
	// small fraction of that, proving cancellation is not gated on the agent
	// process finishing its scripted stream.
	const naturalCompletion = 4500 * time.Millisecond
	if elapsed >= naturalCompletion {
		t.Errorf("turn took %v to end after Cancel(); expected well under the fixture's natural %v completion time", elapsed, naturalCompletion)
	}
	t.Logf("Cancel() ended the turn in %v (natural completion ~%v) ✓", elapsed, naturalCompletion)
}

// TestStreamingBaseline_AfterSeqEqualsMaxSeqReturnsNoEvents pins the
// after_seq == current-max-seq boundary: a client that is fully caught up and
// asks LoadEvents for anything strictly newer than the current max seq must
// receive zero events (not an error, and not a page including the last-seen
// event again).
func TestStreamingBaseline_AfterSeqEqualsMaxSeqReturnsNoEvents(t *testing.T) {
	ts := SetupTestServer(t)

	sess, err := ts.Client.CreateSession(api.CreateSessionRequest{Name: "after-seq-boundary"})
	if err != nil {
		t.Fatalf("CreateSession failed: %v", err)
	}
	defer ts.Client.DeleteSession(sess.SessionID)

	inj := NewEventInjector(t, ts, sess.SessionID)
	inj.InjectMixed(2) // 4 events
	maxSeq := inj.CurrentMaxSeq()
	if maxSeq <= 0 {
		t.Fatalf("expected a positive max seq after injection, got %d", maxSeq)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	var (
		mu       sync.Mutex
		events   []api.SyncEvent
		gotReply = make(chan struct{}, 1)
	)
	ws, err := ts.Client.Connect(ctx, sess.SessionID, api.SessionCallbacks{
		OnEventsLoaded: func(evts []api.SyncEvent, hasMore bool, isPrompting bool) {
			mu.Lock()
			events = append(events, evts...)
			mu.Unlock()
			select {
			case gotReply <- struct{}{}:
			default:
			}
		},
	})
	if err != nil {
		t.Fatalf("Connect failed: %v", err)
	}
	defer ws.Close()

	time.Sleep(200 * time.Millisecond)
	// Ask for anything strictly newer than the current max seq: the caller is
	// fully caught up.
	if err := ws.LoadEvents(50, maxSeq, 0); err != nil {
		t.Fatalf("LoadEvents failed: %v", err)
	}

	select {
	case <-gotReply:
	case <-time.After(5 * time.Second):
		t.Fatal("Timeout waiting for events_loaded response")
	}

	mu.Lock()
	defer mu.Unlock()
	if len(events) != 0 {
		t.Errorf("after_seq==maxSeq(%d) should return zero events, got %d: %v", maxSeq, len(events), events)
	}
	t.Logf("after_seq==maxSeq(%d) correctly returned zero events ✓", maxSeq)
}
