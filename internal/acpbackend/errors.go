package acpbackend

import (
	"context"
	"errors"

	acp "github.com/coder/acp-go-sdk"

	"github.com/inercia/mitto/internal/agentbackend"
)

// jsonRPCMethodNotFound is the standard JSON-RPC "Method not found" error
// code. An ACP agent that doesn't implement an optional RPC (e.g.
// session/set_model on an agent with no model selection) replies with this
// code, which is the ACP-level signal for "unsupported" that
// agentbackend.ErrUnsupported/*UnsupportedError exist to represent uniformly.
const jsonRPCMethodNotFound = -32601

// translateError maps a raw ACP/acpproc error into the agentbackend sentinel
// errors so SessionOps callers can use errors.Is/errors.As uniformly across
// backends, instead of leaking ACP-specific *acp.RequestError values or ad
// hoc fmt.Errorf strings from acpproc. feature is only used to populate
// *UnsupportedError when a method-not-found error is detected; pass "" when
// the call site has no single feature to attribute it to. Falls through to
// the original error unchanged when no known mapping applies — the adapter
// narrows the *known* unsupported/cancelled cases, but does not synthesize a
// classification it cannot ground in the underlying error (ADR
// agent-backend-architecture.md §5: never guess).
func translateError(err error, feature agentbackend.Feature) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, context.Canceled) {
		return agentbackend.ErrCancelled
	}
	var reqErr *acp.RequestError
	if errors.As(err, &reqErr) && reqErr.Code == jsonRPCMethodNotFound {
		return &agentbackend.UnsupportedError{Feature: feature}
	}
	return err
}
