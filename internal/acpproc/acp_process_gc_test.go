package acpproc

import (
	"context"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/inercia/mitto/internal/conversation"
)

// newTestLogger returns a logger that discards all output, suitable for tests.
func newTestLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// newTestGCManager builds a minimal ACPProcessManager wired with the given
// query/close funcs. All GC fields are set directly (no StartGC) so callers
// can invoke RunGCOnce without launching the background goroutine.
func newTestGCManager(
	query SessionQueryFunc,
	closeSession SessionCloseFunc,
) *ACPProcessManager {
	return &ACPProcessManager{
		logger:          newTestLogger(),
		processes:       make(map[string]*SharedACPProcess),
		lastSessionSeen: make(map[string]time.Time),
		auxSessions:     make(map[auxSessionKey]*auxiliarySessionState),
		pinState:        make(map[string]*pinInfo),
		gcConfig: GCConfig{
			Interval:             30 * time.Second,
			GracePeriod:          60 * time.Second,
			ObserverGracePeriod:  60 * time.Second,
			IdleTimeout:          5 * time.Minute,
			AuxIdleTimeout:       10 * time.Minute,
			LoopSuspendThreshold: 30 * time.Minute,
		},
		sessionQuery: query,
		sessionClose: closeSession,
	}
}

// newTestSharedProcess creates a minimal SharedACPProcess whose Close() method
// does not panic. It has no real subprocess — only the context cancel is set.
func newTestSharedProcess() *SharedACPProcess {
	processCtx, processCancel := context.WithCancel(context.Background())
	return &SharedACPProcess{
		ctx:       processCtx,
		ctxCancel: processCancel,
	}
}

// TestGCTier1_ClosesIdleSessions verifies that sessions with no active state
// (not prompting, no observers, empty queue, no loop) are closed by Tier 1.
func TestGCTier1_ClosesIdleSessions(t *testing.T) {
	sessions := map[string][]conversation.SessionInfo{
		"ws-1": {
			{SessionID: "sess-a", WorkspaceUUID: "ws-1"},
			{SessionID: "sess-b", WorkspaceUUID: "ws-1"},
		},
	}

	var mu sync.Mutex
	closed := make(map[string]bool)

	m := newTestGCManager(
		func() map[string][]conversation.SessionInfo { return sessions },
		func(id string) {
			mu.Lock()
			defer mu.Unlock()
			closed[id] = true
		},
	)

	m.RunGCOnce()

	mu.Lock()
	defer mu.Unlock()
	for _, id := range []string{"sess-a", "sess-b"} {
		if !closed[id] {
			t.Errorf("expected %s to be closed by Tier 1 GC", id)
		}
	}
}

// TestGCTier1_SkipsActiveSessions verifies that sessions with any active state
// are never closed by Tier 1.
func TestGCTier1_SkipsActiveSessions(t *testing.T) {
	// NextLoopAt within 2×interval (60s) — should be skipped.
	soon := time.Now().Add(10 * time.Second)

	sessions := map[string][]conversation.SessionInfo{
		"ws-1": {
			{SessionID: "prompting", WorkspaceUUID: "ws-1", IsPrompting: true},
			{SessionID: "observers", WorkspaceUUID: "ws-1", HasObservers: true},
			{SessionID: "queue", WorkspaceUUID: "ws-1", QueueLength: 5},
			{SessionID: "loop", WorkspaceUUID: "ws-1", NextLoopAt: &soon},
			{SessionID: "connected-clients", WorkspaceUUID: "ws-1", HasConnectedClients: true},
		},
	}

	var mu sync.Mutex
	closed := make(map[string]bool)

	m := newTestGCManager(
		func() map[string][]conversation.SessionInfo { return sessions },
		func(id string) {
			mu.Lock()
			defer mu.Unlock()
			closed[id] = true
		},
	)

	m.RunGCOnce()

	mu.Lock()
	defer mu.Unlock()
	if len(closed) > 0 {
		t.Errorf("no active sessions should be closed; got %v", closed)
	}
}

// TestGCTier1_ClosesSessionWithDistantLoop verifies that a session whose
// next loop prompt is far in the future (beyond 2×interval) is still
// considered idle and is closed by Tier 1.
func TestGCTier1_ClosesSessionWithDistantLoop(t *testing.T) {
	far := time.Now().Add(2 * time.Hour) // well beyond 2×30s = 60s threshold

	sessions := map[string][]conversation.SessionInfo{
		"ws-1": {
			{SessionID: "distant-loop", WorkspaceUUID: "ws-1", NextLoopAt: &far},
		},
	}

	var mu sync.Mutex
	closed := make(map[string]bool)

	m := newTestGCManager(
		func() map[string][]conversation.SessionInfo { return sessions },
		func(id string) {
			mu.Lock()
			defer mu.Unlock()
			closed[id] = true
		},
	)

	m.RunGCOnce()

	mu.Lock()
	defer mu.Unlock()
	if !closed["distant-loop"] {
		t.Error("session with distant loop should be closed by Tier 1")
	}
}

// TestGCTier1_SkipsPinnedSession verifies that a session marked Pinned=true is
// exempt from Tier 1 GC as long as PinExpiry is nil or still in the future.
func TestGCTier1_SkipsPinnedSession(t *testing.T) {
	future := time.Now().Add(10 * time.Minute)

	sessions := map[string][]conversation.SessionInfo{
		"ws-1": {
			{SessionID: "pinned-no-expiry", WorkspaceUUID: "ws-1", Pinned: true, PinReason: "slow session/new"},
			{SessionID: "pinned-future-expiry", WorkspaceUUID: "ws-1", Pinned: true, PinReason: "mcp-init timeout", PinExpiry: &future},
		},
	}

	var mu sync.Mutex
	closed := make(map[string]bool)

	m := newTestGCManager(
		func() map[string][]conversation.SessionInfo { return sessions },
		func(id string) {
			mu.Lock()
			defer mu.Unlock()
			closed[id] = true
		},
	)

	m.RunGCOnce()

	mu.Lock()
	defer mu.Unlock()
	if len(closed) > 0 {
		t.Errorf("pinned sessions should not be closed by Tier 1; got %v", closed)
	}
}

// TestGCTier1_ClosesPinnedSessionWithExpiredExpiry verifies that a session with
// Pinned=true but PinExpiry in the past is eligible for GC — the pin is
// treated as expired so the max-pin-duration cap can release a stuck pin.
func TestGCTier1_ClosesPinnedSessionWithExpiredExpiry(t *testing.T) {
	past := time.Now().Add(-1 * time.Minute)

	sessions := map[string][]conversation.SessionInfo{
		"ws-1": {
			{SessionID: "pinned-expired", WorkspaceUUID: "ws-1", Pinned: true, PinReason: "slow session/new", PinExpiry: &past},
		},
	}

	var mu sync.Mutex
	closed := make(map[string]bool)

	m := newTestGCManager(
		func() map[string][]conversation.SessionInfo { return sessions },
		func(id string) {
			mu.Lock()
			defer mu.Unlock()
			closed[id] = true
		},
	)

	m.RunGCOnce()

	mu.Lock()
	defer mu.Unlock()
	if !closed["pinned-expired"] {
		t.Error("pinned session with expired PinExpiry should fall through and be closed by Tier 1")
	}
}

// TestGCTier2_GracePeriod verifies the two-step grace period logic:
//   - First RunGCOnce records the "sessionless" timestamp and keeps the process.
//   - After the grace period elapses the process is stopped on the next cycle.
func TestGCTier2_GracePeriod(t *testing.T) {
	workspaceUUID := "ws-grace"

	proc := newTestSharedProcess()

	m := newTestGCManager(
		func() map[string][]conversation.SessionInfo { return map[string][]conversation.SessionInfo{} }, // no sessions
		func(id string) {}, // no-op close
	)
	m.mu.Lock()
	m.processes[workspaceUUID] = proc
	m.mu.Unlock()

	// First cycle: grace period starts — process must NOT be stopped.
	m.RunGCOnce()

	m.mu.RLock()
	_, exists := m.processes[workspaceUUID]
	m.mu.RUnlock()
	if !exists {
		t.Fatal("process was removed on first RunGCOnce; expected it to survive the grace period")
	}

	// Simulate grace period having elapsed by back-dating lastSessionSeen.
	m.gcMu.Lock()
	m.lastSessionSeen[workspaceUUID] = time.Now().Add(-2 * time.Minute)
	m.gcMu.Unlock()

	// Second cycle: grace period expired — process must be stopped and removed.
	m.RunGCOnce()

	m.mu.RLock()
	_, exists = m.processes[workspaceUUID]
	m.mu.RUnlock()
	if exists {
		t.Error("process should have been removed after grace period expired")
	}
}

// TestGCTier2_ProcessWithActiveSessionsNotStopped verifies that a shared process
// is never stopped as long as its workspace has at least one active session.
func TestGCTier2_ProcessWithActiveSessionsNotStopped(t *testing.T) {
	workspaceUUID := "ws-active"

	proc := newTestSharedProcess()

	// Always return one session for the workspace — Tier 1 will try to close it,
	// but from Tier 2's perspective the workspace still has sessions, so the
	// process must not be stopped.
	m := newTestGCManager(
		func() map[string][]conversation.SessionInfo {
			return map[string][]conversation.SessionInfo{
				workspaceUUID: {{SessionID: "s1", WorkspaceUUID: workspaceUUID}},
			}
		},
		func(id string) {},
	)
	m.mu.Lock()
	m.processes[workspaceUUID] = proc
	m.mu.Unlock()

	for i := 0; i < 5; i++ {
		m.RunGCOnce()
	}

	m.mu.RLock()
	_, exists := m.processes[workspaceUUID]
	m.mu.RUnlock()
	if !exists {
		t.Error("process should not be stopped while workspace has sessions")
	}
}

// TestGCStartStop verifies that StartGC launches the background goroutine,
// the query function is called at least once, and StopGC shuts down cleanly.
func TestGCStartStop(t *testing.T) {
	m := &ACPProcessManager{
		logger:    newTestLogger(),
		processes: make(map[string]*SharedACPProcess),
	}

	var mu sync.Mutex
	queryCalled := 0

	m.StartGC(
		GCConfig{Interval: 10 * time.Millisecond, GracePeriod: 60 * time.Second},
		func() map[string][]conversation.SessionInfo {
			mu.Lock()
			queryCalled++
			mu.Unlock()
			return map[string][]conversation.SessionInfo{}
		},
		func(id string) {},
	)

	time.Sleep(100 * time.Millisecond)
	m.StopGC() // must not block or panic

	mu.Lock()
	n := queryCalled
	mu.Unlock()
	if n == 0 {
		t.Error("expected SessionQueryFunc to be called at least once during GC loop")
	}
}

// TestGCTier2_SkipsProcessWithActiveRPCs reproduces the race condition where the GC
// would kill the shared ACP pipe while a LoadSession or NewSession RPC was in-flight.
//
// Scenario:
//  1. A workspace process has no active sessions (sessionless) for longer than
//     the grace period — the GC would normally stop it.
//  2. However, activeRPCs > 0 because a LoadSession RPC is in-flight (e.g. a
//     session resuming after being closed by Tier 1 during the same GC run).
//
// Expected: the GC resets the grace period clock and skips the kill; the process
// survives. On the NEXT GC cycle, once activeRPCs == 0, the process is stopped.
func TestGCTier2_SkipsProcessWithActiveRPCs(t *testing.T) {
	workspaceUUID := "ws-inflight"

	proc := newTestSharedProcess()
	// Simulate an in-flight LoadSession/NewSession RPC.
	proc.activeRPCs.Add(1)

	m := newTestGCManager(
		func() map[string][]conversation.SessionInfo { return map[string][]conversation.SessionInfo{} }, // no sessions
		func(id string) {},
	)
	m.mu.Lock()
	m.processes[workspaceUUID] = proc
	m.mu.Unlock()

	// Pre-date lastSessionSeen so the grace period has already expired.
	m.gcMu.Lock()
	m.lastSessionSeen[workspaceUUID] = time.Now().Add(-2 * time.Minute)
	m.gcMu.Unlock()

	// First cycle: grace period expired, but in-flight RPC must protect the process.
	m.RunGCOnce()

	m.mu.RLock()
	_, exists := m.processes[workspaceUUID]
	m.mu.RUnlock()
	if !exists {
		t.Fatal("process was killed while an RPC was in-flight; expected it to survive")
	}

	// Complete the RPC and verify that the next GC cycle stops the process.
	proc.activeRPCs.Add(-1)

	// Pre-date again so the deferred grace period also appears expired.
	m.gcMu.Lock()
	m.lastSessionSeen[workspaceUUID] = time.Now().Add(-2 * time.Minute)
	m.gcMu.Unlock()

	m.RunGCOnce()

	m.mu.RLock()
	_, exists = m.processes[workspaceUUID]
	m.mu.RUnlock()
	if exists {
		t.Error("process should have been stopped after in-flight RPC completed")
	}
}

// TestGCTier1_SkipsRecentlyResumedSession verifies that a session resumed less than
// one GC interval ago is not closed, even when it has no observers or active prompts.
// This prevents the race where an async resume goroutine hasn't yet completed
// load_events / observer registration before the first GC cycle fires.
func TestGCTier1_SkipsRecentlyResumedSession(t *testing.T) {
	sessions := map[string][]conversation.SessionInfo{
		"ws-1": {
			{
				SessionID:     "recently-resumed",
				WorkspaceUUID: "ws-1",
				IsPrompting:   false,
				HasObservers:  false,
				QueueLength:   0,
				ResumedAt:     time.Now().Add(-5 * time.Second), // Resumed 5s ago, within 30s interval
			},
			{
				SessionID:     "old-idle",
				WorkspaceUUID: "ws-1",
				IsPrompting:   false,
				HasObservers:  false,
				QueueLength:   0,
				ResumedAt:     time.Now().Add(-5 * time.Minute), // Resumed 5 minutes ago
			},
		},
	}

	var mu sync.Mutex
	closed := make(map[string]bool)

	m := newTestGCManager(
		func() map[string][]conversation.SessionInfo { return sessions },
		func(id string) {
			mu.Lock()
			defer mu.Unlock()
			closed[id] = true
		},
	)

	m.RunGCOnce()

	mu.Lock()
	defer mu.Unlock()
	if closed["recently-resumed"] {
		t.Error("recently resumed session (5s ago) should not be GC'd within the grace period")
	}
	if !closed["old-idle"] {
		t.Error("old idle session (5min ago) should be closed by Tier 1 GC")
	}
}

// TestGCStartStop_DoubleStartIsNoop verifies that calling StartGC a second time
// while the GC is already running is a no-op, and StopGC still shuts down cleanly.
func TestGCStartStop_DoubleStartIsNoop(t *testing.T) {
	m := &ACPProcessManager{
		logger:    newTestLogger(),
		processes: make(map[string]*SharedACPProcess),
	}

	cfg := GCConfig{Interval: 10 * time.Millisecond, GracePeriod: 60 * time.Second}
	query := func() map[string][]conversation.SessionInfo { return map[string][]conversation.SessionInfo{} }
	closeF := func(id string) {}

	m.StartGC(cfg, query, closeF)
	m.StartGC(cfg, query, closeF) // second call must be a no-op, not panic

	m.StopGC() // clean shutdown

	// Calling StopGC again must also be a no-op.
	m.StopGC()
}

// TestGCTier1_SkipsRecentlyDisconnectedObservers verifies that a session whose
// last observer disconnected recently (within the observer grace period) is NOT
// closed by the GC, even if the resume grace period has expired. This prevents
// sessions from being closed during staggered reconnects (e.g., macOS app activation).
func TestGCTier1_SkipsRecentlyDisconnectedObservers(t *testing.T) {
	sessions := map[string][]conversation.SessionInfo{
		"ws-1": {
			{
				SessionID:             "recent-disconnect",
				WorkspaceUUID:         "ws-1",
				IsPrompting:           false,
				HasObservers:          false,
				QueueLength:           0,
				ResumedAt:             time.Now().Add(-5 * time.Minute), // Resumed long ago
				LastObserverRemovedAt: time.Now().Add(-2 * time.Second), // Observer disconnected 2s ago
			},
			{
				SessionID:             "old-disconnect",
				WorkspaceUUID:         "ws-1",
				IsPrompting:           false,
				HasObservers:          false,
				QueueLength:           0,
				ResumedAt:             time.Now().Add(-5 * time.Minute),  // Resumed long ago
				LastObserverRemovedAt: time.Now().Add(-90 * time.Second), // Observer disconnected 90s ago (well past 60s grace)
				LastActivityAt:        time.Now().Add(-10 * time.Minute), // No recent activity
			},
		},
	}

	var mu sync.Mutex
	closed := make(map[string]bool)

	m := newTestGCManager(
		func() map[string][]conversation.SessionInfo { return sessions },
		func(id string) {
			mu.Lock()
			defer mu.Unlock()
			closed[id] = true
		},
	)

	m.RunGCOnce()

	mu.Lock()
	defer mu.Unlock()
	if closed["recent-disconnect"] {
		t.Error("session with recently disconnected observers (2s ago) should not be GC'd within the observer grace period")
	}
	if !closed["old-disconnect"] {
		t.Error("session with observers disconnected 90s ago should be closed by Tier 1 GC")
	}
}

// TestGCTier1_ObserverGracePeriodDoesNotProtectForever verifies that the observer
// grace period eventually expires and the session is GC'd.
func TestGCTier1_ObserverGracePeriodDoesNotProtectForever(t *testing.T) {
	sessions := map[string][]conversation.SessionInfo{
		"ws-1": {
			{
				SessionID:             "expired-grace",
				WorkspaceUUID:         "ws-1",
				IsPrompting:           false,
				HasObservers:          false,
				QueueLength:           0,
				ResumedAt:             time.Now().Add(-10 * time.Minute),
				LastObserverRemovedAt: time.Now().Add(-90 * time.Second), // Well past the 60s grace
				LastActivityAt:        time.Now().Add(-10 * time.Minute), // No recent activity
			},
		},
	}

	var mu sync.Mutex
	closed := make(map[string]bool)

	m := newTestGCManager(
		func() map[string][]conversation.SessionInfo { return sessions },
		func(id string) {
			mu.Lock()
			defer mu.Unlock()
			closed[id] = true
		},
	)

	m.RunGCOnce()

	mu.Lock()
	defer mu.Unlock()
	if !closed["expired-grace"] {
		t.Error("session with expired observer grace period should be GC'd")
	}
}

// TestGCTier1_SkipsSessionsWithConnectedClients verifies that sessions with
// HasConnectedClients=true are not closed by the GC, even when there are no
// registered observers. Sessions with no connected clients and no observers
// are still eligible for closure.
func TestGCTier1_SkipsSessionsWithConnectedClients(t *testing.T) {
	sessions := map[string][]conversation.SessionInfo{
		"ws-1": {
			{
				SessionID:           "connected-clients",
				WorkspaceUUID:       "ws-1",
				HasConnectedClients: true,
				ResumedAt:           time.Now().Add(-5 * time.Minute),
			},
			{
				SessionID:           "no-clients",
				WorkspaceUUID:       "ws-1",
				HasConnectedClients: false,
				ResumedAt:           time.Now().Add(-5 * time.Minute),
			},
		},
	}

	var mu sync.Mutex
	closed := make(map[string]bool)

	m := newTestGCManager(
		func() map[string][]conversation.SessionInfo { return sessions },
		func(id string) {
			mu.Lock()
			defer mu.Unlock()
			closed[id] = true
		},
	)

	m.RunGCOnce()

	mu.Lock()
	defer mu.Unlock()
	if closed["connected-clients"] {
		t.Error("session with connected WebSocket clients should not be GC'd")
	}
	if !closed["no-clients"] {
		t.Error("session with no connected clients and no observers should be GC'd")
	}
}

// TestGCTier1_IdleTimeoutPreventsEarlyClosure verifies that sessions with recent
// activity (within IdleTimeout) are not GC'd, but sessions whose last activity
// exceeds the timeout are closed normally.
func TestGCTier1_IdleTimeoutPreventsEarlyClosure(t *testing.T) {
	sessions := map[string][]conversation.SessionInfo{
		"ws-1": {
			{
				SessionID:      "recent-activity",
				WorkspaceUUID:  "ws-1",
				ResumedAt:      time.Now().Add(-10 * time.Minute),
				LastActivityAt: time.Now().Add(-1 * time.Minute), // Within 5min IdleTimeout
			},
			{
				SessionID:      "old-activity",
				WorkspaceUUID:  "ws-1",
				ResumedAt:      time.Now().Add(-10 * time.Minute),
				LastActivityAt: time.Now().Add(-10 * time.Minute), // Beyond 5min IdleTimeout
			},
		},
	}

	var mu sync.Mutex
	closed := make(map[string]bool)

	m := newTestGCManager(
		func() map[string][]conversation.SessionInfo { return sessions },
		func(id string) {
			mu.Lock()
			defer mu.Unlock()
			closed[id] = true
		},
	)

	m.RunGCOnce()

	mu.Lock()
	defer mu.Unlock()
	if closed["recent-activity"] {
		t.Error("session with recent activity (1min ago) should not be GC'd within the idle timeout")
	}
	if !closed["old-activity"] {
		t.Error("session with old activity (10min ago) should be GC'd after idle timeout")
	}
}

// TestGCTier1_MaxClosuresPerCycle verifies that MaxClosuresPerCycle limits how many
// sessions the GC closes per cycle. Sessions beyond the limit are deferred to the
// next GC cycle.
func TestGCTier1_MaxClosuresPerCycle(t *testing.T) {
	var mu sync.Mutex
	closed := make(map[string]bool)

	allSessions := []conversation.SessionInfo{
		{SessionID: "idle-1", WorkspaceUUID: "ws-1", ResumedAt: time.Now().Add(-10 * time.Minute)},
		{SessionID: "idle-2", WorkspaceUUID: "ws-1", ResumedAt: time.Now().Add(-10 * time.Minute)},
		{SessionID: "idle-3", WorkspaceUUID: "ws-1", ResumedAt: time.Now().Add(-10 * time.Minute)},
		{SessionID: "idle-4", WorkspaceUUID: "ws-1", ResumedAt: time.Now().Add(-10 * time.Minute)},
		{SessionID: "idle-5", WorkspaceUUID: "ws-1", ResumedAt: time.Now().Add(-10 * time.Minute)},
	}

	m := newTestGCManager(
		func() map[string][]conversation.SessionInfo {
			mu.Lock()
			defer mu.Unlock()
			var remaining []conversation.SessionInfo
			for _, s := range allSessions {
				if !closed[s.SessionID] {
					remaining = append(remaining, s)
				}
			}
			return map[string][]conversation.SessionInfo{"ws-1": remaining}
		},
		func(id string) {
			mu.Lock()
			defer mu.Unlock()
			closed[id] = true
		},
	)
	m.gcConfig.MaxClosuresPerCycle = 3

	// First cycle: only 3 should be closed.
	m.RunGCOnce()

	mu.Lock()
	if len(closed) != 3 {
		mu.Unlock()
		t.Fatalf("expected 3 sessions closed in first cycle, got %d", len(closed))
	}
	mu.Unlock()

	// Second cycle: remaining 2 should be closed.
	m.RunGCOnce()

	mu.Lock()
	defer mu.Unlock()
	if len(closed) != 5 {
		t.Errorf("expected all 5 sessions closed after two cycles, got %d", len(closed))
	}
}

// TestGCTier1_MaxClosuresUnlimited verifies that MaxClosuresPerCycle=0 (unlimited)
// closes all idle sessions in a single GC cycle.
func TestGCTier1_MaxClosuresUnlimited(t *testing.T) {
	sessions := map[string][]conversation.SessionInfo{
		"ws-1": {
			{SessionID: "idle-1", WorkspaceUUID: "ws-1", ResumedAt: time.Now().Add(-10 * time.Minute)},
			{SessionID: "idle-2", WorkspaceUUID: "ws-1", ResumedAt: time.Now().Add(-10 * time.Minute)},
			{SessionID: "idle-3", WorkspaceUUID: "ws-1", ResumedAt: time.Now().Add(-10 * time.Minute)},
			{SessionID: "idle-4", WorkspaceUUID: "ws-1", ResumedAt: time.Now().Add(-10 * time.Minute)},
			{SessionID: "idle-5", WorkspaceUUID: "ws-1", ResumedAt: time.Now().Add(-10 * time.Minute)},
		},
	}

	var mu sync.Mutex
	closed := make(map[string]bool)

	m := newTestGCManager(
		func() map[string][]conversation.SessionInfo { return sessions },
		func(id string) {
			mu.Lock()
			defer mu.Unlock()
			closed[id] = true
		},
	)
	// MaxClosuresPerCycle defaults to 0 in newTestGCManager — unlimited.

	m.RunGCOnce()

	mu.Lock()
	defer mu.Unlock()
	if len(closed) != 5 {
		t.Errorf("expected all 5 sessions closed in one cycle with unlimited closures, got %d", len(closed))
	}
}

// TestGCTier3_CleansUpStaleAuxiliarySessions verifies that auxiliary sessions idle
// longer than AuxIdleTimeout are removed by Tier 3, while fresh sessions remain.
func TestGCTier3_CleansUpStaleAuxiliarySessions(t *testing.T) {
	m := newTestGCManager(
		func() map[string][]conversation.SessionInfo { return map[string][]conversation.SessionInfo{} },
		func(id string) {},
	)

	staleKey := auxSessionKey{workspaceUUID: "ws-1", purpose: "title-gen"}
	freshKey := auxSessionKey{workspaceUUID: "ws-1", purpose: "follow-up"}

	m.auxSessions[staleKey] = &auxiliarySessionState{
		sessionID: "aux-stale",
		client:    newAuxiliaryClient(),
		lastUsed:  time.Now().Add(-20 * time.Minute),
	}
	m.auxSessions[freshKey] = &auxiliarySessionState{
		sessionID: "aux-fresh",
		client:    newAuxiliaryClient(),
		lastUsed:  time.Now().Add(-1 * time.Minute),
	}

	m.RunGCOnce()

	m.auxMu.Lock()
	defer m.auxMu.Unlock()

	if _, ok := m.auxSessions[staleKey]; ok {
		t.Error("stale auxiliary session (20min idle) should have been cleaned up by Tier 3 GC")
	}
	if _, ok := m.auxSessions[freshKey]; !ok {
		t.Error("fresh auxiliary session (1min idle) should NOT have been cleaned up by Tier 3 GC")
	}
}

// TestGCTier3_NoCleanupWhenAllFresh verifies that Tier 3 does not remove auxiliary
// sessions that are within the AuxIdleTimeout window.
func TestGCTier3_NoCleanupWhenAllFresh(t *testing.T) {
	m := newTestGCManager(
		func() map[string][]conversation.SessionInfo { return map[string][]conversation.SessionInfo{} },
		func(id string) {},
	)

	key1 := auxSessionKey{workspaceUUID: "ws-1", purpose: "title-gen"}
	key2 := auxSessionKey{workspaceUUID: "ws-1", purpose: "follow-up"}

	m.auxSessions[key1] = &auxiliarySessionState{
		sessionID: "aux-1",
		client:    newAuxiliaryClient(),
		lastUsed:  time.Now().Add(-1 * time.Minute),
	}
	m.auxSessions[key2] = &auxiliarySessionState{
		sessionID: "aux-2",
		client:    newAuxiliaryClient(),
		lastUsed:  time.Now().Add(-1 * time.Minute),
	}

	m.RunGCOnce()

	m.auxMu.Lock()
	defer m.auxMu.Unlock()

	if _, ok := m.auxSessions[key1]; !ok {
		t.Error("fresh auxiliary session key1 should NOT have been cleaned up by Tier 3 GC")
	}
	if _, ok := m.auxSessions[key2]; !ok {
		t.Error("fresh auxiliary session key2 should NOT have been cleaned up by Tier 3 GC")
	}
}

// TestGCTier1_ObserverGracePeriodIgnoredWhenHasObservers verifies that sessions
// WITH observers are kept alive regardless of LastObserverRemovedAt.
func TestGCTier1_ObserverGracePeriodIgnoredWhenHasObservers(t *testing.T) {
	sessions := map[string][]conversation.SessionInfo{
		"ws-1": {
			{
				SessionID:             "has-observers",
				WorkspaceUUID:         "ws-1",
				IsPrompting:           false,
				HasObservers:          true,
				QueueLength:           0,
				ResumedAt:             time.Now().Add(-10 * time.Minute),
				LastObserverRemovedAt: time.Now().Add(-30 * time.Second), // Old, but has observers
			},
		},
	}

	var mu sync.Mutex
	closed := make(map[string]bool)

	m := newTestGCManager(
		func() map[string][]conversation.SessionInfo { return sessions },
		func(id string) {
			mu.Lock()
			defer mu.Unlock()
			closed[id] = true
		},
	)

	m.RunGCOnce()

	mu.Lock()
	defer mu.Unlock()
	if closed["has-observers"] {
		t.Error("session with active observers should never be GC'd")
	}
}

// =============================================================================
// Loop Suspend Heuristic Tests
// =============================================================================

// TestGCTier1_LoopSuspend_ClosesWithObservers verifies that a loop session
// with active observers is suspended (closed) when its next loop prompt is
// farther away than the LoopSuspendThreshold.
func TestGCTier1_LoopSuspend_ClosesWithObservers(t *testing.T) {
	// Next loop is 2 hours away — well beyond the 30m threshold.
	far := time.Now().Add(2 * time.Hour)

	sessions := map[string][]conversation.SessionInfo{
		"ws-1": {
			{
				SessionID:      "loop-far",
				WorkspaceUUID:  "ws-1",
				HasObservers:   true,
				NextLoopAt:     &far,
				ResumedAt:      time.Now().Add(-10 * time.Minute),
				LastActivityAt: time.Now().Add(-1 * time.Minute), // Recent activity — normally would prevent GC
			},
		},
	}

	var mu sync.Mutex
	closed := make(map[string]bool)

	m := newTestGCManager(
		func() map[string][]conversation.SessionInfo { return sessions },
		func(id string) {
			mu.Lock()
			defer mu.Unlock()
			closed[id] = true
		},
	)

	m.RunGCOnce()

	mu.Lock()
	defer mu.Unlock()
	if !closed["loop-far"] {
		t.Error("loop session with distant next-run should be suspended even with observers")
	}
	// Verify the GC-suspended flag is set to prevent auto-resume thrashing
	if !m.IsGCSuspended("loop-far") {
		t.Error("loop session should be marked as GC-suspended after suspension")
	}
}

// TestGCTier1_LoopSuspend_GCSuspendedFlagCleared verifies that ClearGCSuspended
// removes the flag and IsGCSuspended returns false for non-suspended sessions.
func TestGCTier1_LoopSuspend_GCSuspendedFlagCleared(t *testing.T) {
	m := newTestGCManager(
		func() map[string][]conversation.SessionInfo { return nil },
		func(id string) {},
	)

	// Initially not suspended
	if m.IsGCSuspended("test-session") {
		t.Error("session should not be GC-suspended initially")
	}

	// Mark as suspended
	m.MarkGCSuspended("test-session")
	if !m.IsGCSuspended("test-session") {
		t.Error("session should be GC-suspended after MarkGCSuspended")
	}

	// Clear the flag
	m.ClearGCSuspended("test-session")
	if m.IsGCSuspended("test-session") {
		t.Error("session should not be GC-suspended after ClearGCSuspended")
	}
}

// TestGCTier1_LoopSuspend_IdleClosureNotMarkedSuspended verifies that regular
// idle session closures do NOT set the GC-suspended flag (only loop suspensions do).
func TestGCTier1_LoopSuspend_IdleClosureNotMarkedSuspended(t *testing.T) {
	sessions := map[string][]conversation.SessionInfo{
		"ws-1": {
			{
				SessionID:      "idle-session",
				WorkspaceUUID:  "ws-1",
				HasObservers:   false,
				ResumedAt:      time.Now().Add(-10 * time.Minute),
				LastActivityAt: time.Now().Add(-10 * time.Minute),
			},
		},
	}

	m := newTestGCManager(
		func() map[string][]conversation.SessionInfo { return sessions },
		func(id string) {},
	)

	m.RunGCOnce()

	if m.IsGCSuspended("idle-session") {
		t.Error("regular idle session closure should NOT set GC-suspended flag")
	}
}

// TestGCTier1_LoopSuspend_KeepsCloseLoopWithObservers verifies that a
// loop session with observers is NOT suspended when its next loop prompt
// is within the LoopSuspendThreshold.
func TestGCTier1_LoopSuspend_KeepsCloseLoopWithObservers(t *testing.T) {
	// Next loop is 10 minutes away — within the 30m threshold.
	close_ := time.Now().Add(10 * time.Minute)

	sessions := map[string][]conversation.SessionInfo{
		"ws-1": {
			{
				SessionID:     "loop-close",
				WorkspaceUUID: "ws-1",
				HasObservers:  true,
				NextLoopAt:    &close_,
				ResumedAt:     time.Now().Add(-10 * time.Minute),
			},
		},
	}

	var mu sync.Mutex
	closed := make(map[string]bool)

	m := newTestGCManager(
		func() map[string][]conversation.SessionInfo { return sessions },
		func(id string) {
			mu.Lock()
			defer mu.Unlock()
			closed[id] = true
		},
	)

	m.RunGCOnce()

	mu.Lock()
	defer mu.Unlock()
	if closed["loop-close"] {
		t.Error("loop session with nearby next-run and observers should NOT be suspended")
	}
}

// TestGCTier1_LoopSuspend_KeepsNonLoopWithObservers verifies that a
// non-loop session with observers is never closed (the loop suspend
// heuristic does not apply to non-loop sessions).
func TestGCTier1_LoopSuspend_KeepsNonLoopWithObservers(t *testing.T) {
	sessions := map[string][]conversation.SessionInfo{
		"ws-1": {
			{
				SessionID:     "non-loop",
				WorkspaceUUID: "ws-1",
				HasObservers:  true,
				ResumedAt:     time.Now().Add(-10 * time.Minute),
			},
		},
	}

	var mu sync.Mutex
	closed := make(map[string]bool)

	m := newTestGCManager(
		func() map[string][]conversation.SessionInfo { return sessions },
		func(id string) {
			mu.Lock()
			defer mu.Unlock()
			closed[id] = true
		},
	)

	m.RunGCOnce()

	mu.Lock()
	defer mu.Unlock()
	if closed["non-loop"] {
		t.Error("non-loop session with observers should NOT be closed")
	}
}

// TestGCTier1_LoopSuspend_SkipsPrompting verifies that a loop session
// eligible for suspension is NOT closed when it is actively prompting.
func TestGCTier1_LoopSuspend_SkipsPrompting(t *testing.T) {
	far := time.Now().Add(2 * time.Hour)

	sessions := map[string][]conversation.SessionInfo{
		"ws-1": {
			{
				SessionID:     "loop-prompting",
				WorkspaceUUID: "ws-1",
				HasObservers:  true,
				IsPrompting:   true,
				NextLoopAt:    &far,
				ResumedAt:     time.Now().Add(-10 * time.Minute),
			},
		},
	}

	var mu sync.Mutex
	closed := make(map[string]bool)

	m := newTestGCManager(
		func() map[string][]conversation.SessionInfo { return sessions },
		func(id string) {
			mu.Lock()
			defer mu.Unlock()
			closed[id] = true
		},
	)

	m.RunGCOnce()

	mu.Lock()
	defer mu.Unlock()
	if closed["loop-prompting"] {
		t.Error("loop session that is prompting should NOT be suspended")
	}
}

// TestGCTier1_LoopSuspend_SkipsNonEmptyQueue verifies that a loop session
// eligible for suspension is NOT closed when it has queued messages.
func TestGCTier1_LoopSuspend_SkipsNonEmptyQueue(t *testing.T) {
	far := time.Now().Add(2 * time.Hour)

	sessions := map[string][]conversation.SessionInfo{
		"ws-1": {
			{
				SessionID:     "loop-queue",
				WorkspaceUUID: "ws-1",
				HasObservers:  true,
				QueueLength:   3,
				NextLoopAt:    &far,
				ResumedAt:     time.Now().Add(-10 * time.Minute),
			},
		},
	}

	var mu sync.Mutex
	closed := make(map[string]bool)

	m := newTestGCManager(
		func() map[string][]conversation.SessionInfo { return sessions },
		func(id string) {
			mu.Lock()
			defer mu.Unlock()
			closed[id] = true
		},
	)

	m.RunGCOnce()

	mu.Lock()
	defer mu.Unlock()
	if closed["loop-queue"] {
		t.Error("loop session with non-empty queue should NOT be suspended")
	}
}

// TestGCTier1_LoopSuspend_SkipsRecentlyResumed verifies that a loop session
// eligible for suspension is NOT closed when it was recently resumed (within one
// GC interval). This prevents a resume → immediate close → resume loop.
func TestGCTier1_LoopSuspend_SkipsRecentlyResumed(t *testing.T) {
	far := time.Now().Add(2 * time.Hour)

	sessions := map[string][]conversation.SessionInfo{
		"ws-1": {
			{
				SessionID:     "loop-just-resumed",
				WorkspaceUUID: "ws-1",
				HasObservers:  true,
				NextLoopAt:    &far,
				ResumedAt:     time.Now().Add(-5 * time.Second), // Resumed 5s ago, within 30s interval
			},
		},
	}

	var mu sync.Mutex
	closed := make(map[string]bool)

	m := newTestGCManager(
		func() map[string][]conversation.SessionInfo { return sessions },
		func(id string) {
			mu.Lock()
			defer mu.Unlock()
			closed[id] = true
		},
	)

	m.RunGCOnce()

	mu.Lock()
	defer mu.Unlock()
	if closed["loop-just-resumed"] {
		t.Error("recently resumed loop session should NOT be suspended (anti-thrash)")
	}
}

// TestGCTier1_LoopSuspend_SkipsWithinGrace verifies that a loop session
// that recently finished a turn is NOT suspended while it is within the generous
// LoopSuspendGracePeriod. This protects a conversation that just ended a turn
// (and may be about to continue) from being reclaimed too aggressively. The grace
// is keyed on LastResponseCompleteAt (turn END), not LastActivityAt (prompt START).
func TestGCTier1_LoopSuspend_SkipsWithinGrace(t *testing.T) {
	far := time.Now().Add(2 * time.Hour)

	sessions := map[string][]conversation.SessionInfo{
		"ws-1": {
			{
				SessionID:     "loop-grace",
				WorkspaceUUID: "ws-1",
				HasObservers:  true,
				NextLoopAt:    &far,
				ResumedAt:     time.Now().Add(-2 * time.Hour), // long ago — not "recently resumed"
				// Prompt started long ago (stale), but the turn ended just 2m ago.
				LastActivityAt:         time.Now().Add(-90 * time.Minute),
				LastResponseCompleteAt: time.Now().Add(-2 * time.Minute),
			},
		},
	}

	var mu sync.Mutex
	closed := make(map[string]bool)

	m := newTestGCManager(
		func() map[string][]conversation.SessionInfo { return sessions },
		func(id string) {
			mu.Lock()
			defer mu.Unlock()
			closed[id] = true
		},
	)
	m.gcConfig.LoopSuspendGracePeriod = 10 * time.Minute

	m.RunGCOnce()

	mu.Lock()
	defer mu.Unlock()
	if closed["loop-grace"] {
		t.Error("loop session that finished a turn within the grace window should NOT be suspended")
	}
	if m.IsGCSuspended("loop-grace") {
		t.Error("loop session within grace window should NOT be marked GC-suspended")
	}
}

// TestGCTier1_LoopSuspend_SuspendsAfterGrace verifies that once a loop
// session has been idle longer than LoopSuspendGracePeriod (no recent turn
// completion or activity), it is suspended as normal.
func TestGCTier1_LoopSuspend_SuspendsAfterGrace(t *testing.T) {
	far := time.Now().Add(2 * time.Hour)

	sessions := map[string][]conversation.SessionInfo{
		"ws-1": {
			{
				SessionID:              "loop-past-grace",
				WorkspaceUUID:          "ws-1",
				HasObservers:           true,
				NextLoopAt:             &far,
				ResumedAt:              time.Now().Add(-2 * time.Hour),
				LastActivityAt:         time.Now().Add(-90 * time.Minute),
				LastResponseCompleteAt: time.Now().Add(-30 * time.Minute), // well beyond grace
			},
		},
	}

	var mu sync.Mutex
	closed := make(map[string]bool)

	m := newTestGCManager(
		func() map[string][]conversation.SessionInfo { return sessions },
		func(id string) {
			mu.Lock()
			defer mu.Unlock()
			closed[id] = true
		},
	)
	m.gcConfig.LoopSuspendGracePeriod = 10 * time.Minute

	m.RunGCOnce()

	mu.Lock()
	defer mu.Unlock()
	if !closed["loop-past-grace"] {
		t.Error("loop session idle beyond the grace window should be suspended")
	}
	if !m.IsGCSuspended("loop-past-grace") {
		t.Error("loop session suspended after grace should be marked GC-suspended")
	}
}

// TestGCTier1_LoopSuspend_WithConnectedClients verifies that a loop session
// eligible for suspension is closed even when it has connected WebSocket clients
// (pre-connected background sessions that haven't sent load_events yet).
func TestGCTier1_LoopSuspend_WithConnectedClients(t *testing.T) {
	far := time.Now().Add(2 * time.Hour)

	sessions := map[string][]conversation.SessionInfo{
		"ws-1": {
			{
				SessionID:           "loop-clients",
				WorkspaceUUID:       "ws-1",
				HasObservers:        false,
				HasConnectedClients: true,
				NextLoopAt:          &far,
				ResumedAt:           time.Now().Add(-10 * time.Minute),
				LastActivityAt:      time.Now().Add(-1 * time.Minute),
			},
		},
	}

	var mu sync.Mutex
	closed := make(map[string]bool)

	m := newTestGCManager(
		func() map[string][]conversation.SessionInfo { return sessions },
		func(id string) {
			mu.Lock()
			defer mu.Unlock()
			closed[id] = true
		},
	)

	m.RunGCOnce()

	mu.Lock()
	defer mu.Unlock()
	if !closed["loop-clients"] {
		t.Error("loop session with distant next-run should be suspended even with connected clients")
	}
}

// TestGCTier1_LoopSuspend_DisabledWhenThresholdZero verifies that setting
// LoopSuspendThreshold to 0 disables the loop suspend heuristic.
func TestGCTier1_LoopSuspend_DisabledWhenThresholdZero(t *testing.T) {
	far := time.Now().Add(2 * time.Hour)

	sessions := map[string][]conversation.SessionInfo{
		"ws-1": {
			{
				SessionID:     "loop-no-suspend",
				WorkspaceUUID: "ws-1",
				HasObservers:  true,
				NextLoopAt:    &far,
				ResumedAt:     time.Now().Add(-10 * time.Minute),
			},
		},
	}

	var mu sync.Mutex
	closed := make(map[string]bool)

	m := newTestGCManager(
		func() map[string][]conversation.SessionInfo { return sessions },
		func(id string) {
			mu.Lock()
			defer mu.Unlock()
			closed[id] = true
		},
	)
	// Disable loop suspend by setting threshold to 0 (disabled).
	// StartGC converts negative values to 0; RunGCOnce skips the heuristic when <= 0.
	m.gcConfig.LoopSuspendThreshold = 0

	m.RunGCOnce()

	mu.Lock()
	defer mu.Unlock()
	if closed["loop-no-suspend"] {
		t.Error("loop session should NOT be suspended when LoopSuspendThreshold is disabled")
	}
}

// gcTier4Threshold is a convenient RSS threshold (1 GB) for Tier 4 tests.
const gcTier4Threshold uint64 = 1 << 30

// TestGCTier4_RecyclesBloatedIdleProcess verifies that an idle but memory-bloated
// shared process is recycled: its sessions are GC-suspended and closed, and the
// process is stopped. Sessions have observers so Tier 1 skips them, isolating the
// Tier 4 behavior.
func TestGCTier4_RecyclesBloatedIdleProcess(t *testing.T) {
	workspaceUUID := "ws-bloat"
	proc := newTestSharedProcess()

	sessions := map[string][]conversation.SessionInfo{
		workspaceUUID: {
			{SessionID: "s1", WorkspaceUUID: workspaceUUID, HasObservers: true},
			{SessionID: "s2", WorkspaceUUID: workspaceUUID, HasObservers: true},
		},
	}

	var mu sync.Mutex
	closed := make(map[string]bool)

	m := newTestGCManager(
		func() map[string][]conversation.SessionInfo { return sessions },
		func(id string) {
			mu.Lock()
			defer mu.Unlock()
			closed[id] = true
		},
	)
	m.mu.Lock()
	m.processes[workspaceUUID] = proc
	m.mu.Unlock()

	m.gcConfig.MemoryRecycleThreshold = gcTier4Threshold
	m.rssSampler = func(p *SharedACPProcess) (uint64, error) { return gcTier4Threshold + 1, nil }

	var recycledCalls int
	var gotUUID string
	var gotRSS, gotThreshold uint64
	var gotCount int
	m.onMemoryRecycled = func(workspaceUUID string, rssBytes, threshold uint64, sessionCount int) {
		mu.Lock()
		defer mu.Unlock()
		recycledCalls++
		gotUUID = workspaceUUID
		gotRSS = rssBytes
		gotThreshold = threshold
		gotCount = sessionCount
	}

	m.RunGCOnce()

	m.mu.RLock()
	_, exists := m.processes[workspaceUUID]
	m.mu.RUnlock()
	if exists {
		t.Error("bloated idle process should have been recycled (stopped)")
	}

	mu.Lock()
	defer mu.Unlock()
	for _, id := range []string{"s1", "s2"} {
		if !closed[id] {
			t.Errorf("expected session %s to be closed during recycle", id)
		}
		if !m.IsGCSuspended(id) {
			t.Errorf("expected session %s to be marked GC-suspended before close", id)
		}
	}

	// The recycle notification callback must fire exactly once with the
	// recycled workspace, the sampled RSS, the configured threshold, and the
	// number of recycled sessions.
	if recycledCalls != 1 {
		t.Errorf("expected onMemoryRecycled to be called once, got %d", recycledCalls)
	}
	if gotUUID != workspaceUUID {
		t.Errorf("expected recycled workspace %q, got %q", workspaceUUID, gotUUID)
	}
	if gotRSS != gcTier4Threshold+1 {
		t.Errorf("expected recycled rss %d, got %d", gcTier4Threshold+1, gotRSS)
	}
	if gotThreshold != gcTier4Threshold {
		t.Errorf("expected recycled threshold %d, got %d", gcTier4Threshold, gotThreshold)
	}
	if gotCount != 2 {
		t.Errorf("expected recycled session count 2, got %d", gotCount)
	}
}

// TestGCTier4_SkipsPromptingSession verifies that a process is not recycled while
// any of its sessions is actively prompting.
func TestGCTier4_SkipsPromptingSession(t *testing.T) {
	workspaceUUID := "ws-prompting"
	proc := newTestSharedProcess()

	sessions := map[string][]conversation.SessionInfo{
		workspaceUUID: {
			{SessionID: "s1", WorkspaceUUID: workspaceUUID, HasObservers: true, IsPrompting: true},
		},
	}

	m := newTestGCManager(
		func() map[string][]conversation.SessionInfo { return sessions },
		func(id string) {},
	)
	m.mu.Lock()
	m.processes[workspaceUUID] = proc
	m.mu.Unlock()

	m.gcConfig.MemoryRecycleThreshold = gcTier4Threshold
	m.rssSampler = func(p *SharedACPProcess) (uint64, error) { return gcTier4Threshold + 1, nil }

	m.RunGCOnce()

	m.mu.RLock()
	_, exists := m.processes[workspaceUUID]
	m.mu.RUnlock()
	if !exists {
		t.Error("process should NOT be recycled while a session is prompting")
	}
}

// TestGCTier4_SkipsActiveRPCs verifies that a process is not recycled while it has
// in-flight RPCs.
func TestGCTier4_SkipsActiveRPCs(t *testing.T) {
	workspaceUUID := "ws-rpcs"
	proc := newTestSharedProcess()
	proc.activeRPCs.Add(1)

	sessions := map[string][]conversation.SessionInfo{
		workspaceUUID: {
			{SessionID: "s1", WorkspaceUUID: workspaceUUID, HasObservers: true},
		},
	}

	m := newTestGCManager(
		func() map[string][]conversation.SessionInfo { return sessions },
		func(id string) {},
	)
	m.mu.Lock()
	m.processes[workspaceUUID] = proc
	m.mu.Unlock()

	m.gcConfig.MemoryRecycleThreshold = gcTier4Threshold
	m.rssSampler = func(p *SharedACPProcess) (uint64, error) { return gcTier4Threshold + 1, nil }

	m.RunGCOnce()

	m.mu.RLock()
	_, exists := m.processes[workspaceUUID]
	m.mu.RUnlock()
	if !exists {
		t.Error("process should NOT be recycled while RPCs are in-flight")
	}
}

// TestGCTier4_SkipsNonEmptyQueue verifies that a process is not recycled while any
// of its sessions has a non-empty queue.
func TestGCTier4_SkipsNonEmptyQueue(t *testing.T) {
	workspaceUUID := "ws-queue"
	proc := newTestSharedProcess()

	sessions := map[string][]conversation.SessionInfo{
		workspaceUUID: {
			{SessionID: "s1", WorkspaceUUID: workspaceUUID, HasObservers: true, QueueLength: 1},
		},
	}

	m := newTestGCManager(
		func() map[string][]conversation.SessionInfo { return sessions },
		func(id string) {},
	)
	m.mu.Lock()
	m.processes[workspaceUUID] = proc
	m.mu.Unlock()

	m.gcConfig.MemoryRecycleThreshold = gcTier4Threshold
	m.rssSampler = func(p *SharedACPProcess) (uint64, error) { return gcTier4Threshold + 1, nil }

	m.RunGCOnce()

	m.mu.RLock()
	_, exists := m.processes[workspaceUUID]
	m.mu.RUnlock()
	if !exists {
		t.Error("process should NOT be recycled while a session has a non-empty queue")
	}
}

// TestGCTier4_DisabledWhenThresholdZero verifies that the memory-recycle tier is
// skipped entirely when MemoryRecycleThreshold is 0, and the RSS sampler is never
// invoked.
func TestGCTier4_DisabledWhenThresholdZero(t *testing.T) {
	workspaceUUID := "ws-disabled"
	proc := newTestSharedProcess()

	sessions := map[string][]conversation.SessionInfo{
		workspaceUUID: {
			{SessionID: "s1", WorkspaceUUID: workspaceUUID, HasObservers: true},
		},
	}

	m := newTestGCManager(
		func() map[string][]conversation.SessionInfo { return sessions },
		func(id string) {},
	)
	m.mu.Lock()
	m.processes[workspaceUUID] = proc
	m.mu.Unlock()

	// Threshold left at 0 (disabled).
	sampled := false
	m.rssSampler = func(p *SharedACPProcess) (uint64, error) {
		sampled = true
		return gcTier4Threshold + 1, nil
	}

	m.RunGCOnce()

	if sampled {
		t.Error("RSS sampler must not be called when MemoryRecycleThreshold is 0")
	}
	m.mu.RLock()
	_, exists := m.processes[workspaceUUID]
	m.mu.RUnlock()
	if !exists {
		t.Error("process should NOT be recycled when memory recycling is disabled")
	}
}

// TestGCHealthTier_RecyclesSaturatedIdleProcess reproduces mitto-tfb: a shared
// ACP process that has become saturated/degraded (repeated session/new + LoadSession
// context-deadlines tracked by mitto-13ck.2) is NOT recycled by GC, even though it
// is fully idle and safe to recycle. The saturation infra only fails fast on new
// requests; nothing converts "repeated deadlines" into a fresh process, so the
// degraded process keeps starving the next resume / loop-prompt.
//
// This test drives the process to saturation via recordRPCTimeout() (mirroring the
// signal from real session/new timeouts) while keeping RSS well BELOW the Tier 4
// memory threshold — isolating the HEALTH signal from the memory signal. With the
// process fully idle (no in-flight RPCs, no prompting/queued/loop-due sessions), a
// proactive-health GC tier SHOULD recycle it so the next resume lands on a fresh,
// healthy process.
//
// Before the fix this FAILS: no health-based recycle tier exists, so the saturated
// process survives GC. After the fix it passes: the idle saturated process is
// GC-suspended, its sessions closed, and the process stopped.
func TestGCHealthTier_RecyclesSaturatedIdleProcess(t *testing.T) {
	workspaceUUID := "ws-saturated"
	proc := newTestSharedProcess()

	// Drive the process to saturation: consecutive RPC timeouts up to the
	// threshold trip the saturated state (same path real session/new deadlines
	// take via recordRPCTimeout).
	for i := 0; i < sessionSaturationTimeoutThreshold; i++ {
		proc.recordRPCTimeout()
	}
	if !proc.isSaturated() {
		t.Fatalf("test setup: process should be saturated after %d timeouts", sessionSaturationTimeoutThreshold)
	}
	// isSaturated() self-clears to a probe when the cooldown elapses; re-trip so
	// the process is unambiguously saturated for the GC pass below.
	for i := 0; i < sessionSaturationTimeoutThreshold; i++ {
		proc.recordRPCTimeout()
	}

	sessions := map[string][]conversation.SessionInfo{
		workspaceUUID: {
			{SessionID: "s1", WorkspaceUUID: workspaceUUID, HasObservers: true},
			{SessionID: "s2", WorkspaceUUID: workspaceUUID, HasObservers: true},
		},
	}

	var mu sync.Mutex
	closed := make(map[string]bool)

	m := newTestGCManager(
		func() map[string][]conversation.SessionInfo { return sessions },
		func(id string) {
			mu.Lock()
			defer mu.Unlock()
			closed[id] = true
		},
	)
	m.mu.Lock()
	m.processes[workspaceUUID] = proc
	m.mu.Unlock()

	// Keep RSS BELOW the memory threshold so Tier 4 does NOT fire — only the
	// health/saturation signal should drive the recycle.
	m.gcConfig.MemoryRecycleThreshold = gcTier4Threshold
	m.rssSampler = func(p *SharedACPProcess) (uint64, error) { return gcTier4Threshold / 2, nil }

	m.RunGCOnce()

	m.mu.RLock()
	_, exists := m.processes[workspaceUUID]
	m.mu.RUnlock()
	if exists {
		t.Error("saturated idle process should have been recycled (stopped) by the health tier")
	}

	mu.Lock()
	defer mu.Unlock()
	for _, id := range []string{"s1", "s2"} {
		if !closed[id] {
			t.Errorf("expected session %s to be closed during health recycle", id)
		}
		if !m.IsGCSuspended(id) {
			t.Errorf("expected session %s to be marked GC-suspended before close", id)
		}
	}
}

// TestGCHealthTier_OnHealthRecycledCallback verifies the mitto-aoo
// notification wiring: recycling a saturated-idle process (Tier 5) invokes
// onHealthRecycled exactly once with reason "saturated_idle" and the correct
// workspace UUID, saturation level, and recycled session count — the signal
// the web layer uses to broadcast the "agent_recycled" toast.
func TestGCHealthTier_OnHealthRecycledCallback(t *testing.T) {
	workspaceUUID := "ws-saturated-cb"
	proc := newTestSharedProcess()

	for i := 0; i < sessionSaturationTimeoutThreshold; i++ {
		proc.recordRPCTimeout()
	}
	// Re-trip in case the cooldown already elapsed and opened a probe.
	for i := 0; i < sessionSaturationTimeoutThreshold; i++ {
		proc.recordRPCTimeout()
	}
	wantLevel := proc.SaturationLevel()

	sessions := map[string][]conversation.SessionInfo{
		workspaceUUID: {
			{SessionID: "s1", WorkspaceUUID: workspaceUUID, HasObservers: true},
			{SessionID: "s2", WorkspaceUUID: workspaceUUID, HasObservers: true},
		},
	}

	var mu sync.Mutex
	m := newTestGCManager(
		func() map[string][]conversation.SessionInfo { return sessions },
		func(id string) {},
	)
	m.mu.Lock()
	m.processes[workspaceUUID] = proc
	m.mu.Unlock()
	m.gcConfig.MemoryRecycleThreshold = gcTier4Threshold
	m.rssSampler = func(p *SharedACPProcess) (uint64, error) { return gcTier4Threshold / 2, nil }

	var calls int
	var gotUUID, gotReason string
	var gotLevel, gotCount int
	m.onHealthRecycled = func(workspaceUUID, reason string, saturationLevel, sessionCount int) {
		mu.Lock()
		defer mu.Unlock()
		calls++
		gotUUID = workspaceUUID
		gotReason = reason
		gotLevel = saturationLevel
		gotCount = sessionCount
	}

	m.RunGCOnce()

	mu.Lock()
	defer mu.Unlock()
	if calls != 1 {
		t.Fatalf("expected onHealthRecycled to be called once, got %d", calls)
	}
	if gotUUID != workspaceUUID {
		t.Errorf("expected recycled workspace %q, got %q", workspaceUUID, gotUUID)
	}
	if gotReason != "saturated_idle" {
		t.Errorf("expected reason %q, got %q", "saturated_idle", gotReason)
	}
	if gotLevel != wantLevel {
		t.Errorf("expected saturationLevel %d, got %d", wantLevel, gotLevel)
	}
	if gotCount != 2 {
		t.Errorf("expected recycled session count 2, got %d", gotCount)
	}
}

// TestGCHealthTier_RecyclesMCPInitGatedProcess is the mitto-13n regression
// test: a shared ACP process whose stderr monitor has observed the agent
// report "MCP initialization timed out" (mcpInitTimedOut=true) is permanently
// gated in getOrCreateAuxiliarySession (the "mcp_init_gated" bail in
// acp_process_manager.go) — every auxiliary NewSession call (title-gen,
// follow-up, keepalive, the adaptive prewarm health probe itself) bails
// before issuing an RPC, so mcpInitTimedOut is NEVER cleared (it is only
// cleared by beginMCPInitWindow(), called from an actual NewSession/
// LoadSession attempt) and the gate never opens.
//
// Before the fix, Tier 5 (the GC's only proactive health-recycle path for an
// idle process) gated exclusively on IsSaturated(), which reads the SEPARATE
// saturatedUntil state set only by recordRPCTimeout/recordRPCWedgeFailure/etc
// — none of which getOrCreateAuxiliarySession's early-return bails ever call.
// So a process that has only ever hit the mcp_init_gated bail had
// IsSaturated()==false and IsConfirmedDegraded()==false: Tier 5/6 never saw
// it, even though it was fully idle (zero sessions) and permanently unable to
// serve auxiliary work — surviving GC forever.
//
// Tier 5 now also treats MCPInitTimedOut()==true as a health-recycle signal
// (independent of IsSaturated()), so this idle mcp-init-gated process is
// recycled and this test verifies that outcome, plus the "mcp_init_gated"
// reason surfaced via onHealthRecycled.
func TestGCHealthTier_RecyclesMCPInitGatedProcess(t *testing.T) {
	workspaceUUID := "ws-mcp-init-gated"
	proc := newTestSharedProcess()
	proc.mcpInitTimedOut.Store(true)

	// Preconditions: confirm this process is invisible to the saturation
	// signal, isolating this test from the already-covered saturation-timeout
	// path (TestGCHealthTier_RecyclesSaturatedIdleProcess) — recycling here
	// must be driven solely by MCPInitTimedOut().
	if proc.IsSaturated() {
		t.Fatal("test setup: mcp-init-timed-out process must read IsSaturated()=false (isolates the gap from the timeout-driven saturation path)")
	}
	if proc.IsConfirmedDegraded() {
		t.Fatal("test setup: mcp-init-timed-out process must read IsConfirmedDegraded()=false")
	}

	// Zero sessions: this workspace is otherwise fully idle, satisfying every
	// hard safety gate Tier 5 already checks (ActiveRPCs()==0, no prompting
	// session, no queued work, no imminent loop). The only thing that used to
	// keep this process alive was the missing MCPInitTimedOut() health signal.
	sessions := map[string][]conversation.SessionInfo{}

	var mu sync.Mutex
	m := newTestGCManager(
		func() map[string][]conversation.SessionInfo { return sessions },
		func(id string) {},
	)
	m.mu.Lock()
	m.processes[workspaceUUID] = proc
	m.mu.Unlock()
	// Keep this workspace out of Tier 2's idle-timeout path (which recycles
	// purely on session absence + grace period, unrelated to health) so a
	// pass fairly isolates the Tier 5 health-recycle path under test.
	m.lastSessionSeen[workspaceUUID] = time.Now()
	m.gcConfig.MemoryRecycleThreshold = gcTier4Threshold
	m.rssSampler = func(p *SharedACPProcess) (uint64, error) { return gcTier4Threshold / 2, nil }

	var calls int
	var gotReason string
	m.onHealthRecycled = func(_, reason string, _, _ int) {
		mu.Lock()
		defer mu.Unlock()
		calls++
		gotReason = reason
	}

	m.RunGCOnce()

	m.mu.RLock()
	_, exists := m.processes[workspaceUUID]
	m.mu.RUnlock()
	if exists {
		t.Fatal("mcp-init-gated idle process was NOT recycled by any GC health tier " +
			"— it would survive indefinitely while every auxiliary call keeps bailing on it")
	}

	mu.Lock()
	defer mu.Unlock()
	if calls != 1 {
		t.Fatalf("expected onHealthRecycled to be called once, got %d", calls)
	}
	if gotReason != "mcp_init_gated" {
		t.Errorf("expected reason %q, got %q", "mcp_init_gated", gotReason)
	}
}

// TestGCHealthTier_RecyclesMCPInitInProgressWedgedProcess is the mitto-13n.1
// reproduction test: a shared ACP process whose stderr monitor observed the
// agent begin an MCP-init handshake (mcpInitInProgress=true) that never
// completes (mcpInitDone=false) is invisible to EVERY existing health-recycle
// signal, even though it is fully idle:
//
//   - IsSaturated()/IsConfirmedDegraded(): both read saturatedUntil, set only
//     by recordRPCTimeout/recordRPCWedgeFailure — never called because the
//     mcp_init_gated bail in getOrCreateAuxiliarySession returns BEFORE any
//     RPC is attempted.
//   - MCPInitTimedOut(): false — per the mitto-13n.1 investigation, the agent
//     frequently gives up on its own MCP-init wait WITHOUT ever emitting the
//     stderr line the timeout callback watches for, so this flag never
//     latches true for the cold in-progress case (disjunct (2) in the
//     investigation comment, distinct from the already-fixed disjunct (1)
//     covered by TestGCHealthTier_RecyclesMCPInitGatedProcess above).
//
// Tier 5's predicate today (acp_process_gc.go) is
// `!p.IsSaturated() && !p.MCPInitTimedOut() -> continue`, which has no term
// for MCPInitInProgress()&&!MCPInitDone() at all — so this process survives
// GC indefinitely regardless of how long it has been wedged, reproducing the
// "waiting for GC recycle" symptom logged 30 times in production
// (internal/processors/apply.go) for a recycle that could never occur.
//
// This test currently FAILS: it asserts the wedged-but-idle process IS
// recycled, which is the intended (not yet implemented) behavior once Tier 5
// gains the MCPInitInProgress()&&!MCPInitDone() disjunct.
func TestGCHealthTier_RecyclesMCPInitInProgressWedgedProcess(t *testing.T) {
	workspaceUUID := "ws-mcp-init-in-progress-wedged"
	proc := newTestSharedProcess()
	proc.config.MCPInitTimeout = 5 * time.Second
	proc.mcpInitInProgress.Store(true)
	// mcpInitInProgressSince mirrors the timestamp onMCPInitProgress stamps at
	// the false->true edge; back-dated well past the 2x-MCPInitTimeout bound
	// so this reproduces a handshake that has been wedged for a while, not one
	// merely slow-but-progressing.
	proc.mcpInitInProgressSince = time.Now().Add(-3 * proc.config.MCPInitTimeout)
	// mcpInitDone and mcpInitTimedOut are left at their zero value (false),
	// mirroring the observed production case: the handshake started and
	// never finished, and the agent never logged its own timeout either.

	// Preconditions: confirm this process is invisible to every
	// currently-implemented health signal, isolating this test from the
	// already-covered saturation and MCPInitTimedOut paths.
	if proc.IsSaturated() {
		t.Fatal("test setup: mcp-init-in-progress process must read IsSaturated()=false")
	}
	if proc.IsConfirmedDegraded() {
		t.Fatal("test setup: mcp-init-in-progress process must read IsConfirmedDegraded()=false")
	}
	if proc.MCPInitTimedOut() {
		t.Fatal("test setup: mcp-init-in-progress process must read MCPInitTimedOut()=false")
	}
	if !proc.MCPInitInProgress() || proc.MCPInitDone() {
		t.Fatal("test setup: expected MCPInitInProgress()=true, MCPInitDone()=false")
	}

	// Zero sessions: this workspace is otherwise fully idle, satisfying every
	// hard safety gate Tier 5 already checks (ActiveRPCs()==0, no prompting
	// session, no queued work, no imminent loop).
	sessions := map[string][]conversation.SessionInfo{}

	var mu sync.Mutex
	m := newTestGCManager(
		func() map[string][]conversation.SessionInfo { return sessions },
		func(id string) {},
	)
	m.mu.Lock()
	m.processes[workspaceUUID] = proc
	m.mu.Unlock()
	// Keep this workspace out of Tier 2's idle-timeout path so a pass fairly
	// isolates the Tier 5 health-recycle path under test.
	m.lastSessionSeen[workspaceUUID] = time.Now()
	m.gcConfig.MemoryRecycleThreshold = gcTier4Threshold
	m.rssSampler = func(p *SharedACPProcess) (uint64, error) { return gcTier4Threshold / 2, nil }

	var calls int
	var gotReason string
	m.onHealthRecycled = func(_, reason string, _, _ int) {
		mu.Lock()
		defer mu.Unlock()
		calls++
		gotReason = reason
	}

	m.RunGCOnce()

	m.mu.RLock()
	_, exists := m.processes[workspaceUUID]
	m.mu.RUnlock()
	if exists {
		t.Fatal("mcp-init-in-progress wedged idle process was NOT recycled by any GC health tier " +
			"— it would survive indefinitely since MCPInitTimedOut() never latches for this case (mitto-13n.1)")
	}

	mu.Lock()
	defer mu.Unlock()
	if calls != 1 {
		t.Fatalf("expected onHealthRecycled to be called once, got %d", calls)
	}
	if gotReason != "mcp_init_wedged" {
		t.Errorf("expected reason %q, got %q", "mcp_init_wedged", gotReason)
	}
}

// driveToConfirmedDegraded pushes proc's saturation state machine to
// saturationLevel >= confirmedDegradedLevel (2), mirroring the real sequence:
// trip saturation (threshold consecutive timeouts) -> cooldown elapses (probe
// opens) -> the probe itself times out (escalates to level 2).
func driveToConfirmedDegraded(t *testing.T, proc *SharedACPProcess) {
	t.Helper()
	for i := 0; i < sessionSaturationTimeoutThreshold; i++ {
		proc.recordRPCTimeout()
	}
	if proc.SaturationLevel() != 1 {
		t.Fatalf("test setup: expected saturationLevel 1 after initial trip, got %d", proc.SaturationLevel())
	}
	// Force the cooldown to have already elapsed so isSaturated() opens the probe.
	proc.saturationMu.Lock()
	proc.saturatedUntil = time.Now().Add(-time.Millisecond)
	proc.saturationMu.Unlock()
	if proc.isSaturated() {
		t.Fatalf("test setup: expected isSaturated()=false once cooldown elapses (probe opens)")
	}
	proc.saturationMu.Lock()
	inProbe := proc.inProbe
	proc.saturationMu.Unlock()
	if !inProbe {
		t.Fatalf("test setup: expected inProbe=true after cooldown elapse")
	}
	// The probe itself times out -> escalates to level 2 and re-saturates.
	proc.recordRPCTimeout()
	if !proc.IsConfirmedDegraded() {
		t.Fatalf("test setup: expected IsConfirmedDegraded()=true, saturationLevel=%d", proc.SaturationLevel())
	}
}

// TestGCTier6_RecyclesBusyConfirmedDegradedProcess verifies that a confirmed-
// degraded (saturationLevel >= 2) shared ACP process is recycled by Tier 6 even
// while busy (in-flight RPCs / a prompting session), provided no session shows
// recent streamed activity (mitto-1h0).
func TestGCTier6_RecyclesBusyConfirmedDegradedProcess(t *testing.T) {
	workspaceUUID := "ws-degraded-busy"
	proc := newTestSharedProcess()
	driveToConfirmedDegraded(t, proc)
	proc.activeRPCs.Add(1) // simulate an in-flight (wedged) control RPC

	sessions := map[string][]conversation.SessionInfo{
		workspaceUUID: {
			{
				SessionID:            "s1",
				WorkspaceUUID:        workspaceUUID,
				HasObservers:         true,
				IsPrompting:          true,
				LastStreamActivityAt: time.Now().Add(-time.Hour), // stale
			},
		},
	}

	var mu sync.Mutex
	closed := make(map[string]bool)

	m := newTestGCManager(
		func() map[string][]conversation.SessionInfo { return sessions },
		func(id string) {
			mu.Lock()
			defer mu.Unlock()
			closed[id] = true
		},
	)
	m.mu.Lock()
	m.processes[workspaceUUID] = proc
	m.mu.Unlock()

	m.RunGCOnce()

	m.mu.RLock()
	_, exists := m.processes[workspaceUUID]
	m.mu.RUnlock()
	if exists {
		t.Error("confirmed-degraded busy process should have been recycled by Tier 6")
	}

	mu.Lock()
	defer mu.Unlock()
	if !closed["s1"] {
		t.Error("expected session s1 to be closed during Tier 6 recycle")
	}
	if !m.IsGCSuspended("s1") {
		t.Error("expected session s1 to be marked GC-suspended before close")
	}
}

// TestGCTier6_OnHealthRecycledCallback verifies the mitto-aoo notification
// wiring for the Tier 6 (confirmed-degraded, busy) recycle path: the
// callback fires once with reason "confirmed_degraded" and the correct
// workspace UUID, saturation level, and recycled session count.
func TestGCTier6_OnHealthRecycledCallback(t *testing.T) {
	workspaceUUID := "ws-degraded-busy-cb"
	proc := newTestSharedProcess()
	driveToConfirmedDegraded(t, proc)
	proc.activeRPCs.Add(1)
	wantLevel := proc.SaturationLevel()

	sessions := map[string][]conversation.SessionInfo{
		workspaceUUID: {
			{
				SessionID:            "s1",
				WorkspaceUUID:        workspaceUUID,
				HasObservers:         true,
				IsPrompting:          true,
				LastStreamActivityAt: time.Now().Add(-time.Hour), // stale
			},
		},
	}

	var mu sync.Mutex
	m := newTestGCManager(
		func() map[string][]conversation.SessionInfo { return sessions },
		func(id string) {},
	)
	m.mu.Lock()
	m.processes[workspaceUUID] = proc
	m.mu.Unlock()

	var calls int
	var gotUUID, gotReason string
	var gotLevel, gotCount int
	m.onHealthRecycled = func(workspaceUUID, reason string, saturationLevel, sessionCount int) {
		mu.Lock()
		defer mu.Unlock()
		calls++
		gotUUID = workspaceUUID
		gotReason = reason
		gotLevel = saturationLevel
		gotCount = sessionCount
	}

	m.RunGCOnce()

	mu.Lock()
	defer mu.Unlock()
	if calls != 1 {
		t.Fatalf("expected onHealthRecycled to be called once, got %d", calls)
	}
	if gotUUID != workspaceUUID {
		t.Errorf("expected recycled workspace %q, got %q", workspaceUUID, gotUUID)
	}
	if gotReason != "confirmed_degraded" {
		t.Errorf("expected reason %q, got %q", "confirmed_degraded", gotReason)
	}
	if gotLevel != wantLevel {
		t.Errorf("expected saturationLevel %d, got %d", wantLevel, gotLevel)
	}
	if gotCount != 1 {
		t.Errorf("expected recycled session count 1, got %d", gotCount)
	}
}

// TestGCTier6_SkipsProgressingDegradedProcess verifies that a confirmed-degraded
// process is NOT recycled by Tier 6 when a session has streamed activity within
// the quiet window — it is legitimately slow but progressing (mitto-1h0
// anti-regression guard).
func TestGCTier6_SkipsProgressingDegradedProcess(t *testing.T) {
	workspaceUUID := "ws-degraded-progressing"
	proc := newTestSharedProcess()
	driveToConfirmedDegraded(t, proc)
	proc.activeRPCs.Add(1)

	sessions := map[string][]conversation.SessionInfo{
		workspaceUUID: {
			{
				SessionID:            "s1",
				WorkspaceUUID:        workspaceUUID,
				HasObservers:         true,
				IsPrompting:          true,
				LastStreamActivityAt: time.Now(), // recent -> progressing
			},
		},
	}

	m := newTestGCManager(
		func() map[string][]conversation.SessionInfo { return sessions },
		func(id string) {},
	)
	m.mu.Lock()
	m.processes[workspaceUUID] = proc
	m.mu.Unlock()

	m.RunGCOnce()

	m.mu.RLock()
	_, exists := m.processes[workspaceUUID]
	m.mu.RUnlock()
	if !exists {
		t.Error("progressing confirmed-degraded process should NOT have been recycled by Tier 6")
	}
}

// TestGCTier6_DoesNotKillPromptingSessionAwaitingFirstToken reproduces
// mitto-6a7p: Tier 6's no-streamed-progress guard is
//
//	!s.LastStreamActivityAt.IsZero() && now.Sub(s.LastStreamActivityAt) < tier6StreamedProgressQuietWindow
//
// A session that has dispatched a prompt but has not yet received its first
// streamed token has LastStreamActivityAt == zero value, so the IsZero()
// clause short-circuits the whole check to false — the session is classified
// as "not progressing", the exact opposite of the truth (mode A from the
// investigation: the handshake/set_model window before the first token
// arrives). LastActivityAt mirrors what production always sets at the same
// instant a prompt starts (BackgroundSession.TouchActivity, called from
// PromptWithMeta right after promptStartTime is stamped), so it is set here
// too to reflect a realistic prompting-session snapshot. Verifies a
// confirmed-degraded busy process is NOT recycled while a session is in this
// state.
func TestGCTier6_DoesNotKillPromptingSessionAwaitingFirstToken(t *testing.T) {
	workspaceUUID := "ws-degraded-awaiting-first-token"
	proc := newTestSharedProcess()
	driveToConfirmedDegraded(t, proc)
	proc.activeRPCs.Add(1) // simulate an in-flight session/new or set_model RPC

	sessions := map[string][]conversation.SessionInfo{
		workspaceUUID: {
			{
				SessionID:     "s1",
				WorkspaceUUID: workspaceUUID,
				HasObservers:  true,
				IsPrompting:   true,
				// Never streamed: prompt just dispatched, awaiting first token.
				LastStreamActivityAt: time.Time{},
				// Set synchronously at prompt start in production (TouchActivity).
				LastActivityAt: time.Now(),
			},
		},
	}

	m := newTestGCManager(
		func() map[string][]conversation.SessionInfo { return sessions },
		func(id string) {},
	)
	m.mu.Lock()
	m.processes[workspaceUUID] = proc
	m.mu.Unlock()

	m.RunGCOnce()

	m.mu.RLock()
	_, exists := m.processes[workspaceUUID]
	m.mu.RUnlock()
	if !exists {
		t.Error("session awaiting its first token (IsPrompting=true, zero LastStreamActivityAt) " +
			"should NOT have been recycled by Tier 6 (mitto-6a7p)")
	}
}

// TestGCTier6_SkipsLevel1BusyProcess verifies that a level-1 (first-trip)
// saturated busy process is NOT recycled by Tier 6 — only Tier 5's idle-gated
// path governs level-1 saturation (mitto-1h0).
func TestGCTier6_SkipsLevel1BusyProcess(t *testing.T) {
	workspaceUUID := "ws-level1-busy"
	proc := newTestSharedProcess()
	for i := 0; i < sessionSaturationTimeoutThreshold; i++ {
		proc.recordRPCTimeout()
	}
	if proc.SaturationLevel() != 1 {
		t.Fatalf("test setup: expected saturationLevel 1, got %d", proc.SaturationLevel())
	}
	if proc.IsConfirmedDegraded() {
		t.Fatalf("test setup: level-1 process should not be IsConfirmedDegraded()")
	}
	proc.activeRPCs.Add(1)

	sessions := map[string][]conversation.SessionInfo{
		workspaceUUID: {
			{
				SessionID:            "s1",
				WorkspaceUUID:        workspaceUUID,
				HasObservers:         true,
				IsPrompting:          true,
				LastStreamActivityAt: time.Now().Add(-time.Hour),
			},
		},
	}

	m := newTestGCManager(
		func() map[string][]conversation.SessionInfo { return sessions },
		func(id string) {},
	)
	m.mu.Lock()
	m.processes[workspaceUUID] = proc
	m.mu.Unlock()

	m.RunGCOnce()

	m.mu.RLock()
	_, exists := m.processes[workspaceUUID]
	m.mu.RUnlock()
	if !exists {
		t.Error("level-1 saturated busy process should NOT have been recycled by Tier 6")
	}
}

// TestGCTier4_SkipsPinnedWorkspace verifies that a bloated idle process is NOT
// memory-recycled when its workspace is pinned by the adaptive pre-warming
// controller (mitto-54k.7). Recycling a pinned workspace would defeat the
// purpose of the pin and force a cold MCP-server spawn on the next prompt.
func TestGCTier4_SkipsPinnedWorkspace(t *testing.T) {
	workspaceUUID := "ws-pinned-bloat"
	proc := newTestSharedProcess()

	sessions := map[string][]conversation.SessionInfo{
		workspaceUUID: {
			{SessionID: "s1", WorkspaceUUID: workspaceUUID, HasObservers: true},
		},
	}

	m := newTestGCManager(
		func() map[string][]conversation.SessionInfo { return sessions },
		func(id string) {},
	)
	m.mu.Lock()
	m.processes[workspaceUUID] = proc
	m.mu.Unlock()

	// Pin the workspace (no expiry) — the proactive keepalive pin flavour.
	if !m.PinWorkspace(workspaceUUID, "prewarm", 0, 0) {
		t.Fatal("PinWorkspace returned false")
	}

	// Configure the memory-recycle tier so the process would otherwise be reaped.
	m.gcConfig.MemoryRecycleThreshold = gcTier4Threshold
	sampled := false
	m.rssSampler = func(p *SharedACPProcess) (uint64, error) {
		sampled = true
		return gcTier4Threshold + 1, nil
	}

	m.RunGCOnce()

	// The RSS sampler must NOT be invoked for a pinned workspace: the pinned
	// check is placed before the sampler in Tier 4.
	if sampled {
		t.Error("RSS sampler must not be called for a pinned workspace")
	}
	m.mu.RLock()
	_, exists := m.processes[workspaceUUID]
	m.mu.RUnlock()
	if !exists {
		t.Error("pinned workspace should NOT be memory-recycled")
	}
}

// captureHandler is a minimal slog.Handler that records every log Record so
// tests can assert on the attribute keys/values emitted by production code.
// It is safe for concurrent use across GC goroutines and RunGCOnce paths.
type captureHandler struct {
	mu      sync.Mutex
	records []slog.Record
}

func (h *captureHandler) Enabled(_ context.Context, _ slog.Level) bool { return true }
func (h *captureHandler) Handle(_ context.Context, r slog.Record) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.records = append(h.records, r.Clone())
	return nil
}
func (h *captureHandler) WithAttrs(_ []slog.Attr) slog.Handler { return h }
func (h *captureHandler) WithGroup(_ string) slog.Handler      { return h }

// findRecord returns the first captured record whose Message equals msg, or
// nil if none was captured. Caller must hold no external lock.
func (h *captureHandler) findRecord(msg string) *slog.Record {
	h.mu.Lock()
	defer h.mu.Unlock()
	for i := range h.records {
		if h.records[i].Message == msg {
			return &h.records[i]
		}
	}
	return nil
}

// attrInt64 extracts an int-kind attribute from a slog.Record as int64. slog
// normalizes all Go signed-int values to Int64 internally, so `int`, `int32`,
// and `int64` all round-trip as int64. Returns (0, false) if the key is absent
// or the attribute is not int-kind.
func attrInt64(r *slog.Record, key string) (int64, bool) {
	var out int64
	var ok bool
	r.Attrs(func(a slog.Attr) bool {
		if a.Key == key && a.Value.Kind() == slog.KindInt64 {
			out = a.Value.Int64()
			ok = true
			return false
		}
		return true
	})
	return out, ok
}

// attrValue extracts the value of the top-level attribute with the given key
// from a slog.Record, or returns nil if the key is absent.
func attrValue(r *slog.Record, key string) any {
	var out any
	r.Attrs(func(a slog.Attr) bool {
		if a.Key == key {
			out = a.Value.Any()
			return false
		}
		return true
	})
	return out
}

// TestGCTier4_LogsRSSBreakdown_MITTO3GU is the failing reproduction for mitto-3gu.
//
// The bead's acceptance criterion — "Root of the 7.5 GB growth identified (agent
// process vs. MCP children)" — cannot be met from log evidence alone today,
// because the two Tier 4 log lines emit only the process-tree total `rss_bytes`,
// with no split between the parent (auggie/node) process and its descendants
// (MCP children). Every future occurrence therefore remains ambiguous.
//
// The fix must extend both Tier 4 recycler log lines
// ("GC: recycling memory-bloated idle shared ACP process" and
// "GC: memory recycle below threshold") to also carry
//   - parent_rss_bytes      (RSS of the ACP agent process itself)
//   - descendant_rss_bytes  (RSS summed over all descendants)
//   - descendant_count      (number of descendant processes counted)
//
// This test asserts BOTH log lines carry those three fields. Before the fix it
// FAILS (the current code does not emit any of the three keys); after the fix
// it passes.
func TestGCTier4_LogsRSSBreakdown_MITTO3GU(t *testing.T) {
	// ---- Sub-scenario A: over threshold -> "recycling memory-bloated" log ----
	t.Run("recycling_memory_bloated_carries_breakdown", func(t *testing.T) {
		workspaceUUID := "ws-bloat-breakdown"
		proc := newTestSharedProcess()

		sessions := map[string][]conversation.SessionInfo{
			workspaceUUID: {
				{SessionID: "s1", WorkspaceUUID: workspaceUUID, HasObservers: true},
			},
		}

		m := newTestGCManager(
			func() map[string][]conversation.SessionInfo { return sessions },
			func(id string) {},
		)
		cap := &captureHandler{}
		m.logger = slog.New(cap)
		m.mu.Lock()
		m.processes[workspaceUUID] = proc
		m.mu.Unlock()

		// Synthetic split: parent 500 MB, descendants 7 GB across 9 children —
		// matches the on-call workspace's shape (auggie + 8-9 stdio MCPs).
		const (
			parentRSS       uint64 = 500 * 1024 * 1024
			descendantRSS   uint64 = 7 * 1024 * 1024 * 1024
			descendantCount int    = 9
		)
		total := parentRSS + descendantRSS

		m.gcConfig.MemoryRecycleThreshold = gcTier4Threshold
		if total <= gcTier4Threshold {
			t.Fatalf("test setup: synthetic total %d must exceed threshold %d", total, gcTier4Threshold)
		}
		// Sampler returns the tree total, exactly as today. The breakdown must
		// flow through some other seam that the fix introduces (e.g. a
		// detailed-sampler field on ACPProcessManager, or an extended return
		// from rssSampler). We do NOT prescribe which seam here — we only
		// assert on the observable log payload.
		m.rssSampler = func(p *SharedACPProcess) (uint64, error) { return total, nil }
		installBreakdownSampler(m, parentRSS, descendantRSS, descendantCount)

		m.RunGCOnce()

		rec := cap.findRecord("GC: recycling memory-bloated idle shared ACP process")
		if rec == nil {
			t.Fatalf("expected log record for memory-bloated recycle, got none; captured=%d", len(cap.records))
		}
		// Existing fields must still be present (regression guard).
		if got := attrValue(rec, "rss_bytes"); got != total {
			t.Errorf("rss_bytes: want %d, got %v", total, got)
		}
		if got := attrValue(rec, "effective_memory_bytes"); got != total {
			t.Errorf("effective_memory_bytes: want %d, got %v", total, got)
		}
		if got := attrValue(rec, "threshold_bytes"); got != gcTier4Threshold {
			t.Errorf("threshold_bytes: want %d, got %v", gcTier4Threshold, got)
		}
		// New breakdown fields the fix must add.
		if got := attrValue(rec, "parent_rss_bytes"); got != parentRSS {
			t.Errorf("parent_rss_bytes: want %d, got %v (fix must add this field)", parentRSS, got)
		}
		if got := attrValue(rec, "descendant_rss_bytes"); got != descendantRSS {
			t.Errorf("descendant_rss_bytes: want %d, got %v (fix must add this field)", descendantRSS, got)
		}
		if got, ok := attrInt64(rec, "descendant_count"); !ok || got != int64(descendantCount) {
			t.Errorf("descendant_count: want %d, got %d (ok=%v) (fix must add this field)", descendantCount, got, ok)
		}
	})

	// ---- Sub-scenario B: below threshold -> "memory recycle below threshold" log ----
	t.Run("below_threshold_carries_breakdown", func(t *testing.T) {
		workspaceUUID := "ws-below-breakdown"
		proc := newTestSharedProcess()

		sessions := map[string][]conversation.SessionInfo{
			workspaceUUID: {
				{SessionID: "s1", WorkspaceUUID: workspaceUUID, HasObservers: true},
			},
		}

		m := newTestGCManager(
			func() map[string][]conversation.SessionInfo { return sessions },
			func(id string) {},
		)
		cap := &captureHandler{}
		m.logger = slog.New(cap)
		m.mu.Lock()
		m.processes[workspaceUUID] = proc
		m.mu.Unlock()

		const (
			parentRSS       uint64 = 200 * 1024 * 1024
			descendantRSS   uint64 = 300 * 1024 * 1024
			descendantCount int    = 3
		)
		total := parentRSS + descendantRSS

		m.gcConfig.MemoryRecycleThreshold = gcTier4Threshold
		if total >= gcTier4Threshold {
			t.Fatalf("test setup: synthetic total %d must be below threshold %d", total, gcTier4Threshold)
		}
		m.rssSampler = func(p *SharedACPProcess) (uint64, error) { return total, nil }
		installBreakdownSampler(m, parentRSS, descendantRSS, descendantCount)

		m.RunGCOnce()

		rec := cap.findRecord("GC: memory recycle below threshold")
		if rec == nil {
			t.Fatalf("expected log record for below-threshold sample, got none; captured=%d", len(cap.records))
		}
		if got := attrValue(rec, "rss_bytes"); got != total {
			t.Errorf("rss_bytes: want %d, got %v", total, got)
		}
		if got := attrValue(rec, "effective_memory_bytes"); got != total {
			t.Errorf("effective_memory_bytes: want %d, got %v", total, got)
		}
		if got := attrValue(rec, "parent_rss_bytes"); got != parentRSS {
			t.Errorf("parent_rss_bytes: want %d, got %v (fix must add this field)", parentRSS, got)
		}
		if got := attrValue(rec, "descendant_rss_bytes"); got != descendantRSS {
			t.Errorf("descendant_rss_bytes: want %d, got %v (fix must add this field)", descendantRSS, got)
		}
		if got, ok := attrInt64(rec, "descendant_count"); !ok || got != int64(descendantCount) {
			t.Errorf("descendant_count: want %d, got %d (ok=%v) (fix must add this field)", descendantCount, got, ok)
		}
	})
}

// installBreakdownSampler wires the detailed-RSS seam so tests can inject a
// synthetic parent/descendant split for Tier 4's log lines (mitto-3gu).
func installBreakdownSampler(m *ACPProcessManager, parentRSS, descendantRSS uint64, descendantCount int) {
	m.rssBreakdownSampler = func(p *SharedACPProcess) (uint64, uint64, int, error) {
		return parentRSS, descendantRSS, descendantCount, nil
	}
}

// TestGCTier4_DescendantCountRatchet_MITTO52MT is the failing reproduction for
// mitto-52mt: an auggie ACP process aborted with a V8 "JavaScript heap out of
// memory" fatal error after its descendant (MCP-child) process count ratcheted
// monotonically from 82 to 98 over ~2 hours, while combined tree RSS stayed
// pinned at ~2.16 GB -- well under the configured 6 GB MemoryRecycleThreshold.
//
// Root cause (see the mitto-52mt Investigation comment): Tier 4's only recycle
// predicate is `rss <= MemoryRecycleThreshold` (acp_process_gc.go). The
// descendantCount returned by the breakdown sampler is sampled every cycle but
// used ONLY as a log attribute on the Debug-level "GC: memory recycle below
// threshold" line -- it never drives a decision. The V8 heap abort is a
// per-process cap decoupled from tree RSS (the parent held only ~204 MB at the
// moment of the abort), so no RSS threshold can ever see this failure mode.
//
// This test replays the incident's exact descendant-count ratchet (82 -> 88 ->
// 92 -> 98) across four successive GC cycles while RSS is held fixed at the
// incident's reported values (parent ~204 MB, descendants ~1.95 GB), all far
// below a 6 GB threshold so Tier 4's RSS predicate never fires. It asserts a
// WARN-level log record surfaces the unbounded climb before the fix; today no
// such record is ever emitted, so this test FAILS. After the fix adds a
// count-based ratchet signal to Tier 4, it must PASS.
func TestGCTier4_DescendantCountRatchet_MITTO52MT(t *testing.T) {
	workspaceUUID := "ws-oncall-3d1c815e"
	proc := newTestSharedProcess()

	sessions := map[string][]conversation.SessionInfo{
		workspaceUUID: {
			{SessionID: "s1", WorkspaceUUID: workspaceUUID, HasObservers: true},
		},
	}

	m := newTestGCManager(
		func() map[string][]conversation.SessionInfo { return sessions },
		func(id string) {},
	)
	cap := &captureHandler{}
	m.logger = slog.New(cap)
	m.mu.Lock()
	m.processes[workspaceUUID] = proc
	m.mu.Unlock()

	// Mirrors the reported incident values: parent_rss_bytes=204390400,
	// descendant_rss_bytes=1953677312, threshold_bytes=6442450944 (6 GiB).
	const (
		parentRSS     uint64 = 204390400
		descendantRSS uint64 = 1953677312
		threshold     uint64 = 6 * 1024 * 1024 * 1024
	)
	total := parentRSS + descendantRSS
	if total >= threshold {
		t.Fatalf("test setup: synthetic total %d must stay below threshold %d (matching the incident)", total, threshold)
	}
	m.gcConfig.MemoryRecycleThreshold = threshold
	m.rssSampler = func(p *SharedACPProcess) (uint64, error) { return total, nil }

	// Descendant count ratchets monotonically across successive GC cycles,
	// exactly mirroring the incident timeline (14:23 -> 82, 15:08 -> 88,
	// 16:11 -> 92, 17:08 -> 98), never shrinking, while RSS stays fixed and
	// far below threshold throughout.
	counts := []int{82, 88, 92, 98}
	idx := 0
	m.rssBreakdownSampler = func(p *SharedACPProcess) (uint64, uint64, int, error) {
		c := counts[idx]
		if idx < len(counts)-1 {
			idx++
		}
		return parentRSS, descendantRSS, c, nil
	}

	for range counts {
		m.RunGCOnce()
	}

	// Sanity: the process must survive every cycle -- RSS never crosses the
	// threshold, so Tier 4's RSS-based recycle correctly never fires. If this
	// fails, the test setup itself (not the ratchet-detection bug) is wrong.
	m.mu.RLock()
	_, exists := m.processes[workspaceUUID]
	m.mu.RUnlock()
	if !exists {
		t.Fatal("test setup: process should not have been RSS-recycled (RSS stays below threshold throughout)")
	}

	// The bug: an unbounded, monotonically climbing descendant count is
	// invisible today -- no WARN is ever emitted for it.
	rec := cap.findRecord("GC: descendant count climbing without bound")
	if rec == nil {
		t.Fatalf("expected a WARN log once the descendant count ratchets %v while RSS stays below threshold (mitto-52mt), got none; captured=%d records", counts, len(cap.records))
	}
	if got, ok := attrInt64(rec, "descendant_count"); !ok || got != int64(counts[len(counts)-1]) {
		t.Errorf("descendant_count: want %d, got %d (ok=%v)", counts[len(counts)-1], got, ok)
	}
	if got := attrValue(rec, "workspace_uuid"); got != workspaceUUID {
		t.Errorf("workspace_uuid: want %q, got %v", workspaceUUID, got)
	}
}

func TestEffectiveProcessTreeMemory_MITTO52MT(t *testing.T) {
	const (
		finalRSS         uint64 = 521 * 1024 * 1024
		v8Footprint      uint64 = 12073 * 1024 * 1024
		recycleThreshold uint64 = 6 * 1024 * 1024 * 1024
	)

	got := effectiveProcessTreeMemory(finalRSS, v8Footprint)
	if got != v8Footprint {
		t.Fatalf("effective memory = %d, want physical footprint %d", got, v8Footprint)
	}
	if got <= recycleThreshold {
		t.Fatalf("effective memory %d must cross recycle threshold %d", got, recycleThreshold)
	}
	if got := effectiveProcessTreeMemory(v8Footprint, finalRSS); got != v8Footprint {
		t.Fatalf("RSS fallback = %d, want larger RSS %d", got, v8Footprint)
	}
}
