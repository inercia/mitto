package conversation

// backend_provider.go defines the protocol-neutral backend acquisition +
// ownership seam (mitto-lrt.7). It mirrors the existing ProcessManager
// dependency-inversion pattern (SetACPProcessManager, interfaces.go): a
// conversation-local interface that the ACP implementation satisfies by
// delegating to today's ProcessManager/SharedProcess (byte-identical
// behavior), and that a non-process fake can satisfy without any process,
// PID, or runner (see backend_provider_fake_test.go).
//
// BackendLease models OWNERSHIP of an acquired backend session, not the
// per-prompt data path: the existing ACP prompt/streaming pipeline keeps
// flowing through SharedProcess/SessionHandle unchanged via the LocalProcess/
// SessionHandle escape hatches below. Routing that data path itself through
// agentbackend's neutral Events is explicitly deferred (blocked on the event
// projection work in mitto-lrt.8; see docs/devel/agent-backend-architecture.md).

import (
	"context"

	"github.com/inercia/mitto/internal/agentbackend"
	"github.com/inercia/mitto/internal/config"
	"github.com/inercia/mitto/internal/runner"
)

// Intent describes why a caller is acquiring a backend session.
type Intent int

const (
	// IntentNew requests a brand-new session.
	IntentNew Intent = iota
	// IntentLoad requests loading (replaying) an existing session's history.
	IntentLoad
	// IntentResume requests resuming a previously archived/disconnected session.
	IntentResume
)

// AcquireRequest describes what BackendProvider.AcquireSession should
// acquire. Agent/Session carry protocol-neutral identity; the ACP-local
// fields below are used only by the ACP-backed provider and are ignored by
// non-ACP backends (e.g. the fake).
type AcquireRequest struct {
	// Agent identifies the backend+provider pair to acquire against.
	Agent agentbackend.AgentRef
	// Session identifies the conversation/provider-session pair being
	// acquired. ProviderSession is empty for IntentNew.
	Session agentbackend.SessionRef
	// Intent says whether this is a new/load/resume acquisition.
	Intent Intent

	// --- ACP-local parameters (ignored by non-ACP backends) ---

	// Workspace is used to resolve/create the shared ACP process.
	Workspace *config.WorkspaceSettings
	// ACPCommand, ACPCwd, ACPEnv are the resolved ACP connection parameters.
	ACPCommand string
	ACPCwd     string
	ACPEnv     map[string]string
	// Runner restricts the ACP process, if configured.
	Runner *runner.Runner
	// CWD is the working directory passed to NewSession/LoadSession/ResumeSession.
	CWD string
	// MCPServers is the list of MCP servers to advertise to the session.
	MCPServers []agentbackend.MCPServerDescriptor
	// Prewarm requests the underlying process manager pre-warm the process.
	Prewarm bool

	// DeferSession requests process-only acquisition: AcquireSession gets/
	// creates the backend process but skips the NewSession/LoadSession/
	// ResumeSession RPC, returning a lease whose SessionHandle is nil. This
	// preserves the mitto-220 deferred-session pattern (the session RPC is
	// deferred to the first prompt, with its own resume→load→new fallback
	// hardening) for callers that need eager, cheap process acquisition
	// through this seam without firing a blocking session RPC yet. Ignored
	// by non-ACP backends, which have no equivalent split.
	DeferSession bool
}

// SessionPromptOps is the neutral hot-path operation set — Prompt, Cancel,
// SetModel, SetMode — that a BackendLease exposes for its own
// already-established session (mitto-mx9.1). It is deliberately narrower
// than agentbackend.SessionOps, which also owns session CREATION
// (NewSession/LoadSession/ResumeSession): that responsibility stays with
// BackendProvider.AcquireSession/BackendLease.Bind, unchanged by this seam.
// Every method signature here matches its agentbackend.SessionOps
// counterpart exactly, so a value already typed as that wider interface
// (e.g. the fake backend's in-memory host) satisfies this one with no
// adapter needed — only the ACP lease needs a real translator, since ACP's
// SharedProcess is not itself an agentbackend.SessionOps.
type SessionPromptOps interface {
	// Prompt sends prompt content to this lease's session and blocks until
	// the turn completes, ctx is cancelled, or an error occurs.
	Prompt(ctx context.Context, ref agentbackend.SessionRef, content []agentbackend.ContentBlock) (agentbackend.PromptOutcome, error)
	// Cancel requests cancellation of the in-flight prompt turn, if any.
	Cancel(ctx context.Context, ref agentbackend.SessionRef) error
	// SetModel switches the session's active model.
	SetModel(ctx context.Context, ref agentbackend.SessionRef, modelID string) error
	// SetMode switches the session's active mode.
	SetMode(ctx context.Context, ref agentbackend.SessionRef, modeID string) error
}

// ProviderDiscoverer is the neutral provider-discovery seam a BackendLease
// exposes (mitto-mx9.5). It is structurally identical to
// agentbackend.ProviderDiscovery.Providers — any value that already
// satisfies that contract (e.g. the fake host used by the non-ACP backend)
// satisfies this one too, with no adapter needed — but is declared as its
// own interface here rather than imported, because internal/acpbackend
// (which implements agentbackend.ProviderDiscovery for the ACP path) already
// imports internal/conversation for conversation.SharedProcess, so this
// package cannot import internal/acpbackend back without a cycle.
type ProviderDiscoverer interface {
	// Providers reports the provider(s) reachable through this lease's
	// backend connection. Implementations must only read already-known
	// connection state — never invoke a local install/status/MCP script.
	Providers(ctx context.Context) ([]agentbackend.ProviderID, error)
}

// BackendLease models ownership of one acquired backend session.
type BackendLease interface {
	// Ref returns the neutral session identity for this lease.
	Ref() agentbackend.SessionRef
	// State reports the lease's current neutral lifecycle state.
	State() agentbackend.LifecycleState
	// Capabilities reports the lease's current capability state. Must
	// return agentbackend.CapabilityUnknown rather than guessing when a
	// feature's support has not been advertised one way or the other.
	Capabilities() agentbackend.Capabilities
	// Detach releases this lease's ownership share. For ACP this
	// unregisters the session from the shared process's multiplex layer;
	// it never kills a shared OS process or a remote host session that
	// other leases/clients may still own ("detach, not kill").
	Detach()
	// Bind attaches this lease to the concrete provider session identity
	// established by a deferred handshake (AcquireRequest.DeferSession): a
	// DeferSession lease is returned before any session/new|load|resume RPC
	// runs, so it starts with no session identity. A caller that completes
	// its own deferred handshake afterwards calls Bind with the resulting
	// agentbackend.SessionRef so later Detach/Reconnect calls target the
	// real session instead of treating the lease as unbound. Implementations
	// for leases that already carry a session identity from AcquireSession
	// itself (i.e. every non-deferred acquisition) may treat Bind as a no-op.
	Bind(ref agentbackend.SessionRef)
	// Reconnect attempts to re-establish a lost connection for this lease.
	// Implementations must coalesce concurrent callers (single-flight)
	// rather than duplicating work or replaying a possibly already-accepted
	// prompt.
	Reconnect(ctx context.Context) error
	// Terminate requests the backend actively stop this session.
	// Capability-gated and local-only: a backend that does not support
	// this MUST return an *agentbackend.UnsupportedError rather than
	// faking success or tearing down a host it does not own.
	Terminate(ctx context.Context) error

	// LocalProcess is an ACP-only escape hatch exposing the underlying
	// SharedProcess so the existing ACP prompt/streaming pipeline keeps
	// flowing through it unchanged. Non-ACP leases return (nil, false).
	LocalProcess() (SharedProcess, bool)
	// SessionHandle is an ACP-only escape hatch exposing the raw session
	// handle returned by NewSession/LoadSession/ResumeSession. Non-ACP
	// leases return (nil, false).
	SessionHandle() (*SessionHandle, bool)

	// SessionOps returns the neutral hot-path operation set (SessionPromptOps)
	// for this lease's session, once one has been established — immediately
	// after a non-deferred AcquireSession, or after a later Bind for a
	// DeferSession lease (see AcquireRequest.DeferSession doc). Returns
	// (nil, agentbackend.SessionRef{}, false) before that point; callers
	// MUST fall back to their pre-existing LocalProcess()-based path when ok
	// is false (mitto-mx9.1 additive routing: this accessor supplements
	// LocalProcess/SessionHandle rather than replacing them yet).
	SessionOps() (SessionPromptOps, agentbackend.SessionRef, bool)

	// ProviderDiscoverer returns the neutral provider-discovery seam
	// (mitto-mx9.5) for this lease's backend connection, when the backend
	// exposes one. Returns (nil, false) when it does not — a nil-safe,
	// common state mirroring SessionOps' own (nil, ref, false) pattern;
	// callers must treat that as "no discovery available", not an error.
	ProviderDiscoverer() (ProviderDiscoverer, bool)
}

// BackendProvider acquires a BackendLease for a conversation. It is the
// dependency-inversion seam that lets SessionManager acquire connections and
// session handles without depending on any specific backend implementation,
// mirroring the existing ProcessManager injection (SetACPProcessManager). A
// nil BackendProvider is a valid, common state — callers must fall back to
// their pre-existing direct acquisition path when none is injected.
type BackendProvider interface {
	AcquireSession(ctx context.Context, req AcquireRequest) (BackendLease, error)
}
