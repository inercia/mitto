package client

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// TestEvents_EarlyContextCancellation pins the ctx.Done() branch of Events:
// a caller that cancels before any event arrives gets exactly one terminal
// (Event{}, ctx.Err()) yield, never a hang.
func TestEvents_EarlyContextCancellation(t *testing.T) {
	ts := newWSTestServer(t, func(idx int, conn *websocket.Conn) {
		defer conn.Close()
		time.Sleep(200 * time.Millisecond)
	})
	c := New(ts.srv.URL)
	sess, err := c.Connect(context.Background(), "sess-1", SessionCallbacks{})
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer sess.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // already cancelled before ranging starts

	var gotErr error
	iters := 0
	for _, err := range sess.Events(ctx) {
		iters++
		gotErr = err
	}
	if iters != 1 {
		t.Fatalf("Events yielded %d times, want exactly 1 (terminal error only)", iters)
	}
	if !errors.Is(gotErr, context.Canceled) {
		t.Errorf("got err = %v, want context.Canceled", gotErr)
	}
}

// TestEvents_MidStreamDisconnect_TerminatesWithErrDisconnected pins the
// disconnect-termination contract: a delivered event is followed by a
// non-nil, ErrDisconnected-wrapped terminal error when the connection drops.
func TestEvents_MidStreamDisconnect_TerminatesWithErrDisconnected(t *testing.T) {
	ts := newWSTestServer(t, func(idx int, conn *websocket.Conn) {
		defer conn.Close()
		_ = conn.WriteJSON(map[string]interface{}{
			"type": "agent_message", "seq": 1, "max_seq": 1,
			"data": map[string]string{"html": "hi"},
		})
		time.Sleep(30 * time.Millisecond)
		// Abrupt close: no WS close handshake.
	})
	c := New(ts.srv.URL)
	sess, err := c.Connect(context.Background(), "sess-1", SessionCallbacks{})
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer sess.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	var gotEvent Event
	var gotErr error
	for ev, err := range sess.Events(ctx) {
		if err != nil {
			gotErr = err
			break
		}
		gotEvent = ev
	}
	if gotEvent.Kind != EventAgentMessage || gotEvent.HTML != "hi" {
		t.Fatalf("first event = %+v, want agent_message/hi", gotEvent)
	}
	if !errors.Is(gotErr, ErrDisconnected) {
		t.Errorf("terminal err = %v, want ErrDisconnected", gotErr)
	}
}

// TestEvents_SlowConsumer_TerminatesWithErrSlowConsumer pins the
// non-blocking-send backpressure contract: with a bounded buffer of 1, a
// consumer that falls behind the producer terminates the stream with
// ErrSlowConsumer instead of blocking the read loop or dropping silently.
func TestEvents_SlowConsumer_TerminatesWithErrSlowConsumer(t *testing.T) {
	ts := newWSTestServer(t, func(idx int, conn *websocket.Conn) {
		defer conn.Close()
		for i := 1; i <= 5; i++ {
			_ = conn.WriteJSON(map[string]interface{}{
				"type": "agent_message", "seq": i, "max_seq": i,
				"data": map[string]string{"html": fmt.Sprintf("msg-%d", i)},
			})
		}
		time.Sleep(200 * time.Millisecond)
	})
	c := New(ts.srv.URL)
	sess, err := c.Connect(context.Background(), "sess-1", SessionCallbacks{}, WithStreamBuffer(1))
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer sess.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	var gotErr error
	count := 0
	for _, err := range sess.Events(ctx) {
		if err != nil {
			gotErr = err
			break
		}
		count++
		if count == 1 {
			// Let the producer race far ahead of the size-1 buffer before
			// pulling the next event, forcing an overflow.
			time.Sleep(150 * time.Millisecond)
		}
	}
	if !errors.Is(gotErr, ErrSlowConsumer) {
		t.Fatalf("got err = %v, want ErrSlowConsumer", gotErr)
	}
}

// TestEvents_ConcurrentCall_ReturnsErrStreamActive pins the at-most-one-
// stream-per-Session rule: a second Events call while one is already
// ranging must fail fast with ErrStreamActive rather than blocking or
// silently sharing the buffer.
func TestEvents_ConcurrentCall_ReturnsErrStreamActive(t *testing.T) {
	ts := newWSTestServer(t, func(idx int, conn *websocket.Conn) {
		defer conn.Close()
		time.Sleep(300 * time.Millisecond)
	})
	c := New(ts.srv.URL)
	sess, err := c.Connect(context.Background(), "sess-1", SessionCallbacks{})
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer sess.Close()

	firstCtx, cancelFirst := context.WithCancel(context.Background())
	defer cancelFirst()
	started := make(chan struct{})
	go func() {
		for _, err := range sess.Events(firstCtx) {
			close(started)
			if err != nil {
				return
			}
		}
	}()
	// registerStream happens synchronously at the top of the ranging
	// goroutine; give it a moment to run before racing the second call.
	time.Sleep(30 * time.Millisecond)
	_ = started

	var gotErr error
	for _, err := range sess.Events(context.Background()) {
		gotErr = err
		break
	}
	if !errors.Is(gotErr, ErrStreamActive) {
		t.Fatalf("got err = %v, want ErrStreamActive", gotErr)
	}
}

// TestEventsChan_CoexistsWithCallbacks pins the documented coexistence
// guarantee: SessionCallbacks and the channel/iterator stream are both fed
// from the same read loop without racing, so a caller can rely on both at
// once for the same message.
func TestEventsChan_CoexistsWithCallbacks(t *testing.T) {
	ts := newWSTestServer(t, func(idx int, conn *websocket.Conn) {
		defer conn.Close()
		_ = conn.WriteJSON(map[string]interface{}{
			"type": "agent_message", "seq": 1, "max_seq": 1,
			"data": map[string]string{"html": "hi"},
		})
		time.Sleep(100 * time.Millisecond)
	})

	var mu sync.Mutex
	var gotCallback string
	c := New(ts.srv.URL)
	sess, err := c.Connect(context.Background(), "sess-1", SessionCallbacks{
		OnAgentMessage: func(html string) {
			mu.Lock()
			gotCallback = html
			mu.Unlock()
		},
	})
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer sess.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	events, errc, err := sess.EventsChan(ctx)
	if err != nil {
		t.Fatalf("EventsChan: %v", err)
	}

	select {
	case ev := <-events:
		if ev.Kind != EventAgentMessage || ev.HTML != "hi" {
			t.Fatalf("got event %+v, want agent_message/hi", ev)
		}
	case streamErr := <-errc:
		t.Fatalf("stream terminated early: %v", streamErr)
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for streamed event")
	}

	// Give the callback (invoked before the stream, same read-loop
	// goroutine) a moment to be observed.
	time.Sleep(20 * time.Millisecond)
	mu.Lock()
	defer mu.Unlock()
	if gotCallback != "hi" {
		t.Errorf("OnAgentMessage callback = %q, want %q", gotCallback, "hi")
	}
}
