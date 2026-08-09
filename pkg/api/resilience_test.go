package client

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// wsTestServer is a minimal stub WebSocket server for exercising Session's
// opt-in resilience features (reconnect, seq resync, keepalive) end to end,
// without depending on internal/web. Each upgraded connection is dispatched
// to handler with its 0-based connection index, so tests can script
// different behavior per (re)dial attempt — e.g. drop the first connection
// to force a redial, then assert on what the second connection receives.
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

// TestReconnectDelay_Table pins the pure backoff function against the same
// constants the browser client uses (docs/devel/websockets/synchronization.md
// "Exponential Backoff (M2 fix)"): base 1s, doubling per attempt, capped at
// 30s, plus up to 30% jitter. No sleeping involved, so this covers the whole
// table instantly.
func TestReconnectDelay_Table(t *testing.T) {
	zero := func() float64 { return 0 }
	tests := []struct {
		name    string
		attempt int
		cfg     ReconnectConfig
		want    time.Duration
	}{
		{"attempt0_baseDelay_noJitter", 0, ReconnectConfig{jitter: zero}, 1 * time.Second},
		{"attempt1_doubles", 1, ReconnectConfig{jitter: zero}, 2 * time.Second},
		{"attempt2_quadruples", 2, ReconnectConfig{jitter: zero}, 4 * time.Second},
		{"attempt5_capsAtMaxDelay", 5, ReconnectConfig{jitter: zero}, 30 * time.Second},
		{"attempt10_staysCappedAtMaxDelay", 10, ReconnectConfig{jitter: zero}, 30 * time.Second},
		{
			"customBaseAndMax",
			3,
			ReconnectConfig{BaseDelay: 500 * time.Millisecond, MaxDelay: 2 * time.Second, jitter: zero},
			2 * time.Second, // 500ms*2^3=4s, capped to the 2s MaxDelay
		},
		{
			"fullJitterAddsExactlyJitterFactor",
			0,
			ReconnectConfig{jitter: func() float64 { return 1.0 }},
			1300 * time.Millisecond, // 1s base + 30%*1s*1.0 = 1.3s
		},
		{
			"halfJitter",
			2,
			ReconnectConfig{jitter: func() float64 { return 0.5 }},
			4*time.Second + 600*time.Millisecond, // exp=4s, jitter=4s*0.3*0.5=0.6s
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := reconnectDelay(tt.attempt, tt.cfg); got != tt.want {
				t.Errorf("reconnectDelay(%d, %+v) = %v, want %v", tt.attempt, tt.cfg, got, tt.want)
			}
		})
	}
}

// TestConnect_DefaultBehavior_DisconnectNotRetried pins the backward
// compatibility guarantee: a Session created with no SessionOption values
// behaves exactly as before this feature existed — a dropped connection is
// reported once via OnDisconnected and never redialed.
func TestConnect_DefaultBehavior_DisconnectNotRetried(t *testing.T) {
	ts := newWSTestServer(t, func(idx int, conn *websocket.Conn) {
		defer conn.Close()
		time.Sleep(20 * time.Millisecond)
		// Abrupt close, no WS close handshake.
	})

	var disconnected int32
	c := New(ts.srv.URL)
	sess, err := c.Connect(context.Background(), "sess-1", SessionCallbacks{
		OnDisconnected: func(err error) { atomic.AddInt32(&disconnected, 1) },
	})
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer sess.Close()

	time.Sleep(150 * time.Millisecond)

	if got := atomic.LoadInt32(&disconnected); got != 1 {
		t.Errorf("OnDisconnected called %d times, want exactly 1", got)
	}
	if got := atomic.LoadInt32(&ts.conns); got != 1 {
		t.Errorf("server saw %d connections, want exactly 1 (no reconnect without WithReconnect)", got)
	}
}

// TestReconnect_RedialsAndResyncsFromWatermark exercises the core resilience
// flow: an unexpected disconnect after WithReconnect fires OnReconnecting,
// backs off, redials (OnReconnected), and immediately resyncs via
// load_events{after_seq: watermark} using the seq observed before the drop
// (docs/devel/websockets/synchronization.md ws.onopen flow).
func TestReconnect_RedialsAndResyncsFromWatermark(t *testing.T) {
	loadEventsCh := make(chan map[string]interface{}, 1)
	ts := newWSTestServer(t, func(idx int, conn *websocket.Conn) {
		defer conn.Close()
		switch idx {
		case 0:
			_ = conn.WriteJSON(map[string]interface{}{
				"type": "agent_message", "seq": 5, "max_seq": 5,
				"data": map[string]string{"html": "hi"},
			})
			time.Sleep(30 * time.Millisecond)
			// Abrupt close: no close handshake, simulating a dropped connection.
		case 1:
			var msg map[string]interface{}
			if err := conn.ReadJSON(&msg); err == nil {
				loadEventsCh <- msg
			}
			time.Sleep(100 * time.Millisecond)
		}
	})

	var reconnecting, reconnected int32
	c := New(ts.srv.URL)
	sess, err := c.Connect(context.Background(), "sess-1", SessionCallbacks{
		OnReconnecting: func(attempt int, delay time.Duration) { atomic.AddInt32(&reconnecting, 1) },
		OnReconnected:  func() { atomic.AddInt32(&reconnected, 1) },
	}, WithReconnect(ReconnectConfig{BaseDelay: 10 * time.Millisecond, MaxDelay: 20 * time.Millisecond}))
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer sess.Close()

	select {
	case msg := <-loadEventsCh:
		if msg["type"] != "load_events" {
			t.Fatalf("second connection's first message = %v, want type=load_events", msg)
		}
		data, _ := msg["data"].(map[string]interface{})
		if data == nil || data["after_seq"] != float64(5) {
			t.Fatalf("load_events data = %v, want after_seq=5", data)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for resync load_events after reconnect")
	}

	if atomic.LoadInt32(&reconnecting) == 0 {
		t.Error("OnReconnecting was never called")
	}
	if atomic.LoadInt32(&reconnected) == 0 {
		t.Error("OnReconnected was never called")
	}
}

// TestReconnect_SessionGoneIsTerminal pins the documented circuit breaker
// (docs/devel/websockets/synchronization.md "Circuit Breaker: Terminal
// Session Errors"): session_gone must never be retried, even with
// WithReconnect enabled.
func TestReconnect_SessionGoneIsTerminal(t *testing.T) {
	ts := newWSTestServer(t, func(idx int, conn *websocket.Conn) {
		defer conn.Close()
		_ = conn.WriteJSON(map[string]interface{}{
			"type": "session_gone",
			"data": map[string]string{"session_id": "sess-1"},
		})
		time.Sleep(200 * time.Millisecond)
	})

	var goneCalled int32
	c := New(ts.srv.URL)
	sess, err := c.Connect(context.Background(), "sess-1", SessionCallbacks{
		OnSessionGone: func(sessionID string) { atomic.AddInt32(&goneCalled, 1) },
	}, WithReconnect(ReconnectConfig{BaseDelay: 5 * time.Millisecond, MaxDelay: 10 * time.Millisecond}))
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer sess.Close()

	// Give the supervisor plenty of time to (incorrectly) redial if
	// session_gone were not terminal.
	time.Sleep(150 * time.Millisecond)

	if got := atomic.LoadInt32(&ts.conns); got != 1 {
		t.Errorf("server saw %d connections, want exactly 1 (session_gone must not be retried)", got)
	}
	if atomic.LoadInt32(&goneCalled) != 1 {
		t.Error("OnSessionGone was not called")
	}
}

// TestClose_DoesNotTriggerRedial pins the other documented terminal
// condition: an explicit Close() must never be retried, even with
// WithReconnect enabled.
func TestClose_DoesNotTriggerRedial(t *testing.T) {
	ts := newWSTestServer(t, func(idx int, conn *websocket.Conn) {
		defer conn.Close()
		<-time.After(500 * time.Millisecond)
	})

	c := New(ts.srv.URL)
	sess, err := c.Connect(context.Background(), "sess-1", SessionCallbacks{},
		WithReconnect(ReconnectConfig{BaseDelay: 5 * time.Millisecond, MaxDelay: 10 * time.Millisecond}))
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}

	if err := sess.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	time.Sleep(100 * time.Millisecond)

	if got := atomic.LoadInt32(&ts.conns); got != 1 {
		t.Errorf("server saw %d connections after explicit Close, want exactly 1 (Close must not be retried)", got)
	}
}

// TestSeqDedup_DropsDuplicatesButAllowsSameSeqCoalescing pins the
// deduplication rule from docs/devel/websockets/sequence-numbers.md: a seq
// already seen is dropped, except when it equals the last-delivered seq
// (streaming coalescing), which must always pass through.
func TestSeqDedup_DropsDuplicatesButAllowsSameSeqCoalescing(t *testing.T) {
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

	var mu sync.Mutex
	var received []string
	c := New(ts.srv.URL)
	sess, err := c.Connect(context.Background(), "sess-1", SessionCallbacks{
		OnAgentMessage: func(html string) {
			mu.Lock()
			received = append(received, html)
			mu.Unlock()
		},
	}, WithSeqDedup(true))
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer sess.Close()

	time.Sleep(150 * time.Millisecond)

	mu.Lock()
	defer mu.Unlock()
	// seq=1 delivered, seq=2 delivered, seq=2 again allowed (coalescing with
	// the last-delivered seq), seq=1 again dropped (stale duplicate), seq=3
	// delivered.
	want := []string{"msg-1", "msg-2", "msg-2", "msg-3"}
	if !reflect.DeepEqual(received, want) {
		t.Errorf("received = %v, want %v", received, want)
	}
}

// TestEventsLoaded_StaleWatermarkResetsBeforeRepopulating pins the "server
// is always right" rule from docs/devel/websockets/sequence-numbers.md
// ("Stale Client Reset"): when the client's watermark exceeds the server's
// reported total_count on events_loaded, the client must reset its
// watermark/dedup window before repopulating from the fresh events, rather
// than trying to correct the server.
func TestEventsLoaded_StaleWatermarkResetsBeforeRepopulating(t *testing.T) {
	ts := newWSTestServer(t, func(idx int, conn *websocket.Conn) {
		defer conn.Close()
		_ = conn.WriteJSON(map[string]interface{}{
			"type": "agent_message", "seq": 100, "max_seq": 100,
			"data": map[string]string{"html": "stale-high-watermark"},
		})
		time.Sleep(20 * time.Millisecond)
		_ = conn.WriteJSON(map[string]interface{}{
			"type": "events_loaded",
			"data": map[string]interface{}{
				"events": []map[string]interface{}{
					{"seq": 1, "type": "agent_message", "timestamp": "t", "html": "fresh"},
				},
				"has_more":     false,
				"is_prompting": false,
				"total_count":  5,
			},
		})
		time.Sleep(50 * time.Millisecond)
	})

	var gotTotal int
	var gotHasMore bool
	done := make(chan struct{})
	c := New(ts.srv.URL)
	sess, err := c.Connect(context.Background(), "sess-1", SessionCallbacks{
		OnEventsLoadedWithMeta: func(events []SyncEvent, hasMore bool, isPrompting bool, totalCount int) {
			gotTotal = totalCount
			gotHasMore = hasMore
			close(done)
		},
	}, WithSeqDedup(true))
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer sess.Close()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for events_loaded")
	}

	if gotTotal != 5 || gotHasMore {
		t.Errorf("OnEventsLoadedWithMeta(totalCount=%d, hasMore=%v), want (5,false)", gotTotal, gotHasMore)
	}
	if got := sess.Watermark(); got != 1 {
		t.Errorf("Watermark() after stale reset+repopulate = %d, want 1 (reset from 100, then re-derived from the fresh event)", got)
	}
}

// TestKeepalive_ZombieDetectionForcesReconnect pins the zombie-connection
// detection contract: with WithKeepalive enabled and the server never
// acknowledging, MaxMissed consecutive un-acked sends must force-close the
// connection and — with WithReconnect also enabled — drive a normal
// redial (docs/devel/websockets/synchronization.md).
func TestKeepalive_ZombieDetectionForcesReconnect(t *testing.T) {
	ts := newWSTestServer(t, func(idx int, conn *websocket.Conn) {
		defer conn.Close()
		// Server never acknowledges keepalives; idle until force-closed by
		// the client (zombie case) or the test's own timeout.
		time.Sleep(300 * time.Millisecond)
	})

	var reconnected int32
	c := New(ts.srv.URL)
	sess, err := c.Connect(context.Background(), "sess-1", SessionCallbacks{
		OnReconnected: func() { atomic.AddInt32(&reconnected, 1) },
	},
		WithReconnect(ReconnectConfig{BaseDelay: 5 * time.Millisecond, MaxDelay: 10 * time.Millisecond}),
		WithKeepalive(KeepaliveConfig{Interval: 15 * time.Millisecond, MaxMissed: 1}),
	)
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer sess.Close()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if atomic.LoadInt32(&reconnected) > 0 && atomic.LoadInt32(&ts.conns) >= 2 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("zombie connection was not force-closed and redialed: reconnected=%d conns=%d",
		atomic.LoadInt32(&reconnected), atomic.LoadInt32(&ts.conns))
}

// TestKeepaliveAck_GapDetectionTriggersLoadEventsFromWatermark pins the
// max_seq piggybacking contract (docs/devel/websockets/synchronization.md
// "max_seq piggybacking"): a keepalive_ack whose max_seq is ahead of the
// client's watermark must reset the missed counter, fire OnKeepaliveAck,
// and immediately request the missing events via
// load_events{after_seq: watermark} — without waiting for the next
// keepalive round trip.
func TestKeepaliveAck_GapDetectionTriggersLoadEventsFromWatermark(t *testing.T) {
	loadEventsCh := make(chan map[string]interface{}, 1)
	ackSent := make(chan struct{})

	ts := newWSTestServer(t, func(idx int, conn *websocket.Conn) {
		defer conn.Close()
		_ = conn.WriteJSON(map[string]interface{}{
			"type": "agent_message", "seq": 3, "max_seq": 3,
			"data": map[string]string{"html": "hi"},
		})

		for {
			var msg map[string]interface{}
			if err := conn.ReadJSON(&msg); err != nil {
				return
			}
			switch msg["type"] {
			case "keepalive":
				_ = conn.WriteJSON(map[string]interface{}{
					"type": "keepalive_ack",
					"data": map[string]interface{}{"max_seq": 10, "is_prompting": false},
				})
				select {
				case <-ackSent:
				default:
					close(ackSent)
				}
			case "load_events":
				loadEventsCh <- msg
				return
			}
		}
	})

	var gotMaxSeq int64
	var gotIsPrompting bool
	c := New(ts.srv.URL)
	sess, err := c.Connect(context.Background(), "sess-1", SessionCallbacks{
		OnKeepaliveAck: func(maxSeq int64, isPrompting bool) {
			gotMaxSeq = maxSeq
			gotIsPrompting = isPrompting
		},
	},
		WithSeqDedup(true),
		WithKeepalive(KeepaliveConfig{Interval: 15 * time.Millisecond, MaxMissed: 100}),
	)
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer sess.Close()

	select {
	case <-ackSent:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for keepalive_ack round trip")
	}

	select {
	case msg := <-loadEventsCh:
		data, _ := msg["data"].(map[string]interface{})
		if data == nil || data["after_seq"] != float64(3) {
			t.Fatalf("gap-fill load_events data = %v, want after_seq=3", data)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for gap-fill load_events triggered by keepalive_ack")
	}

	if gotMaxSeq != 10 || gotIsPrompting {
		t.Errorf("OnKeepaliveAck(maxSeq=%d, isPrompting=%v), want (10,false)", gotMaxSeq, gotIsPrompting)
	}
}
