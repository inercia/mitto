package agentbackend

import "fmt"

// BackendConnection is a protocol-neutral, purely descriptive value type for
// how to reach a backend host. Constructing one does not connect to
// anything — see the Connection interface (contracts.go) for the live
// lifecycle.
//
// Command/Cwd/Env describe a locally-spawned host and mirror the shape of
// the legacy config.ACPServer for ProtocolACP. Endpoint/CredentialRef
// describe a remote host. CredentialRef MUST be an opaque reference to a
// credential (e.g. a secret-store key) and must never carry a raw secret
// value — remote connection support is internal/test-only for now (see
// docs/devel/agent-backend-architecture.md, mitto-lrt.5).
type BackendConnection struct {
	// Backend identifies which configured backend instance this connection
	// is for. Purely descriptive; see BackendID.
	Backend BackendID
	// Protocol is the wire protocol this connection speaks.
	Protocol Protocol

	// Command is the shell command used to start a locally-spawned host.
	Command string
	// Cwd is the working directory for a locally-spawned host. Empty means
	// the process inherits the current working directory.
	Cwd string
	// Env holds environment variables merged into a locally-spawned host's
	// process environment.
	Env map[string]string

	// Endpoint is the network address of a remote host. Empty for local
	// (Command-based) connections.
	Endpoint string
	// CredentialRef is an opaque reference to the credential needed to
	// reach Endpoint. Never populate this with a raw secret value.
	CredentialRef string
}

// Validate reports whether c is well-formed: it must name a Backend, use a
// supported Protocol, and describe either a local (Command) or remote
// (Endpoint) target — never neither.
func (c BackendConnection) Validate() error {
	if c.Backend == "" {
		return fmt.Errorf("agentbackend: BackendConnection.Backend must not be empty")
	}
	if err := ValidateProtocol(c.Protocol); err != nil {
		return err
	}
	if c.Command == "" && c.Endpoint == "" {
		return fmt.Errorf("agentbackend: BackendConnection %q must set either Command (local) or Endpoint (remote)", c.Backend)
	}
	return nil
}

// IsLocal reports whether this connection describes a locally-spawned host
// (Command is set).
func (c BackendConnection) IsLocal() bool {
	return c.Command != ""
}
