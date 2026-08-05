package coldstart

import (
	"bytes"
	"context"
	"log/slog"
	"math"
	"runtime"
	"strings"
	"testing"
	"time"
)

func newTestLogger() (*slog.Logger, *bytes.Buffer) {
	var buf bytes.Buffer
	h := slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo})
	return slog.New(h), &buf
}

func TestPhaseMonotonicAndPhaseMs(t *testing.T) {
	logger, buf := newTestLogger()
	tr := New(logger, "sess-1", "ws-1")

	tr.Phase("begin")
	time.Sleep(20 * time.Millisecond)
	tr.Phase("second")

	// Inspect recorded phases via Summary.
	tr.Summary("ok")
	sums := RecentSummaries(1)
	if len(sums) != 1 {
		t.Fatalf("expected 1 summary, got %d", len(sums))
	}
	ph := sums[0].Phases
	if len(ph) != 2 {
		t.Fatalf("expected 2 phases, got %d", len(ph))
	}
	if ph[0].ElapsedMs != 0 || ph[0].PhaseMs != 0 {
		t.Errorf("first phase should have zero elapsed/phase, got %+v", ph[0])
	}
	if ph[1].ElapsedMs < ph[0].ElapsedMs {
		t.Errorf("elapsed not monotonic: %d < %d", ph[1].ElapsedMs, ph[0].ElapsedMs)
	}
	if ph[1].PhaseMs <= 0 {
		t.Errorf("second phase_ms should be > 0, got %d", ph[1].PhaseMs)
	}
	if !strings.Contains(buf.String(), "cold_start_phase") {
		t.Errorf("expected cold_start_phase log line, got %q", buf.String())
	}
	if !strings.Contains(buf.String(), "cold_start_summary") {
		t.Errorf("expected cold_start_summary log line, got %q", buf.String())
	}
}

func TestNilTraceIsSafe(t *testing.T) {
	var tr *Trace
	if got := tr.ID(); got != "" {
		t.Errorf("nil ID want empty, got %q", got)
	}
	tr.Phase("x")   // must not panic
	tr.Summary("y") // must not panic
}

func TestSummaryIdempotent(t *testing.T) {
	tr := New(nil, "s", "w")
	tr.Phase("a")
	before := len(RecentSummaries(0))
	tr.Summary("ok")
	tr.Summary("ok") // second call should be a no-op
	after := len(RecentSummaries(0))
	if after-before != 1 {
		t.Errorf("expected exactly one summary appended, got delta %d", after-before)
	}
}

func TestRingBufferCapAndOrder(t *testing.T) {
	// Fill well beyond capacity.
	for i := 0; i < ringCapacity+10; i++ {
		tr := New(nil, "s", "w")
		tr.Phase("begin")
		tr.Summary("ok")
	}
	all := RecentSummaries(0)
	if len(all) != ringCapacity {
		t.Errorf("expected len %d, got %d", ringCapacity, len(all))
	}
	// Newest-first: At timestamps should be non-increasing.
	for i := 1; i < len(all); i++ {
		if all[i].At.After(all[i-1].At) {
			t.Errorf("summaries not newest-first at %d: %v after %v", i, all[i].At, all[i-1].At)
		}
	}
	// k limit honored.
	small := RecentSummaries(3)
	if len(small) != 3 {
		t.Errorf("expected 3, got %d", len(small))
	}
}

func TestContentionDefaults(t *testing.T) {
	// Ensure clean provider state before assertions.
	SetPromptingCounter(nil)
	SetLiveACPCounter(nil)
	SetConnectedWSCounter(nil)
	SetOpenMCPStreamCounter(nil)

	c := Contention()
	if c.NumGoroutine <= 0 {
		t.Errorf("NumGoroutine should be > 0, got %d", c.NumGoroutine)
	}
	if c.NumCPU <= 0 {
		t.Errorf("NumCPU should be > 0, got %d", c.NumCPU)
	}
	if c.ConcurrentPrompting != -1 {
		t.Errorf("expected ConcurrentPrompting=-1 without provider, got %d", c.ConcurrentPrompting)
	}
	if c.LiveACPProcesses != -1 {
		t.Errorf("expected LiveACPProcesses=-1 without provider, got %d", c.LiveACPProcesses)
	}
	if c.ConnectedWSClients != -1 {
		t.Errorf("expected ConnectedWSClients=-1 without provider, got %d", c.ConnectedWSClients)
	}
	if c.OpenMCPSSEStreams != -1 {
		t.Errorf("expected OpenMCPSSEStreams=-1 without provider, got %d", c.OpenMCPSSEStreams)
	}

	SetPromptingCounter(func() int { return 7 })
	SetLiveACPCounter(func() int { return 3 })
	SetConnectedWSCounter(func() int { return 5 })
	SetOpenMCPStreamCounter(func() int { return 2 })
	t.Cleanup(func() {
		SetPromptingCounter(nil)
		SetLiveACPCounter(nil)
		SetConnectedWSCounter(nil)
		SetOpenMCPStreamCounter(nil)
	})
	c2 := Contention()
	if c2.ConcurrentPrompting != 7 {
		t.Errorf("expected ConcurrentPrompting=7, got %d", c2.ConcurrentPrompting)
	}
	if c2.LiveACPProcesses != 3 {
		t.Errorf("expected LiveACPProcesses=3, got %d", c2.LiveACPProcesses)
	}
	if c2.ConnectedWSClients != 5 {
		t.Errorf("expected ConnectedWSClients=5, got %d", c2.ConnectedWSClients)
	}
	if c2.OpenMCPSSEStreams != 2 {
		t.Errorf("expected OpenMCPSSEStreams=2, got %d", c2.OpenMCPSSEStreams)
	}
}

func TestContentionLogAttrsOmissions(t *testing.T) {
	SetPromptingCounter(nil)
	SetLiveACPCounter(nil)
	SetConnectedWSCounter(nil)
	SetOpenMCPStreamCounter(nil)
	c := Contention()
	attrs := c.LogAttrs()
	// Convert to a set of keys for lookup.
	keys := map[string]bool{}
	for i := 0; i < len(attrs); i += 2 {
		if k, ok := attrs[i].(string); ok {
			keys[k] = true
		}
	}
	if !keys["num_goroutine"] || !keys["num_cpu"] {
		t.Errorf("num_goroutine/num_cpu should always be present, got %v", keys)
	}
	if keys["concurrent_prompting"] || keys["live_acp_processes"] {
		t.Errorf("expected prompting/acp omitted when -1, got %v", keys)
	}
	if keys["connected_ws_clients"] || keys["open_mcp_sse_streams"] {
		t.Errorf("expected ws/sse omitted when -1, got %v", keys)
	}

	SetConnectedWSCounter(func() int { return 4 })
	SetOpenMCPStreamCounter(func() int { return 1 })
	t.Cleanup(func() {
		SetConnectedWSCounter(nil)
		SetOpenMCPStreamCounter(nil)
	})
	attrs2 := c2LogAttrs(t)
	if !attrs2["connected_ws_clients"] || !attrs2["open_mcp_sse_streams"] {
		t.Errorf("expected ws/sse present once providers registered, got %v", attrs2)
	}
}

// c2LogAttrs is a small helper that samples Contention() and returns its
// LogAttrs() as a key-presence set, used by TestContentionLogAttrsOmissions.
func c2LogAttrs(t *testing.T) map[string]bool {
	t.Helper()
	attrs := Contention().LogAttrs()
	keys := map[string]bool{}
	for i := 0; i < len(attrs); i += 2 {
		if k, ok := attrs[i].(string); ok {
			keys[k] = true
		}
	}
	return keys
}

func TestContentionLoadPlausible(t *testing.T) {
	c := Contention()
	if math.IsNaN(c.Load1) || math.IsInf(c.Load1, 0) {
		t.Errorf("Load1 must be finite, got %v", c.Load1)
	}
	if c.Load1 < 0 {
		t.Errorf("Load1 must be non-negative, got %v", c.Load1)
	}
	if runtime.GOOS == "darwin" {
		if !c.LoadAvailable {
			t.Errorf("expected LoadAvailable=true on darwin")
		}
	}
}

func TestContextRoundTrip(t *testing.T) {
	tr := New(nil, "s", "w")
	ctx := WithTrace(context.Background(), tr)
	if got := FromContext(ctx); got != tr {
		t.Errorf("FromContext returned %v, want %v", got, tr)
	}
	if got := FromContext(context.Background()); got != nil {
		t.Errorf("bare context should return nil, got %v", got)
	}
	//nolint:staticcheck // intentional nil ctx to exercise guard
	if got := FromContext(nil); got != nil {
		t.Errorf("nil ctx should return nil, got %v", got)
	}
}

func TestNewGeneratesID(t *testing.T) {
	tr := New(nil, "s", "w")
	if tr.ID() == "" {
		t.Errorf("expected non-empty id")
	}
}
