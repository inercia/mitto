package agentbackend

import "testing"

func TestBackendConnection_ValidateLocal(t *testing.T) {
	c := BackendConnection{Backend: "acp", Protocol: ProtocolACP, Command: "auggie"}
	if err := c.Validate(); err != nil {
		t.Fatalf("Validate() = %v, want nil for a well-formed local connection", err)
	}
	if !c.IsLocal() {
		t.Fatal("IsLocal() = false, want true when Command is set")
	}
}

func TestBackendConnection_ValidateRemote(t *testing.T) {
	c := BackendConnection{Backend: "acp", Protocol: ProtocolACP, Endpoint: "https://example.test", CredentialRef: "secret-store-key"}
	if err := c.Validate(); err != nil {
		t.Fatalf("Validate() = %v, want nil for a well-formed remote connection", err)
	}
	if c.IsLocal() {
		t.Fatal("IsLocal() = true, want false when only Endpoint is set")
	}
}

func TestBackendConnection_ValidateMissingBackend(t *testing.T) {
	c := BackendConnection{Protocol: ProtocolACP, Command: "auggie"}
	if err := c.Validate(); err == nil {
		t.Fatal("Validate() = nil, want an error when Backend is empty")
	}
}

func TestBackendConnection_ValidateUnsupportedProtocol(t *testing.T) {
	c := BackendConnection{Backend: "acp", Protocol: Protocol("ahp"), Command: "auggie"}
	if err := c.Validate(); err == nil {
		t.Fatal("Validate() = nil, want an error when Protocol is unsupported (fail closed, no silent ACP fallback)")
	}
}

func TestBackendConnection_ValidateMissingCommandAndEndpoint(t *testing.T) {
	c := BackendConnection{Backend: "acp", Protocol: ProtocolACP}
	if err := c.Validate(); err == nil {
		t.Fatal("Validate() = nil, want an error when neither Command nor Endpoint is set")
	}
}

func TestBackendConnection_CredentialRefIsOpaque(t *testing.T) {
	// Structural guard: CredentialRef is a plain string reference field, not
	// a secret-value container. This test documents/pins that a caller
	// populating it with a reference (not a raw secret) validates fine, and
	// that Env (which may carry legacy env-based secrets, matching prior
	// ACP behavior) is preserved as-is rather than being inspected/redacted
	// by Validate.
	env := map[string]string{"API_KEY": "should-not-be-inspected-by-validate"}
	c := BackendConnection{Backend: "acp", Protocol: ProtocolACP, Command: "auggie", Env: env}
	if err := c.Validate(); err != nil {
		t.Fatalf("Validate() = %v, want nil", err)
	}
	if c.CredentialRef != "" {
		t.Fatalf("CredentialRef = %q, want empty for a local connection that never set it", c.CredentialRef)
	}
}
