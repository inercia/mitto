package mcpserver

import (
	"bytes"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeEpisodeClock is a manually-advanceable clock for deterministic aging
// checks, safe for concurrent use by the tracker's tests.
type fakeEpisodeClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *fakeEpisodeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeEpisodeClock) Advance(d time.Duration) {
	c.mu.Lock()
	c.now = c.now.Add(d)
	c.mu.Unlock()
}

func newTestEpisodeTracker(buf *bytes.Buffer, clock *fakeEpisodeClock, statsFn mcpInitStatsFn) *mcpInitEpisodeTracker {
	logger := slog.New(slog.NewTextHandler(buf, nil))
	return newMCPInitEpisodeTracker(logger, statsFn, clock.Now)
}

func TestMCPInitEpisodeTracker_FastEpisode_LogsCompletedNoWarn(t *testing.T) {
	buf := &bytes.Buffer{}
	clock := &fakeEpisodeClock{now: time.Now()}
	tr := newTestEpisodeTracker(buf, clock, nil)
	base := clock.Now()

	tr.RecordPhase("sess-fast", mcpInitPhaseInitializeReceived, base)
	tr.RecordPhase("sess-fast", mcpInitPhaseInitializeResponded, base.Add(10*time.Millisecond))
	tr.RecordPhase("sess-fast", mcpInitPhaseInitializedNotification, base.Add(20*time.Millisecond))
	tr.RecordPhase("sess-fast", mcpInitPhaseFirstGet, base.Add(30*time.Millisecond))
	tr.RecordPhase("sess-fast", mcpInitPhaseToolsListReceived, base.Add(40*time.Millisecond))
	tr.RecordPhase("sess-fast", mcpInitPhaseToolsListResponded, base.Add(50*time.Millisecond))

	out := buf.String()
	if !strings.Contains(out, `msg="MCP init episode completed"`) {
		t.Fatalf("expected completed log line, got:\n%s", out)
	}
	if strings.Contains(out, "stalled") {
		t.Fatalf("fast episode must not log a stall, got:\n%s", out)
	}
	tr.mu.Lock()
	_, stillTracked := tr.episodes["sess-fast"]
	tr.mu.Unlock()
	if stillTracked {
		t.Fatal("completed episode must be evicted from the tracker")
	}
}

func TestMCPInitEpisodeTracker_SlowPhase_LogsStalled(t *testing.T) {
	buf := &bytes.Buffer{}
	clock := &fakeEpisodeClock{now: time.Now()}
	tr := newTestEpisodeTracker(buf, clock, nil)
	base := clock.Now()

	tr.RecordPhase("sess-slow", mcpInitPhaseInitializeReceived, base)
	// initialize_responded arrives 6s after initialize_received: exceeds the
	// 5s per-phase slow threshold.
	tr.RecordPhase("sess-slow", mcpInitPhaseInitializeResponded, base.Add(6*time.Second))

	out := buf.String()
	if !strings.Contains(out, `msg="MCP init episode stalled"`) {
		t.Fatalf("expected stalled log line, got:\n%s", out)
	}
	if !strings.Contains(out, "laggard_phase=initialize_responded") {
		t.Fatalf("expected laggard_phase=initialize_responded, got:\n%s", out)
	}

	// A second slow phase on the same episode must not emit a second WARN.
	buf.Reset()
	tr.RecordPhase("sess-slow", mcpInitPhaseInitializedNotification, base.Add(20*time.Second))
	if strings.Contains(buf.String(), "stalled") {
		t.Fatalf("expected at most one stalled WARN per episode, got second:\n%s", buf.String())
	}
}

func TestMCPInitEpisodeTracker_AgingWithoutCompletion_LogsStalled(t *testing.T) {
	buf := &bytes.Buffer{}
	clock := &fakeEpisodeClock{now: time.Now()}
	tr := newTestEpisodeTracker(buf, clock, nil)
	base := clock.Now()

	tr.RecordPhase("sess-aging", mcpInitPhaseInitializeReceived, base)
	if strings.Contains(buf.String(), "stalled") {
		t.Fatalf("must not stall immediately, got:\n%s", buf.String())
	}

	// Advance wall clock past mcpInitEpisodeMaxAge without the episode
	// completing; the next phase update (even a fast one) must flag aging.
	clock.Advance(mcpInitEpisodeMaxAge + time.Second)
	tr.RecordPhase("sess-aging", mcpInitPhaseFirstGet, clock.Now())

	out := buf.String()
	if !strings.Contains(out, `msg="MCP init episode stalled"`) {
		t.Fatalf("expected aging to trigger a stalled log line, got:\n%s", out)
	}
	if !strings.Contains(out, "aging=true") {
		t.Fatalf("expected aging=true, got:\n%s", out)
	}
}

// TestMCPInitEpisodeTracker_ConcurrentEpisodes_NoCrossTalk runs N independent
// fast episodes concurrently (run with -race to also pin down the tracker's
// synchronization safety) and asserts each completes cleanly with no
// cross-episode contamination.
func TestMCPInitEpisodeTracker_ConcurrentEpisodes_NoCrossTalk(t *testing.T) {
	buf := &bytes.Buffer{}
	var mu sync.Mutex
	syncBuf := &syncWriter{buf: buf, mu: &mu}
	clock := &fakeEpisodeClock{now: time.Now()}
	logger := slog.New(slog.NewTextHandler(syncBuf, nil))
	tr := newMCPInitEpisodeTracker(logger, nil, clock.Now)

	const n = 4
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			sid := fmt.Sprintf("sess-concurrent-%d", i)
			base := clock.Now()
			tr.RecordPhase(sid, mcpInitPhaseInitializeReceived, base)
			tr.RecordPhase(sid, mcpInitPhaseInitializeResponded, base.Add(time.Millisecond))
			tr.RecordPhase(sid, mcpInitPhaseInitializedNotification, base.Add(2*time.Millisecond))
			tr.RecordPhase(sid, mcpInitPhaseFirstGet, base.Add(3*time.Millisecond))
			tr.RecordPhase(sid, mcpInitPhaseToolsListReceived, base.Add(4*time.Millisecond))
			tr.RecordPhase(sid, mcpInitPhaseToolsListResponded, base.Add(5*time.Millisecond))
		}(i)
	}
	wg.Wait()

	mu.Lock()
	out := buf.String()
	mu.Unlock()
	if strings.Contains(out, "stalled") {
		t.Fatalf("no fast concurrent episode should stall, got:\n%s", out)
	}
	if got := strings.Count(out, `msg="MCP init episode completed"`); got != n {
		t.Fatalf("expected %d completed log lines, got %d:\n%s", n, got, out)
	}
	tr.mu.Lock()
	remaining := len(tr.episodes)
	tr.mu.Unlock()
	if remaining != 0 {
		t.Fatalf("expected all completed episodes evicted, %d remain", remaining)
	}
}

// syncWriter serializes concurrent writes to an underlying *bytes.Buffer,
// since slog.Handler implementations do not guarantee thread-safe io.Writer
// usage on their own for a shared, non-locking writer.
type syncWriter struct {
	buf *bytes.Buffer
	mu  *sync.Mutex
}

func (w *syncWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.buf.Write(p)
}

func TestMCPInitEpisodeTracker_TTLEviction(t *testing.T) {
	buf := &bytes.Buffer{}
	clock := &fakeEpisodeClock{now: time.Now()}
	tr := newTestEpisodeTracker(buf, clock, nil)

	tr.RecordPhase("sess-stale", mcpInitPhaseInitializeReceived, clock.Now())
	clock.Advance(mcpInitEpisodeTTL + time.Second)
	// Any RecordPhase call sweeps expired entries first.
	tr.RecordPhase("sess-other", mcpInitPhaseInitializeReceived, clock.Now())

	tr.mu.Lock()
	_, staleStillTracked := tr.episodes["sess-stale"]
	_, otherTracked := tr.episodes["sess-other"]
	tr.mu.Unlock()
	if staleStillTracked {
		t.Fatal("expected TTL-expired episode to be evicted")
	}
	if !otherTracked {
		t.Fatal("expected the fresh episode to remain tracked")
	}
}

func TestMCPInitEpisodeTracker_MaxEntriesEviction(t *testing.T) {
	buf := &bytes.Buffer{}
	clock := &fakeEpisodeClock{now: time.Now()}
	tr := newTestEpisodeTracker(buf, clock, nil)

	total := mcpInitEpisodeMaxEntries + 10
	for i := 0; i < total; i++ {
		tr.RecordPhase(fmt.Sprintf("sess-%d", i), mcpInitPhaseInitializeReceived, clock.Now())
	}

	tr.mu.Lock()
	count := len(tr.episodes)
	_, oldestTracked := tr.episodes["sess-0"]
	_, newestTracked := tr.episodes[fmt.Sprintf("sess-%d", total-1)]
	tr.mu.Unlock()
	if count != mcpInitEpisodeMaxEntries {
		t.Fatalf("expected tracker bounded at %d entries, got %d", mcpInitEpisodeMaxEntries, count)
	}
	if oldestTracked {
		t.Fatal("expected the oldest-inserted episode to have been evicted")
	}
	if !newestTracked {
		t.Fatal("expected the most-recently-inserted episode to still be tracked")
	}
}

func TestMCPInitEpisodeTracker_StatsFnPlumbedIntoStalledLog(t *testing.T) {
	buf := &bytes.Buffer{}
	clock := &fakeEpisodeClock{now: time.Now()}
	statsFn := func() (int64, int, bool) { return 3, 7, true }
	tr := newTestEpisodeTracker(buf, clock, statsFn)
	base := clock.Now()

	tr.RecordPhase("sess-stats", mcpInitPhaseInitializeReceived, base)
	tr.RecordPhase("sess-stats", mcpInitPhaseInitializeResponded, base.Add(6*time.Second))

	out := buf.String()
	for _, want := range []string{"open_sse_streams=3", "mcp_session_leases=7", "reaper_recent_scan=true"} {
		if !strings.Contains(out, want) {
			t.Fatalf("expected stalled log to contain %q, got:\n%s", want, out)
		}
	}
}
