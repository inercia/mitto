// Package acperrors carries the classifier surface (sentinels + predicates) for
// shared-ACP-process failures that callers OUTSIDE internal/acpproc need to
// inspect. It intentionally has zero dependencies on internal/acpproc so
// consumer packages such as internal/conversation can import it without
// creating an import cycle (internal/acpproc imports internal/conversation for
// SessionHandle et al.).
//
// The canonical predicates and bail sites live in internal/acpproc; the
// helpers here are the classifier seam callers use to short-circuit their own
// retry loops on wedge/saturation signals (see mitto-ammz.1).
package acperrors

import (
	"context"
	"errors"
	"fmt"
	"strings"

	acp "github.com/coder/acp-go-sdk"
)

// ErrSharedProcessSaturated is the umbrella sentinel returned by all three
// aux-session bail paths in acpproc.ACPProcessManager.getOrCreateAuxiliarySession
// — the reactive saturation bail (mitto-z70), the proactive ActiveRPCs bail
// (mitto-9gt), and the MCP-init-aware bail (mitto-337). Callers running their
// own retry loops (e.g. title-gen, mitto-ammz.1) can errors.Is for this
// sentinel and abandon their loop instead of piling further NewSession RPCs
// onto an already-struggling shared process.
//
// mitto-13n.2: the three bails above are conceptually distinct conditions
// (real timeout-driven degradation vs. transient concurrent-RPC load vs. a
// wedged/in-progress MCP handshake) that historically shared this one
// sentinel, preventing callers from telling them apart. ErrProcessSaturated,
// ErrProcessBusy, and ErrMCPInitGated below give each condition its own
// identity while each wraps this umbrella so errors.Is(err,
// ErrSharedProcessSaturated) keeps working for callers that have not yet
// migrated to the granular sentinels (e.g. internal/processors/apply.go's
// string-matched isSaturationDispatchErr).
var ErrSharedProcessSaturated = errors.New("shared ACP process is saturated")

// ErrProcessSaturated is the sentinel returned by the REACTIVE saturation
// bail (mitto-z70): the shared process has already been flagged saturated by
// repeated RPC timeouts or a cold-MCP wedge. Wraps ErrSharedProcessSaturated
// for transition-era callers.
var ErrProcessSaturated = fmt.Errorf("%w: process saturated (reactive degradation)", ErrSharedProcessSaturated)

// ErrProcessBusy is the sentinel returned by the PROACTIVE load-based bail
// (mitto-9gt): the shared process is currently serving more concurrent
// user-facing RPCs than the configured threshold. This is transient
// load-shedding, not the same condition as ErrProcessSaturated — it clears as
// soon as concurrent load drops, with no GC recycle involved. Wraps
// ErrSharedProcessSaturated for transition-era callers.
var ErrProcessBusy = fmt.Errorf("%w: process busy (concurrent RPC load-shedding)", ErrSharedProcessSaturated)

// ErrMCPInitGated is the sentinel returned by the MCP-init-aware bail
// (mitto-337): the shared process has either given up on its MCP handshake
// (MCPInitTimedOut) or is still in the middle of one that has never completed
// (MCPInitInProgress && !MCPInitDone). Distinct from both saturation and
// busy-load, since the process may otherwise be quiescent. Wraps
// ErrSharedProcessSaturated for transition-era callers.
var ErrMCPInitGated = fmt.Errorf("%w: process mcp-init gated", ErrSharedProcessSaturated)

// ErrProcessClosedConcurrently is returned by SharedACPProcess.Restart when the
// target instance has already been permanently retired by a concurrent Close()
// (e.g. a GC Tier 5 saturated-idle recycle, mitto-13n.1) racing a resume's own
// restart attempt (mitto-ei81). A SharedACPProcess's process-lifetime context is
// cancelled exactly once, in Close(), and can never be un-cancelled — so once
// that has happened, retrying Restart() on the SAME instance can never succeed;
// every subsequent process-start attempt fails immediately with a generic
// "context canceled" that misleadingly looks like a transient startup failure.
// Callers hitting this sentinel must fetch a fresh process from the manager
// (e.g. ACPProcessManager.GetOrCreateProcess) instead of retrying this instance.
var ErrProcessClosedConcurrently = errors.New("shared ACP process was closed by a concurrent operation (e.g. GC recycle); a fresh process must be obtained")

// IsAgentInternalDeadlineErr reports whether err is the agent's OWN internal
// deadline firing on a session/new (or session/load) RPC — the auggie
// "session/new wedge" signature. The agent's handler completes its own
// internal timeout and returns a JSON-RPC application error -32603 ("Internal
// error") whose data carries "context deadline exceeded".
//
// Crucially this is NOT a Go context.DeadlineExceeded — it is delivered as an
// *acp.RequestError, so errors.Is(err, context.DeadlineExceeded) returns
// false. Callers that must distinguish the wedge from a plain transient
// timeout (e.g. the title-generation retry loop, mitto-ammz.1) use this
// predicate to abandon their loop early rather than burning the full 60s
// extended-MCP budget on every attempt.
//
// The canonical (package-private) implementation lives in
// internal/acpproc/shared_acp_process.go:isAgentInternalDeadlineErr; keep the
// two implementations in sync.
func IsAgentInternalDeadlineErr(err error) bool {
	if err == nil {
		return false
	}
	var re *acp.RequestError
	if !errors.As(err, &re) || re == nil || re.Code != -32603 {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "deadline exceeded") ||
		strings.Contains(msg, "context deadline")
}

// IsAgentQueryClosedErr reports whether err is the agent's "query closed before
// response received" wedge signature on a session/new (or session/load) RPC
// (mitto-aoo). Like the internal-deadline wedge above, the agent's handler
// returns a JSON-RPC application error -32603 ("Internal error"), but here the
// data carries "query closed before response received" instead of a deadline
// message — evidence the agent's internal query loop was torn down and can
// never complete another session/new, even though the process is still alive
// and answering JSON-RPC. Unlike the deadline wedge this reply is fast (1-10ms),
// not a timeout, so it previously fed NO saturation signal at all: the GC's
// Tier 5/6 recycle tiers stayed inert while every session/new failed for hours
// (observed: 38 consecutive failures over 9h). Treating it as a fast-path
// failure sample lets the mitto-13ck.2 saturation machinery (and therefore GC
// Tier 5/6 recycle) heal the wedged process.
//
// The canonical (package-private) implementation lives in
// internal/acpproc/shared_acp_process.go:isAgentQueryClosedErr; keep the two
// implementations in sync.
func IsAgentQueryClosedErr(err error) bool {
	if err == nil {
		return false
	}
	var re *acp.RequestError
	if !errors.As(err, &re) || re == nil || re.Code != -32603 {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "query closed before response received")
}

// silentStartupHangHint is the actionable diagnostic returned by
// ClassifySilentStartupHang when the pre-handshake silent-hang signature
// matches. See ClassifySilentStartupHang's doc comment for the signature.
const silentStartupHangHint = "agent appears stuck in its own startup before the ACP handshake " +
	"(no output, no handshake observed in the startup window) — it may be blocked on a " +
	"self-update or a slow/hung dependency; try running the agent CLI directly (e.g. " +
	"`<agent> --version`) and complete any pending update"

// ClassifySilentStartupHang detects the pre-handshake silent-hang signature
// (mitto-3a4): an ACP agent (e.g. github-copilot) that hangs in its OWN
// startup — before the ACP server is even listening — so Mitto's Initialize
// RPC times out on a live-but-unresponsive process with no stderr output at
// all. This is distinct from a crash (conn.Done()/processDone would have
// cancelled the init context with context.Canceled, not a deadline) and from
// a normal failure that produces stderr diagnostics.
//
// Given the Initialize-attempt error, the captured stderr output, and whether
// the OS process has already exited, it returns a non-empty actionable hint
// and matched=true only when ALL three signature conditions hold:
//  1. err indicates the per-attempt deadline fired (context.DeadlineExceeded,
//     matched either via errors.Is for a properly wrapped error, or via a
//     case-insensitive substring match on the error text for callers that
//     format the error as a plain string instead of wrapping it).
//  2. stderrOutput is empty once whitespace-trimmed (the agent's own startup
//     preamble went to its own log files, not the pipe Mitto captures).
//  3. processExited is false (the process is still alive; a crash is a
//     different failure mode).
//
// Fails open: any other input shape (nil error, non-deadline error, non-empty
// stderr, or an already-exited process) returns ("", false) so the existing
// opaque error message is unchanged for every other failure class.
func ClassifySilentStartupHang(err error, stderrOutput string, processExited bool) (string, bool) {
	if err == nil || processExited {
		return "", false
	}
	if strings.TrimSpace(stderrOutput) != "" {
		return "", false
	}
	msg := strings.ToLower(err.Error())
	isDeadline := errors.Is(err, context.DeadlineExceeded) ||
		strings.Contains(msg, "deadline exceeded") ||
		strings.Contains(msg, "context deadline")
	if !isDeadline {
		return "", false
	}
	return silentStartupHangHint, true
}
