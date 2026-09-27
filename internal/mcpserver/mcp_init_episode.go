package mcpserver

import (
	"log/slog"
	"sync"
	"time"
)

// mcpInitPhase identifies one step of the MCP Streamable-HTTP init handshake
// sequence an agent's MCP client executes against Mitto's /mcp endpoint
// before declaring "MCP initialization timed out" (mitto-e9b instrumentation
// pass 1): initialize -> notifications/initialized -> first SSE GET ->
// tools/list.
type mcpInitPhase string

const (
	mcpInitPhaseInitializeReceived      mcpInitPhase = "initialize_received"
	mcpInitPhaseInitializeResponded     mcpInitPhase = "initialize_responded"
	mcpInitPhaseInitializedNotification mcpInitPhase = "initialized_notification"
	mcpInitPhaseFirstGet                mcpInitPhase = "first_get"
	mcpInitPhaseToolsListReceived       mcpInitPhase = "tools_list_received"
	mcpInitPhaseToolsListResponded      mcpInitPhase = "tools_list_responded"
)

// mcpInitPhaseOrder is the expected chronological order of the handshake
// phases, used to attribute latency to whichever phase took the longest
// (the "laggard").
var mcpInitPhaseOrder = []mcpInitPhase{
	mcpInitPhaseInitializeReceived,
	mcpInitPhaseInitializeResponded,
	mcpInitPhaseInitializedNotification,
	mcpInitPhaseFirstGet,
	mcpInitPhaseToolsListReceived,
	mcpInitPhaseToolsListResponded,
}

const (
	// mcpInitPhaseSlowThreshold is the per-phase latency (elapsed time since
	// the previous recorded phase) above which an episode is flagged
	// stalled, even if it later completes.
	mcpInitPhaseSlowThreshold = 5 * time.Second
	// mcpInitEpisodeMaxAge is how long an episode may remain incomplete
	// before it is flagged stalled purely on elapsed wall-clock time,
	// independent of any single phase's latency.
	mcpInitEpisodeMaxAge = 30 * time.Second
	// mcpInitEpisodeMaxEntries bounds the tracker so a permanently-stalled
	// episode (agent crashed mid-handshake, never sent tools/list) cannot
	// leak memory across a long-running Mitto process.
	mcpInitEpisodeMaxEntries = 256
	// mcpInitEpisodeTTL is the absolute max age after which an entry is
	// evicted regardless of completion state.
	mcpInitEpisodeTTL = 10 * time.Minute
)

// mcpInitStatsFn returns a point-in-time snapshot of server state useful for
// diagnosing a stalled episode: the number of currently-open SSE keepalive
// GET streams, the number of tracked MCP session leases, and whether the
// idle-session reaper scanned within the last 2s (Plan hypothesis H2 —
// reaper synthetic DELETE contention as a stall cause).
type mcpInitStatsFn func() (openStreams int64, leaseCount int, reaperRecentScan bool)

// mcpInitEpisode tracks the phase timestamps of one MCP init handshake,
// keyed by its Streamable-HTTP protocol session id.
type mcpInitEpisode struct {
	sessionID string
	// insertedAt is the timestamp of this episode's first recorded phase;
	// used for the aging check and TTL eviction.
	insertedAt time.Time
	phases     map[mcpInitPhase]time.Time
	// stalledLogged prevents emitting more than one "stalled" WARN per
	// episode.
	stalledLogged bool
	// completed is true once tools_list_responded has been recorded.
	completed bool
}

// mcpInitEpisodeTracker correlates the MCP init handshake's phases into
// per-protocol-session episodes so a single "MCP init episode
// completed"/"stalled" log line carries the full phase breakdown, instead of
// the sequence being scattered across 4+ isolated per-request log lines with
// no join key back to the agent's failed handshake (mitto-e9b instrumentation
// pass 1). Safe for concurrent use.
type mcpInitEpisodeTracker struct {
	logger  *slog.Logger
	statsFn mcpInitStatsFn
	now     func() time.Time

	mu       sync.Mutex
	episodes map[string]*mcpInitEpisode
	// order tracks insertion order for bounded-size eviction: handshakes are
	// short-lived and monotonic, so oldest-by-insertion is an adequate
	// approximation of least-relevant when the tracker overflows.
	order []string
}

// newMCPInitEpisodeTracker creates a tracker. now defaults to time.Now when
// nil (production); tests inject a fake clock for deterministic aging
// checks.
func newMCPInitEpisodeTracker(logger *slog.Logger, statsFn mcpInitStatsFn, now func() time.Time) *mcpInitEpisodeTracker {
	if now == nil {
		now = time.Now
	}
	return &mcpInitEpisodeTracker{
		logger:   logger,
		statsFn:  statsFn,
		now:      now,
		episodes: make(map[string]*mcpInitEpisode),
	}
}

// RecordPhase records that `phase` occurred at `at` for the init episode
// keyed by sessionID (the MCP Streamable-HTTP protocol session id), creating
// the episode on first use. Idempotent per phase: a phase already recorded
// for this episode is left untouched (guards against a retried/duplicate
// request skewing latencies). Evaluates the new phase's latency against the
// nearest preceding recorded phase and the episode's overall age; on
// completion (tools_list_responded) logs a single correlated summary line —
// INFO for a clean handshake, still INFO (but annotated) if the episode was
// flagged stalled earlier in its life. A no-op if sessionID is empty.
func (t *mcpInitEpisodeTracker) RecordPhase(sessionID string, phase mcpInitPhase, at time.Time) {
	if sessionID == "" {
		return
	}

	t.mu.Lock()
	t.evictExpiredLocked()

	ep, ok := t.episodes[sessionID]
	if !ok {
		ep = &mcpInitEpisode{sessionID: sessionID, insertedAt: at, phases: make(map[mcpInitPhase]time.Time)}
		t.episodes[sessionID] = ep
		t.order = append(t.order, sessionID)
		t.evictOverflowLocked()
	}
	if ep.completed {
		// Late/duplicate signal after the episode already logged its
		// completion (e.g. a retried tools/list) — nothing more to do.
		t.mu.Unlock()
		return
	}
	if _, already := ep.phases[phase]; !already {
		ep.phases[phase] = at
	}

	prevTS, prevOK := t.previousPhaseTimestampLocked(ep, phase)
	var slowPhase mcpInitPhase
	if prevOK && at.Sub(prevTS) > mcpInitPhaseSlowThreshold {
		slowPhase = phase
	}
	nowTS := t.now()
	ageMs := nowTS.Sub(ep.insertedAt).Milliseconds()
	aging := nowTS.Sub(ep.insertedAt) > mcpInitEpisodeMaxAge
	completedNow := phase == mcpInitPhaseToolsListResponded

	emitStall := (slowPhase != "" || aging) && !ep.stalledLogged && !completedNow
	if emitStall {
		ep.stalledLogged = true
	}
	wasStalled := ep.stalledLogged

	breakdown, total := t.breakdownLocked(ep)

	var open int64
	var leases int
	var recentScan bool
	if t.statsFn != nil {
		open, leases, recentScan = t.statsFn()
	}

	if completedNow {
		ep.completed = true
		delete(t.episodes, sessionID)
		t.removeFromOrderLocked(sessionID)
	}
	t.mu.Unlock()

	if emitStall && t.logger != nil {
		attrs := []any{
			"mcp_session_id", sessionID,
			"phase", string(phase),
			"laggard_phase", string(slowPhase),
			"aging", aging,
			"episode_age_ms", ageMs,
			"open_sse_streams", open,
			"mcp_session_leases", leases,
			"reaper_recent_scan", recentScan,
		}
		attrs = append(attrs, breakdown...)
		t.logger.Warn("MCP init episode stalled", attrs...)
	}
	if completedNow && t.logger != nil {
		msg := "MCP init episode completed"
		if wasStalled {
			msg = "MCP init episode completed (previously stalled)"
		}
		attrs := []any{
			"mcp_session_id", sessionID,
			"total_ms", total.Milliseconds(),
		}
		attrs = append(attrs, breakdown...)
		t.logger.Info(msg, attrs...)
	}
}

// previousPhaseTimestampLocked returns the timestamp of the nearest recorded
// phase preceding `phase` in mcpInitPhaseOrder. The caller must hold t.mu.
func (t *mcpInitEpisodeTracker) previousPhaseTimestampLocked(ep *mcpInitEpisode, phase mcpInitPhase) (time.Time, bool) {
	idx := -1
	for i, p := range mcpInitPhaseOrder {
		if p == phase {
			idx = i
			break
		}
	}
	if idx <= 0 {
		return time.Time{}, false
	}
	for i := idx - 1; i >= 0; i-- {
		if ts, ok := ep.phases[mcpInitPhaseOrder[i]]; ok {
			return ts, true
		}
	}
	return time.Time{}, false
}

// breakdownLocked returns per-phase latency attrs (key/value pairs suitable
// for slog's variadic args) and the total elapsed time from the first to the
// last recorded phase. The caller must hold t.mu.
func (t *mcpInitEpisodeTracker) breakdownLocked(ep *mcpInitEpisode) (attrs []any, total time.Duration) {
	var first, prev time.Time
	haveFirst, havePrev := false, false
	for _, p := range mcpInitPhaseOrder {
		ts, ok := ep.phases[p]
		if !ok {
			continue
		}
		if !haveFirst {
			first, haveFirst = ts, true
		}
		if havePrev {
			attrs = append(attrs, string(p)+"_latency_ms", ts.Sub(prev).Milliseconds())
		}
		prev, havePrev = ts, true
	}
	if haveFirst && havePrev {
		total = prev.Sub(first)
	}
	return attrs, total
}

// evictExpiredLocked drops episodes older than mcpInitEpisodeTTL. The caller
// must hold t.mu.
func (t *mcpInitEpisodeTracker) evictExpiredLocked() {
	if len(t.order) == 0 {
		return
	}
	now := t.now()
	kept := make([]string, 0, len(t.order))
	for _, sid := range t.order {
		ep, ok := t.episodes[sid]
		if !ok {
			continue // already removed (e.g. completion)
		}
		if now.Sub(ep.insertedAt) > mcpInitEpisodeTTL {
			delete(t.episodes, sid)
			continue
		}
		kept = append(kept, sid)
	}
	t.order = kept
}

// evictOverflowLocked drops the oldest-inserted episodes once the tracker
// exceeds mcpInitEpisodeMaxEntries. The caller must hold t.mu.
func (t *mcpInitEpisodeTracker) evictOverflowLocked() {
	for len(t.order) > mcpInitEpisodeMaxEntries {
		oldest := t.order[0]
		t.order = t.order[1:]
		delete(t.episodes, oldest)
	}
}

// removeFromOrderLocked removes sessionID from t.order. The caller must hold
// t.mu.
func (t *mcpInitEpisodeTracker) removeFromOrderLocked(sessionID string) {
	for i, sid := range t.order {
		if sid == sessionID {
			t.order = append(t.order[:i], t.order[i+1:]...)
			return
		}
	}
}
