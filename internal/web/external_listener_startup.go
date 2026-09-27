package web

// DecideExternalListenerStartup decides, purely from already-computed
// configuration facts, whether the external listener should be started at
// boot and — when it should NOT — why not.
//
// This is the shared decision the two startup call sites (cmd/mitto-app/main.go
// and internal/cmd/web.go) need in order to distinguish three cases:
//   - external access intentionally disabled (externalPort < 0): no reason,
//     nothing to log.
//   - external access intended and auth effectively enabled: start.
//   - external access intended but auth is NOT effectively enabled: credErr
//     (when non-nil) is surfaced verbatim as reason, so callers can log an
//     ERROR and notify the operator instead of skipping silently (mitto-688m,
//     "External listener skipped silently when auth is incomplete").
//
// authEnabled must be the effective value of Server.IsAuthenticationEnabled().
// credErr, when non-nil, further distinguishes "auth was intended but is
// broken" (e.g. incomplete simple-auth credentials) from "auth was simply
// never configured".
func DecideExternalListenerStartup(externalPort int, authEnabled bool, credErr error) (start bool, reason string) {
	if externalPort < 0 {
		// Intentionally disabled — not a failure, nothing to report.
		return false, ""
	}
	if authEnabled {
		return true, ""
	}
	if credErr != nil {
		return false, credErr.Error()
	}
	return false, ""
}
