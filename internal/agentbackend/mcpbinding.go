package agentbackend

import (
	"context"
	"errors"
	"fmt"
)

// FeatureMCPBinding is the queryable capability naming whether a backend can
// preserve secure per-conversation MCP tool attribution (see
// docs/devel/agent-backend-architecture.md, mitto-apvg). Query it through
// the existing three-state Capabilities.Query rather than a boolean: a host
// that only offers an MCP catalog/side-channel (no per-conversation
// binding) must report CapabilityUnsupported, never CapabilitySupported —
// scoped tools MUST be BLOCKED, not silently broadened to every
// conversation, whenever this capability is anything other than
// CapabilitySupported.
const FeatureMCPBinding Feature = "mcp_binding"

// ErrCrossSessionMCPBinding is returned when a caller attempts to rebind an
// already-bound MCP transport/session to a different SessionRef. A binding
// is immutable for the lifetime of the protocol session it was created for;
// a rebind attempt must be rejected rather than silently repointed (mirrors
// mitto-apvg: "Conflicting rebinds return 409").
var ErrCrossSessionMCPBinding = errors.New("agentbackend: MCP binding is immutable; cross-session rebind rejected")

// MCPBindingHandle is an opaque, reference-only handle identifying one
// backend's live per-conversation MCP binding. It never carries the
// underlying transport secret (the token/header value) — only the bound
// SessionRef is ever observable, so logging or persisting a handle cannot
// leak the binding value (mitto-apvg: "Never log or persist the binding
// value"). Two handles compare equal (via ==) only when they were built for
// the same session with the same internal id.
type MCPBindingHandle struct {
	ref SessionRef
	id  string
}

// NewMCPBindingHandle constructs an opaque handle for ref. id is an
// implementation-internal identifier (e.g. derived from the backend's own
// binding-token generation) and is never exposed via String().
func NewMCPBindingHandle(ref SessionRef, id string) MCPBindingHandle {
	return MCPBindingHandle{ref: ref, id: id}
}

// Session returns the SessionRef this handle is bound to.
func (h MCPBindingHandle) Session() SessionRef { return h.ref }

// IsZero reports whether h is the zero value (no binding).
func (h MCPBindingHandle) IsZero() bool { return h == MCPBindingHandle{} }

// String deliberately omits the internal id: only the bound conversation is
// ever printable, so logging a handle cannot leak the binding secret.
func (h MCPBindingHandle) String() string {
	return fmt.Sprintf("MCPBindingHandle{session=%s}", h.ref.ConversationID)
}

// MCPBinder is an optional per-session contract for backends that support
// FeatureMCPBinding: it issues an immutable binding handle for a session and
// enforces ownership-aware teardown. Provider/host identity must NEVER be
// usable as conversation identity, and FIFO self_id inference must NEVER be
// reintroduced — both invariants are enforced by keying strictly off the
// caller-supplied SessionRef, never off any transport-level identity alone.
type MCPBinder interface {
	// BindMCP returns the binding handle for ref, creating one on first
	// call. Calling it again for the SAME ref is idempotent and returns the
	// existing handle unchanged. Calling it for a transport/session already
	// bound to a DIFFERENT ref must return ErrCrossSessionMCPBinding rather
	// than repointing the binding.
	BindMCP(ctx context.Context, ref SessionRef) (MCPBindingHandle, error)
	// UnbindMCP releases ref's binding. Ownership-aware: releasing a
	// binding still attributed to a different SessionRef must return
	// ErrCrossSessionMCPBinding rather than tearing down another session's
	// binding.
	UnbindMCP(ctx context.Context, ref SessionRef) error
}

// AddressClass classifies the network reachability of an MCP endpoint
// binding. The zero value is AddressLoopback: a caller must explicitly opt
// in to AddressRemote rather than accidentally exposing an endpoint that
// defaults to remote-reachable.
type AddressClass int

const (
	// AddressLoopback restricts the endpoint to the local host only.
	AddressLoopback AddressClass = iota
	// AddressRemote allows non-loopback reachability; see
	// EndpointBinding.Validate for the additional requirements this
	// carries (TLS + authentication).
	AddressRemote
)

// String returns a lowercase, stable string form for logging/debugging.
func (c AddressClass) String() string {
	if c == AddressRemote {
		return "remote"
	}
	return "loopback"
}

// AuthScheme names how a caller must authenticate to an MCP endpoint
// binding. AuthNone (the zero value) is only valid for a loopback endpoint —
// see EndpointBinding.Validate.
type AuthScheme string

// AuthNone means no authentication is required (loopback-only endpoints).
const AuthNone AuthScheme = ""

// AuthBearer means a bearer-token credential (resolved via CredentialRef) is
// required.
const AuthBearer AuthScheme = "bearer"

// EndpointBinding describes a neutral MCP endpoint binding's reachability,
// TLS, and auth requirements. It never carries a secret value: CredentialRef
// is an opaque reference into the credential store (mirrors
// agentbackend.BackendConnection.CredentialRef / internal/secrets.CredentialRef
// by reference only — see docs/devel/agent-backend-architecture.md) and is
// resolved by the caller, never by this package. The zero value is the
// safe/closed default: loopback-only, no TLS requirement (loopback doesn't
// need one), no auth, and — critically — ToolsRelayed is false, so tools are
// NOT relayed unless a caller explicitly opts in.
type EndpointBinding struct {
	Address       AddressClass
	TLSRequired   bool
	Auth          AuthScheme
	CredentialRef string
	// ToolsRelayed must be explicitly set true by a caller that has
	// confirmed the workspace/backend/session scoping (and any required
	// cross-workspace confirmation); the zero value (false) means tools are
	// NOT relayed, matching the closed-by-default invariant.
	ToolsRelayed bool
}

// Validate enforces the reachability/auth invariants: a remote address
// requires TLS and a non-empty auth scheme backed by a credential
// reference — silent, unauthenticated remote access is never permitted.
func (b EndpointBinding) Validate() error {
	if b.Address != AddressRemote {
		return nil
	}
	if !b.TLSRequired {
		return fmt.Errorf("agentbackend: remote EndpointBinding must require TLS")
	}
	if b.Auth == AuthNone {
		return fmt.Errorf("agentbackend: remote EndpointBinding must set an auth scheme")
	}
	if b.CredentialRef == "" {
		return fmt.Errorf("agentbackend: remote EndpointBinding with auth %q must set CredentialRef", b.Auth)
	}
	return nil
}

// String never includes a secret value: CredentialRef only ever holds an
// opaque reference (see the field's own doc comment), never the credential
// itself, so it is safe to include verbatim.
func (b EndpointBinding) String() string {
	return fmt.Sprintf("EndpointBinding{address=%s tls=%v auth=%s toolsRelayed=%v}", b.Address, b.TLSRequired, b.Auth, b.ToolsRelayed)
}
