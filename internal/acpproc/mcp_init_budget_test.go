package acpproc

// Tests for the MCP-init extended budget policy (mitto-8ul.1). The helper
// coldMCPBudget() decides whether NewSession/LoadSession should widen its
// per-attempt and total deadlines to give a cold agent time to finish its
// internal MCP-server handshake before Mitto times out.

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestColdMCPBudget_DisabledByDefault(t *testing.T) {
	p := &SharedACPProcess{} // MCPInitTimeout unset = 0
	perAttempt, total, extended := p.coldMCPBudget(true /*hasMCPServers*/)
	if extended {
		t.Fatalf("expected extended=false when MCPInitTimeout=0, got extended=true")
	}
	if perAttempt != sessionCreateAttemptTimeout {
		t.Fatalf("perAttempt=%v, want %v", perAttempt, sessionCreateAttemptTimeout)
	}
	if total != sessionCreateTotalBudget {
		t.Fatalf("total=%v, want %v", total, sessionCreateTotalBudget)
	}
}

func TestColdMCPBudget_NoMCPServersStillExtends(t *testing.T) {
	// Under the current design (MCP attached globally, not per session/new),
	// hasMCPServers is not load-bearing: the extended budget applies to every
	// cold session/new so long as MCPInitTimeout > 0. mitto-8ul.1.
	p := &SharedACPProcess{}
	p.config.MCPInitTimeout = 240 * time.Second

	perAttempt, total, extended := p.coldMCPBudget(false /*hasMCPServers*/)
	if !extended {
		t.Fatal("expected extended=true even without hasMCPServers on cold start")
	}
	if perAttempt != 240*time.Second || total != 240*time.Second {
		t.Fatalf("budgets = (%v, %v), want (240s, 240s)", perAttempt, total)
	}
}

func TestColdMCPBudget_ColdWithMCPServersExtends(t *testing.T) {
	p := &SharedACPProcess{}
	p.config.MCPInitTimeout = 240 * time.Second

	perAttempt, total, extended := p.coldMCPBudget(true /*hasMCPServers*/)
	if !extended {
		t.Fatal("expected extended=true for cold session with MCP servers")
	}
	if perAttempt != 240*time.Second {
		t.Fatalf("perAttempt=%v, want 240s", perAttempt)
	}
	if total != 240*time.Second {
		t.Fatalf("total=%v, want 240s", total)
	}
}

func TestColdMCPBudget_WarmProcessRevertsToNormal(t *testing.T) {
	p := &SharedACPProcess{}
	p.config.MCPInitTimeout = 240 * time.Second
	// Simulate the process having completed one successful cold-start session RPC.
	p.mcpInitDone.Store(true)

	perAttempt, total, extended := p.coldMCPBudget(true /*hasMCPServers*/)
	if extended {
		t.Fatalf("expected extended=false once mcpInitDone=true, got extended=true")
	}
	if perAttempt != sessionCreateAttemptTimeout {
		t.Fatalf("perAttempt=%v, want %v", perAttempt, sessionCreateAttemptTimeout)
	}
	if total != sessionCreateTotalBudget {
		t.Fatalf("total=%v, want %v", total, sessionCreateTotalBudget)
	}
}

func TestColdMCPBudget_ReinitAfterFirstSuccessReExtends(t *testing.T) {
	// mitto-29q: an agent that re-runs the MCP handshake on a later session/new
	// (mcpInitInProgress=true) must get the extended budget re-granted even though
	// a prior session already succeeded (mcpInitDone=true).
	p := &SharedACPProcess{}
	p.config.MCPInitTimeout = 240 * time.Second
	p.mcpInitDone.Store(true)
	p.mcpInitInProgress.Store(true)

	perAttempt, total, extended := p.coldMCPBudget(true /*hasMCPServers*/)
	if !extended {
		t.Fatal("expected extended=true when mcpInitInProgress even though mcpInitDone=true")
	}
	if perAttempt != 240*time.Second || total != 240*time.Second {
		t.Fatalf("budgets = (%v, %v), want (240s, 240s)", perAttempt, total)
	}
}

func TestColdMCPBudget_WarmIdleNotInProgressRevertsToNormal(t *testing.T) {
	// After a success closes the window (mcpInitInProgress=false) and no new
	// handshake is running, revert to the normal budget (mitto-29q).
	p := &SharedACPProcess{}
	p.config.MCPInitTimeout = 240 * time.Second
	p.mcpInitDone.Store(true)
	p.mcpInitInProgress.Store(false)

	_, _, extended := p.coldMCPBudget(true)
	if extended {
		t.Fatal("expected extended=false when warm and no handshake in progress")
	}
}

func TestRecommendedLoadTimeout(t *testing.T) {
	p := &SharedACPProcess{}
	p.config.MCPInitTimeout = 240 * time.Second

	// Cold: widen regardless of hasMCPServers hint (Mitto attaches MCP globally).
	if got := p.RecommendedLoadTimeout(true); got != 240*time.Second {
		t.Errorf("cold+mcp: got %v, want 240s", got)
	}
	if got := p.RecommendedLoadTimeout(false); got != 240*time.Second {
		t.Errorf("cold no-mcp-hint: got %v, want 240s", got)
	}
	// Warm: 0.
	p.mcpInitDone.Store(true)
	if got := p.RecommendedLoadTimeout(true); got != 0 {
		t.Errorf("warm: got %v, want 0", got)
	}
	// Warm but a new handshake is in progress: re-widen (mitto-29q).
	p.mcpInitInProgress.Store(true)
	if got := p.RecommendedLoadTimeout(true); got != 240*time.Second {
		t.Errorf("warm+reinit: got %v, want 240s", got)
	}
	p.mcpInitInProgress.Store(false)
	// Disabled: 0.
	p2 := &SharedACPProcess{}
	p2.config.MCPInitTimeout = 0
	if got := p2.RecommendedLoadTimeout(true); got != 0 {
		t.Errorf("disabled: got %v, want 0", got)
	}
}

// --- Cold-start admission gate (mitto-8tb) ---

// newTestProcessWithGate returns a SharedACPProcess with just enough state to
// exercise acquireColdStartGate independently of a real ACP subprocess.
func newTestProcessWithGate() *SharedACPProcess {
	return &SharedACPProcess{coldStartGate: make(chan struct{}, 1)}
}

func TestColdStartGate_AcquireReleaseAllowsSerialCallers(t *testing.T) {
	p := newTestProcessWithGate()

	release1, err := p.acquireColdStartGate(context.Background())
	if err != nil {
		t.Fatalf("first acquire failed: %v", err)
	}
	release1()

	release2, err := p.acquireColdStartGate(context.Background())
	if err != nil {
		t.Fatalf("second acquire after release failed: %v", err)
	}
	release2()
}

func TestColdStartGate_BlocksSecondCallerUntilFirstReleases(t *testing.T) {
	p := newTestProcessWithGate()

	release1, err := p.acquireColdStartGate(context.Background())
	if err != nil {
		t.Fatalf("first acquire failed: %v", err)
	}

	// Second acquire must not complete until release1 fires.
	var acquired atomic.Bool
	done := make(chan struct{})
	go func() {
		release2, err := p.acquireColdStartGate(context.Background())
		if err == nil {
			acquired.Store(true)
			release2()
		}
		close(done)
	}()

	time.Sleep(20 * time.Millisecond)
	if acquired.Load() {
		t.Fatal("second acquire completed before first release — gate not serializing")
	}

	release1()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("second acquire never completed after release")
	}
	if !acquired.Load() {
		t.Fatal("second acquire did not report success")
	}
}

func TestColdStartGate_HonorsCtxCancellationWhileWaiting(t *testing.T) {
	p := newTestProcessWithGate()

	// Hold the gate to force the next acquire to wait.
	release1, err := p.acquireColdStartGate(context.Background())
	if err != nil {
		t.Fatalf("first acquire failed: %v", err)
	}
	defer release1()

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()

	start := time.Now()
	release2, err := p.acquireColdStartGate(ctx)
	if err == nil {
		release2()
		t.Fatal("expected context error while gate held by another caller")
	}
	if time.Since(start) > 500*time.Millisecond {
		t.Fatalf("acquire took too long to honour ctx cancellation: %v", time.Since(start))
	}
}

func TestColdStartGate_NilGateIsNoOp(t *testing.T) {
	// A zero-value SharedACPProcess (no gate) must be safe to call — the RPC
	// paths should just proceed as before.
	p := &SharedACPProcess{}
	release, err := p.acquireColdStartGate(context.Background())
	if err != nil {
		t.Fatalf("nil-gate acquire returned error: %v", err)
	}
	if release == nil {
		t.Fatal("nil-gate acquire returned nil release func")
	}
	release() // must not panic
}

func TestColdStartGate_SerializesUnderConcurrency(t *testing.T) {
	// N goroutines racing the gate must observe strictly serialized entry —
	// only one holder at a time — with all eventually completing.
	p := newTestProcessWithGate()

	var inFlight atomic.Int32
	var maxInFlight atomic.Int32
	var wg sync.WaitGroup
	const N = 8
	wg.Add(N)
	for i := 0; i < N; i++ {
		go func() {
			defer wg.Done()
			release, err := p.acquireColdStartGate(context.Background())
			if err != nil {
				t.Errorf("acquire failed: %v", err)
				return
			}
			now := inFlight.Add(1)
			for {
				prev := maxInFlight.Load()
				if now <= prev || maxInFlight.CompareAndSwap(prev, now) {
					break
				}
			}
			time.Sleep(5 * time.Millisecond)
			inFlight.Add(-1)
			release()
		}()
	}
	wg.Wait()
	if got := maxInFlight.Load(); got != 1 {
		t.Fatalf("gate did not serialize callers: max in-flight = %d, want 1", got)
	}
}

func TestBeginMCPInitWindow_ResetsPerCall(t *testing.T) {
	p := &SharedACPProcess{}
	p.mcpInitTimedOut.Store(true)

	ch := p.beginMCPInitWindow()
	if ch == nil {
		t.Fatal("expected non-nil channel from beginMCPInitWindow")
	}
	if p.mcpInitTimedOut.Load() {
		t.Fatal("expected mcpInitTimedOut to be reset by beginMCPInitWindow")
	}

	// A second call must return a fresh channel (the old one is orphaned so signals
	// from a previous RPC do not affect the new one).
	ch2 := p.beginMCPInitWindow()
	if ch == ch2 {
		t.Fatal("expected beginMCPInitWindow to return a fresh channel per call")
	}
}
