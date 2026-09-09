package agentbackend

import (
	"errors"
	"testing"
)

func TestValidateProtocol_ACPSupported(t *testing.T) {
	if err := ValidateProtocol(ProtocolACP); err != nil {
		t.Fatalf("ValidateProtocol(ProtocolACP) = %v, want nil", err)
	}
}

func TestValidateProtocol_UnknownFailsClosed(t *testing.T) {
	err := ValidateProtocol(Protocol("ahp"))
	if err == nil {
		t.Fatal("ValidateProtocol(\"ahp\") = nil, want an error (fail closed on unknown protocol)")
	}
	if !errors.Is(err, ErrUnsupported) {
		t.Fatalf("errors.Is(err, ErrUnsupported) = false for err = %v", err)
	}
	var upErr *UnsupportedProtocolError
	if !errors.As(err, &upErr) {
		t.Fatalf("errors.As(err, *UnsupportedProtocolError) = false for err = %v", err)
	}
	if upErr.Protocol != "ahp" {
		t.Fatalf("UnsupportedProtocolError.Protocol = %q, want %q", upErr.Protocol, "ahp")
	}
}

func TestValidateProtocol_EmptyFailsClosed(t *testing.T) {
	// An empty Protocol must never be silently treated as ProtocolACP.
	err := ValidateProtocol(Protocol(""))
	if err == nil {
		t.Fatal("ValidateProtocol(\"\") = nil, want an error (must not default to ProtocolACP)")
	}
	if !errors.Is(err, ErrUnsupported) {
		t.Fatalf("errors.Is(err, ErrUnsupported) = false for err = %v", err)
	}
}
