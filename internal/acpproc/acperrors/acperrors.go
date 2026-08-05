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
