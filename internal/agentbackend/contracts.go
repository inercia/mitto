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

// TerminalHandle identifies one terminal created via TerminalServices,
// scoped to the SessionRef it was created for. Opaque to callers outside the
// owning backend.
type TerminalHandle string

// TerminalExitStatus reports how a terminal command finished. At most one
// field is meaningful at a time: ExitCode is set when the process exited
// normally (a zero value is a valid exit code, hence the pointer), Signal is
// set when it was terminated by a signal instead — mirroring ACP's
// terminal/wait_for_exit response shape without depending on the ACP SDK.
type TerminalExitStatus struct {
	ExitCode *int
	Signal   *string
}

// TerminalServices is an optional client-side contract for running commands
// in a host-managed terminal on behalf of the agent (create/output/wait/
// kill/release), kept separate from ClientServices so a backend that cannot
// safely execute anything (e.g. a host-owned session — see ResourceOwner)
// can simply not implement it. A backend/session that doesn't implement this
// interface, or a caller invoking one of its methods without checking
// ResourceOwner first, must be treated identically to every method
// returning *UnsupportedError{Feature: FeatureTerminals}.
type TerminalServices interface {
	CreateTerminal(ctx context.Context, ref SessionRef, command string, args []string, cwd string, env map[string]string) (TerminalHandle, error)
	TerminalOutput(ctx context.Context, ref SessionRef, handle TerminalHandle) (output string, truncated bool, exit *TerminalExitStatus, err error)
	WaitForTerminalExit(ctx context.Context, ref SessionRef, handle TerminalHandle) (TerminalExitStatus, error)
	KillTerminal(ctx context.Context, ref SessionRef, handle TerminalHandle) error
	ReleaseTerminal(ctx context.Context, ref SessionRef, handle TerminalHandle) error
}

// ResourceOwnership classifies whether a client-service resource request
// (a file path, a terminal command) targets something owned by the LOCAL
// Mitto machine, or by the connected backend host itself. A host-owned
// session's "file path" or "command" is meaningful only in the host's own
// environment; executing it against the local filesystem/process table
// merely because it looks like an ordinary path/command would be a sandbox
// escape from the host's point of view. ACP sessions are always local-owned
// today (the agent runs as a local subprocess); a future remote backend
// (e.g. AHP) may report OwnershipHost instead.
type ResourceOwnership int

const (
	// OwnershipLocal means file/terminal requests for this session may be
	// served against the local Mitto machine (today's ACP behavior).
	OwnershipLocal ResourceOwnership = iota
	// OwnershipHost means file/terminal requests for this session describe
	// resources on the connected host, NOT the local Mitto machine; callers
	// MUST reject such requests with *UnsupportedError rather than executing
	// them locally.
	OwnershipHost
)

// String returns a lowercase, stable string form for logging/debugging.
func (o ResourceOwnership) String() string {
	if o == OwnershipHost {
		return "host"
	}
	return "local"
}

// ResourceOwner is an optional contract a backend/connection may implement
// to report ResourceOwnership for a session. A backend that does not
// implement this interface is assumed OwnershipLocal (matches every backend
// today). Callers that honor ClientServices/TerminalServices requests MUST
// consult Ownership first when it is available and refuse with
// *UnsupportedError for any request against an OwnershipHost session.
type ResourceOwner interface {
	Ownership(ref SessionRef) ResourceOwnership
}

// RejectIfHostOwned enforces the ResourceOwner invariant in one call: when
// owner is non-nil and reports OwnershipHost for ref, it returns
// *UnsupportedError{Feature: feature} so a ClientServices/TerminalServices
// caller can refuse a host-owned session's file/terminal request rather
// than risk executing it against the local Mitto machine. A nil owner (a
// backend that doesn't implement ResourceOwner) is treated as
// OwnershipLocal, matching ResourceOwner's own doc comment.
func RejectIfHostOwned(owner ResourceOwner, ref SessionRef, feature Feature) error {
	if owner == nil {
		return nil
	}
	if owner.Ownership(ref) == OwnershipHost {
		return &UnsupportedError{Feature: feature}
	}
	return nil
}
