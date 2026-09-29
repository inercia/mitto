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

// TestMCPInitEpisodeTracker_KeepaliveGetAfterCompletion_MustNotFalselyStall
// reproduces mitto-1ngs: SSE keepalive GET requests recycle roughly every 5
// minutes for the lifetime of an MCP session, long after its init handshake
// has already completed. RecordPhase's lazy episode-creation on any unknown
// sessionID means a keepalive-recycle GET (mcpInitPhaseFirstGet) arriving
// after completion re-creates a "phantom" episode for that already-completed
// session. Because no further tools/list is ever issued on that session, the
// phantom episode ages past mcpInitEpisodeMaxAge and the tracker emits a
// spurious "MCP init episode stalled" WARN for a session whose handshake in
// fact completed cleanly minutes earlier — a false positive, not a real
// stall. This test asserts the CORRECT (bug-free) behavior — no stall log
// for a session that already completed its handshake — which the current
// implementation violates.
func TestMCPInitEpisodeTracker_KeepaliveGetAfterCompletion_MustNotFalselyStall(t *testing.T) {
	buf := &bytes.Buffer{}
	clock := &fakeEpisodeClock{now: time.Now()}
	tr := newTestEpisodeTracker(buf, clock, nil)
	base := clock.Now()

	const sid = "sess-keepalive-recycle"

	// Full clean handshake: episode completes and is deleted from the
	// tracker (mirrors mcpInitPhaseToolsListResponded handling).
	tr.RecordPhase(sid, mcpInitPhaseInitializeReceived, base)
	tr.RecordPhase(sid, mcpInitPhaseInitializeResponded, base.Add(10*time.Millisecond))
	tr.RecordPhase(sid, mcpInitPhaseInitializedNotification, base.Add(20*time.Millisecond))
	tr.RecordPhase(sid, mcpInitPhaseFirstGet, base.Add(30*time.Millisecond))
	tr.RecordPhase(sid, mcpInitPhaseToolsListReceived, base.Add(40*time.Millisecond))
	tr.RecordPhase(sid, mcpInitPhaseToolsListResponded, base.Add(50*time.Millisecond))

	buf.Reset()
	if !strings.Contains(func() string {
		tr.mu.Lock()
		defer tr.mu.Unlock()
		_, tracked := tr.episodes[sid]
		if tracked {
			return "still-tracked"
		}
		return "evicted"
	}(), "evicted") {
		t.Fatal("expected completed episode to be evicted from the tracker")
	}

	// Simulate the SSE keepalive GET stream recycling ~5 minutes later
	// (server.go records mcpInitPhaseFirstGet on every GET carrying an
	// Mcp-Session-Id, not only the handshake's first GET). This re-creates a
	// phantom episode for the already-completed session.
	//
	// The fake clock (t.now()) must be advanced in lockstep with the `at`
	// timestamp passed to RecordPhase: in production both derive from the
	// same time.Now() call, so they can never diverge. Advancing only one
	// of them (as an earlier version of this test mistakenly did, computing
	// `firstRecycle`/`secondRecycle` via base.Add(...) without moving the
	// clock) desyncs `nowTS` from `ep.insertedAt` by minutes, permanently
	// yielding a negative (never-aging) duration and masking the bug.
	clock.Advance(5 * time.Minute)
	firstRecycle := clock.Now()
	tr.RecordPhase(sid, mcpInitPhaseFirstGet, firstRecycle)

	// Advance the clock past mcpInitEpisodeMaxAge and simulate the next
	// keepalive recycle. The phantom episode is now "aging" purely because
	// no tools/list will ever be reissued on an already-initialized session.
	clock.Advance(mcpInitEpisodeMaxAge + time.Second)
	secondRecycle := clock.Now()
	tr.RecordPhase(sid, mcpInitPhaseFirstGet, secondRecycle)

	out := buf.String()
	if strings.Contains(out, `msg="MCP init episode stalled"`) {
		t.Fatalf("mitto-1ngs: keepalive GET recycle on an already-completed "+
			"session must not emit a false-positive stall WARN, got:\n%s", out)
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
