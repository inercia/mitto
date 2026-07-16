package acpproc

// Tests for the MCP-init extended budget policy (mitto-8ul.1). The helper
// coldMCPBudget() decides whether NewSession/LoadSession should widen its
// per-attempt and total deadlines to give a cold agent time to finish its
// internal MCP-server handshake before Mitto times out.

import (
	"context"
	"errors"
	"fmt"
	"strings"
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

// TestWarmOnceBarrier_OnlyOneHolderThroughWarmup models the mitto-54k.3
// warm-once barrier logic used inside NewSession/LoadSession: given N
// concurrent COLD callers racing the barrier, only ONE holds the gate
// through the warm-up window and the rest proceed fast once mcpInitDone
// latches. The real ACP subprocess is not exercised here — we replay the
// entry condition (MCPInitTimeout>0 && !mcpInitDone) + acquire +
// post-acquire re-check exactly as the production code does.
func TestWarmOnceBarrier_OnlyOneHolderThroughWarmup(t *testing.T) {
	p := newTestProcessWithGate()
	p.config.MCPInitTimeout = 240 * time.Second // cold-barrier entry enabled

	const N = 8
	var holders atomic.Int32   // callers that were still cold on acquire (kept the gate)
	var bypassers atomic.Int32 // callers that found the process warm after acquire
	var skipped atomic.Int32   // callers that saw warm before ever taking the gate

	var wg sync.WaitGroup
	wg.Add(N)
	for i := 0; i < N; i++ {
		go func() {
			defer wg.Done()

			// (a) barrier ENTRY condition: skip when NOT cold (inverse of the
			// raw cold predicate MCPInitTimeout > 0 && !mcpInitDone).
			if p.config.MCPInitTimeout <= 0 || p.mcpInitDone.Load() {
				skipped.Add(1)
				return
			}

			// (b) acquire the gate.
			release, err := p.acquireColdStartGate(context.Background())
			if err != nil {
				t.Errorf("acquire failed: %v", err)
				return
			}

			// (c) post-acquire warmth re-check.
			if p.mcpInitDone.Load() {
				// Barrier holder warmed the process while we waited: release
				// IMMEDIATELY and proceed as a warm caller.
				release()
				bypassers.Add(1)
				return
			}

			// Holder path: hold the gate through the warm-up, then latch
			// mcpInitDone BEFORE the deferred release fires (mirroring the
			// production ordering where mcpInitDone.Store(true) at line ~1507
			// / ~1732 happens before `defer release()` runs).
			holders.Add(1)
			time.Sleep(30 * time.Millisecond) // simulate the MCP-init handshake
			p.mcpInitDone.Store(true)
			release()
		}()
	}
	wg.Wait()

	if got := holders.Load(); got != 1 {
		t.Fatalf("expected exactly 1 barrier holder, got %d (bypassers=%d, skipped=%d)",
			got, bypassers.Load(), skipped.Load())
	}
	if got := holders.Load() + bypassers.Load() + skipped.Load(); got != N {
		t.Fatalf("caller accounting mismatch: %d != %d", got, N)
	}
	if !p.mcpInitDone.Load() {
		t.Fatal("expected mcpInitDone to be latched after the holder warmed the process")
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

// TestColdMCPBudget_ExtendedBudgetFundsRetry is the mitto-54k.12 reproduction.
//
// The bug: coldMCPBudget returns (MCPInitTimeout, MCPInitTimeout, true) on a
// cold process — i.e. totalBudget == perAttemptBudget. NewSession's retry loop
// derives budgetCtx from totalBudget and per-attempt subcontexts from
// perAttemptBudget; when attempt 1 hits the agent's own internal MCP-init
// deadline (~240s on Auggie, evidence: agent_internal_deadline=true on all 5
// WARN "SharedACPProcess.NewSession failed" events on 2026-07-14), attempt 1
// returns at ~perAttemptBudget with context.DeadlineExceeded. The loop then
// advances to attempt 2, the attempt>1 budget guard fires with
// budgetCtx.Err()==DeadlineExceeded, and emits the observed wedge log:
//
//	session/new: shared handshake budget exhausted before attempt 2
//	(240002ms elapsed, per-attempt budget 240000ms, extended_mcp=true);
//	no budget left to retry: context deadline exceeded
//
// Root cause is arithmetic: the retry policy documented in
// sessionCreateTotalBudget's comment ("attempt 1 (~25s) + attempt 2 (~25s)")
// requires the invariant totalBudget >= 2 × perAttemptBudget. coldMCPBudget's
// extended-mode return violates it by design (total == perAttempt), which
// silently degrades cold NewSession to a single-attempt policy on exactly the
// case that most needs a retry — a cold, saturated agent.
//
// This is the extended-MCP-budget analogue of TestAuxSessionCreateBudgetFundsRetry
// (mitto-54k.11). Same invariant, different budget source. Pure math — no ACP
// process needed.
//
// With today's coldMCPBudget the test FAILS (that is the reproduction). The
// Fix phase will restore the invariant, options recorded on the bead:
//
//	A. Widen extended totalBudget = MCPInitTimeout × N attempts (~480–720s).
//	B. Force effectiveMaxAttempts=1 when extendedBudget==true (preferred:
//	   least regression, matches empirical reality — the retry is already
//	   impossible in the extended-MCP case; also fixes the misleading
//	   "before attempt 2" log wording).
//	C. Hybrid: widen totalBudget by 1.5×, cap retries to 2 in extended mode.
func TestColdMCPBudget_ExtendedBudgetFundsRetry(t *testing.T) {
	p := &SharedACPProcess{}
	p.config.MCPInitTimeout = 240 * time.Second

	perAttempt, total, extended := p.coldMCPBudget(true /*hasMCPServers*/)
	if !extended {
		t.Fatalf("preconditions: expected extended=true on cold process, got false")
	}

	// Post-fix invariant (mitto-54k.12): the extended-MCP path must NOT emit the
	// misleading "shared handshake budget exhausted before attempt 2" wedge log
	// after attempt 1 legally drains its full per-attempt budget hitting the
	// agent's own MCP-init deadline. The retry loop can satisfy this in either
	// of two shapes:
	//
	//   A. Widen totalBudget so remainingAfterAttempt1 >= perAttempt — the fail-
	//      fast predicate lets attempt 2 proceed on the same wall-clock window.
	//   B. Cap effectiveMaxAttempts=1 in extendedBudget mode — the loop never
	//      reaches the attempt-2 boundary at all (see NewSession, mitto-54k.12
	//      fix: `if extendedBudget { effectiveMaxAttempts = 1 }`).
	//
	// This test accepts EITHER shape: it fails only when both are absent (the
	// pre-fix state that emits the wedge).
	remainingAfterAttempt1 := total - perAttempt
	bail, reason := shouldFailFastCreateAttempt(
		2,                      // attempt
		false,                  // not saturated (isolate the budget path)
		true,                   // ctx has deadline (budgetCtx always deadlined per mitto-8d7)
		remainingAfterAttempt1, // remaining wall-clock in budgetCtx
		perAttempt,             // per-attempt budget the loop wants to fund
	)
	shapeA := total >= 2*perAttempt && !bail
	shapeB := effectiveMaxAttemptsForBudget(extended) == 1
	if !shapeA && !shapeB {
		t.Errorf("extended coldMCPBudget total (%v) drained by one full per-attempt "+
			"(%v) leaves only %v — shouldFailFastCreateAttempt bails at attempt 2 "+
			"(reason=%q) AND effectiveMaxAttemptsForBudget(extended=true)=%d != 1. "+
			"Neither the option-A (widen totalBudget) nor option-B (cap to 1 attempt) "+
			"fix is in place; cold NewSession is degraded to a single-attempt policy "+
			"that emits the misleading \"shared handshake budget exhausted before "+
			"attempt 2\" wedge log observed in the mitto-54k.12 evidence (2026-07-14, "+
			"5 events @ 240000–240002ms elapsed).",
			total, perAttempt, remainingAfterAttempt1, reason,
			effectiveMaxAttemptsForBudget(extended))
	}

	t.Logf("extended coldMCPBudget: perAttempt=%v, total=%v, remaining-after-attempt1=%v, "+
		"bail=%v, effectiveMaxAttempts=%d, shapeA=%v, shapeB=%v",
		perAttempt, total, remainingAfterAttempt1, bail,
		effectiveMaxAttemptsForBudget(extended), shapeA, shapeB)
}

// TestColdMCPBudget_ExtendedRetryLoopAllowsAttempt2 is a second, higher-
// fidelity reproduction of mitto-54k.12 that exercises the actual budget/retry
// arithmetic used inside NewSession (shared_acp_process.go:1480-1554) against
// a compressed timescale.
//
// Timescale is compressed 1000× (MCPInitTimeout=240ms rather than 240s) so the
// test runs in < 1s while preserving the arithmetic that produces the wedge.
// The RPC "attempt" here is a select on the per-attempt context — no real
// agent is invoked; we simulate the exact "attempt 1 drains its full
// perAttemptBudget" pattern observed in the field
// (agent_internal_deadline=true, rpc_ms≈perAttemptBudget).
//
// The test asserts the CORRECT behaviour after the fix: once attempt 1 legally
// consumes its full per-attempt budget, the retry loop must not fail with the
// "shared handshake budget exhausted before attempt 2" wedge — either because
// the extended budget was widened to fund a real retry (option A), or because
// the retry policy honestly caps to a single attempt with a non-misleading
// error (option B), or a hybrid (option C). All three fixes MUST at minimum
// stop emitting the specific verbatim wedge string that appeared 5× in the
// 2026-07-14 evidence — that log line is the operator-visible symptom.
//
// Today (pre-fix) this test FAILS: it triggers the exact wedge error. After
// any of A/B/C it PASSES.
func TestColdMCPBudget_ExtendedRetryLoopAllowsAttempt2(t *testing.T) {
	p := &SharedACPProcess{}
	p.config.MCPInitTimeout = 240 * time.Millisecond // 1000× compressed

	perAttemptBudget, totalBudget, extendedBudget := p.coldMCPBudget(true)
	if !extendedBudget {
		t.Fatalf("preconditions: expected extendedBudget=true, got false")
	}

	// Mirror NewSession lines 1480-1485: derive a budgetCtx from totalBudget.
	totalStart := time.Now()
	budgetCtx, budgetCancel := context.WithTimeout(context.Background(), totalBudget)
	defer budgetCancel()

	// Mirror NewSession's effectiveMaxAttempts computation (mitto-54k.12 fix at
	// shared_acp_process.go: `if extendedBudget { effectiveMaxAttempts = 1 }`).
	// The retry loop must respect this cap: with extendedBudget=true, the loop
	// runs exactly one attempt and never reaches the attempt-2 boundary guard.
	effectiveMaxAttempts := effectiveMaxAttemptsForBudget(extendedBudget)

	// Attempt 1: mirror lines 1596-1598 (WithTimeout(budgetCtx, perAttemptBudget)),
	// then simulate the agent taking its full internal MCP-init deadline by
	// waiting for the per-attempt context to expire — exactly the field pattern.
	attemptCtx, attemptCancel := context.WithTimeout(budgetCtx, perAttemptBudget)
	<-attemptCtx.Done()
	attemptCancel()

	// Attempt 2 boundary: mirror lines 1541-1554. Only reached if the loop's
	// effectiveMaxAttempts allows a second attempt. With the option-B fix the
	// cap is 1 in extendedBudget mode, so this guard is unreachable and the
	// wedge error is never emitted.
	var wedgeErr error
	for attempt := 2; attempt <= effectiveMaxAttempts; attempt++ {
		if budgetCtx.Err() != nil {
			if errors.Is(budgetCtx.Err(), context.DeadlineExceeded) {
				wedgeErr = fmt.Errorf(
					"session/new: shared handshake budget exhausted before attempt %d "+
						"(%dms elapsed, per-attempt budget %dms, extended_mcp=%t); "+
						"no budget left to retry: %w",
					attempt, time.Since(totalStart).Milliseconds(),
					perAttemptBudget.Milliseconds(), extendedBudget, budgetCtx.Err())
			}
		}
	}

	if wedgeErr != nil && strings.Contains(wedgeErr.Error(), "before attempt 2") {
		t.Errorf("cold NewSession retry loop emits the mitto-54k.12 wedge error "+
			"after attempt 1 legally consumes its extended per-attempt budget: %v — "+
			"either totalBudget must fund attempt 2 (option A), or the retry policy "+
			"must honestly cap to 1 attempt in extendedBudget mode without emitting "+
			"the misleading \"before attempt 2\" log (option B), or both (option C). "+
			"This is the operator-visible symptom observed 5× on 2026-07-14.",
			wedgeErr)
	}
}
