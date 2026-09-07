package agentbackend

import "context"

// Connection manages the lifecycle of a single backend host connection.
// Deliberately excludes subprocess-specific concerns (ProcessDone, Restart,
// PID, generation counters, MCP initialization, GC) which remain specific to
// the ACP-over-subprocess transport and must not be forced onto every
// backend — e.g. a remote host has no local process to restart.
type Connection interface {
	// Connect establishes (or re-establishes) the connection to the backend
	// host. Implementations should be safe to call again after Close.
	Connect(ctx context.Context) error
	// Close tears down the connection. Safe to call multiple times.
	Close() error
	// State reports the current lifecycle state of the connection.
	State() LifecycleState
}

// ProviderDiscovery lists the agents/providers a connected backend host
// advertises. A single host may expose more than one provider.
type ProviderDiscovery interface {
	Providers(ctx context.Context) ([]ProviderID, error)
}

// Session represents one active conversation session on a backend.
type Session interface {
	// Ref returns the SessionRef identifying this session.
	Ref() SessionRef
	// Capabilities returns the session's current capability state.
	Capabilities() Capabilities
}

// SessionOps creates, resumes, and drives sessions on a backend.
//
// Concurrency: implementations must be safe for concurrent use by multiple
// goroutines across different SessionRef values; behavior for concurrent
// calls on the SAME SessionRef (e.g. two Prompt calls racing) is
// implementation-defined but must not corrupt shared state or panic.
type SessionOps interface {
	// NewSession starts a brand-new session for the given provider.
	NewSession(ctx context.Context, provider ProviderID) (Session, error)
	// LoadSession loads an existing session by ref (e.g. from persisted history).
	LoadSession(ctx context.Context, ref SessionRef) (Session, error)
	// ResumeSession resumes a session after a disconnect/reconnect.
	ResumeSession(ctx context.Context, ref SessionRef) (Session, error)

	// Prompt sends prompt content to a session and blocks until the turn
	// completes, ctx is cancelled, or an error occurs. Streamed updates
	// during the turn are delivered separately via EventDelivery.
	Prompt(ctx context.Context, ref SessionRef, content []ContentBlock) (PromptOutcome, error)
	// Cancel requests cancellation of the in-flight prompt turn for ref, if any.
	Cancel(ctx context.Context, ref SessionRef) error

	// SetModel switches the session's active model. Returns an
	// *UnsupportedError if the backend/session does not support model
	// selection.
	SetModel(ctx context.Context, ref SessionRef, modelID string) error
	// SetMode switches the session's active mode. Returns an
	// *UnsupportedError if the backend/session does not support mode
	// selection.
	SetMode(ctx context.Context, ref SessionRef, modeID string) error
}

// Subscription represents one active event subscription. Close must
// deterministically halt further delivery to the associated callback, with
// no leaked goroutines or channels.
type Subscription interface {
	Close() error
}

// EventDelivery delivers session and lifecycle events to subscribers.
type EventDelivery interface {
	// Subscribe registers fn to receive events for ref (or all sessions on
	// the host, when ref is the zero value). Delivery order for a single
	// subscription matches the order events occurred; concurrent delivery
	// across distinct subscriptions is not ordered relative to each other.
	// Implementations must not block the caller of Subscribe, and must stop
	// calling fn once the returned Subscription is closed.
	Subscribe(ctx context.Context, ref SessionRef, fn func(Event)) (Subscription, error)
}

// ClientServices is an optional set of client-side request handlers a
// backend may need from its host (file access, terminals, permission
// prompts). A backend that doesn't need one or more of these may return an
// *UnsupportedError for the corresponding method; implementing this
// interface at all is optional.
type ClientServices interface {
	ReadFile(ctx context.Context, ref SessionRef, path string) ([]byte, error)
	WriteFile(ctx context.Context, ref SessionRef, path string, data []byte) error
	RequestPermission(ctx context.Context, ref SessionRef, prompt string) (bool, error)
}
