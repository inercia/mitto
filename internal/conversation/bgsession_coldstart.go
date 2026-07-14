package conversation

// Cold-start diagnostic helpers for BackgroundSession (mitto-3mv WI-2).
//
// A cold-start Trace is created once per session on the FIRST activation
// (session creation OR resume) and finalized once when the agent is ready
// to receive prompts (or the activation fails). All emit helpers are nil-safe
// via coldstart.Trace's own nil-safety, so callers may skip nil checks.

import (
	"context"
	"time"

	"github.com/inercia/mitto/internal/coldstart"
)

// beginColdTrace lazily creates the per-session cold-start Trace and emits
// the initial phase. Safe to call multiple times: only the first call
// installs the Trace and emits the phase.
func (bs *BackgroundSession) beginColdTrace(phase string, kv ...any) {
	if bs == nil {
		return
	}
	bs.coldTraceOnce.Do(func() {
		bs.coldTrace = coldstart.New(bs.logger, bs.persistedID, bs.workspaceUUID)
	})
	bs.coldTrace.Phase(phase, kv...)
}

// coldPhase emits a phase on the session's cold-start Trace when one is
// already active. Emits nothing when the trace hasn't been begun or has
// been finalized. Nil-safe.
func (bs *BackgroundSession) coldPhase(phase string, kv ...any) {
	if bs == nil {
		return
	}
	if bs.coldTraceDone.Load() {
		return
	}
	bs.coldTrace.Phase(phase, kv...)
}

// coldPhaseFirstToken emits the "first_token" phase exactly once per trace.
// Wired to the agent-activity signal so the first streaming byte on any
// prompt in the trace's lifetime marks the ready-to-stream boundary.
func (bs *BackgroundSession) coldPhaseFirstToken() {
	if bs == nil {
		return
	}
	if bs.coldTrace == nil || bs.coldTraceDone.Load() {
		return
	}
	if !bs.coldTraceFirst.CompareAndSwap(false, true) {
		return
	}
	bs.coldTrace.Phase("first_token")
}

// markMcpInitStart records the wall time of the current MCP-init episode
// so the closing markMcpInitEnd can report elapsed time. Only records when
// no episode is already active — safe under concurrent callers and stderr
// vs. handshaker races.
func (bs *BackgroundSession) markMcpInitStart() {
	if bs == nil {
		return
	}
	bs.coldTraceMcpAt.CompareAndSwap(0, time.Now().UnixNano())
}

// markMcpInitEnd emits an "mcp_init" phase with the episode's duration when
// an MCP-init episode is active. Called from the activation success path so
// the elapsed time from the boundary to first agent readiness is visible in
// the timeline. Resets the episode marker so a future re-init (e.g. per
// session/new on Auggie) records a fresh episode. Nil-safe / no-op when no
// episode is active.
func (bs *BackgroundSession) markMcpInitEnd() {
	if bs == nil {
		return
	}
	startNanos := bs.coldTraceMcpAt.Swap(0)
	if startNanos == 0 {
		return
	}
	elapsed := time.Since(time.Unix(0, startNanos))
	bs.coldPhase("mcp_init", "episode_ms", elapsed.Milliseconds())
}

// finishColdTrace finalizes the trace once. outcome is a short label like
// "ready", "handshake_failed", or "session_creation_failed". Extra kv pairs
// are appended to the summary log. Safe to call multiple times; only the
// first invocation writes to the ring buffer.
func (bs *BackgroundSession) finishColdTrace(outcome string, kv ...any) {
	if bs == nil {
		return
	}
	if !bs.coldTraceDone.CompareAndSwap(false, true) {
		return
	}
	bs.coldTrace.Summary(outcome, kv...)
}

// coldTraceCtx wraps base with the session's active cold-start Trace so the
// acpproc RPC layer (WI-3) can recover the cold_start_id via
// coldstart.FromContext. Returns base unchanged when no trace is active, so it
// is safe to call unconditionally at every RPC context site.
func (bs *BackgroundSession) coldTraceCtx(base context.Context) context.Context {
	if bs == nil || bs.coldTrace == nil {
		return base
	}
	return coldstart.WithTrace(base, bs.coldTrace)
}
