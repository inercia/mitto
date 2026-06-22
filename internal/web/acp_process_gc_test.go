package web

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
		gcConfig: GCConfig{
			Interval:                 30 * time.Second,
			GracePeriod:              60 * time.Second,
			ObserverGracePeriod:      60 * time.Second,
			IdleTimeout:              5 * time.Minute,
			AuxIdleTimeout:           10 * time.Minute,
			PeriodicSuspendThreshold: 30 * time.Minute,
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
// (not prompting, no observers, empty queue, no periodic) are closed by Tier 1.
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
	// NextPeriodicAt within 2×interval (60s) — should be skipped.
	soon := time.Now().Add(10 * time.Second)

	sessions := map[string][]conversation.SessionInfo{
		"ws-1": {
			{SessionID: "prompting", WorkspaceUUID: "ws-1", IsPrompting: true},
			{SessionID: "observers", WorkspaceUUID: "ws-1", HasObservers: true},
			{SessionID: "queue", WorkspaceUUID: "ws-1", QueueLength: 5},
			{SessionID: "periodic", WorkspaceUUID: "ws-1", NextPeriodicAt: &soon},
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

// TestGCTier1_ClosesSessionWithDistantPeriodic verifies that a session whose
// next periodic prompt is far in the future (beyond 2×interval) is still
// considered idle and is closed by Tier 1.
func TestGCTier1_ClosesSessionWithDistantPeriodic(t *testing.T) {
	far := time.Now().Add(2 * time.Hour) // well beyond 2×30s = 60s threshold

	sessions := map[string][]conversation.SessionInfo{
		"ws-1": {
			{SessionID: "distant-periodic", WorkspaceUUID: "ws-1", NextPeriodicAt: &far},
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
	if !closed["distant-periodic"] {
		t.Error("session with distant periodic should be closed by Tier 1")
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
// Periodic Suspend Heuristic Tests
// =============================================================================

// TestGCTier1_PeriodicSuspend_ClosesWithObservers verifies that a periodic session
// with active observers is suspended (closed) when its next periodic prompt is
// farther away than the PeriodicSuspendThreshold.
func TestGCTier1_PeriodicSuspend_ClosesWithObservers(t *testing.T) {
	// Next periodic is 2 hours away — well beyond the 30m threshold.
	far := time.Now().Add(2 * time.Hour)

	sessions := map[string][]conversation.SessionInfo{
		"ws-1": {
			{
				SessionID:      "periodic-far",
				WorkspaceUUID:  "ws-1",
				HasObservers:   true,
				NextPeriodicAt: &far,
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
	if !closed["periodic-far"] {
		t.Error("periodic session with distant next-run should be suspended even with observers")
	}
	// Verify the GC-suspended flag is set to prevent auto-resume thrashing
	if !m.IsGCSuspended("periodic-far") {
		t.Error("periodic session should be marked as GC-suspended after suspension")
	}
}

// TestGCTier1_PeriodicSuspend_GCSuspendedFlagCleared verifies that ClearGCSuspended
// removes the flag and IsGCSuspended returns false for non-suspended sessions.
func TestGCTier1_PeriodicSuspend_GCSuspendedFlagCleared(t *testing.T) {
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

// TestGCTier1_PeriodicSuspend_IdleClosureNotMarkedSuspended verifies that regular
// idle session closures do NOT set the GC-suspended flag (only periodic suspensions do).
func TestGCTier1_PeriodicSuspend_IdleClosureNotMarkedSuspended(t *testing.T) {
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

// TestGCTier1_PeriodicSuspend_KeepsClosePeriodicWithObservers verifies that a
// periodic session with observers is NOT suspended when its next periodic prompt
// is within the PeriodicSuspendThreshold.
func TestGCTier1_PeriodicSuspend_KeepsClosePeriodicWithObservers(t *testing.T) {
	// Next periodic is 10 minutes away — within the 30m threshold.
	close_ := time.Now().Add(10 * time.Minute)

	sessions := map[string][]conversation.SessionInfo{
		"ws-1": {
			{
				SessionID:      "periodic-close",
				WorkspaceUUID:  "ws-1",
				HasObservers:   true,
				NextPeriodicAt: &close_,
				ResumedAt:      time.Now().Add(-10 * time.Minute),
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
	if closed["periodic-close"] {
		t.Error("periodic session with nearby next-run and observers should NOT be suspended")
	}
}

// TestGCTier1_PeriodicSuspend_KeepsNonPeriodicWithObservers verifies that a
// non-periodic session with observers is never closed (the periodic suspend
// heuristic does not apply to non-periodic sessions).
func TestGCTier1_PeriodicSuspend_KeepsNonPeriodicWithObservers(t *testing.T) {
	sessions := map[string][]conversation.SessionInfo{
		"ws-1": {
			{
				SessionID:     "non-periodic",
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
	if closed["non-periodic"] {
		t.Error("non-periodic session with observers should NOT be closed")
	}
}

// TestGCTier1_PeriodicSuspend_SkipsPrompting verifies that a periodic session
// eligible for suspension is NOT closed when it is actively prompting.
func TestGCTier1_PeriodicSuspend_SkipsPrompting(t *testing.T) {
	far := time.Now().Add(2 * time.Hour)

	sessions := map[string][]conversation.SessionInfo{
		"ws-1": {
			{
				SessionID:      "periodic-prompting",
				WorkspaceUUID:  "ws-1",
				HasObservers:   true,
				IsPrompting:    true,
				NextPeriodicAt: &far,
				ResumedAt:      time.Now().Add(-10 * time.Minute),
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
	if closed["periodic-prompting"] {
		t.Error("periodic session that is prompting should NOT be suspended")
	}
}

// TestGCTier1_PeriodicSuspend_SkipsNonEmptyQueue verifies that a periodic session
// eligible for suspension is NOT closed when it has queued messages.
func TestGCTier1_PeriodicSuspend_SkipsNonEmptyQueue(t *testing.T) {
	far := time.Now().Add(2 * time.Hour)

	sessions := map[string][]conversation.SessionInfo{
		"ws-1": {
			{
				SessionID:      "periodic-queue",
				WorkspaceUUID:  "ws-1",
				HasObservers:   true,
				QueueLength:    3,
				NextPeriodicAt: &far,
				ResumedAt:      time.Now().Add(-10 * time.Minute),
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
	if closed["periodic-queue"] {
		t.Error("periodic session with non-empty queue should NOT be suspended")
	}
}

// TestGCTier1_PeriodicSuspend_SkipsRecentlyResumed verifies that a periodic session
// eligible for suspension is NOT closed when it was recently resumed (within one
// GC interval). This prevents a resume → immediate close → resume loop.
func TestGCTier1_PeriodicSuspend_SkipsRecentlyResumed(t *testing.T) {
	far := time.Now().Add(2 * time.Hour)

	sessions := map[string][]conversation.SessionInfo{
		"ws-1": {
			{
				SessionID:      "periodic-just-resumed",
				WorkspaceUUID:  "ws-1",
				HasObservers:   true,
				NextPeriodicAt: &far,
				ResumedAt:      time.Now().Add(-5 * time.Second), // Resumed 5s ago, within 30s interval
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
	if closed["periodic-just-resumed"] {
		t.Error("recently resumed periodic session should NOT be suspended (anti-thrash)")
	}
}

// TestGCTier1_PeriodicSuspend_SkipsWithinGrace verifies that a periodic session
// that recently finished a turn is NOT suspended while it is within the generous
// PeriodicSuspendGracePeriod. This protects a conversation that just ended a turn
// (and may be about to continue) from being reclaimed too aggressively. The grace
// is keyed on LastResponseCompleteAt (turn END), not LastActivityAt (prompt START).
func TestGCTier1_PeriodicSuspend_SkipsWithinGrace(t *testing.T) {
	far := time.Now().Add(2 * time.Hour)

	sessions := map[string][]conversation.SessionInfo{
		"ws-1": {
			{
				SessionID:      "periodic-grace",
				WorkspaceUUID:  "ws-1",
				HasObservers:   true,
				NextPeriodicAt: &far,
				ResumedAt:      time.Now().Add(-2 * time.Hour), // long ago — not "recently resumed"
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
	m.gcConfig.PeriodicSuspendGracePeriod = 10 * time.Minute

	m.RunGCOnce()

	mu.Lock()
	defer mu.Unlock()
	if closed["periodic-grace"] {
		t.Error("periodic session that finished a turn within the grace window should NOT be suspended")
	}
	if m.IsGCSuspended("periodic-grace") {
		t.Error("periodic session within grace window should NOT be marked GC-suspended")
	}
}

// TestGCTier1_PeriodicSuspend_SuspendsAfterGrace verifies that once a periodic
// session has been idle longer than PeriodicSuspendGracePeriod (no recent turn
// completion or activity), it is suspended as normal.
func TestGCTier1_PeriodicSuspend_SuspendsAfterGrace(t *testing.T) {
	far := time.Now().Add(2 * time.Hour)

	sessions := map[string][]conversation.SessionInfo{
		"ws-1": {
			{
				SessionID:              "periodic-past-grace",
				WorkspaceUUID:          "ws-1",
				HasObservers:           true,
				NextPeriodicAt:         &far,
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
	m.gcConfig.PeriodicSuspendGracePeriod = 10 * time.Minute

	m.RunGCOnce()

	mu.Lock()
	defer mu.Unlock()
	if !closed["periodic-past-grace"] {
		t.Error("periodic session idle beyond the grace window should be suspended")
	}
	if !m.IsGCSuspended("periodic-past-grace") {
		t.Error("periodic session suspended after grace should be marked GC-suspended")
	}
}

// TestGCTier1_PeriodicSuspend_WithConnectedClients verifies that a periodic session
// eligible for suspension is closed even when it has connected WebSocket clients
// (pre-connected background sessions that haven't sent load_events yet).
func TestGCTier1_PeriodicSuspend_WithConnectedClients(t *testing.T) {
	far := time.Now().Add(2 * time.Hour)

	sessions := map[string][]conversation.SessionInfo{
		"ws-1": {
			{
				SessionID:           "periodic-clients",
				WorkspaceUUID:       "ws-1",
				HasObservers:        false,
				HasConnectedClients: true,
				NextPeriodicAt:      &far,
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
	if !closed["periodic-clients"] {
		t.Error("periodic session with distant next-run should be suspended even with connected clients")
	}
}

// TestGCTier1_PeriodicSuspend_DisabledWhenThresholdZero verifies that setting
// PeriodicSuspendThreshold to 0 disables the periodic suspend heuristic.
func TestGCTier1_PeriodicSuspend_DisabledWhenThresholdZero(t *testing.T) {
	far := time.Now().Add(2 * time.Hour)

	sessions := map[string][]conversation.SessionInfo{
		"ws-1": {
			{
				SessionID:      "periodic-no-suspend",
				WorkspaceUUID:  "ws-1",
				HasObservers:   true,
				NextPeriodicAt: &far,
				ResumedAt:      time.Now().Add(-10 * time.Minute),
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
	// Disable periodic suspend by setting threshold to 0 (disabled).
	// StartGC converts negative values to 0; RunGCOnce skips the heuristic when <= 0.
	m.gcConfig.PeriodicSuspendThreshold = 0

	m.RunGCOnce()

	mu.Lock()
	defer mu.Unlock()
	if closed["periodic-no-suspend"] {
		t.Error("periodic session should NOT be suspended when PeriodicSuspendThreshold is disabled")
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
