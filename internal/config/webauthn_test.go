package config

import (
	"errors"
	"testing"
)

func TestDeriveWebAuthnRP_ValidHTTPSAddress(t *testing.T) {
	rpID, rpOrigin, err := DeriveWebAuthnRP("https://mitto.example.com", "", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if rpID != "mitto.example.com" {
		t.Errorf("rpID = %q, want %q", rpID, "mitto.example.com")
	}
	if rpOrigin != "https://mitto.example.com" {
		t.Errorf("rpOrigin = %q, want %q", rpOrigin, "https://mitto.example.com")
	}
}

func TestDeriveWebAuthnRP_ValidHTTPSAddressWithPort(t *testing.T) {
	rpID, rpOrigin, err := DeriveWebAuthnRP("https://mitto.example.com:8443", "", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// RP ID must be the host only (no port) per the WebAuthn spec.
	if rpID != "mitto.example.com" {
		t.Errorf("rpID = %q, want %q", rpID, "mitto.example.com")
	}
	if rpOrigin != "https://mitto.example.com:8443" {
		t.Errorf("rpOrigin = %q, want %q", rpOrigin, "https://mitto.example.com:8443")
	}
}

func TestDeriveWebAuthnRP_EmptyAddressRejected(t *testing.T) {
	_, _, err := DeriveWebAuthnRP("", "", "")
	if !errors.Is(err, ErrWebAuthnRPUnavailable) {
		t.Fatalf("err = %v, want ErrWebAuthnRPUnavailable", err)
	}
}

func TestDeriveWebAuthnRP_NonHTTPSSchemeRejected(t *testing.T) {
	tests := []string{
		"http://mitto.example.com",
		"ftp://mitto.example.com",
		"mitto.example.com", // no scheme at all
	}
	for _, addr := range tests {
		t.Run(addr, func(t *testing.T) {
			_, _, err := DeriveWebAuthnRP(addr, "", "")
			if !errors.Is(err, ErrWebAuthnRPUnavailable) {
				t.Fatalf("err = %v, want ErrWebAuthnRPUnavailable for %q", err, addr)
			}
		})
	}
}

func TestDeriveWebAuthnRP_MalformedURLRejected(t *testing.T) {
	// A control character makes url.Parse fail outright.
	_, _, err := DeriveWebAuthnRP("https://mitto.example.com/\x7f", "", "")
	if !errors.Is(err, ErrWebAuthnRPUnavailable) {
		t.Fatalf("err = %v, want ErrWebAuthnRPUnavailable", err)
	}
}

func TestDeriveWebAuthnRP_EmptyHostRejected(t *testing.T) {
	_, _, err := DeriveWebAuthnRP("https://", "", "")
	if !errors.Is(err, ErrWebAuthnRPUnavailable) {
		t.Fatalf("err = %v, want ErrWebAuthnRPUnavailable", err)
	}
}

func TestDeriveWebAuthnRP_BothOverridesSkipExternalAddress(t *testing.T) {
	// externalAddress is invalid, but both overrides are set, so it must never
	// be consulted.
	rpID, rpOrigin, err := DeriveWebAuthnRP("not-a-url", "override.example.com", "https://override.example.com")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if rpID != "override.example.com" {
		t.Errorf("rpID = %q, want %q", rpID, "override.example.com")
	}
	if rpOrigin != "https://override.example.com" {
		t.Errorf("rpOrigin = %q, want %q", rpOrigin, "https://override.example.com")
	}
}

func TestDeriveWebAuthnRP_PartialOverrideRPIDOnly(t *testing.T) {
	// Only rpID overridden; rpOrigin must still be derived from externalAddress.
	rpID, rpOrigin, err := DeriveWebAuthnRP("https://mitto.example.com", "custom.example.com", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if rpID != "custom.example.com" {
		t.Errorf("rpID = %q, want %q", rpID, "custom.example.com")
	}
	if rpOrigin != "https://mitto.example.com" {
		t.Errorf("rpOrigin = %q, want %q", rpOrigin, "https://mitto.example.com")
	}
}

func TestDeriveWebAuthnRP_PartialOverrideRPOriginOnly(t *testing.T) {
	// Only rpOrigin overridden; rpID must still be derived from externalAddress.
	rpID, rpOrigin, err := DeriveWebAuthnRP("https://mitto.example.com", "", "https://custom-origin.example.com")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if rpID != "mitto.example.com" {
		t.Errorf("rpID = %q, want %q", rpID, "mitto.example.com")
	}
	if rpOrigin != "https://custom-origin.example.com" {
		t.Errorf("rpOrigin = %q, want %q", rpOrigin, "https://custom-origin.example.com")
	}
}

func TestDeriveWebAuthnRP_PartialOverrideWithInvalidAddressRejected(t *testing.T) {
	// Only one override is set, and externalAddress is invalid: the missing
	// piece cannot be derived, so this must fail even though one override
	// was provided.
	_, _, err := DeriveWebAuthnRP("http://mitto.example.com", "custom.example.com", "")
	if !errors.Is(err, ErrWebAuthnRPUnavailable) {
		t.Fatalf("err = %v, want ErrWebAuthnRPUnavailable", err)
	}
}
