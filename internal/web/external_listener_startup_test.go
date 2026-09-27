package web

import (
	"testing"

	"github.com/inercia/mitto/internal/web/middleware"
)

// TestDecideExternalListenerStartup_ReportsReasonWhenCredentialsIncomplete
// reproduces mitto-688m: "External listener skipped silently when auth is
// incomplete". External access was intended (externalPort >= 0) but auth is
// not effectively enabled because simple-auth credentials are incomplete
// (e.g. password missing after a Keychain load failure) — this is
// distinguishable from "auth never configured" via a non-nil credErr
// (mirroring AuthManager.CredentialError()). Callers (cmd/mitto-app/main.go,
// internal/cmd/web.go) need a non-empty reason here so they can log an ERROR
// and notify the operator instead of silently skipping the listener.
func TestDecideExternalListenerStartup_ReportsReasonWhenCredentialsIncomplete(t *testing.T) {
	start, reason := DecideExternalListenerStartup(0, false, middleware.ErrEmptyPassword)

	if start {
		t.Fatal("external listener must not start when auth is not effectively enabled")
	}
	if reason == "" {
		t.Error("mitto-688m: DecideExternalListenerStartup must surface WHY the external " +
			"listener was skipped when credErr indicates auth was intended but incomplete " +
			"(e.g. ErrEmptyPassword); got an empty reason, which is indistinguishable from " +
			"external access never being configured at all")
	}
}

// TestDecideExternalListenerStartup_NoReasonWhenIntentionallyDisabled pins
// the non-buggy branch: externalPort < 0 means the operator explicitly
// disabled external access, so no reason should ever be reported for it.
func TestDecideExternalListenerStartup_NoReasonWhenIntentionallyDisabled(t *testing.T) {
	start, reason := DecideExternalListenerStartup(-1, false, middleware.ErrEmptyPassword)
	if start {
		t.Fatal("external listener must not start when externalPort < 0")
	}
	if reason != "" {
		t.Errorf("expected no reason when external access is intentionally disabled, got %q", reason)
	}
}

// TestDecideExternalListenerStartup_StartsWhenAuthEnabled pins the
// happy-path branch: externalPort >= 0 and auth effectively enabled means
// the listener should start with no reason to report.
func TestDecideExternalListenerStartup_StartsWhenAuthEnabled(t *testing.T) {
	start, reason := DecideExternalListenerStartup(0, true, nil)
	if !start {
		t.Fatal("external listener must start when auth is effectively enabled and port >= 0")
	}
	if reason != "" {
		t.Errorf("expected no reason on the happy path, got %q", reason)
	}
}
