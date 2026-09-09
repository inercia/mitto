package agentbackend

import "fmt"

// Protocol identifies the wire protocol used to communicate with a backend
// host (e.g. "acp"). It is distinct from BackendID: BackendID names a
// specific configured backend instance, while Protocol names the transport
// it speaks. A protocol value must never imply resource ownership on its
// own (see docs/devel/agent-backend-architecture.md, mitto-lrt.5).
type Protocol string

const (
	// ProtocolACP is the existing Agent Client Protocol transport
	// (internal/acp, internal/acpproc). It is the only protocol any
	// shipping code path uses today.
	ProtocolACP Protocol = "acp"
)

// UnsupportedProtocolError reports that a Protocol value is not recognized
// or not yet implemented. Callers must fail closed on this error rather
// than silently falling back to ProtocolACP.
type UnsupportedProtocolError struct {
	Protocol Protocol
}

func (e *UnsupportedProtocolError) Error() string {
	return fmt.Sprintf("agentbackend: protocol %q not supported", string(e.Protocol))
}

func (e *UnsupportedProtocolError) Unwrap() error { return ErrUnsupported }

// ValidateProtocol returns a non-nil *UnsupportedProtocolError if p is not a
// known, supported protocol. An empty Protocol is also rejected: callers
// must set it explicitly rather than relying on an implicit default.
func ValidateProtocol(p Protocol) error {
	switch p {
	case ProtocolACP:
		return nil
	default:
		return &UnsupportedProtocolError{Protocol: p}
	}
}
