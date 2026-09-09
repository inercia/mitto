package agentbackend

import "testing"

func TestAgentRef_ValidateOK(t *testing.T) {
	r := AgentRef{Backend: "acp", Provider: "auggie"}
	if err := r.Validate(); err != nil {
		t.Fatalf("Validate() = %v, want nil", err)
	}
}

func TestAgentRef_ValidateMissingBackend(t *testing.T) {
	r := AgentRef{Provider: "auggie"}
	if err := r.Validate(); err == nil {
		t.Fatal("Validate() = nil, want an error when Backend is empty")
	}
}

func TestAgentRef_ValidateMissingProvider(t *testing.T) {
	r := AgentRef{Backend: "acp"}
	if err := r.Validate(); err == nil {
		t.Fatal("Validate() = nil, want an error when Provider is empty")
	}
}

func TestAgentRef_RenameDoesNotChangeIdentity(t *testing.T) {
	// Pins the design intent (agentref.go doc comment): an AgentRef's
	// identity is the (Backend, Provider) pair, never a display name. Two
	// refs naming the same backend+provider must compare equal even if a
	// caller only ever displayed a different name for the server.
	a := AgentRef{Backend: "acp", Provider: "auggie"}
	b := AgentRef{Backend: "acp", Provider: "auggie"}
	if a != b {
		t.Fatalf("AgentRef{%v} != AgentRef{%v}, want equal for identical Backend+Provider", a, b)
	}
}
