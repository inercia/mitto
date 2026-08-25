package config

import (
	"errors"
	"net/url"
)

// ErrWebAuthnRPUnavailable indicates that a WebAuthn Relying Party (RP) ID
// and/or origin could not be derived because no https external address (or
// explicit override) is configured. WebAuthn requires a secure context, and
// passkeys must never be armed for ephemeral, randomly-addressed deployments
// (e.g. quick tunnels) where the RP would be unstable across restarts.
var ErrWebAuthnRPUnavailable = errors.New("webauthn: no https external_address configured to derive relying party ID/origin")

// DeriveWebAuthnRP derives the WebAuthn Relying Party ID and origin from
// externalAddress (the web.hooks.external_address URL), the source the
// health monitor already parses (see internal/hooks/monitor.go).
//
// rpIDOverride and rpOriginOverride, when non-empty, win over the derived
// values for their respective piece. If both overrides are provided,
// externalAddress is not consulted at all. If either is empty, deriving the
// missing piece requires externalAddress to be a valid https URL with a
// non-empty host; any other scheme (including empty) is rejected.
func DeriveWebAuthnRP(externalAddress, rpIDOverride, rpOriginOverride string) (rpID, rpOrigin string, err error) {
	var derivedID, derivedOrigin string

	if rpIDOverride == "" || rpOriginOverride == "" {
		if externalAddress == "" {
			return "", "", ErrWebAuthnRPUnavailable
		}
		u, parseErr := url.Parse(externalAddress)
		if parseErr != nil || u.Scheme != "https" || u.Hostname() == "" {
			return "", "", ErrWebAuthnRPUnavailable
		}
		derivedID = u.Hostname()
		derivedOrigin = u.Scheme + "://" + u.Host
	}

	rpID = rpIDOverride
	if rpID == "" {
		rpID = derivedID
	}
	rpOrigin = rpOriginOverride
	if rpOrigin == "" {
		rpOrigin = derivedOrigin
	}
	return rpID, rpOrigin, nil
}
