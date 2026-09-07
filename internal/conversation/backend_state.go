package conversation

// backend_state.go maps known conversation/ACP-layer acquisition errors to
// agentbackend's neutral LifecycleState so BackendProvider/BackendLease
// callers can distinguish connection-unavailable, busy/saturated, and
// recovering-vs-terminal cases without depending on ACP-specific error types
// (mitto-lrt.7 acceptance criteria: "Distinguish connection unavailable,
// session missing, busy/saturated, authentication required, recovering, idle
// and terminal failure").

import (
	"errors"

	mittoAcp "github.com/inercia/mitto/internal/acp"
	"github.com/inercia/mitto/internal/acpproc/acperrors"
	"github.com/inercia/mitto/internal/agentbackend"
)

// ClassifyAcquireError maps err (as returned by BackendProvider.AcquireSession
// or BackendLease.Reconnect) to the neutral agentbackend.LifecycleState that
// best describes it. err is always returned unchanged (never swallowed) so
// callers can still errors.Is/errors.As against it; the state is purely
// advisory routing information.
//
// nil err classifies as LifecycleConnected. Unrecognized errors classify as
// LifecycleDisconnected rather than guessing a more specific — and possibly
// misleading — state.
func ClassifyAcquireError(err error) (agentbackend.LifecycleState, error) {
	if err == nil {
		return agentbackend.LifecycleConnected, nil
	}

	switch {
	case errors.Is(err, acperrors.ErrSharedProcessSaturated):
		// Busy/saturated: reactive degradation (ErrProcessSaturated),
		// proactive concurrent-RPC load-shedding (ErrProcessBusy), or
		// MCP-init gating (ErrMCPInitGated) all wrap this umbrella
		// sentinel. All three are transient — the caller should retry
		// later, not treat this as a terminal failure.
		return agentbackend.LifecycleReconnecting, err

	case errors.Is(err, acperrors.ErrProcessClosedConcurrently):
		// A concurrent GC recycle retired the process instance backing
		// this lease. The caller must acquire a fresh lease rather than
		// retrying this one, but the condition itself is recoverable.
		return agentbackend.LifecycleReconnecting, err

	case errors.Is(err, agentbackend.ErrSessionNotFound):
		return agentbackend.LifecycleDisconnected, err

	case errors.Is(err, agentbackend.ErrNotConnected):
		return agentbackend.LifecycleDisconnected, err
	}

	var classified *mittoAcp.ACPClassifiedError
	if errors.As(err, &classified) {
		if classified.IsRetryable() {
			return agentbackend.LifecycleReconnecting, err
		}
		// Permanent classification (e.g. missing binary/module, bad
		// config): retrying will not help.
		return agentbackend.LifecycleStopped, err
	}

	return agentbackend.LifecycleDisconnected, err
}
