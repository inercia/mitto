package acpproc

import (
	"sync"
	"testing"
	"time"

	"github.com/inercia/mitto/internal/conversation"
)

// --- processDegradedState (pure predicate) -------------------------------

func TestProcessDegradedState_NilProcess(t *testing.T) {
	if got := processDegradedState(nil, time.Now()); got != "" {
		t.Errorf("expected \"\" for nil process, got %q", got)
	}
}

func TestProcessDegradedState_Healthy(t *testing.T) {
	proc := newTestSharedProcess()
	if got := processDegradedState(proc, time.Now()); got != "" {
		t.Errorf("expected \"\" for a fresh healthy process, got %q", got)
	}
}

func TestProcessDegradedState_Saturated(t *testing.T) {
	proc := newTestSharedProcess()
	for i := 0; i < sessionSaturationTimeoutThreshold; i++ {
		proc.recordRPCTimeout()
	}
	if !proc.IsSaturated() {
		t.Fatal("test setup: expected proc to be saturated")
	}
	if got := processDegradedState(proc, time.Now()); got != "process_saturated" {
		t.Errorf("expected %q, got %q", "process_saturated", got)
	}
}

func TestProcessDegradedState_MCPInitGated(t *testing.T) {
	proc := newTestSharedProcess()
	proc.mcpInitTimedOut.Store(true)
	if got := processDegradedState(proc, time.Now()); got != "mcp_init_gated" {
		t.Errorf("expected %q, got %q", "mcp_init_gated", got)
	}
}

func TestProcessDegradedState_MCPInitWedged(t *testing.T) {
	proc := newTestSharedProcess()
	proc.config.MCPInitTimeout = 5 * time.Second
	proc.mcpInitInProgress.Store(true)
	proc.mcpInitInProgressSince = time.Now().Add(-3 * proc.config.MCPInitTimeout)
	if got := processDegradedState(proc, time.Now()); got != "mcp_init_wedged" {
		t.Errorf("expected %q, got %q", "mcp_init_wedged", got)
	}
}

// TestProcessDegradedState_MCPInitInProgress_NotYetWedged verifies a
// handshake that is merely slow-but-progressing (within the 2x bound) is NOT
// flagged degraded — only a handshake wedged past that window should be.
func TestProcessDegradedState_MCPInitInProgress_NotYetWedged(t *testing.T) {
	proc := newTestSharedProcess()
	proc.config.MCPInitTimeout = 5 * time.Second
	proc.mcpInitInProgress.Store(true)
	proc.mcpInitInProgressSince = time.Now() // just started
	if got := processDegradedState(proc, time.Now()); got != "" {
		t.Errorf("expected \"\" (not yet wedged), got %q", got)
	}
}

// TestProcessDegradedState_MCPInitTimeoutDisabled verifies the wedged check
// is skipped entirely when MCPInitTimeout is disabled (<=0), matching the
// cold-budget gating used elsewhere in this package.
func TestProcessDegradedState_MCPInitTimeoutDisabled(t *testing.T) {
	proc := newTestSharedProcess()
	proc.config.MCPInitTimeout = 0
	proc.mcpInitInProgress.Store(true)
	proc.mcpInitInProgressSince = time.Now().Add(-time.Hour)
	if got := processDegradedState(proc, time.Now()); got != "" {
		t.Errorf("expected \"\" with MCPInitTimeout disabled, got %q", got)
	}
}

// TestProcessDegradedState_ExcludesBusy is the mitto-13n.3 acceptance
// criterion: a process merely busy (high ActiveRPCs) but otherwise healthy
// must NOT be reported degraded — that is momentary load, not a stuck/wedged
// process, and must not produce a user-facing alarm.
func TestProcessDegradedState_ExcludesBusy(t *testing.T) {
	proc := newTestSharedProcess()
	proc.activeRPCs.Store(int32(auxSessionCreateBusyRPCThreshold + 5))
	if proc.IsSaturated() || proc.MCPInitTimedOut() {
		t.Fatal("test setup: busy process must not otherwise be saturated/mcp-init-timed-out")
	}
	if got := processDegradedState(proc, time.Now()); got != "" {
		t.Errorf("expected \"\" for a merely-busy process, got %q", got)
	}
}

// TestProcessDegradedState_PriorityOrder verifies IsSaturated() is checked
// first, matching Tier 5's own priority when a process satisfies more than
// one signal simultaneously.
func TestProcessDegradedState_PriorityOrder(t *testing.T) {
	proc := newTestSharedProcess()
	proc.mcpInitTimedOut.Store(true)
	for i := 0; i < sessionSaturationTimeoutThreshold; i++ {
		proc.recordRPCTimeout()
	}
	if got := processDegradedState(proc, time.Now()); got != "process_saturated" {
		t.Errorf("expected process_saturated to take priority, got %q", got)
	}
}

// --- updateDegradedState / dropDegradedStateSilently ---------------------

func newTestDegradedManager() (*ACPProcessManager, *[]struct {
	uuid, state string
	degraded    bool
}) {
	m := &ACPProcessManager{logger: newTestLogger()}
	var calls []struct {
		uuid, state string
		degraded    bool
	}
	var mu sync.Mutex
	m.onDegraded = func(workspaceUUID, state string, degraded bool) {
		mu.Lock()
		defer mu.Unlock()
		calls = append(calls, struct {
			uuid, state string
			degraded    bool
		}{workspaceUUID, state, degraded})
	}
	return m, &calls
}

func TestUpdateDegradedState_FiresOnceOnEdgeAndIsIdempotent(t *testing.T) {
	m, calls := newTestDegradedManager()
	m.updateDegradedState("ws-1", "process_saturated")
	m.updateDegradedState("ws-1", "process_saturated") // steady state: no-op
	m.updateDegradedState("ws-1", "process_saturated") // steady state: no-op

	if len(*calls) != 1 {
		t.Fatalf("expected exactly 1 call, got %d: %+v", len(*calls), *calls)
	}
	got := (*calls)[0]
	if got.uuid != "ws-1" || got.state != "process_saturated" || !got.degraded {
		t.Errorf("unexpected call: %+v", got)
	}
}

func TestUpdateDegradedState_RecoveryEdgeFires(t *testing.T) {
	m, calls := newTestDegradedManager()
	m.updateDegradedState("ws-1", "mcp_init_gated")
	m.updateDegradedState("ws-1", "") // recovery edge

	if len(*calls) != 2 {
		t.Fatalf("expected exactly 2 calls, got %d: %+v", len(*calls), *calls)
	}
	if (*calls)[1].degraded || (*calls)[1].state != "" {
		t.Errorf("expected recovery call with degraded=false and state=\"\", got %+v", (*calls)[1])
	}
}

func TestUpdateDegradedState_ReasonChangeRefiresWithoutRecoveryEdge(t *testing.T) {
	// A change from one degraded reason to another (e.g. saturation trips
	// while already mcp-init-gated) is itself an edge and must re-fire with
	// degraded=true, not a spurious recovery.
	m, calls := newTestDegradedManager()
	m.updateDegradedState("ws-1", "mcp_init_gated")
	m.updateDegradedState("ws-1", "process_saturated")

	if len(*calls) != 2 {
		t.Fatalf("expected exactly 2 calls, got %d: %+v", len(*calls), *calls)
	}
	if !(*calls)[1].degraded || (*calls)[1].state != "process_saturated" {
		t.Errorf("expected second call degraded=true state=process_saturated, got %+v", (*calls)[1])
	}
}

func TestDropDegradedStateSilently_NoRecoveryEdge(t *testing.T) {
	m, calls := newTestDegradedManager()
	m.updateDegradedState("ws-1", "process_saturated")
	m.dropDegradedStateSilently("ws-1")

	if len(*calls) != 1 {
		t.Fatalf("expected exactly 1 call (drop must NOT fire a recovery edge), got %d: %+v", len(*calls), *calls)
	}

	// A subsequent healthy tick must be able to re-fire the degraded edge
	// (the map entry is truly gone, not just suppressed).
	m.updateDegradedState("ws-1", "process_saturated")
	if len(*calls) != 2 {
		t.Fatalf("expected a fresh degraded edge to fire after silent drop, got %d calls: %+v", len(*calls), *calls)
	}
}

func TestDropDegradedStateSilently_UntrackedIsNoOp(t *testing.T) {
	m, calls := newTestDegradedManager()
	m.dropDegradedStateSilently("ws-untracked")
	if len(*calls) != 0 {
		t.Fatalf("expected no calls for an untracked workspace, got %d: %+v", len(*calls), *calls)
	}
}

// TestStopProcess_DropsDegradedStateSilently pins the cleanup contract: every
// path that removes a process funnels through StopProcess, which must drop the
// tracked degraded state so it cannot leak into a later process for the same
// workspace UUID — and must do so without firing a recovery edge (the stop
// paths users care about broadcast their own toast).
func TestStopProcess_DropsDegradedStateSilently(t *testing.T) {
	workspaceUUID := "ws-stopped"
	m := newTestGCManager(
		func() map[string][]conversation.SessionInfo { return map[string][]conversation.SessionInfo{} },
		func(string) {},
	)
	var mu sync.Mutex
	var degradedCalls int
	m.onDegraded = func(string, string, bool) {
		mu.Lock()
		defer mu.Unlock()
		degradedCalls++
	}
	m.mu.Lock()
	m.processes[workspaceUUID] = newTestSharedProcess()
	m.mu.Unlock()

	m.updateDegradedState(workspaceUUID, "process_saturated")
	m.StopProcess(workspaceUUID)

	mu.Lock()
	defer mu.Unlock()
	if degradedCalls != 1 {
		t.Fatalf("expected exactly 1 onDegraded call (entry edge only), got %d", degradedCalls)
	}
	m.degradedMu.Lock()
	_, tracked := m.degradedState[workspaceUUID]
	m.degradedMu.Unlock()
	if tracked {
		t.Error("expected degradedState entry to be dropped by StopProcess")
	}
}

// --- GC integration: onDegraded vs. onHealthRecycled ordering/dedup ------

// TestGCTier5_OnDegradedFiresEvenWhenBusy verifies the pre-recycle signal
// fires BEFORE the idle safety gates: a saturated-but-busy process (which
// Tier 5 will NOT recycle) must still surface degraded=true, closing the
// mitto-13n.3 visibility gap.
func TestGCTier5_OnDegradedFiresEvenWhenBusy(t *testing.T) {
	workspaceUUID := "ws-saturated-busy"
	proc := newTestSharedProcess()
	for i := 0; i < sessionSaturationTimeoutThreshold; i++ {
		proc.recordRPCTimeout()
	}
	proc.activeRPCs.Store(1) // in-flight RPC: fails the Tier 5 idle safety gate

	m := newTestGCManager(
		func() map[string][]conversation.SessionInfo { return map[string][]conversation.SessionInfo{} },
		func(string) {},
	)
	m.mu.Lock()
	m.processes[workspaceUUID] = proc
	m.mu.Unlock()
	m.gcConfig.MemoryRecycleThreshold = gcTier4Threshold
	installFixedMemorySample(m, gcTier4Threshold/2)

	var mu sync.Mutex
	var degradedCalls int
	var recycledCalls int
	m.onDegraded = func(uuid, state string, degraded bool) {
		mu.Lock()
		defer mu.Unlock()
		degradedCalls++
		if uuid != workspaceUUID || state != "process_saturated" || !degraded {
			t.Errorf("unexpected onDegraded call: uuid=%q state=%q degraded=%v", uuid, state, degraded)
		}
	}
	m.onHealthRecycled = func(string, string, int, int) {
		mu.Lock()
		defer mu.Unlock()
		recycledCalls++
	}

	m.RunGCOnce()

	mu.Lock()
	defer mu.Unlock()
	if degradedCalls != 1 {
		t.Fatalf("expected onDegraded to fire once for the busy-but-saturated process, got %d", degradedCalls)
	}
	if recycledCalls != 0 {
		t.Fatalf("expected onHealthRecycled NOT to fire while busy, got %d", recycledCalls)
	}
	m.mu.RLock()
	_, exists := m.processes[workspaceUUID]
	m.mu.RUnlock()
	if !exists {
		t.Error("busy process must not have been recycled")
	}
}

// TestGCTier5_RecycleDropsDegradedStateWithoutDoubleNotification verifies
// the mitto-13n.3 anti-double-toast contract: once a degraded process is
// actually recycled (idle), onDegraded's recovery edge (degraded=false) must
// NOT fire — onHealthRecycled already covers that transition with its own
// dedicated toast.
func TestGCTier5_RecycleDropsDegradedStateWithoutDoubleNotification(t *testing.T) {
	workspaceUUID := "ws-saturated-idle-dedup"
	proc := newTestSharedProcess()
	for i := 0; i < sessionSaturationTimeoutThreshold; i++ {
		proc.recordRPCTimeout()
	}

	m := newTestGCManager(
		func() map[string][]conversation.SessionInfo { return map[string][]conversation.SessionInfo{} },
		func(string) {},
	)
	m.mu.Lock()
	m.processes[workspaceUUID] = proc
	m.mu.Unlock()
	m.gcConfig.MemoryRecycleThreshold = gcTier4Threshold
	installFixedMemorySample(m, gcTier4Threshold/2)

	var mu sync.Mutex
	var degradedCalls, recycledCalls int
	m.onDegraded = func(string, string, bool) {
		mu.Lock()
		defer mu.Unlock()
		degradedCalls++
	}
	m.onHealthRecycled = func(string, string, int, int) {
		mu.Lock()
		defer mu.Unlock()
		recycledCalls++
	}

	m.RunGCOnce()

	mu.Lock()
	defer mu.Unlock()
	if recycledCalls != 1 {
		t.Fatalf("expected the idle saturated process to be recycled once, got %d", recycledCalls)
	}
	if degradedCalls != 1 {
		t.Fatalf("expected exactly 1 onDegraded call (the entry edge only, no recovery double-toast), got %d", degradedCalls)
	}

	m.degradedMu.Lock()
	_, tracked := m.degradedState[workspaceUUID]
	m.degradedMu.Unlock()
	if tracked {
		t.Error("expected degradedState entry to be dropped after recycle")
	}
}
