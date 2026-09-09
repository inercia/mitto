package agentbackend

import "sync"

// PermissionDecision is the fail-closed outcome of a client-service
// permission request. PermissionUnknown (the zero value) is a DISTINCT state
// from PermissionDenied but behaves identically for access purposes: only
// PermissionApproved grants access. An indeterminate decision (e.g. no
// responder answered, or the answer could not be attributed — see
// PermissionFence) must never be treated as an implicit grant.
type PermissionDecision int

const (
	// PermissionUnknown is the zero value: no definite decision has been
	// made yet (or the responder that would have made one was superseded).
	// Fails closed exactly like PermissionDenied.
	PermissionUnknown PermissionDecision = iota
	// PermissionApproved is the only decision that grants access.
	PermissionApproved
	// PermissionDenied explicitly refuses access.
	PermissionDenied
)

// Approved reports whether d grants access. Only PermissionApproved does;
// PermissionUnknown fails closed exactly like PermissionDenied, so a caller
// can always gate on Approved() without a separate nil/unknown check.
func (d PermissionDecision) Approved() bool { return d == PermissionApproved }

// String returns a lowercase, stable string form for logging/debugging.
func (d PermissionDecision) String() string {
	switch d {
	case PermissionApproved:
		return "approved"
	case PermissionDenied:
		return "denied"
	default:
		return "unknown"
	}
}

// PermissionFence sequences competing responders for a single outstanding
// permission request so that at most one response is ever honored. Next
// issues a monotonically increasing generation for a newly-outstanding
// request, superseding any earlier one still in flight (e.g. a session
// reconnect that re-issues the same logical request); Accept reports
// whether a response tagged with a given generation is still current. A
// response whose generation was superseded (Accept returns false) MUST be
// dropped rather than applied — it belongs to a request that is no longer
// the one awaiting an answer, so honoring it would let a stale or
// duplicate responder win over the live one. Safe for concurrent use.
type PermissionFence struct {
	mu      sync.Mutex
	current uint64
}

// Next starts a new outstanding request, superseding any prior one, and
// returns the generation the caller must tag its eventual response with.
func (f *PermissionFence) Next() uint64 {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.current++
	return f.current
}

// Accept reports whether gen is still the fence's current generation. A
// false result means gen was superseded by a later Next call and the
// associated response must be ignored (fail closed: the caller should treat
// this exactly as PermissionUnknown, never as an implicit approval).
func (f *PermissionFence) Accept(gen uint64) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return gen != 0 && gen == f.current
}
