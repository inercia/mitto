package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// safeBuf is a thread-safe bytes.Buffer for capturing proxy output.
type safeBuf struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (s *safeBuf) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.Write(p)
}

func (s *safeBuf) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.String()
}

// TestMCPProxy_PingNotStarvedBySlowCall verifies that the proxy dispatches
// requests concurrently: a fast ping (id=3) must be answered BEFORE the slow
// tools/call (id=2) even though the ping is sent after the slow call.
func TestMCPProxy_PingNotStarvedBySlowCall(t *testing.T) {
	const slowDelay = 400 * time.Millisecond

	// Track the order in which the server responds.
	var respondedOrder []int
	var orderMu sync.Mutex

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, "read body", http.StatusInternalServerError)
			return
		}

		var msg struct {
			Method string      `json:"method"`
			ID     interface{} `json:"id"`
		}
		if err := json.Unmarshal(body, &msg); err != nil {
			http.Error(w, "bad json", http.StatusBadRequest)
			return
		}

		// initialize: respond immediately and set a session ID.
		if msg.Method == "initialize" {
			w.Header().Set("Mcp-Session-Id", "test-session-123")
			w.Header().Set("Content-Type", "application/json")
			resp := fmt.Sprintf(`{"jsonrpc":"2.0","id":%v,"result":{}}`, jsonID(msg.ID))
			w.Write([]byte(resp))
			return
		}

		// tools/call (slow): block for slowDelay.
		if msg.Method == "tools/call" {
			time.Sleep(slowDelay)
			orderMu.Lock()
			respondedOrder = append(respondedOrder, 2)
			orderMu.Unlock()
			w.Header().Set("Content-Type", "application/json")
			resp := fmt.Sprintf(`{"jsonrpc":"2.0","id":%v,"result":{}}`, jsonID(msg.ID))
			w.Write([]byte(resp))
			return
		}

		// ping: respond immediately.
		if msg.Method == "ping" {
			orderMu.Lock()
			respondedOrder = append(respondedOrder, 3)
			orderMu.Unlock()
			w.Header().Set("Content-Type", "application/json")
			resp := fmt.Sprintf(`{"jsonrpc":"2.0","id":%v,"result":{}}`, jsonID(msg.ID))
			w.Write([]byte(resp))
			return
		}

		// Unknown — 204.
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	// Build the input: initialize, then slow tools/call, then fast ping.
	input := strings.Join([]string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"slow_tool","arguments":{}}}`,
		`{"jsonrpc":"2.0","id":3,"method":"ping","params":{}}`,
	}, "\n") + "\n"

	in := strings.NewReader(input)
	var out safeBuf

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	done := make(chan error, 1)
	go func() {
		done <- runMCPProxyIO(ctx, srv.URL, in, &out)
	}()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("runMCPProxyIO returned error: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("runMCPProxyIO did not return within timeout")
	}

	// Verify response order: ping (id=3) must have been answered BEFORE slow call (id=2).
	orderMu.Lock()
	order := respondedOrder
	orderMu.Unlock()

	if len(order) != 2 {
		t.Fatalf("expected 2 timed responses (tools/call + ping), got %d: %v", len(order), order)
	}
	if order[0] != 3 || order[1] != 2 {
		t.Errorf("ping should be answered before slow call; got response order %v (want [3 2])", order)
	}

	// Also verify the proxy output contains all three response IDs.
	captured := out.String()
	for _, id := range []string{`"id":1`, `"id":2`, `"id":3`} {
		if !strings.Contains(captured, id) {
			t.Errorf("output missing response with %s; captured:\n%s", id, captured)
		}
	}
}

// jsonID renders an interface{} ID as JSON (handles float64 from json.Unmarshal).
func jsonID(id interface{}) string {
	b, _ := json.Marshal(id)
	return string(b)
}
