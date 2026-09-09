// Package agentbackend defines protocol-neutral contracts for driving an AI
// coding agent backend (see docs/devel/agent-backend-architecture.md). It
// must never import a protocol-specific SDK (e.g. github.com/coder/acp-go-sdk),
// internal/acp, internal/acpproc, internal/web, internal/conversation, or
// os/exec — see imports_test.go for the enforcing guard.
//
// This package is purely additive: it does not replace or wire into the
// existing ACP path (conversation.SharedProcess / SessionHandle /
// SessionCallbacks, acpproc.SharedACPProcess). Bridging an ACP adapter to
// these contracts is deferred to later work (mitto-lrt.6/.7/.8); here the
// contracts are proven only via the in-memory fake in fake.go.
package agentbackend

// BackendID identifies a backend implementation kind (e.g. "acp", "ahp",
// "fake"). Purely descriptive; not used for dispatch within this package.
type BackendID string

// ProviderID identifies a single agent/provider advertised by a connected
// backend host. A single host (one Connection) may advertise more than one
// provider.
type ProviderID string

// ProviderSessionID is the upstream-assigned session identifier for a
// conversation, as understood by the provider (e.g. ACP's SessionId). It is
// kept distinct from any Mitto-owned conversation id so the two identifier
// spaces never collapse into one (see ADR agent-backend-architecture.md §4).
type ProviderSessionID string

// SessionRef pairs a Mitto-owned conversation id with the upstream provider
// and its upstream-assigned session id, without collapsing the two
// identifier spaces into one.
type SessionRef struct {
	// ConversationID is the Mitto-owned conversation identifier.
	ConversationID string
	// Provider identifies which provider on the backend host owns the session.
	Provider ProviderID
	// ProviderSession is the upstream-assigned session id for this conversation.
	ProviderSession ProviderSessionID
}
