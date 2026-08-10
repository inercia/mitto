package chatui

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/gorilla/websocket"
	"github.com/inercia/mitto/pkg/api"
)

// wsTestServer is a minimal stub WebSocket server for driving RunPump
// against a scripted socket. Copied from pkg/api/resilience_test.go's
// unexported harness (~30 lines) per the mitto-pscc.12 plan decision: no
// second consumer has appeared for that helper, so it stays duplicated
// rather than becoming a new exported testing surface.
type wsTestServer struct {
	srv   *httptest.Server
	conns int32
}

func newWSTestServer(t *testing.T, handler func(idx int, conn *websocket.Conn)) *wsTestServer {
	t.Helper()
	ts := &wsTestServer{}
	upgrader := websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }}
	mux := http.NewServeMux()
	mux.HandleFunc("/mitto/api/sessions/", func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		idx := int(atomic.AddInt32(&ts.conns, 1)) - 1
		go handler(idx, conn)
	})
	ts.srv = httptest.NewServer(mux)
	t.Cleanup(ts.srv.Close)
	return ts
}

// fakeProgram is a programSender that records every tea.Msg sent to it, so
// pump tests can assert on what RunPump forwards without a real terminal
// program.
type fakeProgram struct {
	mu   sync.Mutex
	msgs []tea.Msg
}

func (p *fakeProgram) Send(msg tea.Msg) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.msgs = append(p.msgs, msg)
}

func (p *fakeProgram) snapshot() []tea.Msg {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]tea.Msg, len(p.msgs))
	copy(out, p.msgs)
	return out
}

// waitFor polls until cond returns true or the timeout elapses, failing the
// test on timeout with msg.
func waitFor(t *testing.T, timeout time.Duration, cond func() bool, msg string) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	if !cond() {
		t.Fatal(msg)
	}
}

// agentEventMsgs returns the eventMsg-wrapped api.Event.HTML bodies (in
// order) that RunPump has forwarded so far.
func agentEventMsgs(msgs []tea.Msg) []string {
	var out []string
	for _, m := range msgs {
		if em, ok := m.(eventMsg); ok && em.event.Kind == api.EventAgentMessage {
			out = append(out, em.event.HTML)
		}
	}
	return out
}

// --- reconnect across a forced disconnect -----------------------------------

// TestRunPump_DisconnectEndsStreamEvenWithSessionReconnectEnabled pins the
// real, documented contract at the pump/stream boundary: Session.
// terminateActiveStream fires unconditionally on every disconnect
// (session.go readUntilError), "regardless of whether WithReconnect will
// redial afterward" — so a *api.Session configured with WithReconnect
// still terminates the specific EventsChan() stream RunPump is reading from
// on the very first drop, before any redial happens. RunPump therefore sees
// exactly one streamEndMsg here and returns, never the second connection's
// event — matching pump.go's own doc comment that pump-level reconnect is
// explicitly out of scope until mitto-rwxq.5 lands. This is the behavior a
// future reconnect-aware pump would need to change (re-register a fresh
// EventsChan() after OnReconnected), so it is worth pinning explicitly
// rather than assuming reconnect "just works" through this layer.
func TestRunPump_DisconnectEndsStreamEvenWithSessionReconnectEnabled(t *testing.T) {
	secondConnSeen := make(chan struct{})
	ts := newWSTestServer(t, func(idx int, conn *websocket.Conn) {
		defer conn.Close()
		switch idx {
		case 0:
			_ = conn.WriteJSON(map[string]interface{}{
				"type": "agent_message", "seq": 1, "max_seq": 1,
				"data": map[string]string{"html": "first"},
			})
			time.Sleep(30 * time.Millisecond)
			// Abrupt close, no WS close handshake, forcing the Session-level
			// supervisor to redial.
		case 1:
			close(secondConnSeen)
			_ = conn.WriteJSON(map[string]interface{}{
				"type": "agent_message", "seq": 2, "max_seq": 2,
				"data": map[string]string{"html": "second"},
			})
			time.Sleep(100 * time.Millisecond)
		}
	})

	c := api.New(ts.srv.URL)
	sess, err := c.Connect(context.Background(), "sess-1", api.SessionCallbacks{},
		api.WithReconnect(api.ReconnectConfig{BaseDelay: 10 * time.Millisecond, MaxDelay: 20 * time.Millisecond}))
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer sess.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	evCh, errCh, err := sess.EventsChan(ctx)
	if err != nil {
		t.Fatalf("EventsChan: %v", err)
	}

	program := &fakeProgram{}
	done := make(chan struct{})
	go func() {
		RunPump(ctx, evCh, errCh, program)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("RunPump did not return after the disconnect terminated its stream")
	}

	// The Session-level supervisor redials independently of the (now dead)
	// stream RunPump was reading from; wait for it so the test does not
	// exit while that goroutine is still writing to the closed connection.
	waitFor(t, 2*time.Second, func() bool {
		select {
		case <-secondConnSeen:
			return true
		default:
			return false
		}
	}, "Session-level supervisor never redialed")

	msgs := program.snapshot()
	got := agentEventMsgs(msgs)
	if len(got) != 1 || got[0] != "first" {
		t.Fatalf("agent messages relayed via the pump = %v, want exactly [\"first\"] (the redial's \"second\" event belongs to a stream RunPump was never re-registered on)", got)
	}
	var ends int
	for _, m := range msgs {
		if _, ok := m.(streamEndMsg); ok {
			ends++
		}
	}
	if ends != 1 {
		t.Errorf("streamEndMsg count = %d, want exactly 1", ends)
	}
}

// --- duplicate / out-of-order seq --------------------------------------------

// TestRunPump_DuplicateAndOutOfOrderSeq_DedupApplies pins that RunPump sees
// exactly the events the Session's own dedup (WithSeqDedup) lets through:
// a duplicate is dropped, but a repeat of the last-delivered seq (streaming
// coalescing) still passes, matching
// pkg/api's TestSeqDedup_DropsDuplicatesButAllowsSameSeqCoalescing at the
// pump layer.
func TestRunPump_DuplicateAndOutOfOrderSeq_DedupApplies(t *testing.T) {
	ts := newWSTestServer(t, func(idx int, conn *websocket.Conn) {
		defer conn.Close()
		for _, seq := range []int64{1, 2, 2, 1, 3} {
			_ = conn.WriteJSON(map[string]interface{}{
				"type": "agent_message", "seq": seq, "max_seq": seq,
				"data": map[string]string{"html": fmt.Sprintf("msg-%d", seq)},
			})
		}
		time.Sleep(100 * time.Millisecond)
	})

	c := api.New(ts.srv.URL)
	sess, err := c.Connect(context.Background(), "sess-1", api.SessionCallbacks{}, api.WithSeqDedup(true))
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer sess.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	evCh, errCh, err := sess.EventsChan(ctx)
	if err != nil {
		t.Fatalf("EventsChan: %v", err)
	}

	program := &fakeProgram{}
	go RunPump(ctx, evCh, errCh, program)

	want := []string{"msg-1", "msg-2", "msg-2", "msg-3"}
	waitFor(t, 2*time.Second, func() bool {
		return len(agentEventMsgs(program.snapshot())) >= len(want)
	}, "RunPump did not relay the expected deduplicated sequence")

	got := agentEventMsgs(program.snapshot())
	if len(got) != len(want) {
		t.Fatalf("agent messages relayed = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("agent messages relayed = %v, want %v", got, want)
			break
		}
	}
}

// --- close-frame path yields exactly one streamEndMsg ------------------------

// TestRunPump_StreamCloses_SendsExactlyOneStreamEndMsgWithTerminalError pins
// the termination contract pump.go documents against Session.EventsChan:
// when evCh closes, RunPump must send exactly one streamEndMsg carrying the
// terminal error consumed from errCh, then return.
func TestRunPump_StreamCloses_SendsExactlyOneStreamEndMsgWithTerminalError(t *testing.T) {
	ts := newWSTestServer(t, func(idx int, conn *websocket.Conn) {
		defer conn.Close()
		<-time.After(500 * time.Millisecond)
	})

	c := api.New(ts.srv.URL)
	sess, err := c.Connect(context.Background(), "sess-1", api.SessionCallbacks{})
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer sess.Close()

	ctx, cancel := context.WithCancel(context.Background())
	evCh, errCh, err := sess.EventsChan(ctx)
	if err != nil {
		t.Fatalf("EventsChan: %v", err)
	}

	program := &fakeProgram{}
	done := make(chan struct{})
	go func() {
		RunPump(context.Background(), evCh, errCh, program)
		close(done)
	}()

	// Cancelling ctx is EventsChan's own documented termination path: its
	// background goroutine sends ctx.Err() on errc and closes both channels.
	cancel()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("RunPump did not return after the event stream terminated")
	}

	msgs := program.snapshot()
	var ends []streamEndMsg
	for _, m := range msgs {
		if se, ok := m.(streamEndMsg); ok {
			ends = append(ends, se)
		}
	}
	if len(ends) != 1 {
		t.Fatalf("streamEndMsg count = %d, want exactly 1 (msgs = %+v)", len(ends), msgs)
	}
	if ends[0].err == nil {
		t.Error("streamEndMsg.err is nil, want the terminal error from errCh")
	}
	if !errors.Is(ends[0].err, context.Canceled) {
		t.Errorf("streamEndMsg.err = %v, want context.Canceled (EventsChan's ctx-cancel termination path)", ends[0].err)
	}
}
