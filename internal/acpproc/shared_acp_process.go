package acpproc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math/rand"
	"os"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/coder/acp-go-sdk"

	mittoAcp "github.com/inercia/mitto/internal/acp"
	"github.com/inercia/mitto/internal/acpproc/acperrors"
	"github.com/inercia/mitto/internal/acpproc/procstart"
	"github.com/inercia/mitto/internal/coldstart"
	"github.com/inercia/mitto/internal/conversation"
	"github.com/inercia/mitto/internal/logging"
	"github.com/inercia/mitto/internal/runner"
)

// traceSessionResponse dumps the full session/new|session/load|session/resume
// response as JSON at INFO level when MITTO_ACP_TRACE_SESSION_RESP=1. Opt-in
// only: this is a wire-level diagnostic for cases where the parsed struct is
// suspiciously empty (e.g. ConfigOptions=[]) and we need to confirm the agent
// actually sent that vs an SDK deserialization gap. No-op when unset or when
// logger is nil. The env is read on every call so it can be flipped without
// restart. Response payloads may contain _meta which is opaque — do not enable
// this in shared/production environments.
func traceSessionResponse(logger *slog.Logger, source string, resp any) {
	if logger == nil {
		return
	}
	if os.Getenv("MITTO_ACP_TRACE_SESSION_RESP") != "1" {
		return
	}
	buf, err := json.Marshal(resp)
	if err != nil {
		logger.Info("ACP session response trace (marshal failed)",
			"source", source, "error", err)
		return
	}
	logger.Info("ACP session response trace",
		"source", source,
		"payload_bytes", len(buf),
		"payload_json", string(buf))
}

const (
	// maxProcessStartRetries is the maximum number of times to retry starting the ACP process.
	maxProcessStartRetries = 3
	// processStartRetryBaseDelay is the initial delay between process start retries.
	processStartRetryBaseDelay = 500 * time.Millisecond
	// processStartRetryMaxDelay is the maximum delay between process start retries.
	processStartRetryMaxDelay = 4 * time.Second
	// processStartRetryJitterRatio is the jitter ratio (±) applied to retry delays.
	processStartRetryJitterRatio = 0.3

	// setSessionModelMaxAttempts is the maximum number of set_model RPC attempts per call.
	// Schedule {20s,15s,8s} totals 43s per caller + jitter (≤1.2s) ≈ 44s (attempt-1 widened
	// for mitto-8qp so large-context warm-up fits). The total stays within the contention
	// bound covered by setModelAsyncCallerBudget (see TestSetModelAsyncBudgetMath).
	setSessionModelMaxAttempts = 3
	// setSessionModelRetryBaseDelay is the base backoff between set_model retry attempts.
	setSessionModelRetryBaseDelay = 300 * time.Millisecond
	// setSessionModelRetryJitterRatio is the maximum jitter as a fraction of the base delay
	// added to each retry backoff. Jitter in [0, base×ratio) de-correlates concurrent callers
	// that would otherwise retry in lock-step (mitto-f7q, Option 3).
	// With ratio=0.5: attempt-2 delay ∈ [300ms, 450ms), attempt-3 ∈ [600ms, 750ms).
	// Total per-caller worst-case: sum(schedule) + ~1.2s jitter ≈ 44s.
	setSessionModelRetryJitterRatio = 0.5

	// sessionCreateMaxAttempts is the maximum number of session/new RPC attempts per call.
	// Mirrors set_model's bounded-retry policy (mitto-4no7, parity with mitto-f7q).
	sessionCreateMaxAttempts = 3
	// sessionCreateAttemptTimeout is the per-attempt deadline for session/new RPCs.
	// Keeps the documented widened create deadline (was sessionCreationRPCTimeout=25s,
	// mitto-63o8) as a FRESH per-attempt budget so a single slow create is not regressed.
	sessionCreateAttemptTimeout = 25 * time.Second
	// sessionCreateRetryBaseDelay is the base backoff between session/new retry attempts.
	sessionCreateRetryBaseDelay = 300 * time.Millisecond
	// sessionCreateRetryJitterRatio is the max jitter as a fraction of the base delay added
	// to each retry backoff, de-correlating concurrent callers (mitto-4no7, mirrors set_model).
	// With ratio=0.5: attempt-2 delay ∈ [300ms,450ms), attempt-3 ∈ [600ms,750ms).
	sessionCreateRetryJitterRatio = 0.5
	// sessionCreateTotalBudget caps the wall-clock time of the entire NewSession retry
	// sequence (mitto-8d7). A deadline-less caller previously let the loop burn the full
	// sessionCreateMaxAttempts × sessionCreateAttemptTimeout (~75s) on a hung transport.
	// At 60s the loop completes attempt 1 (~25s) and attempt 2 (~25s), then bails before
	// attempt 3 once the remaining budget can no longer fund a full per-attempt timeout —
	// bounding the worst case to ~50s while never extending a caller's own deadline.
	sessionCreateTotalBudget = 60 * time.Second
	// auxSessionCreateBudget is the wall-clock budget the ACPProcessManager gives to a
	// single call to SharedACPProcess.NewSession when creating an auxiliary session
	// (title-gen, mcp-check, follow-up, etc.). The context is derived from m.ctx —
	// NOT the caller's ctx — so a slow prior same-key create cannot drain it
	// (mitto-rlk). This budget MUST be large enough to fund at least two full
	// per-attempt session/new RPCs (2 × sessionCreateAttemptTimeout), otherwise a
	// first attempt that hits the agent's own internal 25 s deadline leaves
	// insufficient headroom for a retry and shouldFailFastCreateAttempt bails before
	// attempt 2 — the mitto-54k.11 wedge. TestAuxSessionCreateBudgetFundsRetry pins
	// this invariant. Set to sessionCreateTotalBudget for parity with the underlying
	// NewSession retry sequence.
	auxSessionCreateBudget = sessionCreateTotalBudget

	// setModelAsyncCallerBudget is the context timeout given to the background goroutine
	// that performs the aux-session model switch asynchronously (mitto-f7q, Option 4).
	// Budget reasoning: the capacity-1 setModelSem may be held by up to ~3 concurrent callers,
	// each taking at most ~44s (schedule {20s,15s,8s} + jitter). 90s covers the EXPECTED
	// contention at server wakeup — (N-2)×perCallerMax with N=4, i.e. 2×~44s ≈ 88s ≤ 90s
	// (see TestSetModelAsyncBudgetMath) — while avoiding an indefinite hang if the process
	// is unhealthy. m.ctx cancels on manager shutdown as a hard backstop.
	setModelAsyncCallerBudget = 90 * time.Second

	// auxModelSwitchStartupJitter is the maximum random startup delay applied to each
	// async aux-session set_model goroutine before it enters the budget context window
	// (mitto-xicp). When prewarmAuxiliarySessions fires all 4 purposes in parallel, each
	// spawns an async model-set goroutine at nearly the same instant; without this jitter
	// they all race onto the capacity-1 setModelSem simultaneously. With a 10 s jitter
	// window the goroutines are de-staggered so later arrivals are still well within the
	// 90 s setModelAsyncCallerBudget, eliminating the "context deadline exceeded" failures
	// observed during cold-process wakeup.
	//
	// Widened from 5 s → 10 s (mitto-13ck.1): evidence showed first-attempt 8 s hangs even
	// with 5 s de-stagger, because a cold process may need more warm-up time before it can
	// serve model-switch RPCs reliably. Wider spread reduces simultaneous pressure on the
	// semaphore during the critical post-Initialize warm-up window.
	//
	// This mirrors the child-session de-stagger pattern (constraintModelSwitchChildStartupJitter
	// in internal/conversation/bgsession_config.go, introduced for mitto-x4e). The jitter
	// waits on m.ctx — not the budget context — so it does NOT consume the 90 s budget.
	// Do NOT increase the sum of setSessionModelAttemptTimeouts beyond the contention bound
	// enforced by TestSetModelAsyncBudgetMath (≈43.8s at 4 concurrent callers).
	auxModelSwitchStartupJitter = 10 * time.Second

	// processInitializeAttemptTimeout is the per-attempt deadline for the ACP Initialize
	// handshake in doStartProcess. Bounding this prevents dead sessions from hanging the
	// full SDK-internal 60 s control timeout (DEFAULT_CONTROL_REQUEST_TIMEOUT) on each
	// attempt. The existing conn.Done()/processDone watcher cancels initCtx on detected
	// crashes; this timeout is the backstop for cases where neither signal arrives (e.g.
	// a live-but-unresponsive process with open pipes).
	//
	// 25 s: generous for healthy cold starts (agent typically initialises in < 10 s) while
	// cutting dead-session total retry tail from ~180 s (3×60 s) to ~90 s (3×25 s + backoffs).
	// Do NOT increase toward 60 s — that defeats the purpose. (mitto-13ck.2)
	processInitializeAttemptTimeout = 25 * time.Second

	// sessionSaturationTimeoutThreshold is the number of consecutive NewSession/
	// LoadSession RPC timeouts after which the shared process is treated as
	// saturated/hung (mitto-13ck.2). Subsequent start/resume RPCs then fail fast
	// with a clear deadline-classified error instead of each independently draining
	// the full retry budget on an unresponsive process. A single successful RPC
	// resets the counter.
	sessionSaturationTimeoutThreshold = 3
	// sessionSaturationCooldownBase is the initial cooldown duration after the first
	// saturation trip. The cooldown doubles each time a post-cooldown probe also times
	// out (escalating saturation, mitto-13ck.2): level 1 → 30s, level 2 → 60s,
	// level 3 → 120s, capped at sessionSaturationCooldownMax. A successful RPC resets
	// the level to 0, reverting to the base for the next saturation event.
	sessionSaturationCooldownBase = 30 * time.Second
	// sessionSaturationCooldownMax is the upper bound on the escalating cooldown
	// (mitto-13ck.2). At this cap a spaced-out series of failures can drain at most
	// ~325s (one ~25s probe + 5min cooldown) per event rather than the unbounded tail
	// of the pre-fix flat-cooldown design.
	sessionSaturationCooldownMax = 5 * time.Minute

	// confirmedDegradedLevel is the minimum saturationLevel at which a process is
	// considered "confirmed degraded" (mitto-1h0): it has tripped saturation, served
	// its cooldown, run a single-attempt probe, and that probe ALSO timed out — i.e.
	// it has demonstrably failed to self-heal. Used by IsConfirmedDegraded() to gate
	// the GC's non-idle recycle tier (Tier 6), which recycles even a busy process
	// once this bar is met.
	confirmedDegradedLevel = 2

	// Rate/rolling-window saturation trigger (mitto-5eq). The consecutive-timeout
	// path above only trips after N *back-to-back* full RPC deadlines; any interspersed
	// success zeroes the counter and budget-exhaustion bails in shouldFailFastCreateAttempt
	// intentionally skip recordRPCTimeout — so a shared process that fails intermittently
	// (e.g. 30-50% of session/new + set_model RPCs deadline over 5-10 minutes, but ~2000
	// unrelated ACP events keep succeeding in between) never accumulates enough consecutive
	// timeouts to trip, and the GC's Tier 5/6 recycle tiers stay inert. Observed 2026-07-06
	// 16:34–16:42: 38 context-deadlines, 9 NewSession + 17 SetSessionModel failures, ~10 min
	// aux-session starvation → ZERO recycles.
	//
	// The rate trigger complements (does not replace) the consecutive fast path by counting
	// full-deadline timeouts AND budget-exhaustion bails against successes in a bounded
	// sliding window. Bookkeeping is bucketed (fixed-size ring) so cost is O(1) per record
	// and O(bucketCount) per evaluate, with no unbounded growth. All state is guarded by
	// the existing saturationMu — no second lock is introduced.
	//
	// saturationWindowDuration is the sliding-window length. 5 min is chosen to match the
	// upper end of sessionSaturationCooldownMax and the observed incident timescale: a
	// process that fails ≥50% of its control-plane RPCs for 5 minutes is not going to
	// self-heal in another 5 minutes, but we still recycle no more aggressively than the
	// existing max cooldown.
	saturationWindowDuration = 5 * time.Minute
	// saturationWindowBucketCount is the number of ring-buffer buckets covering
	// saturationWindowDuration. 10 → 30-second buckets, granular enough that aging is
	// smooth on the same order as sessionSaturationCooldownBase (30s) but small enough that
	// aggregation stays trivially cheap. Must be > 0 and divide the window duration
	// cleanly for bucket alignment to be exact.
	saturationWindowBucketCount = 10
	// saturationWindowMinSamples is the minimum total sample count (timeouts + bails +
	// successes) required inside the window before the rate trigger can fire. This is the
	// primary false-positive guard: a healthy process that happens to see 1 timeout in an
	// otherwise-empty window (1/1 = 100%) must NOT trip. Set to 8 so a single burst has to
	// clearly dominate ordinary traffic before the trigger arms.
	saturationWindowMinSamples = 8
	// saturationWindowFailRatio is the (timeouts + bails) / (timeouts + bails + successes)
	// threshold at which the rate trigger fires. 0.5 (50%) is well outside any plausible
	// steady-state healthy baseline (real deployments show <5% timeouts) yet captures the
	// intermittent-degradation regime the incident exhibited (~40-60% failed control-plane
	// RPCs interleaved with successes). Paired with saturationWindowMinSamples this
	// preserves the "no false positives on light traffic" acceptance criterion.
	saturationWindowFailRatio = 0.5

	// Note: Runtime restart constants (maxProcessRestarts, processRestartWindow,
	// processRestartBaseDelay, processRestartMaxDelay) are now defined in
	// internal/acp/errors.go as shared constants (mittoAcp.MaxACPRestarts, mittoAcp.ACPRestartWindow,
	// mittoAcp.ACPRestartBaseDelay, mittoAcp.ACPRestartMaxDelay) to ensure consistent behavior between
	// SharedACPProcess and conversation.BackgroundSession.
)

// setSessionModelAttemptTimeouts is the per-attempt deadline schedule for set_model RPCs
// (mitto-f7q; attempt-1 widened for mitto-8qp). Attempt-1 is sized above the genuine
// warm-up latency of large-context models (e.g. claude-sonnet-5-0-500k, observed >12s):
// the prior 12s attempt-1 was smaller than that latency, so every attempt of the
// then-shrinking 12/8/5 schedule timed out with "context deadline exceeded" even though
// ~60-90s of the outer setModelAsyncCallerBudget (90s) sat unused (mitto-8qp). Later
// attempts shrink to keep the total bounded so setModelSem contention stays covered by the
// async budget. The array length is tied to setSessionModelMaxAttempts at compile time.
// Do NOT let the sum exceed the contention bound enforced by TestSetModelAsyncBudgetMath
// (≈43.8s at 4 concurrent callers) or setModelAsyncCallerBudget (90s) math stops holding.
var setSessionModelAttemptTimeouts = [setSessionModelMaxAttempts]time.Duration{
	20 * time.Second, // attempt 1: sized for large-context model warm-up (mitto-8qp)
	15 * time.Second, // attempt 2: standard retry
	8 * time.Second,  // attempt 3: final, minimal budget
}

// auxStartupJitter returns a random duration in [0, max) to de-stagger concurrent
// async aux-session model-set goroutines that would otherwise all hit the capacity-1
// setModelSem at the same instant (mitto-xicp). Returns 0 if max ≤ 0.
// Mirrors childStartupJitter in internal/conversation/bgsession_config.go (mitto-x4e).
func auxStartupJitter(max time.Duration) time.Duration {
	if max <= 0 {
		return 0
	}
	return time.Duration(rand.Int63n(int64(max)))
}

// SharedACPProcessConfig holds configuration for creating a SharedACPProcess.
type SharedACPProcessConfig struct {
	// WorkspaceUUID is the unique identifier for the workspace this process belongs to.
	// Used for PID file tracking to detect orphaned processes on startup.
	WorkspaceUUID string
	// ACPCommand is the shell command to start the ACP server process.
	ACPCommand string
	// ACPCwd is the working directory for the ACP server process itself.
	ACPCwd string
	// ACPServer is the name of the ACP server (for logging).
	ACPServer string
	// WorkingDir is the workspace's project directory (e.g., /Users/.../myproject).
	// Used as the cwd for auxiliary sessions so the agent discovers MCP servers.
	WorkingDir string
	// Env is a map of environment variables to set when starting the ACP server.
	// These are merged with the current environment (server-specific vars take precedence).
	// Comes from the ACP server definition in settings.json.
	Env map[string]string
	// Runner is an optional restricted runner for sandboxed execution.
	Runner *runner.Runner
	// Logger for process-level logging.
	Logger *slog.Logger
	// CanRestartGlobal is an optional callback that checks the global (cross-workspace)
	// restart rate limiter. When set, Restart() checks this before proceeding.
	// Returns true if restart is globally allowed, false to block.
	CanRestartGlobal func() bool
	// RecordRestart is an optional callback to record a restart in the global tracker.
	RecordRestart func()
	// MCPInitTimeout is the extended per-attempt/total budget granted to the very
	// first session/new (and session/load) on a cold shared ACP process when the
	// request carries MCP servers. Zero disables the extended budget and the normal
	// sessionCreateAttemptTimeout/sessionCreateTotalBudget are used. See
	// SessionConfig.ParseMcpInitTimeout for the rationale (mitto-8ul.1).
	MCPInitTimeout time.Duration
	// OnMCPInitProgress is called once per MCP-init handshake episode (re-armed
	// after each successful session RPC), from the stderr-monitor goroutine, when
	// the agent reports it is blocked waiting for MCP servers to initialize.
	// Used by the web layer to emit an "MCP initializing" UI notification (mitto-8ul.1).
	// Agents like Auggie re-run the MCP handshake on every session/new, so this
	// callback fires again on subsequent per-session re-handshakes (mitto-29q).
	// Optional.
	OnMCPInitProgress func()
	// OnMCPInitTimeout is called at most once when the agent reports its internal
	// MCP-init wait has timed out. The pending session/new should then be aborted with
	// an actionable error rather than waiting for the RPC deadline to elapse
	// (mitto-8ul.1). The []string argument carries the names of the MCP servers the
	// agent reported as timed out on the same stderr chunk, or nil if the chunk
	// carried no per-server status line (mitto-m8nx AC2). Optional.
	OnMCPInitTimeout func(servers []string)
	// StderrPatterns holds per-agent compiled stderr regex patterns (mitto-k6h).
	// Nil means only the hardcoded baseline crash patterns apply. Compiled once
	// by the caller from the agent's metadata.yaml. Kept as a pointer to
	// procstart.CompiledStderrPatterns (leaf subpackage) so acpproc does NOT
	// depend on internal/agents and internal/conversation can consume the same
	// type without an import cycle (mitto-iuw2).
	StderrPatterns *procstart.CompiledStderrPatterns

	// AgentDefaultEnv holds per-agent default environment variables declared in
	// the agent's metadata.yaml (defaults.env, e.g.
	// NODE_OPTIONS=--max-old-space-size=N; mitto-6dur). Resolved once by the
	// caller (mirrors StderrPatterns above) and layered by
	// procstart.BuildACPProcessEnv below Env (an explicit acp_servers[].env
	// entry still wins). Kept as a separate field rather than folded into Env
	// so it is NOT part of sharedProcessConfigMatchesWorkspace's reuse-key
	// comparison — folding it into Env would make every pre-existing process
	// look "changed" on the first post-upgrade resolve and force a spurious
	// mass-restart.
	AgentDefaultEnv map[string]string
}

type processTerminationIntent struct {
	mu     sync.RWMutex
	reason string
}

func (i *processTerminationIntent) mark(reason string) {
	i.mu.Lock()
	i.reason = reason
	i.mu.Unlock()
}

func (i *processTerminationIntent) shutdownReason() string {
	i.mu.RLock()
	defer i.mu.RUnlock()
	return i.reason
}

// Compile-time assertion: *SharedACPProcess must satisfy the conversation.SharedProcess interface.
var _ conversation.SharedProcess = (*SharedACPProcess)(nil)

// SharedACPProcess manages a single ACP server process that can host multiple sessions.
// Multiple BackgroundSessions share this process via the MultiplexClient.
type SharedACPProcess struct {
	mu sync.RWMutex

	// Process state
	cmd    *exec.Cmd
	conn   *acp.ClientSideConnection
	client *MultiplexClient
	wait   func() error
	cancel context.CancelFunc // for restricted runner processes
	// terminationIntent belongs to the current OS process generation. The wait
	// closure captures the same pointer so a replacement never inherits intent.
	terminationIntent *processTerminationIntent

	// generation is bumped (under mu) each time Restart() actually kills and
	// replaces the process. Restart() snapshots this value on entry and
	// re-checks it (under mu) right before killing, so a caller whose
	// observed death has already been remediated by a concurrent Restart()
	// call no-ops instead of tearing down the healthy replacement — fixes
	// the mitto-x611 restart storm (N sessions independently detecting the
	// same process death each triggering a redundant kill+start cycle).
	generation int

	// Process death detection (Fix A: faster crash detection)
	// processDone is closed when the ACP OS process exits, providing sub-second
	// detection via OS-level liveness checks (signal 0 polling).
	processDone     chan struct{} // Closed when ACP OS process exits
	processDoneOnce sync.Once     // Ensures processDone is closed exactly once

	// Configuration (immutable after creation)
	config SharedACPProcessConfig

	// Agent capabilities (set after Initialize)
	capabilities *acp.AgentCapabilities

	// Context for process lifetime
	ctx       context.Context
	ctxCancel context.CancelFunc

	// activeRPCs tracks the number of in-flight RPCs on this process (session/prompt,
	// session/load, and session/new). The GC checks this counter before stopping an
	// idle process to avoid killing the pipe while an RPC is in-flight (LoadSession
	// can take 70+ seconds).
	activeRPCs      atomic.Int32
	activeRPCMu     sync.Mutex
	activeRPCIdleCh chan struct{}

	// setModelSem serialises set_model RPCs per process so concurrent callers queue
	// instead of racing the serially-served agent subprocess (mitto-3q9).
	// Capacity 1 means at most one set_model RPC is in flight at a time; additional
	// callers block (respecting their ctx) until the slot is released.
	// This semaphore guards ONLY set_model — it must never be held during prompts.
	setModelSem chan struct{}

	// Saturation tracking (mitto-13ck.2): consecutive NewSession/LoadSession RPC
	// timeouts against this shared process. After sessionSaturationTimeoutThreshold
	// consecutive timeouts the process is flagged saturated until saturatedUntil,
	// causing new start/resume RPCs to fail fast. Cleared on the next successful RPC
	// or when the cooldown elapses. Guarded by saturationMu.
	//
	// saturationLevel tracks how many times saturation has been tripped without a
	// successful RPC in between. Each trip (including post-cooldown probe timeouts)
	// increments the level, doubling the cooldown from sessionSaturationCooldownBase
	// up to sessionSaturationCooldownMax. A successful RPC resets level to 0.
	//
	// inProbe is true during the single-attempt probe window that opens when a cooldown
	// elapses: the next NewSession RPC is capped to ONE attempt so a still-hung process
	// costs ~25s (one attempt), not ~75s (three). A probe timeout immediately escalates
	// the cooldown (level+1) without waiting for the full threshold. A probe success
	// resets all saturation state.
	saturationMu           sync.Mutex
	consecutiveRPCTimeouts int
	saturatedUntil         time.Time
	saturationLevel        int
	inProbe                bool
	// saturationBuckets is the fixed-size ring buffer backing the rate/rolling-window
	// saturation trigger (mitto-5eq). Each bucket covers saturationWindowDuration /
	// saturationWindowBucketCount and records timeouts, budget-exhaustion bails, and
	// successful control-plane RPCs falling in its time slot. Buckets age out purely by
	// timestamp — a success does NOT wipe the window; it only adds a success sample so
	// the failure ratio drops naturally. This deliberately avoids the "one success
	// resets everything" bug that made the consecutive-timeout trigger inert for
	// intermittently-degraded processes. Guarded by saturationMu; lazily allocated on
	// first record.
	saturationBuckets []saturationBucket

	// Restart tracking
	restartMu    sync.Mutex
	restartCount int
	restartTimes []time.Time

	// onRestart is called after a successful Restart(), allowing the process manager
	// to invalidate caches (e.g., auxiliary sessions) that reference old session IDs.
	onRestart func()

	// MCP-init lifecycle tracking (mitto-8ul.1, mitto-29q). Set from the stderr-monitor
	// goroutine. mcpInitInProgress flips to true (edge-detected) when the agent reports
	// it is waiting for MCP servers to initialize, and is cleared to false on each
	// successful session/new or session/load RPC. Agents like Auggie re-run the MCP
	// handshake on every session/new, so this field re-arms per handshake episode and
	// is used by coldMCPBudget / RecommendedLoadTimeout to re-grant the extended budget
	// for per-session re-handshakes even after mcpInitDone has latched (mitto-29q).
	// mcpInitDone latches once the first session RPC succeeds; on its own it would
	// starve every subsequent session on agents that re-handshake, hence the additional
	// mcpInitInProgress gate. mcpInitTimedOut flips to true when the agent reports its
	// internal MCP-init wait budget elapsed; the currently-pending NewSession call
	// watches this via mcpInitTimeoutCh so it can abort promptly with an actionable
	// error rather than waiting for the RPC deadline. mcpInitTimeoutCh is (re-)created
	// per session/new attempt so a signal from a previous attempt does not fire
	// spuriously.
	mcpInitInProgress atomic.Bool
	mcpInitDone       atomic.Bool
	mcpInitTimedOut   atomic.Bool
	mcpInitMu         sync.Mutex
	mcpInitTimeoutCh  chan struct{}

	// mcpInitInProgressSince records when the current mcpInitInProgress window
	// began (stamped at the false->true edge in onMCPInitProgress, cleared
	// back to the zero Time whenever mcpInitInProgress is cleared to false).
	// Guarded by mcpInitMu. Used by GC Tier 5 (mitto-13n.1) to detect a
	// handshake the agent silently abandons — MCPInitInProgress()==true,
	// MCPInitDone()==false, with no MCPInitTimedOut() stderr line ever
	// observed — by bounding how long the in-progress state may persist
	// before the process is treated as wedged, rather than relying solely on
	// the agent's own (sometimes never-emitted) timeout signal.
	mcpInitInProgressSince time.Time

	// mcpInitDoneCh is closed exactly once (via mcpInitDoneOnce) when mcpInitDone
	// first latches, so WaitForMCPInit can block until the process is warm without
	// polling (mitto-54k.4).
	mcpInitDoneOnce sync.Once
	mcpInitDoneCh   chan struct{}

	// coldStartGate serializes concurrent NewSession/LoadSession callers on a
	// still-cold process (mitto-8tb). N conversations racing their deferred
	// session/new against a fresh shared process would each make the agent re-run
	// its MCP handshake in parallel, multiplying MCP child processes (36-78 obs.)
	// and self-inflicting saturation. The gate is a capacity-1 semaphore held only
	// on the cold path (extendedBudget=true); warm calls bypass it entirely.
	coldStartGate chan struct{}

	// loadReplaySuppressions identifies sessions whose session/load history replay
	// must be discarded before it enters the SDK's shared bounded notification queue.
	// Guarded by loadReplayMu; each state uses an atomic counter because filtering
	// runs on the SDK reader goroutine while LoadSession runs on its caller goroutine.
	loadReplayMu           sync.RWMutex
	loadReplaySuppressions map[acp.SessionId]*loadReplaySuppression

	// Logger
	logger *slog.Logger
}

// NewSharedACPProcess creates and starts a new shared ACP process.
// The process is initialized (ACP handshake) but no sessions are created yet.
func NewSharedACPProcess(ctx context.Context, config SharedACPProcessConfig) (*SharedACPProcess, error) {
	processCtx, processCancel := context.WithCancel(ctx)

	p := &SharedACPProcess{
		config:        config,
		client:        NewMultiplexClient(),
		ctx:           processCtx,
		ctxCancel:     processCancel,
		logger:        config.Logger,
		setModelSem:   make(chan struct{}, 1),
		coldStartGate: make(chan struct{}, 1),
		mcpInitDoneCh: make(chan struct{}),
	}

	if err := p.startProcess(); err != nil {
		processCancel()
		return nil, err
	}

	return p, nil
}

// startProcess starts the ACP process and performs the Initialize handshake.
// Must be called with appropriate synchronization (only from constructor or restart).
// Returns an *mittoAcp.ACPClassifiedError when the error has been classified, allowing
// callers to distinguish permanent from transient failures.
func (p *SharedACPProcess) startProcess() error {
	var lastErr error
	var lastClassified *mittoAcp.ACPClassifiedError

	for attempt := 0; attempt < maxProcessStartRetries; attempt++ {
		if attempt > 0 {
			delay := mittoAcp.BackoffDelay(attempt-1, processStartRetryBaseDelay, processStartRetryMaxDelay, processStartRetryJitterRatio)
			if p.logger != nil {
				p.logger.Info("Retrying ACP process start",
					"attempt", attempt+1,
					"max_attempts", maxProcessStartRetries,
					"delay", delay.String(),
					"last_error", lastErr,
					"error_class", lastClassified.Class.String(),
					"command", p.config.ACPCommand,
					"cwd", p.config.ACPCwd)
			}
			select {
			case <-p.ctx.Done():
				return fmt.Errorf("context cancelled during retry: %w", p.ctx.Err())
			case <-time.After(delay):
			}
		}

		stderr, processErr := p.doStartProcess()
		if processErr == nil {
			return nil
		}
		lastErr = processErr

		// Classify the error to determine if retrying is worthwhile.
		lastClassified = mittoAcp.ClassifyACPError(processErr, stderr)

		if p.logger != nil {
			p.logger.Warn("ACP process start failed",
				"attempt", attempt+1,
				"max_attempts", maxProcessStartRetries,
				"error", processErr,
				"error_class", lastClassified.Class.String(),
				"command", p.config.ACPCommand,
				"cwd", p.config.ACPCwd)
		}

		// Don't retry permanent errors — they won't resolve by retrying.
		if !lastClassified.IsRetryable() {
			if p.logger != nil {
				p.logger.Error("ACP process start failed with permanent error, skipping retries",
					"error", processErr,
					"user_message", lastClassified.UserMessage,
					"user_guidance", lastClassified.UserGuidance,
					"command", p.config.ACPCommand,
					"cwd", p.config.ACPCwd)
			}
			return lastClassified
		}
	}

	// All retries exhausted — return the classified error if available.
	if lastClassified != nil {
		return lastClassified
	}
	return lastErr
}

// doStartProcess performs a single attempt to start the ACP process and run the Initialize handshake.
// Returns the error and any captured stderr output for error classification.
func (p *SharedACPProcess) doStartProcess() (string, error) {
	processStart := time.Now()
	acpCommand := p.config.ACPCommand
	acpCwd := p.config.ACPCwd

	if p.logger != nil {
		p.logger.Info("Starting ACP process",
			"command", acpCommand,
			"cwd", acpCwd,
			"acp_server", p.config.ACPServer)
	}

	// Parse command using shell-aware tokenization FIRST,
	// then expand $MITTO_* references in each arg individually.
	// This preserves paths with spaces as single arguments.
	// Session ID is empty for shared processes (they serve multiple sessions).
	args, err := mittoAcp.ParseCommand(acpCommand)
	if err != nil {
		return "", fmt.Errorf("parse command: %w", err)
	}
	mittoEnv := mittoAcp.BuildMittoEnv("", p.config.WorkingDir, p.config.ACPServer, "")
	agentHintEnv := mittoAcp.BuildAgentHintEnv(p.config.ACPServer)
	expandedArgs := mittoAcp.ExpandArgs(args, mittoEnv)
	if p.logger != nil {
		changedIndices := make([]int, 0)
		for i, orig := range args {
			if orig != expandedArgs[i] {
				changedIndices = append(changedIndices, i)
			}
		}
		if len(changedIndices) > 0 {
			p.logger.Debug("expanded MITTO_* vars in shared ACP command args",
				"changed_indices", changedIndices,
				"changed_count", len(changedIndices),
				"acp_server", p.config.ACPServer)
		}
	}
	args = expandedArgs
	// Expand cwd (single string, not shlex-parsed)
	originalCwd := acpCwd
	acpCwd = mittoAcp.ExpandCommand(acpCwd, mittoEnv)
	if acpCwd != originalCwd && p.logger != nil {
		p.logger.Debug("expanded MITTO_* vars in shared ACP cwd",
			"acp_server", p.config.ACPServer)
	}

	var stdin runner.WriteCloser
	var stdout runner.ReadCloser
	var stderr runner.ReadCloser
	var wait func() error
	var cmd *exec.Cmd

	stderrCollector := procstart.NewStderrCollector(procstart.DefaultStderrCollectorBytes, p.logger)
	terminationIntent := &processTerminationIntent{}
	p.terminationIntent = terminationIntent
	// Install per-agent ignore patterns (mitto-k6h) so matching writes are
	// suppressed from the debug-level stderr log. Nil is a safe no-op.
	if p.config.StderrPatterns != nil {
		stderrCollector.SetIgnorePatterns(p.config.StderrPatterns.Ignore)
	}

	// Pre-create process death detection channel so the stderr crash detector
	// (Fix C) can signal it immediately when crash patterns are detected.
	p.processDone = make(chan struct{})
	p.processDoneOnce = sync.Once{}

	onCrashDetected := func() {
		if p.logger != nil {
			p.logger.Warn("ACP subprocess crash detected via stderr patterns",
				"acp_server", p.config.ACPServer)
		}
		p.processDoneOnce.Do(func() {
			close(p.processDone)
		})
	}

	// onDegraded is invoked on each stderr chunk matching a per-agent Degraded
	// regex (mitto-k6h). It feeds a fail-side sample into the mitto-5eq rolling
	// window so stderr-observed degradation can promote the process to saturated
	// and let GC Tier 5/6 recycle it. Unlike onCrashDetected, this is NOT latched
	// — recurring degraded output keeps contributing samples.
	onDegraded := func() {
		if p.logger != nil {
			p.logger.Warn("ACP subprocess degraded via stderr pattern (feeding saturation)",
				"acp_server", p.config.ACPServer)
		}
		p.recordDegradedStderr()
	}

	// MCP-init lifecycle callbacks (mitto-8ul.1). The progress callback re-arms per
	// handshake episode (mitto-29q); the timeout callback stays effectively one-shot
	// per process because it closes mcpInitTimeoutCh. The pending NewSession call
	// watches mcpInitTimeoutCh so it can abort promptly on a hard timeout signal
	// instead of waiting for the RPC deadline.
	onMCPInitProgress := func() {
		// Edge-detected (mitto-29q): only act on the false->true transition so a
		// handshake that logs "Waiting for MCP" repeatedly does not spam the log or
		// the mcp_initializing broadcast. The window is re-armed after each success
		// clears mcpInitInProgress, so a subsequent per-session re-handshake fires
		// this again.
		if !p.mcpInitInProgress.CompareAndSwap(false, true) {
			return
		}
		p.mcpInitMu.Lock()
		p.mcpInitInProgressSince = time.Now()
		p.mcpInitMu.Unlock()
		if p.logger != nil {
			p.logger.Info("ACP agent reports MCP servers initializing",
				"acp_server", p.config.ACPServer)
		}
		if cb := p.config.OnMCPInitProgress; cb != nil {
			cb()
		}
	}
	onMCPInitTimeout := func(servers []string) {
		p.mcpInitTimedOut.Store(true)
		if p.logger != nil {
			p.logger.Warn("ACP agent reports MCP initialization timed out",
				"acp_server", p.config.ACPServer,
				"mcp_servers", servers)
		}
		p.mcpInitMu.Lock()
		ch := p.mcpInitTimeoutCh
		p.mcpInitMu.Unlock()
		if ch != nil {
			select {
			case <-ch:
				// already closed
			default:
				close(ch)
			}
		}
		if cb := p.config.OnMCPInitTimeout; cb != nil {
			cb(servers)
		}
	}

	// Startup watchdog: warn/error if no stderr activity and no Initialize completion
	// within the configured windows. Cancelled when doStartProcess returns.
	watchdogCtx, watchdogCancel := context.WithCancel(p.ctx)
	defer watchdogCancel()
	var signalStartupActivity func()

	if p.config.Runner != nil {
		if acpCwd != "" && p.logger != nil {
			p.logger.Warn("cwd is not supported with restricted runners, ignoring",
				"cwd", acpCwd,
				"runner_type", p.config.Runner.Type())
		}
		if p.logger != nil {
			p.logger.Info("starting shared ACP process through restricted runner",
				"runner_type", p.config.Runner.Type(),
				"command", acpCommand)
		}

		var runCancel context.CancelFunc
		var runCtx context.Context
		runCtx, runCancel = context.WithCancel(p.ctx)

		// Build env using the same layering as the direct-exec branch below so that
		// server-specific vars (from settings.json acp_servers[].env) AND MITTO_* vars
		// are propagated to the restricted-runner-spawned process. agentHintEnv
		// (e.g. AGENT_MODE=1) sits below serverEnv so per-server settings can override.
		runnerEnv := procstart.BuildACPProcessEnv(p.config.Env, mittoEnv, agentHintEnv, p.config.AgentDefaultEnv)
		stdin, stdout, stderr, wait, err = p.config.Runner.RunWithPipes(runCtx, args[0], args[1:], runnerEnv)
		if err != nil {
			runCancel()
			return "", fmt.Errorf("failed to start with runner: %w", err)
		}
		p.cancel = runCancel

		if p.logger != nil && len(p.config.Env) > 0 {
			envKeys := make([]string, 0, len(p.config.Env))
			for k := range p.config.Env {
				envKeys = append(envKeys, k)
			}
			p.logger.Info("Applied server-specific environment variables to runner-spawned process",
				"env_keys", envKeys,
				"acp_server", p.config.ACPServer)
		}

		signalStartupActivity = procstart.StartACPStartupWatchdog(watchdogCtx, p.logger, acpCommand, p.config.ACPServer, -1)

		procstart.StartStderrMonitor(stderr, stderrCollector, onCrashDetected, signalStartupActivity, onMCPInitProgress, onMCPInitTimeout, onDegraded, p.config.StderrPatterns)
	} else {
		cmd = exec.CommandContext(p.ctx, args[0], args[1:]...)
		cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

		if acpCwd != "" {
			cmd.Dir = acpCwd
			if p.logger != nil {
				p.logger.Info("setting ACP process working directory",
					"cwd", acpCwd,
					"command", acpCommand)
			}
		}

		stdin, err = cmd.StdinPipe()
		if err != nil {
			return "", fmt.Errorf("stdin pipe: %w", err)
		}
		stdout, err = cmd.StdoutPipe()
		if err != nil {
			return "", fmt.Errorf("stdout pipe: %w", err)
		}
		stderrPipe, err := cmd.StderrPipe()
		if err != nil {
			return "", fmt.Errorf("stderr pipe: %w", err)
		}

		// Set environment variables for the ACP subprocess. Same layering as the
		// runner branch (os.Environ + agent hints + server-specific Env + MITTO_*).
		cmd.Env = procstart.BuildACPProcessEnv(p.config.Env, mittoEnv, agentHintEnv, p.config.AgentDefaultEnv)

		if p.logger != nil && len(p.config.Env) > 0 {
			envKeys := make([]string, 0, len(p.config.Env))
			for k := range p.config.Env {
				envKeys = append(envKeys, k)
			}
			p.logger.Info("Applied server-specific environment variables",
				"env_keys", envKeys,
				"acp_server", p.config.ACPServer)
		}

		if err := cmd.Start(); err != nil {
			return "", fmt.Errorf("failed to start ACP server: %w", err)
		}

		// Track process PID for orphan detection on restart
		if p.config.WorkspaceUUID != "" {
			if pidErr := writeACPPIDFile(p.config.WorkspaceUUID, cmd.Process.Pid, false); pidErr != nil {
				if p.logger != nil {
					p.logger.Warn("Failed to write ACP PID file", "error", pidErr,
						"workspace_uuid", p.config.WorkspaceUUID)
				}
			}
		}

		pid := -1
		if cmd.Process != nil {
			pid = cmd.Process.Pid
		}
		signalStartupActivity = procstart.StartACPStartupWatchdog(watchdogCtx, p.logger, acpCommand, p.config.ACPServer, pid)

		procstart.StartStderrMonitor(stderrPipe, stderrCollector, onCrashDetected, signalStartupActivity, onMCPInitProgress, onMCPInitTimeout, onDegraded, p.config.StderrPatterns)

		wait = func() error {
			return cmd.Wait()
		}
	}

	p.cmd = cmd

	// Wrap wait function to also close processDone channel on process exit.
	// The channel was pre-created above (before stderr monitors started).
	origWait := wait
	p.wait = func() error {
		err := origWait()

		// Log exit code and signal for crash telemetry
		if err != nil && p.logger != nil {
			if exitErr, ok := err.(*exec.ExitError); ok {
				logAttrs := []any{
					"exit_code", exitErr.ExitCode(),
					"acp_server", p.config.ACPServer,
				}
				shutdownReason := terminationIntent.shutdownReason()
				if shutdownReason == "" && p.ctx.Err() != nil {
					shutdownReason = "context_cancelled"
				}
				intentional := shutdownReason != ""
				var (
					sig      os.Signal
					signaled bool
				)
				if status, ok := exitErr.Sys().(syscall.WaitStatus); ok {
					if status.Signaled() {
						signaled = true
						sig = status.Signal()
						// Keep the legacy `signal=<Go string>` field for
						// back-compat with existing log-grep habits.
						logAttrs = append(logAttrs, "signal", sig.String())
					}
				}
				// mitto-7vq: enrich with canonical death_signal + stderr tail
				// so operators can tell OOM (SIGKILL) from panic (SIGSEGV) from
				// upstream disconnect. Intentional shutdowns get death_signal
				// for symmetry but skip the stderr tail (not diagnostic).
				var tailCollector *procstart.StderrCollector
				if !intentional {
					tailCollector = stderrCollector
				}
				logAttrs = append(logAttrs,
					procstart.AbnormalExitAttrs(sig, signaled, tailCollector, procstart.DefaultStderrTailBytes)...)

				// Log at DEBUG if we intentionally killed it, WARN if it crashed on its own
				if intentional {
					logAttrs = append(logAttrs, "shutdown_reason", shutdownReason)
					p.logger.Debug("ACP process exited (intentional shutdown)", logAttrs...)
				} else {
					p.logger.Warn("ACP process exited abnormally", logAttrs...)
				}
			} else {
				// Non-ExitError wait failures (shouldn't happen in practice)
				shutdownReason := terminationIntent.shutdownReason()
				if shutdownReason == "" && p.ctx.Err() != nil {
					shutdownReason = "context_cancelled"
				}
				if shutdownReason != "" {
					p.logger.Debug("ACP process wait error (intentional shutdown)",
						"error", err,
						"acp_server", p.config.ACPServer,
						"shutdown_reason", shutdownReason)
				} else {
					p.logger.Warn("ACP process wait error",
						"error", err,
						"acp_server", p.config.ACPServer)
				}
			}
		}

		p.processDoneOnce.Do(func() {
			close(p.processDone)
		})
		return err
	}

	// Start process liveness monitor for direct-exec processes.
	// Polls process every 2 seconds using kill(pid, 0) for fast death detection.
	if cmd != nil && cmd.Process != nil {
		processDoneCh := p.processDone
		processDoneOnce := &p.processDoneOnce
		pid := cmd.Process.Pid
		processCtx := p.ctx
		logger := p.logger
		acpServer := p.config.ACPServer
		go func() {
			ticker := time.NewTicker(2 * time.Second)
			defer ticker.Stop()
			for {
				select {
				case <-processDoneCh:
					return
				case <-processCtx.Done():
					return
				case <-ticker.C:
					err := syscall.Kill(pid, 0)
					if err != nil {
						if logger != nil {
							logger.Warn("ACP process no longer alive (detected by liveness check)",
								"pid", pid,
								"error", err,
								"acp_server", acpServer)
						}
						processDoneOnce.Do(func() {
							close(processDoneCh)
						})
						return
					}
				}
			}
		}()
	}

	filteredStdout := mittoAcp.NewJSONLineFilterReaderWithFilter(stdout, p.logger, p.filterLoadReplayNotification)

	// Use a larger notification queue for shared processes since all sessions
	// multiplex over the same connection. The default 1024 can overflow when
	// many sessions stream concurrently, killing the connection.
	p.conn = acp.NewClientSideConnection(p.client, stdin, filteredStdout,
		acp.WithMaxQueuedNotifications(sharedACPNotificationQueueCapacity))
	if p.logger != nil {
		p.conn.SetLogger(logging.DowngradeACPSDKErrors(p.logger))
	}

	// Create an init context with a per-attempt timeout (mitto-13ck.2).
	// This bounds each Initialize attempt so a dead-session doesn't hang the full
	// SDK-internal 60 s control timeout (DEFAULT_CONTROL_REQUEST_TIMEOUT) on every
	// retry, cutting the total retry tail from ~180 s to ~90 s (3 × 25 s + backoffs).
	// The conn.Done()/processDone watcher below cancels initCtx immediately on detected
	// crashes; the timeout is the backstop when neither signal arrives (live-but-hung
	// process with open pipes). 25 s is generous for healthy cold starts.
	initCtx, initCancel := context.WithTimeout(p.ctx, processInitializeAttemptTimeout)
	defer initCancel()

	// Monitor ACP process health: if the connection's Done() channel closes
	// or the OS process exits (processDone), cancel the init context immediately.
	go func() {
		select {
		case <-p.conn.Done():
			if p.logger != nil {
				p.logger.Warn("ACP connection closed during initialization, cancelling",
					"acp_server", p.config.ACPServer)
			}
			initCancel()
		case <-p.processDone:
			if p.logger != nil {
				p.logger.Warn("ACP process exited during initialization, cancelling",
					"acp_server", p.config.ACPServer)
			}
			initCancel()
		case <-initCtx.Done():
			// Initialization completed normally or was cancelled for another reason
		}
	}()

	initStart := time.Now()
	initResp, err := p.conn.Initialize(initCtx, acp.InitializeRequest{
		ProtocolVersion: acp.ProtocolVersionNumber,
		ClientCapabilities: acp.ClientCapabilities{
			Fs: acp.FileSystemCapabilities{
				ReadTextFile:  true,
				WriteTextFile: true,
			},
		},
		ClientInfo: &acp.Implementation{
			Name:    "mitto",
			Title:   strPtr("Mitto"),
			Version: "dev", // Use a constant for now, we'll improve this later
		},
	})
	initDuration := time.Since(initStart)

	if err != nil {
		time.Sleep(100 * time.Millisecond)

		stderrOutput := strings.TrimSpace(stderrCollector.GetOutput())
		if p.logger != nil {
			logAttrs := []any{
				"command", acpCommand,
				"cwd", acpCwd,
				"error", err,
				"initialize_ms", initDuration.Milliseconds(),
			}
			if stderrOutput != "" {
				logAttrs = append(logAttrs, "stderr", stderrOutput)
			}
			p.logger.Warn("ACP process initialization failed", logAttrs...)
		}

		p.killProcess("")
		return stderrOutput, fmt.Errorf("failed to initialize: %w", err)
	}

	p.capabilities = &initResp.AgentCapabilities

	if p.logger != nil {
		logAttrs := []any{
			"acp_server", p.config.ACPServer,
			"command", acpCommand,
			"cwd", acpCwd,
			"protocol_version", initResp.ProtocolVersion,
			"load_session", initResp.AgentCapabilities.LoadSession,
			"process_start_ms", time.Since(processStart).Milliseconds(),
			"initialize_rpc_ms", initDuration.Milliseconds(),
		}

		// Add agent info if available
		if initResp.AgentInfo != nil {
			logAttrs = append(logAttrs,
				"agent_name", initResp.AgentInfo.Name,
				"agent_version", initResp.AgentInfo.Version)
		}

		// Add detailed capabilities
		logAttrs = append(logAttrs,
			"prompt_image", initResp.AgentCapabilities.PromptCapabilities.Image,
			"prompt_audio", initResp.AgentCapabilities.PromptCapabilities.Audio,
			"prompt_embedded_context", initResp.AgentCapabilities.PromptCapabilities.EmbeddedContext)

		p.logger.Info("Shared ACP process started", logAttrs...)

		// Log SessionCapabilities at DEBUG level
		p.logger.Debug("Agent session capabilities",
			"acp_server", p.config.ACPServer,
			"resume_supported", initResp.AgentCapabilities.SessionCapabilities.Resume != nil,
			"fork_supported", initResp.AgentCapabilities.SessionCapabilities.Fork != nil,
			"list_supported", initResp.AgentCapabilities.SessionCapabilities.List != nil)

		// Log Meta fields separately at DEBUG level if present
		if len(initResp.Meta) > 0 {
			p.logger.Debug("ACP initialize response meta",
				"acp_server", p.config.ACPServer,
				"meta", initResp.Meta)
		}
		if len(initResp.AgentCapabilities.Meta) > 0 {
			p.logger.Debug("ACP agent capabilities meta",
				"acp_server", p.config.ACPServer,
				"meta", initResp.AgentCapabilities.Meta)
		}
		if initResp.AgentInfo != nil && len(initResp.AgentInfo.Meta) > 0 {
			p.logger.Debug("ACP agent info meta",
				"acp_server", p.config.ACPServer,
				"meta", initResp.AgentInfo.Meta)
		}
	}

	return "", nil
}

// saturationCooldownForLevel returns the escalating cooldown duration for the given
// saturation level (mitto-13ck.2). Level 1 → base (30s), level 2 → 2×base (60s),
// level 3 → 4×base (120s), and so on, capped at sessionSaturationCooldownMax (5min).
// Guards against int64 overflow via an early cap at shift≥25.
func saturationCooldownForLevel(level int) time.Duration {
	if level <= 0 {
		return sessionSaturationCooldownBase
	}
	shift := level - 1
	if shift >= 25 {
		// 30s × 2^25 would overflow int64 nanoseconds; return the cap early.
		return sessionSaturationCooldownMax
	}
	d := sessionSaturationCooldownBase * time.Duration(1<<uint(shift))
	if d > sessionSaturationCooldownMax || d <= 0 {
		return sessionSaturationCooldownMax
	}
	return d
}

// saturationBucket is one time-slot of the rate/rolling-window saturation
// counter (mitto-5eq). Each bucket covers saturationWindowDuration /
// saturationWindowBucketCount and records how many timeouts, budget-exhaustion
// bails, and successful control-plane RPCs happened during that slot. The `start`
// timestamp is aligned to the bucket duration so ring-buffer slot reuse can be
// detected (a new event whose aligned slot no longer matches `start` means this
// bucket has aged out and must be zeroed before being incremented).
type saturationBucket struct {
	start     time.Time
	timeouts  int
	bails     int
	successes int
}

// saturationBucketDuration returns the per-bucket time slot length. Kept as a
// function (not a const) so the arithmetic is centralised and both writers and
// readers agree on the divisor even if the constants ever change.
func saturationBucketDuration() time.Duration {
	return saturationWindowDuration / saturationWindowBucketCount
}

// saturationCurrentBucketLocked returns a pointer to the ring-buffer slot for the
// current wall-clock time, zeroing it first if it belonged to an older window
// (implicit prune). saturationMu MUST be held by the caller.
func (p *SharedACPProcess) saturationCurrentBucketLocked(now time.Time) *saturationBucket {
	if p.saturationBuckets == nil {
		p.saturationBuckets = make([]saturationBucket, saturationWindowBucketCount)
	}
	bucketDur := saturationBucketDuration()
	slot := now.Truncate(bucketDur)
	// Map the aligned slot to a ring index. Both bucketDur and UnixNano are >0 here,
	// so the modulo is well-defined and stable across all sample times.
	idx := int(slot.UnixNano()/int64(bucketDur)) % saturationWindowBucketCount
	if idx < 0 {
		idx += saturationWindowBucketCount
	}
	if !p.saturationBuckets[idx].start.Equal(slot) {
		p.saturationBuckets[idx] = saturationBucket{start: slot}
	}
	return &p.saturationBuckets[idx]
}

// saturationWindowStatsLocked aggregates all live buckets (those whose start is
// within the current window ending at `now`) into totals. Buckets whose start is
// older than now-saturationWindowDuration are treated as expired and skipped.
// saturationMu MUST be held by the caller.
func (p *SharedACPProcess) saturationWindowStatsLocked(now time.Time) (timeouts, bails, successes int) {
	cutoff := now.Add(-saturationWindowDuration)
	for i := range p.saturationBuckets {
		b := p.saturationBuckets[i]
		if b.start.IsZero() || b.start.Before(cutoff) {
			continue
		}
		timeouts += b.timeouts
		bails += b.bails
		successes += b.successes
	}
	return
}

// evaluateSaturationRateTriggerLocked checks whether the current rolling window
// meets the rate/min-sample threshold and, if so, promotes the process into the
// SAME saturation state that the consecutive-timeout path uses (bumping
// saturationLevel and arming saturatedUntil). This is a no-op when the process is
// already saturated (saturatedUntil in the future) to avoid re-arming the cooldown
// on every subsequent failure — the consecutive-path probe/escalation logic then
// takes over as normal once the cooldown elapses. saturationMu MUST be held.
func (p *SharedACPProcess) evaluateSaturationRateTriggerLocked(now time.Time) {
	if !p.saturatedUntil.IsZero() && now.Before(p.saturatedUntil) {
		return
	}
	timeouts, bails, successes := p.saturationWindowStatsLocked(now)
	total := timeouts + bails + successes
	if total < saturationWindowMinSamples {
		return
	}
	fails := timeouts + bails
	if float64(fails)/float64(total) < saturationWindowFailRatio {
		return
	}
	// Trip via the shared saturation state so IsSaturated()/IsConfirmedDegraded()
	// (and therefore GC Tier 5/6) pick it up unchanged. We deliberately do NOT
	// touch consecutiveRPCTimeouts here — the two triggers stay independent so
	// the consecutive fast path for the fully-wedged case is unaffected.
	p.saturationLevel++
	p.saturatedUntil = now.Add(saturationCooldownForLevel(p.saturationLevel))
}

// recordRPCTimeout records a NewSession/LoadSession RPC timeout (mitto-13ck.2).
// In normal mode the consecutive counter increments toward the threshold; once the
// threshold is reached, saturationLevel is incremented and a fresh cooldown is set.
// In probe mode (inProbe=true) a single timeout immediately escalates the level and
// re-saturates, because the probe has already confirmed the process is still hung.
// The timeout is ALSO recorded into the rate/rolling-window trigger (mitto-5eq)
// which can promote the process to saturated independently — see
// evaluateSaturationRateTriggerLocked for the rate-based fallback path.
func (p *SharedACPProcess) recordRPCTimeout() {
	p.saturationMu.Lock()
	defer p.saturationMu.Unlock()
	p.recordRPCFailureLocked(time.Now())
}

// recordRPCFailureLocked is the shared escalation body for both full RPC
// deadlines (recordRPCTimeout) and the agent's self-reported "query closed
// before response received" wedge (recordRPCWedgeFailure, mitto-aoo). Both
// are direct evidence the shared process cannot complete a session/new, so
// they feed the SAME consecutive-timeout fast path and rolling window.
// Callers must hold saturationMu.
func (p *SharedACPProcess) recordRPCFailureLocked(now time.Time) {
	p.saturationCurrentBucketLocked(now).timeouts++
	if p.inProbe {
		// Probe failed: immediately escalate and re-saturate.
		p.inProbe = false
		p.saturationLevel++
		p.consecutiveRPCTimeouts = 0
		p.saturatedUntil = now.Add(saturationCooldownForLevel(p.saturationLevel))
		return
	}
	p.consecutiveRPCTimeouts++
	if p.consecutiveRPCTimeouts >= sessionSaturationTimeoutThreshold {
		p.saturationLevel++
		p.saturatedUntil = now.Add(saturationCooldownForLevel(p.saturationLevel))
		return
	}
	// Consecutive threshold not reached — the rate/rolling-window trigger may still
	// fire for the intermittent-storm case (mitto-5eq).
	p.evaluateSaturationRateTriggerLocked(now)
}

// recordRPCWedgeFailure records a NewSession/LoadSession RPC failing with the
// agent's "query closed before response received" wedge signature (mitto-aoo,
// see isAgentQueryClosedErr). This reply is fast (1-10ms), not a timeout, but
// it is direct, self-reported evidence the agent's query loop is torn down
// and can never complete another session/new on this process — so it is fed
// into the SAME consecutive-timeout fast path as recordRPCTimeout rather than
// only the rolling window (unlike recordRPCBudgetBail/recordDegradedStderr,
// which record softer signals). Deliberately not gated on extendedBudget: a
// 1-10ms failure cannot be cold-start MCP latency.
func (p *SharedACPProcess) recordRPCWedgeFailure() {
	p.saturationMu.Lock()
	defer p.saturationMu.Unlock()
	p.recordRPCFailureLocked(time.Now())
}

// recordRPCBudgetBail records a mid-flight budget-exhaustion bail from
// shouldFailFastCreateAttempt (mitto-5eq). These bails are NOT full RPC deadlines
// (nothing was actually attempted) so they intentionally do NOT feed the
// consecutive-timeout fast path (that path is reserved for the fully-wedged case
// where the RPC itself runs to deadline). They DO feed the rate/rolling-window
// signal — this is the "budget-exhaustion bails don't count" gap the rate trigger
// was designed to close.
func (p *SharedACPProcess) recordRPCBudgetBail() {
	p.saturationMu.Lock()
	defer p.saturationMu.Unlock()
	now := time.Now()
	p.saturationCurrentBucketLocked(now).bails++
	p.evaluateSaturationRateTriggerLocked(now)
}

// recordDegradedStderr records a per-agent "degraded" stderr pattern match
// (mitto-k6h) as a fail-side sample in the mitto-5eq rolling window. A degraded
// stderr line is real degradation evidence but is NOT an RPC deadline, so it does
// NOT touch the consecutive-timeout fast path (reserved for the fully-wedged case).
// It feeds the SAME rolling-window rate trigger as recordRPCBudgetBail, so frequent
// degraded output — alone or combined with real RPC timeouts/bails — can promote the
// process to saturated and let GC Tier 5/6 recycle it.
func (p *SharedACPProcess) recordDegradedStderr() {
	p.saturationMu.Lock()
	defer p.saturationMu.Unlock()
	now := time.Now()
	p.saturationCurrentBucketLocked(now).timeouts++
	p.evaluateSaturationRateTriggerLocked(now)
}

// recordRPCSuccess clears the consecutive-timeout saturation tracking after a
// successful NewSession/LoadSession RPC (mitto-13ck.2). Resets the saturation
// level so the next event starts again from the base cooldown (30s).
//
// A success is ALSO recorded as a sample in the rolling-window trigger
// (mitto-5eq), but the window itself is NOT wiped: entries age out purely by
// timestamp. If we cleared the window here we would reintroduce the exact
// interspersed-success reset bug that made the consecutive-timeout trigger inert
// for intermittently-degraded processes. Keeping window history intact means a
// single fluke success right after a rate-trip clears the fast-path state but the
// NEXT timeout/bail can immediately re-trip via the still-populated window.
func (p *SharedACPProcess) recordRPCSuccess() {
	p.saturationMu.Lock()
	defer p.saturationMu.Unlock()
	p.saturationCurrentBucketLocked(time.Now()).successes++
	p.consecutiveRPCTimeouts = 0
	p.saturatedUntil = time.Time{}
	p.saturationLevel = 0
	p.inProbe = false
}

// shouldFailFastCreateAttempt decides whether a NewSession retry attempt should
// bail early instead of consuming another full per-attempt budget. The first
// attempt always proceeds (the first victim pays the budget); subsequent
// attempts bail if the shared process has become saturated mid-flight, or if the
// caller's remaining deadline can no longer fund a full per-attempt budget.
// Returns a non-empty reason when the attempt should fail fast.
func shouldFailFastCreateAttempt(attempt int, saturated bool, hasDeadline bool, remaining time.Duration, perAttemptBudget time.Duration) (bail bool, reason string) {
	if attempt <= 1 {
		return false, ""
	}
	if saturated {
		return true, "shared ACP process became saturated mid-flight"
	}
	if hasDeadline && remaining < perAttemptBudget {
		return true, "insufficient remaining budget for another attempt"
	}
	return false, ""
}

// coldMCPBudget decides whether the extended MCP-init budget applies to the
// current NewSession/LoadSession attempt (mitto-8ul.1) and returns the
// per-attempt and total budget to use.
//
// The extended budget applies when ALL of the following hold:
//   - MCPInitTimeout > 0 (the operator has not disabled it).
//   - Either the process has not yet observed a successful cold-start session RPC
//     (mcpInitDone is false), OR an MCP-init handshake is currently in progress
//     (mcpInitInProgress is true). The extended budget is ALSO re-granted whenever
//     mcpInitInProgress is true — i.e. the agent is (re-)running an MCP handshake —
//     because agents like Auggie re-handshake MCP on every session/new, so a
//     one-shot mcpInitDone latch would starve every session after the first
//     (mitto-29q).
//
// The extended budget does NOT gate on the request carrying MCP servers, because
// Mitto attaches MCP through a globally-registered server (not per session/new
// call), and even agents whose only MCP is configured globally block session/new
// on the same handshake. Applying the widened budget to every cold session/new is
// safe: it is capped by the actual RPC deadline anyway and reverts to the normal
// 25 s once one call succeeds AND no new handshake is running. When the extended
// budget applies both the per-attempt and total budgets are widened to
// MCPInitTimeout, sized above the agent's own MCP-init wait (e.g. Auggie's 225 s)
// plus margin.
//
// hasMCPServers is retained on the signature for observability / future gating.
func (p *SharedACPProcess) coldMCPBudget(hasMCPServers bool) (perAttempt time.Duration, total time.Duration, extended bool) {
	_ = hasMCPServers // reserved for future per-request gating
	if p.config.MCPInitTimeout <= 0 {
		return sessionCreateAttemptTimeout, sessionCreateTotalBudget, false
	}
	if p.mcpInitDone.Load() && !p.mcpInitInProgress.Load() {
		return sessionCreateAttemptTimeout, sessionCreateTotalBudget, false
	}
	return p.config.MCPInitTimeout, p.config.MCPInitTimeout, true
}

// effectiveMaxAttemptsForBudget returns the retry cap NewSession should honour
// for a given budget mode (mitto-54k.12). In extendedBudget mode the total
// budget equals the per-attempt budget by design, so a retry on the same wall-
// clock window cannot succeed against the agent's own internal MCP-init
// deadline — cap to a single honest attempt. Warm/normal callers keep the
// documented sessionCreateMaxAttempts retry-with-jitter policy.
func effectiveMaxAttemptsForBudget(extendedBudget bool) int {
	if extendedBudget {
		return 1
	}
	return sessionCreateMaxAttempts
}

// acquireColdStartGate blocks until the capacity-1 cold-start gate is acquired
// or ctx is done (mitto-8tb). Returns a release func (nil on error). Only cold
// callers (extendedBudget=true) invoke this; warm calls bypass it.
func (p *SharedACPProcess) acquireColdStartGate(ctx context.Context) (release func(), err error) {
	if p.coldStartGate == nil {
		return func() {}, nil
	}
	gateWaitStart := time.Now()
	select {
	case p.coldStartGate <- struct{}{}:
		if p.logger != nil {
			p.logger.Debug("cold_start_gate acquired",
				"wait_ms", time.Since(gateWaitStart).Milliseconds(),
				"permits_in_use", len(p.coldStartGate),
				"capacity", cap(p.coldStartGate),
				"cold_start_id", coldstart.FromContext(ctx).ID())
		}
		return func() { <-p.coldStartGate }, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// RecommendedLoadTimeout implements conversation.SharedProcess (mitto-8ul.1).
// For a cold process (mcpInitDone=false), returns MCPInitTimeout so the caller's
// outer timeout does not truncate the process's own extended budget. Returns 0
// once the process is warm (mcpInitDone=true) AND no handshake is currently
// running (mcpInitInProgress=false). Re-widens to MCPInitTimeout while
// mcpInitInProgress is true so per-session re-handshakes on agents that re-run
// the MCP init on every session/new (e.g. Auggie) still get the extended budget
// (mitto-29q). The hasMCPServers hint is retained for future per-request gating;
// it is not currently load-bearing because Mitto attaches MCP globally.
func (p *SharedACPProcess) RecommendedLoadTimeout(hasMCPServers bool) time.Duration {
	_ = hasMCPServers
	if p.config.MCPInitTimeout <= 0 {
		return 0
	}
	if p.mcpInitDone.Load() && !p.mcpInitInProgress.Load() {
		return 0
	}
	return p.config.MCPInitTimeout
}

// markMCPInitDone latches mcpInitDone and closes mcpInitDoneCh exactly once so
// waiters in WaitForMCPInit unblock. Safe to call on every successful session RPC.
func (p *SharedACPProcess) markMCPInitDone() {
	p.mcpInitDone.Store(true)
	p.mcpInitDoneOnce.Do(func() {
		if p.mcpInitDoneCh != nil {
			close(p.mcpInitDoneCh)
		}
	})
}

// MCPInitDone reports whether the shared process's MCP-init window has
// closed (the agent's first successful RPC observed). Used by the adaptive
// pre-warming controller (mitto-mw0) to compute the health verdict.
func (p *SharedACPProcess) MCPInitDone() bool {
	return p.mcpInitDone.Load()
}

// MCPInitInProgress reports whether the shared process's stderr monitor has
// observed the agent actively waiting on an MCP-init handshake that has not
// yet completed (mitto-337). Edge-detected true/false via the stderr monitor
// (see onMCPInitProgress/mcpInitInProgress field doc); cleared on each
// successful session/new or session/load RPC. Used alongside MCPInitDone() by
// getOrCreateAuxiliarySession to bail proactively on a quiescent-but-MCP-gated
// process — checking this flag alone (without MCPInitDone()) would also match
// the normal per-session re-handshake on agents like Auggie that re-run MCP
// init on every warm session/new (mitto-29q), so callers must gate on
// (!MCPInitDone() && MCPInitInProgress()) to target only the cold-start case.
func (p *SharedACPProcess) MCPInitInProgress() bool {
	return p.mcpInitInProgress.Load()
}

// MCPInitInProgressSince reports when the current MCPInitInProgress() window
// began, or the zero Time if no window is currently open (see
// mcpInitInProgressSince field doc). Used by GC Tier 5 (mitto-13n.1) to bound
// how long a process may sit in the in-progress-but-not-done state before
// being treated as wedged, independent of MCPInitTimedOut() which the agent
// does not always emit.
func (p *SharedACPProcess) MCPInitInProgressSince() time.Time {
	p.mcpInitMu.Lock()
	defer p.mcpInitMu.Unlock()
	return p.mcpInitInProgressSince
}

// WaitForMCPInit blocks until the shared process's MCP-init window closes
// (mcpInitDone latched via a successful session RPC), ctx is done, or the
// process exits. Returns true only if the process became warm. Used by the
// background resume path (mitto-54k.4) to defer non-foreground LoadSession
// until the foreground session's handshake warms the agent, without stranding
// background sessions (the caller bounds ctx).
func (p *SharedACPProcess) WaitForMCPInit(ctx context.Context) bool {
	if p.mcpInitDone.Load() {
		return true
	}
	if p.mcpInitDoneCh == nil {
		return p.mcpInitDone.Load()
	}
	processDone := p.processDone
	if processDone == nil {
		processDone = make(chan struct{}) // never fires; process-exit not observable
	}
	select {
	case <-p.mcpInitDoneCh:
		return true
	case <-ctx.Done():
		return false
	case <-processDone:
		return false
	}
}

// MCPInitTimedOut reports whether the shared process's stderr monitor has
// seen the agent report its internal MCP-init wait budget elapsed (a hard
// "MCP is broken" signal). Used by the adaptive pre-warming controller
// (mitto-mw0) to compute the health verdict.
func (p *SharedACPProcess) MCPInitTimedOut() bool {
	return p.mcpInitTimedOut.Load()
}

// beginMCPInitWindow prepares per-RPC MCP-init lifecycle tracking (mitto-8ul.1):
// it (re-)creates a fresh timeout channel so a signal from a previous RPC does not
// fire on this one, and clears the mcpInitTimedOut flag if it was set. Returns the
// channel the caller should select on.
func (p *SharedACPProcess) beginMCPInitWindow() <-chan struct{} {
	p.mcpInitMu.Lock()
	defer p.mcpInitMu.Unlock()
	p.mcpInitTimedOut.Store(false)
	p.mcpInitTimeoutCh = make(chan struct{})
	return p.mcpInitTimeoutCh
}

// isSaturated reports whether the shared process is currently flagged saturated.
// When the cooldown has elapsed it self-clears and sets inProbe=true so the next
// NewSession call is capped to a single probe attempt (mitto-13ck.2). The probe
// outcome drives further state transitions: a timeout re-escalates the cooldown
// (recordRPCTimeout) and a success resets all state (recordRPCSuccess).
func (p *SharedACPProcess) isSaturated() bool {
	p.saturationMu.Lock()
	defer p.saturationMu.Unlock()
	if p.saturatedUntil.IsZero() {
		return false
	}
	if time.Now().After(p.saturatedUntil) {
		// Cooldown elapsed: enter probe mode; the level is preserved until a
		// successful RPC resets it, so a probe timeout can escalate from here.
		p.saturatedUntil = time.Time{}
		p.consecutiveRPCTimeouts = 0
		p.inProbe = true
		return false
	}
	return true
}

// IsSaturated reports whether the shared process is currently flagged saturated
// (mitto-tfb Phase 2). Unlike the private isSaturated(), this is a NON-mutating read:
// it never self-clears to probe mode, so the GC's proactive health-recycle tier can
// poll it without perturbing the saturation state machine. It returns true while the
// cooldown window (saturatedUntil) is set and has not yet elapsed.
func (p *SharedACPProcess) IsSaturated() bool {
	p.saturationMu.Lock()
	defer p.saturationMu.Unlock()
	if p.saturatedUntil.IsZero() {
		return false
	}
	return time.Now().Before(p.saturatedUntil)
}

// IsConfirmedDegraded reports whether the process is currently saturated AND has
// reached confirmedDegradedLevel (mitto-1h0): it tripped saturation, served its
// cooldown, ran a single-attempt probe, and that probe also timed out. Like
// IsSaturated(), this is a NON-mutating read guarded by saturationMu — it never
// flips inProbe or otherwise perturbs the saturation state machine, so the GC's
// non-idle recycle tier (Tier 6) can poll it safely.
func (p *SharedACPProcess) IsConfirmedDegraded() bool {
	p.saturationMu.Lock()
	defer p.saturationMu.Unlock()
	if p.saturatedUntil.IsZero() {
		return false
	}
	return time.Now().Before(p.saturatedUntil) && p.saturationLevel >= confirmedDegradedLevel
}

// SaturationLevel returns the current saturation escalation level (0 = healthy).
// Non-mutating; for tests and observability.
func (p *SharedACPProcess) SaturationLevel() int {
	p.saturationMu.Lock()
	defer p.saturationMu.Unlock()
	return p.saturationLevel
}

// rpcErrorCode extracts the JSON-RPC error code from err when it (or any error it
// wraps) is an *acp.RequestError. The second return reports whether a code was
// found. Used to surface a structured, queryable rpc_code on NewSession failures
// (mitto-8d7) in addition to the full error string.
func rpcErrorCode(err error) (int, bool) {
	var re *acp.RequestError
	if errors.As(err, &re) && re != nil {
		return re.Code, true
	}
	return 0, false
}

// ErrSharedProcessSaturated is re-exported from internal/acpproc/acperrors
// so consumers already importing internal/acpproc get the classifier through
// the same package they use for aux calls. The canonical definition lives in
// acperrors to keep the classifier surface cycle-free (internal/acpproc
// imports internal/conversation, which cannot in turn import
// internal/acpproc — mitto-ammz.1).
var ErrSharedProcessSaturated = acperrors.ErrSharedProcessSaturated

// ErrProcessSaturated, ErrProcessBusy, and ErrMCPInitGated are re-exported
// from internal/acpproc/acperrors (same rationale as ErrSharedProcessSaturated
// above) so the three bail sites in acp_process_manager.go can return the
// specific sentinel for their condition using the same acpproc-local name
// they already use for the umbrella (mitto-13n.2).
var (
	ErrProcessSaturated = acperrors.ErrProcessSaturated
	ErrProcessBusy      = acperrors.ErrProcessBusy
	ErrMCPInitGated     = acperrors.ErrMCPInitGated
)

// IsAgentInternalDeadlineErr is re-exported from internal/acpproc/acperrors
// (same rationale as ErrSharedProcessSaturated). Prefer acperrors as the
// import path from packages that only need the classifier and do not depend
// on internal/acpproc otherwise.
func IsAgentInternalDeadlineErr(err error) bool {
	return acperrors.IsAgentInternalDeadlineErr(err)
}

// isAgentInternalDeadlineErr reports whether err is the agent's OWN internal
// deadline firing on a session/new (or session/load) RPC — the auggie
// "session/new wedge" signature (mitto-y1g follow-up): the agent's handler
// completes its own internal timeout and returns a JSON-RPC application error
// -32603 ("Internal error") whose data carries "context deadline exceeded".
//
// Crucially this is NOT a Go context.DeadlineExceeded — it is delivered as an
// *acp.RequestError, so errors.Is(err, context.DeadlineExceeded) is false and
// the RPC returns at exactly the agent's internal budget (observed rpc_ms=30000,
// ctx_remaining_ms=29999). Because the standard timeout accounting only counts
// errors.Is(DeadlineExceeded), this wedge previously never fed saturation and the
// process was never recycled — the interactive prompt stayed gated behind a dead
// session/new handler. Treating it as a timeout lets the mitto-13ck.2 saturation
// path (and therefore GC Tier 5/6 recycle) heal the wedged process.
func isAgentInternalDeadlineErr(err error) bool {
	if err == nil {
		return false
	}
	code, ok := rpcErrorCode(err)
	if !ok || code != -32603 {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "deadline exceeded") ||
		strings.Contains(msg, "context deadline")
}

// isAgentQueryClosedErr reports whether err is the agent's "query closed
// before response received" wedge signature on a session/new (or
// session/load) RPC (mitto-aoo): a JSON-RPC application error -32603
// ("Internal error") whose data carries "query closed before response
// received". Observed returning in 1-10ms — the process is alive and
// answering JSON-RPC, but its internal query loop is torn down and can never
// create another session. Unlike isAgentInternalDeadlineErr this is not a
// deadline/timeout signature, so it previously fed zero saturation samples:
// neither the consecutive-timeout fast path nor the mitto-5eq rolling window
// ever saw it, so a process wedged this way was never recycled by GC Tier
// 5/6 (observed: 38 consecutive session/new failures over 9h). Keep this in
// sync with acperrors.IsAgentQueryClosedErr.
func isAgentQueryClosedErr(err error) bool {
	if err == nil {
		return false
	}
	code, ok := rpcErrorCode(err)
	if !ok || code != -32603 {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "query closed before response received")
}

// NewSession creates a new ACP session on this shared process.
func (p *SharedACPProcess) NewSession(ctx context.Context, cwd string, mcpServers []acp.McpServer) (*conversation.SessionHandle, error) {
	p.beginRPC()
	defer p.endRPC()

	totalStart := time.Now()

	p.mu.RLock()
	conn := p.conn
	caps := p.capabilities
	processDone := p.processDone
	p.mu.RUnlock()

	if conn == nil {
		return nil, fmt.Errorf("shared ACP process is not running")
	}

	// Liveness check: fail fast if the OS process is already confirmed dead.
	// This catches the race window between OS termination and detection (up to 2s).
	if processDone != nil {
		select {
		case <-processDone:
			return nil, fmt.Errorf("shared ACP process has exited")
		default:
		}
	}

	// Saturation fail-fast (mitto-13ck.2): if recent NewSession/LoadSession RPCs
	// against this shared process have repeatedly timed out, the process is hung or
	// overloaded. Fail fast with a clear deadline-classified error instead of
	// draining the full retry budget on a process that is not responding — which
	// previously surfaced as a user-visible "context deadline exceeded".
	if p.isSaturated() {
		// mitto-wub: wrap acperrors.ErrProcessSaturated alongside context.DeadlineExceeded
		// (mirrors the acp_process_manager.go aux-session bails) so callers upstream —
		// notably session_manager.go's ACP-start failure counter — can errors.Is for the
		// sentinel and recognize this as transient saturation, not a hard start failure.
		return nil, fmt.Errorf("shared ACP process is saturated (repeated RPC timeouts); failing fast: %w: %w", acperrors.ErrProcessSaturated, context.DeadlineExceeded)
	}

	// Probe mode (mitto-13ck.2): isSaturated() sets inProbe when a cooldown elapses.
	// Cap the retry loop to ONE attempt so a still-hung process costs ~25s (one
	// attempt budget), not ~75s (three attempts), to re-confirm. A probe timeout
	// re-escalates the cooldown via recordRPCTimeout; success resets all state.
	effectiveMaxAttempts := sessionCreateMaxAttempts
	p.saturationMu.Lock()
	if p.inProbe {
		effectiveMaxAttempts = 1
	}
	p.saturationMu.Unlock()

	if cwd == "" {
		cwd = "."
	}

	// Extended MCP-init budget (mitto-8ul.1): a cold session/new on a process with
	// MCP servers may block up to the agent's own MCP-init wait (Auggie: ~225s).
	// coldMCPBudget widens both budgets to MCPInitTimeout for that first call only;
	// subsequent sessions on the same warm process use the normal budgets.
	perAttemptBudget, totalBudget, extendedBudget := p.coldMCPBudget(len(mcpServers) > 0)

	// Extended-budget single-shot cap (mitto-54k.12): in extendedBudget mode
	// coldMCPBudget returns totalBudget == perAttemptBudget, so attempt 1 that
	// legally consumes its full per-attempt budget hitting the agent's own
	// MCP-init deadline (~240s on Auggie) leaves 0s for a retry. The generic
	// retry loop would then trip the "shared handshake budget exhausted before
	// attempt 2" guard at :1549 and emit a misleading log even though a retry
	// on the same wall-clock cannot possibly succeed against the same agent-
	// internal gate. Cap to a single honest attempt in extended mode; a real
	// retry on a genuinely-cold process needs a fresh wall-clock window, not a
	// second lap inside the same one.
	if extendedBudget && effectiveMaxAttempts > effectiveMaxAttemptsForBudget(true) {
		effectiveMaxAttempts = effectiveMaxAttemptsForBudget(true)
	}

	// Bounded total wall-clock budget (mitto-8d7): a deadline-less (or very generous)
	// caller context would otherwise let the retry loop burn the full
	// effectiveMaxAttempts × sessionCreateAttemptTimeout (~75s) on a hung transport —
	// the evidence showed ctx_remaining_ms=-1, so the per-attempt remaining-budget
	// fail-fast in shouldFailFastCreateAttempt never tripped. Derive a budgetCtx that
	// caps the whole sequence; we only ever tighten the caller's deadline, never extend
	// it. This makes the existing remaining-budget bail active for every caller and
	// guarantees NewSession returns within totalBudget.
	budgetCtx := ctx
	if dl, ok := ctx.Deadline(); !ok || time.Until(dl) > totalBudget {
		var budgetCancel context.CancelFunc
		budgetCtx, budgetCancel = context.WithTimeout(ctx, totalBudget)
		defer budgetCancel()
	}

	// Warm-once barrier (mitto-54k.3): at a genuinely COLD shared ACP process the
	// first session/new triggers the agent's full MCP handshake (Auggie re-handshakes
	// ALL its MCP servers on EVERY session/new — mitto-29q — so N stampeding cold
	// ops = N full handshakes competing on the agent's single event loop). We admit
	// ONE cold caller through the capacity-1 gate as the barrier holder, let it run
	// the RPC (which latches mcpInitDone on success — see line ~1507 below), and
	// keep the gate held via `defer release()` until it warms the process. Every
	// other cold caller that arrives while the holder is warming waits on the gate,
	// then on acquire finds mcpInitDone=true, releases the gate IMMEDIATELY and
	// re-computes its own budgets via coldMCPBudget — which now returns warm
	// (extendedBudget=false, normal 25s budget). The barrier ENTRY condition uses
	// the raw cold predicate (MCPInitTimeout>0 && !mcpInitDone) rather than
	// extendedBudget, so mitto-29q warm per-session re-handshakes
	// (mcpInitDone=true, mcpInitInProgress=true → extendedBudget=true) do NOT
	// serialize on the barrier — they still bypass it and keep their extended
	// budget from the UNCHANGED coldMCPBudget. On holder RPC FAILURE the deferred
	// release still fires so the next queued caller becomes the new (still-cold)
	// holder — no caller stranded. budgetCtx bounds the wait so a wedged holder
	// can't block past MCPInitTimeout.
	if p.config.MCPInitTimeout > 0 && !p.mcpInitDone.Load() {
		release, err := p.acquireColdStartGate(budgetCtx)
		if err != nil {
			return nil, fmt.Errorf("session/new: context cancelled while waiting for cold-start gate: %w", err)
		}
		// Post-acquire warmth re-check: if the barrier holder warmed the process
		// while we waited, release the gate immediately (do NOT hold it through
		// our RPC) and recompute budgets so we run as a warm caller (normal 25s
		// per-attempt budget, extendedBudget=false). Only a caller still cold on
		// acquire keeps the gate via `defer release()` and holds it through its
		// RPC — mcpInitDone latches on RPC success BEFORE deferred release runs,
		// so the gate is inherently held "until warm."
		if p.mcpInitDone.Load() {
			release()
			perAttemptBudget, totalBudget, extendedBudget = p.coldMCPBudget(len(mcpServers) > 0)
			_ = totalBudget // budgetCtx already derived above; wider ceiling is acceptable
		} else {
			defer release()
		}
	}

	// Arm the MCP-init timeout watch so a hard timeout signal from the agent's
	// stderr can abort the pending RPC promptly (mitto-8ul.1). Only meaningful for
	// requests that carry MCP servers on a not-yet-warm process; harmless otherwise.
	mcpTimeoutCh := p.beginMCPInitWindow()

	// Bounded retry-with-jitter loop (mitto-4no7): mirrors SetSessionModel's policy so
	// transient deadline failures on session/new are retried up to effectiveMaxAttempts.
	// Each attempt gets a fresh per-attempt budget, preserving the documented 25s
	// per-attempt create deadline (mitto-63o8) — or the extended MCP-init budget for
	// cold sessions with MCP servers (mitto-8ul.1) — without regression. In probe mode
	// effectiveMaxAttempts=1, limiting the probe to a single attempt.
	var lastErr error
	for attempt := 1; attempt <= effectiveMaxAttempts; attempt++ {
		// Honour caller cancellation / total budget before each attempt.
		if budgetCtx.Err() != nil {
			// Observability (mitto-1ut): distinguish the diagnosable "caller budget
			// exhausted before we could even start / retry" outcome from a plain
			// cancellation. When the caller passed a deadline (e.g. the resume path's
			// shared handshake deadline) that was already spent by the time we reached
			// this attempt boundary — the classic "context cancelled before attempt 2"
			// wedge symptom — say so explicitly and report how much wall-clock elapsed,
			// so logs point at the truncated budget rather than a raw deadline.
			if attempt > 1 && errors.Is(budgetCtx.Err(), context.DeadlineExceeded) {
				return nil, fmt.Errorf(
					"session/new: shared handshake budget exhausted before attempt %d (%dms elapsed, per-attempt budget %dms, extended_mcp=%t); no budget left to retry: %w",
					attempt, time.Since(totalStart).Milliseconds(), perAttemptBudget.Milliseconds(), extendedBudget, budgetCtx.Err())
			}
			return nil, fmt.Errorf("session/new: context cancelled before attempt %d: %w", attempt, budgetCtx.Err())
		}

		// Mid-flight fail-fast (mitto-13ck.2): once a sibling caller has tripped the
		// saturation flag, bail at the next retry boundary instead of draining another
		// full per-attempt budget on a process that is not responding. Also bail if the
		// remaining total budget can no longer fund a full attempt — budgetCtx always
		// carries a deadline now (mitto-8d7), so this bail is active for every caller.
		{
			hasDeadline := false
			var remaining time.Duration
			if dl, ok := budgetCtx.Deadline(); ok {
				hasDeadline = true
				remaining = time.Until(dl)
			}
			if bail, reason := shouldFailFastCreateAttempt(attempt, p.isSaturated(), hasDeadline, remaining, perAttemptBudget); bail {
				// Feed the budget-exhaustion bail into the rate/rolling-window trigger
				// (mitto-5eq). This is the "bails don't count" gap: nothing was actually
				// attempted so the consecutive fast path stays untouched, but a stream of
				// bails on the same process IS evidence of intermittent degradation and
				// should promote saturation via the rate signal. Skip during a cold-start
				// MCP-init window (extendedBudget) since that latency isn't saturation
				// evidence, mirroring recordRPCTimeout's gating.
				if !extendedBudget {
					p.recordRPCBudgetBail()
				}
				// mitto-wub: see the isSaturated() bail above — wrap the sentinel too.
				return nil, fmt.Errorf("session/new: %s (after %d attempt(s)); failing fast: %w: %w", reason, attempt-1, acperrors.ErrProcessSaturated, context.DeadlineExceeded)
			}
		}

		// Jittered backoff between retries (skip before first attempt). Mirrors set_model
		// (mitto-4no7): de-correlates concurrent callers that would retry in lock-step.
		if attempt > 1 {
			jitter := time.Duration(rand.Int63n(int64(float64(sessionCreateRetryBaseDelay) * sessionCreateRetryJitterRatio)))
			delay := time.Duration(attempt-1)*sessionCreateRetryBaseDelay + jitter
			select {
			case <-time.After(delay):
			case <-budgetCtx.Done():
				return nil, fmt.Errorf("session/new: context cancelled during retry backoff: %w", budgetCtx.Err())
			}
		}

		// Fresh per-attempt sub-context so each attempt gets a full create budget,
		// capped by the remaining total budget (budgetCtx).
		attemptCtx, attemptCancel := context.WithTimeout(budgetCtx, perAttemptBudget)

		ctxRemainingMs := int64(-1)
		if dl, ok := budgetCtx.Deadline(); ok {
			ctxRemainingMs = time.Until(dl).Milliseconds()
		}

		// Wire MCP-init hard-timeout abort (mitto-8ul.1): if the agent's stderr says
		// its own MCP-init wait timed out we cancel the attempt context so the RPC
		// returns immediately with context.Canceled rather than draining the full
		// per-attempt budget on a request the agent has already given up on.
		rpcCtx, rpcCancel := attemptCtx, attemptCancel
		if extendedBudget {
			var stopWatch context.CancelFunc
			rpcCtx, stopWatch = context.WithCancel(attemptCtx)
			done := make(chan struct{})
			go func() {
				select {
				case <-mcpTimeoutCh:
					stopWatch()
				case <-done:
				}
			}()
			// Ensure we release the watcher when the attempt completes.
			rpcCancel = func() {
				close(done)
				stopWatch()
				attemptCancel()
			}
		}

		rpcStart := time.Now()
		sessResp, err := conn.NewSession(rpcCtx, acp.NewSessionRequest{
			Cwd:        cwd,
			McpServers: mcpServers,
		})
		rpcDuration := time.Since(rpcStart)
		rpcCancel()

		if err == nil {
			p.recordRPCSuccess()
			p.markMCPInitDone()
			p.mcpInitInProgress.Store(false) // close the MCP-init window (mitto-29q)
			p.mcpInitMu.Lock()
			p.mcpInitInProgressSince = time.Time{}
			p.mcpInitMu.Unlock()
			traceSessionResponse(p.logger, "new", sessResp)
			// mitto-886: local-profile fallback (branch 3) is applied later on the
			// BackgroundSession side via applySynthesizedModelsIfEmpty (config not in
			// scope here). Pass "" so this log line remains byte-identical to the
			// pre-mitto-886 output; the fallback, when it happens, logs its own
			// "ACP session model fallback" line.
			conversation.LogSessionConfigOptions(p.logger, "new", sessResp.ConfigOptions, "")
			models, modelCfgId := conversation.ModelStateFromConfigOptions(sessResp.ConfigOptions)
			if models == nil {
				// Fall back to the SDK-decoded top-level `models` field (Auggie
				// ships its catalog there rather than in configOptions[]; mitto-i8n).
				models = conversation.ModelStateFromACP(sessResp.Models)
			}
			handle := &conversation.SessionHandle{
				SessionID:     string(sessResp.SessionId),
				Process:       p,
				Modes:         sessResp.Modes,
				Models:        models,
				ModelConfigId: modelCfgId,
			}
			if caps != nil {
				handle.Capabilities = *caps
			}
			if p.logger != nil {
				p.logger.Info("Created new ACP session on shared process",
					"acp_session_id", handle.SessionID,
					"attempt", attempt,
					"total_ms", time.Since(totalStart).Milliseconds(),
					"rpc_new_session_ms", rpcDuration.Milliseconds(),
					"extended_mcp_budget", extendedBudget,
					"per_attempt_budget_ms", perAttemptBudget.Milliseconds(),
					"cold_start_id", coldstart.FromContext(rpcCtx).ID())
			}
			return handle, nil
		}

		// MCP-init hard timeout (mitto-8ul.1): the agent already reported it gave up
		// on its own MCP-init wait. Surface an actionable, deadline-classified error
		// so classification promotes it to a permanent (non-retryable) failure and the
		// UI can render a meaningful message instead of "context deadline exceeded".
		if p.mcpInitTimedOut.Load() {
			p.recordRPCTimeout()
			lastErr = fmt.Errorf("session/new: mcp initialization timed out (agent reported MCP-init wait exhausted): %w", context.DeadlineExceeded)
			if p.logger != nil {
				p.logger.Warn("SharedACPProcess.NewSession aborted by MCP-init-timeout signal",
					"attempt", attempt, "rpc_ms", rpcDuration.Milliseconds())
			}
			return nil, lastErr
		}

		lastErr = err
		// Saturation accounting. Two distinct deadline signatures feed the
		// mitto-13ck.2 consecutive-timeout counter (which promotes the process to
		// saturated → GC Tier 5/6 recycle):
		//
		//  1. Mitto's own per-attempt deadline (errors.Is(DeadlineExceeded)). A
		//     cold-start-with-MCP window uses the extended budget deliberately, so a
		//     deadline on THAT call is expected agent-side latency, not evidence the
		//     shared process is hung — do NOT count it while extendedBudget is set.
		//     Once the window closes (first successful RPC → mcpInitDone) the normal
		//     accounting applies again (mitto-8ul.1).
		//
		//  2. The agent's OWN internal deadline delivered as -32603 with
		//     "context deadline exceeded" in its data — the auggie session/new wedge.
		//     This IS counted even under extendedBudget: it is not Mitto's extended
		//     budget being consumed but the agent explicitly reporting its handler
		//     timed out, which is direct evidence the process is wedged. Without this
		//     the wedge (rpc_ms=30000, ctx_remaining_ms=29999, rpc_code=-32603) never
		//     tripped saturation, so the wedged process was never recycled and the
		//     interactive prompt stayed gated behind a dead session/new handler.
		//  3. The agent's OWN "query closed before response received" wedge
		//     (mitto-aoo): a JSON-RPC -32603 returned in 1-10ms, not a deadline.
		//     Direct evidence the agent's query loop is torn down and will never
		//     complete another session/new. Not gated on extendedBudget: a
		//     1-10ms failure cannot be cold-start MCP latency.
		if isAgentInternalDeadlineErr(err) {
			p.recordRPCTimeout()
		} else if isAgentQueryClosedErr(err) {
			p.recordRPCWedgeFailure()
		} else if errors.Is(err, context.DeadlineExceeded) && !extendedBudget {
			p.recordRPCTimeout()
		}
		if p.logger != nil {
			rpcCode, _ := rpcErrorCode(err)
			p.logger.Warn("SharedACPProcess.NewSession failed",
				"attempt", attempt,
				"max_attempts", sessionCreateMaxAttempts,
				"rpc_ms", rpcDuration.Milliseconds(),
				"ctx_remaining_ms", ctxRemainingMs,
				"rpc_code", rpcCode,
				"agent_internal_deadline", isAgentInternalDeadlineErr(err),
				"agent_query_closed", isAgentQueryClosedErr(err),
				"extended_mcp_budget", extendedBudget,
				"cold_start_id", coldstart.FromContext(rpcCtx).ID(),
				"error", err)
		}

		// Non-transient errors are not retried.
		if !isRetryableCreateError(err) {
			return nil, fmt.Errorf("failed to create session: %w", err)
		}
	}

	return nil, fmt.Errorf("session/new failed after %d attempts: %w", effectiveMaxAttempts, lastErr)
}

// LoadSession attempts to load/resume an existing ACP session.
func (p *SharedACPProcess) LoadSession(ctx context.Context, acpSessionID, cwd string, mcpServers []acp.McpServer) (*conversation.SessionHandle, error) {
	p.beginRPC()
	defer p.endRPC()

	totalStart := time.Now()

	p.mu.RLock()
	conn := p.conn
	caps := p.capabilities
	processDone := p.processDone
	p.mu.RUnlock()

	if conn == nil {
		return nil, fmt.Errorf("shared ACP process is not running")
	}

	// Liveness check: fail fast if the OS process is already confirmed dead.
	if processDone != nil {
		select {
		case <-processDone:
			return nil, fmt.Errorf("shared ACP process has exited")
		default:
		}
	}

	// Saturation fail-fast (mitto-13ck.2): see NewSession. A hung/overloaded shared
	// process makes session/load hang its full deadline; fail fast instead.
	if p.isSaturated() {
		// mitto-wub: wrap acperrors.ErrProcessSaturated too (see NewSession above).
		return nil, fmt.Errorf("shared ACP process is saturated (repeated RPC timeouts); failing fast: %w: %w", acperrors.ErrProcessSaturated, context.DeadlineExceeded)
	}

	if caps == nil || !caps.LoadSession {
		return nil, fmt.Errorf("agent does not support session loading")
	}

	if cwd == "" {
		cwd = "."
	}

	// Entry guard (mitto-13ck.2): if the caller's context is already done on entry,
	// fail fast with the real cause WITHOUT recording an RPC timeout. An expired
	// caller budget is not evidence the shared process is hung — recording it would
	// inflate the saturation counter with a false signal.
	if err := ctx.Err(); err != nil {
		if p.logger != nil {
			p.logger.Info("SharedACPProcess.LoadSession: context already done on entry; failing fast",
				"acp_session_id", acpSessionID, "error", err)
		}
		return nil, fmt.Errorf("session/load: context already done on entry: %w", err)
	}

	ctxRemainingMs := int64(-1)
	if dl, ok := ctx.Deadline(); ok {
		ctxRemainingMs = time.Until(dl).Milliseconds()
	}
	ctxAlreadyExpired := ctx.Err() != nil

	// Extended MCP-init budget (mitto-8ul.1): symmetric with NewSession. session/load
	// on a cold process with MCP servers can also block on the agent's MCP-init wait,
	// so widen the deadline for that first call only. Wraps ctx with a sub-context so
	// the caller's own deadline is still honoured (we never extend it).
	rpcCtx := ctx
	perAttemptBudget, _, extendedBudget := p.coldMCPBudget(len(mcpServers) > 0)

	// Warm-once barrier (mitto-54k.3): symmetric with NewSession. Admit ONE cold
	// caller as the barrier holder, hold the gate via `defer release()` through
	// its RPC (mcpInitDone latches on success at line ~1732 BEFORE the deferred
	// release runs, so the gate is inherently held "until warm"). Other cold
	// callers that arrive while the holder is warming wait on the gate, then on
	// acquire find mcpInitDone=true, release the gate IMMEDIATELY and recompute
	// their own budgets via coldMCPBudget — which now returns warm (normal 25s
	// per-attempt budget, extendedBudget=false). The barrier ENTRY condition uses
	// the raw cold predicate (MCPInitTimeout>0 && !mcpInitDone) rather than
	// extendedBudget, so mitto-29q warm per-session re-handshakes
	// (mcpInitDone=true, mcpInitInProgress=true → extendedBudget=true) do NOT
	// serialize on the barrier — they still bypass it and keep their extended
	// budget from the UNCHANGED coldMCPBudget. On holder RPC FAILURE the deferred
	// release still fires so the next queued caller becomes the new (still-cold)
	// holder — no caller stranded. ctx bounds the wait so a wedged holder can't
	// block past MCPInitTimeout.
	if p.config.MCPInitTimeout > 0 && !p.mcpInitDone.Load() {
		release, gateErr := p.acquireColdStartGate(ctx)
		if gateErr != nil {
			return nil, fmt.Errorf("session/load: context cancelled while waiting for cold-start gate: %w", gateErr)
		}
		if p.mcpInitDone.Load() {
			release()
			perAttemptBudget, _, extendedBudget = p.coldMCPBudget(len(mcpServers) > 0)
		} else {
			defer release()
		}
	}

	var mcpTimeoutCh <-chan struct{}
	if extendedBudget {
		mcpTimeoutCh = p.beginMCPInitWindow()
		if dl, ok := ctx.Deadline(); !ok || time.Until(dl) > perAttemptBudget {
			var loadCancel context.CancelFunc
			rpcCtx, loadCancel = context.WithTimeout(ctx, perAttemptBudget)
			defer loadCancel()
		}
		// Wire hard-timeout abort so the agent's stderr signal cancels the RPC.
		var abortCancel context.CancelFunc
		rpcCtx, abortCancel = context.WithCancel(rpcCtx)
		defer abortCancel()
		done := make(chan struct{})
		defer close(done)
		go func() {
			select {
			case <-mcpTimeoutCh:
				abortCancel()
			case <-done:
			}
		}()
	}

	rpcStart := time.Now()
	loadSessionID := acp.SessionId(acpSessionID)
	p.beginLoadReplaySuppression(loadSessionID)
	defer p.endLoadReplaySuppression(loadSessionID)
	loadResp, err := conn.LoadSession(rpcCtx, acp.LoadSessionRequest{
		SessionId:  loadSessionID,
		Cwd:        cwd,
		McpServers: mcpServers,
	})
	rpcDuration := time.Since(rpcStart)

	if err != nil {
		if p.mcpInitTimedOut.Load() {
			p.recordRPCTimeout()
			if p.logger != nil {
				p.logger.Warn("SharedACPProcess.LoadSession aborted by MCP-init-timeout signal",
					"acp_session_id", acpSessionID, "rpc_ms", rpcDuration.Milliseconds())
			}
			return nil, fmt.Errorf("session/load: mcp initialization timed out (agent reported MCP-init wait exhausted): %w", context.DeadlineExceeded)
		}
		if errors.Is(err, context.DeadlineExceeded) && !extendedBudget {
			p.recordRPCTimeout()
		} else if isAgentQueryClosedErr(err) {
			// mitto-aoo: fast "query closed before response received" wedge,
			// not a deadline. Not gated on extendedBudget (see NewSession).
			p.recordRPCWedgeFailure()
		}
		if p.logger != nil {
			p.logger.Info("SharedACPProcess.LoadSession failed",
				"acp_session_id", acpSessionID,
				"rpc_ms", rpcDuration.Milliseconds(),
				"ctx_remaining_ms", ctxRemainingMs,
				"ctx_already_expired", ctxAlreadyExpired,
				"extended_mcp_budget", extendedBudget,
				"agent_query_closed", isAgentQueryClosedErr(err),
				"cold_start_id", coldstart.FromContext(rpcCtx).ID(),
				"error", err)
		}
		return nil, fmt.Errorf("failed to load session: %w", err)
	}

	p.recordRPCSuccess()
	p.markMCPInitDone()
	p.mcpInitInProgress.Store(false) // close the MCP-init window (mitto-29q)
	p.mcpInitMu.Lock()
	p.mcpInitInProgressSince = time.Time{}
	p.mcpInitMu.Unlock()
	traceSessionResponse(p.logger, "load", loadResp)
	conversation.LogSessionConfigOptions(p.logger, "load", loadResp.ConfigOptions, "")
	loadModels, loadModelCfgId := conversation.ModelStateFromConfigOptions(loadResp.ConfigOptions)
	if loadModels == nil {
		loadModels = conversation.ModelStateFromACP(loadResp.Models)
	}
	handle := &conversation.SessionHandle{
		SessionID:     acpSessionID,
		Capabilities:  *caps,
		Modes:         loadResp.Modes,
		Models:        loadModels,
		ModelConfigId: loadModelCfgId,
		Process:       p,
	}

	if p.logger != nil {
		p.logger.Info("Loaded ACP session on shared process",
			"acp_session_id", acpSessionID,
			"total_ms", time.Since(totalStart).Milliseconds(),
			"rpc_load_session_ms", rpcDuration.Milliseconds(),
			"extended_mcp_budget", extendedBudget,
			"cold_start_id", coldstart.FromContext(rpcCtx).ID())
	}

	return handle, nil
}

// ResumeSession attempts to resume an existing ACP session without replaying history.
// This is faster than LoadSession but requires the agent to support session/resume
// and still have the session in memory.
func (p *SharedACPProcess) ResumeSession(ctx context.Context, acpSessionID, cwd string, mcpServers []acp.McpServer) (*conversation.SessionHandle, error) {
	p.beginRPC()
	defer p.endRPC()

	totalStart := time.Now()

	p.mu.RLock()
	conn := p.conn
	caps := p.capabilities
	processDone := p.processDone
	p.mu.RUnlock()

	if conn == nil {
		return nil, fmt.Errorf("shared ACP process is not running")
	}

	// Liveness check: fail fast if the OS process is already confirmed dead.
	if processDone != nil {
		select {
		case <-processDone:
			return nil, fmt.Errorf("shared ACP process has exited")
		default:
		}
	}

	// Check capability
	if caps == nil || caps.SessionCapabilities.Resume == nil {
		return nil, fmt.Errorf("agent does not support session resume (UNSTABLE API)")
	}

	if cwd == "" {
		cwd = "."
	}

	rpcStart := time.Now()
	resumeResp, err := conn.ResumeSession(ctx, acp.ResumeSessionRequest{
		SessionId:  acp.SessionId(acpSessionID),
		Cwd:        cwd,
		McpServers: mcpServers,
	})
	rpcDuration := time.Since(rpcStart)

	if err != nil {
		if p.logger != nil {
			p.logger.Info("SharedACPProcess.ResumeSession failed",
				"acp_session_id", acpSessionID,
				"rpc_ms", rpcDuration.Milliseconds(),
				"error", err)
		}
		return nil, fmt.Errorf("failed to resume session: %w", err)
	}

	traceSessionResponse(p.logger, "resume", resumeResp)
	conversation.LogSessionConfigOptions(p.logger, "resume", resumeResp.ConfigOptions, "")
	resumeModels, resumeModelCfgId := conversation.ModelStateFromConfigOptions(resumeResp.ConfigOptions)
	if resumeModels == nil {
		resumeModels = conversation.ModelStateFromACP(resumeResp.Models)
	}
	handle := &conversation.SessionHandle{
		SessionID:     acpSessionID,
		Capabilities:  *caps,
		Modes:         resumeResp.Modes,
		Models:        resumeModels,
		ModelConfigId: resumeModelCfgId,
		Process:       p,
	}

	if p.logger != nil {
		p.logger.Info("Resumed ACP session on shared process (UNSTABLE API)",
			"acp_session_id", acpSessionID,
			"total_ms", time.Since(totalStart).Milliseconds(),
			"rpc_resume_session_ms", rpcDuration.Milliseconds())
	}

	return handle, nil
}

// RegisterSession registers per-session callbacks with the MultiplexClient.
func (p *SharedACPProcess) RegisterSession(sessionID acp.SessionId, callbacks *conversation.SessionCallbacks) {
	p.client.RegisterSession(sessionID, callbacks)
}

// UnregisterSession removes per-session callbacks.
func (p *SharedACPProcess) UnregisterSession(sessionID acp.SessionId) {
	p.client.UnregisterSession(sessionID)
}

// ProcessDone returns a channel that is closed when the ACP OS process exits.
// This provides faster death detection than conn.Done() which relies on pipe EOF.
// Returns nil if the process has not been started yet.
func (p *SharedACPProcess) ProcessDone() <-chan struct{} {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.processDone
}

// Prompt sends a prompt to a specific session on this shared process.
func (p *SharedACPProcess) Prompt(ctx context.Context, sessionID acp.SessionId, content []acp.ContentBlock) (acp.PromptResponse, error) {
	p.beginRPC()
	defer p.endRPC()

	p.mu.RLock()
	conn := p.conn
	p.mu.RUnlock()

	if conn == nil {
		return acp.PromptResponse{}, fmt.Errorf("shared ACP process is not running")
	}

	return conn.Prompt(ctx, acp.PromptRequest{
		SessionId: sessionID,
		Prompt:    content,
	})
}

// ActiveRPCs returns the number of in-flight RPCs on this process (session/prompt,
// session/load, and session/new). Used by the GC to avoid killing the process
// while it is actively serving requests.
func (p *SharedACPProcess) ActiveRPCs() int32 {
	return p.activeRPCs.Load()
}

func (p *SharedACPProcess) beginRPC() {
	p.activeRPCMu.Lock()
	if p.activeRPCs.Add(1) == 1 {
		p.activeRPCIdleCh = make(chan struct{})
	}
	p.activeRPCMu.Unlock()
}

func (p *SharedACPProcess) endRPC() {
	p.activeRPCMu.Lock()
	if p.activeRPCs.Add(-1) == 0 && p.activeRPCIdleCh != nil {
		close(p.activeRPCIdleCh)
		p.activeRPCIdleCh = nil
	}
	p.activeRPCMu.Unlock()
}

// WaitForQuiescence blocks until all in-flight RPCs finish, the process exits,
// or ctx is cancelled. The transition notification prevents deferred auxiliary
// work from missing short idle windows between fixed polling instants.
func (p *SharedACPProcess) WaitForQuiescence(ctx context.Context) bool {
	p.activeRPCMu.Lock()
	if p.activeRPCs.Load() == 0 {
		p.activeRPCMu.Unlock()
		return true
	}
	idleCh := p.activeRPCIdleCh
	if idleCh == nil {
		idleCh = make(chan struct{})
		p.activeRPCIdleCh = idleCh
	}
	p.activeRPCMu.Unlock()

	p.mu.RLock()
	processDone := p.processDone
	p.mu.RUnlock()
	select {
	case <-idleCh:
		return true
	case <-processDone:
		return false
	case <-ctx.Done():
		return false
	}
}

// memorySample returns effective memory and the RSS breakdown from one process
// topology snapshot. Tier 4 uses this unified sample to avoid repeated walks.
func (p *SharedACPProcess) memorySample() (processMemorySample, error) {
	p.mu.RLock()
	if p.cmd == nil || p.cmd.Process == nil {
		p.mu.RUnlock()
		return processMemorySample{}, fmt.Errorf("shared ACP process is not running")
	}
	pid := p.cmd.Process.Pid
	p.mu.RUnlock()

	return sampleProcessTreeMemory(pid)
}

// Cancel cancels the current operation for a specific session.
func (p *SharedACPProcess) Cancel(ctx context.Context, sessionID acp.SessionId) error {
	p.mu.RLock()
	conn := p.conn
	p.mu.RUnlock()

	if conn == nil {
		return nil
	}

	return conn.Cancel(ctx, acp.CancelNotification{SessionId: sessionID})
}

// SetSessionMode sets the mode for a specific session.
func (p *SharedACPProcess) SetSessionMode(ctx context.Context, sessionID acp.SessionId, modeID string) error {
	p.mu.RLock()
	conn := p.conn
	p.mu.RUnlock()

	if conn == nil {
		return fmt.Errorf("shared ACP process is not running")
	}

	_, err := conn.SetSessionMode(ctx, acp.SetSessionModeRequest{
		SessionId: sessionID,
		ModeId:    acp.SessionModeId(modeID),
	})
	return err
}

// SetSessionModel sets the model for a specific session.
// It serialises concurrent callers via setModelSem (one in-flight RPC at a time per
// process) and retries on transient timeouts so burst startups don't race the
// serially-served agent subprocess (mitto-3q9).
func (p *SharedACPProcess) SetSessionModel(ctx context.Context, sessionID acp.SessionId, modelID string) error {
	// Read conn under RLock; keep existing nil-check semantics. processDone is
	// re-read fresh AFTER the semaphore acquisition below (mitto-qy0j) since a
	// long queueing wait can span a process restart — capturing it here would
	// be immediately superseded and stale.
	p.mu.RLock()
	conn := p.conn
	p.mu.RUnlock()

	if conn == nil {
		return fmt.Errorf("shared ACP process is not running")
	}

	// Saturation fail-fast (mitto-13ck.1, reusing the mitto-13ck.2 state machine): if
	// recent RPCs against this shared process have repeatedly timed out, it is hung or
	// overloaded. Fail fast instead of exhausting all attempts (each an 8s hang) and
	// leaving aux sessions on the wrong model. A single alive-but-slow set_model on a
	// NON-saturated process still gets its full per-attempt budget (mitto-f7q) — only an
	// already-tripped saturation flag short-circuits here.
	if p.isSaturated() {
		// mitto-wub: wrap acperrors.ErrProcessSaturated too (see NewSession above).
		return fmt.Errorf("set_model: shared ACP process is saturated (repeated RPC timeouts); failing fast: %w: %w", acperrors.ErrProcessSaturated, context.DeadlineExceeded)
	}

	// Acquire the per-process serialisation semaphore, respecting caller ctx.
	// This ensures only one set_model RPC is in-flight at a time — concurrent
	// callers queue here instead of racing the serially-served agent subprocess.
	setModelWaitStart := time.Now()
	select {
	case p.setModelSem <- struct{}{}:
		if p.logger != nil {
			p.logger.Debug("set_model_sem acquired",
				"wait_ms", time.Since(setModelWaitStart).Milliseconds(),
				"permits_in_use", len(p.setModelSem),
				"capacity", cap(p.setModelSem),
				"cold_start_id", coldstart.FromContext(ctx).ID())
		}
		defer func() { <-p.setModelSem }()
	case <-ctx.Done():
		return fmt.Errorf("set_model: cancelled while waiting for serialization slot: %w", ctx.Err())
	}

	// mitto-qy0j: re-check saturation/liveness AFTER winning the serialization
	// slot, using freshly-read process state. The pre-semaphore checks above
	// only see the process as it was BEFORE this caller queued — a sibling
	// call ahead of us (e.g. another child conversation requesting the same
	// model) may have exhausted its own attempts and tripped saturation, or
	// the process may have died/been replaced entirely, while we waited.
	// Without this recheck every queued sibling still burns a full attempt-1
	// budget (up to 20s) against an already-confirmed-degraded process before
	// the mid-flight (attempt>1) fail-fast below ever gets a chance to apply —
	// turning N sibling requests into N independent deadline/recycle cycles
	// instead of one failure epoch plus fast fails for the rest.
	p.mu.RLock()
	freshConn := p.conn
	processDone := p.processDone
	p.mu.RUnlock()
	if freshConn == nil {
		return fmt.Errorf("set_model: shared ACP process is not running")
	}
	if freshConn != conn {
		// The process was restarted while we queued: our sessionID belongs to
		// a generation that no longer exists on the new connection. Fail so
		// the caller re-derives a fresh generation/session instead of issuing
		// an RPC that can only ever return "session not found".
		return fmt.Errorf("set_model: shared ACP process was replaced while waiting for the serialization slot")
	}
	select {
	case <-processDone:
		return fmt.Errorf("set_model: ACP process has exited while waiting for the serialization slot")
	default:
	}
	if p.isSaturated() {
		return fmt.Errorf("set_model: shared ACP process became saturated while waiting for the serialization slot; failing fast: %w: %w", acperrors.ErrProcessSaturated, context.DeadlineExceeded)
	}

	// Track as an active RPC for GC visibility (mirrors other methods).
	p.beginRPC()
	defer p.endRPC()

	var lastErr error
	for attempt := 1; attempt <= setSessionModelMaxAttempts; attempt++ {
		// Honour caller cancellation before each attempt.
		if ctx.Err() != nil {
			return fmt.Errorf("set_model: context cancelled before attempt %d: %w", attempt, ctx.Err())
		}

		// Fail fast if the OS process is already confirmed dead (mitto-13ck.1).
		// Without this check a dead process would cause each attempt to hang for
		// the full 8 s per-attempt deadline instead of failing in microseconds.
		// Returns a non-timeout error so isRetryableSetModelError breaks the loop.
		if processDone != nil {
			select {
			case <-processDone:
				return fmt.Errorf("set_model: ACP process has exited")
			default:
			}
		}

		// Mid-flight fail-fast (mitto-13ck.1): once the shared process trips the saturation
		// flag (this call's earlier attempts or a sibling RPC repeatedly timed out), bail at
		// the next attempt boundary instead of draining another full 8s budget. Attempt 1
		// always proceeds so a single slow set_model on a healthy process keeps its budget.
		if attempt > 1 && p.isSaturated() {
			// mitto-wub: wrap acperrors.ErrProcessSaturated too (see NewSession above).
			return fmt.Errorf("set_model: shared ACP process became saturated mid-flight (after %d attempt(s)); failing fast: %w: %w", attempt-1, acperrors.ErrProcessSaturated, context.DeadlineExceeded)
		}

		// Backoff between retries (skip before first attempt).
		// Jitter (mitto-f7q, Option 3): add a random fraction up to 50% of the base
		// delay so concurrent callers de-correlate instead of retrying in lock-step.
		// attempt 2: delay ∈ [300ms, 450ms); attempt 3: ∈ [600ms, 750ms).
		if attempt > 1 {
			jitter := time.Duration(rand.Int63n(int64(float64(setSessionModelRetryBaseDelay) * setSessionModelRetryJitterRatio)))
			delay := time.Duration(attempt-1)*setSessionModelRetryBaseDelay + jitter
			select {
			case <-time.After(delay):
			case <-ctx.Done():
				return fmt.Errorf("set_model: context cancelled during retry backoff: %w", ctx.Err())
			}
		}

		// Fresh per-attempt sub-context using the attempt schedule so each attempt
		// (especially a caller that waited on the semaphore) gets its full budget.
		attemptCtx, attemptCancel := context.WithTimeout(ctx, setSessionModelAttemptTimeouts[attempt-1])

		ctxRemainingMs := int64(-1)
		if dl, ok := ctx.Deadline(); ok {
			ctxRemainingMs = time.Until(dl).Milliseconds()
		}

		// Primary path (mitto-vd5): the ACP 0.13 schema moved model semantics out
		// of the generic ConfigOptions collection into a dedicated top-level
		// 'models' field plus a dedicated session/set_model RPC. Auggie 0.34+
		// only recognises the new RPC and rejects session/set_config_option with
		// -32601 Method not found. Prefer the new RPC; fall back to the legacy
		// setter (single-shot, inside this same attempt) on -32601 so pre-0.13
		// agents keep working.
		rpcStart := time.Now()
		_, err := conn.UnstableSetSessionModel(attemptCtx, acp.UnstableSetSessionModelRequest{
			SessionId: sessionID,
			ModelId:   acp.UnstableModelId(modelID),
		})
		if code, ok := rpcErrorCode(err); ok && code == -32601 {
			// Legacy fallback for pre-0.13-schema agents that only implement
			// session/set_config_option. One-shot inside this attempt — do NOT
			// consume a separate retry-loop iteration.
			if p.logger != nil {
				p.logger.Debug("SetSessionModel: legacy fallback (pre-0.13 agent)",
					"session_id", sessionID,
					"model_id", modelID,
					"attempt", attempt)
			}
			_, err = conn.SetSessionConfigOption(attemptCtx, acp.SetSessionConfigOptionRequest{
				ValueId: &acp.SetSessionConfigOptionValueId{
					SessionId: sessionID,
					ConfigId:  conversation.ModelConfigId,
					Value:     acp.SessionConfigValueId(modelID),
				},
			})
		}
		rpcDuration := time.Since(rpcStart)
		attemptCancel()

		if err == nil {
			p.recordRPCSuccess()
			if attempt > 1 && p.logger != nil {
				p.logger.Info("SharedACPProcess.SetSessionModel succeeded after retry",
					"session_id", sessionID,
					"model_id", modelID,
					"attempt", attempt,
					"rpc_ms", rpcDuration.Milliseconds())
			}
			return nil
		}

		lastErr = err
		// mitto-qy0j: a cold/wedged agent's own internal deadline on session/set_model
		// surfaces as a client-side *acp.RequestError{-32603} manufactured by the SDK's
		// toReqErr (NOT errors.Is(err, context.DeadlineExceeded)-compatible — it has no
		// Unwrap). Without this classification these RPC timeouts fed zero saturation
		// samples, so a wedged shared process was never recycled by GC Tier 5/6 even
		// though NewSession/LoadSession already handle the identical signature via
		// isAgentInternalDeadlineErr/isAgentQueryClosedErr (mitto-y1g, mitto-aoo).
		if isAgentInternalDeadlineErr(err) {
			p.recordRPCTimeout()
		} else if isAgentQueryClosedErr(err) {
			p.recordRPCWedgeFailure()
		} else if errors.Is(err, context.DeadlineExceeded) {
			p.recordRPCTimeout()
		}
		retryable := isRetryableSetModelError(err)
		// Only the terminal failure (a non-retryable error, or the last attempt with
		// the retry budget exhausted) is logged at Warn. Intermediate retryable attempts
		// log at Debug so a best-effort switch that later succeeds — or that cleanly
		// falls back — no longer emits repeated "SetSessionModel failed" Warn noise
		// (mitto-8qp: fail once and cleanly fall back, not 3x).
		if p.logger != nil {
			logAttemptFailure := p.logger.Debug
			if setModelFailureIsTerminal(attempt, retryable) {
				logAttemptFailure = p.logger.Warn
			}
			logAttemptFailure("SharedACPProcess.SetSessionModel failed",
				"session_id", sessionID,
				"model_id", modelID,
				"attempt", attempt,
				"max_attempts", setSessionModelMaxAttempts,
				"rpc_ms", rpcDuration.Milliseconds(),
				"ctx_remaining_ms", ctxRemainingMs,
				"error", err)
		}

		// Non-transient errors are not retried (e.g. invalid model ID).
		if !retryable {
			return err
		}
	}

	return fmt.Errorf("set_model failed after %d attempts: %w", setSessionModelMaxAttempts, lastErr)
}

// setModelFailureIsTerminal reports whether a failed set_model attempt is the final
// one — i.e. the error is non-retryable, or the retry budget is exhausted. Terminal
// failures are logged at Warn; intermediate retryable attempts log at Debug so a
// best-effort switch does not emit repeated "SetSessionModel failed" Warn noise
// (mitto-8qp). Pure so the log-level decision can be unit-tested without a live RPC.
func setModelFailureIsTerminal(attempt int, retryable bool) bool {
	return !retryable || attempt >= setSessionModelMaxAttempts
}

// isRetryableSetModelError reports whether a set_model error is worth retrying.
// set_model is idempotent so retrying on timeout is safe. The agent's own internal
// deadline (-32603 "context deadline exceeded", mitto-qy0j) and the "query closed
// before response received" wedge (mitto-aoo) are treated as retryable too, mirroring
// isRetryableCreateError, so the bounded retry loop keeps retrying — and recording
// each attempt's timeout toward saturation via the classification above — instead of
// relying solely on the string-Contains fallback below.
//
// mitto-gbf5: an upstream connect-timeout brownout (UND_ERR_CONNECT_TIMEOUT /
// apiStatus=unavailable) is checked BEFORE the generic "timeout" substring
// fallback and is deliberately NOT retryable here. Unlike a local slow RPC,
// the upstream API being unreachable will not resolve within the next
// attempt's budget, so retrying just burns the full setSessionModelMaxAttempts
// budget (~43s) for no benefit and delays surfacing the real condition.
func isRetryableSetModelError(err error) bool {
	if err == nil {
		return false
	}
	if mittoAcp.IsUpstreamUnavailableError(err) {
		return false
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	if isAgentInternalDeadlineErr(err) {
		return true
	}
	if isAgentQueryClosedErr(err) {
		return true
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "deadline exceeded") ||
		strings.Contains(msg, "timeout") ||
		strings.Contains(msg, "timed out")
}

// isRetryableCreateError reports whether a session/new error is worth retrying.
// NOTE: unlike set_model, session/new is NOT idempotent — a create that times out
// MAY have succeeded server-side, so a retry can orphan a session on the shared
// process. We accept this trade-off (mitto-4no7): on a deadline we never received a
// session ID, so the only recovery is to create again; the orphan is bounded by the
// shared process lifetime. Only deadline/timeout failures are retried. The agent's
// own internal deadline (-32603 "context deadline exceeded", the session/new wedge)
// is treated as retryable too so the bounded loop records each attempt's timeout
// toward saturation rather than returning permanently after a single attempt.
func isRetryableCreateError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	if isAgentInternalDeadlineErr(err) {
		return true
	}
	if isAgentQueryClosedErr(err) {
		// mitto-aoo: retry the wedge signature too so the bounded
		// sessionCreateMaxAttempts loop records multiple consecutive failure
		// samples (recordRPCWedgeFailure) within a single NewSession call,
		// tripping saturation on the first wedged create instead of waiting
		// for sessionSaturationTimeoutThreshold separate caller attempts.
		return true
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "deadline exceeded") ||
		strings.Contains(msg, "timeout") ||
		strings.Contains(msg, "timed out")
}

// SetSessionConfigOption sets a config option for a specific session.
// TODO: Implement when SDK supports SetSessionConfigOption
func (p *SharedACPProcess) SetSessionConfigOption(ctx context.Context, sessionID acp.SessionId, configID, value string) error {
	// p.mu.RLock()
	// conn := p.conn
	// p.mu.RUnlock()

	// if conn == nil {
	// 	return fmt.Errorf("shared ACP process is not running")
	// }

	// _, err := conn.SetSessionConfigOption(ctx, acp.SetSessionConfigOptionRequest{
	// 	SessionId: sessionID,
	// 	ConfigId:  acp.SessionConfigId(configID),
	// 	Value:     acp.SessionConfigValueId(value),
	// })
	// return err
	return fmt.Errorf("SetSessionConfigOption not yet implemented in SDK")
}

// Done returns a channel that is closed when the ACP process exits.
func (p *SharedACPProcess) Done() <-chan struct{} {
	p.mu.RLock()
	conn := p.conn
	p.mu.RUnlock()

	if conn == nil {
		ch := make(chan struct{})
		close(ch)
		return ch
	}
	return conn.Done()
}

// Capabilities returns the agent's capabilities.
func (p *SharedACPProcess) Capabilities() *acp.AgentCapabilities {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.capabilities
}

// WorkingDir returns the workspace's project directory.
// Falls back to ACPCwd if WorkingDir is not set.
func (p *SharedACPProcess) WorkingDir() string {
	if p.config.WorkingDir != "" {
		return p.config.WorkingDir
	}
	return p.config.ACPCwd
}

// Close terminates the ACP process and cleans up resources.
func (p *SharedACPProcess) Close() {
	p.ctxCancel()
	p.killProcess("close")
}

// killProcess terminates the ACP process.
func (p *SharedACPProcess) killProcess(reason string) {
	if reason != "" && p.terminationIntent != nil {
		p.terminationIntent.mark(reason)
	}
	if p.cancel != nil {
		p.cancel()
	}

	if p.cmd != nil && p.cmd.Process != nil {
		// Kill the entire process group to ensure all child processes are terminated.
		// Without this, child processes (e.g., "claude" spawned by "node claude-code-acp")
		// survive and become orphans.
		mittoAcp.KillProcessGroup(p.cmd.Process.Pid)
	}

	// Remove PID tracking file
	if p.config.WorkspaceUUID != "" {
		_ = removeACPPIDFile(p.config.WorkspaceUUID, false)
	}

	if p.wait != nil {
		p.wait()
		p.wait = nil
	}
}

// canRestart checks if we can restart the process based on rate limiting.
func (p *SharedACPProcess) canRestart() bool {
	p.restartMu.Lock()
	defer p.restartMu.Unlock()

	now := time.Now()
	cutoff := now.Add(-mittoAcp.ACPRestartWindow)

	// Remove old restart timestamps
	valid := p.restartTimes[:0]
	for _, t := range p.restartTimes {
		if t.After(cutoff) {
			valid = append(valid, t)
		}
	}
	p.restartTimes = valid

	return len(p.restartTimes) < mittoAcp.MaxACPRestarts
}

// recordRestart records a restart attempt.
func (p *SharedACPProcess) recordRestart() {
	p.restartMu.Lock()
	defer p.restartMu.Unlock()
	p.restartCount++
	p.restartTimes = append(p.restartTimes, time.Now())
}

// Generation returns the current process generation, bumped each time
// Restart() actually replaces the process. See conversation.SharedProcess.Generation
// for the intended caller-side usage (snapshot before detecting/acting on a death).
func (p *SharedACPProcess) Generation() int {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.generation
}

// Restart kills the old process and starts a new one, but only if the
// process generation still matches observedGen. Pass
// conversation.RestartAnyGeneration to force an unconditional restart.
// All sessions must re-register their callbacks and LoadSession after restart.
// Returns nil on success (including the already-remediated no-op case).
// Returns an *mittoAcp.ACPClassifiedError for permanent failures.
//
// Idempotency (mitto-x611): concurrent callers may each independently observe
// the same underlying process death and call Restart() around the same time.
// canRestart()/CanRestartGlobal are rate limits, not a dedup, so without the
// generation check below every concurrent caller would unconditionally kill
// whatever process is currently running — including a healthy replacement a
// prior caller already started — producing a restart storm. observedGen MUST
// be captured by the caller as early as possible (ideally at the moment the
// death was detected, before any backoff/rate-limit delay of its own) so that
// all callers reacting to the SAME death agree on the generation they intend
// to replace; it is re-checked under mu immediately before killing, since the
// rate-limit check and backoff wait below are not synchronized against other
// Restart() callers.
func (p *SharedACPProcess) Restart(observedGen int) error {
	if observedGen != conversation.RestartAnyGeneration {
		p.mu.RLock()
		curGen := p.generation
		p.mu.RUnlock()
		if curGen != observedGen {
			if p.logger != nil {
				p.logger.Info("Shared ACP process already restarted by another caller, skipping redundant restart",
					"command", p.config.ACPCommand,
					"cwd", p.config.ACPCwd)
			}
			return nil
		}
	}

	if !p.canRestart() {
		return fmt.Errorf("restart limit exceeded (%d restarts in %v)", mittoAcp.MaxACPRestarts, mittoAcp.ACPRestartWindow)
	}

	// Check global (cross-workspace) restart rate limiter before proceeding.
	if p.config.CanRestartGlobal != nil && !p.config.CanRestartGlobal() {
		return fmt.Errorf("global restart limit exceeded (cross-workspace cooldown active)")
	}

	// Apply backoff based on how many recent restarts have occurred.
	p.restartMu.Lock()
	recentCount := len(p.restartTimes)
	p.restartMu.Unlock()

	if recentCount > 0 {
		delay := mittoAcp.BackoffDelay(recentCount-1, mittoAcp.ACPRestartBaseDelay, mittoAcp.ACPRestartMaxDelay, processStartRetryJitterRatio)
		if p.logger != nil {
			p.logger.Info("Waiting before restart",
				"delay", delay.String(),
				"recent_restarts", recentCount,
				"command", p.config.ACPCommand,
				"cwd", p.config.ACPCwd)
		}
		select {
		case <-p.ctx.Done():
			return fmt.Errorf("context cancelled during restart backoff: %w", p.ctx.Err())
		case <-time.After(delay):
		}
	}

	// Re-check the generation now, under mu, right before killing — the rate
	// limit check and backoff wait above are not synchronized against other
	// Restart() callers, so the process this caller observed as dead may
	// already have been replaced while we were waiting.
	p.mu.Lock()
	if observedGen != conversation.RestartAnyGeneration && p.generation != observedGen {
		p.mu.Unlock()
		if p.logger != nil {
			p.logger.Info("Shared ACP process already restarted by another caller, skipping redundant restart",
				"command", p.config.ACPCommand,
				"cwd", p.config.ACPCwd)
		}
		return nil
	}

	// mitto-ei81: bail out here if this instance was already permanently
	// retired by a concurrent Close() (e.g. a GC Tier 5 saturated-idle
	// recycle) racing this very restart. Close() cancels p.ctx exactly once
	// and it can never be un-cancelled, so proceeding to startProcess() below
	// is doomed — exec.CommandContext(p.ctx, ...) fails immediately with a
	// generic "context canceled" that looks like a transient startup failure
	// rather than what it actually is: a lost race against a legitimate
	// recycle. Detect it up front and return a distinguishable sentinel so
	// the caller knows to fetch a fresh process instead of retrying this one.
	select {
	case <-p.ctx.Done():
		p.mu.Unlock()
		if p.logger != nil {
			p.logger.Warn("Shared ACP process restart aborted: process was closed by a concurrent operation",
				"command", p.config.ACPCommand,
				"cwd", p.config.ACPCwd)
		}
		return acperrors.ErrProcessClosedConcurrently
	default:
	}

	if p.logger != nil {
		p.logger.Info("Restarting shared ACP process",
			"restart_count", p.restartCount+1,
			"command", p.config.ACPCommand,
			"cwd", p.config.ACPCwd)
	}

	p.killProcess("restart")
	p.conn = nil
	p.capabilities = nil
	p.generation++
	p.mu.Unlock()

	p.recordRestart()

	// Record in the global restart tracker (cross-workspace rate limiter).
	if p.config.RecordRestart != nil {
		p.config.RecordRestart()
	}

	if err := p.startProcess(); err != nil {
		if p.logger != nil {
			logAttrs := []any{"error", err}
			if classified, ok := err.(*mittoAcp.ACPClassifiedError); ok {
				logAttrs = append(logAttrs,
					"error_class", classified.Class.String(),
					"user_message", classified.UserMessage,
					"user_guidance", classified.UserGuidance)
			}
			p.logger.Error("Failed to restart shared ACP process", logAttrs...)
		}
		return err
	}

	if p.logger != nil {
		p.logger.Info("Shared ACP process restarted successfully",
			"command", p.config.ACPCommand)
	}

	// Notify the process manager so it can invalidate stale auxiliary sessions.
	if p.onRestart != nil {
		p.onRestart()
	}

	return nil
}

// SetOnRestart registers a callback that is called after a successful Restart().
// This allows the process manager to invalidate caches (e.g., auxiliary sessions)
// that reference old session IDs from the previous process instance.
func (p *SharedACPProcess) SetOnRestart(fn func()) {
	p.onRestart = fn
}

// strPtr returns a pointer to the given string.
func strPtr(s string) *string {
	return &s
}
