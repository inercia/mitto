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
	"strings"

	acp "github.com/coder/acp-go-sdk"
)

// ErrSharedProcessSaturated is the sentinel returned by both aux-session bail
// paths in acpproc.ACPProcessManager.getOrCreateAuxiliarySession — the
// reactive saturation bail (mitto-z70) and the proactive ActiveRPCs bail
// (mitto-9gt). Callers running their own retry loops (e.g. title-gen,
// mitto-ammz.1) can errors.Is for this sentinel and abandon their loop
// instead of piling further NewSession RPCs onto an already-struggling
// shared process.
var ErrSharedProcessSaturated = errors.New("shared ACP process is saturated")

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
