//go:build integration

package inprocess

import (
	"context"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/inercia/mitto/internal/client"
	"github.com/inercia/mitto/internal/web"
)

// coldstartCapturingHandler collects records whose message is one of the
// coldstart marker names, along with a snapshot of their attributes. The
// integration test uses it to assert that at least one cold_start_phase (or
// cold_start_summary) record carries a live_acp_processes>=1 attribute — the
// signal that the ACPProcessManager contention provider is wired (mitto-7o2).
type coldstartCapturingHandler struct {
	mu   sync.Mutex
	recs []map[string]any
}

func (h *coldstartCapturingHandler) Enabled(context.Context, slog.Level) bool { return true }

func (h *coldstartCapturingHandler) Handle(_ context.Context, r slog.Record) error {
	if r.Message != "cold_start_phase" && r.Message != "cold_start_summary" {
		return nil
	}
	attrs := make(map[string]any, r.NumAttrs())
	r.Attrs(func(a slog.Attr) bool {
		attrs[a.Key] = a.Value.Any()
		return true
	})
	h.mu.Lock()
	h.recs = append(h.recs, attrs)
	h.mu.Unlock()
	return nil
}

func (h *coldstartCapturingHandler) WithAttrs([]slog.Attr) slog.Handler { return h }
func (h *coldstartCapturingHandler) WithGroup(string) slog.Handler      { return h }

// findLiveACPNonZero returns true if any captured record has live_acp_processes
// present with a numeric value >= 1.
func (h *coldstartCapturingHandler) findLiveACPNonZero() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, rec := range h.recs {
		v, ok := rec["live_acp_processes"]
		if !ok {
			continue
		}
		switch n := v.(type) {
		case int64:
			if n >= 1 {
				return true
			}
		case int:
			if n >= 1 {
				return true
			}
		}
	}
	return false
}

// TestColdStart_LiveACPContentionAttribute asserts that at least one
// cold_start_phase (or cold_start_summary) log entry produced during a real
// session cold-start carries live_acp_processes>=1, proving the coldstart
// LiveACPCounter provider is wired end-to-end (mitto-7o2 acceptance criterion).
func TestColdStart_LiveACPContentionAttribute(t *testing.T) {
	cap := &coldstartCapturingHandler{}
	logger := slog.New(cap)

	ts := SetupTestServer(t, func(c *web.Config) {
		c.Logger = logger
	})

	// Create a session — this triggers the full cold-start path, which the
	// coldstart tracer instruments via bs.coldTrace.Phase(...) inside
	// BackgroundSession initialization.
	sess, err := ts.Client.CreateSession(client.CreateSessionRequest{Name: "coldstart-test"})
	if err != nil {
		t.Fatalf("CreateSession failed: %v", err)
	}
	if sess.SessionID == "" {
		t.Fatal("CreateSession returned empty session id")
	}

	// The first Phase() call in each cold-start attaches a ContentionSnapshot
	// via Contention().LogAttrs(). Give the async coldstart trace a brief window
	// to reach the first Phase and flush the log.
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if cap.findLiveACPNonZero() {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}

	// Dump what we saw to aid debugging on failure.
	cap.mu.Lock()
	defer cap.mu.Unlock()
	t.Fatalf("no cold_start_phase/summary record with live_acp_processes>=1 within timeout; captured %d records: %+v",
		len(cap.recs), cap.recs)
}
