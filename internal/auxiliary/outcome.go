package auxiliary

// outcome.go classifies the result of an auxiliary dispatch attempt so
// callers can distinguish a transient, retryable condition (the provider is
// temporarily busy/saturated/gated) from a permanent one (the provider does
// not support this kind of auxiliary work at all), instead of treating every
// non-nil error identically. This mirrors
// conversation.ClassifyAcquireError's error-to-LifecycleState taxonomy
// (mitto-lrt.7) but is scoped to the narrower auxiliary ProcessProvider seam
// (mitto-lrt.10).

import (
	"errors"

	"github.com/inercia/mitto/internal/acpproc/acperrors"
	"github.com/inercia/mitto/internal/agentbackend"
)

// OutcomeKind classifies the result of a ProcessProvider.PromptAuxiliary /
// PromptAuxiliaryAsync call.
type OutcomeKind int

const (
	// OutcomeSuccess means the dispatch completed normally (nil error).
	OutcomeSuccess OutcomeKind = iota
	// OutcomeRetryable means the underlying provider reported a temporary
	// busy/saturated/gated condition (e.g. shared-ACP-process saturation,
	// proactive concurrent-RPC load-shedding, or MCP-init admission
	// gating — all of which wrap acperrors.ErrSharedProcessSaturated).
	// Callers should back off and retry later rather than surface this as
	// a hard failure.
	OutcomeRetryable
	// OutcomeUnsupported means the provider does not support this kind of
	// auxiliary work at all (agentbackend.ErrUnsupported, or an
	// *agentbackend.UnsupportedError). Retrying will not help; this is a
	// permanent fact about the backend, not a transient condition.
	OutcomeUnsupported
	// OutcomeError is any other, unclassified error.
	OutcomeError
)

// String returns a lowercase, stable string form for logging/debugging.
func (k OutcomeKind) String() string {
	switch k {
	case OutcomeSuccess:
		return "success"
	case OutcomeRetryable:
		return "retryable"
	case OutcomeUnsupported:
		return "unsupported"
	default:
		return "error"
	}
}

// ClassifyOutcome inspects err (as returned by ProcessProvider.PromptAuxiliary
// or PromptAuxiliaryAsync) and reports which OutcomeKind it represents. err
// is never swallowed or altered by this function — the returned OutcomeKind
// is purely advisory routing information; callers that need the original
// error for logging/wrapping keep using err directly.
//
// nil classifies as OutcomeSuccess. Unrecognized errors classify as
// OutcomeError rather than guessing a more specific — and possibly
// misleading — classification.
func ClassifyOutcome(err error) OutcomeKind {
	if err == nil {
		return OutcomeSuccess
	}

	if errors.Is(err, agentbackend.ErrUnsupported) {
		return OutcomeUnsupported
	}
	var unsupported *agentbackend.UnsupportedError
	if errors.As(err, &unsupported) {
		return OutcomeUnsupported
	}

	if errors.Is(err, acperrors.ErrSharedProcessSaturated) {
		return OutcomeRetryable
	}

	return OutcomeError
}

// CapabilityProvider is an OPTIONAL ProcessProvider capability (type-asserted
// like ProcessQuiescenceProvider) that lets a backend declare, ahead of
// dispatch, whether it supports a given auxiliary purpose (e.g. "title-gen",
// "follow-up", or a PurposeProcessorPrefix-prefixed processor name). ACP
// (which supports every auxiliary purpose today) does not implement this;
// callers that skip the type assertion simply attempt the dispatch and
// classify the resulting error via ClassifyOutcome instead of probing ahead
// of time. A future non-ACP backend that only supports a subset of
// auxiliary purposes can implement this to let callers short-circuit
// without an RPC round-trip.
type CapabilityProvider interface {
	// SupportsAuxiliaryPurpose reports whether the provider can service the
	// given auxiliary purpose at all. False does not distinguish "not right
	// now" from "never" — callers that need that distinction still dispatch
	// and classify the resulting error via ClassifyOutcome.
	SupportsAuxiliaryPurpose(purpose string) bool
}
